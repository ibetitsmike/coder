package agentcontext

// This file is fork code: it tests that a resolve pass whose capabilities
// flip while it runs is never cached as a mix of two capability states, and
// that the fork-context-cache capability gates the cache.

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/testutil"
	"github.com/coder/quartz"
)

// advertisedCacheManager returns a Manager with every fork capability on,
// the context cache included, and a running watcher synced on syncRoots.
func advertisedCacheManager(t *testing.T, opts ManagerOptions, syncRoots []ScanRoot) (*Manager, *Watcher) {
	t.Helper()
	opts.Logger = testutil.Logger(t)
	m := NewManager(opts)
	t.Cleanup(func() { _ = m.Close() })
	m.resolveCache.setEnabled(true)
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

		m.resolvePass(context.Background(), m.resolver, roots, w, 0)
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

		snap, _ := m.resolvePass(context.Background(), m.resolver, roots, w, 0)
		require.False(t, armed.Load(), "the flip ran during the pass")
		require.False(t, hasResource(snap, KindMCPServer, "deploy"), "the pass saw the flip")
		if entry, ok := cachedFor(m, w, roots); ok {
			require.True(t, hasResource(entry.snap, KindMCPServer, "deploy"), "the cached snapshot holds the pack server its capabilities call for")
		}

		m.resolvePass(context.Background(), m.resolver, roots, w, 0)
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

	m.resolvePass(context.Background(), m.resolver, roots, w, 0)
	after, _ := w.coverage()
	require.Greater(t, after, before, "the pass added watches")
	_, ok := cachedFor(m, w, roots)
	require.False(t, ok, "a pass that raced a new watch is not cached")

	m.resolvePass(context.Background(), m.resolver, roots, w, 0)
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

// An event that the watcher scheduled after publishCached read the watch
// generation stops the publish, though its debounce has not signaled yet.
func TestPublishCached_MissesAfterScheduledEvent(t *testing.T) {
	t.Parallel()
	m, w, roots := readyCacheManager(t, t.TempDir(), ManagerOptions{})
	ctx := testutil.Context(t, testutil.WaitShort)
	var scheduled atomic.Bool
	m.resolveCache.afterLockedLookup = func() {
		if _, ok := cachedFor(m, w, roots); ok && scheduled.CompareAndSwap(false, true) {
			w.schedule()
		}
	}
	var published atomic.Bool
	require.Eventually(t, func() bool {
		if _, ok := m.publishCached(ctx); ok && scheduled.Load() {
			published.Store(true)
		}
		if !scheduled.Load() {
			// Events under the home directory can keep a pass from caching.
			_, _ = m.Resync(ctx)
		}
		return scheduled.Load()
	}, testutil.WaitShort, testutil.IntervalFast)
	require.False(t, published.Load(), "a publish that raced a scheduled event")
}

// A pass that could not read a file it found is not cached: the failure can
// be transient and no event follows when it clears. An MCP server that
// failed to connect does not stop caching, since the reconnect before a hit
// retries it.
func TestResolvePass_UnreadableFileIsNotCached(t *testing.T) {
	t.Parallel()
	t.Run("File", func(t *testing.T) {
		t.Parallel()
		if runtime.GOOS == "windows" {
			t.Skip("file permissions are not POSIX modes on Windows")
		}
		if os.Geteuid() == 0 {
			t.Skip("root reads files without read permission")
		}
		dir := t.TempDir()
		writePackFiles(t, dir, map[string]string{"AGENTS.md": "v1", "CLAUDE.md": "v1"})
		require.NoError(t, os.Chmod(filepath.Join(dir, "AGENTS.md"), 0))
		t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "AGENTS.md"), 0o600) })
		roots := []ScanRoot{{Path: dir}}
		m, w := advertisedCacheManager(t, ManagerOptions{}, roots)
		snap, _ := m.resolvePass(context.Background(), m.resolver, roots, w, 0)
		require.True(t, slices.ContainsFunc(snap.Resources, func(r Resource) bool {
			return r.Status == StatusUnreadable && filepath.Base(r.Source) == "AGENTS.md"
		}), "the pass reports AGENTS.md unreadable")
		_, ok := cachedFor(m, w, roots)
		require.False(t, ok, "a pass with an unreadable file is not cached")
	})
	t.Run("MCPServer", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writePackFiles(t, dir, map[string]string{"AGENTS.md": "v1"})
		roots := []ScanRoot{{Path: dir}}
		m, w := advertisedCacheManager(t, ManagerOptions{
			MCPCatalog: func() []MCPServerStatus {
				return []MCPServerStatus{{Name: "down", Err: "connection refused"}}
			},
		}, roots)
		snap, _ := m.resolvePass(context.Background(), m.resolver, roots, w, 0)
		require.True(t, slices.ContainsFunc(snap.Resources, func(r Resource) bool {
			return r.Kind == KindMCPServer && r.Status == StatusUnreadable
		}), "the pass reports the server unreadable")
		_, ok := cachedFor(m, w, roots)
		require.True(t, ok, "a failed MCP server does not stop caching")
	})
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
	m.resolvePass(context.Background(), m.resolver, roots, w, 0)
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

	snap, _ := m.resolvePass(context.Background(), m.resolver, roots, w, 0)
	_, covered := w.coverage()
	require.True(t, covered, "no event during the pass")
	require.True(t, slices.ContainsFunc(snap.Resources, func(res Resource) bool {
		return res.Kind == KindInstructionFile && string(res.Payload) == "v2 is longer"
	}), "the pass read the link")
	_, ok = cachedFor(m, w, roots)
	require.False(t, ok, "the older entry is evicted")
}

