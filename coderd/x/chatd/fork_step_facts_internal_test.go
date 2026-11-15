package chatd

// This file is fork code: it tests that one generation step reads the
// per-chat facts of the fork hooks once.

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sqlc-dev/pqtype"
	"github.com/stretchr/testify/require"

	"cdr.dev/slog/v3/sloggers/slogtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbmetrics"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

// queryCounts returns the number of calls of each store query that reg has
// counted.
func queryCounts(t *testing.T, reg *prometheus.Registry) map[string]int {
	t.Helper()
	families, err := reg.Gather()
	require.NoError(t, err)
	counts := map[string]int{}
	for _, family := range families {
		if family.GetName() != "coderd_db_query_counts_total" {
			continue
		}
		for _, m := range family.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "query" {
					counts[l.GetValue()] += int(m.GetCounter().GetValue())
				}
			}
		}
	}
	return counts
}

// stepFactsFixture is a depth-2 shared subagent under a child spawned with
// isolation worktree, with a tool policy and two plugin packs enabled on
// the root chat. Every chat is bound to one workspace agent whose pinned
// context holds a skill of each pack.
type stepFactsFixture struct {
	server     *Server
	db         database.Store
	fdb        forkdb.Store
	reg        *prometheus.Registry
	org        database.Organization
	packs      []forkdb.PluginPack
	root       database.Chat
	grandchild database.Chat
}

func newStepFactsFixture(t *testing.T) stepFactsFixture {
	t.Helper()
	raw, ps := dbtestutil.NewDB(t)
	reg := prometheus.NewRegistry()
	db := dbmetrics.NewQueryMetrics(raw, slogtest.Make(t, nil), reg)
	server := newInternalTestServer(t, db, ps, chatprovider.ProviderAPIKeys{},
		withInternalTestServerExperiments(codersdk.Experiments{
			codersdk.ExperimentForkNestedSubagents,
			codersdk.ExperimentForkSubagentIsolation,
			codersdk.ExperimentForkAgentDefinitions,
			codersdk.ExperimentForkPluginPacks,
		}),
		// A depth-2 chat can then spawn, so the step builds spawn_agent.
		withInternalTestServerLimits(Limits{Fork: ForkLimits{MaxSubagentDepth: 3}}),
		withInternalTestServerTransportFactory(&aibridgeTestFactory{}))
	fdb, err := forkdb.From(raw)
	require.NoError(t, err)
	ctx := chatdTestContext(t)
	user, org, model := seedInternalChatDeps(t, raw)
	workspace, build, agent := seedWorkspaceBinding(t, raw, user.ID)

	files := &fakeWorkspaceFiles{files: map[string]string{}}
	server.agentConnFn = func(context.Context, uuid.UUID) (workspacesdk.AgentConn, func(), error) {
		return files.conn(t), func() {}, nil
	}

	now := dbtime.Now()
	_, err = raw.UpsertWorkspaceAgentContextSnapshot(ctx, database.UpsertWorkspaceAgentContextSnapshotParams{
		WorkspaceAgentID: agent.ID,
		Version:          1,
		AggregateHash:    []byte("step-facts"),
		ReceivedAt:       now,
	})
	require.NoError(t, err)
	var packs []forkdb.PluginPack
	for _, name := range []string{"alpha", "beta"} {
		pack, err := fdb.InsertPluginPack(ctx, forkdb.InsertPluginPackParams{
			ID: uuid.New(), OrganizationID: org.ID, Name: name, SourceUrl: "https://example.com/packs.git",
			Ref: "main", CommitSha: "0123456789abcdef0123456789abcdef01234567", CreatedBy: user.ID,
			CreatedAt: now, UpdatedAt: now,
		})
		require.NoError(t, err)
		require.NoError(t, fdb.InsertPluginPackResources(ctx, forkdb.InsertPluginPackResourcesParams{
			PackID: pack.ID, Paths: []string{".claude-plugin/plugin.json", "skills/" + name + "/SKILL.md"},
			Contents: [][]byte{[]byte("{}"), []byte("# " + name)}, ContentHashes: []string{"hash", "hash"},
		}))
		packs = append(packs, pack)
		row := packSkillRow(t, name, name)
		_, err = raw.UpsertWorkspaceAgentContextResource(ctx, database.UpsertWorkspaceAgentContextResourceParams{
			WorkspaceAgentID: agent.ID,
			Source:           row.Source,
			SourcePath:       row.SourcePath,
			BodyKind:         row.BodyKind,
			Body:             row.Body,
			ContentHash:      []byte(name),
			SizeBytes:        1,
			Status:           row.Status,
			Now:              now,
		})
		require.NoError(t, err)
	}

	create := func(title string, parent, root uuid.NullUUID) database.Chat {
		chat, err := server.CreateChat(ctx, CreateOptions{
			OrganizationID:     org.ID,
			OwnerID:            user.ID,
			WorkspaceID:        uuid.NullUUID{UUID: workspace.ID, Valid: true},
			BuildID:            uuid.NullUUID{UUID: build.ID, Valid: true},
			AgentID:            uuid.NullUUID{UUID: agent.ID, Valid: true},
			ParentChatID:       parent,
			RootChatID:         root,
			Title:              title,
			ModelConfigID:      model.ID,
			InitialUserContent: []codersdk.ChatMessagePart{codersdk.ChatMessageText("hello")},
		})
		require.NoError(t, err)
		chat, err = raw.GetChatByID(ctx, chat.ID)
		require.NoError(t, err)
		return chat
	}
	root := create("root", uuid.NullUUID{}, uuid.NullUUID{})
	for _, pack := range packs {
		_, err := fdb.InsertChatPluginPack(ctx, forkdb.InsertChatPluginPackParams{ChatID: root.ID, PackID: pack.ID})
		require.NoError(t, err)
	}
	rootID := uuid.NullUUID{UUID: root.ID, Valid: true}
	child := create("child", rootID, rootID)
	dbgen.ChatSubagent(t, raw, forkdb.ChatSubagent{
		ChatID: child.ID, Depth: 1, Isolation: subagentIsolationWorktree,
		WorktreePath: "/home/coder/repo-wt", Branch: "agent/child",
	})
	grandchild := create("grandchild", uuid.NullUUID{UUID: child.ID, Valid: true}, rootID)
	policy, err := json.Marshal(customAgentToolPolicy{DisallowedTools: []string{"process_signal"}, MCPMode: "inherit"})
	require.NoError(t, err)
	dbgen.ChatSubagent(t, raw, forkdb.ChatSubagent{
		ChatID: grandchild.ID, Depth: 2, Isolation: subagentIsolationShared,
		ToolPolicy: pqtype.NullRawMessage{RawMessage: policy, Valid: true},
	})
	return stepFactsFixture{server: server, db: raw, fdb: fdb, reg: reg, org: org, packs: packs, root: root, grandchild: grandchild}
}

