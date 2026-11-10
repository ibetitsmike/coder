package chatd

// This file is fork code: it tests custom agent types stored for users,
// organizations and chat projects behind the fork-agent-definitions
// experiment.

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"cdr.dev/slog/v3/sloggers/slogtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/coderd/rbac/policy"
	"github.com/coder/coder/v2/codersdk"
)

func TestForkAgentDefinitionStoredTiers(t *testing.T) {
	t.Parallel()

	f := newAgentDefinitionsFixture(t,
		codersdk.Experiments{codersdk.ExperimentForkAgentDefinitions},
		func(database.ChatModelConfig) map[string]string {
			return map[string]string{
				"reviewer": agentDefinitionMarkdown("name: reviewer\ndescription: Workspace reviewer.", "WORKSPACE-REVIEWER-MARKER"),
			}
		})
	ctx := chatdTestContext(t)
	insertUser := func(name, body string) {
		_, err := f.fdb.InsertUserAgentDefinition(ctx, forkdb.InsertUserAgentDefinitionParams{
			OrganizationID: f.root.OrganizationID,
			UserID:         f.root.OwnerID,
			Name:           name,
			Description:    "Personal " + name + ".",
			Content:        agentDefinitionMarkdown("name: "+name, body),
		})
		require.NoError(t, err)
	}
	insertUser("reviewer", "PERSONAL-REVIEWER-MARKER")
	insertUser("planner", "PERSONAL-PLANNER-MARKER")
	insertUser("general", "SHADOW-GENERAL-MARKER")
	insertUser("retired", "RETIRED-MARKER")
	_, err := f.fdb.InsertUserAgentDefinition(ctx, forkdb.InsertUserAgentDefinitionParams{
		OrganizationID: f.root.OrganizationID,
		UserID:         f.root.OwnerID,
		Name:           "renamed",
		Content:        agentDefinitionMarkdown("name: general", "RENAMED-MARKER"),
	})
	require.NoError(t, err)
	_, err = f.fdb.UpdateUserAgentDefinitionByName(ctx, forkdb.UpdateUserAgentDefinitionByNameParams{
		OrganizationID: f.root.OrganizationID,
		UserID:         f.root.OwnerID,
		Name:           "retired",
		Enabled:        sql.NullBool{Bool: false, Valid: true},
	})
	require.NoError(t, err)
	_, err = f.fdb.InsertOrganizationAgentDefinition(ctx, forkdb.InsertOrganizationAgentDefinitionParams{
		OrganizationID: f.root.OrganizationID,
		Name:           "reviewer",
		Description:    "Organization reviewer.",
		Content:        agentDefinitionMarkdown("name: reviewer", "ORG-REVIEWER-MARKER"),
		GroupAcl:       database.ChatACL{f.root.OrganizationID.String(): {Permissions: []policy.Action{policy.ActionRead}}},
		UserAcl:        database.ChatACL{},
	})
	require.NoError(t, err)

	description, err := buildSpawnAgentDescription(ctx, f.server, f.root)
	require.NoError(t, err)
	for _, want := range []string{
		"personal/reviewer (Personal reviewer.)",
		"org/reviewer (Organization reviewer.)",
		"workspace/reviewer (Workspace reviewer.)",
		"planner (Personal planner.)",
	} {
		require.Contains(t, description, want)
	}
	require.NotContains(t, description, " reviewer (")
	require.NotContains(t, description, "retired")
	require.NotContains(t, description, "Personal general")

	_, err = f.spawn(t, spawnAgentArgs{Type: "reviewer"})
	require.ErrorContains(t, err, "type must be one of")
	_, err = f.spawn(t, spawnAgentArgs{Type: "retired"})
	require.ErrorContains(t, err, "type must be one of")
	_, err = f.spawn(t, spawnAgentArgs{Type: "renamed"})
	require.ErrorContains(t, err, `agent "renamed" content names "general"`)

	for agentType, marker := range map[string]string{
		"personal/reviewer":  "PERSONAL-REVIEWER-MARKER",
		"org/reviewer":       "ORG-REVIEWER-MARKER",
		"workspace/reviewer": "WORKSPACE-REVIEWER-MARKER",
		"planner":            "PERSONAL-PLANNER-MARKER",
		"personal/planner":   "PERSONAL-PLANNER-MARKER",
	} {
		child, err := f.spawn(t, spawnAgentArgs{Type: agentType})
		require.NoError(t, err, agentType)
		require.Contains(t, f.systemPrompt(t, child.ID), marker, agentType)
		row, err := f.fdb.GetChatSubagentByChatID(ctx, child.ID)
		require.NoError(t, err)
		require.Equal(t, agentType[strings.LastIndex(agentType, "/")+1:], row.AgentType, agentType)
	}
}