// Of two overlapping passes with the same key, the one that claimed the
// later epoch is the one the Manager published, so a pass with an older
// epoch that finishes last does not replace its entry.
func TestResolvePass_OlderEpochDoesNotReplaceEntry(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writePackFiles(t, dir, map[string]string{"AGENTS.md": "v1"})
	roots := []ScanRoot{{Path: dir}}
	m, w := advertisedCacheManager(t, ManagerOptions{}, roots)
	_, newer := m.resolvePass(context.Background(), m.resolver, roots, w, 2)
	_, older := m.resolvePass(context.Background(), m.resolver, roots, w, 1)
	require.NotSame(t, newer, older)
	entry, ok := cachedFor(m, w, roots)
	require.True(t, ok, "the newer pass is cached")
	require.Same(t, newer, entry.pass, "the older pass replaced the newer entry")

	_, newest := m.resolvePass(context.Background(), m.resolver, roots, w, 3)
	entry, ok = cachedFor(m, w, roots)
	require.True(t, ok)
	require.Same(t, newest, entry.pass, "a later pass replaces the entry")
}

// A watch that Sync removes advances the watch generation, so a result that
// relied on it misses.
func TestResolvePass_RemovedWatchMissesCache(t *testing.T) {
	t.Parallel()
	roots := []ScanRoot{{Path: t.TempDir()}}
	m, w := advertisedCacheManager(t, ManagerOptions{}, roots)
	m.resolvePass(context.Background(), m.resolver, roots, w, 0)
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

// The relay keeps taking what fsnotify sends while the event loop waits for
// w.mu. inotify drops a moved watch while it holds its lock, and sends the
// error for a watch the kernel already dropped while it still holds it, so
// a send that waited on the event loop would also block Add and Remove.
// The test holds w.mu while the watched directories move and go, and until
// the relay took at least one send for each of them. With w.mu still held,
// an Add must not block on inotify's lock.
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
	probe := filepath.Join(base, "probe")
	require.NoError(t, os.Mkdir(probe, 0o755))
	w, err := NewWatcher(WatcherOptions{Logger: testutil.Logger(t), OnChange: func() {}})
	require.NoError(t, err)
	ctx := testutil.Context(t, testutil.WaitShort)
	w.Sync(ctx, roots)

	w.mu.Lock()
	require.Len(t, w.watched, len(roots))
	fsw := w.watcher
	before := w.cover.relayed.Load()
	for _, root := range roots {
		require.NoError(t, os.Rename(root.Path, root.Path+".old"))
	}
	for _, root := range roots {
		require.NoError(t, os.RemoveAll(root.Path+".old"))
	}
	relayed := assert.Eventually(t, func() bool {
		return w.cover.relayed.Load()-before >= uint64(len(roots))
	}, testutil.WaitShort, testutil.IntervalFast, "the relay takes sends while w.mu is held")
	added := make(chan error, 1)
	go func() { added <- fsw.Add(probe) }()
	select {
	case err = <-added:
	case <-ctx.Done():
	}
	w.mu.Unlock()
	require.True(t, relayed)
	require.NoError(t, ctx.Err(), "Add is blocked on inotify's lock")
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		w.mu.Lock()
		defer w.mu.Unlock()
		return len(w.watched) == 0
	}, testutil.WaitShort, testutil.IntervalFast, "the event loop forgets every moved watch")
	closed := make(chan struct{})
	go func() {
		_ = w.Close()
		close(closed)
	}()
	testutil.TryReceive(ctx, t, closed)
}

