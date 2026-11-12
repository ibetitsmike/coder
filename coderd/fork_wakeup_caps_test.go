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

func TestForkChatOrganizationWakeupCaps(t *testing.T) {
	t.Parallel()

	defaults := codersdk.ForkWakeupCapValues{
		MaxChatRunsPerDay:                288,
		MaxOrganizationRunsPerDay:        2880,
		MaxChatSpendMicrosPerDay:         20_000_000,
		MaxOrganizationSpendMicrosPerDay: 200_000_000,
	}

	t.Run("Enabled", func(t *testing.T) {
		t.Parallel()

		ctx := testutil.Context(t, testutil.WaitLong)
		client := coderdtest.New(t, &coderdtest.Options{
			DeploymentValues: coderdtest.DeploymentValues(t, func(dv *codersdk.DeploymentValues) {
				dv.Experiments = []string{string(codersdk.ExperimentForkChatWakeups)}
			}),
		})
		owner := coderdtest.CreateFirstUser(t, client)
		orgID := owner.OrganizationID
		orgAdmin, _ := coderdtest.CreateAnotherUser(t, client, orgID, rbac.ScopedRoleOrgAdmin(orgID))
		member, _ := coderdtest.CreateAnotherUser(t, client, orgID)
		auditor, _ := coderdtest.CreateAnotherUser(t, client, orgID, rbac.ScopedRoleOrgAuditor(orgID))

		got, err := orgAdmin.ForkOrganizationWakeupCaps(ctx, orgID)
		require.NoError(t, err)
		require.Equal(t, codersdk.ForkOrganizationWakeupCapsResponse{Default: defaults, Effective: defaults}, got,
			"an organization without caps uses the defaults")

		// One cap is raised, one lowered, and the others keep the defaults.
		caps := codersdk.ForkOrganizationWakeupCaps{
			MaxChatRunsPerDay:        ptr.Ref[int32](1440),
			MaxChatSpendMicrosPerDay: ptr.Ref[int64](1_500_000),
		}
		want := codersdk.ForkOrganizationWakeupCapsResponse{
			Organization: caps,
			Default:      defaults,
			Effective: codersdk.ForkWakeupCapValues{
				MaxChatRunsPerDay:                1440,
				MaxOrganizationRunsPerDay:        2880,
				MaxChatSpendMicrosPerDay:         1_500_000,
				MaxOrganizationSpendMicrosPerDay: 200_000_000,
			},
		}
		updated, err := orgAdmin.UpdateForkOrganizationWakeupCaps(ctx, orgID, caps)
		require.NoError(t, err)
		require.Equal(t, want, updated)
		got, err = orgAdmin.ForkOrganizationWakeupCaps(ctx, orgID)
		require.NoError(t, err)
		require.Equal(t, want, got)

		for _, invalid := range []codersdk.ForkOrganizationWakeupCaps{
			{MaxChatRunsPerDay: ptr.Ref[int32](0)},
			{MaxOrganizationRunsPerDay: ptr.Ref[int32](-1)},
			{MaxChatSpendMicrosPerDay: ptr.Ref[int64](0)},
			{MaxOrganizationSpendMicrosPerDay: ptr.Ref[int64](0)},
		} {
			_, err = orgAdmin.UpdateForkOrganizationWakeupCaps(ctx, orgID, invalid)
			require.Len(t, requireSDKError(t, err, http.StatusBadRequest).Validations, 1)
		}

		_, err = member.ForkOrganizationWakeupCaps(ctx, orgID)
		_ = requireSDKError(t, err, http.StatusNotFound)
		_, err = member.UpdateForkOrganizationWakeupCaps(ctx, orgID, codersdk.ForkOrganizationWakeupCaps{})
		_ = requireSDKError(t, err, http.StatusNotFound)

		got, err = auditor.ForkOrganizationWakeupCaps(ctx, orgID)
		require.NoError(t, err)
		require.Equal(t, want, got)
		_, err = auditor.UpdateForkOrganizationWakeupCaps(ctx, orgID, codersdk.ForkOrganizationWakeupCaps{})
		_ = requireSDKError(t, err, http.StatusForbidden)

		got, err = orgAdmin.ForkOrganizationWakeupCaps(ctx, orgID)
		require.NoError(t, err)
		require.Equal(t, want, got, "rejected writes change nothing")
	})

	t.Run("ExperimentOff", func(t *testing.T) {
		t.Parallel()

		ctx := testutil.Context(t, testutil.WaitLong)
		client := coderdtest.New(t, nil)
		owner := coderdtest.CreateFirstUser(t, client)

		_, err := client.ForkOrganizationWakeupCaps(ctx, owner.OrganizationID)
		_ = requireSDKError(t, err, http.StatusForbidden)
		_, err = client.UpdateForkOrganizationWakeupCaps(ctx, owner.OrganizationID, codersdk.ForkOrganizationWakeupCaps{})
		_ = requireSDKError(t, err, http.StatusForbidden)
	})
}
