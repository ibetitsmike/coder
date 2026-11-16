package coderd_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/util/ptr"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
)

// forkChatFamily inserts a root -> child -> grandchild chat family the
// way the nested spawn path creates one: every descendant carries its
// direct parent's ID and the family root's ID.
func forkChatFamily(
	t testing.TB,
	db database.Store,
	orgID, ownerID, modelConfigID uuid.UUID,
) (root, child, grandchild database.Chat) {
	t.Helper()
	root = dbgen.Chat(t, db, database.Chat{
		OrganizationID:    orgID,
		OwnerID:           ownerID,
		LastModelConfigID: modelConfigID,
		Title:             "fork family root",
	})
	child = dbgen.Chat(t, db, database.Chat{
		OrganizationID:    orgID,
		OwnerID:           ownerID,
		LastModelConfigID: modelConfigID,
		Title:             "fork family child",
		ParentChatID:      uuid.NullUUID{UUID: root.ID, Valid: true},
		RootChatID:        uuid.NullUUID{UUID: root.ID, Valid: true},
	})
	grandchild = dbgen.Chat(t, db, database.Chat{
		OrganizationID:    orgID,
		OwnerID:           ownerID,
		LastModelConfigID: modelConfigID,
		Title:             "fork family grandchild",
		ParentChatID:      uuid.NullUUID{UUID: child.ID, Valid: true},
		RootChatID:        uuid.NullUUID{UUID: root.ID, Valid: true},
	})
	return root, child, grandchild
}

// chatChildrenByID indexes an embedded children list by chat ID.
func chatChildrenByID(t testing.TB, children []codersdk.Chat) map[uuid.UUID]codersdk.Chat {
	t.Helper()
	byID := make(map[uuid.UUID]codersdk.Chat, len(children))
	for _, c := range children {
		byID[c.ID] = c
	}
	return byID
}

