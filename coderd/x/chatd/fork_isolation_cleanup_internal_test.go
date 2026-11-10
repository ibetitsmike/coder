package chatd

// This file is fork code: it tests the worktree cleanup that follows a family
// archive behind the fork-subagent-isolation experiment, and the stop of
// its subagents' workspaces.

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbfake"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/testutil"
)

// cleanupWorktreeConn creates a worktree at /wt/<name> for each child and
// answers each delete with the error failDelete set for its path.
type cleanupWorktreeConn struct {
	workspacesdk.AgentConn

	mu         sync.Mutex
	deleteErrs map[string]error
	deletes    []workspacesdk.ForkDeleteWorktreeRequest
}

func (c *cleanupWorktreeConn) ForkCreateWorktree(_ context.Context, req workspacesdk.ForkCreateWorktreeRequest) (workspacesdk.ForkWorktree, error) {
	return workspacesdk.ForkWorktree{Path: "/wt/" + req.Name, Branch: req.Branch}, nil
}

func (c *cleanupWorktreeConn) ForkDeleteWorktree(_ context.Context, req workspacesdk.ForkDeleteWorktreeRequest) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deletes = append(c.deletes, req)
	return c.deleteErrs[req.Path]
}

func (c *cleanupWorktreeConn) failDelete(path string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.deleteErrs == nil {
		c.deleteErrs = map[string]error{}
	}
	c.deleteErrs[path] = err
}

func (c *cleanupWorktreeConn) deleted() []workspacesdk.ForkDeleteWorktreeRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]workspacesdk.ForkDeleteWorktreeRequest(nil), c.deletes...)
}

// archiveAndAwaitNotices archives root and returns the user-visible system
// messages on root once wantDeletes deletes reached conn. It checks that the
// notice left root archived and in the same status.
func (f *isolationFixture) archiveAndAwaitNotices(t *testing.T, root database.Chat, conn *cleanupWorktreeConn, wantDeletes int) []string {
	t.Helper()
	ctx := testutil.Context(t, testutil.WaitMedium)
	// Archiving needs every family member at rest.
	family := []uuid.UUID{root.ID}
	for _, row := range f.children(t, root) {
		family = append(family, row.Chat.ID)
	}
	for _, id := range family {
		_, err := f.db.UpdateChatStatus(ctx, database.UpdateChatStatusParams{ID: id, Status: database.ChatStatusWaiting})
		require.NoError(t, err)
	}
	require.NoError(t, f.server.ArchiveChat(ctx, root))
	archived, err := f.db.GetChatByID(ctx, root.ID)
	require.NoError(t, err)

	var notices []string
	testutil.Eventually(ctx, t, func(ctx context.Context) bool {
		if len(conn.deleted()) < wantDeletes {
			return false
		}
		messages, err := f.db.GetChatMessagesByChatID(ctx, database.GetChatMessagesByChatIDParams{ChatID: root.ID})
		require.NoError(t, err)
		notices = nil
		for _, msg := range messages {
			if msg.Role == database.ChatMessageRoleSystem && msg.Visibility == database.ChatMessageVisibilityUser {
				notices = append(notices, string(msg.Content.RawMessage))
			}
		}
		return len(notices) > 0
	}, testutil.IntervalFast)

	after, err := f.db.GetChatByID(ctx, root.ID)
	require.NoError(t, err)
	require.True(t, after.Archived)
	require.Equal(t, archived.Status, after.Status)
	return notices
}

