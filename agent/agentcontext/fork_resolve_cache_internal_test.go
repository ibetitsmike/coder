package agentcontext

// This file is fork code: it tests that a resolve pass whose capabilities
// flip while it runs is never cached as a mix of two capability states.

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/testutil"
)

// advertisedCacheManager returns a Manager with every fork capability on
// and a running watcher synced on syncRoots.
func advertisedCacheManager(t *testing.T, opts ManagerOptions, syncRoots []ScanRoot) (*Manager, *Watcher) {
	t.Helper()
	opts.Logger = testutil.Logger(t)
	m := NewManager(opts)
	m.packRowsAdvertised.Store(true)
	m.packAgentDefinitionsAdvertised.Store(true)
	m.globRulesAdvertised.Store(true)
	w, err := NewWatcher(WatcherOptions{Logger: opts.Logger, OnChange: func() {}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })
	w.Sync(testutil.Context(t, testutil.WaitShort), syncRoots)
	_, ok := w.coverage()
	require.True(t, ok, "the watcher covers the roots")
	return m, w
}

// cachedFor returns the cached result for the Manager's current state.
func cachedFor(m *Manager, w *Watcher, roots []ScanRoot) (resolveCacheEntry, bool) {
	gen, _ := w.coverage()
	return m.resolveCache.lookup(m.resolveCaps(), roots, statRootDirs(roots), m.resolveCache.seq.Load(), gen)
}

func TestResolvePass_CapabilityFlipIsNotCachedMixed(t *testing.T) {
	t.Parallel()

	// The resolver reads the pack-rows flag once per root. A flip after
	// the first root and back before the pass ends must not leave a
	// cached snapshot that lacks the second root's pack rows.
	t.Run("Resolver", func(t *testing.T) {
		t.Parallel()
		first, second := t.TempDir(), t.TempDir()
		writePackFiles(t, second, map[string]string{
			"pstack/plugin.json":               `{"name": "pstack"}`,
			"pstack/skills/review-pr/SKILL.md": "---\nname: review-pr\ndescription: A pack skill.\n---\nDo the thing.",
		})
		roots := []ScanRoot{{Path: first}, {Path: second}}
		m, w := advertisedCacheManager(t, ManagerOptions{}, roots)
		var armed atomic.Bool
		armed.Store(true)
		live := m.resolver.PackRowsAdvertised
		m.resolver.PackRowsAdvertised = func() bool {
			v := live()
			if armed.CompareAndSwap(true, false) {
				m.packRowsAdvertised.Store(false)
			}
			return v
		}
		m.resolver.ACPResources = func() []Resource {
			m.packRowsAdvertised.Store(true)
			return nil
		}

		m.resolvePass(context.Background(), m.resolver, roots, w)
		entry, ok := cachedFor(m, w, roots)
		require.True(t, ok, "a pass with pinned capabilities is cached")
		require.True(t, hasResource(entry.snap, KindSkill, "review-pr"), "the cached snapshot holds the pack rows its capabilities call for")
	})

	// The Manager's MCP provider gates pack servers on the live flag.
	t.Run("MCPGate", func(t *testing.T) {
		t.Parallel()
		roots := []ScanRoot{{Path: t.TempDir()}}
		var armed atomic.Bool
		armed.Store(true)
		var m *Manager
		m, w := advertisedCacheManager(t, ManagerOptions{
			MCPCatalog: func() []MCPServerStatus {
				if armed.CompareAndSwap(true, false) {
					m.packRowsAdvertised.Store(false)
				}
				return []MCPServerStatus{
					{Name: "deploy", Connected: true, Tools: []MCPTool{{Name: "run"}}, PackManifest: "/p/plugin.json", PackSource: "/p/.mcp.json"},
				}
			},
			ACPResources: func() []Resource {
				m.packRowsAdvertised.Store(true)
				return nil
			},
		}, roots)

		snap, _ := m.resolvePass(context.Background(), m.resolver, roots, w)
		require.False(t, armed.Load(), "the flip ran during the pass")
		require.False(t, hasResource(snap, KindMCPServer, "deploy"), "the pass saw the flip")
		if entry, ok := cachedFor(m, w, roots); ok {
			require.True(t, hasResource(entry.snap, KindMCPServer, "deploy"), "the cached snapshot holds the pack server its capabilities call for")
		}

		m.resolvePass(context.Background(), m.resolver, roots, w)
		entry, ok := cachedFor(m, w, roots)
		require.True(t, ok, "a pass without a flip is cached")
		require.True(t, hasResource(entry.snap, KindMCPServer, "deploy"))
	})
}

