package pluginpacks_test

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/x/pluginpacks"
	"github.com/coder/coder/v2/coderd/x/pluginpacks/pluginpackstest"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/safedial"
)

func TestFetch(t *testing.T) {
	t.Parallel()

	repo := pluginpackstest.New(t)
	fetcher := pluginpacks.Fetcher{
		AllowedHosts:    []string{"127.0.0.1"},
		AllowedPrefixes: loopback,
		Transport:       repo.Client.Transport,
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
		// A repository and server of its own, so the parallel subtests above
		// cannot add requests while this one asserts that none arrived.
		rejectRepo := pluginpackstest.New(t)
		rejectFetcher := pluginpacks.Fetcher{
			AllowedHosts:    []string{"127.0.0.1"},
			AllowedPrefixes: loopback,
			Transport:       rejectRepo.Client.Transport,
		}

		for _, src := range []pluginpacks.Source{
			{URL: strings.Replace(rejectRepo.URL, "127.0.0.1", "localhost", 1), Ref: "main"},
			{URL: strings.Replace(rejectRepo.URL, "https://", "http://", 1), Ref: "main"},
			{URL: strings.Replace(rejectRepo.URL, "https://", "https://user:pass@", 1), Ref: "main"},
		} {
			_, err := rejectFetcher.Fetch(ctx, src)
			require.Error(t, err, src.URL)
		}
		require.Zero(t, rejectRepo.Requests(), "rejected sources must not reach the server")

		_, err := rejectFetcher.Fetch(ctx, pluginpacks.Source{URL: rejectRepo.URL, Ref: "missing"})
		require.ErrorContains(t, err, `ref "missing"`)
		_, err = rejectFetcher.Fetch(ctx, pluginpacks.Source{URL: rejectRepo.URL, Path: "big", Ref: "main"})
		require.ErrorContains(t, err, "larger than")
	})
}

var loopback = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}

func TestFetchPrivateAddress(t *testing.T) {
	t.Parallel()

	t.Run("RefusedByDefault", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitLong)
		repo := pluginpackstest.New(t)
		// The hosts are allowed sources, but they resolve to loopback and
		// no prefix allows it.
		fetcher := pluginpacks.Fetcher{
			AllowedHosts: []string{"127.0.0.1", "localhost"},
			Transport:    repo.Client.Transport,
		}
		for _, src := range []string{repo.URL, strings.Replace(repo.URL, "127.0.0.1", "localhost", 1)} {
			_, err := fetcher.Fetch(ctx, pluginpacks.Source{URL: src, Path: "pack", Ref: "main"})
			var blocked *safedial.BlockedError
			require.True(t, errors.As(err, &blocked), "%s: %v", src, err)
		}
		require.Zero(t, repo.Requests(), "a refused address must not reach the server")
	})

	t.Run("Redirect", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitLong)
		// The origin is allowed, but it redirects to a loopback address
		// outside the allowed prefix.
		origin := httptest.NewTLSServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			_, port, _ := net.SplitHostPort(r.Host)
			target := *r.URL
			target.Scheme = "https"
			target.Host = net.JoinHostPort("127.0.0.2", port)
			http.Redirect(rw, r, target.String(), http.StatusFound)
		}))
		t.Cleanup(origin.Close)
		fetcher := pluginpacks.Fetcher{
			AllowedHosts:    []string{"127.0.0.1", "127.0.0.2"},
			AllowedPrefixes: loopback,
			Transport:       origin.Client().Transport,
		}
		_, err := fetcher.Fetch(ctx, pluginpacks.Source{URL: origin.URL + "/repo.git", Ref: "main"})
		var blocked *safedial.BlockedError
		require.True(t, errors.As(err, &blocked), "%v", err)
		require.Equal(t, netip.MustParseAddr("127.0.0.2"), blocked.Addr)
	})

	t.Run("UnguardableTransport", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitLong)
		fetcher := pluginpacks.Fetcher{
			AllowedHosts: []string{"example.com"},
			Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				t.Error("an unguarded transport must not send requests")
				return nil, xerrors.New("unreachable")
			}),
		}
		_, err := fetcher.Fetch(ctx, pluginpacks.Source{URL: "https://example.com/repo.git", Ref: "main"})
		require.ErrorContains(t, err, "cannot be guarded")
	})
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
