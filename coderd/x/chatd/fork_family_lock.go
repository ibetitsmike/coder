package chatd

// This file is fork code: it extends a family archive behind fork
// experiments. Upstream's setChatFamilyArchived and the auto-archive worker
// call forkSetFamilyArchived, which runs chatstate.SetFamilyArchived
// unchanged when no fork experiment applies. Behind fork-nested-subagents it
// orders the row locks, and behind fork-chat-wakeups an archive cancels the
// wake-ups of the family.

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatstate"
	"github.com/coder/coder/v2/codersdk"
)

// forkSetFamilyArchived archives or unarchives a family in
// setFamilyArchivedInLockOrder. After an archive commits, it schedules the
// cleanup of the family's subagent worktrees.
func (p *Server) forkSetFamilyArchived(
	ctx context.Context,
	store database.Store,
	publisher chatstate.Publisher,
	input chatstate.SetFamilyArchivedInput,
) ([]database.Chat, error) {
	familyChats, err := p.setFamilyArchivedInLockOrder(ctx, store, publisher, input)
	if err == nil && input.Archived {
		p.scheduleArchivedSubagentCleanup(ctx, input.RootID, familyChats)
	}
	return familyChats, err
}

// setFamilyArchivedInLockOrder runs chatstate.SetFamilyArchived after it
// locks the family in an order that cannot deadlock with a member's own
// transition.
// An archive also cancels the active and paused wake-ups of every member in
// the same transaction, so none stays scheduled until the wake-up worker
// claims it. Unarchiving does not resume them.
//
// A transition updates its chat row twice in one transaction, and the second
// update rechecks the foreign keys, which takes FOR KEY SHARE on the parent
// and the root. chatstate.SetFamilyArchived locks the root FOR UPDATE, which
// conflicts with FOR KEY SHARE, and then waits for each member: a member that
// is in such a transition waits for the root, and Postgres aborts one of the
// two.
//
// Here the root is locked FOR NO KEY UPDATE first. It blocks spawns, which
// lock the root FOR SHARE, so the family cannot grow, but it lets those
// foreign key checks through. The members are then locked leaves first: a
// member is locked only after every transition of its descendants has
// committed, so no transition waits for a parent that is locked here.
// chatstate.SetFamilyArchived then finds every row locked.
func (p *Server) setFamilyArchivedInLockOrder(
	ctx context.Context,
	store database.Store,
	publisher chatstate.Publisher,
	input chatstate.SetFamilyArchivedInput,
) ([]database.Chat, error) {
	lockFamily := p.nestedSubagentsEnabled()
	cancelWakeups := input.Archived && p.experiments.Enabled(codersdk.ExperimentForkChatWakeups)
	if !lockFamily && !cancelWakeups {
		return chatstate.SetFamilyArchived(ctx, store, publisher, input)
	}

	// SetFamilyArchived publishes into this buffer when its nested
	// transaction returns, so nothing reaches subscribers before the outer
	// transaction commits.
	buffer := chatstate.NewPublishBuffer(publisher)
	defer buffer.Discard()
	var familyChats []database.Chat
	err := forkdb.InTx(store, func(tx database.Store, ftx forkdb.Store) error {
		if lockFamily {
			if err := lockFamilyForArchive(ctx, tx, ftx, input.RootID); err != nil {
				return err
			}
		}
		var err error
		familyChats, err = chatstate.SetFamilyArchived(ctx, tx, buffer, input)
		if err != nil || !cancelWakeups {
			return err
		}
		return cancelArchivedWakeups(ctx, ftx, familyChats, dbtime.Time(p.clock.Now()).UTC())
	}, nil)
	if err != nil {
		return nil, err
	}
	return familyChats, buffer.Flush()
}

// lockFamilyForArchive locks the root of a family FOR NO KEY UPDATE and then
// every member, leaves first.
func lockFamilyForArchive(ctx context.Context, tx database.Store, ftx forkdb.Store, rootID uuid.UUID) error {
	if _, err := ftx.LockChatForNoKeyUpdate(ctx, rootID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return chatstate.ErrChatNotFound
		}
		return xerrors.Errorf("lock root chat for archive: %w", err)
	}
	rows, err := ftx.GetChatSubtreeIDs(ctx, rootID)
	if err != nil {
		return xerrors.Errorf("get chat subtree: %w", err)
	}
	for _, row := range rows {
		if _, err := tx.GetChatByIDForUpdate(ctx, row.ID); err != nil {
			return xerrors.Errorf("lock family chat for archive: %w", err)
		}
	}
	return nil
}