// A change in a directory before its watch existed fired no event, so a
// pass during which a watch was added is not cached.
func TestResolvePass_WatchAddedDuringPassIsNotCached(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	roots := []ScanRoot{{Path: dir}}
	var w *Watcher
	var added atomic.Bool
	// The watcher starts with no watches, as before the Manager's first
	// Sync, so the pass's Sync adds them without any file event.
	m, w := advertisedCacheManager(t, ManagerOptions{
		MCPCatalog: func() []MCPServerStatus {
			if added.CompareAndSwap(false, true) {
				w.Sync(context.Background(), roots)
			}
			return nil
		},
	}, nil)
	before, _ := w.coverage()

	m.resolvePass(context.Background(), m.resolver, roots, w)
	after, _ := w.coverage()
	require.Greater(t, after, before, "the pass added watches")
	_, ok := cachedFor(m, w, roots)
	require.False(t, ok, "a pass that raced a new watch is not cached")

	m.resolvePass(context.Background(), m.resolver, roots, w)
	_, ok = cachedFor(m, w, roots)
	require.True(t, ok, "the next pass is cached")
}

func hasResource(snap Snapshot, kind ResourceKind, name string) bool {
	for _, res := range snap.Resources {
		if res.Kind == kind && res.Name == name {
			return true
		}
	}
	return false
}

// A cache hit while an explicit Resync walks does not make that Resync
// discard its own pass: Resync is a barrier that re-reads the filesystem.
func TestPublishCached_DoesNotSupersedeRunningResync(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	roots := []ScanRoot{{Path: dir}}
	var block atomic.Bool
	entered, release := make(chan struct{}), make(chan struct{})
	m, w := advertisedCacheManager(t, ManagerOptions{
		WorkingDir: func() string { return dir },
		MCPCatalog: func() []MCPServerStatus {
			if block.CompareAndSwap(true, false) {
				close(entered)
				<-release
			}
			return nil
		},
	}, roots)
	m.mu.Lock()
	m.watcher = w
	m.ready = true
	scanRoots := m.scanRootsLocked()
	m.mu.Unlock()
	w.Sync(context.Background(), scanRoots)
	ctx := testutil.Context(t, testutil.WaitShort)
	// The built-in roots can put the home directory under watch, so retry
	// while unrelated events there leave a debounce pending.
	require.Eventually(t, func() bool {
		if _, err := m.Resync(ctx); err != nil {
			return false
		}
		_, ok := cachedFor(m, w, scanRoots)
		return ok
	}, testutil.WaitShort, testutil.IntervalFast)

	block.Store(true)
	done := make(chan Snapshot, 1)
	go func() {
		snap, _ := m.Resync(ctx)
		done <- snap
	}()
	testutil.TryReceive(ctx, t, entered)
	var cached Snapshot
	require.Eventually(t, func() bool {
		var ok bool
		cached, ok = m.publishCached(ctx)
		return ok
	}, testutil.WaitShort, testutil.IntervalFast, "the cache serves while the Resync walks")
	close(release)
	fresh := testutil.TryReceive(ctx, t, done)
	require.Greater(t, fresh.Version, cached.Version, "the Resync publishes its own pass")
}

// A scan root that was missing when the pass ran and has no watch that
// reports its creation, as when it vanished between Sync's scan and its Add,
// invalidates the cached result once it exists.
func TestResolvePass_CreatedRootIsNotServedFromCache(t *testing.T) {
	t.Parallel()
	ws := filepath.Join(t.TempDir(), "ws")
	roots := []ScanRoot{{Path: ws}}
	m, w := advertisedCacheManager(t, ManagerOptions{}, nil)

	m.resolvePass(context.Background(), m.resolver, roots, w)
	_, ok := cachedFor(m, w, roots)
	require.True(t, ok, "the pass over the missing root is cached")

	writePackFiles(t, ws, map[string]string{"AGENTS.md": "v1"})
	_, ok = cachedFor(m, w, roots)
	require.False(t, ok, "the created root misses the cache")
}
