package agentgit

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/testutil"
)

func doSummaryRequest(t *testing.T, h http.Handler, wt workspacesdk.ForkWorktree) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(workspacesdk.ForkWorktreeSummaryRequest{Path: wt.Path, Branch: wt.Branch})
	require.NoError(t, err)
	req := httptest.NewRequestWithContext(testutil.Context(t, testutil.WaitLong), http.MethodPost, "/summary", bytes.NewReader(data))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestForkWorktreeSummary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// work changes the worktree at dir.
		work func(t *testing.T, dir string)
		want workspacesdk.ForkWorktreeSummary
	}{
		{
			name: "Clean",
			work: func(*testing.T, string) {},
		},
		{
			name: "CommitsAndUncommittedChanges",
			work: func(t *testing.T, dir string) {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "new.txt"), []byte("a\nb\n"), 0o600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "bin.dat"), []byte{0, 1, 2}, 0o600))
				wtGit(t, dir, "add", "new.txt", "bin.dat")
				wtGit(t, dir, "commit", "-m", "add files")
				wtCommit(t, dir, "# Changed\n")
				require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Changed\nextra\n"), 0o600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "scratch.txt"), []byte("x\n"), 0o600))
			},
			// README.md goes from "# Test" to two new lines; scratch.txt is
			// untracked, so only status counts it.
			want: workspacesdk.ForkWorktreeSummary{
				Commits:            2,
				FilesChanged:       3,
				Insertions:         4,
				Deletions:          1,
				UncommittedChanges: 2,
			},
		},
		{
			name: "CommitsOnAnotherBranch",
			work: func(t *testing.T, dir string) {
				wtCommit(t, dir, "# Shared\n")
				wtGit(t, dir, "branch", "shared")
			},
		},
		{
			name: "RootCommit",
			work: func(t *testing.T, dir string) {
				wtGit(t, dir, "checkout", "--orphan", "orphan")
				wtGit(t, dir, "commit", "-m", "root")
				wtGit(t, dir, "branch", "-f", "feature", "orphan")
				wtGit(t, dir, "checkout", "feature")
				wtGit(t, dir, "branch", "-D", "orphan")
			},
			want: workspacesdk.ForkWorktreeSummary{
				Commits:      1,
				FilesChanged: 1,
				Insertions:   1,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h, _ := newTestWorktreeAPI(t)
			wt := wtCreate(t, h, workspacesdk.ForkCreateWorktreeRequest{RepoPath: wtInitRepo(t), Name: "child", Branch: "feature"})
			tt.work(t, wt.Path)

			rec := doSummaryRequest(t, h, wt)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			var got workspacesdk.ForkWorktreeSummary
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestForkWorktreeSummaryRejects(t *testing.T) {
	t.Parallel()

	t.Run("RemovedWorktree", func(t *testing.T) {
		t.Parallel()
		h, _ := newTestWorktreeAPI(t)
		repo := wtInitRepo(t)
		wt := wtCreate(t, h, workspacesdk.ForkCreateWorktreeRequest{RepoPath: repo, Name: "child", Branch: "feature"})
		wtGit(t, repo, "worktree", "remove", wt.Path)

		rec := doSummaryRequest(t, h, wt)
		require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
		assert.Contains(t, wtErrorMessage(t, rec), "does not exist")
	})

	t.Run("WrongBranch", func(t *testing.T) {
		t.Parallel()
		h, _ := newTestWorktreeAPI(t)
		wt := wtCreate(t, h, workspacesdk.ForkCreateWorktreeRequest{RepoPath: wtInitRepo(t), Name: "child", Branch: "feature"})
		wtGit(t, wt.Path, "checkout", "-b", "other")

		rec := doSummaryRequest(t, h, wt)
		require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
		assert.Contains(t, wtErrorMessage(t, rec), "is on other, not branch feature")
	})
}