// TestForkAgentDefinitionStoredTiersAuthz checks that stored tiers are read
// as the chat owner, so organization membership and ACLs decide which
// stored types a chat sees.
func TestForkAgentDefinitionStoredTiersAuthz(t *testing.T) {
	t.Parallel()

	ctx := chatdTestContext(t)
	db, _ := dbtestutil.NewDB(t)
	authzDB := dbauthz.New(db, rbac.NewStrictCachingAuthorizer(prometheus.NewRegistry()), slogtest.Make(t, nil), nil)
	server := &Server{
		db:          authzDB,
		logger:      slogtest.Make(t, &slogtest.Options{IgnoreErrors: true}),
		experiments: codersdk.Experiments{codersdk.ExperimentForkAgentDefinitions},
		modelConfigContext: func(ctx context.Context, ownerID uuid.UUID) (context.Context, error) {
			return callerModelConfigContext(ctx, authzDB, ownerID)
		},
	}
	fdb, err := forkdb.From(db)
	require.NoError(t, err)
	org := dbgen.Organization(t, db, database.Organization{})
	member := dbgen.User(t, db, database.User{})
	other := dbgen.User(t, db, database.User{})
	outsider := dbgen.User(t, db, database.User{})
	for _, user := range []database.User{member, other} {
		dbgen.OrganizationMember(t, db, database.OrganizationMember{UserID: user.ID, OrganizationID: org.ID})
	}
	for name, user := range map[string]database.User{"member-type": member, "other-type": other, "outsider-type": outsider} {
		_, err := fdb.InsertUserAgentDefinition(ctx, forkdb.InsertUserAgentDefinitionParams{
			OrganizationID: org.ID,
			UserID:         user.ID,
			Name:           name,
			Content:        agentDefinitionMarkdown("name: "+name, "Mine."),
		})
		require.NoError(t, err)
	}
	for name, groupACL := range map[string]database.ChatACL{
		"shared":  {org.ID.String(): {Permissions: []policy.Action{policy.ActionRead}}},
		"private": {},
	} {
		_, err := fdb.InsertOrganizationAgentDefinition(ctx, forkdb.InsertOrganizationAgentDefinitionParams{
			OrganizationID: org.ID,
			Name:           name,
			Content:        agentDefinitionMarkdown("name: "+name, "Team."),
			GroupAcl:       groupACL,
			UserAcl:        database.ChatACL{},
		})
		require.NoError(t, err)
	}

	typeIDs := func(owner database.User) []string {
		var ids []string
		for _, def := range customSubagentDefinitions(ctx, server, database.Chat{ID: uuid.New(), OwnerID: owner.ID, OrganizationID: org.ID}) {
			ids = append(ids, def.id)
		}
		return ids
	}
	require.Equal(t, []string{"member-type", "shared"}, typeIDs(member))
	require.Empty(t, typeIDs(outsider))
}

func TestForkAgentDefinitionProjectTier(t *testing.T) {
	t.Parallel()

	f := newAgentDefinitionsFixture(t,
		codersdk.Experiments{codersdk.ExperimentForkAgentDefinitions},
		func(database.ChatModelConfig) map[string]string {
			return map[string]string{
				"reviewer": agentDefinitionMarkdown("name: reviewer\ndescription: Workspace reviewer.", "WORKSPACE-REVIEWER-MARKER"),
			}
		})
	ctx := chatdTestContext(t)
	project := dbgen.ChatProject(t, f.db, database.ChatProject{OrganizationID: f.root.OrganizationID, OwnerID: f.root.OwnerID})
	f.root.ProjectID = uuid.NullUUID{UUID: project.ID, Valid: true}
	require.NoError(t, f.db.UpdateChatProjectIDByID(ctx, database.UpdateChatProjectIDByIDParams{ProjectID: f.root.ProjectID, ID: f.root.ID}))
	for name, marker := range map[string]string{"reviewer": "PROJECT-REVIEWER-MARKER", "projbot": "PROJECT-BOT-MARKER"} {
		_, err := f.fdb.InsertProjectAgentDefinition(ctx, forkdb.InsertProjectAgentDefinitionParams{
			OrganizationID: project.OrganizationID,
			ProjectID:      project.ID,
			Name:           name,
			Description:    "Project " + name + ".",
			Content:        agentDefinitionMarkdown("name: "+name, marker),
		})
		require.NoError(t, err)
	}

	description, err := buildSpawnAgentDescription(ctx, f.server, f.root)
	require.NoError(t, err)
	for _, want := range []string{
		"project/reviewer (Project reviewer.)",
		"workspace/reviewer (Workspace reviewer.)",
		"projbot (Project projbot.)",
	} {
		require.Contains(t, description, want)
	}

	var child database.Chat
	for agentType, marker := range map[string]string{
		"project/reviewer": "PROJECT-REVIEWER-MARKER",
		"projbot":          "PROJECT-BOT-MARKER",
	} {
		child, err = f.spawn(t, spawnAgentArgs{Type: agentType})
		require.NoError(t, err, agentType)
		require.Contains(t, f.systemPrompt(t, child.ID), marker, agentType)
		row, err := f.fdb.GetChatSubagentByChatID(ctx, child.ID)
		require.NoError(t, err)
		require.Equal(t, agentType[strings.LastIndex(agentType, "/")+1:], row.AgentType, agentType)
	}

	// Subagent chats carry no project and inherit the root chat's.
	require.False(t, child.ProjectID.Valid)
	var childTypes []string
	for _, def := range customSubagentDefinitions(ctx, f.server, child) {
		childTypes = append(childTypes, def.id)
	}
	require.Contains(t, childTypes, "projbot")
}

