package coderd_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/forkdb"
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
			client, db := newChatClientWithDatabase(t, withChatWorkerDisabled, func(o *coderdtest.Options) {
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
			newChild := func(title, agentType string) database.Chat {
				child := dbgen.Chat(t, db, database.Chat{
					OrganizationID: user.OrganizationID, OwnerID: user.UserID,
					LastModelConfigID: modelConfig.ID, Title: title,
					ParentChatID: uuid.NullUUID{UUID: root.ID, Valid: true},
					RootChatID:   uuid.NullUUID{UUID: root.ID, Valid: true},
				})
				if agentType != "" {
					dbgen.ChatSubagent(t, db, forkdb.ChatSubagent{ChatID: child.ID, AgentType: agentType})
				}
				return child
			}
			reviewer := newChild("reviewer child", "reviewer")
			explorer := newChild("explore child", "explore")
			untyped := newChild("untyped child", "")

			want := map[uuid.UUID]string{reviewer.ID: "", explorer.ID: "", untyped.ID: ""}
			if tc.enabled {
				want[reviewer.ID] = "reviewer"
				want[explorer.ID] = "explore"
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
		})
	}
}