// The relay queue holds at most its bound plus one overflow error. Past the
// bound it drops sends until the event loop takes that error.
func TestRelayQueue_BoundsAndCoalescesOverflow(t *testing.T) {
	t.Parallel()
	q := relayQueue{max: 3}
	event := func(name string) relayItem { return relayItem{ev: fsnotify.Event{Name: name}} }
	for i := range 3 {
		require.False(t, q.push(event(fmt.Sprint(i))))
	}
	require.True(t, q.push(event("3")), "a send past the bound is dropped")
	require.True(t, q.push(event("4")))
	require.Len(t, q.items, 4, "one overflow error stands for every drop")
	require.ErrorIs(t, q.items[3].err, fsnotify.ErrEventOverflow)

	for range 3 {
		q.pop()
	}
	require.True(t, q.push(event("5")), "drops go on until the overflow error is taken")
	q.pop()
	require.Empty(t, q.items)
	require.False(t, q.push(event("6")), "the queue takes sends again")
	require.Equal(t, "6", q.items[0].ev.Name)
}

// fakeFsnotify returns an fsnotify.Watcher whose channels the test sends
// on, for the relay alone.
func fakeFsnotify(t *testing.T) *fsnotify.Watcher {
	t.Helper()
	fsw := &fsnotify.Watcher{Events: make(chan fsnotify.Event), Errors: make(chan error)}
	t.Cleanup(func() {
		close(fsw.Events)
		close(fsw.Errors)
	})
	return fsw
}

// A watcher whose relay dropped sends reports no coverage until the event
// loop took the overflow error and scheduled a re-resolve. The test fills a
// relay that nothing reads past its bound.
func TestWatcher_RelayOverflowUncoversUntilHandled(t *testing.T) {
	t.Parallel()
	// The mock clock keeps the debounce timer armed.
	w, err := NewWatcher(WatcherOptions{Logger: testutil.Logger(t), Clock: quartz.NewMock(t), OnChange: func() {}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })
	w.Sync(testutil.Context(t, testutil.WaitShort), []ScanRoot{{Path: t.TempDir()}})
	_, ok := w.coverage()
	require.True(t, ok)

	ctx := testutil.Context(t, testutil.WaitShort)
	fsw := fakeFsnotify(t)
	events, errs := w.relayFsnotify(fsw)
	// The relay takes a send only after it queued or dropped the one before,
	// so the last send orders the count for the first drop.
	for i := range relayQueueMax + 2 {
		testutil.RequireSend(ctx, t, fsw.Events, fsnotify.Event{Name: fmt.Sprint(i)})
	}
	require.EqualValues(t, 1, w.cover.relayDropped.Load())
	_, ok = w.coverage()
	require.False(t, ok, "a dropped send leaves the watcher uncovered")

	for range relayQueueMax {
		testutil.TryReceive(ctx, t, events)
	}
	overflow := testutil.TryReceive(ctx, t, errs)
	require.ErrorIs(t, overflow, errRelayOverflow)
	w.scheduleOnOverflow(overflow)
	require.Zero(t, w.cover.relayDropped.Load())
	w.mu.Lock()
	scheduled := w.timer != nil
	w.mu.Unlock()
	require.True(t, scheduled, "the overflow schedules a re-resolve")
}

