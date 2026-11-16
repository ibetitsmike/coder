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
	if err := l.acquireAdmissionLocks(ctx, store, fdb, chat, maxConcurrent, limits); err != nil {
		return false, err
	}
	rows, err := fdb.GetSubagentCapacityCounts(ctx, forkdb.GetSubagentCapacityCountsParams{
		CandidateIds: []uuid.UUID{chat.ID},
		StaleSeconds: int32(l.server.inFlightChatStaleAfter.Seconds()),
	})
	if err != nil {
		return false, xerrors.Errorf("count active subagents: %w", err)
	}
	if len(rows) != 1 {
		return false, xerrors.Errorf("count active subagents: got %d rows for chat %s, want 1", len(rows), chat.ID)
	}
	counts := rows[0]
	if subagentCapacityFull(counts, maxConcurrent, limits) {
		// A plain refusal makes the worker skip every other subagent in its
		// batch as if the whole subagent pool were full, but each count
		// excludes the candidate's own ancestors, so another subagent may
		// still fit. errSkipAcquire skips only this chat.
		return false, errSkipAcquire
	}
	return true, nil
}

// acquireAdmissionLocks takes the locks that serialize the capacity count of
// chat with every other admission that the same limits cover: the deployment
// limit maxConcurrent and the organization's limits. A deployment or
// organization limit counts chats of every family, so it takes the
// deployment admission lock exclusively. A family limit alone counts only the
// family of chat, so it takes the family capacity lock exclusively and the
// deployment lock in shared mode: admissions in different families run in
// parallel, and heartbeat renewal, which takes the deployment lock
// exclusively, still cannot extend a lease that the count read as stale. The
// family capacity lock is not the family admission lock that spawns and
// resumes hold: those never change the active count, and an Admit that waited
// for them while it held the shared deployment lock could deadlock with a
// resume and a lease takeover, or delay heartbeat renewal. A
// takeover of a stale lease always takes the deployment lock exclusively,
// because the lease recheck after Admit takes it exclusively too, and two
// transactions that upgrade a shared lock to exclusive deadlock.
func (*forkCapacityLimiter) acquireAdmissionLocks(ctx context.Context, store database.Store, fdb forkdb.Store, chat database.Chat, maxConcurrent int, limits forkSubagentLimits) error {
	if err := store.SetTransactionLockTimeout(ctx, admissionLockTimeout.Milliseconds()); err != nil {
		return xerrors.Errorf("set lock timeout: %w", err)
	}
	if maxConcurrent > 0 || limits.maxConcurrent > 0 || (chat.WorkerID.Valid && chat.RunnerID.Valid) {
		if err := store.AcquireLock(ctx, database.LockIDChatCapacityAdmission); err != nil {
			return xerrors.Errorf("acquire capacity admission lock: %w", err)
		}
		return nil
	}
	if err := fdb.AcquireSharedLock(ctx, database.LockIDChatCapacityAdmission); err != nil {
		return xerrors.Errorf("acquire shared capacity admission lock: %w", err)
	}
	if err := store.AcquireLock(ctx, chatFamilyCapacityLockID(subagentRootChatID(chat))); err != nil {
		return xerrors.Errorf("acquire family capacity lock: %w", err)
	}
	return nil
}

// chatFamilyCapacityLockID is the transaction lock that serializes the
// admissions of one chat family when only the family concurrency limit
// applies.
func chatFamilyCapacityLockID(rootChatID uuid.UUID) int64 {
	return database.GenLockID("fork_chat_family_capacity:" + rootChatID.String())
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
func subagentCapacityFull(counts forkdb.GetSubagentCapacityCountsRow, maxConcurrent int, limits forkSubagentLimits) bool {
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
	waiting, err := p.subagentsWaitingForCapacity(ctx, fdb, limits, []uuid.UUID{chat.ID})
	if err != nil {
		return false, true, err
	}
	return waiting[chat.ID], true, nil
}

// subagentConcurrencyLimited reports whether any concurrency limit applies
// to subagents of an organization with limits.
func (p *Server) subagentConcurrencyLimited(limits forkSubagentLimits) bool {
	return p.subagentPoolLimit(p.agentCapacityLimiter) > 0 || limits.maxConcurrent > 0 || limits.maxConcurrentPerFamily > 0
}

// subagentsWaitingForCapacity returns the chats among candidateIDs, subagent
// chats of the organization whose limits are given, that are running with no
// worker while a concurrency limit is full. It counts every candidate in one
// statement. Like forkChatQueuedForCapacity it takes no lock.
func (p *Server) subagentsWaitingForCapacity(ctx context.Context, fdb forkdb.Store, limits forkSubagentLimits, candidateIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	waiting := make(map[uuid.UUID]bool, len(candidateIDs))
	if len(candidateIDs) == 0 || !p.subagentConcurrencyLimited(limits) {
		return waiting, nil
	}
	rows, err := fdb.GetSubagentCapacityCounts(ctx, forkdb.GetSubagentCapacityCountsParams{
		CandidateIds: candidateIDs,
		AwaitingOnly: true,
		StaleSeconds: int32(p.inFlightChatStaleAfter.Seconds()),
	})
	if err != nil {
		return nil, xerrors.Errorf("count active subagents: %w", err)
	}
	maxConcurrent := p.subagentPoolLimit(p.agentCapacityLimiter)
	for _, row := range rows {
		if subagentCapacityFull(row, maxConcurrent, limits) {
			waiting[row.ChatID] = true
		}
	}
	return waiting, nil
}
