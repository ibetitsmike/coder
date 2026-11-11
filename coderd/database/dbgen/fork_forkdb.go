package dbgen

// This file is fork code: it seeds the fork tables of package forkdb. See
// ../migrations/chatdmig/README.md#fork-queries.

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/testutil"
)

// Plugin inserts a plugin through the fork queries of db.
func Plugin(t testing.TB, db database.Store, seed forkdb.Plugin) forkdb.Plugin {
	t.Helper()

	fdb, err := forkdb.From(db)
	require.NoError(t, err, "fork queries")
	now := dbtime.Now()
	plugin, err := fdb.InsertPlugin(genCtx, forkdb.InsertPluginParams{
		ID:             takeFirst(seed.ID, uuid.New()),
		OrganizationID: takeFirst(seed.OrganizationID, uuid.New()),
		Name:           takeFirst(seed.Name, testutil.GetRandomName(t)),
		Version:        seed.Version,
		FileID:         takeFirst(seed.FileID, uuid.New()),
		Manifest:       takeFirstSlice(seed.Manifest, []byte("{}")),
		Grants:         takeFirstSlice(seed.Grants, []byte("{}")),
		Config:         seed.Config,
		ConfigKeyID:    seed.ConfigKeyID,
		Enabled:        seed.Enabled,
		CreatedBy:      takeFirst(seed.CreatedBy, uuid.New()),
		CreatedAt:      takeFirst(seed.CreatedAt, now),
		UpdatedAt:      takeFirst(seed.UpdatedAt, now),
	})
	require.NoError(t, err, "insert plugin")
	return plugin
}

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
		ToolPolicy:   seed.ToolPolicy,
	})
	require.NoError(t, err, "insert chat subagent")
	return sub
}

// SandboxOperation inserts a sandbox operation intent row for the build
// seed.WorkspaceBuildID through the fork queries of db.
func SandboxOperation(t testing.TB, db database.Store, seed forkdb.SandboxOperation) forkdb.SandboxOperation {
	t.Helper()

	fdb, err := forkdb.From(db)
	require.NoError(t, err, "fork queries")
	op, err := fdb.InsertSandboxOperation(genCtx, forkdb.InsertSandboxOperationParams{
		ID:               takeFirst(seed.ID, uuid.New()),
		WorkspaceID:      seed.WorkspaceID,
		WorkspaceBuildID: seed.WorkspaceBuildID,
		Operation:        takeFirst(seed.Operation, "create"),
		Generation:       takeFirst(seed.Generation, 1),
	})
	require.NoError(t, err, "insert sandbox operation")
	return op
}
