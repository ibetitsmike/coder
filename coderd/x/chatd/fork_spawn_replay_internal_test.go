package chatd

// This file is fork code: it tests that a replayed spawn_agent call returns
// the child it already created (fork_spawn_replay.go).

import (
	"testing"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

// TestForkSpawnAgentReplay runs spawn_agent twice with one tool call ID, as
// chatd does when it replays a tool batch that a restart cut. The replay
// must return the first child and create no other.
func TestForkSpawnAgentReplay(t *testing.T) {
	t.Parallel()

	f := newNestedSubagentsFixture(t, codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}, Limits{})
	ctx := chatdTestContext(t)
	toolsOf := func(chat database.Chat) []fantasy.AgentTool {
		t.Helper()
		var tools []fantasy.AgentTool
		if !chat.ParentChatID.Valid {
			var err error
			tools, err = f.server.subagentTools(ctx, func() database.Chat { return chat }, chat.LastModelConfigID)
			require.NoError(t, err)
		}
		tools, err := f.server.appendForkTools(ctx, tools, chat, chat.LastModelConfigID)
		require.NoError(t, err)
		require.NotNil(t, findToolByName(tools, spawnAgentToolName))
		return tools
	}
	spawn := func(tools []fantasy.AgentTool, callID uuid.UUID) string {
		t.Helper()
		resp, err := findToolByName(tools, spawnAgentToolName).Run(workspacesdk.WithToolCallID(ctx, callID),
			fantasy.ToolCall{Input: `{"type":"general","prompt":"do the work"}`})
		require.NoError(t, err)
		id, ok := requireToolResponseMap(t, resp, false)["chat_id"].(string)
		require.True(t, ok, resp.Content)
		return id
	}
	children := func(parent database.Chat) int {
		t.Helper()
		rows, err := f.db.GetChildChatsByParentIDs(ctx, database.GetChildChatsByParentIDsParams{ParentIds: []uuid.UUID{parent.ID}})
		require.NoError(t, err)
		return len(rows)
	}

	// The root gets spawn_agent from upstream, a child from the nested tools.
	rootTools := toolsOf(f.root)
	callID := uuid.New()
	first := spawn(rootTools, callID)
	require.Equal(t, first, spawn(rootTools, callID), "a replay returns the first child")
	require.Equal(t, 1, children(f.root))
	other := spawn(rootTools, uuid.New())
	require.NotEqual(t, first, other, "a new call spawns a new child")
	require.Equal(t, 2, children(f.root))

	child, err := f.db.GetChatByID(ctx, uuid.MustParse(first))
	require.NoError(t, err)
	childTools := toolsOf(child)
	childCallID := uuid.New()
	grandchild := spawn(childTools, childCallID)
	require.Equal(t, grandchild, spawn(childTools, childCallID))
	require.Equal(t, 1, children(child))

	// A tool call ID whose derived chat belongs to another parent fails
	// instead of returning that chat or creating another one.
	resp, err := findToolByName(childTools, spawnAgentToolName).Run(workspacesdk.WithToolCallID(ctx, callID),
		fantasy.ToolCall{Input: `{"type":"general","prompt":"do the work"}`})
	require.NoError(t, err)
	require.True(t, resp.IsError, resp.Content)
	require.Equal(t, 1, children(child))
}
