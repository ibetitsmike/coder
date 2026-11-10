package chatd

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
	"github.com/coder/coder/v2/codersdk"
)

func TestForkComputerUseSpawnOverrides(t *testing.T) {
	t.Parallel()

	db, ps := dbtestutil.NewDB(t)
	server := newInternalTestServer(t, db, ps, chatprovider.ProviderAPIKeys{},
		withInternalTestServerExperiments(append(upstreamExperimentsKnown(), codersdk.ExperimentForkAgentDefinitions)))

	ctx := chatdTestContext(t)
	user, org, model := seedInternalChatDeps(t, db)
	insertEnabledAnthropicProvider(t, db, user.ID)
	overrideModel := insertInternalChatModelConfigForProvider(t, db, org.ID, "anthropic", "claude-opus-5", true)
	upsertInternalChatOrganizationModelOverride(t, db, org.ID, codersdk.ChatModelOverrideContextComputerUse, overrideModel.ID.String()+":high")
	explicitModel := insertInternalChatModelConfigForProvider(t, db, org.ID, "anthropic", "claude-sonnet-5", true)
	insertInternalAIProvider(t, db, database.AIProviderTypeGoogle, "test-key", true)
	incapableModel := insertInternalChatModelConfigForProvider(t, db, org.ID, "google", "gemini-test", true)
	workspace, build, agent := seedWorkspaceBinding(t, db, user.ID)

	parent, err := server.CreateChat(ctx, CreateOptions{
		OrganizationID:     org.ID,
		OwnerID:            user.ID,
		WorkspaceID:        uuid.NullUUID{UUID: workspace.ID, Valid: true},
		BuildID:            uuid.NullUUID{UUID: build.ID, Valid: true},
		AgentID:            uuid.NullUUID{UUID: agent.ID, Valid: true},
		Title:              "parent-computer-use-overrides",
		ModelConfigID:      model.ID,
		InitialUserContent: []codersdk.ChatMessagePart{codersdk.ChatMessageText("hello")},
	})
	require.NoError(t, err)
	parentChat, err := db.GetChatByID(ctx, parent.ID)
	require.NoError(t, err)

	spawn := func(t *testing.T, args spawnAgentArgs) database.Chat {
		t.Helper()
		args.Type = subagentTypeComputerUse
		args.Prompt = "take a screenshot"
		result := requireSpawnAgentResponse(t, runSpawnAgentTool(ctx, t, server, parentChat, args))
		childID, err := uuid.Parse(result.ChatID)
		require.NoError(t, err)
		child, err := db.GetChatByID(ctx, childID)
		require.NoError(t, err)
		require.Equal(t, database.ChatModeComputerUse, child.Mode.ChatMode)
		return child
	}

	t.Run("ExplicitModelAndEffort", func(t *testing.T) {
		t.Parallel()
		child := spawn(t, spawnAgentArgs{ModelConfigID: explicitModel.ID.String(), ReasoningEffort: "low"})
		require.Equal(t, explicitModel.ID, child.LastModelConfigID)
		require.Equal(t, database.NullChatReasoningEffort{ChatReasoningEffort: database.ChatReasoningEffortLow, Valid: true}, child.LastReasoningEffort)
	})

	t.Run("ExplicitModelDropsOverrideEffort", func(t *testing.T) {
		t.Parallel()
		child := spawn(t, spawnAgentArgs{ModelConfigID: explicitModel.ID.String()})
		require.Equal(t, explicitModel.ID, child.LastModelConfigID)
		require.False(t, child.LastReasoningEffort.Valid, "the override's effort belongs to the override's model")
	})

	t.Run("ExplicitEffortKeepsOverrideModel", func(t *testing.T) {
		t.Parallel()
		child := spawn(t, spawnAgentArgs{ReasoningEffort: "low"})
		require.Equal(t, overrideModel.ID, child.LastModelConfigID)
		require.Equal(t, database.NullChatReasoningEffort{ChatReasoningEffort: database.ChatReasoningEffortLow, Valid: true}, child.LastReasoningEffort)
	})

	t.Run("RejectsModelWithoutComputerUse", func(t *testing.T) {
		t.Parallel()
		resp := runSpawnAgentTool(ctx, t, server, parentChat, spawnAgentArgs{
			Type:          subagentTypeComputerUse,
			Prompt:        "take a screenshot",
			ModelConfigID: incapableModel.ID.String(),
		})
		require.True(t, resp.IsError, "expected tool error but got: %s", resp.Content)
		require.Contains(t, resp.Content, `model_config_id model "gemini-test" uses AI provider`)
		require.Contains(t, resp.Content, `which cannot run type "computer_use"`)
	})
}

func TestForkComputerUseSpawnOverridesToolText(t *testing.T) {
	t.Parallel()

	spawnInfo := func(t *testing.T, experiments codersdk.Experiments) (string, string, string) {
		t.Helper()
		db, ps := dbtestutil.NewDB(t)
		server := newInternalTestServer(t, db, ps, chatprovider.ProviderAPIKeys{}, withInternalTestServerExperiments(experiments))
		ctx := chatdTestContext(t)
		user, org, model := seedInternalChatDeps(t, db)
		parentChat := createInternalParentChat(ctx, t, server, db, org.ID, user.ID, model.ID, "parent-tool-text")
		tools, err := server.subagentTools(ctx, func() database.Chat { return parentChat }, model.ID)
		require.NoError(t, err)
		tool := findToolByName(server.withComputerUseSpawnOverrides(tools), spawnAgentToolName)
		require.NotNil(t, tool)
		info := tool.Info()
		modelConfigID, _ := info.Parameters["model_config_id"].(map[string]any)["description"].(string)
		reasoningEffort, _ := info.Parameters["reasoning_effort"].(map[string]any)["description"].(string)
		return info.Description, modelConfigID, reasoningEffort
	}

	t.Run("ExperimentOn", func(t *testing.T) {
		t.Parallel()
		description, modelConfigID, reasoningEffort := spawnInfo(t, append(upstreamExperimentsKnown(), codersdk.ExperimentForkAgentDefinitions))
		require.Contains(t, description, "both apply to every type. A computer_use model_config_id must support computer use.")
		require.NotContains(t, description, upstreamSpawnAgentOverridesSentence)
		require.Contains(t, modelConfigID, "Optional model config UUID")
		require.NotContains(t, modelConfigID, "computer_use")
		require.Contains(t, reasoningEffort, "Optional reasoning effort")
		require.NotContains(t, reasoningEffort, "computer_use")
	})

	t.Run("ExperimentOff", func(t *testing.T) {
		t.Parallel()
		description, modelConfigID, reasoningEffort := spawnInfo(t, upstreamExperimentsKnown())
		require.Contains(t, description, upstreamSpawnAgentOverridesSentence)
		require.Contains(t, modelConfigID, upstreamSpawnAgentArgUnsupported)
		require.Contains(t, reasoningEffort, upstreamSpawnAgentArgUnsupported)
	})
}
