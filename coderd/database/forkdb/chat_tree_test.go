package forkdb_test

import (
	"database/sql"
	"reflect"
	"testing"

	"github.com/brianvoe/gofakeit/v7"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/testutil"
)

// Chat copies every column of chats_expanded into the field of
// database.Chat with the same column tag, and database.Chat has no column
// that ChatsExpanded lacks.
func TestChatsExpandedChat(t *testing.T) {
	t.Parallel()

	var expanded forkdb.ChatsExpanded
	require.NoError(t, gofakeit.New(0).Struct(&expanded))
	chat := expanded.Chat()

	from := reflect.ValueOf(expanded)
	to := reflect.ValueOf(chat)
	toByColumn := map[string]reflect.Value{}
	for i := range to.NumField() {
		toByColumn[to.Type().Field(i).Tag.Get("db")] = to.Field(i)
	}
	require.Equal(t, to.NumField(), from.NumField(), "database.Chat and forkdb.ChatsExpanded must have the same columns")
	for i := range from.NumField() {
		column := from.Type().Field(i).Tag.Get("db")
		got, ok := toByColumn[column]
		require.True(t, ok, "database.Chat has no column %q", column)
		if from.Field(i).Kind() != reflect.Bool {
			require.False(t, from.Field(i).IsZero(), "the faker left column %q zero, so the test cannot see it copied", column)
		}
		require.Equal(t, from.Field(i).Interface(), got.Interface(), "column %q", column)
	}
}

func TestGetDescendantChatsByRootIDs(t *testing.T) {
	t.Parallel()

	db, _ := dbtestutil.NewDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitShort)

	orgID := dbgen.Organization(t, db, database.Organization{}).ID
	ownerID := dbgen.User(t, db, database.User{}).ID
	modelID := dbgen.ChatModelConfig(t, db, database.ChatModelConfig{}).ID
	chat := func(root, parent *database.Chat) database.Chat {
		seed := database.Chat{OrganizationID: orgID, OwnerID: ownerID, LastModelConfigID: modelID}
		if parent != nil {
			seed.ParentChatID = uuid.NullUUID{UUID: parent.ID, Valid: true}
		}
		if root != nil {
			seed.RootChatID = uuid.NullUUID{UUID: root.ID, Valid: true}
		}
		return dbgen.Chat(t, db, seed)
	}
	root := chat(nil, nil)
	child := chat(&root, &root)
	// A direct child without root_chat_id still belongs to the root.
	legacyChild := chat(nil, &root)
	grandchild := chat(&root, &child)
	greatGrandchild := chat(&root, &grandchild)
	archivedChild := chat(&root, &root)
	_, err := db.ArchiveChatByID(ctx, archivedChild.ID)
	require.NoError(t, err)
	// Another family must not appear.
	otherRoot := chat(nil, nil)
	_ = chat(&otherRoot, &otherRoot)

	// Each row matches upstream's row of the same chat.
	upstreamRows := map[uuid.UUID]database.GetChildChatsByParentIDsRow{}
	for _, parent := range []database.Chat{root, child, grandchild, otherRoot} {
		rows, err := db.GetChildChatsByParentIDs(ctx, database.GetChildChatsByParentIDsParams{ParentIds: []uuid.UUID{parent.ID}})
		require.NoError(t, err)
		for _, row := range rows {
			upstreamRows[row.Chat.ID] = row
		}
	}

	ids := func(archived sql.NullBool) []uuid.UUID {
		t.Helper()
		rows, err := fdb.GetDescendantChatsByRootIDs(ctx, forkdb.GetDescendantChatsByRootIDsParams{
			RootIds:  []uuid.UUID{root.ID},
			Archived: archived,
		})
		require.NoError(t, err)
		var got []uuid.UUID
		for _, row := range rows {
			require.Equal(t, upstreamRows[row.ChatsExpanded.ID], row.ChildChatRow())
			got = append(got, row.ChatsExpanded.ID)
		}
		return got
	}
	require.ElementsMatch(t, []uuid.UUID{archivedChild.ID, greatGrandchild.ID, grandchild.ID, legacyChild.ID, child.ID}, ids(sql.NullBool{}))
	require.ElementsMatch(t, []uuid.UUID{greatGrandchild.ID, grandchild.ID, legacyChild.ID, child.ID}, ids(sql.NullBool{Bool: false, Valid: true}))
	require.Equal(t, []uuid.UUID{archivedChild.ID}, ids(sql.NullBool{Bool: true, Valid: true}))
}

// DescendantsByLevel returns the reached rows level by level in the order
// of the query, drops the subtree of a row the query left out, and stops
// after levels.
func TestDescendantsByLevel(t *testing.T) {
	t.Parallel()

	row := func(parent uuid.UUID) forkdb.GetDescendantChatsByRootIDsRow {
		return forkdb.GetDescendantChatsByRootIDsRow{ChatsExpanded: forkdb.ChatsExpanded{
			ID:           uuid.New(),
			ParentChatID: uuid.NullUUID{UUID: parent, Valid: true},
		}}
	}
	root := uuid.New()
	childA := row(root)
	childB := row(root)
	grandA := row(childA.ChatsExpanded.ID)
	greatA := row(grandA.ChatsExpanded.ID)
	// hidden is not in the rows, as when the archive filter or
	// authorization drops it, so its child must not be reached.
	hidden := uuid.New()
	orphan := row(hidden)
	orphanChild := row(orphan.ChatsExpanded.ID)

	// The query orders by created_at DESC, so deeper chats can come first.
	rows := []forkdb.GetDescendantChatsByRootIDsRow{greatA, orphanChild, grandA, childB, orphan, childA}
	ids := func(rows []forkdb.GetDescendantChatsByRootIDsRow) []uuid.UUID {
		var got []uuid.UUID
		for _, r := range rows {
			got = append(got, r.ChatsExpanded.ID)
		}
		return got
	}

	require.Equal(t, []uuid.UUID{
		childB.ChatsExpanded.ID, childA.ChatsExpanded.ID,
		grandA.ChatsExpanded.ID,
		greatA.ChatsExpanded.ID,
	}, ids(forkdb.DescendantsByLevel(rows, []uuid.UUID{root}, 64)))
	require.Equal(t, []uuid.UUID{childB.ChatsExpanded.ID, childA.ChatsExpanded.ID},
		ids(forkdb.DescendantsByLevel(rows, []uuid.UUID{root}, 1)))
	// A subtree root inside the family yields only its own subtree.
	require.Equal(t, []uuid.UUID{grandA.ChatsExpanded.ID, greatA.ChatsExpanded.ID},
		ids(forkdb.DescendantsByLevel(rows, []uuid.UUID{childA.ChatsExpanded.ID}, 64)))
	require.Empty(t, forkdb.DescendantsByLevel(rows, []uuid.UUID{root}, 0))
}
