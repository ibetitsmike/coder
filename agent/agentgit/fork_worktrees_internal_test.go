package agentgit

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"cdr.dev/slog/v3/sloggers/slogtest"
	"github.com/coder/coder/v2/agent/agentexec"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/testutil"
)

func wtGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@test.com",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

func wtInitRepo(t *testing.T) string {
	t.Helper()
	dir := testutil.TempDirResolved(t)
	wtGit(t, dir, "init")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test\n"), 0o600))
	wtGit(t, dir, "add", "README.md")
	wtGit(t, dir, "commit", "-m", "initial commit")
	return dir
}

func wtCommit(t *testing.T, dir, content string) string {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte(content), 0o600))
	wtGit(t, dir, "commit", "-am", "edit "+content)
	return wtGit(t, dir, "rev-parse", "HEAD")
}

// repoState captures what a worktree operation must leave untouched in the
// parent checkout.
type repoState struct {
	head   string
	branch string
	status string
}

func wtRepoState(t *testing.T, dir string) repoState {
	t.Helper()
	return repoState{
		head:   wtGit(t, dir, "rev-parse", "HEAD"),
		branch: wtGit(t, dir, "symbolic-ref", "--short", "HEAD"),
		status: wtGit(t, dir, "status", "--porcelain=v1", "--untracked-files=all"),
	}
}

func wtWorktreeCount(t *testing.T, repo string) int {
	t.Helper()
	return strings.Count(wtGit(t, repo, "worktree", "list", "--porcelain"), "worktree ")
}

func newTestWorktreeAPI(t *testing.T) (http.Handler, string) {
	t.Helper()
	root := filepath.Join(testutil.TempDirResolved(t), "worktrees")
	api := &worktreeAPI{
		logger: slogtest.Make(t, nil),
		execer: agentexec.DefaultExecer,
		gitBin: "git",
		root:   root,
	}
	return api.routes(), root
}

func doWorktreeRequest(t *testing.T, h http.Handler, method string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequestWithContext(testutil.Context(t, testutil.WaitLong), method, "/", bytes.NewReader(data))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func wtCreate(t *testing.T, h http.Handler, req workspacesdk.ForkCreateWorktreeRequest) workspacesdk.ForkWorktree {
	t.Helper()
	rec := doWorktreeRequest(t, h, http.MethodPost, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var wt workspacesdk.ForkWorktree
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &wt))
	return wt
}

func wtErrorMessage(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var resp codersdk.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp), rec.Body.String())
	return resp.Message
}

func TestForkWorktreeCreate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// baseRef returns the request base_ref and the commit the
		// worktree must start at.
		baseRef func(t *testing.T, repo string) (string, string)
	}{
		{
			name: "DefaultsToHEAD",
			baseRef: func(t *testing.T, repo string) (string, string) {
				return "", wtGit(t, repo, "rev-parse", "HEAD")
			},
		},
		{
			name: "ExplicitBaseRef",
			baseRef: func(t *testing.T, repo string) (string, string) {
				wtGit(t, repo, "checkout", "-b", "other")
				sha := wtCommit(t, repo, "other\n")
				wtGit(t, repo, "checkout", "-")
				return "other", sha
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h, root := newTestWorktreeAPI(t)
			repo := wtInitRepo(t)
			baseRef, wantBase := tt.baseRef(t, repo)
			before := wtRepoState(t, repo)

			wt := wtCreate(t, h, workspacesdk.ForkCreateWorktreeRequest{
				RepoPath: repo,
				Name:     "child",
				Branch:   "feature",
				BaseRef:  baseRef,
			})

			assert.Equal(t, filepath.Join(root, filepath.Base(repo), "child"), wt.Path)
			assert.Equal(t, "feature", wt.Branch)
			assert.Equal(t, wantBase, wt.BaseCommit)
			assert.Equal(t, repo, wt.RepoRoot)
			assert.Equal(t, "feature", wtGit(t, wt.Path, "symbolic-ref", "--short", "HEAD"))
			assert.Equal(t, wantBase, wtGit(t, wt.Path, "rev-parse", "HEAD"))
			assert.Equal(t, before, wtRepoState(t, repo))
			info, err := os.Stat(root)
			require.NoError(t, err)
			assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
		})
	}
}

func TestForkWorktreeCreateRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// setup prepares repo and root and returns the request.
		setup       func(t *testing.T, repo, root string) workspacesdk.ForkCreateWorktreeRequest
		wantStatus  int
		wantMessage string
	}{
		{
			name: "RelativeRepoPath",
			setup: func(_ *testing.T, _, _ string) workspacesdk.ForkCreateWorktreeRequest {
				return workspacesdk.ForkCreateWorktreeRequest{RepoPath: "repo", Name: "child", Branch: "feature"}
			},
			wantStatus:  http.StatusBadRequest,
			wantMessage: "must be an absolute path",
		},
		{
			name: "NotAGitRepo",
			setup: func(t *testing.T, _, _ string) workspacesdk.ForkCreateWorktreeRequest {
				return workspacesdk.ForkCreateWorktreeRequest{RepoPath: testutil.TempDirResolved(t), Name: "child", Branch: "feature"}
			},
			wantStatus:  http.StatusBadRequest,
			wantMessage: "is not inside a git work tree",
		},
		{
			name: "NameTraversal",
			setup: func(_ *testing.T, repo, _ string) workspacesdk.ForkCreateWorktreeRequest {
				return workspacesdk.ForkCreateWorktreeRequest{RepoPath: repo, Name: "../x", Branch: "feature"}
			},
			wantStatus:  http.StatusBadRequest,
			wantMessage: "must be a single path segment",
		},
		{
			name: "NameWithSlash",
			setup: func(_ *testing.T, repo, _ string) workspacesdk.ForkCreateWorktreeRequest {
				return workspacesdk.ForkCreateWorktreeRequest{RepoPath: repo, Name: "a/b", Branch: "feature"}
			},
			wantStatus:  http.StatusBadRequest,
			wantMessage: "must be a single path segment",
		},
		{
			name: "NameLooksLikeOption",
			setup: func(_ *testing.T, repo, _ string) workspacesdk.ForkCreateWorktreeRequest {
				return workspacesdk.ForkCreateWorktreeRequest{RepoPath: repo, Name: "-x", Branch: "feature"}
			},
			wantStatus:  http.StatusBadRequest,
			wantMessage: "must be a single path segment",
		},
		{
			name: "InvalidBranch",
			setup: func(_ *testing.T, repo, _ string) workspacesdk.ForkCreateWorktreeRequest {
				return workspacesdk.ForkCreateWorktreeRequest{RepoPath: repo, Name: "child", Branch: "bad..name"}
			},
			wantStatus:  http.StatusBadRequest,
			wantMessage: "is not a valid branch name",
		},
		{
			name: "BranchLooksLikeOption",
			setup: func(_ *testing.T, repo, _ string) workspacesdk.ForkCreateWorktreeRequest {
				return workspacesdk.ForkCreateWorktreeRequest{RepoPath: repo, Name: "child", Branch: "-x"}
			},
			wantStatus:  http.StatusBadRequest,
			wantMessage: "branch \"-x\" must be non-empty and must not start with",
		},
		{
			name: "ExistingBranch",
			setup: func(t *testing.T, repo, _ string) workspacesdk.ForkCreateWorktreeRequest {
				wtGit(t, repo, "branch", "taken")
				return workspacesdk.ForkCreateWorktreeRequest{RepoPath: repo, Name: "child", Branch: "taken"}
			},
			wantStatus:  http.StatusConflict,
			wantMessage: "branch \"taken\" already exists",
		},
		{
			name: "ExistingTargetDir",
			setup: func(t *testing.T, repo, root string) workspacesdk.ForkCreateWorktreeRequest {
				require.NoError(t, os.MkdirAll(filepath.Join(root, filepath.Base(repo), "child"), 0o755))
				return workspacesdk.ForkCreateWorktreeRequest{RepoPath: repo, Name: "child", Branch: "feature"}
			},
			wantStatus:  http.StatusConflict,
			wantMessage: "worktree path",
		},
		{
			name: "UnresolvableBaseRef",
			setup: func(_ *testing.T, repo, _ string) workspacesdk.ForkCreateWorktreeRequest {
				return workspacesdk.ForkCreateWorktreeRequest{RepoPath: repo, Name: "child", Branch: "feature", BaseRef: "nope"}
			},
			wantStatus:  http.StatusBadRequest,
			wantMessage: "does not resolve to a commit",
		},
		{
			name: "BaseRefLooksLikeOption",
			setup: func(_ *testing.T, repo, _ string) workspacesdk.ForkCreateWorktreeRequest {
				return workspacesdk.ForkCreateWorktreeRequest{RepoPath: repo, Name: "child", Branch: "feature", BaseRef: "-x"}
			},
			wantStatus:  http.StatusBadRequest,
			wantMessage: "base_ref \"-x\" must not start with",
		},
		{
			name: "RepoDirSymlinkEscapesRoot",
			setup: func(t *testing.T, repo, root string) workspacesdk.ForkCreateWorktreeRequest {
				require.NoError(t, os.MkdirAll(root, 0o755))
				require.NoError(t, os.Symlink(testutil.TempDirResolved(t), filepath.Join(root, filepath.Base(repo))))
				return workspacesdk.ForkCreateWorktreeRequest{RepoPath: repo, Name: "child", Branch: "feature"}
			},
			wantStatus:  http.StatusBadRequest,
			wantMessage: "worktree parent",
		},
		{
			name: "RootInsideRepo",
			setup: func(t *testing.T, _, root string) workspacesdk.ForkCreateWorktreeRequest {
				repoPath := filepath.Dir(root)
				wtGit(t, repoPath, "init")
				require.NoError(t, os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("# Test\n"), 0o600))
				wtGit(t, repoPath, "add", "README.md")
				wtGit(t, repoPath, "commit", "-m", "initial commit")
				return workspacesdk.ForkCreateWorktreeRequest{RepoPath: repoPath, Name: "child", Branch: "feature"}
			},
			wantStatus:  http.StatusBadRequest,
			wantMessage: "contains the worktree root",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h, root := newTestWorktreeAPI(t)
			repo := wtInitRepo(t)
			req := tt.setup(t, repo, root)

			rec := doWorktreeRequest(t, h, http.MethodPost, req)

			require.Equal(t, tt.wantStatus, rec.Code, rec.Body.String())
			assert.Contains(t, wtErrorMessage(t, rec), tt.wantMessage)
			assert.Equal(t, 1, wtWorktreeCount(t, repo))
		})
	}
}