// After Close, the relay keeps taking what fsnotify sends until fsnotify
// closes its channels: fsnotify can wait on a send with the lock that its
// Close takes, and Close closes fsnotify after it closed closedCh.
func TestWatcher_RelayDrainsAfterClose(t *testing.T) {
	t.Parallel()
	ctx := testutil.Context(t, testutil.WaitShort)
	w := &Watcher{closedCh: make(chan struct{})}
	fsw := &fsnotify.Watcher{Events: make(chan fsnotify.Event), Errors: make(chan error)}
	events, errs := w.relayFsnotify(fsw)
	close(w.closedCh)
	for i := range 10 {
		testutil.RequireSend(ctx, t, fsw.Errors, fsnotify.ErrEventOverflow)
		testutil.RequireSend(ctx, t, fsw.Events, fsnotify.Event{Name: fmt.Sprint(i)})
	}
	close(fsw.Events)
	close(fsw.Errors)
	select {
	case _, open := <-events:
		require.False(t, open, "the relay forwards nothing after Close")
	case <-ctx.Done():
		t.Fatal("the relay did not close its events")
	}
	select {
	case _, open := <-errs:
		require.False(t, open, "the relay forwards nothing after Close")
	case <-ctx.Done():
		t.Fatal("the relay did not close its errors")
	}
}

// An event that arms the debounce timer advances the watch generation, so
// a cached result that predates it misses once the timer fired, before the
// Manager took the change signal. OnChange does nothing here, so no signal
// ever comes.
func TestResolvePass_EventMissesCacheBeforeSignal(t *testing.T) {
	t.Parallel()
	ctx := testutil.Context(t, testutil.WaitShort)
	dir := t.TempDir()
	roots := []ScanRoot{{Path: dir}}
	m, _ := advertisedCacheManager(t, ManagerOptions{}, nil)
	clock := quartz.NewMock(t)
	w, err := NewWatcher(WatcherOptions{Logger: testutil.Logger(t), Clock: clock, OnChange: func() {}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })
	w.Sync(ctx, roots)
	m.resolvePass(context.Background(), m.resolver, roots, w, 0)
	_, ok := cachedFor(m, w, roots)
	require.True(t, ok, "the pass is cached")

	w.schedule()
	clock.Advance(w.debounce).MustWait(ctx)
	_, covered := w.coverage()
	require.True(t, covered, "the timer fired")
	_, ok = cachedFor(m, w, roots)
	require.False(t, ok, "the result predates the event")
}

// After dropped events the watcher removes every watch, since a dropped
// removal can leave a dead watch on a path whose new directory reuses the
// inode. The next Sync adds them again.
func TestWatcher_OverflowDropsWatches(t *testing.T) {
	t.Parallel()
	ctx := testutil.Context(t, testutil.WaitShort)
	dir := t.TempDir()
	w, err := NewWatcher(WatcherOptions{Logger: testutil.Logger(t), Clock: quartz.NewMock(t), OnChange: func() {}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })
	w.Sync(ctx, []ScanRoot{{Path: dir}})
	genBefore, _ := w.coverage()

	w.scheduleOnOverflow(fsnotify.ErrEventOverflow)
	w.mu.Lock()
	watched, dirs := len(w.watched), len(w.cover.dirs)
	w.mu.Unlock()
	require.Zero(t, watched, "every watch is removed")
	require.Zero(t, dirs)
	genAfter, _ := w.coverage()
	require.Greater(t, genAfter, genBefore)

	w.Sync(ctx, []ScanRoot{{Path: dir}})
	w.mu.Lock()
	_, rewatched := w.watched[dir]
	w.mu.Unlock()
	require.True(t, rewatched, "Sync adds the watch again")
}

// retargetLink points link at target in one rename.
func retargetLink(t *testing.T, link, target string) {
	t.Helper()
	tmp := link + ".tmp"
	require.NoError(t, os.Symlink(target, tmp))
	require.NoError(t, os.Rename(tmp, link))
}

