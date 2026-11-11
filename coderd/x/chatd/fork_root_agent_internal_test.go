package chatd

// This file is fork code: it tests root chats that start as an agent type
// behind the fork-agent-definitions experiment.

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"

	agentproto "github.com/coder/coder/v2/agent/proto"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/codersdk"
)

// TestForkRootAgent checks that a root chat started as a definition gets its
// tool policy and prompt, and that its own chat_subagents row counts toward
// no subagent limit.
func TestForkRootAgent(t *testing.T) {
	t.Parallel()

	f := newNestedSubagentsFixture(t,
		codersdk.Experiments{codersdk.ExperimentForkAgentDefinitions, codersdk.ExperimentForkNestedSubagents, codersdk.ExperimentChatACPSubagents},
		Limits{Fork: ForkLimits{MaxSubagentDepth: 1}}, withInternalTestServerTransportFactory(&aibridgeTestFactory{}))
	ctx := chatdTestContext(t)
	_, err := f.fdb.UpsertChatOrganizationSubagentLimits(ctx, forkdb.UpsertChatOrganizationSubagentLimitsParams{
		OrganizationID:   f.root.OrganizationID,
		MaxLivePerFamily: sql.NullInt32{Int32: 1, Valid: true},
	})
	require.NoError(t, err)
	_, err = f.fdb.InsertUserAgentDefinition(ctx, forkdb.InsertUserAgentDefinitionParams{
		OrganizationID: f.root.OrganizationID,
		UserID:         f.root.OwnerID,
		Name:           "reviewer",
		Content:        agentDefinitionMarkdown("name: reviewer\nreadonly: true", "ROOT-REVIEWER-MARKER"),
	})
	require.NoError(t, err)
	_, err = f.fdb.InsertUserAgentDefinition(ctx, forkdb.InsertUserAgentDefinitionParams{
		OrganizationID: f.root.OrganizationID,
		UserID:         f.root.OwnerID,
		Name:           "spawner",
		Content:        agentDefinitionMarkdown("name: spawner\ndisallowed_tools: [acp_wait_agent]", "ROOT-SPAWNER-MARKER"),
	})
	require.NoError(t, err)
	dynamicTools, err := json.Marshal([]codersdk.DynamicTool{{
		Name:        "client_tool",
		Description: "A tool the client runs.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}})
	require.NoError(t, err)
	acpBody, err := protojson.Marshal(&agentproto.ACPHarnessBody{Slug: "test", DisplayName: "Test"})
	require.NoError(t, err)
	seedAgentContext(ctx, t, f.db, f.root.AgentID.UUID, "test", []byte("acp"), database.WorkspaceAgentContextBodyKindAcpHarness, acpBody)
	agent, err := f.server.ResolveRootAgent(ctx, RootAgentOptions{
		OwnerID:        f.root.OwnerID,
		OrganizationID: f.root.OrganizationID,
		AgentType:      "reviewer",
		ModelConfigID:  f.root.LastModelConfigID,
	})
	require.NoError(t, err)
	root, err := f.server.CreateChat(ctx, CreateOptions{
		OrganizationID:     f.root.OrganizationID,
		OwnerID:            f.root.OwnerID,
		WorkspaceID:        f.root.WorkspaceID,
		BuildID:            f.root.BuildID,
		AgentID:            f.root.AgentID,
		Title:              "reviewer root",
		ModelConfigID:      f.root.LastModelConfigID,
		InitialUserContent: []codersdk.ChatMessagePart{codersdk.ChatMessageText("review")},
		DynamicTools:       dynamicTools,
		RootAgent:          &agent,
	})
	require.NoError(t, err)
	root, err = f.db.GetChatByID(ctx, root.ID)
	require.NoError(t, err)
	_, err = f.server.CreateChat(ctx, CreateOptions{
		OrganizationID: f.root.OrganizationID,
		OwnerID:        f.root.OwnerID,
		ParentChatID:   uuid.NullUUID{UUID: f.root.ID, Valid: true},
		Title:          "reviewer child",
		ModelConfigID:  f.root.LastModelConfigID,
		RootAgent:      &agent,
	})
	require.ErrorContains(t, err, "a child chat cannot start as a root agent type")

	prompt := (agentDefinitionsFixture{db: f.db}).systemPrompt(t, root.ID)
	require.Contains(t, prompt, "ROOT-REVIEWER-MARKER")
	// readonly removes write_file, which the attached guidance names.
	require.Contains(t, prompt, workspaceAttachedLimitedAwareness)
	require.NotContains(t, prompt, workspaceAttachedAwareness)
	// readonly removes spawn_agent, so the root loses the guidance to use it.
	require.NotContains(t, prompt, subagentOrchestrationPromptBlock)
	require.Contains(t, prompt, strings.Replace(DefaultSystemPrompt, subagentOrchestrationPromptBlock, "", 1))
	messages, err := f.db.GetChatMessagesByChatID(ctx, database.GetChatMessagesByChatIDParams{ChatID: root.ID})
	require.NoError(t, err)
	env, err := f.server.buildTurnEnvironment(ctx, generationPrepareInput{Chat: root, Messages: messages})
	require.NoError(t, err)
	t.Cleanup(env.Close)
	systemText := func(env turnEnvironment) string {
		var system strings.Builder
		for _, message := range env.Prompt {
			for _, part := range message.Content {
				if text, ok := part.(fantasy.TextPart); ok && message.Role == fantasy.MessageRoleSystem {
					system.WriteString(text.Text)
				}
			}
		}
		return system.String()
	}
	names := toolNames(env.Tools)
	require.Contains(t, names, "execute")
	require.NotContains(t, names, "write_file")
	require.NotContains(t, names, spawnAgentToolName)
	// readonly also removes acp_spawn_agent, and with it the ACP guidance.
	require.NotContains(t, names, "acp_spawn_agent")
	require.NotContains(t, systemText(env), acpSubagentPromptBlock)
	// A dynamic tool that the policy removes must not wait for the client.
	require.NotContains(t, names, "client_tool")
	require.NotContains(t, env.DynamicToolNames, "client_tool")

	// The ACP guidance needs every ACP tool, not only acp_spawn_agent.
	spawnerAgent, err := f.server.ResolveRootAgent(ctx, RootAgentOptions{
		OwnerID:        f.root.OwnerID,
		OrganizationID: f.root.OrganizationID,
		AgentType:      "spawner",
		ModelConfigID:  f.root.LastModelConfigID,
	})
	require.NoError(t, err)
	spawner, err := f.server.CreateChat(ctx, CreateOptions{
		OrganizationID:     f.root.OrganizationID,
		OwnerID:            f.root.OwnerID,
		WorkspaceID:        f.root.WorkspaceID,
		BuildID:            f.root.BuildID,
		AgentID:            f.root.AgentID,
		Title:              "spawner root",
		ModelConfigID:      f.root.LastModelConfigID,
		InitialUserContent: []codersdk.ChatMessagePart{codersdk.ChatMessageText("spawn")},
		RootAgent:          &spawnerAgent,
	})
	require.NoError(t, err)
	spawner, err = f.db.GetChatByID(ctx, spawner.ID)
	require.NoError(t, err)
	messages, err = f.db.GetChatMessagesByChatID(ctx, database.GetChatMessagesByChatIDParams{ChatID: spawner.ID})
	require.NoError(t, err)
	spawnerEnv, err := f.server.buildTurnEnvironment(ctx, generationPrepareInput{Chat: spawner, Messages: messages})
	require.NoError(t, err)
	t.Cleanup(spawnerEnv.Close)
	names = toolNames(spawnerEnv.Tools)
	require.Contains(t, names, "acp_spawn_agent")
	require.NotContains(t, names, "acp_wait_agent")
	require.NotContains(t, systemText(spawnerEnv), acpSubagentPromptBlock)
	require.Contains(t, (agentDefinitionsFixture{db: f.db}).systemPrompt(t, spawner.ID), workspaceAttachedAwareness)

	// A detached root that cannot create a workspace is not told to.
	detached, err := f.server.CreateChat(ctx, CreateOptions{
		OrganizationID:     f.root.OrganizationID,
		OwnerID:            f.root.OwnerID,
		Title:              "detached reviewer root",
		ModelConfigID:      f.root.LastModelConfigID,
		InitialUserContent: []codersdk.ChatMessagePart{codersdk.ChatMessageText("review")},
		RootAgent:          &agent,
	})
	require.NoError(t, err)
	prompt = (agentDefinitionsFixture{db: f.db}).systemPrompt(t, detached.ID)
	require.Contains(t, prompt, workspaceDetachedAwarenessBase)
	require.NotContains(t, prompt, workspaceDetachedAwareness)

	// The root is running, and its depth 0 row must not use up the depth
	// limit or the family's one live subagent.
	f.setStatus(t, root, database.ChatStatusRunning, uuid.NullUUID{})
	child, err := f.spawn(t, root, childSubagentChatOptions{})
	require.NoError(t, err)
	row, err := f.fdb.GetChatSubagentByChatID(ctx, child.ID)
	require.NoError(t, err)
	require.Equal(t, int32(1), row.Depth)
	live, err := f.fdb.CountLiveSubagentsInFamily(ctx, root.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), live)
	_, err = f.spawn(t, root, childSubagentChatOptions{})
	require.ErrorContains(t, err, "this chat family has 1 live subagents and the maximum is 1")
	f.requireNoChildren(t, f.root)
}
