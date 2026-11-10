package chatd

// This file is fork code: it tests custom agent types for spawn_agent behind
// the fork-agent-definitions experiment.

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/sqlc-dev/pqtype"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/encoding/protojson"

	agentproto "github.com/coder/coder/v2/agent/proto"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbmock"
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

// applyStoredToolPolicy filters tools and candidates by the tool policy
// stored for chat, as a turn of chat does.
func applyStoredToolPolicy(t *testing.T, server *Server, chat database.Chat, tools []fantasy.AgentTool, candidates []deferredMCPTool) ([]fantasy.AgentTool, []deferredMCPTool) {
	t.Helper()
	ctx := chatdTestContext(t)
	policy, err := server.customAgentToolPolicyOfChat(ctx, chat)
	require.NoError(t, err)
	tools, candidates, _, err = server.applyCustomAgentToolPolicy(ctx, chat, policy, tools, candidates, nil)
	require.NoError(t, err)
	return tools, candidates
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
				"no-writes":   agentDefinitionMarkdown("name: no-writes\ndisallowed_tools: [write_file, lookups__lookup]", "No writes."),
				"docs-reader": agentDefinitionMarkdown("name: docs-reader\ntools: [Read]\nmcp: [docs]", "Docs only."),
				"no-wait":     agentDefinitionMarkdown("name: no-wait\ndisallowed_tools: [wait_agent]", "No waiting."),
			}
		},
		withInternalTestServerTransportFactory(&aibridgeTestFactory{}))
	turnToolNames := func(t *testing.T, child database.Chat) []string {
		t.Helper()
		ctx := chatdTestContext(t)
		messages, err := f.db.GetChatMessagesByChatID(ctx, database.GetChatMessagesByChatIDParams{ChatID: child.ID})
		require.NoError(t, err)
		env, err := f.server.buildTurnEnvironment(ctx, generationPrepareInput{Chat: child, Messages: messages})
		require.NoError(t, err)
		t.Cleanup(env.Close)
		return toolNames(env.Tools)
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

		names := turnToolNames(t, child)
		require.Contains(t, names, "execute")
		require.NotContains(t, names, "write_file")
		require.NotContains(t, names, "edit_files")
		require.NotContains(t, f.systemPrompt(t, child.ID), subagentOrchestrationPromptBlock)
	})

	t.Run("DisallowedTools", func(t *testing.T) {
		t.Parallel()
		child, err := f.spawn(t, spawnAgentArgs{Type: "no-writes"})
		require.NoError(t, err)
		names := turnToolNames(t, child)
		require.Contains(t, names, "edit_files")
		require.NotContains(t, names, "write_file")
		require.Contains(t, f.systemPrompt(t, child.ID), subagentOrchestrationPromptBlock)
		noWait, err := f.spawn(t, spawnAgentArgs{Type: "no-wait"})
		require.NoError(t, err)
		require.NotContains(t, f.systemPrompt(t, noWait.ID), subagentOrchestrationPromptBlock)

		searchTool := newTestMCPAgentTool("lookups__search", uuid.New())
		lookupTool := newTestMCPAgentTool("lookups__lookup", uuid.New())
		tools, candidates := applyStoredToolPolicy(t, f.server, child,
			[]fantasy.AgentTool{newTestAgentTool("read_file"), searchTool, lookupTool},
			[]deferredMCPTool{{tool: lookupTool, server: "lookups"}, {tool: searchTool, server: "lookups"}})
		require.Equal(t, []string{"read_file", "lookups__search"}, toolNames(tools))
		require.Len(t, candidates, 1)
		require.Equal(t, "lookups__search", candidates[0].tool.Info().Name)
	})

	t.Run("AllowlistAndNamedMCP", func(t *testing.T) {
		t.Parallel()
		child, err := f.spawn(t, spawnAgentArgs{Type: "docs-reader"})
		require.NoError(t, err)
		require.NotContains(t, f.systemPrompt(t, child.ID), subagentOrchestrationPromptBlock)
		docs := dbgen.MCPServerConfig(t, f.db, database.MCPServerConfig{OrganizationID: f.root.OrganizationID, Slug: "docs"})
		// The tools of this server are named docs__mirror__<tool>, so their
		// name alone would attribute them to docs.
		mirror := dbgen.MCPServerConfig(t, f.db, database.MCPServerConfig{OrganizationID: f.root.OrganizationID, Slug: "docs__mirror"})
		inline := dbgen.ChatMCPServer(t, f.db, database.ChatMCPServer{ChatID: f.root.ID, Slug: "docs"})
		// Slugs match exactly, so DOCS is a different server.
		inlineUpper := dbgen.ChatMCPServer(t, f.db, database.ChatMCPServer{ChatID: f.root.ID, Slug: "DOCS"})
		internal := InternalMCPServer{Slug: "docs"}
		server := &Server{db: f.db, experiments: f.server.experiments, internalMCPServers: []InternalMCPServer{internal}}
		docsTool := newTestMCPAgentTool("docs__search", docs.ID)
		mirrorTool := newTestMCPAgentTool("docs__mirror__search", mirror.ID)

		tools, candidates := applyStoredToolPolicy(t, server, child,
			[]fantasy.AgentTool{
				newTestAgentTool("read_file"), newTestAgentTool("write_file"), docsTool, mirrorTool,
				newTestMCPAgentTool("docs__fetch", inline.ID), newTestMCPAgentTool("DOCS__fetch", inlineUpper.ID),
				newTestMCPAgentTool("docs__kb", internal.id()),
			},
			[]deferredMCPTool{{tool: mirrorTool}, {tool: docsTool}})
		require.Equal(t, []string{"read_file", "docs__search", "docs__fetch", "docs__kb"}, toolNames(tools))
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
		require.Contains(t, turnToolNames(t, child), "write_file")
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
			dbgen.ChatSubagent(t, f.db, forkdb.ChatSubagent{
				ChatID:     child.ID,
				AgentType:  "reader",
				ToolPolicy: pqtype.NullRawMessage{RawMessage: json.RawMessage(policy), Valid: true},
			})
			_, err := f.server.customAgentToolPolicyOfChat(ctx, child)
			require.Error(t, err, policy)
		}
	})
}

