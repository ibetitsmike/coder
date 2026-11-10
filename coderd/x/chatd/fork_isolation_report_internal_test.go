package chatd

// This file is fork code: it tests the worktree report that the
// fork-subagent-isolation experiment adds to wait_agent.

import (
	"encoding/json"
	"net/http"
	"testing"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

func TestForkIsolationWaitAgentReport(t *testing.T) {
	t.Parallel()

	// With nested subagents on, a shared child also has a chat_subagents
	// row, so the shared case proves the report checks its isolation.
	f := newIsolationFixture(t, codersdk.Experiments{codersdk.ExperimentForkSubagentIsolation, codersdk.ExperimentForkNestedSubagents})
	returned := workspacesdk.ForkWorktree{Path: "/home/coder/.coder/worktrees/repo/child", Branch: "agent/child"}
	summary := workspacesdk.ForkWorktreeSummary{Commits: 2, FilesChanged: 3, Insertions: 4, Deletions: 1, UncommittedChanges: 2}

	for _, tc := range []struct {
		name      string
		conn      *fakeWorktreeConn
		isolation string
		// want is the wait_agent result without its upstream fields.
		want map[string]any
	}{
		{
			name:      "Worktree",
			conn:      &fakeWorktreeConn{worktree: returned, summary: summary},
			isolation: subagentIsolationWorktree,
			want: map[string]any{
				"isolation":     "worktree",
				"branch":        returned.Branch,
				"worktree_path": returned.Path,
				"diff_summary": map[string]any{
					"commits": 2.0, "files_changed": 3.0, "insertions": 4.0, "deletions": 1.0, "uncommitted_changes": 2.0,
				},
			},
		},
		{
			name: "AgentError",
			conn: &fakeWorktreeConn{worktree: returned, summaryErr: codersdk.NewError(http.StatusNotFound, codersdk.Response{
				Message: "worktree " + returned.Path + " does not exist.",
			})},
			isolation: subagentIsolationWorktree,
			want: map[string]any{
				"isolation":          "worktree",
				"branch":             returned.Branch,
				"worktree_path":      returned.Path,
				"diff_summary_error": "summarize worktree: worktree " + returned.Path + " does not exist",
			},
		},
		{
			name:      "Shared",
			conn:      &fakeWorktreeConn{},
			isolation: subagentIsolationShared,
			want:      map[string]any{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := chatdTestContext(t)
			root := f.rootChat(t, tc.conn)
			resp := f.spawn(t, root, map[string]any{"isolation": tc.isolation})
			require.False(t, resp.IsError, resp.Content)
			var spawned struct {
				ChatID string `json:"chat_id"`
			}
			require.NoError(t, json.Unmarshal([]byte(resp.Content), &spawned))
			childID, err := uuid.Parse(spawned.ChatID)
			require.NoError(t, err)
			require.NoError(t, WaitUntilIdleForTest(ctx, f.server))
			setChatStatus(ctx, t, f.db, childID, database.ChatStatusWaiting, "")
			insertAssistantMessage(t, f.db, childID, f.model.ID, "task complete")

			tools, err := f.server.subagentTools(ctx, func() database.Chat { return root }, root.LastModelConfigID)
			require.NoError(t, err)
			tools, err = f.server.appendForkTools(ctx, tools, root, root.LastModelConfigID)
			require.NoError(t, err)
			wait := findToolByName(tools, waitAgentToolName)
			require.NotNil(t, wait)
			input, err := json.Marshal(map[string]any{"chat_id": childID.String()})
			require.NoError(t, err)
			resp, err = wait.Run(ctx, fantasy.ToolCall{ID: uuid.NewString(), Name: waitAgentToolName, Input: string(input)})
			require.NoError(t, err)
			require.False(t, resp.IsError, resp.Content)

			var got map[string]any
			require.NoError(t, json.Unmarshal([]byte(resp.Content), &got))
			require.Equal(t, "task complete", got["report"])
			for _, key := range []string{"chat_id", "report", "status", "title", "type"} {
				delete(got, key)
			}
			require.Equal(t, tc.want, got)

			tc.conn.mu.Lock()
			defer tc.conn.mu.Unlock()
			if tc.isolation == subagentIsolationWorktree {
				require.Equal(t, []workspacesdk.ForkWorktreeSummaryRequest{{Path: returned.Path, Branch: returned.Branch}}, tc.conn.summaries)
			} else {
				require.Empty(t, tc.conn.summaries)
			}
		})
	}
}
