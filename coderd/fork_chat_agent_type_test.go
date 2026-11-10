package coderd_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/pubsub"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
)

func TestForkChatAgentType(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		enabled bool
	}{
		{name: "Enabled", enabled: true},
		{name: "Disabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := testutil.Context(t, testutil.WaitLong)
			client, db, api := newChatClientWithAPIAndDatabase(t, withChatWorkerDisabled, func(o *coderdtest.Options) {
				if tc.enabled {
					o.DeploymentValues.Experiments = append(o.DeploymentValues.Experiments, string(codersdk.ExperimentForkAgentDefinitions))
				}
			})
			user := coderdtest.CreateFirstUser(t, client.Client)
			modelConfig := createChatModel(t, client)

			root := dbgen.Chat(t, db, database.Chat{
				OrganizationID: user.OrganizationID, OwnerID: user.UserID,
				LastModelConfigID: modelConfig.ID, Title: "root",
			})
			newChild := func(title, agentType string, mode database.NullChatMode) database.Chat {
				child := dbgen.Chat(t, db, database.Chat{
					OrganizationID: user.OrganizationID, OwnerID: user.UserID,
					LastModelConfigID: modelConfig.ID, Title: title, Mode: mode,
					ParentChatID: uuid.NullUUID{UUID: root.ID, Valid: true},
					RootChatID:   uuid.NullUUID{UUID: root.ID, Valid: true},
				})
				if agentType != "" {
					dbgen.ChatSubagent(t, db, forkdb.ChatSubagent{ChatID: child.ID, AgentType: agentType})
				}
				return child
			}
			exploreMode := database.NullChatMode{ChatMode: database.ChatModeExplore, Valid: true}
			reviewer := newChild("reviewer child", "reviewer", database.NullChatMode{})
			// Children without a chat_subagents row take their type from
			// their mode.
			unstoredGeneral := newChild("unstored general child", "", database.NullChatMode{})
			unstoredExplore := newChild("unstored explore child", "", exploreMode)

			want := map[uuid.UUID]string{reviewer.ID: "", unstoredGeneral.ID: "", unstoredExplore.ID: ""}
			if tc.enabled {
				want[reviewer.ID] = "reviewer"
				want[unstoredGeneral.ID] = "general"
				want[unstoredExplore.ID] = "explore"
			}
			requireChildTypes := func(t *testing.T, chat codersdk.Chat) {
				t.Helper()
				require.Empty(t, chat.AgentType, "a root chat has no agent type")
				got := map[uuid.UUID]string{}
				for _, child := range chat.Children {
					got[child.ID] = child.AgentType
				}
				require.Equal(t, want, got)
			}

			chats, err := client.ListChats(ctx, nil)
			require.NoError(t, err)
			require.Len(t, chats, 1)
			requireChildTypes(t, chats[0])

			got, err := client.GetChat(ctx, root.ID)
			require.NoError(t, err)
			requireChildTypes(t, got)

			got, err = client.GetChat(ctx, reviewer.ID)
			require.NoError(t, err)
			require.Equal(t, want[reviewer.ID], got.AgentType)

			events, closer, err := client.WatchChats(ctx)
			require.NoError(t, err)
			defer closer.Close()
			created, err := json.Marshal(codersdk.ChatWatchEvent{
				Kind: codersdk.ChatWatchEventKindCreated,
				Chat: codersdk.Chat{ID: reviewer.ID, ParentChatID: &root.ID},
			})
			require.NoError(t, err)
			require.NoError(t, api.Pubsub.Publish(pubsub.ChatWatchEventChannel(user.UserID), created))
			event := testutil.RequireReceive(ctx, t, events)
			require.Equal(t, want[reviewer.ID], event.Chat.AgentType)
		})
	}
}
