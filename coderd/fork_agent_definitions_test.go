package coderd_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/util/ptr"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
)

func TestForkUserAgentDefinitions(t *testing.T) {
	t.Parallel()

	const reviewer = "---\nname: reviewer\ndescription: Reviews diffs\n---\nReview the diff.\n"

	t.Run("Enabled", func(t *testing.T) {
		t.Parallel()

		ctx := testutil.Context(t, testutil.WaitLong)
		client := coderdtest.New(t, &coderdtest.Options{
			DeploymentValues: coderdtest.DeploymentValues(t, func(dv *codersdk.DeploymentValues) {
				dv.Experiments = []string{string(codersdk.ExperimentForkAgentDefinitions)}
			}),
		})
		owner := coderdtest.CreateFirstUser(t, client)
		orgID := owner.OrganizationID
		member, memberUser := coderdtest.CreateAnotherUser(t, client, orgID)
		other, _ := coderdtest.CreateAnotherUser(t, client, orgID)

		created, err := member.CreateForkUserAgentDefinition(ctx, orgID, codersdk.Me, codersdk.CreateForkAgentDefinitionRequest{Content: reviewer})
		require.NoError(t, err)
		require.Equal(t, "reviewer", created.Name)
		require.Equal(t, "Reviews diffs", created.Description)
		require.Equal(t, orgID, created.OrganizationID)
		require.True(t, created.Enabled)

		_, err = member.CreateForkUserAgentDefinition(ctx, orgID, codersdk.Me, codersdk.CreateForkAgentDefinitionRequest{Content: reviewer})
		_ = requireSDKError(t, err, http.StatusConflict)
		for _, content := range []string{
			"Review the diff.\n",
			"---\ndescription: no name\n---\nReview the diff.\n",
			"---\nname: reviewer\n---\n",
		} {
			_, err = member.CreateForkUserAgentDefinition(ctx, orgID, codersdk.Me, codersdk.CreateForkAgentDefinitionRequest{Content: content})
			_ = requireSDKError(t, err, http.StatusBadRequest)
		}

		list, err := member.ForkUserAgentDefinitions(ctx, orgID, memberUser.Username)
		require.NoError(t, err)
		require.Equal(t, []codersdk.ForkAgentDefinitionMetadata{created.ForkAgentDefinitionMetadata}, list)

		updated, err := member.UpdateForkUserAgentDefinition(ctx, orgID, codersdk.Me, "reviewer", codersdk.UpdateForkAgentDefinitionRequest{
			Content: ptr.Ref("---\nname: reviewer\ndescription: Reviews code\n---\nReview the code.\n"),
			Enabled: ptr.Ref(false),
		})
		require.NoError(t, err)
		require.Equal(t, "Reviews code", updated.Description)
		require.False(t, updated.Enabled)
		got, err := member.ForkUserAgentDefinition(ctx, orgID, codersdk.Me, "reviewer")
		require.NoError(t, err)
		require.Equal(t, updated, got)

		for _, req := range []codersdk.UpdateForkAgentDefinitionRequest{
			{},
			{Content: ptr.Ref("---\nname: renamed\n---\nReview the code.\n")},
		} {
			_, err = member.UpdateForkUserAgentDefinition(ctx, orgID, codersdk.Me, "reviewer", req)
			_ = requireSDKError(t, err, http.StatusBadRequest)
		}
		_, err = member.UpdateForkUserAgentDefinition(ctx, orgID, codersdk.Me, "missing", codersdk.UpdateForkAgentDefinitionRequest{Enabled: ptr.Ref(true)})
		_ = requireSDKError(t, err, http.StatusNotFound)

		// Another member can see the user in the organization but not the
		// user's definitions.
		_, err = other.ForkUserAgentDefinitions(ctx, orgID, memberUser.Username)
		_ = requireSDKError(t, err, http.StatusNotFound)
		_, err = other.ForkUserAgentDefinition(ctx, orgID, memberUser.Username, "reviewer")
		_ = requireSDKError(t, err, http.StatusNotFound)
		_, err = other.CreateForkUserAgentDefinition(ctx, orgID, memberUser.Username, codersdk.CreateForkAgentDefinitionRequest{Content: reviewer})
		_ = requireSDKError(t, err, http.StatusForbidden)
		_, err = other.UpdateForkUserAgentDefinition(ctx, orgID, memberUser.Username, "reviewer", codersdk.UpdateForkAgentDefinitionRequest{Enabled: ptr.Ref(true)})
		_ = requireSDKError(t, err, http.StatusForbidden)
		err = other.DeleteForkUserAgentDefinition(ctx, orgID, memberUser.Username, "reviewer")
		_ = requireSDKError(t, err, http.StatusForbidden)

		err = member.DeleteForkUserAgentDefinition(ctx, orgID, codersdk.Me, "reviewer")
		require.NoError(t, err)
		_, err = member.ForkUserAgentDefinition(ctx, orgID, codersdk.Me, "reviewer")
		_ = requireSDKError(t, err, http.StatusNotFound)
	})

	t.Run("ExperimentOff", func(t *testing.T) {
		t.Parallel()

		ctx := testutil.Context(t, testutil.WaitLong)
		client := coderdtest.New(t, nil)
		owner := coderdtest.CreateFirstUser(t, client)

		_, err := client.ForkUserAgentDefinitions(ctx, owner.OrganizationID, codersdk.Me)
		_ = requireSDKError(t, err, http.StatusForbidden)
	})
}
