package agentcontext

// This file is fork code: it tests that a resolve pass whose capabilities
// flip while it runs is never cached as a mix of two capability states.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
	// cached snapshot that lacks the second root's pack rows. The pass
	// pins the flags it read at its start, so the live reader below, which
	// flips the flag on its first call, is never called.
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
		require.True(t, armed.Load(), "the pass reads its pinned flags, not the live ones")
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

// readyCacheManager returns a ready Manager over dir, with a running
// watcher, whose current state is cached.
func readyCacheManager(t *testing.T, dir string, opts ManagerOptions) (*Manager, *Watcher, []ScanRoot) {
	t.Helper()
	opts.WorkingDir = func() string { return dir }
	m, w := advertisedCacheManager(t, opts, []ScanRoot{{Path: dir}})
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
	return m, w, scanRoots
}

// A cached result does not publish while an explicit Resync walks: it would
// land after the Resync's fresh pass when the Resync publishes first, and
// restore the old snapshot and pack MCP list.
func TestPublishCached_MissesWhileResyncRuns(t *testing.T) {
	t.Parallel()
	var block atomic.Bool
	entered, release := make(chan struct{}), make(chan struct{})
	m, _, _ := readyCacheManager(t, t.TempDir(), ManagerOptions{
		MCPCatalog: func() []MCPServerStatus {
			if block.CompareAndSwap(true, false) {
				close(entered)
				<-release
			}
			return nil
		},
	})
	ctx := testutil.Context(t, testutil.WaitShort)

	block.Store(true)
	done := make(chan Snapshot, 1)
	go func() {
		snap, _ := m.Resync(ctx)
		done <- snap
	}()
	testutil.TryReceive(ctx, t, entered)
	require.Never(t, func() bool {
		_, ok := m.publishCached(ctx)
		return ok
	}, testutil.IntervalSlow, testutil.IntervalFast, "the cache does not serve while the Resync walks")
	close(release)
	fresh := testutil.TryReceive(ctx, t, done)

	var cached Snapshot
	require.Eventually(t, func() bool {
		var ok bool
		if cached, ok = m.publishCached(ctx); ok {
			return true
		}
		// Events under the home directory can keep a pass from caching.
		_, _ = m.Resync(ctx)
		return false
	}, testutil.WaitShort, testutil.IntervalFast, "the cache serves once the Resync published")
	require.Greater(t, cached.Version, fresh.Version)
}

// A change signal that arrives after publishCached read the sequence but
// before it publishes stops the publish.
func TestPublishCached_MissesAfterChangeSignal(t *testing.T) {
	t.Parallel()
	m, _, _ := readyCacheManager(t, t.TempDir(), ManagerOptions{})
	ctx := testutil.Context(t, testutil.WaitShort)
	var signaled atomic.Bool
	m.resolveCache.afterLockedLookup = func() {
		if signaled.CompareAndSwap(false, true) {
			m.resolveCache.changed()
		}
	}
	var published atomic.Bool
	require.Eventually(t, func() bool {
		if _, ok := m.publishCached(ctx); ok && signaled.Load() {
			published.Store(true)
		}
		return signaled.Load()
	}, testutil.WaitShort, testutil.IntervalFast)
	require.False(t, published.Load(), "a publish that raced a change signal")
}

// A capability change that lands after publishCached looked the entry up
// stops the publish: it bumps no sequence, and the entry holds the old
// capability state.
func TestPublishCached_MissesAfterCapabilityChange(t *testing.T) {
	t.Parallel()
	m, w, roots := readyCacheManager(t, t.TempDir(), ManagerOptions{})
	ctx := testutil.Context(t, testutil.WaitShort)
	var flipped atomic.Bool
	m.resolveCache.afterLockedLookup = func() {
		// Flip only when the locked lookup found the entry, so the miss
		// below comes from the flip.
		if _, ok := cachedFor(m, w, roots); ok && flipped.CompareAndSwap(false, true) {
			m.globRulesAdvertised.Store(!m.globRulesAdvertised.Load())
		}
	}
	var published atomic.Bool
	require.Eventually(t, func() bool {
		if _, ok := m.publishCached(ctx); ok && flipped.Load() {
			published.Store(true)
		}
		if !flipped.Load() {
			// Events under the home directory can keep a pass from caching.
			_, _ = m.Resync(ctx)
		}
		return flipped.Load()
	}, testutil.WaitShort, testutil.IntervalFast)
	require.False(t, published.Load(), "a publish that raced a capability change")
}