// TestForkAgentDefinitionToolPolicySkillIndex checks that a child keeps the
// skill index only when its tool policy keeps read_skill.
func TestForkAgentDefinitionToolPolicySkillIndex(t *testing.T) {
	t.Parallel()

	f := newAgentDefinitionsFixture(t,
		codersdk.Experiments{codersdk.ExperimentForkAgentDefinitions},
		func(database.ChatModelConfig) map[string]string {
			return map[string]string{
				"reader":    agentDefinitionMarkdown("name: reader\nreadonly: true", "Read only."),
				"no-skills": agentDefinitionMarkdown("name: no-skills\ntools: [Read]", "No skills."),
				"denied":    agentDefinitionMarkdown("name: denied\ndisallowed_tools: [read_skill]", "No skills."),
			}
		},
		withInternalTestServerTransportFactory(&aibridgeTestFactory{}))
	ctx := chatdTestContext(t)
	_, err := f.db.InsertUserSkill(ctx, database.InsertUserSkillParams{
		ID: uuid.New(), UserID: f.root.OwnerID, Name: "notes", Content: "---\nname: notes\ndescription: Notes\n---\n\nTake notes.\n",
	})
	require.NoError(t, err)

	for _, tt := range []struct {
		agentType string
		keeps     bool
	}{
		{agentType: subagentTypeGeneral, keeps: true},
		{agentType: "reader", keeps: true},
		{agentType: "no-skills"},
		{agentType: "denied"},
	} {
		t.Run(tt.agentType, func(t *testing.T) {
			t.Parallel()
			ctx := chatdTestContext(t)
			child, err := f.spawn(t, spawnAgentArgs{Type: tt.agentType})
			require.NoError(t, err)
			messages, err := f.db.GetChatMessagesByChatID(ctx, database.GetChatMessagesByChatIDParams{ChatID: child.ID})
			require.NoError(t, err)
			env, err := f.server.buildTurnEnvironment(ctx, generationPrepareInput{Chat: child, Messages: messages})
			require.NoError(t, err)
			t.Cleanup(env.Close)
			require.Equal(t, tt.keeps, strings.Contains(systemPromptText(t, env.Prompt), "<available-skills>"))
			require.Equal(t, tt.keeps, slices.Contains(toolNames(env.Tools), "read_skill"))
		})
	}
}