func TestForkArchiveWorktreeCleanup(t *testing.T) {
	t.Parallel()

	t.Run("RemovesCleanWorktreesAndListsKept", func(t *testing.T) {
		t.Parallel()
		f := newIsolationFixture(t, codersdk.Experiments{codersdk.ExperimentForkSubagentIsolation})
		conn := &cleanupWorktreeConn{}
		root := f.rootChat(t, conn)
		worktree := map[string]any{"isolation": "worktree"}
		clean := f.spawnChild(t, root, worktree)
		dirty := f.spawnChild(t, root, worktree)
		gone := f.spawnChild(t, root, worktree)
		oldAgent := f.spawnChild(t, root, worktree)
		dropped := f.spawnChild(t, root, worktree)
		f.spawnChild(t, root, map[string]any{})

		path := func(chat database.Chat) string { return "/wt/" + chat.ID.String() }
		conn.failDelete(path(dirty), codersdk.NewError(http.StatusConflict, codersdk.Response{
			Message: "worktree " + path(dirty) + " has 2 uncommitted or untracked changes; commit or discard them first",
		}))
		conn.failDelete(path(gone), codersdk.NewError(http.StatusNotFound, codersdk.Response{Message: "worktree " + path(gone) + " does not exist."}))
		conn.failDelete(path(oldAgent), codersdk.NewError(http.StatusNotFound, codersdk.Response{Message: "Route not found."}))
		conn.failDelete(path(dropped), xerrors.New("connection reset by peer"))

		notices := f.archiveAndAwaitNotices(t, root, conn, 5)
		deleted := map[string]string{}
		for _, req := range conn.deleted() {
			deleted[req.Path] = req.Branch
		}
		require.Len(t, deleted, 5)
		for _, child := range []database.Chat{clean, dirty, gone, oldAgent, dropped} {
			row, err := f.fdb.GetChatSubagentByChatID(chatdTestContext(t), child.ID)
			require.NoError(t, err)
			require.Equal(t, row.Branch, deleted[path(child)])
		}

		require.Len(t, notices, 1)
		notice := notices[0]
		require.Contains(t, notice, "kept 3 subagent git worktree(s)")
		require.Contains(t, notice, path(dirty)+" has 2 uncommitted or untracked changes")
		require.Contains(t, notice, "`"+path(oldAgent)+"`")
		require.Contains(t, notice, "older than the Coder server")
		require.Contains(t, notice, "`"+path(dropped)+"` on branch")
		require.Contains(t, notice, ": agent unreachable")
		require.NotContains(t, notice, path(clean))
		require.NotContains(t, notice, path(gone))
	})

	t.Run("AgentUnreachable", func(t *testing.T) {
		t.Parallel()
		f := newIsolationFixture(t, codersdk.Experiments{codersdk.ExperimentForkSubagentIsolation, codersdk.ExperimentForkNestedSubagents})
		conn := &cleanupWorktreeConn{}
		root := f.rootChat(t, conn)
		child := f.spawnChild(t, root, map[string]any{"isolation": "worktree"})
		f.conns.Delete(root.AgentID.UUID)

		notices := f.archiveAndAwaitNotices(t, root, conn, 0)
		require.Empty(t, conn.deleted())
		require.Len(t, notices, 1)
		require.Contains(t, notices[0], "`/wt/"+child.ID.String()+"`")
		require.Contains(t, notices[0], ": agent unreachable")
	})

	t.Run("AgentDialTimesOut", func(t *testing.T) {
		t.Parallel()
		f := newIsolationFixture(t, codersdk.Experiments{codersdk.ExperimentForkSubagentIsolation})
		conn := &cleanupWorktreeConn{}
		root := f.rootChat(t, conn)
		child := f.spawnChild(t, root, map[string]any{"isolation": "worktree"})
		// Like a dial to an offline agent, this one ends only with its context.
		f.server.dialTimeout = testutil.IntervalFast
		f.server.agentConnFn = func(ctx context.Context, _ uuid.UUID) (workspacesdk.AgentConn, func(), error) {
			<-ctx.Done()
			return nil, nil, ctx.Err()
		}

		notices := f.archiveAndAwaitNotices(t, root, conn, 0)
		require.Empty(t, conn.deleted())
		require.Len(t, notices, 1)
		require.Contains(t, notices[0], "`/wt/"+child.ID.String()+"`")
		require.Contains(t, notices[0], ": agent unreachable")
	})
}

