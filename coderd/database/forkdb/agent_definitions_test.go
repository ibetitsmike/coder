package forkdb_test

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/testutil"
)

func TestAgentDefinitions(t *testing.T) {
	t.Parallel()

	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(t)
	fdb := mustFrom(t, db)
	org := dbgen.Organization(t, db, database.Organization{})
	otherOrg := dbgen.Organization(t, db, database.Organization{})

	insertUser := func(t *testing.T, orgID, userID uuid.UUID, name string) (forkdb.AgentDefinition, error) {
		t.Helper()
		return fdb.InsertUserAgentDefinition(testutil.Context(t, testutil.WaitShort), forkdb.InsertUserAgentDefinitionParams{
			OrganizationID: orgID,
			UserID:         userID,
			Name:           name,
			Content:        "Review the change.",
		})
	}

	t.Run("NamesAreUniquePerOwner", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitShort)
		user := dbgen.User(t, db, database.User{})
		otherUser := dbgen.User(t, db, database.User{})

		_, err := insertUser(t, org.ID, user.ID, "reviewer")
		require.NoError(t, err)
		_, err = insertUser(t, org.ID, user.ID, "reviewer")
		require.True(t, database.IsUniqueViolation(err), "a user has one definition per name in an organization: %v", err)

		_, err = insertUser(t, org.ID, otherUser.ID, "reviewer")
		require.NoError(t, err, "another user")
		_, err = insertUser(t, otherOrg.ID, user.ID, "reviewer")
		require.NoError(t, err, "another organization")
		_, err = fdb.InsertOrganizationAgentDefinition(ctx, forkdb.InsertOrganizationAgentDefinitionParams{OrganizationID: org.ID, Name: "reviewer", Content: "Review the change."})
		require.NoError(t, err, "the organization tier")
		_, err = fdb.InsertOrganizationAgentDefinition(ctx, forkdb.InsertOrganizationAgentDefinitionParams{OrganizationID: org.ID, Name: "reviewer", Content: "Review the change."})
		require.True(t, database.IsUniqueViolation(err), "an organization has one definition per name: %v", err)
	})

	t.Run("CheckConstraints", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitShort)
		user := dbgen.User(t, db, database.User{})
		project := dbgen.ChatProject(t, db, database.ChatProject{OrganizationID: org.ID, OwnerID: user.ID})

		_, err := insertUser(t, org.ID, user.ID, "Bad_Name")
		require.True(t, database.IsCheckViolation(err, "agent_definitions_name_format"), "name format: %v", err)

		_, err = sqlDB.ExecContext(ctx, `INSERT INTO agent_definitions (organization_id, user_id, project_id, name, content) VALUES ($1, $2, $3, 'both', '')`, org.ID, user.ID, project.ID)
		require.True(t, database.IsCheckViolation(err, "agent_definitions_single_tier"), "user and project tier at once: %v", err)

		_, err = sqlDB.ExecContext(ctx, `INSERT INTO agent_definitions (organization_id, user_id, name, content, user_acl) VALUES ($1, $2, 'shared', '', $3)`, org.ID, user.ID, `{"`+uuid.NewString()+`":{"permissions":["read"]}}`)
		require.True(t, database.IsCheckViolation(err, "agent_definitions_acl_only_on_organization_tier"), "ACL on a user-tier row: %v", err)
	})

	t.Run("PerOwnerLimit", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitLong)
		user := dbgen.User(t, db, database.User{})
		limitOrg := dbgen.Organization(t, db, database.Organization{})
		insertOrg := func(name string) error {
			_, err := fdb.InsertOrganizationAgentDefinition(ctx, forkdb.InsertOrganizationAgentDefinitionParams{OrganizationID: limitOrg.ID, Name: name, Content: "Review the change."})
			return err
		}

		for i := range 100 {
			_, err := insertUser(t, limitOrg.ID, user.ID, fmt.Sprintf("agent-%d", i))
			require.NoError(t, err)
		}
		_, err := insertUser(t, limitOrg.ID, user.ID, "one-too-many")
		require.True(t, database.IsCheckViolation(err, "agent_definitions_per_user_limit"), "user limit: %v", err)
		_, err = insertUser(t, org.ID, user.ID, "one-too-many")
		require.NoError(t, err, "the user limit counts per organization")

		for i := range 100 {
			require.NoError(t, insertOrg(fmt.Sprintf("agent-%d", i)), "user-tier rows do not count toward the organization limit")
		}
		err = insertOrg("one-too-many")
		require.True(t, database.IsCheckViolation(err, "agent_definitions_per_organization_limit"), "organization limit: %v", err)
	})

	t.Run("UserDeleteCascades", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitShort)
		user := dbgen.User(t, db, database.User{})
		_, err := insertUser(t, org.ID, user.ID, "reviewer")
		require.NoError(t, err)

		_, err = sqlDB.ExecContext(ctx, `DELETE FROM user_status_changes WHERE user_id = $1`, user.ID)
		require.NoError(t, err)
		_, err = sqlDB.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, user.ID)
		require.NoError(t, err)
		_, err = fdb.GetUserAgentDefinitionByName(ctx, forkdb.GetUserAgentDefinitionByNameParams{OrganizationID: org.ID, UserID: user.ID, Name: "reviewer"})
		require.ErrorIs(t, err, sql.ErrNoRows)
	})

	t.Run("UpdateAndList", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitShort)
		user := dbgen.User(t, db, database.User{})
		listOrg := dbgen.Organization(t, db, database.Organization{})
		_, err := insertUser(t, listOrg.ID, user.ID, "writer")
		require.NoError(t, err)
		inserted, err := insertUser(t, listOrg.ID, user.ID, "reviewer")
		require.NoError(t, err)
		_, err = insertUser(t, org.ID, user.ID, "reviewer")
		require.NoError(t, err)
		getOtherOrg := func() (forkdb.AgentDefinition, error) {
			return fdb.GetUserAgentDefinitionByName(ctx, forkdb.GetUserAgentDefinitionByNameParams{OrganizationID: org.ID, UserID: user.ID, Name: "reviewer"})
		}

		updated, err := fdb.UpdateUserAgentDefinitionByName(ctx, forkdb.UpdateUserAgentDefinitionByNameParams{
			OrganizationID: listOrg.ID,
			UserID:         user.ID,
			Name:           "reviewer",
			Content:        sql.NullString{String: "Review it again.", Valid: true},
		})
		require.NoError(t, err)
		require.Equal(t, "Review it again.", updated.Content)
		require.True(t, updated.UpdatedAt.After(inserted.UpdatedAt), "update moves updated_at")
		other, err := getOtherOrg()
		require.NoError(t, err)
		require.Equal(t, "Review the change.", other.Content, "the update stays in its organization")

		rows, err := fdb.ListUserAgentDefinitionMetadata(ctx, forkdb.ListUserAgentDefinitionMetadataParams{OrganizationID: listOrg.ID, UserID: user.ID})
		require.NoError(t, err)
		require.Len(t, rows, 2)
		require.Equal(t, "reviewer", rows[0].Name)
		require.Equal(t, "writer", rows[1].Name)

		// Organization-tier queries ignore user-tier rows of the same name.
		_, err = fdb.GetOrganizationAgentDefinitionByName(ctx, forkdb.GetOrganizationAgentDefinitionByNameParams{OrganizationID: listOrg.ID, Name: "reviewer"})
		require.ErrorIs(t, err, sql.ErrNoRows)
		orgRows, err := fdb.ListOrganizationAgentDefinitionMetadata(ctx, listOrg.ID)
		require.NoError(t, err)
		require.Empty(t, orgRows)
		_, err = fdb.UpdateOrganizationAgentDefinitionByName(ctx, forkdb.UpdateOrganizationAgentDefinitionByNameParams{OrganizationID: listOrg.ID, Name: "reviewer", Content: sql.NullString{String: "Overwritten.", Valid: true}})
		require.ErrorIs(t, err, sql.ErrNoRows)
		_, err = fdb.DeleteOrganizationAgentDefinitionByName(ctx, forkdb.DeleteOrganizationAgentDefinitionByNameParams{OrganizationID: listOrg.ID, Name: "reviewer"})
		require.ErrorIs(t, err, sql.ErrNoRows)

		_, err = fdb.DeleteUserAgentDefinitionByName(ctx, forkdb.DeleteUserAgentDefinitionByNameParams{OrganizationID: listOrg.ID, UserID: user.ID, Name: "reviewer"})
		require.NoError(t, err)
		_, err = getOtherOrg()
		require.NoError(t, err, "the delete stays in its organization")
	})
}
