package chatd

// This file is fork code: it tests that a replayed spawn_agent call returns
// the child it already created (fork_spawn_replay.go).

import (
	"context"
	"database/sql"
	"net/http"
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

// TestForkSpawnAgentReplayRetiredChild checks that a replay does not return a
// child that an earlier run left unusable: an archived child, or a workspace
// child whose spawn stopped before it got its workspace and prompt. The
// replay spawns a new child at the next derived ID, and a later replay
// returns that one.
func TestForkSpawnAgentReplayRetiredChild(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		retire string
	}{
		{name: "Archived", retire: `UPDATE chats SET archived = true WHERE id = $1`},
		// startSubagentWorkspace has not run: the child waits with no
		// workspace and no prompt.
		{name: "UnstartedWorkspace", retire: `WITH s AS (UPDATE chat_subagents SET isolation = 'workspace', workspace_id = NULL WHERE chat_id = $1)
			UPDATE chats SET status = 'waiting' WHERE id = $1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newNestedSubagentsFixture(t, codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}, Limits{})
			ctx := chatdTestContext(t)
			tools, err := f.server.subagentTools(ctx, func() database.Chat { return f.root }, f.root.LastModelConfigID)
			require.NoError(t, err)
			tools, err = f.server.appendForkTools(ctx, tools, f.root, f.root.LastModelConfigID)
			require.NoError(t, err)
			callID := uuid.New()
			spawn := func() uuid.UUID {
				t.Helper()
				resp, err := findToolByName(tools, spawnAgentToolName).Run(workspacesdk.WithToolCallID(ctx, callID),
					fantasy.ToolCall{Input: `{"type":"general","prompt":"do the work"}`})
				require.NoError(t, err)
				id, ok := requireToolResponseMap(t, resp, false)["chat_id"].(string)
				require.True(t, ok, resp.Content)
				return uuid.MustParse(id)
			}

			first := spawn()
			require.Equal(t, uuid.NewSHA1(callID, []byte(spawnAgentToolName)), first)
			_, err = f.sqlDB.ExecContext(ctx, tc.retire, first)
			require.NoError(t, err)

			second := spawn()
			require.Equal(t, uuid.NewSHA1(first, []byte(spawnAgentToolName)), second)
			require.Equal(t, second, spawn(), "a later replay returns the new child")
			old, err := f.db.GetChatByID(ctx, first)
			require.NoError(t, err)
			require.True(t, old.Archived, "the unusable child is archived")
			rows, err := f.db.GetChildChatsByParentIDs(ctx, database.GetChildChatsByParentIDsParams{ParentIds: []uuid.UUID{f.root.ID}})
			require.NoError(t, err)
			require.Len(t, rows, 2)
		})
	}
}

// conflictWorktreeConn answers the worktree create of one child chat ID with
// the conflict that an existing worktree gets.
type conflictWorktreeConn struct {
	*fakeWorktreeConn
	conflictName string
}

func (c *conflictWorktreeConn) ForkCreateWorktree(ctx context.Context, req workspacesdk.ForkCreateWorktreeRequest) (workspacesdk.ForkWorktree, error) {
	worktree, err := c.fakeWorktreeConn.ForkCreateWorktree(ctx, req)
	if req.Name == c.conflictName {
		return workspacesdk.ForkWorktree{}, codersdk.NewError(http.StatusConflict, codersdk.Response{Message: "worktree path already exists."})
	}
	return worktree, err
}

// TestForkSpawnAgentReplayWorktreeConflict checks that a replay whose child
// worktree an earlier run created, before that run's child existed, spawns
// the child at the next derived ID with a worktree of its own.
func TestForkSpawnAgentReplayWorktreeConflict(t *testing.T) {
	t.Parallel()

	f := newIsolationFixture(t, codersdk.Experiments{codersdk.ExperimentForkNestedSubagents, codersdk.ExperimentForkSubagentIsolation})
	ctx := chatdTestContext(t)
	callID := uuid.New()
	first := uuid.NewSHA1(callID, []byte(spawnAgentToolName))
	conn := &conflictWorktreeConn{
		fakeWorktreeConn: &fakeWorktreeConn{worktree: workspacesdk.ForkWorktree{Path: "/home/coder/.coder/worktrees/child", Branch: "agent/child"}},
		conflictName:     first.String(),
	}
	root := f.rootChat(t, conn)

	resp, err := f.spawnTool(t, root).Run(workspacesdk.WithToolCallID(ctx, callID),
		fantasy.ToolCall{Input: `{"type":"general","prompt":"do the work","isolation":"worktree"}`})
	require.NoError(t, err)
	result := requireToolResponseMap(t, resp, false)
	second := uuid.NewSHA1(first, []byte(spawnAgentToolName))
	require.Equal(t, second.String(), result["chat_id"], resp.Content)
	require.Equal(t, "worktree", result["isolation"])

	creates, deletes := conn.calls()
	require.Len(t, creates, 2)
	require.Equal(t, first.String(), creates[0].Name)
	require.Equal(t, second.String(), creates[1].Name)
	require.Empty(t, deletes, "the replay leaves the earlier worktree alone")
	_, err = f.db.GetChatByID(ctx, first)
	require.ErrorIs(t, err, sql.ErrNoRows)
}

// TestForkSpawnAgentReplayTakenOver checks that a turn whose chat another
// run took over creates no child: the run that took over replays the call
// and creates the child itself.
func TestForkSpawnAgentReplayTakenOver(t *testing.T) {
	t.Parallel()

	f := newNestedSubagentsFixture(t, codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}, Limits{})
	ctx := chatdTestContext(t)
	workerID, runnerID := uuid.New(), uuid.New()
	_, err := f.sqlDB.ExecContext(ctx, `UPDATE chats SET worker_id = $2, runner_id = $3 WHERE id = $1`, f.root.ID, workerID, runnerID)
	require.NoError(t, err)
	children := func() int {
		t.Helper()
		rows, err := f.db.GetChildChatsByParentIDs(ctx, database.GetChildChatsByParentIDsParams{ParentIds: []uuid.UUID{f.root.ID}})
		require.NoError(t, err)
		return len(rows)
	}
	spawn := func(runner uuid.UUID) fantasy.ToolResponse {
		t.Helper()
		chat := f.root
		chat.WorkerID = uuid.NullUUID{UUID: workerID, Valid: true}
		chat.RunnerID = uuid.NullUUID{UUID: runner, Valid: true}
		tools, err := f.server.subagentTools(ctx, func() database.Chat { return chat }, chat.LastModelConfigID)
		require.NoError(t, err)
		tools, err = f.server.appendForkTools(ctx, tools, chat, chat.LastModelConfigID)
		require.NoError(t, err)
		resp, err := findToolByName(tools, spawnAgentToolName).Run(workspacesdk.WithToolCallID(ctx, uuid.New()),
			fantasy.ToolCall{Input: `{"type":"general","prompt":"do the work"}`})
		require.NoError(t, err)
		return resp
	}

	resp := spawn(uuid.New())
	require.True(t, resp.IsError, "a turn of an earlier runner creates no child: %s", resp.Content)
	require.Equal(t, 0, children())

	resp = spawn(runnerID)
	require.False(t, resp.IsError, resp.Content)
	require.Equal(t, 1, children())
}

// TestForkSpawnAgentReplayRetiredWhileStarting checks the retirement of an
// unstarted workspace child against the run that still starts it. A child
// that a replay archived before its workspace was bound gets no prompt, and
// its workspace is stopped. A child whose workspace was recorded is kept.
func TestForkSpawnAgentReplayRetiredWhileStarting(t *testing.T) {
	t.Parallel()

	t.Run("ArchivedBeforeBind", func(t *testing.T) {
		t.Parallel()
		f := newIsolationFixture(t, codersdk.Experiments{codersdk.ExperimentForkNestedSubagents, codersdk.ExperimentForkSubagentIsolation})
		ctx := chatdTestContext(t)
		workspaces := &fakeWorkspaces{t: t, db: f.db}
		// The replay retires the child while the original run creates its
		// workspace.
		workspaces.beforeCreate = func(chatID uuid.UUID) {
			kept, err := f.server.discardSubagentUnless(ctx, chatID, forkSubagentStarted(chatID))
			require.NoError(t, err)
			require.False(t, kept)
		}
		root, _ := f.workspaceRoot(t, workspaces)

		resp := f.spawn(t, root, map[string]any{"isolation": "workspace"})
		require.True(t, resp.IsError, resp.Content)
		require.Contains(t, resp.Content, "archived meanwhile")
		children := f.children(t, root)
		require.Len(t, children, 1)
		child, err := f.db.GetChatByID(ctx, children[0].Chat.ID)
		require.NoError(t, err)
		require.True(t, child.Archived)
		queued, err := f.db.GetChatQueuedMessages(ctx, child.ID)
		require.NoError(t, err)
		require.Empty(t, queued, "the archived child gets no prompt")

		f.server.drainInflight()
		workspaces.mu.Lock()
		defer workspaces.mu.Unlock()
		require.Len(t, workspaces.created, 1)
		require.Equal(t, []uuid.UUID{workspaces.created[0].ID}, workspaces.stops)
	})

	t.Run("KeepsStarted", func(t *testing.T) {
		t.Parallel()
		f := newIsolationFixture(t, codersdk.Experiments{codersdk.ExperimentForkNestedSubagents, codersdk.ExperimentForkSubagentIsolation})
		ctx := chatdTestContext(t)
		workspaces := &fakeWorkspaces{t: t, db: f.db}
		root, _ := f.workspaceRoot(t, workspaces)

		resp := f.spawn(t, root, map[string]any{"isolation": "workspace"})
		require.False(t, resp.IsError, resp.Content)
		childID := f.children(t, root)[0].Chat.ID
		kept, err := f.server.discardSubagentUnless(ctx, childID, forkSubagentStarted(childID))
		require.NoError(t, err)
		require.True(t, kept)
		child, err := f.db.GetChatByID(ctx, childID)
		require.NoError(t, err)
		require.False(t, child.Archived)
	})
}

// TestForkSpawnAgentReplayConcurrentRun checks that a run whose spawn fails
// because another run of the same call created the child meanwhile returns
// that child.
func TestForkSpawnAgentReplayConcurrentRun(t *testing.T) {
	t.Parallel()

	f := newNestedSubagentsFixture(t, codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}, Limits{})
	ctx := chatdTestContext(t)
	callID := uuid.New()
	childID := uuid.NewSHA1(callID, []byte(spawnAgentToolName))
	// The inner spawn stands in for the losing run: the other run inserts
	// the child first, so this run's insert fails.
	inner := fantasy.NewAgentTool(spawnAgentToolName, "", func(ctx context.Context, _ struct{}, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
		_, err := f.server.createChildSubagentChatWithOptions(ctx, f.root, "do the work", "", childSubagentChatOptions{})
		require.NoError(t, err)
		return fantasy.NewTextErrorResponse("create child chat: duplicate key value violates unique constraint \"chats_pkey\""), nil
	})
	tool := forkReplaySafeSpawnTool{AgentTool: inner, server: f.server, chat: f.root}

	resp, err := tool.Run(workspacesdk.WithToolCallID(ctx, callID), fantasy.ToolCall{Input: `{}`})
	require.NoError(t, err)
	require.Equal(t, childID.String(), requireToolResponseMap(t, resp, false)["chat_id"], resp.Content)
}
