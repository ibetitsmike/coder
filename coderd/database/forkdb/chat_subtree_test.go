package forkdb_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/testutil"
)

func TestGetChatSubtreeIDs(t *testing.T) {
	t.Parallel()

	db, _ := dbtestutil.NewDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitShort)

	orgID := dbgen.Organization(t, db, database.Organization{}).ID
	ownerID := dbgen.User(t, db, database.User{}).ID
	modelID := dbgen.ChatModelConfig(t, db, database.ChatModelConfig{}).ID
	chat := func(parent *database.Chat) database.Chat {
		seed := database.Chat{OrganizationID: orgID, OwnerID: ownerID, LastModelConfigID: modelID}
		if parent != nil {
			seed.ParentChatID = uuid.NullUUID{UUID: parent.ID, Valid: true}
		}
		return dbgen.Chat(t, db, seed)
	}
	root := chat(nil)
	child := chat(&root)
	sibling := chat(&root)
	grandchild := chat(&child)
	// A chat outside the subtree must not appear.
	_ = chat(nil)

	rows, err := fdb.GetChatSubtreeIDs(ctx, root.ID)
	require.NoError(t, err)
	require.Len(t, rows, 3)
	require.Equal(t, forkdb.GetChatSubtreeIDsRow{ID: grandchild.ID, Depth: 2}, rows[0], "leaves come first")
	require.ElementsMatch(t, []forkdb.GetChatSubtreeIDsRow{
		{ID: child.ID, Depth: 1},
		{ID: sibling.ID, Depth: 1},
	}, rows[1:])

	rows, err = fdb.GetChatSubtreeIDs(ctx, child.ID)
	require.NoError(t, err)
	require.Equal(t, []forkdb.GetChatSubtreeIDsRow{{ID: grandchild.ID, Depth: 1}}, rows)

	rows, err = fdb.GetChatSubtreeIDs(ctx, grandchild.ID)
	require.NoError(t, err)
	require.Empty(t, rows)
}
