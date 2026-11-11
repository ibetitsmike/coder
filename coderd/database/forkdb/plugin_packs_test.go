package forkdb_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/testutil"
)

func TestPluginPackResources(t *testing.T) {
	t.Parallel()

	db, _ := dbtestutil.NewDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitMedium)

	org := dbgen.Organization(t, db, database.Organization{})
	user := dbgen.User(t, db, database.User{})
	now := dbtime.Now()
	pack, err := fdb.InsertPluginPack(ctx, forkdb.InsertPluginPackParams{
		ID:             uuid.New(),
		OrganizationID: org.ID,
		Name:           "pstack",
		SourceUrl:      "https://github.com/cursor/plugins",
		SourcePath:     "pstack",
		Ref:            "main",
		CommitSha:      "0123456789abcdef0123456789abcdef01234567",
		CreatedBy:      user.ID,
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	require.NoError(t, err)

	// Binary content, including NUL bytes, survives the bytea array.
	err = fdb.InsertPluginPackResources(ctx, forkdb.InsertPluginPackResourcesParams{
		PackID:        pack.ID,
		Paths:         []string{"skills/b/SKILL.md", "agents/a.md"},
		Contents:      [][]byte{[]byte("b"), {0, 1, 2}},
		ContentHashes: []string{"hb", "ha"},
	})
	require.NoError(t, err)
	got, err := fdb.GetPluginPackResources(ctx, pack.ID)
	require.NoError(t, err)
	require.Equal(t, []forkdb.PluginPackResource{
		{PackID: pack.ID, Path: "agents/a.md", Content: []byte{0, 1, 2}, ContentHash: "ha"},
		{PackID: pack.ID, Path: "skills/b/SKILL.md", Content: []byte("b"), ContentHash: "hb"},
	}, got)

	require.NoError(t, fdb.DeletePluginPackByID(ctx, pack.ID))
	got, err = fdb.GetPluginPackResources(ctx, pack.ID)
	require.NoError(t, err)
	require.Empty(t, got, "deleting a pack deletes its resources")
}
