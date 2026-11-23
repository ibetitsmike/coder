package coderd_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/coderd/util/ptr"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
)

// TestForkSavedWorkflowPermissions checks that every workflow a list returns
// carries the caller's own actions on it, and that each action it reports
// matches what the matching handler does for that caller.
//
//nolint:tparallel // The subtests share the workflows and grants set up once.
func TestForkSavedWorkflowPermissions(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	client := coderdtest.New(t, &coderdtest.Options{
		DeploymentValues: coderdtest.DeploymentValues(t, func(dv *codersdk.DeploymentValues) {
			dv.Experiments = []string{string(codersdk.ExperimentForkWorkflows)}
		}),
	})
	owner := coderdtest.CreateFirstUser(t, client)
	orgID := owner.OrganizationID
	orgAdmin, _ := coderdtest.CreateAnotherUser(t, client, orgID, rbac.ScopedRoleOrgAdmin(orgID))
	author, authorUser := coderdtest.CreateAnotherUser(t, client, orgID)
	reader, _ := coderdtest.CreateAnotherUser(t, client, orgID)

	var (
		all      = codersdk.ForkSavedWorkflowPermissions{Update: true, Delete: true, Share: true}
		editOnly = codersdk.ForkSavedWorkflowPermissions{Update: true, Delete: true}
		none     = codersdk.ForkSavedWorkflowPermissions{}
	)

	// listed returns the caller's permissions on the named workflow from a
	// list, so the test covers the list the page renders.
	listed := func(t *testing.T, list []codersdk.ForkSavedWorkflowMetadata, err error, name string) codersdk.ForkSavedWorkflowPermissions {
		t.Helper()
		require.NoError(t, err)
		for _, workflow := range list {
			if workflow.Name == name {
				return workflow.Permissions
			}
		}
		require.Failf(t, "workflow not listed", "%q", name)
		return none
	}
	// requireAllowed checks a handler outcome against a reported action.
	requireAllowed := func(t *testing.T, allowed bool, err error) {
		t.Helper()
		if allowed {
			require.NoError(t, err)
			return
		}
		require.Error(t, err)
	}
	updatePersonal := func(c *codersdk.Client, name string) error {
		_, err := c.UpdateForkUserSavedWorkflow(ctx, orgID, authorUser.ID.String(), name, codersdk.UpdateForkSavedWorkflowRequest{Description: ptr.Ref("same")})
		return err
	}
	updateOrg := func(c *codersdk.Client, name string) error {
		_, err := c.UpdateForkOrganizationSavedWorkflow(ctx, orgID, name, codersdk.UpdateForkSavedWorkflowRequest{Description: ptr.Ref("same")})
		return err
	}
	readACL := func(c *codersdk.Client, name string) error {
		_, err := c.ForkOrganizationSavedWorkflowACL(ctx, orgID, name)
		return err
	}

	created, err := author.CreateForkUserSavedWorkflow(ctx, orgID, codersdk.Me, codersdk.CreateForkSavedWorkflowRequest{Name: "mine", Script: savedWorkflowScript})
	require.NoError(t, err)
	require.Equal(t, all, created.Permissions, "the create response carries the creator's actions")
	_, err = author.CreateForkUserSavedWorkflow(ctx, orgID, codersdk.Me, codersdk.CreateForkSavedWorkflowRequest{Name: "triage", Script: savedWorkflowScript})
	require.NoError(t, err)
	release, err := orgAdmin.CreateForkOrganizationSavedWorkflow(ctx, orgID, codersdk.CreateForkSavedWorkflowRequest{Name: "release", Script: savedWorkflowScript})
	require.NoError(t, err)
	require.Equal(t, all, release.Permissions)

	t.Run("PersonalOwner", func(t *testing.T) { //nolint:paralleltest // subtests share parent state
		list, err := author.ForkUserSavedWorkflows(ctx, orgID, codersdk.Me)
		require.Equal(t, all, listed(t, list, err, "mine"))
		got, err := author.ForkUserSavedWorkflow(ctx, orgID, codersdk.Me, "mine")
		require.NoError(t, err)
		require.Equal(t, all, got.Permissions)
		requireAllowed(t, true, updatePersonal(author, "mine"))
	})

	// A site owner may delete a member's personal workflow, but not edit it,
	// and the share route admits only the owner.
	t.Run("PersonalSiteOwner", func(t *testing.T) { //nolint:paralleltest // subtests share parent state
		list, err := client.ForkUserSavedWorkflows(ctx, orgID, authorUser.ID.String())
		perms := listed(t, list, err, "mine")
		require.Equal(t, codersdk.ForkSavedWorkflowPermissions{Delete: true}, perms)
		requireAllowed(t, perms.Update, updatePersonal(client, "mine"))
		_, err = client.ShareForkUserSavedWorkflow(ctx, orgID, authorUser.ID.String(), "mine")
		requireAllowed(t, perms.Share, err)
		requireAllowed(t, perms.Delete, client.DeleteForkUserSavedWorkflow(ctx, orgID, authorUser.ID.String(), "mine"))
	})

	t.Run("OrganizationMember", func(t *testing.T) { //nolint:paralleltest // subtests share parent state
		list, err := reader.ForkOrganizationSavedWorkflows(ctx, orgID)
		perms := listed(t, list, err, "release")
		require.Equal(t, none, perms)
		got, err := reader.ForkOrganizationSavedWorkflow(ctx, orgID, "release")
		require.NoError(t, err)
		require.Equal(t, none, got.Permissions)
		requireAllowed(t, perms.Update, updateOrg(reader, "release"))
		requireAllowed(t, perms.Delete, reader.DeleteForkOrganizationSavedWorkflow(ctx, orgID, "release"))
		requireAllowed(t, perms.Share, readACL(reader, "release"))
	})

	t.Run("OrganizationAdmins", func(t *testing.T) { //nolint:paralleltest // subtests share parent state
		for _, c := range []*codersdk.Client{orgAdmin, client} {
			list, err := c.ForkOrganizationSavedWorkflows(ctx, orgID)
			perms := listed(t, list, err, "release")
			require.Equal(t, all, perms)
			requireAllowed(t, perms.Update, updateOrg(c, "release"))
			requireAllowed(t, perms.Share, readACL(c, "release"))
		}
	})

	// Sharing grants the author edit and delete through a user ACL, which the
	// list must read from the loaded row. The author cannot manage access.
	// Revoking the grant leaves the author a reader.
	t.Run("SharedThenRevoked", func(t *testing.T) { //nolint:paralleltest // subtests share parent state
		shared, err := author.ShareForkUserSavedWorkflow(ctx, orgID, codersdk.Me, "triage")
		require.NoError(t, err)
		require.Equal(t, editOnly, shared.Permissions, "the share response carries the author's new actions")

		list, err := author.ForkOrganizationSavedWorkflows(ctx, orgID)
		perms := listed(t, list, err, "triage")
		require.Equal(t, editOnly, perms)
		require.Equal(t, none, listed(t, list, err, "release"))
		updated, err := author.UpdateForkOrganizationSavedWorkflow(ctx, orgID, "triage", codersdk.UpdateForkSavedWorkflowRequest{Description: ptr.Ref("same")})
		require.NoError(t, err)
		require.Equal(t, editOnly, updated.Permissions, "the update response carries the caller's actions")
		requireAllowed(t, perms.Share, readACL(author, "triage"))

		list, err = reader.ForkOrganizationSavedWorkflows(ctx, orgID)
		require.Equal(t, none, listed(t, list, err, "triage"))

		err = orgAdmin.UpdateForkOrganizationSavedWorkflowACL(ctx, orgID, "triage", codersdk.UpdateOrganizationSkillACLRequest{
			UserRoles: map[string]codersdk.OrganizationSkillRole{authorUser.ID.String(): codersdk.OrganizationSkillRoleDeleted},
		})
		require.NoError(t, err)
		list, err = author.ForkOrganizationSavedWorkflows(ctx, orgID)
		perms = listed(t, list, err, "triage")
		require.Equal(t, none, perms)
		requireAllowed(t, perms.Update, updateOrg(author, "triage"))
		requireAllowed(t, perms.Delete, author.DeleteForkOrganizationSavedWorkflow(ctx, orgID, "triage"))
	})
}
