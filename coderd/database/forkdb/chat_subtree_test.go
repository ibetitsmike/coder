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
	chat := func(parent *database.Chat, status database.ChatStatus) database.Chat {
		seed := database.Chat{OrganizationID: orgID, OwnerID: ownerID, LastModelConfigID: modelID, Status: status}
		if parent != nil {
			seed.ParentChatID = uuid.NullUUID{UUID: parent.ID, Valid: true}
		}
		return dbgen.Chat(t, db, seed)
	}
	root := chat(nil, database.ChatStatusWaiting)
	child := chat(&root, database.ChatStatusWaiting)
	sibling := chat(&root, database.ChatStatusRunning)
	grandchild := chat(&child, database.ChatStatusRequiresAction)
	// A chat outside the subtree must not appear.
	_ = chat(nil, database.ChatStatusWaiting)

	rows, err := fdb.GetChatSubtreeIDs(ctx, root.ID)
	require.NoError(t, err)
	require.Len(t, rows, 3)
	require.Equal(t, forkdb.GetChatSubtreeIDsRow{ID: grandchild.ID, Status: database.ChatStatusRequiresAction, Depth: 2}, rows[0], "leaves come first")
	require.ElementsMatch(t, []forkdb.GetChatSubtreeIDsRow{
		{ID: child.ID, Status: database.ChatStatusWaiting, Depth: 1},
		{ID: sibling.ID, Status: database.ChatStatusRunning, Depth: 1},
	}, rows[1:])

	rows, err = fdb.GetChatSubtreeIDs(ctx, child.ID)
	require.NoError(t, err)
	require.Equal(t, []forkdb.GetChatSubtreeIDsRow{{ID: grandchild.ID, Status: database.ChatStatusRequiresAction, Depth: 1}}, rows)

	rows, err = fdb.GetChatSubtreeIDs(ctx, grandchild.ID)
	require.NoError(t, err)
	require.Empty(t, rows)
}
