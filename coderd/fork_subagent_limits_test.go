package coderd_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/coderd/util/ptr"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
)

func TestForkChatOrganizationSubagentLimits(t *testing.T) {
	t.Parallel()

	t.Run("Enabled", func(t *testing.T) {
		t.Parallel()

		ctx := testutil.Context(t, testutil.WaitLong)
		client := coderdtest.New(t, &coderdtest.Options{
			DeploymentValues: coderdtest.DeploymentValues(t, func(dv *codersdk.DeploymentValues) {
				dv.Experiments = []string{string(codersdk.ExperimentForkNestedSubagents)}
			}),
		})
		owner := coderdtest.CreateFirstUser(t, client)
		orgID := owner.OrganizationID
		orgAdmin, _ := coderdtest.CreateAnotherUser(t, client, orgID, rbac.ScopedRoleOrgAdmin(orgID))
		member, _ := coderdtest.CreateAnotherUser(t, client, orgID)
		auditor, _ := coderdtest.CreateAnotherUser(t, client, orgID, rbac.ScopedRoleOrgAuditor(orgID))

		got, err := orgAdmin.ForkOrganizationSubagentLimits(ctx, orgID)
		require.NoError(t, err)
		require.Equal(t, codersdk.ForkOrganizationSubagentLimits{}, got, "an organization without limits uses the deployment values")

		want := codersdk.ForkOrganizationSubagentLimits{
			MaxDepth:               ptr.Ref[int32](3),
			MaxConcurrent:          ptr.Ref[int32](10),
			MaxConcurrentPerFamily: ptr.Ref[int32](4),
			MaxLivePerFamily:       ptr.Ref[int32](20),
		}
		updated, err := orgAdmin.UpdateForkOrganizationSubagentLimits(ctx, orgID, want)
		require.NoError(t, err)
		require.Equal(t, want, updated)
		got, err = orgAdmin.ForkOrganizationSubagentLimits(ctx, orgID)
		require.NoError(t, err)
		require.Equal(t, want, got)

		for _, invalid := range []codersdk.ForkOrganizationSubagentLimits{
			{MaxDepth: ptr.Ref[int32](0)},
			{MaxDepth: ptr.Ref[int32](17)},
			{MaxConcurrent: ptr.Ref[int32](0)},
			{MaxConcurrentPerFamily: ptr.Ref[int32](0)},
			{MaxLivePerFamily: ptr.Ref[int32](0)},
		} {
			_, err = orgAdmin.UpdateForkOrganizationSubagentLimits(ctx, orgID, invalid)
			require.Len(t, requireSDKError(t, err, http.StatusBadRequest).Validations, 1)
		}

		_, err = member.ForkOrganizationSubagentLimits(ctx, orgID)
		_ = requireSDKError(t, err, http.StatusNotFound)
		_, err = member.UpdateForkOrganizationSubagentLimits(ctx, orgID, codersdk.ForkOrganizationSubagentLimits{})
		_ = requireSDKError(t, err, http.StatusNotFound)

		got, err = auditor.ForkOrganizationSubagentLimits(ctx, orgID)
		require.NoError(t, err)
		require.Equal(t, want, got)
		_, err = auditor.UpdateForkOrganizationSubagentLimits(ctx, orgID, codersdk.ForkOrganizationSubagentLimits{})
		_ = requireSDKError(t, err, http.StatusForbidden)

		got, err = orgAdmin.ForkOrganizationSubagentLimits(ctx, orgID)
		require.NoError(t, err)
		require.Equal(t, want, got, "rejected writes change nothing")
	})

	t.Run("ExperimentOff", func(t *testing.T) {
		t.Parallel()

		ctx := testutil.Context(t, testutil.WaitLong)
		client := coderdtest.New(t, nil)
		owner := coderdtest.CreateFirstUser(t, client)

		_, err := client.ForkOrganizationSubagentLimits(ctx, owner.OrganizationID)
		_ = requireSDKError(t, err, http.StatusForbidden)
		_, err = client.UpdateForkOrganizationSubagentLimits(ctx, owner.OrganizationID, codersdk.ForkOrganizationSubagentLimits{})
		_ = requireSDKError(t, err, http.StatusForbidden)
	})
}
