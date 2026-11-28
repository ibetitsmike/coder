package dockerruntime

import (
	"context"
	"sync"
)

// locks holds one lock per container ID. A lock lives while a call holds it
// or waits for it, so the map does not grow with every workspace.
type locks struct {
	mu   sync.Mutex
	held map[string]*lock
}

type lock struct {
	ch   chan struct{}
	refs int
}

// acquire waits for the lock of id or for ctx to end. The caller must call
// the returned release once.
func (l *locks) acquire(ctx context.Context, id string) (func(), error) {
	l.mu.Lock()
	k, ok := l.held[id]
	if !ok {
		k = &lock{ch: make(chan struct{}, 1)}
		l.held[id] = k
	}
	k.refs++
	l.mu.Unlock()

	select {
	case k.ch <- struct{}{}:
		return func() {
			<-k.ch
			l.put(id, k)
		}, nil
	case <-ctx.Done():
		l.put(id, k)
		return nil, ctx.Err()
	}
}

func (l *locks) put(id string, k *lock) {
	l.mu.Lock()
	defer l.mu.Unlock()
	k.refs--
	if k.refs == 0 {
		delete(l.held, id)
	}
}
