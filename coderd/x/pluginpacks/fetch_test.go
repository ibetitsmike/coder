package pluginpacks_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/x/pluginpacks"
	"github.com/coder/coder/v2/coderd/x/pluginpacks/pluginpackstest"
	"github.com/coder/coder/v2/testutil"
)

func TestFetch(t *testing.T) {
	t.Parallel()

	repo := pluginpackstest.New(t)
	fetcher := pluginpacks.Fetcher{
		AllowedHosts: []string{"127.0.0.1"},
		Transport:    repo.Client.Transport,
	}

	for _, tc := range []struct {
		name   string
		ref    string
		commit string
		skill  string
	}{
		{name: "AnnotatedTag", ref: "v1", commit: repo.First, skill: "first"},
		{name: "Branch", ref: "main", commit: repo.Second, skill: "second"},
		{name: "CommitSHA", ref: repo.First, commit: repo.First, skill: "first"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := testutil.Context(t, testutil.WaitLong)

			snap, err := fetcher.Fetch(ctx, pluginpacks.Source{URL: repo.URL, Path: "/pack/", Ref: tc.ref})
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
		before := repo.Requests()

		for _, src := range []pluginpacks.Source{
			{URL: strings.Replace(repo.URL, "127.0.0.1", "localhost", 1), Ref: "main"},
			{URL: strings.Replace(repo.URL, "https://", "http://", 1), Ref: "main"},
			{URL: strings.Replace(repo.URL, "https://", "https://user:pass@", 1), Ref: "main"},
		} {
			_, err := fetcher.Fetch(ctx, src)
			require.Error(t, err, src.URL)
		}
		require.Equal(t, before, repo.Requests(), "rejected sources must not reach the server")

		_, err := fetcher.Fetch(ctx, pluginpacks.Source{URL: repo.URL, Ref: "missing"})
		require.ErrorContains(t, err, `ref "missing"`)
		_, err = fetcher.Fetch(ctx, pluginpacks.Source{URL: repo.URL, Path: "big", Ref: "main"})
		require.ErrorContains(t, err, "larger than")
	})
}
