package coderd_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
)

func TestForkPluginPackEnablement(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	db, ps := dbtestutil.NewDB(t)
	client := coderdtest.New(t, &coderdtest.Options{
		Database: db,
		Pubsub:   ps,
		DeploymentValues: coderdtest.DeploymentValues(t, func(dv *codersdk.DeploymentValues) {
			dv.Experiments = []string{string(codersdk.ExperimentForkPluginPacks)}
		}),
	})
	owner := coderdtest.CreateFirstUser(t, client)
	orgID := owner.OrganizationID
	member, memberUser := coderdtest.CreateAnotherUser(t, client, orgID)
	other, _ := coderdtest.CreateAnotherUser(t, client, orgID)

	pack := insertForkPluginPack(t, db, orgID, owner.UserID, "demo")
	foreign := insertForkPluginPack(t, db, dbgen.Organization(t, db, database.Organization{}).ID, owner.UserID, "demo")
	want := []codersdk.ForkEnabledPluginPack{{ID: pack.ID, Name: pack.Name, CommitSHA: pack.CommitSha}}

	t.Run("Project", func(t *testing.T) {
		t.Parallel()
		project := dbgen.ChatProject(t, db, database.ChatProject{OrganizationID: orgID, OwnerID: memberUser.ID})

		// The member cannot read the pack but can enable it on their own
		// project, and enabling twice is a no-op.
		require.NoError(t, member.EnableForkProjectPluginPack(ctx, orgID, project.ID, pack.ID))
		require.NoError(t, member.EnableForkProjectPluginPack(ctx, orgID, project.ID, pack.ID))
		packs, err := member.ForkProjectPluginPacks(ctx, orgID, project.ID)
		require.NoError(t, err)
		require.Equal(t, want, packs)

		err = member.EnableForkProjectPluginPack(ctx, orgID, project.ID, foreign.ID)
		_ = requireSDKError(t, err, http.StatusNotFound)
		err = member.EnableForkProjectPluginPack(ctx, orgID, project.ID, uuid.New())
		_ = requireSDKError(t, err, http.StatusNotFound)
		err = other.EnableForkProjectPluginPack(ctx, orgID, project.ID, pack.ID)
		_ = requireSDKError(t, err, http.StatusNotFound)

		require.NoError(t, member.DisableForkProjectPluginPack(ctx, orgID, project.ID, pack.ID))
		packs, err = member.ForkProjectPluginPacks(ctx, orgID, project.ID)
		require.NoError(t, err)
		require.Empty(t, packs)
	})

	t.Run("Chat", func(t *testing.T) {
		t.Parallel()
		model := dbgen.ChatModelConfig(t, db, database.ChatModelConfig{OrganizationID: orgID})
		chat := dbgen.Chat(t, db, database.Chat{OrganizationID: orgID, OwnerID: memberUser.ID, LastModelConfigID: model.ID})

		require.NoError(t, member.EnableForkChatPluginPack(ctx, chat.ID, pack.ID))
		packs, err := member.ForkChatPluginPacks(ctx, chat.ID)
		require.NoError(t, err)
		require.Equal(t, want, packs)

		err = member.EnableForkChatPluginPack(ctx, chat.ID, foreign.ID)
		_ = requireSDKError(t, err, http.StatusNotFound)
		err = other.EnableForkChatPluginPack(ctx, chat.ID, pack.ID)
		_ = requireSDKError(t, err, http.StatusNotFound)

		require.NoError(t, member.DisableForkChatPluginPack(ctx, chat.ID, pack.ID))
		packs, err = member.ForkChatPluginPacks(ctx, chat.ID)
		require.NoError(t, err)
		require.Empty(t, packs)
	})
}

func insertForkPluginPack(t *testing.T, db database.Store, organizationID, createdBy uuid.UUID, name string) forkdb.PluginPack {
	t.Helper()
	fdb, err := forkdb.From(db)
	require.NoError(t, err)
	now := dbtime.Now()
	pack, err := fdb.InsertPluginPack(testutil.Context(t, testutil.WaitShort), forkdb.InsertPluginPackParams{
		ID:             uuid.New(),
		OrganizationID: organizationID,
		Name:           name,
		SourceUrl:      "https://example.com/packs.git",
		Ref:            "main",
		CommitSha:      "0123456789abcdef0123456789abcdef01234567",
		CreatedBy:      createdBy,
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	require.NoError(t, err)
	return pack
}
