package forkdb_test

import (
	"database/sql"
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

func TestPluginPackEnablementOrganization(t *testing.T) {
	t.Parallel()

	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitMedium)

	org := dbgen.Organization(t, db, database.Organization{})
	otherOrg := dbgen.Organization(t, db, database.Organization{})
	user := dbgen.User(t, db, database.User{})
	now := dbtime.Now()
	pack, err := fdb.InsertPluginPack(ctx, forkdb.InsertPluginPackParams{
		ID: uuid.New(), OrganizationID: org.ID, Name: "pstack", SourceUrl: "https://github.com/cursor/plugins",
		Ref: "main", CommitSha: "0123456789abcdef0123456789abcdef01234567", CreatedBy: user.ID,
		CreatedAt: now, UpdatedAt: now,
	})
	require.NoError(t, err)
	otherProject := dbgen.ChatProject(t, db, database.ChatProject{OrganizationID: otherOrg.ID, OwnerID: user.ID})
	otherModel := dbgen.ChatModelConfig(t, db, database.ChatModelConfig{OrganizationID: otherOrg.ID})
	otherChat := dbgen.Chat(t, db, database.Chat{OrganizationID: otherOrg.ID, OwnerID: user.ID, LastModelConfigID: otherModel.ID})

	// Writers that bypass the queries still cannot enable a pack for a
	// project or chat of another organization: the row's organization must
	// own both the pack and the target.
	_, err = sqlDB.ExecContext(ctx, `INSERT INTO plugin_pack_projects (organization_id, project_id, pack_id) VALUES ($1, $2, $3)`, org.ID, otherProject.ID, pack.ID)
	require.True(t, database.IsCheckViolation(err, "plugin_pack_projects_organization"), "project of another organization: %v", err)
	_, err = sqlDB.ExecContext(ctx, `INSERT INTO plugin_pack_projects (organization_id, project_id, pack_id) VALUES ($1, $2, $3)`, otherOrg.ID, otherProject.ID, pack.ID)
	require.True(t, database.IsForeignKeyViolation(err), "pack of another organization: %v", err)
	_, err = sqlDB.ExecContext(ctx, `INSERT INTO chat_plugin_packs (organization_id, chat_id, pack_id) VALUES ($1, $2, $3)`, org.ID, otherChat.ID, pack.ID)
	require.True(t, database.IsCheckViolation(err, "chat_plugin_packs_organization"), "chat of another organization: %v", err)
	_, err = sqlDB.ExecContext(ctx, `INSERT INTO chat_plugin_packs (organization_id, chat_id, pack_id) VALUES ($1, $2, $3)`, otherOrg.ID, otherChat.ID, pack.ID)
	require.True(t, database.IsForeignKeyViolation(err), "pack of another organization: %v", err)

	// The queries join on the organization, so a pack of another
	// organization matches no row.
	_, err = fdb.InsertPluginPackProject(ctx, forkdb.InsertPluginPackProjectParams{ProjectID: otherProject.ID, PackID: pack.ID})
	require.ErrorIs(t, err, sql.ErrNoRows)
	_, err = fdb.InsertChatPluginPack(ctx, forkdb.InsertChatPluginPackParams{ChatID: otherChat.ID, PackID: pack.ID})
	require.ErrorIs(t, err, sql.ErrNoRows)
}
