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
	maxLivePerFamily       int
}

// subagentLimits returns the subagent limits of chats in the organization:
// the deployment values, lowered by the organization's where it sets them.
func (p *Server) subagentLimits(ctx context.Context, fdb forkdb.Store, organizationID uuid.UUID) (forkSubagentLimits, error) {
	//nolint:gocritic // Chat creators cannot read organization config, so chatd reads it.
	org, err := fdb.GetChatOrganizationSubagentLimits(dbauthz.AsChatd(ctx), organizationID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return forkSubagentLimits{}, xerrors.Errorf("get subagent limits of organization %s: %w", organizationID, err)
	}
	limits := EffectiveSubagentLimits(p.chatLimits.Fork, org)
	return forkSubagentLimits{
		maxDepth:               limits.MaxSubagentDepth,
		maxConcurrent:          limits.MaxConcurrentSubagents,
		maxConcurrentPerFamily: limits.MaxConcurrentSubagentsPerFamily,
		maxLivePerFamily:       limits.MaxLiveSubagentsPerFamily,
	}, nil
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
	maxConcurrent := l.server.subagentPoolLimit(l.AgentCapacityLimiter)
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
	if subagentCapacityFull(counts, maxConcurrent, limits) {
		// A plain refusal makes the worker skip every other subagent in its
		// batch as if the whole subagent pool were full, but each count
		// excludes the candidate's own ancestors, so another subagent may
		// still fit. errSkipAcquire skips only this chat.
		return false, errSkipAcquire
	}
	return true, nil
}

// subagentPoolLimit returns the deployment limit of running subagents: the
// fork limit, lowered to upstream's subagent pool when pool caps it.
// Upstream's pool count includes the candidate's ancestors, so coordinators
// blocked in wait_agent could fill the pool and starve their own children
// forever. The fork applies the pool cap instead, with the ancestors
// excluded.
func (p *Server) subagentPoolLimit(pool AgentCapacityLimiter) int {
	maxConcurrent := p.chatLimits.Fork.MaxConcurrentSubagents
	if limits, capped := pool.Limits(); capped && (maxConcurrent == 0 || int(limits.Subagent) < maxConcurrent) {
		maxConcurrent = int(limits.Subagent)
	}
	return maxConcurrent
}

// subagentCapacityFull reports whether the active subagents leave no slot
// under the deployment limit maxConcurrent or the organization or family
// concurrency limit.
func subagentCapacityFull(counts forkdb.CountActiveSubagentsForCapacityRow, maxConcurrent int, limits forkSubagentLimits) bool {
	return overCapacityLimit(counts.ActiveCount, maxConcurrent) ||
		overCapacityLimit(counts.ActiveOrganizationCount, limits.maxConcurrent) ||
		overCapacityLimit(counts.ActiveFamilyCount, limits.maxConcurrentPerFamily)
}

func overCapacityLimit(count int64, limit int) bool {
	return limit > 0 && count >= int64(limit)
}

// forkChatQueuedForCapacity reports whether chat is a running subagent that
// no worker owns while a concurrency limit is full. handled is true when the
// fork limiter admits chat, so the answer replaces upstream's. It takes no
// lock: the answer is for display and may be stale by the time it is read.
func (p *Server) forkChatQueuedForCapacity(ctx context.Context, chat database.Chat) (queued bool, handled bool, err error) {
	if !p.nestedSubagentsEnabled() || !chat.ParentChatID.Valid {
		return false, false, nil
	}
	if chat.Archived || chat.Status != database.ChatStatusRunning {
		return false, true, nil
	}
	//nolint:gocritic // Capacity accounting is chatd-internal state.
	ctx = dbauthz.AsChatd(ctx)
	fdb, err := forkdb.From(p.db)
	if err != nil {
		return false, true, xerrors.Errorf("fork queries: %w", err)
	}
	limits, err := p.subagentLimits(ctx, fdb, chat.OrganizationID)
	if err != nil {
		return false, true, err
	}
	maxConcurrent := p.subagentPoolLimit(p.agentCapacityLimiter)
	if maxConcurrent == 0 && limits.maxConcurrent == 0 && limits.maxConcurrentPerFamily == 0 {
		return false, true, nil
	}
	staleSeconds := int32(p.inFlightChatStaleAfter.Seconds())
	awaiting, err := fdb.GetChatAwaitingWorker(ctx, forkdb.GetChatAwaitingWorkerParams{
		ChatID:       chat.ID,
		StaleSeconds: staleSeconds,
	})
	if err != nil {
		return false, true, xerrors.Errorf("check chat owner: %w", err)
	}
	if !awaiting {
		return false, true, nil
	}
	counts, err := fdb.CountActiveSubagentsForCapacity(ctx, forkdb.CountActiveSubagentsForCapacityParams{
		ChatID:         chat.ID,
		OrganizationID: chat.OrganizationID,
		RootChatID:     subagentRootChatID(chat),
		StaleSeconds:   staleSeconds,
	})
	if err != nil {
		return false, true, xerrors.Errorf("count active subagents: %w", err)
	}
	return subagentCapacityFull(counts, maxConcurrent, limits), true, nil
}