func TestForkChatsEmbedDescendants(t *testing.T) {
	t.Parallel()

	t.Run("Enabled", func(t *testing.T) {
		t.Parallel()

		ctx := testutil.Context(t, testutil.WaitLong)
		client, db := newChatClientWithDatabase(t, func(opts *coderdtest.Options) {
			opts.DeploymentValues.Experiments = append(
				opts.DeploymentValues.Experiments,
				string(codersdk.ExperimentForkNestedSubagents),
			)
		})
		user := coderdtest.CreateFirstUser(t, client.Client)
		modelConfig := createChatModel(t, client)
		root, child, grandchild := forkChatFamily(
			t, db, user.OrganizationID, user.UserID, modelConfig.ID,
		)

		chats, err := client.ListChats(ctx, nil)
		require.NoError(t, err)
		var listRoot *codersdk.Chat
		for i := range chats {
			if chats[i].ID == root.ID {
				listRoot = &chats[i]
			}
		}
		require.NotNil(t, listRoot, "the root should appear in the list")
		children := chatChildrenByID(t, listRoot.Children)
		require.Len(t, listRoot.Children, 2, "the root embeds its child and grandchild")
		require.Contains(t, children, child.ID)
		require.Contains(t, children, grandchild.ID)
		require.NotNil(t, children[grandchild.ID].ParentChatID)
		require.Equal(t, child.ID, *children[grandchild.ID].ParentChatID,
			"the grandchild is embedded under the root with its own parent")
		require.NotNil(t, children[grandchild.ID].RootChatID)
		require.Equal(t, root.ID, *children[grandchild.ID].RootChatID)
		require.Empty(t, children[child.ID].Children,
			"child chats never embed their own children")

		// The single-chat GET of the root embeds the same family.
		gotRoot, err := client.GetChat(ctx, root.ID)
		require.NoError(t, err)
		gotChildren := chatChildrenByID(t, gotRoot.Children)
		require.Len(t, gotRoot.Children, 2)
		require.Contains(t, gotChildren, child.ID)
		require.Contains(t, gotChildren, grandchild.ID)
		require.NotNil(t, gotChildren[grandchild.ID].ParentChatID)
		require.Equal(t, child.ID, *gotChildren[grandchild.ID].ParentChatID)

		// A mid-level chat still reports no embedded children.
		gotChild, err := client.GetChat(ctx, child.ID)
		require.NoError(t, err)
		require.NotNil(t, gotChild.Children)
		require.Empty(t, gotChild.Children)

		// Archiving the family cascades to every member, and the
		// archived view still embeds the whole depth-2 family while
		// the unarchived list hides it.
		err = client.UpdateChat(ctx, root.ID, codersdk.UpdateChatRequest{
			Archived: ptr.Ref(true),
		})
		require.NoError(t, err)

		archivedRoot, err := client.GetChat(ctx, root.ID)
		require.NoError(t, err)
		require.True(t, archivedRoot.Archived)
		require.Len(t, archivedRoot.Children, 2,
			"an archived root embeds its archived descendants")
		require.Contains(t,
			chatChildrenByID(t, archivedRoot.Children), grandchild.ID)

		archivedList, err := client.ListChats(ctx, &codersdk.ListChatsOptions{
			Query: "archived:true",
		})
		require.NoError(t, err)
		var archivedListRoot *codersdk.Chat
		for i := range archivedList {
			if archivedList[i].ID == root.ID {
				archivedListRoot = &archivedList[i]
			}
		}
		require.NotNil(t, archivedListRoot, "the archived root appears in the archived list")
		require.Len(t, archivedListRoot.Children, 2)
	})
	t.Run("Disabled", func(t *testing.T) {
		t.Parallel()

		ctx := testutil.Context(t, testutil.WaitLong)
		client, db := newChatClientWithDatabase(t)
		user := coderdtest.CreateFirstUser(t, client.Client)
		modelConfig := createChatModel(t, client)
		root, child, grandchild := forkChatFamily(
			t, db, user.OrganizationID, user.UserID, modelConfig.ID,
		)

		// Without the experiment the list embeds only direct children,
		// so the grandchild is absent: upstream behavior.
		chats, err := client.ListChats(ctx, nil)
		require.NoError(t, err)
		var listRoot *codersdk.Chat
		for i := range chats {
			if chats[i].ID == root.ID {
				listRoot = &chats[i]
			}
		}
		require.NotNil(t, listRoot, "the root should appear in the list")
		require.Len(t, listRoot.Children, 1, "only the direct child is embedded")
		require.Equal(t, child.ID, listRoot.Children[0].ID)
		require.NotContains(t,
			chatChildrenByID(t, listRoot.Children), grandchild.ID)

		gotRoot, err := client.GetChat(ctx, root.ID)
		require.NoError(t, err)
		require.Len(t, gotRoot.Children, 1, "only the direct child is embedded")
		require.Equal(t, child.ID, gotRoot.Children[0].ID)
		require.NotContains(t,
			chatChildrenByID(t, gotRoot.Children), grandchild.ID)
	})
}

// chatTreeCountStore counts the queries that read embedded chats, also in
// transactions and in the fork queries it hands out.
type chatTreeCountStore struct {
	database.Store
	children    *atomic.Int64
	descendants *atomic.Int64
}

func (s chatTreeCountStore) GetChildChatsByParentIDs(ctx context.Context, arg database.GetChildChatsByParentIDsParams) ([]database.GetChildChatsByParentIDsRow, error) {
	s.children.Add(1)
	return s.Store.GetChildChatsByParentIDs(ctx, arg)
}

func (s chatTreeCountStore) InTx(fn func(database.Store) error, opts *database.TxOptions) error {
	return s.Store.InTx(func(tx database.Store) error {
		return fn(chatTreeCountStore{Store: tx, children: s.children, descendants: s.descendants})
	}, opts)
}

func (s chatTreeCountStore) ForkUnwrap() database.Store { return s.Store }

func (s chatTreeCountStore) ForkWrap(inner forkdb.Store) forkdb.Store {
	return chatTreeCountForkStore{Store: inner, descendants: s.descendants}
}

type chatTreeCountForkStore struct {
	forkdb.Store
	descendants *atomic.Int64
}

func (s chatTreeCountForkStore) GetDescendantChatsByRootIDs(ctx context.Context, arg forkdb.GetDescendantChatsByRootIDsParams) ([]forkdb.GetDescendantChatsByRootIDsRow, error) {
	s.descendants.Add(1)
	return s.Store.GetDescendantChatsByRootIDs(ctx, arg)
}

