package agentcontext

// This file is fork code: it lets a push connection reuse the snapshot of an
// earlier resolve when nothing it depends on changed since, so connecting,
// disconnecting and reconnecting do not walk the filesystem again. A resolve
// depends on the scan roots, the advertised capabilities and the files under
// the roots; every change to those files reaches the Manager through the
// watcher's signal, which bumps the change sequence and so invalidates every
// cached result.
//
// A result is cached only while the watcher can report every change to what
// the pass read:
//   - the watcher runs, is not degraded, has no debounced event pending, and
//     its latest Sync added every directory it wanted;
//   - every scan root and every watched directory is still the directory it
//     was, because retargeting a symlink to a directory, or creating a root
//     that was missing, can fire no event the watcher sees;
//   - no watch was added or removed since the pass started, because a change
//     in a newly watched directory before its watch existed fired no event,
//     and a removed watch reports nothing;
//   - every symlink the pass followed ends in a watched directory, under a
//     name whose writes the watcher reports, and still resolves to the same
//     target, because retargeting a directory link inside the target path
//     fires no event the watcher sees.
//
// A pass that completes but cannot be cached drops the cached results: it
// read the files as they are now, and an older result can predate a change
// whose event has not arrived. A cached result does not publish while a
// fresh pass has claimed its epoch and not yet published, or after a
// change signal.
//
// The pass reads the capabilities once, at its start, so a capability flip
// during the pass cannot mix two capability states into one snapshot.
//
// A cache hit still runs the MCP reconnect, so servers that failed to
// connect are retried on every new coderd connection as before.
//
// A pass that failed to read a file or a directory for a reason other than
// it being missing, such as EMFILE or an I/O error, is not cached either: no
// event follows when the failure clears.
//
// The cache sits behind the fork-context-cache capability, which coderd
// sets in its push responses while the experiment of the same name is on.
// Until a response sets it, and from the response that clears it, every
// pass resolves in full and the cache and the file memo are neither read
// nor stored. A flip in either direction drops every cached result, and a
// pass that started before a flip stores nothing.
//
// The tradeoff: a change the watcher misses for a reason not listed above
// used to show up on the next reconnect, which resolved again. With the
// cache it shows up on the next watcher event instead.

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"golang.org/x/xerrors"

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

// pin makes the resolver read caps instead of the live capability flags.
func (c resolveCaps) pin(r *Resolver) {
	r.PackRowsAdvertised = func() bool { return c.packRows }
	r.PackAgentDefinitionsAdvertised = func() bool { return c.packAgents }
	r.GlobRulesAdvertised = func() bool { return c.globRules }
}

type resolveCacheEntry struct {
	caps  resolveCaps
	roots []ScanRoot
	// rootDirs are the scan roots' directories when the pass started.
	rootDirs []os.FileInfo
	seq      uint64
	// watchGen is the watcher's watch generation when the pass started.
	watchGen uint64
	// epoch is the Manager resolve epoch the pass claimed. Of two passes
	// with the same key, the one that started later is the one the Manager
	// published, so its result wins whichever pass finishes last.
	epoch uint64
	// flips is resolveCache.flips when the pass started.
	flips uint64
	// snap is the resolver's output before the Manager stamped a version
	// or a negotiation marker on it.
	snap Snapshot
	pass *packPass
	// links maps each symlink the pass followed to its resolved target.
	// Retargeting a directory link inside a target path fires no event
	// where the watcher looks, so a hit resolves every link again.
	links map[string]string
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
	// on is the fork-context-cache capability the server last reported.
	// While it is off no pass reads or stores a result.
	on bool
	// flips counts the changes of on, so a pass that started before a
	// flip stores nothing.
	flips uint64
	// closed is set when the Manager closed. The cache stays off then.
	closed bool

	// afterLockedLookup, when set by a test, runs in publishCached between
	// its locked lookup and its sequence check.
	afterLockedLookup func()
}

func (c *resolveCache) changed() {
	c.seq.Add(1)
}

// setEnabled turns the cache on or off and reports whether that changed
// it. A change drops every result. While a cache is on it holds the file
// memo on.
func (c *resolveCache) setEnabled(enable bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.setEnabledLocked(enable)
}

func (c *resolveCache) setEnabledLocked(enable bool) bool {
	on := enable && !c.closed
	changed := c.on != on
	c.on = on
	if !changed {
		return false
	}
	c.flips++
	c.entries = nil
	if c.on {
		resolveMemo.acquire()
	} else {
		resolveMemo.release()
	}
	return true
}

