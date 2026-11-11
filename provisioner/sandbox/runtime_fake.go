// This file is fork code: the fake runtime the sandbox provisioner tests run
// against. This workspace has no containerd, so the fake is the only runtime
// under test.
package sandbox

import (
	"context"
	"slices"
	"sync"
	"time"

	"golang.org/x/xerrors"
)

// FakeRuntime is an in-memory Runtime. It records the call order so tests can
// assert create-before-start, and it can fail specific calls.
type FakeRuntime struct {
	mu         sync.Mutex
	containers map[string]Container
	order      []string
	failures   map[string]error
}

// NewFakeRuntime returns an empty fake.
func NewFakeRuntime() *FakeRuntime {
	return &FakeRuntime{
		containers: make(map[string]Container),
		failures:   make(map[string]error),
	}
}

// Fail makes the next call of the named method return err. The method name is
// one of Create, Start, Stop, Remove, Status, Exec.
func (f *FakeRuntime) Fail(method string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures[method] = err
}

// Calls returns the recorded call order, one method name per call.
func (f *FakeRuntime) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.order)
}

// Containers returns a copy of the container map.
func (f *FakeRuntime) Containers() map[string]Container {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]Container, len(f.containers))
	for id, c := range f.containers {
		out[id] = c
	}
	return out
}

func (f *FakeRuntime) record(method string) error {
	f.order = append(f.order, method)
	err, ok := f.failures[method]
	if ok {
		delete(f.failures, method)
	}
	return err
}

// Create makes the container in the created state.
func (f *FakeRuntime) Create(_ context.Context, req CreateRequest) (Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("Create"); err != nil {
		return Container{}, err
	}
	if _, ok := f.containers[req.ID]; ok {
		return Container{}, xerrors.Errorf("%w: %s", ErrExists, req.ID)
	}
	c := Container{
		ID:        req.ID,
		Image:     req.Image,
		State:     ContainerStateCreated,
		CreatedAt: time.Now(),
	}
	f.containers[req.ID] = c
	return c, nil
}

// Start runs the container process.
func (f *FakeRuntime) Start(_ context.Context, containerID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("Start"); err != nil {
		return err
	}
	c, ok := f.containers[containerID]
	if !ok {
		return xerrors.Errorf("%w: %s", ErrNotFound, containerID)
	}
	if c.State != ContainerStateRunning {
		c.State = ContainerStateRunning
		c.StartedAt = time.Now()
		f.containers[containerID] = c
	}
	return nil
}

// Stop stops the container process.
func (f *FakeRuntime) Stop(_ context.Context, containerID string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("Stop"); err != nil {
		return err
	}
	c, ok := f.containers[containerID]
	if !ok {
		return xerrors.Errorf("%w: %s", ErrNotFound, containerID)
	}
	if c.State == ContainerStateRunning {
		c.State = ContainerStateStopped
		c.StoppedAt = time.Now()
		f.containers[containerID] = c
	}
	return nil
}

// Remove deletes the container.
func (f *FakeRuntime) Remove(_ context.Context, containerID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("Remove"); err != nil {
		return err
	}
	if _, ok := f.containers[containerID]; !ok {
		return xerrors.Errorf("%w: %s", ErrNotFound, containerID)
	}
	delete(f.containers, containerID)
	return nil
}

// Status reads one container.
func (f *FakeRuntime) Status(_ context.Context, containerID string) (Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("Status"); err != nil {
		return Container{}, err
	}
	c, ok := f.containers[containerID]
	if !ok {
		return Container{}, xerrors.Errorf("%w: %s", ErrNotFound, containerID)
	}
	return c, nil
}

// Exec runs a command in a running container and returns its output.
func (f *FakeRuntime) Exec(_ context.Context, req ExecRequest) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("Exec"); err != nil {
		return nil, err
	}
	c, ok := f.containers[req.ContainerID]
	if !ok {
		return nil, xerrors.Errorf("%w: %s", ErrNotFound, req.ContainerID)
	}
	if c.State != ContainerStateRunning {
		return nil, xerrors.Errorf("%w: %s", ErrNotRunning, req.ContainerID)
	}
	return []byte("fake exec " + req.ContainerID), nil
}
