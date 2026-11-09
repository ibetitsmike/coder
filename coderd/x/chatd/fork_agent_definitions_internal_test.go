package chatd

// This file is fork code: it tests custom agent types for spawn_agent behind
// the fork-agent-definitions experiment.

import (
	"encoding/json"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/sqlc-dev/pqtype"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"

	agentproto "github.com/coder/coder/v2/agent/proto"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprompt"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
	"github.com/coder/coder/v2/codersdk"
)

type agentDefinitionsFixture struct {
	server     *Server
	db         database.Store
	fdb        forkdb.Store
	root       database.Chat
	otherModel database.ChatModelConfig
}

// newAgentDefinitionsFixture pins the definitions returned by definitions to
// a new root chat. definitions receives a second enabled model config the
// definitions can name.
func newAgentDefinitionsFixture(
	t *testing.T,
	experiments codersdk.Experiments,
	definitions func(otherModel database.ChatModelConfig) map[string]string,
	opts ...internalTestServerOpt,
) agentDefinitionsFixture {
	t.Helper()

	db, ps := dbtestutil.NewDB(t)
	server := newInternalTestServer(t, db, ps, chatprovider.ProviderAPIKeys{},
		append(opts, withInternalTestServerExperiments(experiments))...)
	fdb, err := forkdb.From(db)
	require.NoError(t, err)

	ctx := chatdTestContext(t)
	user, org, model := seedInternalChatDeps(t, db)
	otherModel := insertInternalChatModelConfig(t, db, org.ID, "custom-agent-model-"+uuid.NewString(), true)
	workspace, build, agent := seedWorkspaceBinding(t, db, user.ID)

	now := dbtime.Now()
	_, err = db.UpsertWorkspaceAgentContextSnapshot(ctx, database.UpsertWorkspaceAgentContextSnapshotParams{
		WorkspaceAgentID: agent.ID,
		Version:          1,
		AggregateHash:    []byte("agent-definitions"),
		ReceivedAt:       now,
	})
	require.NoError(t, err)
	for name, definition := range definitions(otherModel) {
		body, err := protojson.Marshal(&agentproto.SubagentBody{Definition: []byte(definition), Name: name})
		require.NoError(t, err)
		_, err = db.UpsertWorkspaceAgentContextResource(ctx, database.UpsertWorkspaceAgentContextResourceParams{
			WorkspaceAgentID: agent.ID,
			Source:           "/home/coder/.claude/agents/" + name + ".md",
			BodyKind:         database.WorkspaceAgentContextBodyKindSubagent,
			Body:             body,
			ContentHash:      []byte(name),
			SizeBytes:        int64(len(definition)),
			Status:           database.WorkspaceAgentContextResourceStatusOk,
			Now:              now,
		})
		require.NoError(t, err)
	}

	root, err := server.CreateChat(ctx, CreateOptions{
		OrganizationID:     org.ID,
		OwnerID:            user.ID,
		WorkspaceID:        uuid.NullUUID{UUID: workspace.ID, Valid: true},
		BuildID:            uuid.NullUUID{UUID: build.ID, Valid: true},
		AgentID:            uuid.NullUUID{UUID: agent.ID, Valid: true},
		Title:              "root",
		ModelConfigID:      model.ID,
		InitialUserContent: []codersdk.ChatMessagePart{codersdk.ChatMessageText("hello")},
	})
	require.NoError(t, err)
	root, err = db.GetChatByID(ctx, root.ID)
	require.NoError(t, err)
	return agentDefinitionsFixture{server: server, db: db, fdb: fdb, root: root, otherModel: otherModel}
}

func (f agentDefinitionsFixture) spawn(t *testing.T, args spawnAgentArgs) (database.Chat, error) {
	t.Helper()
	ctx := chatdTestContext(t)
	args.Prompt = "do the work"
	child, err := f.server.spawnAgent(ctx, func() database.Chat { return f.root }, f.root, f.root.LastModelConfigID, args)
	if err != nil {
		return database.Chat{}, err
	}
	child, err = f.db.GetChatByID(ctx, child.ID)
	require.NoError(t, err)
	return child, nil
}