// The list and the single-chat GET read a whole family in one statement,
// and a chat that the archive filter or authorization hides also hides its
// subtree.
func TestForkChatTreeStatements(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	raw, ps := dbtestutil.NewDB(t)
	var children, descendants atomic.Int64
	client, _ := newChatClientWithDatabase(t, func(opts *coderdtest.Options) {
		opts.Database = chatTreeCountStore{Store: raw, children: &children, descendants: &descendants}
		opts.Pubsub = ps
		opts.DeploymentValues.Experiments = append(
			opts.DeploymentValues.Experiments,
			string(codersdk.ExperimentForkNestedSubagents),
		)
	})
	firstUser := coderdtest.CreateFirstUser(t, client.Client)
	modelConfig := createChatModel(t, client)
	memberRaw, member := coderdtest.CreateAnotherUser(t, client.Client, firstUser.OrganizationID)
	memberClient := codersdk.NewExperimentalClient(memberRaw)
	root, child, grandchild := forkChatFamily(t, raw, firstUser.OrganizationID, member.ID, modelConfig.ID)
	greatGrandchild := dbgen.Chat(t, raw, database.Chat{
		OrganizationID:    firstUser.OrganizationID,
		OwnerID:           member.ID,
		LastModelConfigID: modelConfig.ID,
		ParentChatID:      uuid.NullUUID{UUID: grandchild.ID, Valid: true},
		RootChatID:        uuid.NullUUID{UUID: root.ID, Valid: true},
	})

	reset := func() {
		children.Store(0)
		descendants.Store(0)
	}
	requireOneStatement := func(what string) {
		t.Helper()
		require.EqualValues(t, 0, children.Load(), "%s: per-level child statements", what)
		require.EqualValues(t, 1, descendants.Load(), "%s: descendant statements", what)
	}
	listedRoot := func(c *codersdk.ExperimentalClient) codersdk.Chat {
		t.Helper()
		chats, err := c.ListChats(ctx, nil)
		require.NoError(t, err)
		for _, chat := range chats {
			if chat.ID == root.ID {
				return chat
			}
		}
		require.FailNow(t, "the root should appear in the list")
		return codersdk.Chat{}
	}
	ids := func(chats []codersdk.Chat) []uuid.UUID {
		got := make([]uuid.UUID, 0, len(chats))
		for _, chat := range chats {
			got = append(got, chat.ID)
		}
		return got
	}
	family := []uuid.UUID{child.ID, grandchild.ID, greatGrandchild.ID}

	reset()
	gotRoot, err := memberClient.GetChat(ctx, root.ID)
	require.NoError(t, err)
	requireOneStatement("GET")
	require.Equal(t, family, ids(gotRoot.Children), "level by level")

	reset()
	require.Equal(t, family, ids(listedRoot(memberClient).Children), "level by level")
	requireOneStatement("list")

	// A grandchild the member cannot read hides its own child.
	hiddenRoot, hiddenChild, hiddenGrandchild := forkChatFamily(t, raw, firstUser.OrganizationID, member.ID, modelConfig.ID)
	foreign := dbgen.Chat(t, raw, database.Chat{
		OrganizationID:    firstUser.OrganizationID,
		OwnerID:           firstUser.UserID,
		LastModelConfigID: modelConfig.ID,
		ParentChatID:      uuid.NullUUID{UUID: hiddenChild.ID, Valid: true},
		RootChatID:        uuid.NullUUID{UUID: hiddenRoot.ID, Valid: true},
	})
	_ = dbgen.Chat(t, raw, database.Chat{
		OrganizationID:    firstUser.OrganizationID,
		OwnerID:           member.ID,
		LastModelConfigID: modelConfig.ID,
		ParentChatID:      uuid.NullUUID{UUID: foreign.ID, Valid: true},
		RootChatID:        uuid.NullUUID{UUID: hiddenRoot.ID, Valid: true},
	})
	gotRoot, err = memberClient.GetChat(ctx, hiddenRoot.ID)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{hiddenChild.ID, hiddenGrandchild.ID}, ids(gotRoot.Children))

	// An archived child hides its unarchived subtree from the
	// unarchived list.
	//nolint:gocritic // The test archives one chat without its subtree.
	_, err = raw.ArchiveChatByID(dbauthz.AsSystemRestricted(ctx), child.ID)
	require.NoError(t, err)
	require.Empty(t, listedRoot(memberClient).Children)
}
