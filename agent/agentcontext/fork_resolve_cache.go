package agentcontext

// This file is fork code: it lets a push connection reuse the snapshot of an
// earlier resolve when nothing it depends on changed since, so connecting,
// disconnecting and reconnecting do not walk the filesystem again. A resolve
// depends on the scan roots, the advertised capabilities and the files under
// the roots; every change to those files reaches the Manager through the
// watcher's signal, which bumps the change sequence and so invalidates every
// cached result. While the watcher cannot report every change (degraded,
// closed, or not started) nothing is cached, and neither is a pass that read
// through a symlink whose target directory has no watch: an edit to such a
// target fires no event.
//
// A cache hit still runs the MCP reconnect, so servers that failed to
// connect are retried on every new coderd connection as before.

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"

	"cdr.dev/slog/v3"
)

// maxResolveCacheEntries bounds the cached results. A connection needs two:
// the capability probe (pack rows off) and the settled snapshot.
const maxResolveCacheEntries = 8

// resolveCaps is the capability state a resolve pass reads.
type resolveCaps struct {
	packRows   bool
	packAgents bool
	globRules  bool
}

func (m *Manager) resolveCaps() resolveCaps {
	rows := m.packRowsAdvertised.Load()
	return resolveCaps{
		packRows:   rows,
		packAgents: rows && m.packAgentDefinitionsAdvertised.Load(),
		globRules:  m.globRulesAdvertised.Load(),
	}
}

type resolveCacheEntry struct {
	caps  resolveCaps
	roots []ScanRoot
	seq   uint64
	// snap is the resolver's output before the Manager stamped a version
	// or a negotiation marker on it.
	snap Snapshot
	pass *packPass
}

// resolveCache holds the results of recent resolve passes.
type resolveCache struct {
	// seq counts the change signals the Manager received. A result is
	// valid only while seq still equals the value read before its pass.
	seq atomic.Uint64
	// resolves counts resolver passes, for tests.
	resolves atomic.Uint64

	mu      sync.Mutex
	entries []resolveCacheEntry
}

func (c *resolveCache) changed() {
	c.seq.Add(1)
}

func (c *resolveCache) store(e resolveCacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if slices.ContainsFunc(c.entries, func(old resolveCacheEntry) bool { return old.seq > e.seq }) {
		// A pass that started after a later change already stored.
		return
	}
	// A result with an older sequence can never match again.
	c.entries = slices.DeleteFunc(c.entries, func(old resolveCacheEntry) bool {
		return old.seq < e.seq || (old.caps == e.caps && slices.Equal(old.roots, e.roots))
	})
	if len(c.entries) >= maxResolveCacheEntries {
		c.entries = c.entries[1:]
	}
	c.entries = append(c.entries, e)
}

func (c *resolveCache) lookup(caps resolveCaps, roots []ScanRoot, seq uint64) (resolveCacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.entries {
		if e.seq == seq && e.caps == caps && slices.Equal(e.roots, roots) {
			return e, true
		}
	}
	return resolveCacheEntry{}, false
}

func (c *resolveCache) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = nil
}

// resolvePass runs one resolver pass over roots and caches its result when
// the watcher reports every change and no change signal or capability flip
// raced the pass. The returned pack pass feeds the pack MCP publisher.
func (m *Manager) resolvePass(ctx context.Context, resolver *Resolver, roots []ScanRoot, watcher *Watcher) (Snapshot, *packPass) {
	start := time.Now()
	seq := m.resolveCache.seq.Load()
	caps := m.resolveCaps()
	m.resolveCache.resolves.Add(1)

	pass := &packPass{}
	r := *resolver
	r.packPass = pass
	followed := trackSymlinkTargets(roots)
	snap := r.ResolveContext(ctx, roots)
	targetDirs := followed.stop()
	resolveMemo.sweep(start)

	if !watcher.reportsAllChanges() {
		m.resolveCache.reset()
		return snap, pass
	}
	if ctx.Err() != nil || m.resolveCaps() != caps || m.resolveCache.seq.Load() != seq {
		return snap, pass
	}
	if !watcher.watchesAll(targetDirs) {
		return snap, pass
	}
	m.resolveCache.store(resolveCacheEntry{caps: caps, roots: slices.Clone(roots), seq: seq, snap: snap, pass: pass})
	return snap, pass
}

// resyncCached is Resync for the push loop: it republishes a cached result
// when nothing changed since it was resolved, and resolves otherwise. Before
// a hit it runs the MCP reconnect that Resync runs, so failed servers are
// retried; a reconnect that changes the catalog signals the Manager, the
// hit turns into a miss, and Resync resolves the new catalog.
func (m *Manager) resyncCached(ctx context.Context) (Snapshot, error) {
	if !m.hasCached() {
		return m.Resync(ctx)
	}
	if m.mcpReconnect != nil {
		if err := m.mcpReconnect(ctx); err != nil {
			m.logger.Warn(ctx, "failed to reconnect MCP servers", slog.Error(err))
		}
	}
	if snap, ok := m.publishCached(ctx); ok {
		return snap, nil
	}
	return m.Resync(ctx)
}

// hasCached reports whether publishCached would find a result right now.
func (m *Manager) hasCached() bool {
	m.mu.Lock()
	watcher := m.watcher
	usable := m.ready && !m.closed
	roots := m.scanRootsLocked()
	m.mu.Unlock()
	if !usable || !watcher.reportsAllChanges() {
		return false
	}
	_, ok := m.resolveCache.lookup(m.resolveCaps(), roots, m.resolveCache.seq.Load())
	return ok
}