// TestForkAgentDefinitionProjectTierUsable checks that a chat sees its
// project's types only when the chat owner can use the project, so site role
// grants do not expose them.
func TestForkAgentDefinitionProjectTierUsable(t *testing.T) {
	t.Parallel()

	ctx := chatdTestContext(t)
	db, _ := dbtestutil.NewDB(t)
	authzDB := dbauthz.New(db, rbac.NewStrictCachingAuthorizer(prometheus.NewRegistry()), slogtest.Make(t, nil), nil)
	server := &Server{
		db:          authzDB,
		logger:      slogtest.Make(t, &slogtest.Options{IgnoreErrors: true}),
		experiments: codersdk.Experiments{codersdk.ExperimentForkAgentDefinitions},
		modelConfigContext: func(ctx context.Context, ownerID uuid.UUID) (context.Context, error) {
			return callerModelConfigContext(ctx, authzDB, ownerID)
		},
	}
	fdb, err := forkdb.From(db)
	require.NoError(t, err)
	org := dbgen.Organization(t, db, database.Organization{})
	owner := dbgen.User(t, db, database.User{})
	shared := dbgen.User(t, db, database.User{})
	unshared := dbgen.User(t, db, database.User{})
	siteOwner := dbgen.User(t, db, database.User{RBACRoles: []string{rbac.RoleOwner().String()}})
	for _, user := range []database.User{owner, shared, unshared, siteOwner} {
		dbgen.OrganizationMember(t, db, database.OrganizationMember{UserID: user.ID, OrganizationID: org.ID})
	}
	project := dbgen.ChatProject(t, db, database.ChatProject{OrganizationID: org.ID, OwnerID: owner.ID})
	require.NoError(t, db.UpdateChatProjectACLByID(ctx, database.UpdateChatProjectACLByIDParams{
		UserACL:  database.ChatACL{shared.ID.String(): {Permissions: []policy.Action{policy.ActionRead}}},
		GroupACL: database.ChatACL{},
		ID:       project.ID,
	}))
	_, err = fdb.InsertProjectAgentDefinition(ctx, forkdb.InsertProjectAgentDefinitionParams{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Name:           "projbot",
		Content:        agentDefinitionMarkdown("name: projbot", "Project."),
	})
	require.NoError(t, err)

	typeIDs := func(user database.User) []string {
		var ids []string
		for _, def := range customSubagentDefinitions(ctx, server, database.Chat{
			ID:             uuid.New(),
			OwnerID:        user.ID,
			OrganizationID: org.ID,
			ProjectID:      uuid.NullUUID{UUID: project.ID, Valid: true},
		}) {
			ids = append(ids, def.id)
		}
		return ids
	}
	require.Equal(t, []string{"projbot"}, typeIDs(owner))
	require.Equal(t, []string{"projbot"}, typeIDs(shared))
	require.Empty(t, typeIDs(unshared))
	require.Empty(t, typeIDs(siteOwner))
}

func TestParseStoredAgentDefinitionDisabled(t *testing.T) {
	t.Parallel()

	_, err := parseStoredAgentDefinition(forkdb.AgentDefinition{
		Name:    "reviewer",
		Content: agentDefinitionMarkdown("name: reviewer", "Review."),
	})
	require.ErrorContains(t, err, `agent "reviewer" is disabled`)
}
