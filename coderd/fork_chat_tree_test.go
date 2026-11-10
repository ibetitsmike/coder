package coderd_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
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
