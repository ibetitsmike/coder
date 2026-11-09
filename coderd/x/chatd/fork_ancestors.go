package chatd

// This file is fork code: it resolves the ancestor chain sent to the workspace
// agent behind the fork-nested-subagents experiment. Upstream sends only the
// immediate parent in the Coder-Ancestor-Chat-Ids header, which leaves a
// grandchild's file edits and commands unattributed to the family root. The
// hooks in this file keep upstream's behavior when the experiment is off.

import (
	"context"

	"cdr.dev/slog/v3"
	"github.com/google/uuid"

	"github.com/coder/coder/v2/coderd/database"
)

// maxAncestorWalk bounds the ParentChatID walk. Nested subagents are capped at
// a depth far below this, so hitting the bound means the chat graph is corrupt.
const maxAncestorWalk = 64

// AncestorChatIDs returns the chat ids to send in the Coder-Ancestor-Chat-Ids
// header, nearest ancestor first ending at the family root. A root chat has no
// ancestors and returns nil.
//
// With nested subagents disabled this is exactly upstream's behavior: the
// immediate parent alone, with no database reads. With them enabled the chain
// continues up to the root so the agent attributes a grandchild's touched paths
// to every chat in the family. A failed lookup stops the walk and keeps the
// ancestors found so far, which is never worse than upstream.
func AncestorChatIDs(ctx context.Context, db database.Store, log slog.Logger, nested bool, chat database.Chat) []string {
	if !chat.ParentChatID.Valid {
		return nil
	}
	ancestorIDs := []string{chat.ParentChatID.UUID.String()}
	if !nested {
		return ancestorIDs
	}

	// Both the seen set and maxAncestorWalk stop the walk, so a corrupt
	// ParentChatID cycle cannot loop forever or grow the header unbounded.
	seen := map[uuid.UUID]struct{}{
		chat.ID:                {},
		chat.ParentChatID.UUID: {},
	}
	current := chat.ParentChatID.UUID
	for range maxAncestorWalk {
		parent, err := db.GetChatByID(ctx, current)
		if err != nil {
			log.Warn(ctx, "failed to resolve ancestor chat; sending partial ancestor chain",
				slog.F("chat_id", chat.ID),
				slog.F("ancestor_chat_id", current),
				slog.F("error", err),
			)
			return ancestorIDs
		}
		if !parent.ParentChatID.Valid {
			// current is the family root and is already in the chain.
			return ancestorIDs
		}
		next := parent.ParentChatID.UUID
		if _, ok := seen[next]; ok {
			log.Warn(ctx, "ancestor chat chain contains a cycle; sending partial ancestor chain",
				slog.F("chat_id", chat.ID),
				slog.F("ancestor_chat_id", next),
			)
			return ancestorIDs
		}
		seen[next] = struct{}{}
		ancestorIDs = append(ancestorIDs, next.String())
		current = next
	}

	log.Warn(ctx, "ancestor chat chain exceeded the walk bound; sending partial ancestor chain",
		slog.F("chat_id", chat.ID),
		slog.F("bound", maxAncestorWalk),
	)
	return ancestorIDs
}
