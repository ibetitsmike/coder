package dbgen

// This file is fork code: it seeds the fork tables of package forkdb. See
// ../migrations/chatdmig/README.md#fork-queries.

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
)

// ChatSubagent inserts the subagent row of the chat seed.ChatID through the
// fork queries of db.
func ChatSubagent(t testing.TB, db database.Store, seed forkdb.ChatSubagent) forkdb.ChatSubagent {
	t.Helper()

	fdb, err := forkdb.From(db)
	require.NoError(t, err, "fork queries")
	sub, err := fdb.InsertChatSubagent(genCtx, forkdb.InsertChatSubagentParams{
		ChatID:       seed.ChatID,
		AgentType:    takeFirst(seed.AgentType, "general"),
		Depth:        takeFirst(seed.Depth, 1),
		Isolation:    takeFirst(seed.Isolation, "shared"),
		WorktreePath: seed.WorktreePath,
		Branch:       seed.Branch,
		WorkspaceID:  seed.WorkspaceID,
	})
	require.NoError(t, err, "insert chat subagent")
	return sub
}
