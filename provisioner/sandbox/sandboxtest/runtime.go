// Package sandboxtest holds test helpers for the fork sandbox provisioner.
// Nothing in it is a production runtime.
package sandboxtest

import (
	"context"
	"maps"
	"slices"
	"sync"
	"time"

	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/provisioner/sandbox"
)

// FakeRuntime is an in-memory sandbox.Runtime for tests. It records the call
// order so tests can assert create-before-start, and it can fail specific
// calls.
type FakeRuntime struct {
	mu         sync.Mutex
	containers map[string]sandbox.Container
	creates    []sandbox.CreateRequest
	order      []string
	failures   map[string]error
}

var _ sandbox.Runtime = (*FakeRuntime)(nil)

// NewFakeRuntime returns an empty fake.
func NewFakeRuntime() *FakeRuntime {
	return &FakeRuntime{
		containers: make(map[string]sandbox.Container),
		failures:   make(map[string]error),
	}
}

// Fail makes the next call of the named method return err. The method name is
// one of Create, Start, Stop, Remove, Status.
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
func (f *FakeRuntime) Containers() map[string]sandbox.Container {
	f.mu.Lock()
	defer f.mu.Unlock()
	return maps.Clone(f.containers)
}

// Creates returns the requests of the creates that succeeded, in order.
func (f *FakeRuntime) Creates() []sandbox.CreateRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.creates)
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
func (f *FakeRuntime) Create(_ context.Context, req sandbox.CreateRequest) (sandbox.Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("Create"); err != nil {
		return sandbox.Container{}, err
	}
	if _, ok := f.containers[req.ID]; ok {
		return sandbox.Container{}, xerrors.Errorf("%w: %s", sandbox.ErrExists, req.ID)
	}
	c := sandbox.Container{
		ID:        req.ID,
		Image:     req.Image,
		State:     sandbox.ContainerStateCreated,
		CreatedAt: time.Now(),
	}
	f.containers[req.ID] = c
	f.creates = append(f.creates, req)
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
		return xerrors.Errorf("%w: %s", sandbox.ErrNotFound, containerID)
	}
	if c.State != sandbox.ContainerStateRunning {
		c.State = sandbox.ContainerStateRunning
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
		return xerrors.Errorf("%w: %s", sandbox.ErrNotFound, containerID)
	}
	if c.State == sandbox.ContainerStateRunning {
		c.State = sandbox.ContainerStateStopped
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
		return xerrors.Errorf("%w: %s", sandbox.ErrNotFound, containerID)
	}
	delete(f.containers, containerID)
	return nil
}

// Status reads one container.
func (f *FakeRuntime) Status(_ context.Context, containerID string) (sandbox.Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("Status"); err != nil {
		return sandbox.Container{}, err
	}
	c, ok := f.containers[containerID]
	if !ok {
		return sandbox.Container{}, xerrors.Errorf("%w: %s", sandbox.ErrNotFound, containerID)
	}
	return c, nil
}
