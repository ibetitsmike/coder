package chatd

// This file is fork code: it orders the row locks of a family archive behind
// the fork-nested-subagents experiment. Upstream's setChatFamilyArchived calls
// forkSetFamilyArchived, which runs chatstate.SetFamilyArchived unchanged when
// the experiment is off.

import (
	"context"
	"database/sql"
	"errors"

	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatstate"
)

// forkSetFamilyArchived runs chatstate.SetFamilyArchived after it locks the
// family in an order that cannot deadlock with a member's own transition.
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
func (p *Server) forkSetFamilyArchived(ctx context.Context, input chatstate.SetFamilyArchivedInput) ([]database.Chat, error) {
	if !p.nestedSubagentsEnabled() {
		return chatstate.SetFamilyArchived(ctx, p.db, p.pubsub, input)
	}

	// SetFamilyArchived publishes into this buffer when its nested
	// transaction returns, so nothing reaches subscribers before the outer
	// transaction commits.
	buffer := chatstate.NewPublishBuffer(p.pubsub)
	defer buffer.Discard()
	var familyChats []database.Chat
	err := forkdb.InTx(p.db, func(tx database.Store, ftx forkdb.Store) error {
		if _, err := ftx.LockChatForNoKeyUpdate(ctx, input.RootID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return chatstate.ErrChatNotFound
			}
			return xerrors.Errorf("lock root chat for archive: %w", err)
		}
		rows, err := ftx.GetChatSubtreeIDs(ctx, input.RootID)
		if err != nil {
			return xerrors.Errorf("get chat subtree: %w", err)
		}
		for _, row := range rows {
			if _, err := tx.GetChatByIDForUpdate(ctx, row.ID); err != nil {
				return xerrors.Errorf("lock family chat for archive: %w", err)
			}
		}
		familyChats, err = chatstate.SetFamilyArchived(ctx, tx, buffer, input)
		return err
	}, nil)
	if err != nil {
		return nil, err
	}
	return familyChats, buffer.Flush()
}