// step runs one generation step of chat and returns its tool names.
func (f stepFactsFixture) step(t *testing.T, chat database.Chat) []string {
	t.Helper()
	ctx := chatdTestContext(t)
	messages, err := f.db.GetChatMessagesByChatID(ctx, database.GetChatMessagesByChatIDParams{ChatID: chat.ID})
	require.NoError(t, err)
	env, err := f.server.buildTurnEnvironment(ctx, generationPrepareInput{Chat: chat, Messages: messages})
	require.NoError(t, err)
	env.Close()
	return toolNames(env.Tools)
}

// TestForkStepFactsReadOnce checks that one generation step of a subagent
// in a worktree with a tool policy and enabled plugin packs reads each
// per-chat fact of the fork hooks once.
func TestForkStepFactsReadOnce(t *testing.T) {
	t.Parallel()
	f := newStepFactsFixture(t)

	// The first step pins the chat's context and writes the packs to the
	// workspace, so the second is the steady state of a turn.
	f.step(t, f.grandchild)
	before := queryCounts(t, f.reg)
	tools := f.step(t, f.grandchild)
	after := queryCounts(t, f.reg)
	diff := map[string]int{}
	for query, n := range after {
		if d := n - before[query]; d > 0 {
			diff[query] = d
		}
	}
	t.Logf("store calls of one step: %v", diff)

	require.Contains(t, tools, spawnAgentToolName, "the step reads the spawn facts")
	require.NotContains(t, tools, "process_signal", "the step applies the tool policy")
	// The worktree walk reads the row of the chat and of its parent, which
	// holds the worktree. The depth and the tool policy reuse the first.
	require.Equal(t, 2, diff["GetChatSubagentByChatID"], "chat_subagents rows")
	require.Equal(t, 1, diff["GetChatOrganizationSubagentLimits"], "organization subagent limits")
	// Enablement reads the packs and exclusions of the chat and its two
	// ancestors once.
	require.Equal(t, 3, diff["GetPluginPacksByChatID"], "chat plugin packs")
	require.Equal(t, 3, diff["GetExcludedPluginPacksByChatID"], "excluded chat plugin packs")
	// On main the ancestor walks of the worktree and of four pack
	// enablements read 16 chats.
	require.LessOrEqual(t, diff["GetChatByID"], 6, "chats")
}

// TestForkStepFactsFirstReadWins checks the rule for a fact that changes
// during a step: the step keeps the value of its first lookup, other chats
// read live, and once the step is built every lookup reads live.
func TestForkStepFactsFirstReadWins(t *testing.T) {
	t.Parallel()
	f := newStepFactsFixture(t)
	ctx := chatdTestContext(t)
	seal := f.server.withForkStepFacts(&ctx, f.grandchild)

	maxDepth := func() int {
		limits, err := f.server.stepSubagentLimits(ctx, f.fdb, f.org.ID)
		require.NoError(t, err)
		return limits.maxDepth
	}
	packNames := func(chat database.Chat) []string {
		var names []string
		for _, pack := range f.server.enabledPacks(ctx, chat) {
			names = append(names, pack.Name)
		}
		return names
	}
	require.Equal(t, 3, maxDepth())
	require.ElementsMatch(t, []string{"alpha", "beta"}, packNames(f.grandchild))

	_, err := f.fdb.UpsertChatOrganizationSubagentLimits(ctx, forkdb.UpsertChatOrganizationSubagentLimitsParams{
		OrganizationID: f.org.ID,
		MaxDepth:       sql.NullInt32{Int32: 1, Valid: true},
	})
	require.NoError(t, err)
	_, err = f.fdb.InsertChatExcludedPluginPack(ctx, forkdb.InsertChatExcludedPluginPackParams{ChatID: f.grandchild.ID, PackID: f.packs[0].ID})
	require.NoError(t, err)
	_, err = f.fdb.InsertChatExcludedPluginPack(ctx, forkdb.InsertChatExcludedPluginPackParams{ChatID: f.root.ID, PackID: f.packs[1].ID})
	require.NoError(t, err)

	require.Equal(t, 3, maxDepth(), "the step keeps its first limits")
	require.ElementsMatch(t, []string{"alpha", "beta"}, packNames(f.grandchild), "the step keeps its first packs")
	require.Equal(t, []string{"alpha"}, packNames(f.root), "another chat reads live")

	seal()
	require.Equal(t, 1, maxDepth(), "a sealed step reads live")
	require.Empty(t, packNames(f.grandchild), "a sealed step reads live")
}