// A root or a watched directory that a pass read through another directory
// than the one it named at the start, with no event, is not cached: here a
// link is retargeted during the pass and back after it, and nothing watches
// the directory that holds the link.
func TestResolvePass_DirRetargetedDuringPassIsNotCached(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("directory symlinks need privileges on Windows")
	}
	for _, tc := range []struct {
		name string
		// setup returns the scan root, the link and its two targets.
		setup func(t *testing.T, base string) (root, link, a, b string)
	}{{
		name: "Root",
		setup: func(t *testing.T, base string) (string, string, string, string) {
			a, b := filepath.Join(base, "a"), filepath.Join(base, "b")
			writePackFiles(t, a, map[string]string{"AGENTS.md": "in a"})
			writePackFiles(t, b, map[string]string{"AGENTS.md": "in b"})
			ws := filepath.Join(base, "ws")
			require.NoError(t, os.Symlink(a, ws))
			return ws, ws, a, b
		},
	}, {
		name: "Container",
		setup: func(t *testing.T, base string) (string, string, string, string) {
			ws := filepath.Join(base, "ws")
			a, b := filepath.Join(base, "sa"), filepath.Join(base, "sb")
			writePackFiles(t, a, map[string]string{"foo/SKILL.md": "---\nname: foo\ndescription: in a\n---\n"})
			writePackFiles(t, b, map[string]string{"foo/SKILL.md": "---\nname: foo\ndescription: in b\n---\n"})
			// .codex has no watch of its own.
			link := filepath.Join(ws, ".codex", "skills")
			require.NoError(t, os.MkdirAll(filepath.Dir(link), 0o755))
			require.NoError(t, os.Symlink(a, link))
			return ws, link, a, b
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root, link, a, b := tc.setup(t, t.TempDir())
			roots := []ScanRoot{{Path: root}}
			var flip atomic.Bool
			m, w := advertisedCacheManager(t, ManagerOptions{
				// The catalog is read during the pass.
				MCPCatalog: func() []MCPServerStatus {
					if flip.CompareAndSwap(true, false) {
						retargetLink(t, link, b)
					}
					return nil
				},
			}, roots)
			flip.Store(true)
			m.resolvePass(context.Background(), m.resolver, roots, w, 0)
			require.False(t, flip.Load(), "the link was retargeted during the pass")
			_, covered := w.coverage()
			require.True(t, covered, "no event reported the retarget")

			retargetLink(t, link, a)
			require.True(t, w.watchedDirsUnchanged())
			_, ok := cachedFor(m, w, roots)
			require.False(t, ok, "the pass is cached under the directories it did not read through")
		})
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

	m.resolvePass(context.Background(), m.resolver, roots, w, 0)
	_, ok := cachedFor(m, w, roots)
	require.True(t, ok, "the pass over the missing root is cached")

	writePackFiles(t, ws, map[string]string{"AGENTS.md": "v1"})
	_, ok = cachedFor(m, w, roots)
	require.False(t, ok, "the created root misses the cache")
}

// A link retargeted between the stat and the open must not store the new
// target's body under the old target's stamp: once the link points back,
// that stamp would serve the wrong body.
func TestFileMemo_RetargetBetweenStatAndOpenIsNotStored(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json")
	old := time.Now().Add(-time.Hour)
	for path, body := range map[string]string{a: "body of a", b: "body of b"} {
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
		require.NoError(t, os.Chtimes(path, old, old))
	}
	link := filepath.Join(dir, "settings.json")
	require.NoError(t, os.Symlink(a, link))
	statA, err := os.Stat(link)
	require.NoError(t, err)
	if _, ok := stampOf(statA); !ok {
		t.Skip("no file identity on this platform, so the memo is off")
	}
	memo := &fileMemo{entries: make(map[string]*memoEntry), users: 1}
	load := func() ([]byte, fs.FileInfo, error) { return readOpenFile(link, 1<<10) }

	require.NoError(t, os.Remove(link))
	require.NoError(t, os.Symlink(b, link))
	data, err := memo.read(link, statA, load)
	require.NoError(t, err)
	require.Equal(t, "body of b", string(data), "the read returns what the link names now")

	require.NoError(t, os.Remove(link))
	require.NoError(t, os.Symlink(a, link))
	statA, err = os.Stat(link)
	require.NoError(t, err)
	data, err = memo.read(link, statA, load)
	require.NoError(t, err)
	require.Equal(t, "body of a", string(data), "the stamp of a served the body of b")
}

