package pluginpacks_test

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

	"github.com/coder/coder/v2/coderd/x/pluginpacks"
	"github.com/coder/coder/v2/testutil"
)

func TestFetch(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)
	srv, requests := serveRepo(t, repo.root)
	fetcher := pluginpacks.Fetcher{
		AllowedHosts: []string{"127.0.0.1"},
		Transport:    srv.Client().Transport,
	}
	repoURL := srv.URL + "/pack.git"

	for _, tc := range []struct {
		name   string
		ref    string
		commit string
		skill  string
	}{
		{name: "AnnotatedTag", ref: "v1", commit: repo.first, skill: "first"},
		{name: "Branch", ref: "main", commit: repo.second, skill: "second"},
		{name: "CommitSHA", ref: repo.first, commit: repo.first, skill: "first"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := testutil.Context(t, testutil.WaitLong)

			snap, err := fetcher.Fetch(ctx, pluginpacks.Source{URL: repoURL, Path: "/pack/", Ref: tc.ref})
			require.NoError(t, err)
			require.Equal(t, tc.commit, snap.CommitSHA)
			got := map[string]string{}
			for _, f := range snap.Files {
				got[f.Path] = string(f.Content)
			}
			// The symlink and files outside the pack directory are skipped.
			require.Equal(t, map[string]string{
				"agents/reviewer.md":    "agent",
				"skills/demo/SKILL.md":  tc.skill,
				"skills/demo/script.sh": "#!/bin/sh",
			}, got)
		})
	}

	t.Run("Rejected", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitLong)
		before := requests.Load()

		for _, src := range []pluginpacks.Source{
			{URL: strings.Replace(repoURL, "127.0.0.1", "localhost", 1), Ref: "main"},
			{URL: strings.Replace(repoURL, "https://", "http://", 1), Ref: "main"},
			{URL: strings.Replace(repoURL, "https://", "https://user:pass@", 1), Ref: "main"},
		} {
			_, err := fetcher.Fetch(ctx, src)
			require.Error(t, err, src.URL)
		}
		require.Equal(t, before, requests.Load(), "rejected sources must not reach the server")

		_, err := fetcher.Fetch(ctx, pluginpacks.Source{URL: repoURL, Ref: "missing"})
		require.ErrorContains(t, err, `ref "missing"`)
		_, err = fetcher.Fetch(ctx, pluginpacks.Source{URL: repoURL, Path: "big", Ref: "main"})
		require.ErrorContains(t, err, "larger than")
	})
}

type testRepo struct {
	root          string
	first, second string
}

func newTestRepo(t *testing.T) testRepo {
	t.Helper()
	dir := t.TempDir()
	work := filepath.Join(dir, "work")
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
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
	first := git("rev-parse", "HEAD")
	write("pack/skills/demo/SKILL.md", "second")
	git("commit", "-q", "-am", "second")
	second := git("rev-parse", "HEAD")

	bare := filepath.Join(dir, "root", "pack.git")
	git("clone", "-q", "--bare", work, bare)
	git("--git-dir", bare, "config", "uploadpack.allowAnySHA1InWant", "true")
	return testRepo{root: filepath.Dir(bare), first: first, second: second}
}

func serveRepo(t *testing.T, root string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	require.NoError(t, err)
	var requests atomic.Int64
	backend := &cgi.Handler{
		Path: gitPath,
		Args: []string{"http-backend"},
		Env:  []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"},
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		backend.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &requests
}