// A completed pass that cannot be cached read the files as they are now, so
// an older entry for the same state goes: here AGENTS.md became a link
// whose target the watcher drops writes to, and no change signal reached
// the cache, as when an explicit Resync wins the race with the event.
func TestResolvePass_UncacheablePassEvictsOlderEntry(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writePackFiles(t, dir, map[string]string{"AGENTS.md": "v1", "README.md": "v2 is longer"})
	roots := []ScanRoot{{Path: dir}}
	m, w := advertisedCacheManager(t, ManagerOptions{}, roots)
	m.resolvePass(context.Background(), m.resolver, roots, w)
	_, ok := cachedFor(m, w, roots)
	require.True(t, ok, "the first pass is cached")

	require.NoError(t, os.Remove(filepath.Join(dir, "AGENTS.md")))
	require.NoError(t, os.Symlink(filepath.Join(dir, "README.md"), filepath.Join(dir, "AGENTS.md")))
	// Let the event's debounce run out, so the pass below sees the watcher
	// covering everything and only its symlink check refuses to cache it.
	require.Eventually(t, func() bool {
		w.mu.Lock()
		defer w.mu.Unlock()
		return w.timer != nil
	}, testutil.WaitShort, testutil.IntervalFast, "the replacement fires an event")
	require.Eventually(t, func() bool {
		_, ok := w.coverage()
		return ok
	}, testutil.WaitShort, testutil.IntervalFast)

	snap, _ := m.resolvePass(context.Background(), m.resolver, roots, w)
	_, covered := w.coverage()
	require.True(t, covered, "no event during the pass")
	require.True(t, slices.ContainsFunc(snap.Resources, func(res Resource) bool {
		return res.Kind == KindInstructionFile && string(res.Payload) == "v2 is longer"
	}), "the pass read the link")
	_, ok = cachedFor(m, w, roots)
	require.False(t, ok, "the older entry is evicted")
}

// A watch that Sync removes advances the watch generation, so a result that
// relied on it misses.
func TestResolvePass_RemovedWatchMissesCache(t *testing.T) {
	t.Parallel()
	roots := []ScanRoot{{Path: t.TempDir()}}
	m, w := advertisedCacheManager(t, ManagerOptions{}, roots)
	m.resolvePass(context.Background(), m.resolver, roots, w)
	_, ok := cachedFor(m, w, roots)
	require.True(t, ok, "the pass is cached")

	w.Sync(context.Background(), nil)
	_, ok = cachedFor(m, w, roots)
	require.False(t, ok, "the result relied on a watch that is gone")
}

// A watch whose directory could not be stated after its Add, and that
// exists again, is replaced by the next Sync.
func TestWatcher_SyncReplacesWatchWithoutIdentity(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	roots := []ScanRoot{{Path: dir}}
	_, w := advertisedCacheManager(t, ManagerOptions{}, roots)
	w.mu.Lock()
	_, ok := w.cover.dirs[dir]
	require.True(t, ok, "the root has a watch")
	w.cover.dirs[dir] = nil
	w.mu.Unlock()

	w.Sync(context.Background(), roots)
	w.mu.Lock()
	defer w.mu.Unlock()
	require.NotNil(t, w.cover.dirs[dir], "the watch was added again")
	require.Contains(t, w.watched, dir)
}

