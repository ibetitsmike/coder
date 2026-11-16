package agentcontext_test

// This file is fork code: it tests that resolves do work only when events
// call for it (issue #306): a reconnect reuses the previous resolve, and a
// pass over unchanged files reads no file bodies.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/agent/agentcontext"
	"github.com/coder/coder/v2/testutil"
)

func writeBackdated(tb testing.TB, path, content string) {
	tb.Helper()
	require.NoError(tb, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(tb, os.WriteFile(path, []byte(content), 0o600))
	backdate(tb, path)
}

// backdate moves a file's mtime out of the memo's racy window, as for any
// file that was not written in the last seconds.
func backdate(tb testing.TB, path string) {
	tb.Helper()
	old := time.Now().Add(-time.Hour)
	require.NoError(tb, os.Chtimes(path, old, old))
}

// writeClaudePluginCache lays out a Claude Code plugin cache under home with
// n installed and enabled plugins, each with a skill, an agent and an MCP
// config, and returns the cache directory.
func writeClaudePluginCache(tb testing.TB, home string, n int) string {
	tb.Helper()
	plugins := filepath.Join(home, ".claude", "plugins")
	cache := filepath.Join(plugins, "cache")
	records := map[string][]map[string]string{}
	enabled := map[string]bool{}
	for i := range n {
		name := fmt.Sprintf("plugin-%02d", i)
		dir := filepath.Join(cache, "market", name, "1.0.0")
		writeBackdated(tb, filepath.Join(dir, ".claude-plugin", "plugin.json"), fmt.Sprintf(`{"name": %q, "version": "1.0.0"}`, name))
		skill := fmt.Sprintf("skill-%02d", i)
		writeBackdated(tb, filepath.Join(dir, "skills", skill, "SKILL.md"), skillName(skill))
		writeBackdated(tb, filepath.Join(dir, "agents", fmt.Sprintf("agent-%02d.md", i)), packAgent(fmt.Sprintf("agent-%02d", i)))
		writeBackdated(tb, filepath.Join(dir, ".mcp.json"), fmt.Sprintf(`{"mcpServers": {"srv-%02d": {"command": "true"}}}`, i))
		id := name + "@market"
		records[id] = []map[string]string{{"scope": "user", "installPath": dir, "version": "1.0.0"}}
		enabled[id] = true
	}
	writeBackdated(tb, filepath.Join(plugins, "installed_plugins.json"), installRecords(records))
	settings, err := json.Marshal(map[string]any{"enabledPlugins": enabled})
	require.NoError(tb, err)
	writeBackdated(tb, filepath.Join(home, ".claude", "settings.json"), string(settings))
	return cache
}

func advertisingResolver() *agentcontext.Resolver {
	return &agentcontext.Resolver{
		PackRowsAdvertised:             func() bool { return true },
		PackAgentDefinitionsAdvertised: func() bool { return true },
		GlobRulesAdvertised:            func() bool { return true },
	}
}

func TestResolve_WarmPassReadsNoBodies(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cache := writeClaudePluginCache(t, home, 50)
	reads := agentcontext.CountBodyReads(t, home)
	r := advertisingResolver()
	roots := []agentcontext.ScanRoot{{Path: cache}}

	cold := r.Resolve(roots)
	coldReads := reads()
	// Each plugin's manifest, skill and agent, then installed_plugins.json
	// and settings.json. The MCP manager reads .mcp.json, not the pass.
	require.Equal(t, int64(3*50+2), coldReads, "the cold pass reads every body")
	skills := 0
	for _, res := range cold.Resources {
		if res.Kind == agentcontext.KindSkill && res.Status == agentcontext.StatusOK {
			skills++
		}
	}
	require.Equal(t, 50, skills, "the fixture loads every plugin")

	warm := r.Resolve(roots)
	require.Equal(t, coldReads, reads(), "the warm pass reads no file bodies")
	require.Equal(t, cold.AggregateHash, warm.AggregateHash)
	require.Equal(t, cold.Resources, warm.Resources)
}

func TestResolve_RuleChangeReadsOneFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for i := range 10 {
		writeBackdated(t, filepath.Join(dir, ".cursor", "rules", fmt.Sprintf("rule-%d.mdc", i)),
			fmt.Sprintf("---\nalwaysApply: true\n---\nRule %d.", i))
	}
	writeBackdated(t, filepath.Join(dir, "AGENTS.md"), "instructions")
	reads := agentcontext.CountBodyReads(t, dir)
	r := advertisingResolver()
	roots := []agentcontext.ScanRoot{{Path: dir}}

	r.Resolve(roots)
	require.Equal(t, int64(11), reads())
	r.Resolve(roots)
	require.Equal(t, int64(11), reads(), "an unchanged tree reads no bodies")

	changed := filepath.Join(dir, ".cursor", "rules", "rule-3.mdc")
	require.NoError(t, os.WriteFile(changed, []byte("---\nalwaysApply: true\n---\nRule three, edited."), 0o600))
	// The edit lands an hour ago so the pass may memoize it again.
	edited := time.Now().Add(-30 * time.Minute)
	require.NoError(t, os.Chtimes(changed, edited, edited))

	snap := r.Resolve(roots)
	require.Equal(t, int64(12), reads(), "the pass after an edit reads exactly the edited file")
	found := false
	for _, res := range snap.Resources {
		if res.Source == changed {
			found = true
			require.Equal(t, "Rule three, edited.", string(res.Payload))
		}
	}
	require.True(t, found)
	r.Resolve(roots)
	require.Equal(t, int64(12), reads())
}

