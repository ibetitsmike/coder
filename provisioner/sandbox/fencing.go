// This file is fork code: the in-memory generation fence of the sandbox
// provisioner. The durable fence lives in the chatd.sandbox_operations
// table; this primitive lets the engine reject a superseded worker without
// a database round trip.
package sandbox

import "sync"

// Generation is the monotonic fence of one workspace's operations. Every
// build of a workspace carries a generation, and a new build supersedes
// every older one.
type Generation struct {
	mu      sync.Mutex
	current int64
}

// NewGeneration returns a fence at initial.
func NewGeneration(initial int64) *Generation {
	return &Generation{current: initial}
}

// Current returns the fenced generation.
func (g *Generation) Current() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.current
}

// Advance raises the fence to gen when gen is newer than the current
// generation. It reports whether the fence moved.
func (g *Generation) Advance(gen int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if gen <= g.current {
		return false
	}
	g.current = gen
	return true
}

// Superseded reports whether gen is older than the fenced generation, so a
// worker carrying it must stop.
func (g *Generation) Superseded(gen int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return gen < g.current
}
