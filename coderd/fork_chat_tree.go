package coderd

// This file is fork code: behind fork-nested-subagents it embeds every
// descendant of a root chat in the chat list and the single-chat GET.
// Upstream embeds only direct children, so depth-2+ subagents never reached
// the sidebar.

import (
	"context"
	"database/sql"

	"github.com/google/uuid"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/codersdk"
)

// forkMaxEmbeddedChatDepth caps the breadth-first descendant walk so a
// corrupted hierarchy cannot loop it. It matches forkdb's GetChatSubtreeIDs
// depth guard.
const forkMaxEmbeddedChatDepth = 64

// forkEmbeddedChildChats returns the chats embedded under the given roots.
// Without fork-nested-subagents it is exactly upstream's direct-children
// query. With the experiment on it walks the hierarchy level by level with
// the same query, so authorization post-filtering, has_unread and the
// archive filter all keep coming from upstream, and a descendant filtered
// out by authorization also hides its own subtree. Rows come back level by
// level, each level in the query's created_at DESC order.
func (api *API) forkEmbeddedChildChats(ctx context.Context, rootIDs []uuid.UUID, archived sql.NullBool) ([]database.GetChildChatsByParentIDsRow, error) {
	childRows, err := api.Database.GetChildChatsByParentIDs(ctx, database.GetChildChatsByParentIDsParams{
		ParentIds: rootIDs,
		Archived:  archived,
	})
	if err != nil || !api.Experiments.Enabled(codersdk.ExperimentForkNestedSubagents) {
		return childRows, err
	}

	seen := make(map[uuid.UUID]struct{}, len(rootIDs))
	for _, id := range rootIDs {
		seen[id] = struct{}{}
	}
	parentIDs := make([]uuid.UUID, 0, len(childRows))
	allRows := childRows
	for _, row := range childRows {
		if _, ok := seen[row.Chat.ID]; ok {
			continue
		}
		seen[row.Chat.ID] = struct{}{}
		parentIDs = append(parentIDs, row.Chat.ID)
	}
	for level := 1; len(parentIDs) > 0 && level < forkMaxEmbeddedChatDepth; level++ {
		levelRows, err := api.Database.GetChildChatsByParentIDs(ctx, database.GetChildChatsByParentIDsParams{
			ParentIds: parentIDs,
			Archived:  archived,
		})
		if err != nil {
			return nil, err
		}
		parentIDs = parentIDs[:0]
		for _, row := range levelRows {
			if _, ok := seen[row.Chat.ID]; ok {
				// Only a corrupted parent_chat_id cycle revisits a
				// chat; skip it instead of emitting it twice.
				continue
			}
			seen[row.Chat.ID] = struct{}{}
			allRows = append(allRows, row)
			parentIDs = append(parentIDs, row.Chat.ID)
		}
	}
	return allRows, nil
}