func (f agentDefinitionsFixture) systemPrompt(t *testing.T, chatID uuid.UUID) string {
	t.Helper()
	messages, err := f.db.GetChatMessagesForPromptByChatID(chatdTestContext(t), chatID)
	require.NoError(t, err)
	var texts []string
	for _, message := range messages {
		if message.Role != database.ChatMessageRoleSystem {
			continue
		}
		parts, err := chatprompt.ParseContent(message)
		require.NoError(t, err)
		for _, part := range parts {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func agentDefinitionMarkdown(frontmatter, body string) string {
	return "---\n" + frontmatter + "\n---\n" + body + "\n"
}

func TestForkAgentDefinitionSpawn(t *testing.T) {
	t.Parallel()

	f := newAgentDefinitionsFixture(t,
		codersdk.Experiments{codersdk.ExperimentForkAgentDefinitions},
		func(database.ChatModelConfig) map[string]string {
			return map[string]string{
				"reviewer": agentDefinitionMarkdown("name: reviewer\ndescription: Reviews diffs.", "Review the diff for REVIEWER-MARKER."),
				"explore":  agentDefinitionMarkdown("name: explore\ndescription: Shadows explore.", "SHADOW-EXPLORE-MARKER"),
				"broken":   agentDefinitionMarkdown("name: [broken", "BROKEN-MARKER"),
			}
		})

	t.Run("CustomType", func(t *testing.T) {
		t.Parallel()
		ctx := chatdTestContext(t)
		child, err := f.spawn(t, spawnAgentArgs{Type: "reviewer"})
		require.NoError(t, err)
		require.Contains(t, f.systemPrompt(t, child.ID), "Review the diff for REVIEWER-MARKER.")
		row, err := f.fdb.GetChatSubagentByChatID(ctx, child.ID)
		require.NoError(t, err)
		require.Equal(t, "reviewer", row.AgentType)
		require.Equal(t, int32(1), row.Depth)

		description, err := buildSpawnAgentDescription(ctx, f.server, f.root)
		require.NoError(t, err)
		reviewer := strings.Index(description, "reviewer (Reviews diffs.)")
		require.Positive(t, reviewer, description)
		require.Greater(t, reviewer, strings.Index(description, "explore ("))
		require.NotContains(t, description, "Shadows explore")
		require.NotContains(t, description, "broken")
	})

	t.Run("BuiltinWins", func(t *testing.T) {
		t.Parallel()
		child, err := f.spawn(t, spawnAgentArgs{Type: subagentTypeExplore})
		require.NoError(t, err)
		require.Equal(t, database.ChatModeExplore, child.Mode.ChatMode)
		require.NotContains(t, f.systemPrompt(t, child.ID), "SHADOW-EXPLORE-MARKER")
	})
}

func TestForkAgentDefinitionExperimentOff(t *testing.T) {
	t.Parallel()

	f := newAgentDefinitionsFixture(t, upstreamExperimentsKnown(), func(database.ChatModelConfig) map[string]string {
		return map[string]string{
			"reviewer": agentDefinitionMarkdown("name: reviewer\ndescription: Reviews diffs.", "Review the diff."),
		}
	})
	ctx := chatdTestContext(t)

	_, err := f.spawn(t, spawnAgentArgs{Type: "reviewer"})
	require.ErrorContains(t, err, "type must be one of: general, explore")
	require.NotContains(t, err.Error(), "reviewer")

	description, err := buildSpawnAgentDescription(ctx, f.server, f.root)
	require.NoError(t, err)
	require.NotContains(t, description, "reviewer")
}

func TestForkAgentDefinitionModel(t *testing.T) {
	t.Parallel()

	f := newAgentDefinitionsFixture(t,
		codersdk.Experiments{codersdk.ExperimentForkAgentDefinitions},
		func(otherModel database.ChatModelConfig) map[string]string {
			return map[string]string{
				"pinned-model":  agentDefinitionMarkdown("name: pinned-model\nmodel: "+strings.ToUpper(otherModel.DisplayName)+"\neffort: High", "Pinned."),
				"missing-model": agentDefinitionMarkdown("name: missing-model\nmodel: no-such-model", "Missing."),
				"bad-effort":    agentDefinitionMarkdown("name: bad-effort\neffort: turbo", "Bad effort."),
				"inherits":      agentDefinitionMarkdown("name: inherits\nmodel: inherit", "Inherits."),
			}
		})
	// Definitions without a model inherit the parent's model, not the
	// general subagent override.
	upsertInternalChatOrganizationModelOverride(t, f.db, f.root.OrganizationID,
		codersdk.ChatModelOverrideContextGeneral, f.otherModel.ID.String())
	high := database.NullChatReasoningEffort{ChatReasoningEffort: database.ChatReasoningEffortHigh, Valid: true}
	low := database.NullChatReasoningEffort{ChatReasoningEffort: database.ChatReasoningEffortLow, Valid: true}

	t.Run("DefinitionModelAndEffort", func(t *testing.T) {
		t.Parallel()
		child, err := f.spawn(t, spawnAgentArgs{Type: "pinned-model"})
		require.NoError(t, err)
		require.Equal(t, f.otherModel.ID, child.LastModelConfigID)
		require.Equal(t, high, child.LastReasoningEffort)
	})

	t.Run("ExplicitArgsWin", func(t *testing.T) {
		t.Parallel()
		child, err := f.spawn(t, spawnAgentArgs{
			Type:            "pinned-model",
			ModelConfigID:   f.root.LastModelConfigID.String(),
			ReasoningEffort: "low",
		})
		require.NoError(t, err)
		require.Equal(t, f.root.LastModelConfigID, child.LastModelConfigID)
		require.Equal(t, low, child.LastReasoningEffort)
	})

	t.Run("UnknownModel", func(t *testing.T) {
		t.Parallel()
		_, err := f.spawn(t, spawnAgentArgs{Type: "missing-model"})
		require.ErrorContains(t, err, `agent "missing-model" sets model "no-such-model", which is not an available model`)
	})

	t.Run("InheritsParentModel", func(t *testing.T) {
		t.Parallel()
		child, err := f.spawn(t, spawnAgentArgs{Type: "inherits"})
		require.NoError(t, err)
		require.Equal(t, f.root.LastModelConfigID, child.LastModelConfigID)
	})

	t.Run("InvalidEffort", func(t *testing.T) {
		t.Parallel()
		_, err := f.spawn(t, spawnAgentArgs{Type: "bad-effort"})
		require.ErrorContains(t, err, `agent "bad-effort" sets reasoning_effort "turbo", which is not one of`)
	})
}

func TestForkAgentDefinitionDescriptionBudget(t *testing.T) {
	t.Parallel()

	// "é" is two bytes, so 253 bytes of it ends mid-rune.
	long := strings.Repeat("é", 200)
	got := budgetCustomAgentDescriptions([]string{"  Reviews\n\tdiffs.  ", long, ""})
	require.Equal(t, "Reviews diffs.", got[0])
	require.Equal(t, strings.Repeat("é", 126)+"...", got[1])
	require.Equal(t, customAgentFallbackDescription, got[2])

	many := make([]string, 0, 10)
	for range 10 {
		many = append(many, strings.Repeat("a", customAgentDescriptionMaxBytes))
	}
	many = append(many, "short")
	got = budgetCustomAgentDescriptions(many)
	for i, description := range got {
		if i < customAgentDescriptionsMaxBytes/customAgentDescriptionMaxBytes {
			require.Len(t, description, customAgentDescriptionMaxBytes, "entry %d", i)
			continue
		}
		require.Equal(t, customAgentFallbackDescription, description, "entry %d", i)
	}
}

func TestForkAgentDefinitionToolPolicy(t *testing.T) {
	t.Parallel()

	f := newAgentDefinitionsFixture(t,
		codersdk.Experiments{codersdk.ExperimentForkAgentDefinitions, codersdk.ExperimentForkNestedSubagents},
		func(database.ChatModelConfig) map[string]string {
			return map[string]string{
				"reader":      agentDefinitionMarkdown("name: reader\nreadonly: true", "Read only."),
				"no-writes":   agentDefinitionMarkdown("name: no-writes\ndisallowed_tools: [write_file]", "No writes."),
				"docs-reader": agentDefinitionMarkdown("name: docs-reader\ntools: [Read]\nmcp: [Docs]", "Docs only."),
			}
		},
		withInternalTestServerTransportFactory(&aibridgeTestFactory{}))
	toolNames := func(t *testing.T, child database.Chat) []string {
		t.Helper()
		ctx := chatdTestContext(t)
		messages, err := f.db.GetChatMessagesByChatID(ctx, database.GetChatMessagesByChatIDParams{ChatID: child.ID})
		require.NoError(t, err)
		env, err := f.server.buildTurnEnvironment(ctx, generationPrepareInput{Chat: child, Messages: messages})
		require.NoError(t, err)
		t.Cleanup(env.Close)
		names := make([]string, 0, len(env.Tools))
		for _, tool := range env.Tools {
			names = append(names, tool.Info().Name)
		}
		return names
	}

	t.Run("ReadOnly", func(t *testing.T) {
		t.Parallel()
		child, err := f.spawn(t, spawnAgentArgs{Type: "reader"})
		require.NoError(t, err)
		row, err := f.fdb.GetChatSubagentByChatID(chatdTestContext(t), child.ID)
		require.NoError(t, err)
		var policy customAgentToolPolicy
		require.NoError(t, json.Unmarshal(row.ToolPolicy.RawMessage, &policy))
		require.True(t, policy.ReadOnly)

		names := toolNames(t, child)
		require.Contains(t, names, "execute")
		require.NotContains(t, names, "write_file")
		require.NotContains(t, names, "edit_files")
	})

	t.Run("DisallowedTools", func(t *testing.T) {
		t.Parallel()
		child, err := f.spawn(t, spawnAgentArgs{Type: "no-writes"})
		require.NoError(t, err)
		names := toolNames(t, child)
		require.Contains(t, names, "edit_files")
		require.NotContains(t, names, "write_file")
	})

	t.Run("AllowlistAndNamedMCP", func(t *testing.T) {
		t.Parallel()
		ctx := chatdTestContext(t)
		child, err := f.spawn(t, spawnAgentArgs{Type: "docs-reader"})
		require.NoError(t, err)
		docs := dbgen.MCPServerConfig(t, f.db, database.MCPServerConfig{OrganizationID: f.root.OrganizationID, Slug: "docs"})
		other := dbgen.MCPServerConfig(t, f.db, database.MCPServerConfig{OrganizationID: f.root.OrganizationID, Slug: "other"})
		docsTool := newTestMCPAgentTool("docs__search", docs.ID)
		otherTool := newTestMCPAgentTool("other__lookup", other.ID)

		tools, candidates, err := f.server.applyCustomAgentToolPolicy(ctx, child,
			[]fantasy.AgentTool{newTestAgentTool("read_file"), newTestAgentTool("write_file"), docsTool, otherTool},
			[]deferredMCPTool{{tool: otherTool, server: "other"}, {tool: docsTool, server: "docs"}})
		require.NoError(t, err)
		names := make([]string, 0, len(tools))
		for _, tool := range tools {
			names = append(names, tool.Info().Name)
		}
		require.Equal(t, []string{"read_file", "docs__search"}, names)
		require.Len(t, candidates, 1)
		require.Equal(t, "docs__search", candidates[0].tool.Info().Name)
	})

	t.Run("BuiltinType", func(t *testing.T) {
		t.Parallel()
		child, err := f.spawn(t, spawnAgentArgs{Type: subagentTypeGeneral})
		require.NoError(t, err)
		row, err := f.fdb.GetChatSubagentByChatID(chatdTestContext(t), child.ID)
		require.NoError(t, err)
		require.False(t, row.ToolPolicy.Valid)
		require.Contains(t, toolNames(t, child), "write_file")
	})

	t.Run("UndecodablePolicyFailsClosed", func(t *testing.T) {
		t.Parallel()
		ctx := chatdTestContext(t)
		for _, policy := range []string{`{"tools": "Read"}`, `{"mcp_mode": "everything"}`} {
			child := dbgen.Chat(t, f.db, database.Chat{
				OrganizationID:    f.root.OrganizationID,
				OwnerID:           f.root.OwnerID,
				LastModelConfigID: f.root.LastModelConfigID,
				ParentChatID:      uuid.NullUUID{UUID: f.root.ID, Valid: true},
				RootChatID:        uuid.NullUUID{UUID: f.root.ID, Valid: true},
			})
			_, err := f.fdb.InsertChatSubagent(ctx, forkdb.InsertChatSubagentParams{
				ChatID:     child.ID,
				AgentType:  "reader",
				Depth:      1,
				Isolation:  "shared",
				ToolPolicy: pqtype.NullRawMessage{RawMessage: json.RawMessage(policy), Valid: true},
			})
			require.NoError(t, err)
			_, _, err = f.server.applyCustomAgentToolPolicy(ctx, child, []fantasy.AgentTool{newTestAgentTool("write_file")}, nil)
			require.Error(t, err, policy)
		}
	})
}
