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
// The tradeoff: a change the watcher misses for a reason not listed above
// used to show up on the next reconnect, which resolved again. With the
// cache it shows up on the next watcher event instead.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
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

	// afterLockedLookup, when set by a test, runs in publishCached between
	// its locked lookup and its sequence check.
	afterLockedLookup func()
}

func (c *resolveCache) changed() {
	c.seq.Add(1)
}

func (c *resolveCache) store(e resolveCacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if slices.ContainsFunc(c.entries, func(old resolveCacheEntry) bool {
		return old.seq > e.seq || old.watchGen > e.watchGen
	}) {
		// A pass that started after a later change already stored.
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
// the pass. The returned pack pass feeds the pack MCP publisher.
func (m *Manager) resolvePass(ctx context.Context, resolver *Resolver, roots []ScanRoot, watcher *Watcher) (Snapshot, *packPass) {
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
	links := followed.stop()
	resolveMemo.sweep(start)

	if ctx.Err() != nil {
		// A canceled pass is partial and proves nothing about the files.
		return snap, pass
	}
	genAfter, covered := watcher.coverage()
	if !covered || mixed || genAfter != watchGen || m.resolveCache.seq.Load() != seq || !watcher.reportsWritesTo(links) {
		// This pass read the files as they are now and cannot be cached.
		// An older entry with the same key can predate a change whose
		// event has not arrived yet, so it goes too.
		m.resolveCache.reset()
		return snap, pass
	}
	m.resolveCache.store(resolveCacheEntry{caps: caps, roots: slices.Clone(roots), rootDirs: rootDirs, seq: seq, watchGen: watchGen, snap: snap, pass: pass, links: links})
	return snap, pass
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
	// until a pass replaces it. A capability change bumps no sequence, so
	// the capabilities are compared again right before the negotiation
	// state is read.
	if !ok || entry.pass != found.pass || m.resolveCache.seq.Load() != seq || m.resolveCaps() != caps {
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
	// EMFILE from kqueue on darwin.
	addFailed bool
}

// beginSync starts a Sync's add loop, with w.mu held, and returns true, the
// initial value of its addedAll. It drops the watches whose path now names
// another directory, such as a retargeted symlink, so the add loop watches
// the directory the path names now. A watch that Sync removed, or that
// beginSync drops, advances the watch generation.
func (w *Watcher) beginSync() bool {
	w.cover.addFailed = false
	removed := false
	for path, was := range w.cover.dirs {
		if _, ok := w.watched[path]; !ok {
			delete(w.cover.dirs, path)
			removed = true
			continue
		}
		if now, err := os.Stat(path); err != nil || was == nil || !os.SameFile(was, now) {
			_ = w.watcher.Remove(path)
			delete(w.watched, path)
			delete(w.cover.dirs, path)
			removed = true
		}
	}
	if removed {
		w.cover.gen++
	}
	return true
}

// addWatch adds one directory watch for Sync, with w.mu held.
func (w *Watcher) addWatch(path string) error {
	err := w.watcher.Add(path)
	switch {
	case err == nil:
		w.cover.gen++
		info, statErr := os.Stat(path)
		if statErr != nil {
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
// directory it named when its watch was added.
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
		if err != nil || was == nil || !os.SameFile(was, now) {
			return false
		}
	}
	return true
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

// sameDirs reports whether a and b name the same directories, nil standing
// for a missing one.
func sameDirs(a, b []os.FileInfo) bool {
	return slices.EqualFunc(a, b, func(x, y os.FileInfo) bool {
		if x == nil || y == nil {
			return x == nil && y == nil
		}
		return os.SameFile(x, y)
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
	return w.cover.gen, !w.closed && w.watcher != nil && w.degraded == "" && w.timer == nil && !w.cover.addFailed
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
	symlinkTargets.mu.Lock()
	defer symlinkTargets.mu.Unlock()
	delete(symlinkTargets.active, s)
	return s.links
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

// scheduleOnOverflow re-resolves after the kernel dropped events: any of
// them could have been a change to a resolved file.
func (w *Watcher) scheduleOnOverflow(err error) {
	if errors.Is(err, fsnotify.ErrEventOverflow) {
		w.schedule()
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
// w.mu.
func (w *Watcher) relayFsnotify(fsw *fsnotify.Watcher) (<-chan fsnotify.Event, <-chan error) {
	type item struct {
		ev    fsnotify.Event
		err   error
		isErr bool
	}
	events := make(chan fsnotify.Event)
	errs := make(chan error)
	go func() {
		defer close(events)
		defer close(errs)
		inEvents, inErrs := fsw.Events, fsw.Errors
		var queue []item
		for inEvents != nil || inErrs != nil || len(queue) > 0 {
			var outEvents chan<- fsnotify.Event
			var outErrs chan<- error
			var next item
			if len(queue) > 0 {
				next = queue[0]
				if next.isErr {
					outErrs = errs
				} else {
					outEvents = events
				}
			}
			select {
			case <-w.closedCh:
				return
			case ev, ok := <-inEvents:
				if !ok {
					inEvents = nil
					continue
				}
				queue = append(queue, item{ev: ev})
			case err, ok := <-inErrs:
				if !ok {
					inErrs = nil
					continue
				}
				queue = append(queue, item{err: err, isErr: true})
			case outEvents <- next.ev:
				queue = queue[1:]
			case outErrs <- next.err:
				queue = queue[1:]
			}
		}
	}()
	return events, errs
}