func TestForkAgentDefinitionContextResources(t *testing.T) {
	t.Parallel()

	subagent := func(source, name string, status database.WorkspaceAgentContextResourceStatus, errText string) database.ChatContextResource {
		return database.ChatContextResource{
			Source:   source,
			BodyKind: database.WorkspaceAgentContextBodyKindSubagent,
			Body: mustMarshalContextBody(t, &agentproto.SubagentBody{
				Definition:  []byte(agentDefinitionMarkdown("name: "+name, "Review.")),
				Name:        name,
				Description: "Reviews diffs",
			}),
			SizeBytes: 42,
			Status:    status,
			Error:     errText,
		}
	}
	pinned := []database.ChatContextResource{
		instructionResource(t, "/home/coder/AGENTS.md", "be helpful", database.WorkspaceAgentContextResourceStatusOk),
		subagent("/home/coder/.agents/agents/reviewer.md", "reviewer", database.WorkspaceAgentContextResourceStatusOk, "missing description"),
		subagent("/home/coder/.agents/agents/broken.md", "", database.WorkspaceAgentContextResourceStatusInvalid, "missing name"),
		// An OK row without a name is skipped, like an OK skill without one.
		subagent("/home/coder/.agents/agents/unnamed.md", "", database.WorkspaceAgentContextResourceStatusOk, ""),
	}
	want := []codersdk.ChatContextResource{
		{
			Source: "/home/coder/AGENTS.md",
			Kind:   codersdk.ChatContextResourceKindInstructionFile,
			Status: codersdk.ChatContextResourceStatusOK,
		},
		{
			Source:              "/home/coder/.agents/agents/reviewer.md",
			Kind:                codersdk.ChatContextResourceKindSubagent,
			SizeBytes:           42,
			Status:              codersdk.ChatContextResourceStatusOK,
			Error:               "missing description",
			SubagentName:        "reviewer",
			SubagentDescription: "Reviews diffs",
		},
		{
			Source:    "/home/coder/.agents/agents/broken.md",
			Kind:      codersdk.ChatContextResourceKindSubagent,
			SizeBytes: 42,
			Status:    codersdk.ChatContextResourceStatusInvalid,
			Error:     "missing name",
		},
	}

	for _, tc := range []struct {
		name    string
		enabled bool
		want    []codersdk.ChatContextResource
	}{
		{name: "Enabled", enabled: true, want: want},
		{name: "ExperimentOff", want: want[:1]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			db := dbmock.NewMockStore(gomock.NewController(t))
			chatID := uuid.New()
			db.EXPECT().ListChatContextResourcesByChatID(gomock.Any(), chatID).Return(pinned, nil)
			server := newPinServer(t, db)
			if tc.enabled {
				server.experiments = codersdk.Experiments{codersdk.ExperimentForkAgentDefinitions}
			}
			got, err := server.ContextResources(context.Background(), database.Chat{ID: chatID})
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestForkAgentDefinitionToolResultType(t *testing.T) {
	t.Parallel()

	f := newAgentDefinitionsFixture(t,
		codersdk.Experiments{codersdk.ExperimentForkAgentDefinitions},
		func(database.ChatModelConfig) map[string]string {
			return map[string]string{
				"reviewer": agentDefinitionMarkdown("name: reviewer\ndescription: Reviews diffs.", "Review the diff."),
			}
		})
	ctx := chatdTestContext(t)
	tools, err := f.server.subagentTools(ctx, func() database.Chat { return f.root }, f.root.LastModelConfigID)
	require.NoError(t, err)
	tools, err = f.server.appendForkTools(ctx, tools, f.root, f.root.LastModelConfigID)
	require.NoError(t, err)
	run := func(name, input string) map[string]any {
		t.Helper()
		tool := findToolByName(tools, name)
		require.NotNil(t, tool, name)
		resp, err := tool.Run(ctx, fantasy.ToolCall{Input: input})
		require.NoError(t, err)
		return requireToolResponseMap(t, resp, false)
	}

	spawned := run(spawnAgentToolName, `{"type":"reviewer","prompt":"review it"}`)
	require.Equal(t, "reviewer", spawned["type"])
	reviewerID := spawned["chat_id"].(string)
	general := run(spawnAgentToolName, `{"type":"general","prompt":"do it"}`)
	require.Equal(t, subagentTypeGeneral, general["type"])

	interrupted := run("interrupt_agent", `{"chat_id":"`+reviewerID+`"}`)
	require.Equal(t, "reviewer", interrupted["type"])
	waited := run("wait_agent", `{"chat_id":"`+reviewerID+`","timeout_seconds":1}`)
	require.Equal(t, "reviewer", waited["type"])

	listed := run("list_agents", `{}`)
	types := map[string]any{}
	for _, agent := range listed["agents"].([]any) {
		entry := agent.(map[string]any)
		types[entry["chat_id"].(string)] = entry["type"]
	}
	require.Equal(t, map[string]any{reviewerID: "reviewer", general["chat_id"].(string): subagentTypeGeneral}, types)
}

func TestForkAgentDefinitionModelAlias(t *testing.T) {
	t.Parallel()

	f := newAgentDefinitionsFixture(t,
		codersdk.Experiments{codersdk.ExperimentForkAgentDefinitions},
		func(otherModel database.ChatModelConfig) map[string]string {
			return map[string]string{
				"fast":  agentDefinitionMarkdown("name: fast\nmodel: FAST", "Fast."),
				"deep":  agentDefinitionMarkdown("name: deep\nmodel: deep", "Deep."),
				"wide":  agentDefinitionMarkdown("name: wide\nmodel: wide", "Wide."),
				"exact": agentDefinitionMarkdown("name: exact\nmodel: "+otherModel.Model, "Exact."),
			}
		})
	insertInternalAIProvider(t, f.db, database.AIProviderTypeAnthropic, "test-key", true)
	insertInternalAIProvider(t, f.db, database.AIProviderTypeGoogle, "test-key", true)
	aliases := func(names ...string) json.RawMessage {
		raw, err := json.Marshal(codersdk.ChatModelCallConfig{Aliases: names})
		require.NoError(t, err)
		return raw
	}
	parentProviderModel := insertInternalChatModelConfigWithOptions(t, f.db, f.root.OrganizationID, "openai", "gpt-fast", true, aliases("fast"))
	anthropicModel := insertInternalChatModelConfigWithOptions(t, f.db, f.root.OrganizationID, "anthropic", "claude-fast", true,
		aliases("fast", "deep", "wide", f.otherModel.Model))
	googleModel := insertInternalChatModelConfigWithOptions(t, f.db, f.root.OrganizationID, "google", "gemini-wide", true, aliases("wide"))

	t.Run("ParentProviderWins", func(t *testing.T) {
		t.Parallel()
		child, err := f.spawn(t, spawnAgentArgs{Type: "fast"})
		require.NoError(t, err)
		require.Equal(t, parentProviderModel.ID, child.LastModelConfigID)
	})

	t.Run("SingleMatch", func(t *testing.T) {
		t.Parallel()
		child, err := f.spawn(t, spawnAgentArgs{Type: "deep"})
		require.NoError(t, err)
		require.Equal(t, anthropicModel.ID, child.LastModelConfigID)
	})

	t.Run("ExactModelBeatsAlias", func(t *testing.T) {
		t.Parallel()
		child, err := f.spawn(t, spawnAgentArgs{Type: "exact"})
		require.NoError(t, err)
		require.Equal(t, f.otherModel.ID, child.LastModelConfigID)
	})

	t.Run("AmbiguousOnOtherProviders", func(t *testing.T) {
		t.Parallel()
		_, err := f.spawn(t, spawnAgentArgs{Type: "wide"})
		require.ErrorContains(t, err, `agent "wide" sets model "wide", an alias of several models, none on the parent chat's AI provider`)
		require.ErrorContains(t, err, "claude-fast ("+anthropicModel.ID.String()+")")
		require.ErrorContains(t, err, "gemini-wide ("+googleModel.ID.String()+")")
	})

	t.Run("ListedModelsShowAliases", func(t *testing.T) {
		t.Parallel()
		ctx := chatdTestContext(t)
		resp := runSubagentTool(ctx, t, f.server, f.root, f.root.LastModelConfigID, listSubagentModelsToolName, struct{}{})
		require.False(t, resp.IsError, "expected success but got: %s", resp.Content)
		var result struct {
			Models []struct {
				ModelConfigID uuid.UUID `json:"model_config_id"`
				Aliases       []string  `json:"aliases"`
			} `json:"models"`
		}
		require.NoError(t, json.Unmarshal([]byte(resp.Content), &result))
		aliasesByID := make(map[uuid.UUID][]string, len(result.Models))
		for _, model := range result.Models {
			aliasesByID[model.ModelConfigID] = model.Aliases
		}
		require.Equal(t, []string{"wide"}, aliasesByID[googleModel.ID])
		require.Contains(t, aliasesByID, f.otherModel.ID)
		require.Nil(t, aliasesByID[f.otherModel.ID])
	})
}
