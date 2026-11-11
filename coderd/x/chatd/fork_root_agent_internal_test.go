package chatd

// This file is fork code: it tests root chats that start as an agent type
// behind the fork-agent-definitions experiment.

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

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
		codersdk.Experiments{codersdk.ExperimentForkAgentDefinitions, codersdk.ExperimentForkNestedSubagents},
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
		RootAgent:          &agent,
	})
	require.NoError(t, err)
	root, err = f.db.GetChatByID(ctx, root.ID)
	require.NoError(t, err)

	prompt := (agentDefinitionsFixture{db: f.db}).systemPrompt(t, root.ID)
	require.Contains(t, prompt, "ROOT-REVIEWER-MARKER")
	// readonly removes spawn_agent, so the root loses the guidance to use it.
	require.NotContains(t, prompt, subagentOrchestrationPromptBlock)
	require.Contains(t, prompt, strings.Replace(DefaultSystemPrompt, subagentOrchestrationPromptBlock, "", 1))
	messages, err := f.db.GetChatMessagesByChatID(ctx, database.GetChatMessagesByChatIDParams{ChatID: root.ID})
	require.NoError(t, err)
	env, err := f.server.buildTurnEnvironment(ctx, generationPrepareInput{Chat: root, Messages: messages})
	require.NoError(t, err)
	t.Cleanup(env.Close)
	names := toolNames(env.Tools)
	require.Contains(t, names, "execute")
	require.NotContains(t, names, "write_file")
	require.NotContains(t, names, spawnAgentToolName)

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