// A permission change on a watched directory fires only a chmod event,
// which the watcher does not act on, and can change what a pass reads. The
// cache misses until a Sync records the new mode, and caches again after.
func TestResolvePass_ChmodWatchedDirMissesCache(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions are not POSIX modes on Windows")
	}
	dir := t.TempDir()
	writePackFiles(t, dir, map[string]string{"AGENTS.md": "v1"})
	roots := []ScanRoot{{Path: dir}}
	m, w := advertisedCacheManager(t, ManagerOptions{}, roots)
	m.resolvePass(context.Background(), m.resolver, roots, w, 0)
	_, ok := cachedFor(m, w, roots)
	require.True(t, ok, "the pass is cached")
	require.True(t, w.watchedDirsUnchanged())

	info, err := os.Stat(dir)
	require.NoError(t, err)
	mode := fs.FileMode(0o755)
	if info.Mode().Perm() == mode {
		mode = 0o700
	}
	require.NoError(t, os.Chmod(dir, mode))
	_, ok = cachedFor(m, w, roots)
	require.False(t, ok, "the chmod root misses the cache")
	require.False(t, w.watchedDirsUnchanged(), "the chmod watched directory is a change")

	genBefore, _ := w.coverage()
	w.Sync(context.Background(), roots)
	genAfter, _ := w.coverage()
	require.Greater(t, genAfter, genBefore, "the recorded mode change advances the generation")
	require.True(t, w.watchedDirsUnchanged(), "Sync records the new mode")
	m.resolvePass(context.Background(), m.resolver, roots, w, 0)
	_, ok = cachedFor(m, w, roots)
	require.True(t, ok, "the pass after Sync is cached again")
}

// beginSync compares only the directories that collectSyncDirs stated
// before Sync took w.mu. A watch added after that, by a concurrent Sync, is
// kept; one whose stat failed is dropped.
func TestWatcher_BeginSyncKeepsWatchesItDidNotStat(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, w := advertisedCacheManager(t, ManagerOptions{}, []ScanRoot{{Path: dir}})
	w.mu.Lock()
	defer w.mu.Unlock()
	require.Contains(t, w.cover.dirs, dir)

	w.beginSync(map[string]os.FileInfo{})
	require.Contains(t, w.watched, dir, "a watch beginSync did not stat is kept")
	require.NotNil(t, w.cover.dirs[dir])

	w.beginSync(map[string]os.FileInfo{dir: nil})
	require.NotContains(t, w.watched, dir, "a watch whose stat failed is dropped")
	require.NotContains(t, w.cover.dirs, dir)
}

// The memo budget charges each entry an overhead beyond its body, so many
// empty files cannot grow the memo without bound.
func TestFileMemo_EmptyFileCountsAgainstBudget(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "empty.md")
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	old := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(path, old, old))
	info, err := os.Stat(path)
	require.NoError(t, err)
	if _, ok := stampOf(info); !ok {
		t.Skip("no file identity on this platform, so the memo is off")
	}
	memo := &fileMemo{entries: make(map[string]*memoEntry), users: 1}
	data, err := memo.read(path, info, func() ([]byte, fs.FileInfo, error) { return readOpenFile(path, 1<<10) })
	require.NoError(t, err)
	require.Empty(t, data)
	require.Contains(t, memo.entries, path, "the empty file is memoized")
	require.Equal(t, memoEntryCost(path, nil), memo.bytes)
	require.Greater(t, memo.bytes, int64(memoEntryOverhead-1), "an empty body is not free")

	memo.forget(path)
	require.Zero(t, memo.bytes)
}

// With the context cache off a pass stores nothing, and turning it off drops
// what it held. Turning it on again starts empty: a result from before the
// flip is never served, even when nothing else changed.
func TestResolveCache_FlipDropsResults(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writePackFiles(t, dir, map[string]string{"AGENTS.md": "v1"})
	roots := []ScanRoot{{Path: dir}}
	m, w := advertisedCacheManager(t, ManagerOptions{}, roots)
	m.resolvePass(context.Background(), m.resolver, roots, w, 0)
	_, ok := cachedFor(m, w, roots)
	require.True(t, ok, "a pass with the cache on is cached")

	require.True(t, m.resolveCache.setEnabled(false))
	require.Empty(t, m.resolveCache.entries, "turning the cache off drops every result")
	resolves := m.resolveCache.resolves.Load()
	m.resolvePass(context.Background(), m.resolver, roots, w, 0)
	require.Equal(t, resolves+1, m.resolveCache.resolves.Load(), "a pass with the cache off resolves")
	require.Empty(t, m.resolveCache.entries, "a pass with the cache off stores nothing")

	require.True(t, m.resolveCache.setEnabled(true))
	_, ok = cachedFor(m, w, roots)
	require.False(t, ok, "the cache starts empty after it turns on")
	m.resolvePass(context.Background(), m.resolver, roots, w, 0)
	_, ok = cachedFor(m, w, roots)
	require.True(t, ok, "a pass after the flip is cached")
	require.False(t, m.resolveCache.setEnabled(true), "the same value is no flip")
}

