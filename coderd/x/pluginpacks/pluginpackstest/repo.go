// Package pluginpackstest serves a test Git repository over HTTPS for
// plugin pack tests.
package pluginpackstest

import (
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// Repo is a repository served by git http-backend. Its pack directory,
// "pack", has two skill files, an agent and a symlink. "other" is outside
// the pack and "big" holds a file over the per-file cap.
type Repo struct {
	// URL is the clone URL. Its host is 127.0.0.1.
	URL string
	// Client trusts the server certificate.
	Client *http.Client
	// First is tagged v1, where pack/skills/demo/SKILL.md reads "first".
	First string
	// Second is the head of main, where it reads "second".
	Second string

	requests atomic.Int64
}

// Requests returns the number of requests the server received.
func (r *Repo) Requests() int64 { return r.requests.Load() }

// New creates and serves the repository until the test ends. It needs git.
func New(t *testing.T) *Repo {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	require.NoError(t, err)

	dir := t.TempDir()
	work := filepath.Join(dir, "work")
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(gitPath, args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
		return strings.TrimSpace(string(out))
	}
	write := func(name, content string) {
		t.Helper()
		p := filepath.Join(work, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	}

	require.NoError(t, os.MkdirAll(work, 0o755))
	git("init", "-q", "-b", "main")
	write("pack/skills/demo/SKILL.md", "first")
	write("pack/skills/demo/script.sh", "#!/bin/sh")
	write("pack/agents/reviewer.md", "agent")
	write("other/README.md", "not in the pack")
	write("big/blob.bin", strings.Repeat("x", 1<<20+1))
	require.NoError(t, os.Symlink("/etc/passwd", filepath.Join(work, "pack", "link")))
	git("add", ".")
	git("commit", "-q", "-m", "first")
	git("tag", "-a", "v1", "-m", "v1")
	repo := &Repo{First: git("rev-parse", "HEAD")}
	write("pack/skills/demo/SKILL.md", "second")
	git("commit", "-q", "-am", "second")
	repo.Second = git("rev-parse", "HEAD")

	root := filepath.Join(dir, "root")
	bare := filepath.Join(root, "pack.git")
	git("clone", "-q", "--bare", work, bare)
	git("--git-dir", bare, "config", "uploadpack.allowAnySHA1InWant", "true")

	backend := &cgi.Handler{
		Path: gitPath,
		Args: []string{"http-backend"},
		Env:  []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"},
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		repo.requests.Add(1)
		backend.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	repo.URL = srv.URL + "/pack.git"
	repo.Client = srv.Client()
	return repo
}
