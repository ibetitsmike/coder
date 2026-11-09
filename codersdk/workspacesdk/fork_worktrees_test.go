package workspacesdk_test

// This file is fork code: it tests the client for the fork-only agent
// worktree API.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/tailnet"
	"github.com/coder/coder/v2/tailnet/tailnettest"
	"github.com/coder/coder/v2/testutil"
)

// The agent roots worktrees at $HOME, so agent tests cannot reach its
// success responses; this pins the client side of the 201 and 204 contract
// that agentgit tests pin on the server side.
func TestAgentConnForkWorktrees(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	derpMap, _ := tailnettest.RunDERPAndSTUN(t)
	clientID := uuid.New()
	agentID := uuid.New()
	clientConn, _ := newTailnetConn(t, derpMap, clientID, "client")
	agentTailnetConn, agentIP := newTailnetConn(t, derpMap, agentID, "agent")
	stitchTailnet(t, map[uuid.UUID]*tailnet.Conn{
		clientID: clientConn,
		agentID:  agentTailnetConn,
	})

	createReq := workspacesdk.ForkCreateWorktreeRequest{
		RepoPath: "/home/coder/repo",
		Name:     "child",
		Branch:   "feature",
		BaseRef:  "main",
	}
	created := workspacesdk.ForkWorktree{
		Path:       "/home/coder/.coder/worktrees/repo/child",
		Branch:     "feature",
		BaseCommit: "0123456789abcdef0123456789abcdef01234567",
		RepoRoot:   "/home/coder/repo",
	}
	deleteReq := workspacesdk.ForkDeleteWorktreeRequest{Path: created.Path}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v0/git/worktrees", func(w http.ResponseWriter, r *http.Request) {
		var got workspacesdk.ForkCreateWorktreeRequest
		if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&got)) {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		assert.Equal(t, createReq, got)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(created)
	})
	mux.HandleFunc("DELETE /api/v0/git/worktrees", func(w http.ResponseWriter, r *http.Request) {
		var got workspacesdk.ForkDeleteWorktreeRequest
		if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&got)) {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		assert.Equal(t, deleteReq, got)
		w.WriteHeader(http.StatusNoContent)
	})
	serveTailnetHTTP(t, agentTailnetConn, mux)

	conn := workspacesdk.NewAgentConn(clientConn, workspacesdk.AgentConnOptions{
		AgentID: agentID,
	})
	require.True(t, clientConn.AwaitReachable(ctx, agentIP))
	wtConn, ok := conn.(workspacesdk.ForkWorktreeConn)
	require.True(t, ok, "AgentConn must implement ForkWorktreeConn")

	wt, err := wtConn.ForkCreateWorktree(ctx, createReq)
	require.NoError(t, err)
	require.Equal(t, created, wt)

	require.NoError(t, wtConn.ForkDeleteWorktree(ctx, deleteReq))
}
