package agentcontext

import (
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// HoldResolveMemo turns the file memo on until the test ends, as a Manager
// with the context cache on does.
func HoldResolveMemo(tb testing.TB) {
	tb.Helper()
	resolveMemo.acquire()
	tb.Cleanup(resolveMemo.release)
}

// Fork test seam: fork_resolve_cache_test.go is an external _test package, so
// it cannot reach the unexported counters and memo state it asserts on. These
// helpers existed in the upstream export_test.go that the sync deleted; they
// survive as fork-owned code for the fork's tests only.

// ManagerWatching reports whether Run installed a watcher that reports
// every change, so resolve results are cached. A watcher that degraded
// (for example on inotify ENOSPC) counts as not watching.
func ManagerWatching(m *Manager) bool {
	m.mu.Lock()
	w := m.watcher
	m.mu.Unlock()
	return w != nil && w.Degraded() == ""
}

// ResetResolveMemo empties the file memo, so the next pass reads every body.// ResetResolveMemo empties the file memo, so the next pass reads every body.
func ResetResolveMemo() {
	resolveMemo.mu.Lock()
	defer resolveMemo.mu.Unlock()
	resolveMemo.entries = make(map[string]*memoEntry)
	resolveMemo.bytes = 0
}

// ResolveCount returns how many resolver passes the Manager ran.// ResolveCount returns how many resolver passes the Manager ran.
func ResolveCount(m *Manager) uint64 { return m.resolveCache.resolves.Load() }

// CountBodyReads counts the file bodies resolve passes read under dir until
// the test ends. The returned function reads the count. It holds the file
// memo on until then, as a Manager with the context cache on does.
func CountBodyReads(t testing.TB, dir string) func() int64 {
	t.Helper()
	if info, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	} else if _, ok := fileIdentity(info); !ok {
		t.Skip("no file identity on this platform, so the memo is off")
	}
	HoldResolveMemo(t)
	count := &atomic.Int64{}
	addBodyReadObserver(bodyReadObserver{prefix: filepath.Clean(dir), count: count})
	if resolved, err := filepath.EvalSymlinks(dir); err == nil && resolved != filepath.Clean(dir) {
		addBodyReadObserver(bodyReadObserver{prefix: resolved, count: count})
	}
	t.Cleanup(func() { removeBodyReadObserver(count) })
	return count.Load
}

// ContextCacheOn reports whether the Manager's resolve cache is on.
func ContextCacheOn(m *Manager) bool {
	on, _ := m.resolveCache.state()
	return on
}

// EnableContextCache turns the Manager's resolve cache on, as a server with// CachedResults returns how many resolve results the Manager holds.
func CachedResults(m *Manager) int {
	m.resolveCache.mu.Lock()
	defer m.resolveCache.mu.Unlock()
	return len(m.resolveCache.entries)
}

// EnableContextCache turns the Manager's resolve cache on, as a server with
// the fork-context-cache experiment on did on an earlier connection.
func EnableContextCache(m *Manager) { m.resolveCache.setEnabled(true) }

// FileChangeTime returns the ctime of a file as the memo sees it, or false
// where the memo is off.// FileChangeTime returns the ctime of a file as the memo sees it, or false
// where the memo is off.
func FileChangeTime(info fs.FileInfo) (int64, bool) {
	id, ok := fileIdentity(info)
	return id.ctime, ok
}