// A pass that started before the cache flipped stores nothing: it may have
// started from a memo and an entry set that the flip dropped.
func TestResolvePass_FlipDuringPassIsNotCached(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		flips []bool
	}{
		{name: "Off", flips: []bool{false}},
		{name: "OffAndOn", flips: []bool{false, true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writePackFiles(t, dir, map[string]string{"AGENTS.md": "v1"})
			roots := []ScanRoot{{Path: dir}}
			var armed atomic.Bool
			var m *Manager
			m, w := advertisedCacheManager(t, ManagerOptions{
				MCPCatalog: func() []MCPServerStatus {
					if armed.CompareAndSwap(true, false) {
						for _, on := range tc.flips {
							m.resolveCache.setEnabled(on)
						}
					}
					return nil
				},
			}, roots)
			armed.Store(true)
			m.resolvePass(context.Background(), m.resolver, roots, w, 0)
			require.False(t, armed.Load(), "the flip ran during the pass")
			require.Empty(t, m.resolveCache.entries, "a pass that raced a flip is not cached")

			m.resolveCache.setEnabled(true)
			m.resolvePass(context.Background(), m.resolver, roots, w, 0)
			_, ok := cachedFor(m, w, roots)
			require.True(t, ok, "the next pass is cached")
		})
	}
}

// A directory the pass failed to read for a reason other than it being
// missing, such as EMFILE, can become readable with no event, so the pass is
// not cached. A missing directory fires an event when it is created.
func TestResolvePass_ReadDirFailureIsNotCached(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		err    error
		cached bool
	}{
		{name: "EMFILE", err: &fs.PathError{Op: "open", Err: syscall.EMFILE}, cached: false},
		{name: "EIO", err: &fs.PathError{Op: "readdirent", Err: syscall.EIO}, cached: false},
		{name: "NotExist", err: &fs.PathError{Op: "open", Err: fs.ErrNotExist}, cached: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writePackFiles(t, dir, map[string]string{
				"AGENTS.md":                   "v1",
				".claude/skills/foo/SKILL.md": "---\nname: foo\ndescription: A skill.\n---\nbody",
			})
			container := filepath.Join(dir, ".claude", "skills")
			roots := []ScanRoot{{Path: dir}}
			m, w := advertisedCacheManager(t, ManagerOptions{}, roots)
			// The fault goes in after the watcher's Sync: a failure outside
			// any pass of this test would mark the passes of other tests.
			readFaults.Store(container, tc.err)
			t.Cleanup(func() { readFaults.Delete(container) })

			snap, _ := m.resolvePass(context.Background(), m.resolver, roots, w, 0)
			require.False(t, hasResource(snap, KindSkill, "foo"), "the pass could not list the skills")
			_, ok := cachedFor(m, w, roots)
			require.Equal(t, tc.cached, ok)

			readFaults.Delete(container)
			snap, _ = m.resolvePass(context.Background(), m.resolver, roots, w, 0)
			require.True(t, hasResource(snap, KindSkill, "foo"), "the pass after the failure lists the skills")
			_, ok = cachedFor(m, w, roots)
			require.True(t, ok, "a pass with no failure is cached")
		})
	}
}

