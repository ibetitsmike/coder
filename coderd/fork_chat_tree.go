package coderd

// This file is fork code: behind fork-nested-subagents it embeds every
// descendant of a root chat in the chat list and the single-chat GET.
// Upstream embeds only direct children, so depth-2+ subagents never reached
// the sidebar.

import (
	"context"
	"database/sql"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/codersdk"
)

// forkMaxEmbeddedChatDepth caps the descendant levels embedded under a
// root. It matches forkdb's GetChatSubtreeIDs depth guard.
const forkMaxEmbeddedChatDepth = 64

// forkEmbeddedChildChats returns the chats embedded under the given roots.
// Without fork-nested-subagents it is exactly upstream's direct-children
// query. With the experiment on it reads every descendant of the roots in
// one statement, with upstream's authorization post-filter, has_unread and
// archive filter, and drops a descendant whose parent was filtered out, so
// a hidden chat also hides its own subtree. Rows come back level by level,
// each level in created_at DESC order, as a walk with upstream's query
// returns them.
func (api *API) forkEmbeddedChildChats(ctx context.Context, rootIDs []uuid.UUID, archived sql.NullBool) ([]database.GetChildChatsByParentIDsRow, error) {
	if !api.Experiments.Enabled(codersdk.ExperimentForkNestedSubagents) {
		return api.Database.GetChildChatsByParentIDs(ctx, database.GetChildChatsByParentIDsParams{
			ParentIds: rootIDs,
			Archived:  archived,
		})
	}
	fdb, err := forkdb.From(api.Database)
	if err != nil {
		return nil, xerrors.Errorf("fork queries: %w", err)
	}
	rows, err := fdb.GetDescendantChatsByRootIDs(ctx, forkdb.GetDescendantChatsByRootIDsParams{
		RootIds:  rootIDs,
		Archived: archived,
		MaxDepth: forkMaxEmbeddedChatDepth,
	})
	if err != nil {
		return nil, err
	}
	rows = forkdb.DescendantsByLevel(rows, rootIDs, forkMaxEmbeddedChatDepth)
	childRows := make([]database.GetChildChatsByParentIDsRow, len(rows))
	for i, row := range rows {
		childRows[i] = row.ChildChatRow()
	}
	return childRows, nil
}