// A file written in the last seconds is not memoized: a second write inside
// the timestamp granularity could keep its stamp.
func TestResolve_RecentFileIsReread(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "AGENTS.md"), "fresh")
	reads := agentcontext.CountBodyReads(t, dir)
	r := &agentcontext.Resolver{}
	roots := []agentcontext.ScanRoot{{Path: dir}}
	r.Resolve(roots)
	r.Resolve(roots)
	require.Equal(t, int64(2), reads())
}

// A file replaced by one with the same size and mtime is read again.
func TestResolve_ReplacedFileIsReread(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	writeBackdated(t, path, "aaaa")
	info, err := os.Stat(path)
	require.NoError(t, err)
	r := &agentcontext.Resolver{}
	roots := []agentcontext.ScanRoot{{Path: dir}}
	require.Equal(t, "aaaa", string(r.Resolve(roots).Resources[0].Payload))

	next := filepath.Join(dir, "next")
	mustWriteFile(t, next, "bbbb")
	require.NoError(t, os.Chtimes(next, info.ModTime(), info.ModTime()))
	require.NoError(t, os.Rename(next, path))
	require.Equal(t, "bbbb", string(r.Resolve(roots).Resources[0].Payload))
}

// A file rewritten in place with its old size and mtime restored, as cp -p
// does, is read again: its ctime moved.
func TestResolve_RestoredMtimeIsReread(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	writeBackdated(t, path, "aaaa")
	info, err := os.Stat(path)
	require.NoError(t, err)
	reads := agentcontext.CountBodyReads(t, dir)
	before, _ := agentcontext.FileChangeTime(info)
	r := &agentcontext.Resolver{}
	roots := []agentcontext.ScanRoot{{Path: dir}}
	require.Equal(t, "aaaa", string(r.Resolve(roots).Resources[0].Payload))
	require.Equal(t, "aaaa", string(r.Resolve(roots).Resources[0].Payload))
	require.Equal(t, int64(1), reads())

	// Rewrite until the ctime moves: it ticks with the kernel's coarse clock.
	require.Eventually(t, func() bool {
		if err := os.WriteFile(path, []byte("bbbb"), 0o600); err != nil {
			return false
		}
		if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
			return false
		}
		now, err := os.Stat(path)
		if err != nil {
			return false
		}
		ctime, _ := agentcontext.FileChangeTime(now)
		return ctime != before
	}, testutil.WaitShort, testutil.IntervalFast)
	after, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, info.ModTime(), after.ModTime())
	require.Equal(t, info.Size(), after.Size())
	require.Equal(t, "bbbb", string(r.Resolve(roots).Resources[0].Payload))
	require.Equal(t, int64(2), reads())
}