func TestForkArchiveStopsSubagentWorkspaces(t *testing.T) {
	t.Parallel()
	isolated := map[string]any{"isolation": "workspace"}

	// archive archives the family of root once its members are at rest, and
	// waits for the cleanup that follows.
	archive := func(t *testing.T, f *isolationFixture, root database.Chat, members ...database.Chat) {
		t.Helper()
		ctx := chatdTestContext(t)
		require.NoError(t, WaitUntilIdleForTest(ctx, f.server))
		for _, chat := range append([]database.Chat{root}, members...) {
			_, err := f.db.UpdateChatStatus(ctx, database.UpdateChatStatusParams{ID: chat.ID, Status: database.ChatStatusWaiting})
			require.NoError(t, err)
		}
		require.NoError(t, f.server.ArchiveChat(ctx, root))
		// The cleanup runs in the background after the archive commits.
		f.server.drainInflight()
	}

	t.Run("StopsRunningWorkspaces", func(t *testing.T) {
		t.Parallel()
		f := newIsolationFixture(t, codersdk.Experiments{codersdk.ExperimentForkSubagentIsolation})
		workspaces := &fakeWorkspaces{t: t, db: f.db}
		root, _ := f.workspaceRoot(t, workspaces)
		running := f.spawnChild(t, root, isolated)
		stopped := f.spawnChild(t, root, isolated)
		shared := f.spawnChild(t, root, map[string]any{})
		workspace, err := f.db.GetWorkspaceByID(chatdTestContext(t), stopped.WorkspaceID.UUID)
		require.NoError(t, err)
		dbfake.WorkspaceBuild(t, f.db, workspace.WorkspaceTable()).
			Seed(database.WorkspaceBuild{BuildNumber: 2, Transition: database.WorkspaceTransitionStop}).
			Do()

		archive(t, f, root, running, stopped, shared)

		workspaces.mu.Lock()
		defer workspaces.mu.Unlock()
		require.Equal(t, []uuid.UUID{running.WorkspaceID.UUID}, workspaces.stops)
	})

	t.Run("AfterWorktreeRemoval", func(t *testing.T) {
		t.Parallel()
		f := newIsolationFixture(t, codersdk.Experiments{codersdk.ExperimentForkSubagentIsolation, codersdk.ExperimentForkNestedSubagents})
		ctx := chatdTestContext(t)
		conn := &cleanupWorktreeConn{}
		workspaces := &fakeWorkspaces{t: t, db: f.db, conn: conn}
		root, _ := f.workspaceRoot(t, workspaces)
		child := f.spawnChild(t, root, isolated)
		// The child's first turn binds it to the agent of its workspace.
		workspaces.mu.Lock()
		agentID := workspaces.agents[child.WorkspaceID.UUID][0]
		workspaces.mu.Unlock()
		child, err := f.db.UpdateChatWorkspaceBinding(ctx, database.UpdateChatWorkspaceBindingParams{
			ID:          child.ID,
			WorkspaceID: child.WorkspaceID,
			BuildID:     child.BuildID,
			AgentID:     uuid.NullUUID{UUID: agentID, Valid: true},
		})
		require.NoError(t, err)
		grandchild := f.spawnChild(t, child, map[string]any{"isolation": "worktree", "repo_path": "/repo"})

		archive(t, f, root, child, grandchild)

		deleted := conn.deleted()
		require.Len(t, deleted, 1)
		require.Equal(t, "/wt/"+grandchild.ID.String(), deleted[0].Path)
		workspaces.mu.Lock()
		defer workspaces.mu.Unlock()
		require.Equal(t, []uuid.UUID{child.WorkspaceID.UUID}, workspaces.stops)
	})

	t.Run("SkipsReboundChild", func(t *testing.T) {
		t.Parallel()
		f := newIsolationFixture(t, codersdk.Experiments{codersdk.ExperimentForkSubagentIsolation})
		ctx := chatdTestContext(t)
		workspaces := &fakeWorkspaces{t: t, db: f.db}
		root, _ := f.workspaceRoot(t, workspaces)
		child := f.spawnChild(t, root, isolated)
		child, err := f.db.UpdateChatWorkspaceBinding(ctx, database.UpdateChatWorkspaceBindingParams{
			ID:          child.ID,
			WorkspaceID: root.WorkspaceID,
			BuildID:     root.BuildID,
			AgentID:     root.AgentID,
		})
		require.NoError(t, err)

		archive(t, f, root, child)

		workspaces.mu.Lock()
		defer workspaces.mu.Unlock()
		require.Empty(t, workspaces.stops)
	})
}
