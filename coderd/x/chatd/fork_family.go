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
	if !p.subagentConcurrencyLimited(limits) {
		return family, nil
	}
	waiting, err := p.subagentsWaitingForCapacity(ctx, fdb, limits, nil, uuid.NullUUID{UUID: root.ID, Valid: true})
	if err != nil {
		return codersdk.ForkChatFamilyCapacity{}, xerrors.Errorf("list subagents waiting for capacity: %w", err)
	}
	family.WaitingForCapacity = waiting
	return family, nil
}
