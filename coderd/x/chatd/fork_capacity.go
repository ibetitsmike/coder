package chatd

// This file is fork code: it enforces the subagent capacity limits of the
// fork-nested-subagents experiment. Upstream's chatd.go wraps its capacity
// limiter with forkCapacityLimiter, which keeps upstream's behavior when the
// experiment is off.

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/forkdb"
)

// forkSubagentLimits are the effective subagent limits of an organization.
// A capacity limit of 0 means no limit.
type forkSubagentLimits struct {
	maxDepth               int
	maxConcurrent          int
	maxConcurrentPerFamily int
}

// subagentLimits returns the subagent limits of chats in the organization:
// the deployment values, lowered by the organization's where it sets them.
func (p *Server) subagentLimits(ctx context.Context, fdb forkdb.Store, organizationID uuid.UUID) (forkSubagentLimits, error) {
	deployment := p.chatLimits.Fork
	//nolint:gocritic // Chat creators cannot read organization config, so chatd reads it.
	org, err := fdb.GetChatOrganizationSubagentLimits(dbauthz.AsChatd(ctx), organizationID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return forkSubagentLimits{}, xerrors.Errorf("get subagent limits of organization %s: %w", organizationID, err)
	}
	limits := forkSubagentLimits{
		maxDepth:               deployment.MaxSubagentDepth,
		maxConcurrent:          effectiveCapacityLimit(deployment.MaxConcurrentSubagents, org.MaxConcurrent),
		maxConcurrentPerFamily: effectiveCapacityLimit(deployment.MaxConcurrentSubagentsPerFamily, org.MaxConcurrentPerFamily),
	}
	if org.MaxDepth.Valid {
		limits.maxDepth = min(limits.maxDepth, int(org.MaxDepth.Int32))
	}
	return limits, nil
}

// effectiveCapacityLimit lowers the deployment limit to the organization's.
// 0 means no limit.
func effectiveCapacityLimit(deployment int, org sql.NullInt32) int {
	if !org.Valid {
		return deployment
	}
	if deployment == 0 {
		return int(org.Int32)
	}
	return min(deployment, int(org.Int32))
}

// subagentRootChatID returns the root chat of the family that chat belongs
// to, chat itself for a root.
func subagentRootChatID(chat database.Chat) uuid.UUID {
	switch {
	case chat.RootChatID.Valid:
		return chat.RootChatID.UUID
	case chat.ParentChatID.Valid:
		return chat.ParentChatID.UUID
	default:
		return chat.ID
	}
}

// forkCapacityLimiter admits subagents under the deployment, organization
// and family concurrency limits and the upstream subagent pool, counting
// none of the candidate's ancestors. The deployment limit and the pool count
// every organization, and the organization's own limit counts only its chats.
// Root chats, and every chat with the experiment off, go to the inner
// limiter.
type forkCapacityLimiter struct {
	AgentCapacityLimiter
	server *Server
}

func (p *Server) forkCapacityLimiter(inner AgentCapacityLimiter) AgentCapacityLimiter {
	return &forkCapacityLimiter{AgentCapacityLimiter: inner, server: p}
}

func (l *forkCapacityLimiter) Admit(ctx context.Context, store database.Store, chat database.Chat) (bool, error) {
	if !chat.ParentChatID.Valid || !l.server.nestedSubagentsEnabled() {
		return l.AgentCapacityLimiter.Admit(ctx, store, chat)
	}
	//nolint:gocritic // Capacity accounting is chatd-internal state.
	ctx = dbauthz.AsChatd(ctx)
	// The count must run on the transaction store, under the admission lock.
	fdb, err := forkdb.From(store)
	if err != nil {
		return false, xerrors.Errorf("fork queries: %w", err)
	}
	limits, err := l.server.subagentLimits(ctx, fdb, chat.OrganizationID)
	if err != nil {
		return false, err
	}
	// Upstream's pool count includes the candidate's ancestors, so
	// coordinators blocked in wait_agent could fill the pool and starve
	// their own children forever. The pool cap is applied here instead,
	// with the ancestors excluded.
	maxConcurrent := l.server.chatLimits.Fork.MaxConcurrentSubagents
	if pool, capped := l.AgentCapacityLimiter.Limits(); capped && (maxConcurrent == 0 || int(pool.Subagent) < maxConcurrent) {
		maxConcurrent = int(pool.Subagent)
	}
	if maxConcurrent == 0 && limits.maxConcurrent == 0 && limits.maxConcurrentPerFamily == 0 {
		return true, nil
	}
	if err := store.SetTransactionLockTimeout(ctx, admissionLockTimeout.Milliseconds()); err != nil {
		return false, xerrors.Errorf("set lock timeout: %w", err)
	}
	if err := store.AcquireLock(ctx, database.LockIDChatCapacityAdmission); err != nil {
		return false, xerrors.Errorf("acquire capacity admission lock: %w", err)
	}
	counts, err := fdb.CountActiveSubagentsForCapacity(ctx, forkdb.CountActiveSubagentsForCapacityParams{
		ChatID:         chat.ID,
		OrganizationID: chat.OrganizationID,
		RootChatID:     subagentRootChatID(chat),
		StaleSeconds:   int32(l.server.inFlightChatStaleAfter.Seconds()),
	})
	if err != nil {
		return false, xerrors.Errorf("count active subagents: %w", err)
	}
	if overCapacityLimit(counts.ActiveCount, maxConcurrent) ||
		overCapacityLimit(counts.ActiveOrganizationCount, limits.maxConcurrent) ||
		overCapacityLimit(counts.ActiveFamilyCount, limits.maxConcurrentPerFamily) {
		// A plain refusal makes the worker skip every other subagent in its
		// batch as if the whole subagent pool were full, but each count
		// excludes the candidate's own ancestors, so another subagent may
		// still fit. errSkipAcquire skips only this chat.
		return false, errSkipAcquire
	}
	return true, nil
}

func overCapacityLimit(count int64, limit int) bool {
	return limit > 0 && count >= int64(limit)
}