// close turns the cache off for good, when the Manager closes. It sets
// closed and turns the cache off in one critical section, so a response
// that a closing push loop still applies cannot turn it on again.
func (c *resolveCache) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	c.setEnabledLocked(false)
}

// state reports whether the cache is on and its flip count.
func (c *resolveCache) state() (on bool, flips uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.on, c.flips
}

// setContextCache applies the fork-context-cache capability of a push
// response, nil standing for a peer that sent none. Unlike the other
// capabilities it carries over to the next connection, so a reconnect can
// reuse the results; the response to that connection's first push sets it
// again.
func (m *Manager) setContextCache(resp *PushResponse) {
	on := resp != nil && resp.ForkContextCache
	if m.resolveCache.setEnabled(on) {
		m.logger.Debug(context.Background(), "server flipped the context-cache advertisement", slog.F("on", on))
	}
}

func (c *resolveCache) store(e resolveCacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.on || c.flips != e.flips {
		// The cache is off, or it flipped since the pass started.
		return
	}
	if slices.ContainsFunc(c.entries, func(old resolveCacheEntry) bool {
		return old.seq > e.seq || old.watchGen > e.watchGen ||
			(old.epoch > e.epoch && old.caps == e.caps && slices.Equal(old.roots, e.roots))
	}) {
		// A pass that started after a later change, or a later pass with
		// the same key, already stored.
		return
	}
	// A result with an older sequence or generation can never match again.
	c.entries = slices.DeleteFunc(c.entries, func(old resolveCacheEntry) bool {
		return old.seq < e.seq || old.watchGen < e.watchGen || (old.caps == e.caps && slices.Equal(old.roots, e.roots))
	})
	if len(c.entries) >= maxResolveCacheEntries {
		c.entries = c.entries[1:]
	}
	c.entries = append(c.entries, e)
}

