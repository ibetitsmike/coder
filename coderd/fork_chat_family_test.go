package coderd_test

// This file is fork code: it covers the chat family capacity route of the
// fork-nested-subagents experiment.

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
)

func TestForkChatFamilyCapacity(t *testing.T) {
	t.Parallel()

	// newFamily starts coderd and returns the first user's client and a root
	// chat with one live and one finished subagent. The live one is paused,
	// so the chat daemon never acquires it.
	newFamily := func(t *testing.T, experiments []string) (*codersdk.Client, codersdk.CreateFirstUserResponse, database.Chat, database.Chat) {
		t.Helper()
		db, ps := dbtestutil.NewDB(t)
		client := coderdtest.New(t, &coderdtest.Options{
			Database: db,
			Pubsub:   ps,
			DeploymentValues: coderdtest.DeploymentValues(t, func(dv *codersdk.DeploymentValues) {
				dv.Experiments = experiments
				dv.AI.Chat.Fork.MaxLiveSubagentsPerFamily = 4
			}),
		})
		owner := coderdtest.CreateFirstUser(t, client)
		modelConfig := dbgen.ChatModelConfig(t, db, database.ChatModelConfig{OrganizationID: owner.OrganizationID})
		chat := func(parent uuid.NullUUID, status database.ChatStatus) database.Chat {
			return dbgen.Chat(t, db, database.Chat{
				OrganizationID:    owner.OrganizationID,
				OwnerID:           owner.UserID,
				LastModelConfigID: modelConfig.ID,
				ParentChatID:      parent,
				RootChatID:        parent,
				Status:            status,
			})
		}
		root := chat(uuid.NullUUID{}, database.ChatStatusWaiting)
		rootID := uuid.NullUUID{UUID: root.ID, Valid: true}
		live := chat(rootID, database.ChatStatusPaused)
		_ = chat(rootID, database.ChatStatusWaiting)
		return client, owner, root, live
	}

	t.Run("Enabled", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitLong)
		client, owner, root, child := newFamily(t, []string{string(codersdk.ExperimentForkNestedSubagents)})

		got, err := client.ForkChatFamilyCapacity(ctx, root.ID)
		require.NoError(t, err)
		require.Equal(t, codersdk.ForkChatFamilyCapacity{
			LiveSubagents:      1,
			MaxLiveSubagents:   4,
			WaitingForCapacity: []uuid.UUID{},
		}, got)

		_, err = client.ForkChatFamilyCapacity(ctx, child.ID)
		_ = requireSDKError(t, err, http.StatusBadRequest)

		member, _ := coderdtest.CreateAnotherUser(t, client, owner.OrganizationID)
		_, err = member.ForkChatFamilyCapacity(ctx, root.ID)
		_ = requireSDKError(t, err, http.StatusNotFound)
	})

	t.Run("Disabled", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitLong)
		client, _, root, _ := newFamily(t, nil)

		_, err := client.ForkChatFamilyCapacity(ctx, root.ID)
		_ = requireSDKError(t, err, http.StatusForbidden)
	})
}
