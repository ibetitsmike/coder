package coderd_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/coderd/util/ptr"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
)

func TestForkShareUserSavedWorkflow(t *testing.T) {
	t.Parallel()

	setup := func(t *testing.T) (*codersdk.Client, uuid.UUID) {
		client := coderdtest.New(t, &coderdtest.Options{
			DeploymentValues: coderdtest.DeploymentValues(t, func(dv *codersdk.DeploymentValues) {
				dv.Experiments = []string{string(codersdk.ExperimentForkWorkflows)}
			}),
		})
		owner := coderdtest.CreateFirstUser(t, client)
		return client, owner.OrganizationID
	}

	t.Run("OwnerEditsOthersRead", func(t *testing.T) {
		t.Parallel()

		ctx := testutil.Context(t, testutil.WaitLong)
		client, orgID := setup(t)
		author, authorUser := coderdtest.CreateAnotherUser(t, client, orgID)
		reader, _ := coderdtest.CreateAnotherUser(t, client, orgID)

		personal, err := author.CreateForkUserSavedWorkflow(ctx, orgID, codersdk.Me, codersdk.CreateForkSavedWorkflowRequest{
			Name:        "triage",
			Description: "Triage new issues.",
			Script:      savedWorkflowScript,
			ArgsSchema:  json.RawMessage(`{"type":"object"}`),
			Phases:      []string{"collect", "label"},
		})
		require.NoError(t, err)

		// Another member cannot share a personal workflow. The denial comes
		// before any row is read, so it says nothing about whether the
		// workflow exists.
		_, err = reader.ShareForkUserSavedWorkflow(ctx, orgID, authorUser.ID.String(), "triage")
		_ = requireSDKError(t, err, http.StatusForbidden)
		_, err = reader.ShareForkUserSavedWorkflow(ctx, orgID, authorUser.ID.String(), "missing")
		_ = requireSDKError(t, err, http.StatusForbidden)
		_, err = author.ForkUserSavedWorkflow(ctx, orgID, codersdk.Me, "triage")
		require.NoError(t, err, "a refused share keeps the personal workflow")

		shared, err := author.ShareForkUserSavedWorkflow(ctx, orgID, codersdk.Me, "triage")
		require.NoError(t, err)
		require.Nil(t, shared.UserID, "a shared workflow lives in the organization tier")
		require.Equal(t, &authorUser.ID, shared.CreatedBy)
		require.NotEqual(t, personal.ID, shared.ID)
		require.Equal(t, personal.Name, shared.Name)
		require.Equal(t, personal.Description, shared.Description)
		require.Equal(t, personal.Script, shared.Script)
		require.JSONEq(t, string(personal.ArgsSchema), string(shared.ArgsSchema))
		require.Equal(t, personal.Phases, shared.Phases)

		// Sharing moves the workflow, so no personal copy shadows it.
		mine, err := author.ForkUserSavedWorkflows(ctx, orgID, codersdk.Me)
		require.NoError(t, err)
		require.Empty(t, mine)
		_, err = author.ShareForkUserSavedWorkflow(ctx, orgID, codersdk.Me, "triage")
		_ = requireSDKError(t, err, http.StatusNotFound)

		// A second member reads it but cannot change or delete it.
		require.Equal(t, codersdk.ForkSavedWorkflowPermissions{Update: true, Delete: true}, shared.Permissions)
		asReader := shared.ForkSavedWorkflowMetadata
		asReader.Permissions = codersdk.ForkSavedWorkflowPermissions{}
		list, err := reader.ForkOrganizationSavedWorkflows(ctx, orgID)
		require.NoError(t, err)
		require.Len(t, list, 1)
		requireSavedWorkflowMetadata(t, asReader, list[0])
		got, err := reader.ForkOrganizationSavedWorkflow(ctx, orgID, "triage")
		require.NoError(t, err)
		requireSavedWorkflowMetadata(t, asReader, got.ForkSavedWorkflowMetadata)
		require.Equal(t, shared.Script, got.Script)
		_, err = reader.UpdateForkOrganizationSavedWorkflow(ctx, orgID, "triage", codersdk.UpdateForkSavedWorkflowRequest{Description: ptr.Ref("hijacked")})
		_ = requireSDKError(t, err, http.StatusForbidden)
		_ = requireSDKError(t, reader.DeleteForkOrganizationSavedWorkflow(ctx, orgID, "triage"), http.StatusForbidden)
		_, err = reader.ForkOrganizationSavedWorkflowACL(ctx, orgID, "triage")
		_ = requireSDKError(t, err, http.StatusNotFound)

		// The author still edits it, and cannot grant others access.
		updated, err := author.UpdateForkOrganizationSavedWorkflow(ctx, orgID, "triage", codersdk.UpdateForkSavedWorkflowRequest{Description: ptr.Ref("Triage and label.")})
		require.NoError(t, err)
		require.Equal(t, "Triage and label.", updated.Description)
		_, err = author.ForkOrganizationSavedWorkflowACL(ctx, orgID, "triage")
		_ = requireSDKError(t, err, http.StatusNotFound)

		require.NoError(t, author.DeleteForkOrganizationSavedWorkflow(ctx, orgID, "triage"))
		_, err = reader.ForkOrganizationSavedWorkflow(ctx, orgID, "triage")
		_ = requireSDKError(t, err, http.StatusNotFound)
	})

	// The negative control: the author's edit rights come from the user
	// grant that sharing writes. Once an admin revokes it, the author reads
	// the workflow through the Everyone group like any member and gets 403.
	t.Run("RevokedGrantRemovesEdit", func(t *testing.T) {
		t.Parallel()

		ctx := testutil.Context(t, testutil.WaitLong)
		client, orgID := setup(t)
		orgAdmin, _ := coderdtest.CreateAnotherUser(t, client, orgID, rbac.ScopedRoleOrgAdmin(orgID))
		author, authorUser := coderdtest.CreateAnotherUser(t, client, orgID)

		_, err := author.CreateForkUserSavedWorkflow(ctx, orgID, codersdk.Me, codersdk.CreateForkSavedWorkflowRequest{Name: "triage", Script: savedWorkflowScript})
		require.NoError(t, err)
		_, err = author.ShareForkUserSavedWorkflow(ctx, orgID, codersdk.Me, "triage")
		require.NoError(t, err)

		acl, err := orgAdmin.ForkOrganizationSavedWorkflowACL(ctx, orgID, "triage")
		require.NoError(t, err)
		require.Len(t, acl.Users, 1)
		require.Equal(t, authorUser.ID, acl.Users[0].ID)
		require.Len(t, acl.Groups, 1)
		require.Equal(t, orgID, acl.Groups[0].ID, "a shared workflow reaches Everyone")

		err = orgAdmin.UpdateForkOrganizationSavedWorkflowACL(ctx, orgID, "triage", codersdk.UpdateOrganizationSkillACLRequest{
			UserRoles: map[string]codersdk.OrganizationSkillRole{authorUser.ID.String(): codersdk.OrganizationSkillRoleDeleted},
		})
		require.NoError(t, err)
		_, err = author.ForkOrganizationSavedWorkflow(ctx, orgID, "triage")
		require.NoError(t, err)
		_, err = author.UpdateForkOrganizationSavedWorkflow(ctx, orgID, "triage", codersdk.UpdateForkSavedWorkflowRequest{Description: ptr.Ref("changed")})
		_ = requireSDKError(t, err, http.StatusForbidden)
		_ = requireSDKError(t, author.DeleteForkOrganizationSavedWorkflow(ctx, orgID, "triage"), http.StatusForbidden)
	})

	// A site owner may delete any member's personal workflow, but it must not
	// publish one under the member's name.
	t.Run("AdminCannotShareForMember", func(t *testing.T) {
		t.Parallel()

		ctx := testutil.Context(t, testutil.WaitLong)
		client, orgID := setup(t)
		author, authorUser := coderdtest.CreateAnotherUser(t, client, orgID)

		personal, err := author.CreateForkUserSavedWorkflow(ctx, orgID, codersdk.Me, codersdk.CreateForkSavedWorkflowRequest{Name: "triage", Script: savedWorkflowScript})
		require.NoError(t, err)

		_, err = client.ShareForkUserSavedWorkflow(ctx, orgID, authorUser.ID.String(), "triage")
		_ = requireSDKError(t, err, http.StatusForbidden)
		got, err := author.ForkUserSavedWorkflow(ctx, orgID, codersdk.Me, "triage")
		require.NoError(t, err)
		require.Equal(t, personal, got, "a refused share keeps the personal workflow")
		_, err = client.ForkOrganizationSavedWorkflow(ctx, orgID, "triage")
		_ = requireSDKError(t, err, http.StatusNotFound)
	})

	t.Run("NameTakenKeepsPersonal", func(t *testing.T) {
		t.Parallel()

		ctx := testutil.Context(t, testutil.WaitLong)
		client, orgID := setup(t)
		author, _ := coderdtest.CreateAnotherUser(t, client, orgID)

		existing, err := client.CreateForkOrganizationSavedWorkflow(ctx, orgID, codersdk.CreateForkSavedWorkflowRequest{Name: "release", Script: savedWorkflowScript})
		require.NoError(t, err)
		personal, err := author.CreateForkUserSavedWorkflow(ctx, orgID, codersdk.Me, codersdk.CreateForkSavedWorkflowRequest{Name: "release", Description: "Mine.", Script: savedWorkflowScript})
		require.NoError(t, err)

		_, err = author.ShareForkUserSavedWorkflow(ctx, orgID, codersdk.Me, "release")
		sdkErr := requireSDKError(t, err, http.StatusConflict)
		require.Equal(t, "The organization already has a workflow with this name.", sdkErr.Message)
		got, err := author.ForkUserSavedWorkflow(ctx, orgID, codersdk.Me, "release")
		require.NoError(t, err)
		require.Equal(t, personal, got, "a failed share rolls back the move")
		org, err := author.ForkOrganizationSavedWorkflow(ctx, orgID, "release")
		require.NoError(t, err)
		asAuthor := existing
		asAuthor.Permissions = codersdk.ForkSavedWorkflowPermissions{}
		require.Equal(t, asAuthor, org)
	})

	t.Run("ExpectID", func(t *testing.T) {
		t.Parallel()

		ctx := testutil.Context(t, testutil.WaitLong)
		client, orgID := setup(t)
		author, _ := coderdtest.CreateAnotherUser(t, client, orgID)

		personal, err := author.CreateForkUserSavedWorkflow(ctx, orgID, codersdk.Me, codersdk.CreateForkSavedWorkflowRequest{Name: "triage", Script: savedWorkflowScript})
		require.NoError(t, err)

		_, err = author.ShareForkUserSavedWorkflow(ctx, orgID, codersdk.Me, "triage", codersdk.ForkSavedWorkflowWriteParams{ExpectID: ptr.Ref(uuid.New())})
		_ = requireSDKError(t, err, http.StatusConflict)
		_, err = author.ForkUserSavedWorkflow(ctx, orgID, codersdk.Me, "triage")
		require.NoError(t, err, "a stale share keeps the personal workflow")
		_, err = author.ForkOrganizationSavedWorkflow(ctx, orgID, "triage")
		_ = requireSDKError(t, err, http.StatusNotFound)

		shared, err := author.ShareForkUserSavedWorkflow(ctx, orgID, codersdk.Me, "triage", codersdk.ForkSavedWorkflowWriteParams{ExpectID: ptr.Ref(personal.ID)})
		require.NoError(t, err)
		require.Equal(t, "triage", shared.Name)
	})

	t.Run("ExperimentOff", func(t *testing.T) {
		t.Parallel()

		ctx := testutil.Context(t, testutil.WaitLong)
		client := coderdtest.New(t, nil)
		owner := coderdtest.CreateFirstUser(t, client)
		_, err := client.ShareForkUserSavedWorkflow(ctx, owner.OrganizationID, codersdk.Me, "triage")
		_ = requireSDKError(t, err, http.StatusForbidden)
	})
}