func (c *resolveCache) lookup(caps resolveCaps, roots []ScanRoot, rootDirs []os.FileInfo, seq, watchGen uint64) (resolveCacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.on {
		return resolveCacheEntry{}, false
	}
	for _, e := range c.entries {
		if e.seq == seq && e.watchGen == watchGen && e.caps == caps && slices.Equal(e.roots, roots) && sameDirs(e.rootDirs, rootDirs) {
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
// the watcher covers everything the pass read and no change signal raced
// the pass. epoch is the resolve epoch the pass claimed. The returned pack
// pass feeds the pack MCP publisher.
func (m *Manager) resolvePass(ctx context.Context, resolver *Resolver, roots []ScanRoot, watcher *Watcher, epoch uint64) (Snapshot, *packPass) {
	on, flips := m.resolveCache.state()
	if !on {
		return m.resolveUncached(ctx, resolver, roots)
	}
	start := time.Now()
	watchGen, _ := watcher.coverage()
	seq := m.resolveCache.seq.Load()
	caps := m.resolveCaps()
	rootDirs := statRootDirs(roots)
	m.resolveCache.resolves.Add(1)

	pass := &packPass{}
	r := *resolver
	r.packPass = pass
	caps.pin(&r)
	// The Manager's MCP provider gates pack servers on the live pack-rows
	// flag. A flip around that read would mix capability states.
	mixed := false
	if mcp := r.MCPResources; mcp != nil {
		r.MCPResources = func() []Resource {
			before := m.packRowsAdvertised.Load()
			resources := mcp()
			if before != caps.packRows || m.packRowsAdvertised.Load() != caps.packRows {
				mixed = true
			}
			return resources
		}
	}
	followed := trackSymlinkTargets(roots)
	snap := r.ResolveContext(ctx, roots)
	links, readFailed := followed.stopPass()
	resolveMemo.sweep(start)

	if ctx.Err() != nil {
		// A canceled pass is partial and proves nothing about the files.
		return snap, pass
	}
	genAfter, covered := watcher.coverage()
	if !covered || mixed || readFailed || genAfter != watchGen || m.resolveCache.seq.Load() != seq || !watcher.reportsWritesTo(links) || hasUnreadableFile(snap) {
		// This pass read the files as they are now and cannot be cached.
		// An older entry with the same key can predate a change whose
		// event has not arrived yet, so it goes too.
		m.resolveCache.reset()
		return snap, pass
	}
	if !sameDirs(rootDirs, statRootDirs(roots)) || !watcher.watchedDirsUnchanged() {
		// A root or a watched directory named another directory at some
		// point of the pass, as when a symlink was retargeted, and no
		// watch reports that. An entry keyed on the directories at the
		// start could hold what the pass read through the other one.
		m.resolveCache.reset()
		return snap, pass
	}
	m.resolveCache.store(resolveCacheEntry{caps: caps, roots: slices.Clone(roots), rootDirs: rootDirs, seq: seq, watchGen: watchGen, epoch: epoch, flips: flips, snap: snap, pass: pass, links: links})
	return snap, pass
}

// resolveUncached is resolvePass with the cache off: the resolver reads the
// live capability flags, as it did before the cache, and nothing is
// stored. It tracks its own symlinks and read failures so they do not make
// a concurrent cached pass more cautious.
func (m *Manager) resolveUncached(ctx context.Context, resolver *Resolver, roots []ScanRoot) (Snapshot, *packPass) {
	m.resolveCache.resolves.Add(1)
	pass := &packPass{}
	r := *resolver
	r.packPass = pass
	tracked := trackSymlinkTargets(roots)
	defer tracked.stop()
	return r.ResolveContext(ctx, roots), pass
}

// hasUnreadableFile reports whether a pass failed to read a file it found.
// The failure can be transient, such as EMFILE or an I/O error on a network
// filesystem, and no event follows when it clears, so such a pass is not
// cached and the next connection reads again. MCP servers that failed to
// connect are left out: the MCP reconnect before a hit retries them.
func hasUnreadableFile(snap Snapshot) bool {
	for _, resources := range [][]Resource{snap.Resources, snap.ScopedRules} {
		for _, r := range resources {
			if r.Status == StatusUnreadable && r.Kind != KindMCPServer && r.Kind != KindACPHarness {
				return true
			}
		}
	}
	return false
}

// resyncCached is Resync for the push loop: it republishes a cached result
// when nothing changed since it was resolved, and resolves otherwise. Before
// a hit it runs the MCP reconnect that Resync runs, so failed servers are
// retried; a reconnect that changes the catalog signals the Manager, the
// hit turns into a miss, and Resync resolves the new catalog. That miss
// reconnects a second time, which is cheap: the reconnect only connects
// servers that are not connected.
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
	if !usable {
		return false
	}
	watchGen, covered := watcher.coverage()
	if !covered {
		return false
	}
	_, ok := m.resolveCache.lookup(m.resolveCaps(), roots, statRootDirs(roots), m.resolveCache.seq.Load(), watchGen)
	return ok
}

// publishCached publishes the cached result for the current scan roots and
// capabilities as Resync publishes a fresh pass. It misses while a fresh
// pass has claimed a resolve epoch and not yet published, so a cached
// result never lands after the fresh pass that an explicit Resync runs.
// It takes no epoch of its own, and a fresh pass that starts after it
// supersedes it.
func (m *Manager) publishCached(ctx context.Context) (Snapshot, bool) {
	if ctx.Err() != nil {
		return Snapshot{}, false
	}
	m.mu.Lock()
	watcher := m.watcher
	roots := m.scanRootsLocked()
	m.mu.Unlock()
	watchGen, covered := watcher.coverage()
	if !covered || !watcher.watchedDirsUnchanged() {
		return Snapshot{}, false
	}
	rootDirs := statRootDirs(roots)
	seq := m.resolveCache.seq.Load()
	caps := m.resolveCaps()
	found, ok := m.resolveCache.lookup(caps, roots, rootDirs, seq, watchGen)
	if !ok || !linksUnchanged(found.links) {
		return Snapshot{}, false
	}

	m.mu.Lock()
	if m.closed || !m.ready || m.watcher != watcher || !slices.Equal(roots, m.scanRootsLocked()) ||
		m.packMCP.freshEpoch() != m.resolveEpoch {
		m.mu.Unlock()
		return Snapshot{}, false
	}
	entry, ok := m.resolveCache.lookup(caps, roots, rootDirs, seq, watchGen)
	if hook := m.resolveCache.afterLockedLookup; hook != nil {
		hook()
	}
	// A change signal after the loads above leaves the old entry in place
	// until a pass replaces it, and an event the watcher scheduled has not
	// signaled yet. A capability change bumps no sequence, so the
	// capabilities are compared again right before the negotiation state
	// is read. Watcher.mu nests inside m.mu: the watcher never takes m.mu.
	genNow, coveredNow := watcher.coverage()
	if !ok || entry.pass != found.pass || m.resolveCache.seq.Load() != seq || m.resolveCaps() != caps ||
		!coveredNow || genNow != watchGen {
		m.mu.Unlock()
		return Snapshot{}, false
	}
	negotiating := m.negotiating.Load()
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

// watchCoverage is the Watcher state the resolve cache reads, guarded by
// Watcher.mu.
type watchCoverage struct {
	// gen counts the watches added and removed. A pass that started
	// before a watch was added can have read a change that fired no
	// event, and a pass that relied on a watch that is gone is no longer
	// covered.
	gen uint64
	// dirs holds each watched directory as it was when its watch was
	// added, so a Sync can replace a watch whose path now names another
	// directory.
	dirs map[string]os.FileInfo
	// addFailed is set when the latest Sync could not add a directory for
	// a reason other than ENOSPC (which degrades the watcher) or the
	// directory being gone (which its parent's watch reports), such as
	// EMFILE from kqueue on darwin. It is also set when that Sync could
	// not read a directory or the metadata it discovers watches from, for
	// the same kind of reason: it then dropped or never added the watches
	// under that directory, and the failure can clear with no event.
	addFailed bool
	// relayDropped counts the overflow errors the fsnotify relay queued
	// that the event loop has not handled. The relay sets it without w.mu.
	relayDropped atomic.Int64
	// relayed counts the events and errors the relay took from fsnotify,
	// for tests.
	relayed atomic.Uint64
	// adds counts the watches Sync tried to add, so revalidateSync knows
	// a Sync tried one. An Add that failed because the directory vanished
	// counts: the directory can come back before the next pass.
	adds uint64
	// afterCollect, when set by a test, runs in collectSyncDirs after the
	// discovery.
	afterCollect func()
}

// syncDirs is what collectSyncDirs found for beginSync.
type syncDirs struct {
	// current holds each watched directory as stated, nil for one that
	// could not be.
	current map[string]os.FileInfo
	// discoveryFailed is set when collectDirs failed to read a path for a
	// reason other than it being missing.
	discoveryFailed bool
}

// collectSyncDirs is collectDirs for Sync, which calls it without w.mu. It
// tracks the read failures of collectDirs as a resolve pass does, and stats
// every watched directory, so beginSync can compare their identities
// without filesystem calls under w.mu.
func (w *Watcher) collectSyncDirs(roots []ScanRoot) (map[string]struct{}, packWatchPaths, syncDirs) {
	tracked := trackSymlinkTargets(roots)
	desired, pack := w.collectDirs(roots)
	_, discoveryFailed := tracked.stopPass()
	w.mu.Lock()
	hook := w.cover.afterCollect
	w.mu.Unlock()
	if hook != nil {
		hook()
	}
	w.mu.Lock()
	paths := make([]string, 0, len(w.cover.dirs))
	for path := range w.cover.dirs {
		paths = append(paths, path)
	}
	w.mu.Unlock()
	current := make(map[string]os.FileInfo, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			info = nil
		}
		current[path] = info
	}
	return desired, pack, syncDirs{current: current, discoveryFailed: discoveryFailed}
}

// beginSync starts a Sync's add loop, with w.mu held, and returns true, the
// initial value of its addedAll. found.current holds the directories that
// collectSyncDirs stated, nil for one it could not. A discovery failure
// leaves the watcher uncovered until a Sync without one. beginSync drops the
// watches whose path named another directory there, such as a retargeted
// symlink, so the add loop watches the directory the path names now. It
// records a new mode for a directory whose permissions changed, which
// fsnotify reports as a chmod event the watcher does not act on. A watch
// added after that stat, by a concurrent Sync, is kept as is. A watch that
// Sync removed or that beginSync drops, and a recorded mode change, advance
// the watch generation.
func (w *Watcher) beginSync(found syncDirs) bool {
	w.cover.addFailed = found.discoveryFailed
	current := found.current
	changed := false
	for path, was := range w.cover.dirs {
		if _, ok := w.watched[path]; !ok {
			delete(w.cover.dirs, path)
			changed = true
			continue
		}
		now, stated := current[path]
		if !stated {
			continue
		}
		if now == nil || was == nil || !os.SameFile(was, now) {
			_ = w.watcher.Remove(path)
			delete(w.watched, path)
			delete(w.cover.dirs, path)
			changed = true
			continue
		}
		if now.Mode() != was.Mode() {
			w.cover.dirs[path] = now
			changed = true
		}
	}
	if changed {
		w.cover.gen++
	}
	return true
}

// addWatch adds one directory watch for Sync, with w.mu held.
func (w *Watcher) addWatch(path string) error {
	w.cover.adds++
	before, beforeErr := os.Stat(path)
	err := w.watcher.Add(path)
	switch {
	case err == nil:
		w.cover.gen++
		// A path replaced between the stats and the Add would record
		// the identity of a directory the watch is not on. With no
		// identity, the next Sync replaces the watch.
		info, statErr := os.Stat(path)
		if statErr != nil || beforeErr != nil || !sameDir(before, info) {
			info = nil
		}
		if w.cover.dirs == nil {
			w.cover.dirs = make(map[string]os.FileInfo)
		}
		w.cover.dirs[path] = info
	case !errors.Is(err, syscall.ENOSPC) && !errors.Is(err, os.ErrNotExist):
		w.cover.addFailed = true
	}
	return err
}

// watchAdds returns the number of watches Sync tried to add so far.
func (w *Watcher) watchAdds() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cover.adds
}

// revalidateSync runs when Sync returns, after it released w.mu. adds is
// watchAdds when Sync started. When Sync tried to add a watch,
// revalidateSync discovers the directories again: one created between the
// first discovery and the watch on its parent fired no event, and is not
// watched, and pack metadata that changed in between can leave the event
// filter (w.pack) behind. It then installs the new filter, leaves the
// watcher uncovered and schedules a change, so the next Sync watches the
// directory and the next pass reads it. Anything created after this second
// discovery fires an event on a watch that is in place.
func (w *Watcher) revalidateSync(roots []ScanRoot, adds uint64) {
	w.mu.Lock()
	skip := w.closed || w.watcher == nil || w.cover.adds == adds || w.degraded != "" || w.cover.addFailed
	w.mu.Unlock()
	if skip {
		return
	}
	tracked := trackSymlinkTargets(roots)
	desired, pack := w.collectDirs(roots)
	_, missed := tracked.stopPass()
	w.mu.Lock()
	if !pack.equal(w.pack) {
		w.pack = pack
		missed = true
	}
	for path := range desired {
		if _, ok := w.watched[path]; !ok {
			missed = true
			break
		}
	}
	if missed {
		w.cover.addFailed = true
	}
	w.mu.Unlock()
	if missed {
		w.schedule()
	}
}

// addSkillParentWatchDirs watches the existing parent, such as .codex, of
// each skill container under rootPath, so creating a missing container
// fires an event. collectDirs watches only the containers that exist.
func addSkillParentWatchDirs(rootPath string, out map[string]struct{}) {
	for _, rel := range skillContainerRelPaths {
		parent := filepath.Dir(filepath.Join(rootPath, rel))
		if parent == rootPath {
			continue
		}
		if info, err := statNoted(parent); err == nil && info.IsDir() {
			out[parent] = struct{}{}
		}
	}
}

// forgetRemovedWatch drops a watched directory that was removed or renamed.
// fsnotify drops its watch then, so the directory must leave w.watched for
// the next Sync to add a watch on whatever takes its place. The event
// itself is relevant and schedules that re-resolve and Sync. fsnotify
// already dropped the watch, so this calls no Remove.
func (w *Watcher) forgetRemovedWatch(ev fsnotify.Event) {
	if !ev.Has(fsnotify.Remove) && !ev.Has(fsnotify.Rename) {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.watched[ev.Name]; !ok || w.closed {
		return
	}
	delete(w.watched, ev.Name)
	delete(w.cover.dirs, ev.Name)
	w.cover.gen++
}

// linksUnchanged reports whether every symlink still resolves to the
// target it resolved to before.
func linksUnchanged(links map[string]string) bool {
	for link, target := range links {
		if now, err := filepath.EvalSymlinks(link); err != nil || now != target {
			return false
		}
	}
	return true
}

// watchedDirsUnchanged reports whether every watched path still names the
// directory it named when its watch was added, with the same mode: a
// permission change on a directory fires only a chmod event, which the
// watcher does not act on.
func (w *Watcher) watchedDirsUnchanged() bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	dirs := make(map[string]os.FileInfo, len(w.cover.dirs))
	for path := range w.watched {
		dirs[path] = w.cover.dirs[path]
	}
	w.mu.Unlock()
	for path, was := range dirs {
		now, err := os.Stat(path)
		if err != nil || !sameDir(was, now) {
			return false
		}
	}
	return true
}

// sameDir reports whether was and now are the same directory with the same
// mode.
func sameDir(was, now os.FileInfo) bool {
	return was != nil && now != nil && os.SameFile(was, now) && was.Mode() == now.Mode()
}

// statRootDirs returns each scan root's directory, following symlinks, or
// nil for a root that is missing.
func statRootDirs(roots []ScanRoot) []os.FileInfo {
	out := make([]os.FileInfo, len(roots))
	for i, root := range roots {
		if info, err := os.Stat(root.Path); err == nil {
			out[i] = info
		}
	}
	return out
}

// sameDirs reports whether a and b name the same directories with the same
// modes, nil standing for a missing one.
func sameDirs(a, b []os.FileInfo) bool {
	return slices.EqualFunc(a, b, func(x, y os.FileInfo) bool {
		if x == nil || y == nil {
			return x == nil && y == nil
		}
		return sameDir(x, y)
	})
}

// coverage reports the watch generation and whether the watcher is running
// with every watch in place and no debounced event pending, so every change
// to a resolved file has reached the Manager as a signal.
func (w *Watcher) coverage() (gen uint64, ok bool) {
	if w == nil {
		return 0, false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cover.gen, !w.closed && w.watcher != nil && w.degraded == "" && w.timer == nil && !w.cover.addFailed && w.cover.relayDropped.Load() == 0
}

// reportsWritesTo reports whether the watcher reports a write to the
// target of each symlink in links: the target's directory has a watch,
// compared by canonical path because targets come from
// filepath.EvalSymlinks and a watched path may pass through a symlinked
// directory, and the watcher does not filter the write out by its name.
// The empty string stands for a target that is unknown and never reported.
func (w *Watcher) reportsWritesTo(links map[string]string) bool {
	if len(links) == 0 {
		return true
	}
	if w == nil {
		return false
	}
	w.mu.Lock()
	watched := make([]string, 0, len(w.watched))
	for dir := range w.watched {
		watched = append(watched, dir)
	}
	w.mu.Unlock()

	var canonical map[string][]string
	for _, target := range links {
		if target == "" {
			return false
		}
		dir, base := filepath.Dir(target), filepath.Base(target)
		var aliases []string
		if slices.Contains(watched, dir) {
			aliases = append(aliases, dir)
		} else {
			if canonical == nil {
				canonical = make(map[string][]string, len(watched))
				for _, d := range watched {
					if resolved, err := filepath.EvalSymlinks(d); err == nil && resolved != d {
						canonical[resolved] = append(canonical[resolved], d)
					}
				}
			}
			aliases = canonical[dir]
		}
		// fsnotify names an event after the watched path it came through.
		if !slices.ContainsFunc(aliases, func(alias string) bool {
			return w.eventRelevant(fsnotify.Event{Name: filepath.Join(alias, base), Op: fsnotify.Write})
		}) {
			return false
		}
	}
	return true
}

// symlinkTargets collects the symlinks, and their targets, that the running
// resolve passes read through. A link goes to the passes whose scan roots
// hold its owner: the link itself, or the scan root a Claude Code file was
// read for. A link that no running pass owns goes to every running pass, so
// a concurrent pass can only make another pass more cautious; every read
// during a pass or a Sync has an owner, so this is a fallback.
var symlinkTargets struct {
	mu     sync.Mutex
	active map[*symlinkTargetSet]struct{}
}

type symlinkTargetSet struct {
	roots []string
	// links maps each link to its target.
	links map[string]string
	// readFailed is set when the pass failed to read a directory or a file
	// for a reason other than it being missing.
	readFailed bool
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

func (s *symlinkTargetSet) stop() map[string]string {
	links, _ := s.stopPass()
	return links
}

// stopPass is stop that also reports whether the pass failed a read.
func (s *symlinkTargetSet) stopPass() (map[string]string, bool) {
	symlinkTargets.mu.Lock()
	defer symlinkTargets.mu.Unlock()
	delete(symlinkTargets.active, s)
	return s.links, s.readFailed
}

// readDirNoted is os.ReadDir for a resolve pass. The resolver skips a
// directory it cannot read, and a failure such as EMFILE or an I/O error
// can clear with no event the watcher sees, so the failure marks the pass.
func readDirNoted(dir string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(dir)
	if err = noted(dir, err); err != nil {
		return nil, err
	}
	return entries, nil
}

// statNoted, lstatNoted, entryInfoNoted and evalSymlinksNoted are the
// metadata calls of a resolve pass, noted as readDirNoted is. The resolver
// skips a path whose metadata it cannot read, the same way.
func statNoted(path string) (fs.FileInfo, error) {
	info, err := os.Stat(path)
	if err = noted(path, err); err != nil {
		return nil, err
	}
	return info, nil
}

func lstatNoted(path string) (fs.FileInfo, error) {
	info, err := os.Lstat(path)
	if err = noted(path, err); err != nil {
		return nil, err
	}
	return info, nil
}

func entryInfoNoted(dir string, e fs.DirEntry) (fs.FileInfo, error) {
	info, err := e.Info()
	if err = noted(filepath.Join(dir, e.Name()), err); err != nil {
		return nil, err
	}
	return info, nil
}

func evalSymlinksNoted(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err = noted(path, err); err != nil {
		return "", err
	}
	return resolved, nil
}

// noted returns err, or the fault a test injected for path, and marks the
// running passes when the result is a failure.
func noted(path string, err error) error {
	if fault, ok := readFaults.Load(filepath.Clean(path)); ok {
		if ferr, ok := fault.(error); ok {
			err = ferr
		}
	}
	if err != nil {
		noteReadFailure(path, err)
	}
	return err
}

// readFaults maps a path to the error the noted calls return for it, so
// tests can fail a read in a way chmod cannot: a directory without read
// permission cannot be watched either. It is empty in production.
var readFaults sync.Map

// noteReadFailure marks the running passes that own path as unable to be
// cached, unless err says path is missing or not a directory: creating it
// fires an event in its parent. A path that no running pass owns marks
// every running pass, as noteOwnedSymlinkTarget does.
func noteReadFailure(path string, err error) {
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return
	}
	path = filepath.Clean(path)
	symlinkTargets.mu.Lock()
	defer symlinkTargets.mu.Unlock()
	owned := false
	for set := range symlinkTargets.active {
		if set.holds(path) {
			set.readFailed = true
			owned = true
		}
	}
	if owned {
		return
	}
	for set := range symlinkTargets.active {
		set.readFailed = true
	}
}

// trackUncachedResolve tracks the symlinks of a resolve that is not a
// cached pass, which tracks its own, until stopTracking. The links under its
// scan roots then stay with it instead of going to every running pass, which
// could not cache its result then.
func (r *Resolver) trackUncachedResolve(roots []ScanRoot) *symlinkTargetSet {
	if r.packPass != nil {
		return nil
	}
	return trackSymlinkTargets(roots)
}

func stopTracking(set *symlinkTargetSet) {
	if set != nil {
		set.stop()
	}
}

func (s *symlinkTargetSet) holds(link string) bool {
	return slices.ContainsFunc(s.roots, func(root string) bool { return pathHasPrefix(link, root) })
}

func (s *symlinkTargetSet) add(link, target string) {
	if s.links == nil {
		s.links = make(map[string]string)
	}
	if old, ok := s.links[link]; ok && old != target {
		// The link changed during the pass.
		target = ""
	}
	s.links[link] = target
}

// noteSymlinkTarget records the target of the symlink link that a resolve
// pass follows, as filepath.EvalSymlinks returned it. A target that did not
// resolve is recorded as unknown: creating it later fires no event either.
// So is the target of a chain of links, because retargeting a link in the
// middle of the chain fires an event only where that link lives.
func noteSymlinkTarget(link, target string, err error) {
	noteOwnedSymlinkTarget(link, link, target, err)
}

// noteOwnedSymlinkTarget is noteSymlinkTarget for a link that a pass read
// because of owner, a path under its scan roots, such as the user settings
// file that a scan root in the Claude Code plugin cache reads.
func noteOwnedSymlinkTarget(owner, link, target string, err error) {
	link = filepath.Clean(link)
	owner = filepath.Clean(owner)
	if err != nil || isSymlinkChain(link) {
		target = ""
	}
	symlinkTargets.mu.Lock()
	defer symlinkTargets.mu.Unlock()
	owned := false
	for set := range symlinkTargets.active {
		if set.holds(link) || set.holds(owner) {
			set.add(link, target)
			owned = true
		}
	}
	if owned {
		return
	}
	for set := range symlinkTargets.active {
		set.add(link, target)
	}
}

// isSymlinkChain reports whether the symlink link points at another symlink,
// or cannot be read.
func isSymlinkChain(link string) bool {
	next, err := os.Readlink(link)
	if err != nil {
		return true
	}
	if !filepath.IsAbs(next) {
		next = filepath.Join(filepath.Dir(link), next)
	}
	info, err := os.Lstat(next)
	return err != nil || info.Mode()&os.ModeSymlink != 0
}

// scheduleOnOverflow re-resolves after the kernel or the relay dropped
// events: any of them could have been a change to a resolved file.
func (w *Watcher) scheduleOnOverflow(err error) {
	if !errors.Is(err, fsnotify.ErrEventOverflow) {
		return
	}
	w.dropWatchesAfterOverflow()
	w.schedule()
	if errors.Is(err, errRelayOverflow) {
		// The debounce timer now keeps the watcher uncovered.
		w.cover.relayDropped.Add(-1)
	}
}

// dropWatchesAfterOverflow removes every watch after dropped events. A
// dropped event can be the removal of a watched directory, whose watch the
// kernel then dropped, and a directory created in its place can reuse the
// inode, so beginSync would keep the dead watch. The Sync after the
// re-resolve adds every watch again.
func (w *Watcher) dropWatchesAfterOverflow() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.watcher == nil || len(w.watched) == 0 {
		return
	}
	for path := range w.watched {
		_ = w.watcher.Remove(path)
		delete(w.watched, path)
		delete(w.cover.dirs, path)
	}
	w.cover.gen++
}

// noteScheduled advances the watch generation when an event arms the
// debounce timer, with w.mu held. A pass or a cached result that predates
// the event then cannot be served once the timer fired, even before the
// Manager took the change signal.
func (w *Watcher) noteScheduled() {
	w.cover.gen++
}

// relayQueueMax bounds the events and errors the relay holds for the event
// loop. Past it the relay drops what fsnotify sends and queues one
// overflow error in their place, as the kernel does when its queue fills.
const relayQueueMax = 4096

// errRelayOverflow is the overflow error the relay queues when it drops.
var errRelayOverflow = xerrors.Errorf("agentcontext watcher relay: %w", fsnotify.ErrEventOverflow)

type relayItem struct {
	ev    fsnotify.Event
	err   error
	isErr bool
	// overflow marks the errRelayOverflow item the relay queued itself.
	overflow bool
}

// relayQueue is the bounded queue of the relay.
type relayQueue struct {
	items []relayItem
	max   int
	// dropping is set from a drop until the event loop takes the overflow
	// error queued for it.
	dropping bool
}

// push queues it and reports false, or drops it and reports true. The
// first drop queues errRelayOverflow, so the queue holds at most max+1.
func (q *relayQueue) push(it relayItem) (dropped bool) {
	if q.dropping {
		return true
	}
	if len(q.items) >= q.max {
		q.dropping = true
		q.items = append(q.items, relayItem{err: errRelayOverflow, isErr: true, overflow: true})
		return true
	}
	q.items = append(q.items, it)
	return false
}

// pop removes the head of the queue after the event loop took it.
func (q *relayQueue) pop() {
	if q.items[0].overflow {
		q.dropping = false
	}
	q.items[0] = relayItem{}
	q.items = q.items[1:]
	if len(q.items) == 0 {
		q.items = nil
	}
}

// relayFsnotify returns channels that carry the events and errors of fsw, in
// the order fsnotify sent them, through a queue that takes every send at
// once. fsnotify can wait on a send while it holds the lock that Add and
// Remove take: inotify sends an error with its lock held when it drops a
// moved watch, and Windows serves Add and Remove on the goroutine that
// sends. The event loop takes w.mu, and Sync calls Add and Remove with w.mu
// held, so a send that waited on the event loop could block Sync and the
// event loop for good. The queue grows only while the event loop waits for
// w.mu, and is bounded: past relayQueueMax it drops sends and queues one
// overflow error, which re-resolves. Until the event loop takes that error
// the watcher reports no coverage, so no cached result is served while a
// dropped event may be unaccounted for.
func (w *Watcher) relayFsnotify(fsw *fsnotify.Watcher) (<-chan fsnotify.Event, <-chan error) {
	events := make(chan fsnotify.Event)
	errs := make(chan error)
	go func() {
		defer close(events)
		defer close(errs)
		inEvents, inErrs := fsw.Events, fsw.Errors
		q := relayQueue{max: relayQueueMax}
		closed := w.closedCh
		draining := false
		push := func(it relayItem) {
			w.cover.relayed.Add(1)
			if draining {
				return
			}
			wasDropping := q.dropping
			if q.push(it) && !wasDropping {
				w.cover.relayDropped.Add(1)
			}
		}
		for inEvents != nil || inErrs != nil || len(q.items) > 0 {
			var outEvents chan<- fsnotify.Event
			var outErrs chan<- error
			var next relayItem
			if len(q.items) > 0 {
				next = q.items[0]
				if next.isErr {
					outErrs = errs
				} else {
					outEvents = events
				}
			}
			select {
			case <-closed:
				// Keep taking what fsnotify sends until it closes its
				// channels: Close closes fsnotify after this, and
				// fsnotify can wait on a send with the lock its Close
				// takes.
				closed = nil
				draining = true
				q = relayQueue{}
				continue
			case ev, ok := <-inEvents:
				if !ok {
					inEvents = nil
					continue
				}
				push(relayItem{ev: ev})
			case err, ok := <-inErrs:
				if !ok {
					inErrs = nil
					continue
				}
				push(relayItem{err: err, isErr: true})
			case outEvents <- next.ev:
				q.pop()
			case outErrs <- next.err:
				q.pop()
			}
		}
	}()
	return events, errs
}
