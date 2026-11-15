package chatd

// This file is fork code: it builds the Coder-Ancestor-Chat-Ids header value
// behind the fork-nested-subagents experiment, so the workspace agent
// attributes a grandchild's touched paths to every chat in its family.

import (
	"context"

	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"

	"github.com/coder/coder/v2/coderd/database"
)

// maxAncestorWalk bounds the parent walk far above any subagent depth limit,
// so a corrupt parent cycle cannot loop forever.
const maxAncestorWalk = 64

// AncestorChatIDs returns the ancestors of chat, nearest first, for the
// Coder-Ancestor-Chat-Ids header. With nested subagents off it matches
// upstream: the parent alone, with no database reads. A failed lookup keeps
// the ancestors found so far.
func AncestorChatIDs(ctx context.Context, db database.Store, log slog.Logger, nested bool, chat database.Chat) []string {
	ids, err := walkAncestorChatIDs(ctx, db, nested, chat)
	if err != nil {
		log.Warn(ctx, "resolve ancestor chat", slog.F("chat_id", chat.ID), slog.Error(err))
	}
	return ids
}

// walkAncestorChatIDs is AncestorChatIDs that returns a failed lookup, with the
// ancestors found before it.
func walkAncestorChatIDs(ctx context.Context, db database.Store, nested bool, chat database.Chat) ([]string, error) { //nolint:revive // nested is the fork-nested-subagents experiment, as in AncestorChatIDs.
	if !chat.ParentChatID.Valid {
		return nil, nil
	}
	ids := []string{chat.ParentChatID.UUID.String()}
	if !nested {
		return ids, nil
	}
	current := chat.ParentChatID.UUID
	for range maxAncestorWalk {
		parent, err := db.GetChatByID(ctx, current)
		if err != nil {
			return ids, xerrors.Errorf("get ancestor chat %s: %w", current, err)
		}
		if !parent.ParentChatID.Valid {
			return ids, nil
		}
		current = parent.ParentChatID.UUID
		ids = append(ids, current.String())
	}
	return ids, nil
}