// publishCached publishes the cached result for the current scan roots and
// capabilities exactly as Resync publishes a fresh pass.
func (m *Manager) publishCached(ctx context.Context) (Snapshot, bool) {
	if ctx.Err() != nil {
		return Snapshot{}, false
	}
	m.mu.Lock()
	watcher := m.watcher
	m.mu.Unlock()
	if !watcher.reportsAllChanges() {
		return Snapshot{}, false
	}
	seq := m.resolveCache.seq.Load()
	caps := m.resolveCaps()

	m.mu.Lock()
	if m.closed || !m.ready || m.watcher != watcher {
		m.mu.Unlock()
		return Snapshot{}, false
	}
	entry, ok := m.resolveCache.lookup(caps, m.scanRootsLocked(), seq)
	if !ok {
		m.mu.Unlock()
		return Snapshot{}, false
	}
	negotiating := m.negotiating.Load()
	m.resolveEpoch++
	myEpoch := m.resolveEpoch
	snap := entry.snap
	m.version++
	snap.Version = m.version
	m.stampNegotiating(&snap, negotiating)
	m.snapshot = snap
	subs := make([]chan struct{}, 0, len(m.subscribers))
	for ch := range m.subscribers {
		subs = append(subs, ch)
	}
	m.mu.Unlock()
	m.packMCP.publish(entry.pass, myEpoch, negotiating)

	for _, ch := range subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	return snap, true
}

// reportsAllChanges reports whether the watcher is running with every watch
// in place and no debounced event pending, so every change to a resolved
// file has reached the Manager as a signal.
func (w *Watcher) reportsAllChanges() bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return !w.closed && w.watcher != nil && w.degraded == "" && w.timer == nil
}

// watchesAll reports whether every directory in dirs has a watch, compared
// by canonical path: symlink targets come from filepath.EvalSymlinks, and a
// watched path may pass through a symlinked directory. The empty string
// stands for a target that did not resolve and is never watched.
func (w *Watcher) watchesAll(dirs map[string]struct{}) bool {
	if len(dirs) == 0 {
		return true
	}
	if w == nil {
		return false
	}
	w.mu.Lock()
	watched := make([]string, 0, len(w.watched))
	missing := false
	for dir := range dirs {
		if _, ok := w.watched[dir]; !ok {
			missing = true
		}
	}
	if missing {
		for dir := range w.watched {
			watched = append(watched, dir)
		}
	}
	w.mu.Unlock()
	if !missing {
		return true
	}

	canonical := make(map[string]struct{}, len(watched))
	for _, dir := range watched {
		canonical[dir] = struct{}{}
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			canonical[resolved] = struct{}{}
		}
	}
	for dir := range dirs {
		if _, ok := canonical[dir]; !ok || dir == "" {
			return false
		}
	}
	return true
}

// symlinkTargets collects the directories of the symlink targets that the
// running resolve passes read through. A target goes to the passes whose
// scan roots hold the link, and to every running pass when none does, so a
// concurrent pass can only make another pass more cautious.
var symlinkTargets struct {
	mu     sync.Mutex
	active map[*symlinkTargetSet]struct{}
}

type symlinkTargetSet struct {
	roots []string
	dirs  map[string]struct{}
}

func trackSymlinkTargets(roots []ScanRoot) *symlinkTargetSet {
	set := &symlinkTargetSet{}
	for _, root := range roots {
		if root.Path != "" {
			set.roots = append(set.roots, filepath.Clean(root.Path))
		}
	}
	symlinkTargets.mu.Lock()
	defer symlinkTargets.mu.Unlock()
	if symlinkTargets.active == nil {
		symlinkTargets.active = make(map[*symlinkTargetSet]struct{})
	}
	symlinkTargets.active[set] = struct{}{}
	return set
}

func (s *symlinkTargetSet) stop() map[string]struct{} {
	symlinkTargets.mu.Lock()
	defer symlinkTargets.mu.Unlock()
	delete(symlinkTargets.active, s)
	return s.dirs
}

func (s *symlinkTargetSet) holds(link string) bool {
	return slices.ContainsFunc(s.roots, func(root string) bool { return pathHasPrefix(link, root) })
}

func (s *symlinkTargetSet) add(dir string) {
	if s.dirs == nil {
		s.dirs = make(map[string]struct{})
	}
	s.dirs[dir] = struct{}{}
}

// noteSymlinkTarget records the target of the symlink link that a resolve
// pass follows, as filepath.EvalSymlinks returned it. A target that did not
// resolve is recorded as unwatched: creating it later fires no event either.
func noteSymlinkTarget(link, target string, err error) {
	dir := ""
	if err == nil {
		dir = filepath.Dir(target)
	}
	link = filepath.Clean(link)
	symlinkTargets.mu.Lock()
	defer symlinkTargets.mu.Unlock()
	owned := false
	for set := range symlinkTargets.active {
		if set.holds(link) {
			set.add(dir)
			owned = true
		}
	}
	if owned {
		return
	}
	for set := range symlinkTargets.active {
		set.add(dir)
	}
}

// scheduleOnOverflow re-resolves after the kernel dropped events: any of
// them could have been a change to a resolved file.
func (w *Watcher) scheduleOnOverflow(err error) {
	if errors.Is(err, fsnotify.ErrEventOverflow) {
		w.schedule()
	}
}