// The event loop keeps reading Events and Errors while watched directories
// are moved away and deleted. inotify drops a moved watch while it holds
// its lock, and sends the error for a watch the kernel already dropped
// while it still holds it, so the event loop must take that send without
// waiting on w.mu (relayFsnotify). Holding w.mu while the directories move
// and go queues their events, so inotify handles them back to back. Short
// holds of w.mu after that, as Sync and passes take, delay the loop after
// each event so inotify takes its lock first.
func TestWatcher_MovedWatchDirsDoNotBlockEventLoop(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("the blocking send is in the inotify backend")
	}
	base := t.TempDir()
	roots := make([]ScanRoot, 50)
	for i := range roots {
		roots[i] = ScanRoot{Path: filepath.Join(base, fmt.Sprintf("r%d", i))}
		require.NoError(t, os.Mkdir(roots[i].Path, 0o755))
	}
	w, err := NewWatcher(WatcherOptions{Logger: testutil.Logger(t), OnChange: func() {}})
	require.NoError(t, err)
	w.Sync(testutil.Context(t, testutil.WaitShort), roots)

	w.mu.Lock()
	require.Len(t, w.watched, len(roots))
	for _, root := range roots {
		require.NoError(t, os.Rename(root.Path, root.Path+".old"))
	}
	for _, root := range roots {
		require.NoError(t, os.RemoveAll(root.Path+".old"))
	}
	w.mu.Unlock()

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			w.mu.Lock()
			time.Sleep(50 * time.Microsecond)
			w.mu.Unlock()
			runtime.Gosched()
		}
	}()
	if !assert.Eventually(t, func() bool {
		w.mu.Lock()
		defer w.mu.Unlock()
		return len(w.watched) == 0
	}, testutil.WaitShort, testutil.IntervalFast, "the event loop forgets every moved watch") {
		return
	}
	closed := make(chan struct{})
	go func() {
		_ = w.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(testutil.WaitShort):
		t.Fatal("Close is blocked")
	}
}

// Sync calls Remove and Add with w.mu held, the event loop takes w.mu for
// each event, and inotify holds its lock while it sends the error for a
// moved watch that the kernel already dropped. So a Sync while those events
// are queued finishes only when the event loop takes the send without
// waiting on w.mu. Each round queues a batch of them while holding w.mu,
// as TestWatcher_MovedWatchDirsDoNotBlockEventLoop does, and syncs right
// away. The retargeted link makes every Sync replace its watch (beginSync)
// as well as remove the others.
func TestWatcher_SyncDuringMovedWatchDirsDoesNotDeadlock(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("the blocking send is in the inotify backend")
	}
	base := t.TempDir()
	targets := []string{filepath.Join(base, "a"), filepath.Join(base, "b")}
	for _, dir := range targets {
		require.NoError(t, os.Mkdir(dir, 0o755))
	}
	link := filepath.Join(base, "link")
	require.NoError(t, os.Symlink(targets[0], link))
	w, err := NewWatcher(WatcherOptions{Logger: testutil.Logger(t), OnChange: func() {}})
	require.NoError(t, err)
	ctx := testutil.Context(t, testutil.WaitMedium)
	// A Sync that deadlocks fails the test instead of hanging it.
	syncNow := func(roots []ScanRoot) {
		synced := make(chan struct{})
		go func() {
			defer close(synced)
			w.Sync(ctx, roots)
		}()
		testutil.TryReceive(ctx, t, synced)
	}

	for round := range 10 {
		roots := []ScanRoot{{Path: link}}
		for i := range 30 {
			dir := filepath.Join(base, fmt.Sprintf("r%d-%d", round, i))
			require.NoError(t, os.Mkdir(dir, 0o755))
			roots = append(roots, ScanRoot{Path: dir})
		}
		syncNow(roots)

		w.mu.Lock()
		require.Len(t, w.watched, len(roots))
		for _, root := range roots[1:] {
			require.NoError(t, os.Rename(root.Path, root.Path+".old"))
		}
		for _, root := range roots[1:] {
			require.NoError(t, os.RemoveAll(root.Path+".old"))
		}
		tmp := fmt.Sprintf("%s.%d", link, round)
		require.NoError(t, os.Symlink(targets[(round+1)%2], tmp))
		require.NoError(t, os.Rename(tmp, link))
		w.mu.Unlock()

		syncNow(roots[:1])
		w.mu.Lock()
		_, watched := w.watched[link]
		w.mu.Unlock()
		require.True(t, watched, "the link is watched after Sync")
	}

	closed := make(chan struct{})
	go func() {
		_ = w.Close()
		close(closed)
	}()
	testutil.TryReceive(ctx, t, closed)
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
