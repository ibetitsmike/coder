package forkdb_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/sqlc-dev/pqtype"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/rbac/policy"
	"github.com/coder/coder/v2/testutil"
)

const savedWorkflowScript = "def main(args):\n    return args\n"

func TestSavedWorkflows(t *testing.T) {
	t.Parallel()

	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(t)
	fdb := mustFrom(t, db)
	org := dbgen.Organization(t, db, database.Organization{})
	otherOrg := dbgen.Organization(t, db, database.Organization{})

	insertUser := func(t *testing.T, orgID, userID uuid.UUID, name string) (forkdb.SavedWorkflow, error) {
		t.Helper()
		return fdb.InsertUserSavedWorkflow(testutil.Context(t, testutil.WaitShort), forkdb.InsertUserSavedWorkflowParams{
			OrganizationID: orgID,
			UserID:         userID,
			Name:           name,
			Script:         savedWorkflowScript,
		})
	}
	insertOrg := func(t *testing.T, orgID, createdBy uuid.UUID, name string) (forkdb.SavedWorkflow, error) {
		t.Helper()
		return fdb.InsertOrganizationSavedWorkflow(testutil.Context(t, testutil.WaitShort), forkdb.InsertOrganizationSavedWorkflowParams{
			OrganizationID: orgID,
			CreatedBy:      createdBy,
			Name:           name,
			Script:         savedWorkflowScript,
		})
	}

	t.Run("Defaults", func(t *testing.T) {
		t.Parallel()
		user := dbgen.User(t, db, database.User{})

		got, err := insertUser(t, org.ID, user.ID, "defaults")
		require.NoError(t, err)
		require.Equal(t, uuid.NullUUID{UUID: user.ID, Valid: true}, got.UserID)
		require.Equal(t, uuid.NullUUID{UUID: user.ID, Valid: true}, got.CreatedBy, "the saver of a user-tier row is its owner")
		require.Empty(t, got.Description)
		require.NotNil(t, got.Phases, "phases default to an empty list")
		require.Empty(t, got.Phases)
		require.False(t, got.ArgsSchema.Valid, "the args schema is optional")
		require.Empty(t, got.GroupAcl)
		require.Empty(t, got.UserAcl)

		got, err = insertOrg(t, org.ID, user.ID, "defaults")
		require.NoError(t, err)
		require.False(t, got.UserID.Valid, "the organization tier has no owner")
		require.Equal(t, uuid.NullUUID{UUID: user.ID, Valid: true}, got.CreatedBy)
		require.NotNil(t, got.Phases)
		require.Empty(t, got.Phases)
	})

	t.Run("NamesAreUniquePerOwner", func(t *testing.T) {
		t.Parallel()
		user := dbgen.User(t, db, database.User{})
		otherUser := dbgen.User(t, db, database.User{})

		_, err := insertUser(t, org.ID, user.ID, "deploy")
		require.NoError(t, err)
		_, err = insertUser(t, org.ID, user.ID, "deploy")
		require.True(t, database.IsUniqueViolation(err), "a user has one workflow per name in an organization: %v", err)

		_, err = insertUser(t, org.ID, otherUser.ID, "deploy")
		require.NoError(t, err, "another user")
		_, err = insertUser(t, otherOrg.ID, user.ID, "deploy")
		require.NoError(t, err, "another organization")
		_, err = insertOrg(t, org.ID, user.ID, "unique-deploy")
		require.NoError(t, err, "the organization tier")
		_, err = insertOrg(t, org.ID, otherUser.ID, "unique-deploy")
		require.True(t, database.IsUniqueViolation(err), "an organization has one workflow per name: %v", err)
	})

	t.Run("CheckConstraints", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitShort)
		user := dbgen.User(t, db, database.User{})

		_, err := insertUser(t, org.ID, user.ID, "Bad_Name")
		require.True(t, database.IsCheckViolation(err, "saved_workflows_name_format"), "name format: %v", err)

		_, err = fdb.InsertUserSavedWorkflow(ctx, forkdb.InsertUserSavedWorkflowParams{
			OrganizationID: org.ID, UserID: user.ID, Name: "big", Script: strings.Repeat("x", 65537),
		})
		require.True(t, database.IsCheckViolation(err, "saved_workflows_script_size"), "script size: %v", err)

		_, err = fdb.InsertUserSavedWorkflow(ctx, forkdb.InsertUserSavedWorkflowParams{
			OrganizationID: org.ID, UserID: user.ID, Name: "array-schema", Script: savedWorkflowScript,
			ArgsSchema: pqtype.NullRawMessage{RawMessage: json.RawMessage(`[]`), Valid: true},
		})
		require.True(t, database.IsCheckViolation(err, "saved_workflows_args_schema_is_object"), "args schema type: %v", err)

		phases := make([]string, 33)
		for i := range phases {
			phases[i] = fmt.Sprintf("phase-%d", i)
		}
		_, err = fdb.InsertUserSavedWorkflow(ctx, forkdb.InsertUserSavedWorkflowParams{
			OrganizationID: org.ID, UserID: user.ID, Name: "many-phases", Script: savedWorkflowScript, Phases: phases,
		})
		require.True(t, database.IsCheckViolation(err, "saved_workflows_phases_size"), "phase count: %v", err)

		_, err = sqlDB.ExecContext(ctx, `INSERT INTO saved_workflows (organization_id, user_id, name, script, user_acl) VALUES ($1, $2, 'shared', '', $3)`, org.ID, user.ID, `{"`+uuid.NewString()+`":{"permissions":["read"]}}`)
		require.True(t, database.IsCheckViolation(err, "saved_workflows_acl_only_on_organization_tier"), "ACL on a user-tier row: %v", err)
	})

	t.Run("PerOwnerLimit", func(t *testing.T) {
		t.Parallel()
		user := dbgen.User(t, db, database.User{})
		limitOrg := dbgen.Organization(t, db, database.Organization{})

		for i := range 100 {
			_, err := insertUser(t, limitOrg.ID, user.ID, fmt.Sprintf("workflow-%d", i))
			require.NoError(t, err)
		}
		_, err := insertUser(t, limitOrg.ID, user.ID, "one-too-many")
		require.True(t, database.IsCheckViolation(err, "saved_workflows_per_user_limit"), "user limit: %v", err)
		_, err = insertUser(t, org.ID, user.ID, "one-too-many")
		require.NoError(t, err, "the user limit counts per organization")

		for i := range 100 {
			_, err := insertOrg(t, limitOrg.ID, user.ID, fmt.Sprintf("workflow-%d", i))
			require.NoError(t, err, "user-tier rows do not count toward the organization limit")
		}
		_, err = insertOrg(t, limitOrg.ID, user.ID, "one-too-many")
		require.True(t, database.IsCheckViolation(err, "saved_workflows_per_organization_limit"), "organization limit: %v", err)
	})

	t.Run("UserDelete", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitShort)
		user := dbgen.User(t, db, database.User{})
		_, err := insertUser(t, org.ID, user.ID, "personal")
		require.NoError(t, err)
		_, err = insertOrg(t, org.ID, user.ID, "saved-by-deleted-user")
		require.NoError(t, err)

		_, err = sqlDB.ExecContext(ctx, `DELETE FROM user_status_changes WHERE user_id = $1`, user.ID)
		require.NoError(t, err)
		_, err = sqlDB.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, user.ID)
		require.NoError(t, err)

		_, err = fdb.GetUserSavedWorkflowByName(ctx, forkdb.GetUserSavedWorkflowByNameParams{OrganizationID: org.ID, UserID: user.ID, Name: "personal"})
		require.ErrorIs(t, err, sql.ErrNoRows, "the user tier goes with its owner")
		shared, err := fdb.GetOrganizationSavedWorkflowByName(ctx, forkdb.GetOrganizationSavedWorkflowByNameParams{OrganizationID: org.ID, Name: "saved-by-deleted-user"})
		require.NoError(t, err, "the organization tier stays")
		require.False(t, shared.CreatedBy.Valid)
	})

	t.Run("UpdateAndList", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitShort)
		user := dbgen.User(t, db, database.User{})
		listOrg := dbgen.Organization(t, db, database.Organization{})
		_, err := insertUser(t, listOrg.ID, user.ID, "writer")
		require.NoError(t, err)
		inserted, err := fdb.InsertUserSavedWorkflow(ctx, forkdb.InsertUserSavedWorkflowParams{
			OrganizationID: listOrg.ID,
			UserID:         user.ID,
			Name:           "deploy",
			Description:    "Deploy the service.",
			Script:         savedWorkflowScript,
			ArgsSchema:     pqtype.NullRawMessage{RawMessage: json.RawMessage(`{"type":"object"}`), Valid: true},
			Phases:         []string{"build", "ship"},
		})
		require.NoError(t, err)
		require.Equal(t, []string{"build", "ship"}, inserted.Phases)
		_, err = insertUser(t, org.ID, user.ID, "deploy")
		require.NoError(t, err)

		// A partial update keeps the fields it does not set.
		updated, err := fdb.UpdateUserSavedWorkflowByName(ctx, forkdb.UpdateUserSavedWorkflowByNameParams{
			OrganizationID: listOrg.ID,
			UserID:         user.ID,
			Name:           "deploy",
			Script:         sql.NullString{String: "def main(args):\n    return 1\n", Valid: true},
		})
		require.NoError(t, err)
		require.Equal(t, "def main(args):\n    return 1\n", updated.Script)
		require.Equal(t, "Deploy the service.", updated.Description)
		require.JSONEq(t, `{"type":"object"}`, string(updated.ArgsSchema.RawMessage))
		require.Equal(t, []string{"build", "ship"}, updated.Phases)
		require.True(t, updated.UpdatedAt.After(inserted.UpdatedAt), "update moves updated_at")

		// SetArgsSchema with a null schema clears it, and an empty list clears the phases.
		updated, err = fdb.UpdateUserSavedWorkflowByName(ctx, forkdb.UpdateUserSavedWorkflowByNameParams{
			OrganizationID: listOrg.ID,
			UserID:         user.ID,
			Name:           "deploy",
			SetArgsSchema:  true,
			Phases:         []string{},
		})
		require.NoError(t, err)
		require.False(t, updated.ArgsSchema.Valid)
		require.Empty(t, updated.Phases)

		other, err := fdb.GetUserSavedWorkflowByName(ctx, forkdb.GetUserSavedWorkflowByNameParams{OrganizationID: org.ID, UserID: user.ID, Name: "deploy"})
		require.NoError(t, err)
		require.Equal(t, savedWorkflowScript, other.Script, "the update stays in its organization")

		rows, err := fdb.ListUserSavedWorkflowMetadata(ctx, forkdb.ListUserSavedWorkflowMetadataParams{OrganizationID: listOrg.ID, UserID: user.ID})
		require.NoError(t, err)
		require.Len(t, rows, 2)
		require.Equal(t, "deploy", rows[0].Name)
		require.Equal(t, "writer", rows[1].Name)

		// Organization-tier queries ignore user-tier rows of the same name.
		_, err = fdb.GetOrganizationSavedWorkflowByName(ctx, forkdb.GetOrganizationSavedWorkflowByNameParams{OrganizationID: listOrg.ID, Name: "deploy"})
		require.ErrorIs(t, err, sql.ErrNoRows)
		orgRows, err := fdb.ListOrganizationSavedWorkflowMetadata(ctx, listOrg.ID)
		require.NoError(t, err)
		require.Empty(t, orgRows)
		_, err = fdb.UpdateOrganizationSavedWorkflowByName(ctx, forkdb.UpdateOrganizationSavedWorkflowByNameParams{OrganizationID: listOrg.ID, Name: "deploy", Script: sql.NullString{String: "overwritten", Valid: true}})
		require.ErrorIs(t, err, sql.ErrNoRows)
		_, err = fdb.UpdateOrganizationSavedWorkflowACLByName(ctx, forkdb.UpdateOrganizationSavedWorkflowACLByNameParams{OrganizationID: listOrg.ID, Name: "deploy"})
		require.ErrorIs(t, err, sql.ErrNoRows)
		_, err = fdb.DeleteOrganizationSavedWorkflowByName(ctx, forkdb.DeleteOrganizationSavedWorkflowByNameParams{OrganizationID: listOrg.ID, Name: "deploy"})
		require.ErrorIs(t, err, sql.ErrNoRows)

		_, err = fdb.DeleteUserSavedWorkflowByName(ctx, forkdb.DeleteUserSavedWorkflowByNameParams{OrganizationID: listOrg.ID, UserID: user.ID, Name: "deploy"})
		require.NoError(t, err)
		_, err = fdb.GetUserSavedWorkflowByName(ctx, forkdb.GetUserSavedWorkflowByNameParams{OrganizationID: org.ID, UserID: user.ID, Name: "deploy"})
		require.NoError(t, err, "the delete stays in its organization")
	})

	t.Run("OrganizationACL", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitShort)
		admin := dbgen.User(t, db, database.User{})
		member := dbgen.User(t, db, database.User{})
		aclOrg := dbgen.Organization(t, db, database.Organization{})
		dbgen.OrganizationMember(t, db, database.OrganizationMember{OrganizationID: aclOrg.ID, UserID: member.ID})
		_, err := insertOrg(t, aclOrg.ID, admin.ID, "shared")
		require.NoError(t, err)

		read := database.ChatACLEntry{Permissions: []policy.Action{policy.ActionRead}}
		updated, err := fdb.UpdateOrganizationSavedWorkflowACLByName(ctx, forkdb.UpdateOrganizationSavedWorkflowACLByNameParams{
			OrganizationID: aclOrg.ID,
			Name:           "shared",
			GroupAcl:       database.ChatACL{aclOrg.ID.String(): read},
			UserAcl:        database.ChatACL{member.ID.String(): read},
		})
		require.NoError(t, err)
		require.Contains(t, updated.UserAcl, member.ID.String())

		rows, err := fdb.ListOrganizationSavedWorkflowMetadata(ctx, aclOrg.ID)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Contains(t, rows[0].GroupAcl, aclOrg.ID.String())
		require.Contains(t, rows[0].UserAcl, member.ID.String())

		// Leaving the organization drops the member's direct grants.
		_, err = sqlDB.ExecContext(ctx, `DELETE FROM organization_members WHERE organization_id = $1 AND user_id = $2`, aclOrg.ID, member.ID)
		require.NoError(t, err)
		got, err := fdb.GetOrganizationSavedWorkflowByNameForUpdate(ctx, forkdb.GetOrganizationSavedWorkflowByNameForUpdateParams{OrganizationID: aclOrg.ID, Name: "shared"})
		require.NoError(t, err)
		require.NotContains(t, got.UserAcl, member.ID.String())
		require.Contains(t, got.GroupAcl, aclOrg.ID.String(), "group grants stay")

		_, err = fdb.DeleteOrganizationSavedWorkflowByName(ctx, forkdb.DeleteOrganizationSavedWorkflowByNameParams{OrganizationID: aclOrg.ID, Name: "shared"})
		require.NoError(t, err)
		_, err = fdb.GetOrganizationSavedWorkflowByName(ctx, forkdb.GetOrganizationSavedWorkflowByNameParams{OrganizationID: aclOrg.ID, Name: "shared"})
		require.ErrorIs(t, err, sql.ErrNoRows)
	})
}
