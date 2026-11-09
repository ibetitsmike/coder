package coderd_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/rbac"
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

func TestForkOrganizationAgentDefinitions(t *testing.T) {
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
		orgAdmin, _ := coderdtest.CreateAnotherUser(t, client, orgID, rbac.ScopedRoleOrgAdmin(orgID))
		member, _ := coderdtest.CreateAnotherUser(t, client, orgID)

		created, err := orgAdmin.CreateForkOrganizationAgentDefinition(ctx, orgID, codersdk.CreateForkAgentDefinitionRequest{Content: reviewer})
		require.NoError(t, err)
		require.Equal(t, "reviewer", created.Name)
		_, err = orgAdmin.CreateForkOrganizationAgentDefinition(ctx, orgID, codersdk.CreateForkAgentDefinitionRequest{Content: reviewer})
		_ = requireSDKError(t, err, http.StatusConflict)

		// The Everyone grant lets every member read a new definition.
		list, err := member.ForkOrganizationAgentDefinitions(ctx, orgID)
		require.NoError(t, err)
		require.Equal(t, []codersdk.ForkAgentDefinitionMetadata{created.ForkAgentDefinitionMetadata}, list)
		got, err := member.ForkOrganizationAgentDefinition(ctx, orgID, "reviewer")
		require.NoError(t, err)
		require.Equal(t, created, got)

		_, err = member.CreateForkOrganizationAgentDefinition(ctx, orgID, codersdk.CreateForkAgentDefinitionRequest{
			Content: "---\nname: other\n---\nDo things.\n",
		})
		_ = requireSDKError(t, err, http.StatusForbidden)
		_, err = member.UpdateForkOrganizationAgentDefinition(ctx, orgID, "reviewer", codersdk.UpdateForkAgentDefinitionRequest{Enabled: ptr.Ref(false)})
		_ = requireSDKError(t, err, http.StatusForbidden)
		err = member.DeleteForkOrganizationAgentDefinition(ctx, orgID, "reviewer")
		_ = requireSDKError(t, err, http.StatusForbidden)

		updated, err := orgAdmin.UpdateForkOrganizationAgentDefinition(ctx, orgID, "reviewer", codersdk.UpdateForkAgentDefinitionRequest{
			Content: ptr.Ref("---\nname: reviewer\ndescription: Reviews code\n---\nReview the code.\n"),
			Enabled: ptr.Ref(false),
		})
		require.NoError(t, err)
		require.Equal(t, "Reviews code", updated.Description)
		require.False(t, updated.Enabled)

		err = orgAdmin.DeleteForkOrganizationAgentDefinition(ctx, orgID, "reviewer")
		require.NoError(t, err)
		_, err = member.ForkOrganizationAgentDefinition(ctx, orgID, "reviewer")
		_ = requireSDKError(t, err, http.StatusNotFound)
		_, err = orgAdmin.UpdateForkOrganizationAgentDefinition(ctx, orgID, "reviewer", codersdk.UpdateForkAgentDefinitionRequest{Enabled: ptr.Ref(true)})
		_ = requireSDKError(t, err, http.StatusNotFound)
	})

	t.Run("ScopedAPIKeys", func(t *testing.T) {
		t.Parallel()

		ctx := testutil.Context(t, testutil.WaitLong)
		client, db := coderdtest.NewWithDatabase(t, &coderdtest.Options{
			DeploymentValues: coderdtest.DeploymentValues(t, func(dv *codersdk.DeploymentValues) {
				dv.Experiments = []string{string(codersdk.ExperimentForkAgentDefinitions)}
			}),
		})
		owner := coderdtest.CreateFirstUser(t, client)
		orgID := owner.OrganizationID
		scopedClient := func(scope database.APIKeyScope) *codersdk.Client {
			_, token := dbgen.APIKey(t, db, database.APIKey{
				UserID: owner.UserID,
				Scopes: database.APIKeyScopes{"organization:read", scope},
			})
			scoped := codersdk.New(client.URL)
			scoped.SetSessionToken(token)
			return scoped
		}
		_, err := client.CreateForkOrganizationAgentDefinition(ctx, orgID, codersdk.CreateForkAgentDefinitionRequest{Content: reviewer})
		require.NoError(t, err)

		_, err = scopedClient("organization_skill:share").ForkOrganizationAgentDefinitionACL(ctx, orgID, "reviewer")
		require.NoError(t, err)

		// A key that can neither read nor delete must not learn that the
		// definition exists.
		err = scopedClient("organization_skill:update").DeleteForkOrganizationAgentDefinition(ctx, orgID, "reviewer")
		_ = requireSDKError(t, err, http.StatusNotFound)

		deleter := scopedClient("organization_skill:delete")
		_, err = deleter.ForkOrganizationAgentDefinition(ctx, orgID, "reviewer")
		_ = requireSDKError(t, err, http.StatusNotFound)
		err = deleter.DeleteForkOrganizationAgentDefinition(ctx, orgID, "reviewer")
		require.NoError(t, err)
		_, err = client.ForkOrganizationAgentDefinition(ctx, orgID, "reviewer")
		_ = requireSDKError(t, err, http.StatusNotFound)
	})

	t.Run("ACL", func(t *testing.T) {
		t.Parallel()

		ctx := testutil.Context(t, testutil.WaitLong)
		client, db := coderdtest.NewWithDatabase(t, &coderdtest.Options{
			DeploymentValues: coderdtest.DeploymentValues(t, func(dv *codersdk.DeploymentValues) {
				dv.Experiments = []string{string(codersdk.ExperimentForkAgentDefinitions)}
			}),
		})
		owner := coderdtest.CreateFirstUser(t, client)
		orgID := owner.OrganizationID
		orgAdmin, _ := coderdtest.CreateAnotherUser(t, client, orgID, rbac.ScopedRoleOrgAdmin(orgID))
		grantee, granteeUser := coderdtest.CreateAnotherUser(t, client, orgID)
		member, _ := coderdtest.CreateAnotherUser(t, client, orgID)

		_, err := orgAdmin.CreateForkOrganizationAgentDefinition(ctx, orgID, codersdk.CreateForkAgentDefinitionRequest{Content: reviewer})
		require.NoError(t, err)
		err = orgAdmin.UpdateForkOrganizationAgentDefinitionACL(ctx, orgID, "reviewer", codersdk.UpdateOrganizationSkillACLRequest{
			UserRoles:  map[string]codersdk.OrganizationSkillRole{granteeUser.ID.String(): codersdk.OrganizationSkillRoleRead},
			GroupRoles: map[string]codersdk.OrganizationSkillRole{orgID.String(): codersdk.OrganizationSkillRoleDeleted},
		})
		require.NoError(t, err)
		acl, err := orgAdmin.ForkOrganizationAgentDefinitionACL(ctx, orgID, "reviewer")
		require.NoError(t, err)
		require.Empty(t, acl.Groups)
		require.Len(t, acl.Users, 1)
		require.Equal(t, granteeUser.ID, acl.Users[0].ID)

		_, err = grantee.ForkOrganizationAgentDefinition(ctx, orgID, "reviewer")
		require.NoError(t, err)
		_, err = member.ForkOrganizationAgentDefinition(ctx, orgID, "reviewer")
		_ = requireSDKError(t, err, http.StatusNotFound)
		list, err := member.ForkOrganizationAgentDefinitions(ctx, orgID)
		require.NoError(t, err)
		require.Empty(t, list)
		// Readers who cannot share see neither the grants nor the ACL route.
		_, err = grantee.ForkOrganizationAgentDefinitionACL(ctx, orgID, "reviewer")
		_ = requireSDKError(t, err, http.StatusNotFound)
		err = grantee.UpdateForkOrganizationAgentDefinitionACL(ctx, orgID, "reviewer", codersdk.UpdateOrganizationSkillACLRequest{
			UserRoles: map[string]codersdk.OrganizationSkillRole{granteeUser.ID.String(): codersdk.OrganizationSkillRoleDeleted},
		})
		_ = requireSDKError(t, err, http.StatusNotFound)
		err = orgAdmin.UpdateForkOrganizationAgentDefinitionACL(ctx, orgID, "reviewer", codersdk.UpdateOrganizationSkillACLRequest{
			UserRoles: map[string]codersdk.OrganizationSkillRole{"not-a-uuid": codersdk.OrganizationSkillRoleRead},
		})
		_ = requireSDKError(t, err, http.StatusBadRequest)

		// Leaving the organization drops the direct grant, so rejoining does
		// not restore it.
		require.NoError(t, client.DeleteOrganizationMember(ctx, orgID, granteeUser.ID.String()))
		_, err = grantee.ForkOrganizationAgentDefinition(ctx, orgID, "reviewer")
		_ = requireSDKError(t, err, http.StatusNotFound)
		fdb, err := forkdb.From(db)
		require.NoError(t, err)
		//nolint:gocritic // The test reads the stored ACL directly.
		row, err := fdb.GetOrganizationAgentDefinitionByName(dbauthz.AsSystemRestricted(ctx), forkdb.GetOrganizationAgentDefinitionByNameParams{
			OrganizationID: orgID,
			Name:           "reviewer",
		})
		require.NoError(t, err)
		require.NotContains(t, row.UserAcl, granteeUser.ID.String())
		_, err = client.PostOrganizationMember(ctx, orgID, granteeUser.ID.String())
		require.NoError(t, err)
		_, err = grantee.ForkOrganizationAgentDefinition(ctx, orgID, "reviewer")
		_ = requireSDKError(t, err, http.StatusNotFound)
	})

	t.Run("ExperimentOff", func(t *testing.T) {
		t.Parallel()

		ctx := testutil.Context(t, testutil.WaitLong)
		client := coderdtest.New(t, nil)
		owner := coderdtest.CreateFirstUser(t, client)

		_, err := client.ForkOrganizationAgentDefinitions(ctx, owner.OrganizationID)
		_ = requireSDKError(t, err, http.StatusForbidden)
	})
}

