package coderd_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
)

func TestForkChatOrganizationTokenBudget(t *testing.T) {
	t.Parallel()

	t.Run("Enabled", func(t *testing.T) {
		t.Parallel()

		ctx := testutil.Context(t, testutil.WaitLong)
		client := coderdtest.New(t, &coderdtest.Options{
			DeploymentValues: coderdtest.DeploymentValues(t, func(dv *codersdk.DeploymentValues) {
				dv.Experiments = []string{string(codersdk.ExperimentForkTokenBudget)}
			}),
		})
		owner := coderdtest.CreateFirstUser(t, client)
		orgID := owner.OrganizationID
		orgAdmin, _ := coderdtest.CreateAnotherUser(t, client, orgID, rbac.ScopedRoleOrgAdmin(orgID))
		member, _ := coderdtest.CreateAnotherUser(t, client, orgID)
		auditor, _ := coderdtest.CreateAnotherUser(t, client, orgID, rbac.ScopedRoleOrgAuditor(orgID))

		got, err := orgAdmin.ForkOrganizationTokenBudget(ctx, orgID)
		require.NoError(t, err)
		require.Equal(t, codersdk.ForkOrganizationTokenBudget{
			Strategy: codersdk.ForkTokenBudgetStrategySummary,
			Fallback: codersdk.ForkTokenBudgetFallbackNewWindow,
		}, got, "an organization without a setting uses the defaults")

		want := codersdk.ForkOrganizationTokenBudget{
			Strategy: codersdk.ForkTokenBudgetStrategyTokenBudget,
			Fallback: codersdk.ForkTokenBudgetFallbackSummary,
		}
		updated, err := orgAdmin.UpdateForkOrganizationTokenBudget(ctx, orgID, want)
		require.NoError(t, err)
		require.Equal(t, want, updated)
		got, err = orgAdmin.ForkOrganizationTokenBudget(ctx, orgID)
		require.NoError(t, err)
		require.Equal(t, want, got)

		for _, tc := range []struct {
			setting     codersdk.ForkOrganizationTokenBudget
			validations int
		}{
			{codersdk.ForkOrganizationTokenBudget{Strategy: "windows", Fallback: codersdk.ForkTokenBudgetFallbackSummary}, 1},
			{codersdk.ForkOrganizationTokenBudget{Strategy: codersdk.ForkTokenBudgetStrategySummary, Fallback: "drop"}, 1},
			{codersdk.ForkOrganizationTokenBudget{}, 2},
		} {
			_, err = orgAdmin.UpdateForkOrganizationTokenBudget(ctx, orgID, tc.setting)
			require.Len(t, requireSDKError(t, err, http.StatusBadRequest).Validations, tc.validations)
		}

		_, err = member.ForkOrganizationTokenBudget(ctx, orgID)
		_ = requireSDKError(t, err, http.StatusNotFound)
		_, err = member.UpdateForkOrganizationTokenBudget(ctx, orgID, want)
		_ = requireSDKError(t, err, http.StatusNotFound)

		got, err = auditor.ForkOrganizationTokenBudget(ctx, orgID)
		require.NoError(t, err)
		require.Equal(t, want, got)
		_, err = auditor.UpdateForkOrganizationTokenBudget(ctx, orgID, codersdk.DefaultForkOrganizationTokenBudget())
		_ = requireSDKError(t, err, http.StatusForbidden)

		got, err = orgAdmin.ForkOrganizationTokenBudget(ctx, orgID)
		require.NoError(t, err)
		require.Equal(t, want, got, "rejected writes change nothing")
	})

	t.Run("ExperimentOff", func(t *testing.T) {
		t.Parallel()

		ctx := testutil.Context(t, testutil.WaitLong)
		client := coderdtest.New(t, nil)
		owner := coderdtest.CreateFirstUser(t, client)

		_, err := client.ForkOrganizationTokenBudget(ctx, owner.OrganizationID)
		_ = requireSDKError(t, err, http.StatusForbidden)
		_, err = client.UpdateForkOrganizationTokenBudget(ctx, owner.OrganizationID, codersdk.DefaultForkOrganizationTokenBudget())
		_ = requireSDKError(t, err, http.StatusForbidden)
	})
}