func TestForkWorktreeCreateTightensExistingRoot(t *testing.T) {
	t.Parallel()

	h, root := newTestWorktreeAPI(t)
	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.Chmod(root, 0o755))

	wtCreate(t, h, workspacesdk.ForkCreateWorktreeRequest{RepoPath: wtInitRepo(t), Name: "child", Branch: "feature"})

	info, err := os.Stat(root)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}

// The agent API server's WriteTimeout is shorter than a checkout of a large
// repository, so a slow post-checkout hook stands in for one.
func TestForkWorktreeCreateOutlastsWriteTimeout(t *testing.T) {
	t.Parallel()

	h, _ := newTestWorktreeAPI(t)
	repo := wtInitRepo(t)
	hook := filepath.Join(repo, ".git", "hooks", "post-checkout")
	require.NoError(t, os.WriteFile(hook, []byte("#!/bin/sh\nsleep 1\n"), 0o755)) //nolint:gosec // Git runs only executable hooks.
	srv := httptest.NewUnstartedServer(h)
	srv.Config.WriteTimeout = 200 * time.Millisecond
	srv.Start()
	t.Cleanup(srv.Close)

	data, err := json.Marshal(workspacesdk.ForkCreateWorktreeRequest{RepoPath: repo, Name: "child", Branch: "feature"})
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(testutil.Context(t, testutil.WaitLong), http.MethodPost, srv.URL, bytes.NewReader(data))
	require.NoError(t, err)
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
}

