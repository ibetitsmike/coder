package chatd

// This file is fork code: it reports the subagent capacity of a chat family
// for the fork-nested-subagents sidebar.

import (
	"context"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/codersdk"
)

// ForkChatFamilyCapacity returns the live subagent count and limit of the
// family under root, and the running subagents that wait for a concurrency
// slot. Like ChatQueuedForCapacity it takes no lock, so the answer may be
// stale by the time it is read.
func (p *Server) ForkChatFamilyCapacity(ctx context.Context, root database.Chat) (codersdk.ForkChatFamilyCapacity, error) {
	// The concurrency counts span other users' chats, which the requester
	// cannot read directly.
	//nolint:gocritic // Capacity accounting is chatd-internal state.
	ctx = dbauthz.AsChatd(ctx)
	fdb, err := forkdb.From(p.db)
	if err != nil {
		return codersdk.ForkChatFamilyCapacity{}, xerrors.Errorf("fork queries: %w", err)
	}
	limits, err := p.subagentLimits(ctx, fdb, root.OrganizationID)
	if err != nil {
		return codersdk.ForkChatFamilyCapacity{}, err
	}
	live, err := fdb.CountLiveSubagentsInFamily(ctx, root.ID)
	if err != nil {
		return codersdk.ForkChatFamilyCapacity{}, xerrors.Errorf("count live subagents: %w", err)
	}
	family := codersdk.ForkChatFamilyCapacity{
		LiveSubagents:      live,
		MaxLiveSubagents:   int64(limits.maxLivePerFamily),
		WaitingForCapacity: []uuid.UUID{},
	}
	maxConcurrent := p.subagentPoolLimit(p.agentCapacityLimiter)
	if maxConcurrent == 0 && limits.maxConcurrent == 0 && limits.maxConcurrentPerFamily == 0 {
		return family, nil
	}
	staleSeconds := int32(p.inFlightChatStaleAfter.Seconds())
	awaiting, err := fdb.GetFamilySubagentsAwaitingWorker(ctx, forkdb.GetFamilySubagentsAwaitingWorkerParams{
		RootChatID:   root.ID,
		StaleSeconds: staleSeconds,
	})
	if err != nil {
		return codersdk.ForkChatFamilyCapacity{}, xerrors.Errorf("list subagents awaiting a worker: %w", err)
	}
	for _, chatID := range awaiting {
		counts, err := fdb.CountActiveSubagentsForCapacity(ctx, forkdb.CountActiveSubagentsForCapacityParams{
			ChatID:         chatID,
			OrganizationID: root.OrganizationID,
			RootChatID:     root.ID,
			StaleSeconds:   staleSeconds,
		})
		if err != nil {
			return codersdk.ForkChatFamilyCapacity{}, xerrors.Errorf("count active subagents: %w", err)
		}
		if subagentCapacityFull(counts, maxConcurrent, limits) {
			family.WaitingForCapacity = append(family.WaitingForCapacity, chatID)
		}
	}
	return family, nil
}
