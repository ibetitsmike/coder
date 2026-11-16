package agentcontext

import (
	"path/filepath"
	"sync/atomic"
	"testing"
)

// ManagerStarted exposes the unexported started() channel for
// use by external _test packages. Production code does not need
// this signal; the agent calls Run synchronously after wiring
// the Manager. Tests use it to coordinate without polling.
func ManagerStarted(m *Manager) <-chan struct{} { return m.started() }

// ResolveCount returns how many resolver passes the Manager ran.
func ResolveCount(m *Manager) uint64 { return m.resolveCache.resolves.Load() }

// ManagerWatching reports whether Run installed a watcher that reports
// every change, so resolve results are cached.
func ManagerWatching(m *Manager) bool {
	m.mu.Lock()
	w := m.watcher
	m.mu.Unlock()
	return w.reportsAllChanges()
}

// ResetResolveMemo empties the file memo, so the next pass reads every body.
func ResetResolveMemo() {
	resolveMemo.mu.Lock()
	defer resolveMemo.mu.Unlock()
	resolveMemo.entries = make(map[string]*memoEntry)
	resolveMemo.bytes = 0
}

// CountBodyReads counts the file bodies resolve passes read under dir until
// the test ends. The returned function reads the count.
func CountBodyReads(t testing.TB, dir string) func() int64 {
	t.Helper()
	count := &atomic.Int64{}
	addBodyReadObserver(bodyReadObserver{prefix: filepath.Clean(dir), count: count})
	if resolved, err := filepath.EvalSymlinks(dir); err == nil && resolved != filepath.Clean(dir) {
		addBodyReadObserver(bodyReadObserver{prefix: resolved, count: count})
	}
	t.Cleanup(func() { removeBodyReadObserver(count) })
	return count.Load
}
