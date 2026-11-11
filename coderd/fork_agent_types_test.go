package coderd_test

import (
	"maps"
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"

	agentproto "github.com/coder/coder/v2/agent/proto"
	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/dbfake"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/coderd/x/chatd/agentselect"
	"github.com/coder/coder/v2/codersdk"
	sdkproto "github.com/coder/coder/v2/provisionersdk/proto"
	"github.com/coder/coder/v2/testutil"
)

func TestForkAgentTypes(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	client, db, _ := newChatClientWithAPIAndDatabase(t, withChatWorkerDisabled, func(o *coderdtest.Options) {
		o.DeploymentValues.Experiments = append(o.DeploymentValues.Experiments, string(codersdk.ExperimentForkAgentDefinitions))
	})
	user := coderdtest.CreateFirstUser(t, client.Client)
	defaultModel := createChatModel(t, client)
	_ = createAdditionalChatModel(t, client, "openai", "gpt-4.1")
	memberRaw, _ := coderdtest.CreateAnotherUser(t, client.Client, user.OrganizationID)
	memberClient := codersdk.NewExperimentalClient(memberRaw)

	for _, content := range []string{
		"---\nname: reviewer\ndescription: Personal reviewer.\nmodel: gpt-4.1\nbase_type: explore\n---\nReview.\n",
		"---\nname: lost\ndescription: Lost.\nmodel: nope\n---\nLost.\n",
	} {
		_, err := client.CreateForkUserAgentDefinition(ctx, user.OrganizationID, codersdk.Me, codersdk.CreateForkAgentDefinitionRequest{Content: content})
		require.NoError(t, err)
	}
	for _, content := range []string{
		"---\nname: reviewer\ndescription: Org reviewer.\n---\nReview.\n",
		"---\nname: private\ndescription: Private.\n---\nPrivate.\n",
	} {
		_, err := client.CreateForkOrganizationAgentDefinition(ctx, user.OrganizationID, codersdk.CreateForkAgentDefinitionRequest{Content: content})
		require.NoError(t, err)
	}
	require.NoError(t, client.UpdateForkOrganizationAgentDefinitionACL(ctx, user.OrganizationID, "private", codersdk.UpdateOrganizationSkillACLRequest{
		GroupRoles: map[string]codersdk.OrganizationSkillRole{user.OrganizationID.String(): codersdk.OrganizationSkillRoleDeleted},
	}))

	workspace := dbfake.WorkspaceBuild(t, db, database.WorkspaceTable{
		OrganizationID: user.OrganizationID,
		OwnerID:        user.UserID,
	}).WithAgent().Do()
	//nolint:gocritic // The test seeds the workspace agent's context directly.
	sysCtx := dbauthz.AsSystemRestricted(ctx)
	now := dbtime.Now()
	_, err := db.UpsertWorkspaceAgentContextSnapshot(sysCtx, database.UpsertWorkspaceAgentContextSnapshotParams{
		WorkspaceAgentID: workspace.Agents[0].ID,
		Version:          1,
		AggregateHash:    []byte("agent-types"),
		ReceivedAt:       now,
	})
	require.NoError(t, err)
	definition := "---\nname: helper\ndescription: Workspace helper.\n---\nHelp.\n"
	body, err := protojson.Marshal(&agentproto.SubagentBody{Definition: []byte(definition), Name: "helper"})
	require.NoError(t, err)
	_, err = db.UpsertWorkspaceAgentContextResource(sysCtx, database.UpsertWorkspaceAgentContextResourceParams{
		WorkspaceAgentID: workspace.Agents[0].ID,
		Source:           "/home/coder/.claude/agents/helper.md",
		BodyKind:         database.WorkspaceAgentContextBodyKindSubagent,
		Body:             body,
		ContentHash:      []byte("helper"),
		SizeBytes:        int64(len(definition)),
		Status:           database.WorkspaceAgentContextResourceStatusOk,
		Now:              now,
	})
	require.NoError(t, err)

	byName := func(c *codersdk.ExperimentalClient, req codersdk.ForkAgentTypesRequest) map[string]codersdk.ForkAgentType {
		types, err := c.ForkAgentTypes(ctx, user.OrganizationID, req)
		require.NoError(t, err)
		out := map[string]codersdk.ForkAgentType{}
		for _, agentType := range types {
			out[agentType.Name] = agentType
		}
		require.Len(t, out, len(types))
		return out
	}

	got := byName(client, codersdk.ForkAgentTypesRequest{WorkspaceID: workspace.Workspace.ID})
	// The test model supports computer use, so spawn_agent offers it.
	require.Equal(t, `type "computer_use" cannot start a chat`, got["computer_use"].RootUnavailableReason)
	for _, name := range []string{"general", "explore", "computer_use"} {
		require.Equal(t, codersdk.ForkAgentTypeSourceBuiltin, got[name].Source, name)
		require.Equal(t, name, got[name].BaseType, name)
		require.NotEmpty(t, got[name].Description, name)
		require.Equal(t, name != "computer_use", got[name].RootAvailable, name)
		delete(got, name)
	}
	lost := got["lost"]
	require.Contains(t, lost.RootUnavailableReason, `sets model "nope", which is not an available model`)
	lost.RootUnavailableReason = ""
	got["lost"] = lost
	helper := got["helper"]
	require.Contains(t, helper.RootUnavailableReason, "only be used through spawn_agent")
	helper.RootUnavailableReason = ""
	got["helper"] = helper
	require.Equal(t, map[string]codersdk.ForkAgentType{
		"personal/reviewer": {Name: "personal/reviewer", Source: "personal", Description: "Personal reviewer.", Model: "gpt-4.1", BaseType: "explore", RootAvailable: true},
		"org/reviewer":      {Name: "org/reviewer", Source: "org", Description: "Org reviewer.", BaseType: "general", RootAvailable: true},
		"private":           {Name: "private", Source: "org", Description: "Private.", BaseType: "general", RootAvailable: true},
		"lost":              {Name: "lost", Source: "personal", Description: "Lost.", Model: "nope", BaseType: "general"},
		"helper":            {Name: "helper", Source: "workspace", Description: "Workspace helper.", BaseType: "general"},
	}, got)

	// An alias of two providers resolves on the default model's provider,
	// and a chosen model wins over the definition's, as in chat creation.
	aliases := &codersdk.ChatModelCallConfig{Aliases: []string{"shared"}}
	_, err = client.UpdateChatModel(ctx, defaultModel.OrganizationID, defaultModel.ID, codersdk.UpdateChatModelRequest{ModelConfig: aliases})
	require.NoError(t, err)
	anthropicModel := createAdditionalChatModelWithCallConfig(t, client, "anthropic", "claude-shared", aliases)
	_, err = client.CreateForkUserAgentDefinition(ctx, user.OrganizationID, codersdk.Me, codersdk.CreateForkAgentDefinitionRequest{
		Content: "---\nname: aliased\nmodel: shared\n---\nAliased.\n",
	})
	require.NoError(t, err)
	for _, modelConfigID := range []uuid.UUID{uuid.Nil, anthropicModel.ID} {
		aliased := byName(client, codersdk.ForkAgentTypesRequest{ModelConfigID: modelConfigID})["aliased"]
		require.True(t, aliased.RootAvailable, aliased.RootUnavailableReason)
	}

	// The member sees the shared organization definition under its bare
	// name, and neither the owner's personal nor the unshared ones.
	got = byName(memberClient, codersdk.ForkAgentTypesRequest{})
	require.ElementsMatch(t, []string{"general", "explore", "computer_use", "reviewer"}, slices.Collect(maps.Keys(got)))
	require.Equal(t, "org", got["reviewer"].Source)

	memberProject, err := memberClient.CreateChatProject(ctx, user.OrganizationID, codersdk.CreateChatProjectRequest{Name: "member"})
	require.NoError(t, err)
	orgAdminRaw, _ := coderdtest.CreateAnotherUser(t, client.Client, user.OrganizationID, rbac.ScopedRoleOrgAdmin(user.OrganizationID))
	orgAdminClient := codersdk.NewExperimentalClient(orgAdminRaw)
	twoChatAgents := dbfake.WorkspaceBuild(t, db, database.WorkspaceTable{
		OrganizationID: user.OrganizationID,
		OwnerID:        user.UserID,
	}).WithAgent(func(agents []*sdkproto.Agent) []*sdkproto.Agent {
		agents[0].Name = "a" + agentselect.Suffix
		return append(agents, &sdkproto.Agent{
			Id: uuid.NewString(), Name: "b" + agentselect.Suffix, Auth: &sdkproto.Agent_Token{Token: uuid.NewString()},
		})
	}).Do()
	for _, tc := range []struct {
		name   string
		client *codersdk.ExperimentalClient
		req    codersdk.ForkAgentTypesRequest
		status int
	}{
		{name: "WorkspaceNotReadable", client: memberClient, req: codersdk.ForkAgentTypesRequest{WorkspaceID: workspace.Workspace.ID}, status: http.StatusBadRequest},
		{name: "WorkspaceNoSSH", client: orgAdminClient, req: codersdk.ForkAgentTypesRequest{WorkspaceID: workspace.Workspace.ID}, status: http.StatusBadRequest},
		{name: "ProjectNotFound", client: client, req: codersdk.ForkAgentTypesRequest{ProjectID: uuid.New()}, status: http.StatusNotFound},
		{name: "ProjectNotUsable", client: client, req: codersdk.ForkAgentTypesRequest{ProjectID: memberProject.ID}, status: http.StatusNotFound},
		{name: "MultipleChatAgents", client: client, req: codersdk.ForkAgentTypesRequest{WorkspaceID: twoChatAgents.Workspace.ID}, status: http.StatusConflict},
	} {
		_, err := tc.client.ForkAgentTypes(ctx, user.OrganizationID, tc.req)
		var sdkErr *codersdk.Error
		require.ErrorAs(t, err, &sdkErr, tc.name)
		require.Equal(t, tc.status, sdkErr.StatusCode(), tc.name)
	}
}

func TestForkAgentTypesExperimentOff(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	client, _, _ := newChatClientWithAPIAndDatabase(t, withChatWorkerDisabled)
	user := coderdtest.CreateFirstUser(t, client.Client)
	_, err := client.ForkAgentTypes(ctx, user.OrganizationID, codersdk.ForkAgentTypesRequest{})
	var sdkErr *codersdk.Error
	require.ErrorAs(t, err, &sdkErr)
	require.Equal(t, http.StatusForbidden, sdkErr.StatusCode())
}