func TestForkWorktreeCreateFailedHookRollsBack(t *testing.T) {
	t.Parallel()

	h, root := newTestWorktreeAPI(t)
	repo := wtInitRepo(t)

	hooks := filepath.Join(repo, ".git", "hooks", "post-checkout")
	require.NoError(t, os.WriteFile(hooks, []byte("#!/bin/sh\nexit 1\n"), 0o755)) //nolint:gosec // Git runs only executable hooks.

	req := workspacesdk.ForkCreateWorktreeRequest{RepoPath: repo, Name: "child", Branch: "feature"}
	rec := doWorktreeRequest(t, h, http.MethodPost, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	assert.Contains(t, wtErrorMessage(t, rec), "Failed to create the worktree")

	path := filepath.Join(root, filepath.Base(repo), "child")
	assert.NoDirExists(t, path)
	assert.NotContains(t, wtGit(t, repo, "worktree", "list", "--porcelain"), path)
	err := exec.Command("git", "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/feature").Run()
	require.Error(t, err, "branch feature must not exist")

	require.NoError(t, os.Remove(hooks))
	wt := wtCreate(t, h, req)
	assert.Equal(t, path, wt.Path)
	assert.Equal(t, "feature", wt.Branch)
}

func TestForkWorktreeDiscardCanceledContext(t *testing.T) {
	t.Parallel()

	repo := wtInitRepo(t)
	path := filepath.Join(filepath.Dir(repo), "discard-me")
	wtGit(t, repo, "worktree", "add", "-b", "feature", path)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	api := &worktreeAPI{
		logger: slogtest.Make(t, nil),
		execer: agentexec.DefaultExecer,
		gitBin: "git",
	}
	api.discardWorktree(ctx, repo, path, "feature")

	assert.NoDirExists(t, path)
	err := exec.Command("git", "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/feature").Run()
	require.Error(t, err, "branch feature must not exist")
}

// Two isolated children edit the same file on their own branches without
// touching each other or the parent checkout.
func TestForkWorktreeConcurrentEdits(t *testing.T) {
	t.Parallel()
	h, _ := newTestWorktreeAPI(t)
	repo := wtInitRepo(t)
	before := wtRepoState(t, repo)

	a := wtCreate(t, h, workspacesdk.ForkCreateWorktreeRequest{RepoPath: repo, Name: "a", Branch: "child-a"})
	b := wtCreate(t, h, workspacesdk.ForkCreateWorktreeRequest{RepoPath: repo, Name: "b", Branch: "child-b"})
	wtCommit(t, a.Path, "from a\n")
	wtCommit(t, b.Path, "from b\n")

	assert.Equal(t, "from a", wtGit(t, repo, "show", "child-a:README.md"))
	assert.Equal(t, "from b", wtGit(t, repo, "show", "child-b:README.md"))
	assert.Equal(t, before, wtRepoState(t, repo))
	readme, err := os.ReadFile(filepath.Join(repo, "README.md"))
	require.NoError(t, err)
	assert.Equal(t, "# Test\n", string(readme))
}

func TestForkWorktreeDelete(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// setup returns the path to delete; wt is a fresh worktree on
		// branch "feature".
		setup       func(t *testing.T, repo, root string, wt workspacesdk.ForkWorktree) string
		wantStatus  int
		wantMessage string
	}{
		{
			name: "Clean",
			setup: func(_ *testing.T, _, _ string, wt workspacesdk.ForkWorktree) string {
				return wt.Path
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name: "CommitsOnAnotherBranch",
			setup: func(t *testing.T, repo, _ string, wt workspacesdk.ForkWorktree) string {
				wtCommit(t, wt.Path, "merged\n")
				wtGit(t, repo, "branch", "keeper", "feature")
				return wt.Path
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name: "CommitsOnRemote",
			setup: func(t *testing.T, repo, _ string, wt workspacesdk.ForkWorktree) string {
				wtCommit(t, wt.Path, "pushed\n")
				wtGit(t, repo, "update-ref", "refs/remotes/origin/feature", "feature")
				return wt.Path
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name: "DetachedClean",
			setup: func(t *testing.T, _, _ string, wt workspacesdk.ForkWorktree) string {
				wtGit(t, wt.Path, "checkout", "--detach")
				return wt.Path
			},
			wantStatus:  http.StatusConflict,
			wantMessage: "is on a detached HEAD, not branch feature",
		},
		{
			name: "Dirty",
			setup: func(t *testing.T, _, _ string, wt workspacesdk.ForkWorktree) string {
				require.NoError(t, os.WriteFile(filepath.Join(wt.Path, "README.md"), []byte("dirty\n"), 0o600))
				require.NoError(t, os.WriteFile(filepath.Join(wt.Path, "new.txt"), []byte("new\n"), 0o600))
				return wt.Path
			},
			wantStatus:  http.StatusConflict,
			wantMessage: "has 2 uncommitted or untracked changes",
		},
		{
			name: "UnpushedCommits",
			setup: func(t *testing.T, _, _ string, wt workspacesdk.ForkWorktree) string {
				wtCommit(t, wt.Path, "unpushed\n")
				return wt.Path
			},
			wantStatus:  http.StatusConflict,
			wantMessage: "branch feature has 1 commits that are not on any other branch or remote",
		},
		{
			name: "CommitOnlyInReflog",
			setup: func(t *testing.T, _, _ string, wt workspacesdk.ForkWorktree) string {
				wtCommit(t, wt.Path, "reset away\n")
				wtGit(t, wt.Path, "reset", "--hard", "HEAD~1")
				return wt.Path
			},
			wantStatus:  http.StatusConflict,
			wantMessage: "branch feature has 1 commits that are not on any other branch or remote",
		},
		{
			name: "RelativePath",
			setup: func(t *testing.T, _, root string, wt workspacesdk.ForkWorktree) string {
				rel, err := filepath.Rel(root, wt.Path)
				require.NoError(t, err)
				return rel
			},
			wantStatus:  http.StatusBadRequest,
			wantMessage: "must be an absolute path",
		},
		{
			name: "BesideRoot",
			setup: func(t *testing.T, repo, root string, _ workspacesdk.ForkWorktree) string {
				outside := filepath.Join(filepath.Dir(root), "outside")
				wtGit(t, repo, "worktree", "add", "-b", "outside", outside)
				return outside
			},
			wantStatus:  http.StatusBadRequest,
			wantMessage: "is not a worktree directory under",
		},
		{
			name: "Missing",
			setup: func(_ *testing.T, _, _ string, wt workspacesdk.ForkWorktree) string {
				return filepath.Join(filepath.Dir(wt.Path), "missing")
			},
			wantStatus:  http.StatusNotFound,
			wantMessage: "does not exist",
		},
		{
			name: "DirectlyUnderRoot",
			setup: func(t *testing.T, repo, root string, _ workspacesdk.ForkWorktree) string {
				shallow := filepath.Join(root, "shallow")
				wtGit(t, repo, "worktree", "add", "-b", "shallow", shallow)
				return shallow
			},
			wantStatus:  http.StatusBadRequest,
			wantMessage: "is not a worktree directory under",
		},
		{
			name: "NestedBelowName",
			setup: func(t *testing.T, repo, _ string, wt workspacesdk.ForkWorktree) string {
				deep := filepath.Join(filepath.Dir(wt.Path), "x", "y")
				wtGit(t, repo, "worktree", "add", "-b", "deep", deep)
				return deep
			},
			wantStatus:  http.StatusBadRequest,
			wantMessage: "is not a worktree directory under",
		},
		{
			name: "SymlinkEscapesRoot",
			setup: func(t *testing.T, repo, _ string, wt workspacesdk.ForkWorktree) string {
				outside := filepath.Join(testutil.TempDirResolved(t), "outside")
				wtGit(t, repo, "worktree", "add", "-b", "outside", outside)
				link := filepath.Join(filepath.Dir(wt.Path), "link")
				require.NoError(t, os.Symlink(outside, link))
				return link
			},
			wantStatus:  http.StatusBadRequest,
			wantMessage: "is not a worktree directory under",
		},
		{
			name: "MainWorkingTree",
			setup: func(t *testing.T, _, _ string, wt workspacesdk.ForkWorktree) string {
				mainTree := filepath.Join(filepath.Dir(wt.Path), "main")
				require.NoError(t, os.Mkdir(mainTree, 0o755))
				wtGit(t, mainTree, "init")
				return mainTree
			},
			wantStatus:  http.StatusBadRequest,
			wantMessage: "is a main working tree, not a linked worktree",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h, root := newTestWorktreeAPI(t)
			repo := wtInitRepo(t)
			wt := wtCreate(t, h, workspacesdk.ForkCreateWorktreeRequest{RepoPath: repo, Name: "child", Branch: "feature"})
			before := wtWorktreeCount(t, repo)
			path := tt.setup(t, repo, root, wt)
			stateAfterSetup := wtWorktreeCount(t, repo)

			rec := doWorktreeRequest(t, h, http.MethodDelete, workspacesdk.ForkDeleteWorktreeRequest{Path: path, Branch: wt.Branch})

			require.Equal(t, tt.wantStatus, rec.Code, rec.Body.String())
			if tt.wantStatus == http.StatusNoContent {
				assert.NoDirExists(t, wt.Path)
				assert.Equal(t, before-1, wtWorktreeCount(t, repo))
				assert.Empty(t, wtGit(t, repo, "branch", "--list", "feature"), "branch feature must be deleted")
				return
			}
			assert.Contains(t, wtErrorMessage(t, rec), tt.wantMessage)
			assert.DirExists(t, wt.Path)
			assert.Equal(t, stateAfterSetup, wtWorktreeCount(t, repo))
		})
	}
}

func TestForkWorktreeDeleteRemovesBranchConfig(t *testing.T) {
	t.Parallel()
	h, _ := newTestWorktreeAPI(t)
	repo := wtInitRepo(t)
	wt := wtCreate(t, h, workspacesdk.ForkCreateWorktreeRequest{RepoPath: repo, Name: "child", Branch: "feature"})
	wtGit(t, repo, "config", "branch.feature.remote", "origin")
	wtGit(t, repo, "config", "branch.feature.merge", "refs/heads/feature")

	rec := doWorktreeRequest(t, h, http.MethodDelete, workspacesdk.ForkDeleteWorktreeRequest{Path: wt.Path, Branch: wt.Branch})

	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	err := exec.Command("git", "-C", repo, "config", "--get-regexp", `^branch\.feature\.`).Run()
	require.Error(t, err, "branch feature config must be removed")
}

// More reflog entries than fit in one argv must not break the unpushed
// check.
func TestForkWorktreeDeleteLongReflog(t *testing.T) {
	t.Parallel()
	h, _ := newTestWorktreeAPI(t)
	repo := wtInitRepo(t)
	wt := wtCreate(t, h, workspacesdk.ForkCreateWorktreeRequest{RepoPath: repo, Name: "child", Branch: "feature"})
	oid := wtGit(t, wt.Path, "rev-parse", "HEAD")
	entry := oid + " " + oid + " Test <test@example.com> 1700000000 +0000\tcheckout: moving\n"
	logs := filepath.Join(wtGit(t, wt.Path, "rev-parse", "--absolute-git-dir"), "logs", "HEAD")
	require.NoError(t, os.WriteFile(logs, []byte(strings.Repeat(entry, 80000)), 0o600))

	rec := doWorktreeRequest(t, h, http.MethodDelete, workspacesdk.ForkDeleteWorktreeRequest{Path: wt.Path, Branch: wt.Branch})

	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	assert.NoDirExists(t, wt.Path)
}

func TestForkWorktreeDeleteWrongBranch(t *testing.T) {
	t.Parallel()

	h, _ := newTestWorktreeAPI(t)
	repo := wtInitRepo(t)
	wt := wtCreate(t, h, workspacesdk.ForkCreateWorktreeRequest{RepoPath: repo, Name: "child", Branch: "feature"})
	wtGit(t, wt.Path, "switch", "-c", "other")

	rec := doWorktreeRequest(t, h, http.MethodDelete, workspacesdk.ForkDeleteWorktreeRequest{Path: wt.Path, Branch: wt.Branch})

	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	assert.Contains(t, wtErrorMessage(t, rec), "is on other, not branch feature")
	assert.DirExists(t, wt.Path)
	assert.NotEmpty(t, wtGit(t, repo, "branch", "--list", "feature"), "branch feature must be kept")
	assert.NotEmpty(t, wtGit(t, repo, "branch", "--list", "other"), "branch other must be kept")
}

func wtInitSubmoduleRepo(t *testing.T, path string) string {
	t.Helper()
	repo := wtInitRepo(t)
	sub := filepath.Join(filepath.Dir(repo), "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	wtGit(t, sub, "init")
	require.NoError(t, os.WriteFile(filepath.Join(sub, "lib.txt"), []byte("lib\n"), 0o600))
	wtGit(t, sub, "add", "lib.txt")
	wtGit(t, sub, "commit", "-m", "sub commit")
	wtGit(t, repo, "-c", "protocol.file.allow=always", "submodule", "add", sub, path)
	wtGit(t, repo, "commit", "-m", "add submodule")
	return repo
}

func TestForkWorktreeDeleteSubmodules(t *testing.T) {
	t.Parallel()

	t.Run("Clean", func(t *testing.T) {
		t.Parallel()
		h, _ := newTestWorktreeAPI(t)
		repo := wtInitSubmoduleRepo(t, "sub")
		wt := wtCreate(t, h, workspacesdk.ForkCreateWorktreeRequest{RepoPath: repo, Name: "child", Branch: "feature"})
		wtGit(t, wt.Path, "-c", "protocol.file.allow=always", "submodule", "update", "--init")

		rec := doWorktreeRequest(t, h, http.MethodDelete, workspacesdk.ForkDeleteWorktreeRequest{Path: wt.Path, Branch: wt.Branch})

		require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
		assert.NoDirExists(t, wt.Path)
		assert.Empty(t, wtGit(t, repo, "branch", "--list", "feature"), "branch feature must be deleted")
	})

	t.Run("DirtySubmodule", func(t *testing.T) {
		t.Parallel()
		h, _ := newTestWorktreeAPI(t)
		repo := wtInitSubmoduleRepo(t, "sub")
		wt := wtCreate(t, h, workspacesdk.ForkCreateWorktreeRequest{RepoPath: repo, Name: "child", Branch: "feature"})
		wtGit(t, wt.Path, "-c", "protocol.file.allow=always", "submodule", "update", "--init")
		require.NoError(t, os.WriteFile(filepath.Join(wt.Path, "sub", "scratch.txt"), []byte("scratch\n"), 0o600))

		rec := doWorktreeRequest(t, h, http.MethodDelete, workspacesdk.ForkDeleteWorktreeRequest{Path: wt.Path, Branch: wt.Branch})

		require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
		assert.DirExists(t, wt.Path)
	})

	t.Run("DirtySubmoduleIgnoredByConfig", func(t *testing.T) {
		t.Parallel()
		h, _ := newTestWorktreeAPI(t)
		repo := wtInitSubmoduleRepo(t, "sub")
		wtGit(t, repo, "config", "submodule.sub.ignore", "all")
		wt := wtCreate(t, h, workspacesdk.ForkCreateWorktreeRequest{RepoPath: repo, Name: "child", Branch: "feature"})
		wtGit(t, wt.Path, "-c", "protocol.file.allow=always", "submodule", "update", "--init")
		require.NoError(t, os.WriteFile(filepath.Join(wt.Path, "sub", "scratch.txt"), []byte("scratch\n"), 0o600))

		rec := doWorktreeRequest(t, h, http.MethodDelete, workspacesdk.ForkDeleteWorktreeRequest{Path: wt.Path, Branch: wt.Branch})

		require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
		assert.FileExists(t, filepath.Join(wt.Path, "sub", "scratch.txt"))
	})

	t.Run("SubmoduleLocalCommits", func(t *testing.T) {
		t.Parallel()
		h, _ := newTestWorktreeAPI(t)
		repo := wtInitSubmoduleRepo(t, "sub")
		wt := wtCreate(t, h, workspacesdk.ForkCreateWorktreeRequest{RepoPath: repo, Name: "child", Branch: "feature"})
		wtGit(t, wt.Path, "-c", "protocol.file.allow=always", "submodule", "update", "--init")
		sub := filepath.Join(wt.Path, "sub")
		r := wtGit(t, sub, "rev-parse", "HEAD")
		wtGit(t, sub, "switch", "-c", "local")
		wtGit(t, sub, "commit", "--allow-empty", "-m", "local")
		wtGit(t, sub, "checkout", "--detach", r)

		rec := doWorktreeRequest(t, h, http.MethodDelete, workspacesdk.ForkDeleteWorktreeRequest{Path: wt.Path, Branch: wt.Branch})

		require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
		assert.DirExists(t, wt.Path)
	})

	t.Run("SubmoduleReflogOnlyCommit", func(t *testing.T) {
		t.Parallel()
		h, _ := newTestWorktreeAPI(t)
		repo := wtInitSubmoduleRepo(t, "sub")
		wt := wtCreate(t, h, workspacesdk.ForkCreateWorktreeRequest{RepoPath: repo, Name: "child", Branch: "feature"})
		wtGit(t, wt.Path, "-c", "protocol.file.allow=always", "submodule", "update", "--init")
		sub := filepath.Join(wt.Path, "sub")
		r := wtGit(t, sub, "rev-parse", "HEAD")
		wtGit(t, sub, "commit", "--allow-empty", "-m", "detached")
		wtGit(t, sub, "checkout", "--detach", r)

		rec := doWorktreeRequest(t, h, http.MethodDelete, workspacesdk.ForkDeleteWorktreeRequest{Path: wt.Path, Branch: wt.Branch})

		require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
		assert.DirExists(t, wt.Path)
	})

	for _, path := range []string{"refs", "libs/sub"} {
		t.Run("LocalCommitsInSubmoduleAt_"+strings.ReplaceAll(path, "/", "_"), func(t *testing.T) {
			t.Parallel()
			h, _ := newTestWorktreeAPI(t)
			repo := wtInitSubmoduleRepo(t, path)
			wt := wtCreate(t, h, workspacesdk.ForkCreateWorktreeRequest{RepoPath: repo, Name: "child", Branch: "feature"})
			wtGit(t, wt.Path, "-c", "protocol.file.allow=always", "submodule", "update", "--init")
			sub := filepath.Join(wt.Path, path)
			r := wtGit(t, sub, "rev-parse", "HEAD")
			wtGit(t, sub, "switch", "-c", "local")
			wtGit(t, sub, "commit", "--allow-empty", "-m", "local")
			wtGit(t, sub, "checkout", "--detach", r)

			rec := doWorktreeRequest(t, h, http.MethodDelete, workspacesdk.ForkDeleteWorktreeRequest{Path: wt.Path, Branch: wt.Branch})

			require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
			assert.DirExists(t, wt.Path)
		})
	}

	// Only a git dir's modules directory holds nested submodules, so a
	// repository elsewhere in its metadata, such as a cache, is not one.
	t.Run("RepositoryInSubmoduleMetadata", func(t *testing.T) {
		t.Parallel()
		h, _ := newTestWorktreeAPI(t)
		repo := wtInitSubmoduleRepo(t, "sub")
		wt := wtCreate(t, h, workspacesdk.ForkCreateWorktreeRequest{RepoPath: repo, Name: "child", Branch: "feature"})
		wtGit(t, wt.Path, "-c", "protocol.file.allow=always", "submodule", "update", "--init")
		cache := filepath.Join(wtGit(t, filepath.Join(wt.Path, "sub"), "rev-parse", "--absolute-git-dir"), "cache", "repo")
		require.NoError(t, os.MkdirAll(cache, 0o755))
		wtGit(t, cache, "init", "--bare")
		wtGit(t, repo, "push", cache, "HEAD:refs/heads/main")

		rec := doWorktreeRequest(t, h, http.MethodDelete, workspacesdk.ForkDeleteWorktreeRequest{Path: wt.Path, Branch: wt.Branch})

		require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
		assert.NoDirExists(t, wt.Path)
	})

	t.Run("DeinitializedSubmoduleLocalCommits", func(t *testing.T) {
		t.Parallel()
		h, _ := newTestWorktreeAPI(t)
		repo := wtInitSubmoduleRepo(t, "sub")
		wt := wtCreate(t, h, workspacesdk.ForkCreateWorktreeRequest{RepoPath: repo, Name: "child", Branch: "feature"})
		wtGit(t, wt.Path, "-c", "protocol.file.allow=always", "submodule", "update", "--init")
		sub := filepath.Join(wt.Path, "sub")
		wtGit(t, sub, "switch", "-c", "local")
		wtGit(t, sub, "commit", "--allow-empty", "-m", "local")
		wtGit(t, wt.Path, "submodule", "deinit", "-f", "sub")

		rec := doWorktreeRequest(t, h, http.MethodDelete, workspacesdk.ForkDeleteWorktreeRequest{Path: wt.Path, Branch: wt.Branch})

		require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
		assert.DirExists(t, wt.Path)
	})
}
