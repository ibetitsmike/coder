package agentcontext

// This file is fork code: it lets a push connection reuse the snapshot of an
// earlier resolve when nothing it depends on changed since, so connecting,
// disconnecting and reconnecting do not walk the filesystem again. A resolve
// depends on the scan roots, the advertised capabilities and the files under
// the roots; every change to those files reaches the Manager through the
// watcher's signal, which bumps the change sequence and so invalidates every
// cached result. While the watcher cannot report every change (degraded,
// closed, or not started) nothing is cached.

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
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
	snap := r.ResolveContext(ctx, roots)
	resolveMemo.sweep(start)

	if !watcher.reportsAllChanges() {
		m.resolveCache.reset()
		return snap, pass
	}
	if ctx.Err() != nil || m.resolveCaps() != caps || m.resolveCache.seq.Load() != seq {
		return snap, pass
	}
	m.resolveCache.store(resolveCacheEntry{caps: caps, roots: slices.Clone(roots), seq: seq, snap: snap, pass: pass})
	return snap, pass
}

// resyncCached is Resync for the push loop: it republishes a cached result
// when nothing changed since it was resolved, and resolves otherwise. A hit
// skips Resync's MCP reconnect, which a new coderd connection does not call
// for: the MCP servers run in the workspace, and a change to their catalog
// signals the Manager on its own.
func (m *Manager) resyncCached(ctx context.Context) (Snapshot, error) {
	if snap, ok := m.publishCached(ctx); ok {
		return snap, nil
	}
	return m.Resync(ctx)
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

// scheduleOnOverflow re-resolves after the kernel dropped events: any of
// them could have been a change to a resolved file.
func (w *Watcher) scheduleOnOverflow(err error) {
	if errors.Is(err, fsnotify.ErrEventOverflow) {
		w.schedule()
	}
}