func BenchmarkResolve(b *testing.B) {
	home := b.TempDir()
	cache := writeClaudePluginCache(b, home, 50)
	reads := agentcontext.CountBodyReads(b, home)
	r := advertisingResolver()
	roots := []agentcontext.ScanRoot{{Path: cache}}

	b.Run("cold", func(b *testing.B) {
		for b.Loop() {
			b.StopTimer()
			agentcontext.ResetResolveMemo()
			b.StartTimer()
			r.Resolve(roots)
		}
	})
	b.Run("warm", func(b *testing.B) {
		r.Resolve(roots)
		before := reads()
		for b.Loop() {
			r.Resolve(roots)
		}
		if got := reads() - before; got != 0 {
			b.Fatalf("warm passes read %d file bodies, want 0", got)
		}
	})
}

// A connection with no file change reuses the resolve of the previous one:
// the capability probe and the settled snapshot both come from the cache.
func TestRunPush_ReconnectReusesResolve(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		resp agentcontext.PushResponse
		// resolves is the total after the first settle: the ready pass,
		// plus one with the advertised capabilities when they differ.
		resolves uint64
	}{
		{name: "Unadvertised", resp: agentcontext.PushResponse{Accepted: true}, resolves: 1},
		{name: "Advertised", resp: agentcontext.PushResponse{Accepted: true, ForkPluginPacks: true, ForkAgentDefinitions: true, ForkGlobRules: true}, resolves: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			mustWriteFile(t, filepath.Join(dir, "AGENTS.md"), "v1")
			writePack(t, dir, "plugin.json", `{"name": "pstack"}`, "skills", "agents")
			mustWriteFile(t, filepath.Join(dir, "pstack", ".mcp.json"), `{"mcpServers": {"deploy": {"command": "deploy"}}}`)

			delivered := make(chan []agentcontext.PackMCPSource, 16)
			m := newPendingTestManager(t, agentcontext.ManagerOptions{
				WorkingDir: func() string { return dir },
				PackMCPSources: func(s []agentcontext.PackMCPSource) {
					delivered <- s
				},
			})
			runCtx, runCancel := context.WithCancel(testutil.Context(t, testutil.WaitShort))
			defer runCancel()
			go func() { _ = m.Run(runCtx) }()
			require.Eventually(t, func() bool { return agentcontext.ManagerWatching(m) },
				testutil.WaitShort, testutil.IntervalFast)
			m.SetReady()
			require.Eventually(t, func() bool { return m.Snapshot().Version > 0 },
				testutil.WaitShort, testutil.IntervalFast)

			connect := func(name string) []*agentcontext.PushRequest {
				p := newFakePusher()
				p.mu.Lock()
				resp := tc.resp
				p.resp = &resp
				p.mu.Unlock()
				ctx, cancel := context.WithCancel(testutil.Context(t, testutil.WaitShort))
				done := make(chan error, 1)
				go func() {
					done <- m.RunPush(ctx, p, agentcontext.PushOptions{Logger: testutil.Logger(t).Named(name)})
				}()
				settled := waitSettled(t, p, 1)
				cancel()
				require.ErrorIs(t, <-done, context.Canceled)
				return settled
			}

			first := connect("push1")
			require.Equal(t, tc.resolves, agentcontext.ResolveCount(m), "resolves through the first settle")
			second := connect("push2")
			third := connect("push3")
			require.Equal(t, tc.resolves, agentcontext.ResolveCount(m), "a reconnect with no file change resolves nothing")

			require.Equal(t, first[0].AggregateHash, second[0].AggregateHash)
			require.Equal(t, first[0].AggregateHash, third[0].AggregateHash)
			require.Len(t, third[0].Resources, len(first[0].Resources))

			// The settled snapshot of a cache hit still delivers the pack MCP
			// declarations its pass found.
			var last []agentcontext.PackMCPSource
			for len(delivered) > 0 {
				last = <-delivered
			}
			if tc.resp.ForkPluginPacks {
				require.Len(t, last, 1)
			} else {
				require.Empty(t, last)
			}

			// A file change invalidates the cached results: the next
			// connection pushes the new content.
			mustWriteFile(t, filepath.Join(dir, "AGENTS.md"), "v2")
			require.Eventually(t, func() bool {
				for _, res := range m.Snapshot().Resources {
					if res.Kind == agentcontext.KindInstructionFile && string(res.Payload) == "v2" {
						return true
					}
				}
				return false
			}, testutil.WaitShort, testutil.IntervalFast)
			changed := connect("push4")
			require.NotEqual(t, first[0].AggregateHash, changed[0].AggregateHash)
		})
	}
}
