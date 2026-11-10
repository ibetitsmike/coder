package agent_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/agentsdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/testutil"
)

// The agent roots worktrees at $HOME/.coder/worktrees, and parallel tests
// cannot redirect HOME, so this covers the routes and client error mapping
// without creating worktrees. agentgit tests cover create and delete.
func TestForkWorktreeAPI(t *testing.T) {
	t.Parallel()
	ctx := testutil.Context(t, testutil.WaitLong)
	//nolint:dogsled
	conn, _, _, _, _ := setupAgent(t, agentsdk.Manifest{}, 0)
	wtConn, ok := conn.(workspacesdk.ForkWorktreeConn)
	require.True(t, ok, "AgentConn must implement ForkWorktreeConn")

	_, err := wtConn.ForkCreateWorktree(ctx, workspacesdk.ForkCreateWorktreeRequest{
		RepoPath: "relative/repo",
		Name:     "child",
		Branch:   "feature",
	})
	var sdkErr *codersdk.Error
	require.ErrorAs(t, err, &sdkErr)
	require.Equal(t, http.StatusBadRequest, sdkErr.StatusCode())
	require.Contains(t, sdkErr.Message, "must be an absolute path")

	err = wtConn.ForkDeleteWorktree(ctx, workspacesdk.ForkDeleteWorktreeRequest{Path: t.TempDir()})
	require.ErrorAs(t, err, &sdkErr)
	require.Equal(t, http.StatusBadRequest, sdkErr.StatusCode())
	require.Contains(t, sdkErr.Message, "is not a worktree directory")
}