func TestForkProjectAgentDefinitions(t *testing.T) {
	t.Parallel()

	const reviewer = "---\nname: reviewer\ndescription: Reviews diffs\n---\nReview the diff.\n"

	t.Run("Enabled", func(t *testing.T) {
		t.Parallel()

		ctx := testutil.Context(t, testutil.WaitLong)
		client, db := newChatProjectClient(t, func(o *coderdtest.Options) {
			o.DeploymentValues.Experiments = append(o.DeploymentValues.Experiments, string(codersdk.ExperimentForkAgentDefinitions))
		})
		orgID := coderdtest.CreateFirstUser(t, client.Client).OrganizationID
		project := createChatProject(t, client, orgID, "Agent Definitions Project")

		created, err := client.CreateForkProjectAgentDefinition(ctx, orgID, project.ID, codersdk.CreateForkAgentDefinitionRequest{Content: reviewer})
		require.NoError(t, err)
		require.Equal(t, "reviewer", created.Name)
		require.Equal(t, "Reviews diffs", created.Description)
		require.Equal(t, orgID, created.OrganizationID)
		require.True(t, created.Enabled)
		_, err = client.CreateForkProjectAgentDefinition(ctx, orgID, project.ID, codersdk.CreateForkAgentDefinitionRequest{Content: reviewer})
		_ = requireSDKError(t, err, http.StatusConflict)

		list, err := client.ForkProjectAgentDefinitions(ctx, orgID, project.ID)
		require.NoError(t, err)
		require.Equal(t, []codersdk.ForkAgentDefinitionMetadata{created.ForkAgentDefinitionMetadata}, list)

		updated, err := client.UpdateForkProjectAgentDefinition(ctx, orgID, project.ID, "reviewer", codersdk.UpdateForkAgentDefinitionRequest{
			Content: ptr.Ref("---\nname: reviewer\ndescription: Reviews code\n---\nReview the code.\n"),
			Enabled: ptr.Ref(false),
		})
		require.NoError(t, err)
		require.Equal(t, "Reviews code", updated.Description)
		require.False(t, updated.Enabled)
		_, err = client.UpdateForkProjectAgentDefinition(ctx, orgID, project.ID, "reviewer", codersdk.UpdateForkAgentDefinitionRequest{
			Content: ptr.Ref("---\nname: renamed\n---\nReview the code.\n"),
		})
		_ = requireSDKError(t, err, http.StatusBadRequest)

		// The project is not visible through another organization's path.
		otherOrg := dbgen.Organization(t, db, database.Organization{})
		_, err = client.ForkProjectAgentDefinitions(ctx, otherOrg.ID, project.ID)
		_ = requireSDKError(t, err, http.StatusNotFound)

		memberRaw, memberUser := coderdtest.CreateAnotherUser(t, client.Client, orgID)
		member := codersdk.NewExperimentalClient(memberRaw)
		memberCalls := []func() error{
			func() error { _, err := member.ForkProjectAgentDefinitions(ctx, orgID, project.ID); return err },
			func() error {
				_, err := member.ForkProjectAgentDefinition(ctx, orgID, project.ID, "reviewer")
				return err
			},
			func() error {
				_, err := member.CreateForkProjectAgentDefinition(ctx, orgID, project.ID, codersdk.CreateForkAgentDefinitionRequest{Content: reviewer})
				return err
			},
			func() error {
				_, err := member.UpdateForkProjectAgentDefinition(ctx, orgID, project.ID, "reviewer", codersdk.UpdateForkAgentDefinitionRequest{Enabled: ptr.Ref(true)})
				return err
			},
			func() error { return member.DeleteForkProjectAgentDefinition(ctx, orgID, project.ID, "reviewer") },
		}
		// A member who cannot read the project gets 404 on every route.
		for i, call := range memberCalls {
			_ = requireSDKErrorStatus(t, call(), http.StatusNotFound, "member call %d", i)
		}

		// The use role reads the definitions but cannot change them.
		require.NoError(t, client.UpdateChatProjectACL(ctx, orgID, project.ID, codersdk.UpdateChatProjectACL{
			UserRoles: map[string]codersdk.ChatProjectRole{memberUser.ID.String(): codersdk.ChatProjectRoleUse},
		}))
		require.NoError(t, memberCalls[0]())
		got, err := member.ForkProjectAgentDefinition(ctx, orgID, project.ID, "reviewer")
		require.NoError(t, err)
		require.Equal(t, updated, got)
		for i, call := range memberCalls[2:] {
			_ = requireSDKErrorStatus(t, call(), http.StatusNotFound, "use call %d", i)
		}
		got, err = client.ForkProjectAgentDefinition(ctx, orgID, project.ID, "reviewer")
		require.NoError(t, err)
		require.Equal(t, updated, got)

		// The admin role can change them.
		require.NoError(t, client.UpdateChatProjectACL(ctx, orgID, project.ID, codersdk.UpdateChatProjectACL{
			UserRoles: map[string]codersdk.ChatProjectRole{memberUser.ID.String(): codersdk.ChatProjectRoleAdmin},
		}))
		_, err = member.UpdateForkProjectAgentDefinition(ctx, orgID, project.ID, "reviewer", codersdk.UpdateForkAgentDefinitionRequest{Enabled: ptr.Ref(true)})
		require.NoError(t, err)
		require.NoError(t, member.DeleteForkProjectAgentDefinition(ctx, orgID, project.ID, "reviewer"))
		_, err = client.ForkProjectAgentDefinition(ctx, orgID, project.ID, "reviewer")
		_ = requireSDKError(t, err, http.StatusNotFound)
	})

	t.Run("ExperimentOff", func(t *testing.T) {
		t.Parallel()

		ctx := testutil.Context(t, testutil.WaitLong)
		client, _ := newChatProjectClient(t)
		orgID := coderdtest.CreateFirstUser(t, client.Client).OrganizationID
		project := createChatProject(t, client, orgID, "Agent Definitions Project")

		_, err := client.ForkProjectAgentDefinitions(ctx, orgID, project.ID)
		_ = requireSDKError(t, err, http.StatusForbidden)
	})
}