// A pass that cannot read the metadata of a path it would discover skips the
// path, as with a failed ReadDir, so it is not cached either.
func TestResolvePass_MetadataFailureIsNotCached(t *testing.T) {
	t.Parallel()
	eio := &fs.PathError{Op: "stat", Err: syscall.EIO}
	for _, tc := range []struct {
		name string
		// fault is the path whose metadata call fails, relative to the root.
		fault string
		// gone is the resource source the failure hides, relative to the
		// root.
		gone   string
		err    error
		cached bool
	}{
		{name: "RootStat", fault: ".", gone: "AGENTS.md", err: eio},
		{name: "TopLevelInfo", fault: "AGENTS.md", gone: "AGENTS.md", err: eio},
		{name: "SkillContainerStat", fault: ".claude/skills", gone: ".claude/skills/foo", err: eio},
		{name: "SkillMetaLstat", fault: ".claude/skills/foo/SKILL.md", gone: ".claude/skills/foo", err: eio},
		{name: "AgentLstat", fault: ".claude/agents/rev.md", gone: ".claude/agents/rev.md", err: eio},
		{name: "RuleContainerEval", fault: ".cursor/rules", gone: ".cursor/rules/top.mdc", err: eio},
		{name: "RuleSubfolder", fault: ".cursor/rules/sub", gone: ".cursor/rules/sub/nested.mdc", err: eio},
		{name: "NotExist", fault: ".claude/skills/foo/SKILL.md", gone: ".claude/skills/foo", err: &fs.PathError{Op: "lstat", Err: fs.ErrNotExist}, cached: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writePackFiles(t, dir, map[string]string{
				"AGENTS.md":                    "v1",
				".claude/skills/foo/SKILL.md":  "---\nname: foo\ndescription: A skill.\n---\nbody",
				".claude/agents/rev.md":        "---\nname: rev\ndescription: Reviews.\n---\nReview.",
				".cursor/rules/top.mdc":        "---\nalwaysApply: true\n---\nTop.",
				".cursor/rules/sub/nested.mdc": "---\nalwaysApply: true\n---\nNested.",
			})
			roots := []ScanRoot{{Path: dir}}
			m, w := advertisedCacheManager(t, ManagerOptions{}, roots)
			gone := filepath.Join(dir, filepath.FromSlash(tc.gone))
			has := func(snap Snapshot) bool {
				return slices.ContainsFunc(snap.Resources, func(res Resource) bool { return res.Source == gone })
			}
			snap, _ := m.resolvePass(context.Background(), m.resolver, roots, w, 0)
			require.True(t, has(snap), "the fixture discovers %s", tc.gone)

			fault := filepath.Join(dir, filepath.FromSlash(tc.fault))
			readFaults.Store(fault, tc.err)
			t.Cleanup(func() { readFaults.Delete(fault) })
			snap, _ = m.resolvePass(context.Background(), m.resolver, roots, w, 0)
			require.False(t, has(snap), "the failure hides %s", tc.gone)
			_, ok := cachedFor(m, w, roots)
			require.Equal(t, tc.cached, ok)

			readFaults.Delete(fault)
			snap, _ = m.resolvePass(context.Background(), m.resolver, roots, w, 0)
			require.True(t, has(snap), "the pass after the failure discovers %s", tc.gone)
			_, ok = cachedFor(m, w, roots)
			require.True(t, ok, "a pass with no failure is cached")
		})
	}
}

// The file memo stores bodies only while a resolve cache holds it on, and
// drops them when the last one lets go.
func TestFileMemo_StoresOnlyWhileHeld(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	require.NoError(t, os.WriteFile(path, []byte("body"), 0o600))
	old := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(path, old, old))
	info, err := os.Stat(path)
	require.NoError(t, err)
	if _, ok := stampOf(info); !ok {
		t.Skip("no file identity on this platform, so the memo is off")
	}
	memo := &fileMemo{entries: make(map[string]*memoEntry)}
	loads := 0
	var during func()
	load := func() ([]byte, fs.FileInfo, error) {
		loads++
		if during != nil {
			during()
		}
		return readOpenFile(path, 1<<10)
	}
	read := func() {
		t.Helper()
		data, err := memo.read(path, info, load)
		require.NoError(t, err)
		require.Equal(t, "body", string(data))
	}

	read()
	read()
	require.Equal(t, 2, loads, "an unheld memo loads every time")
	require.Empty(t, memo.entries)

	memo.acquire()
	memo.acquire()
	read()
	read()
	require.Equal(t, 3, loads, "a held memo serves the stored body")
	memo.release()
	require.Contains(t, memo.entries, path, "the memo stays on while a user holds it")
	memo.release()
	require.Empty(t, memo.entries, "the last release drops every body")
	require.Zero(t, memo.bytes)

	// A read that started before the last release stores nothing, even
	// when the memo is held again by the time it ends.
	memo.acquire()
	during = func() {
		memo.release()
		memo.acquire()
	}
	read()
	require.Empty(t, memo.entries, "a read across a release is not stored")
	during = nil
	read()
	require.Contains(t, memo.entries, path)
	memo.release()
}
