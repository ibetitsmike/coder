package sandboxtest

import (
	"context"
	"sync"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/provisioner/sandbox"
)

// The statuses FakeOperations reports, the same as the database rows use.
const (
	OperationIntent     = "intent"
	OperationClaimed    = "claimed"
	OperationDone       = "done"
	OperationFailed     = "failed"
	OperationSuperseded = "superseded"
	OperationReconciled = "reconciled"
)

// FakeOperations is an in-memory sandbox.Operations with the semantics of
// the database queries. NewBuild stands in for a new workspace_builds row:
// the generation of a build is its build number, and only the newest
// generation of a workspace may claim or complete.
type FakeOperations struct {
	mu     sync.Mutex
	latest map[uuid.UUID]int64
	builds map[uuid.UUID]sandbox.Operation
	status map[uuid.UUID]string
}

var _ sandbox.Operations = (*FakeOperations)(nil)

// NewFakeOperations returns an empty fake.
func NewFakeOperations() *FakeOperations {
	return &FakeOperations{
		latest: make(map[uuid.UUID]int64),
		builds: make(map[uuid.UUID]sandbox.Operation),
		status: make(map[uuid.UUID]string),
	}
}

// NewBuild adds the next build of the workspace and returns its ID. Like a
// new build row, it fences the older builds before it begins.
func (f *FakeOperations) NewBuild(workspaceID uuid.UUID) uuid.UUID {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.latest[workspaceID]++
	id := uuid.New()
	f.builds[id] = sandbox.Operation{ID: uuid.New(), WorkspaceID: workspaceID, BuildID: id, Generation: f.latest[workspaceID]}
	return id
}

// Begin records, supersedes and claims as sandbox.Operations describes.
func (f *FakeOperations) Begin(_ context.Context, workspaceID, buildID uuid.UUID) (sandbox.Operation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	op, ok := f.builds[buildID]
	if !ok {
		return sandbox.Operation{}, xerrors.Errorf("build %s does not exist", buildID)
	}
	if op.WorkspaceID != workspaceID {
		return sandbox.Operation{}, xerrors.Errorf("build %s belongs to another workspace", buildID)
	}
	if f.status[buildID] == "" {
		f.status[buildID] = OperationIntent
	}
	for id, other := range f.builds {
		s := f.status[id]
		if other.WorkspaceID == workspaceID && other.Generation < op.Generation &&
			(s == OperationIntent || s == OperationClaimed || s == OperationSuperseded) {
			f.status[id] = OperationSuperseded
			op.Abandoned = true
		}
	}
	if f.status[buildID] != OperationIntent || op.Generation != f.latest[workspaceID] {
		return sandbox.Operation{}, xerrors.Errorf("claim: %w", sandbox.ErrSuperseded)
	}
	f.status[buildID] = OperationClaimed
	return op, nil
}

// Complete records the result as sandbox.Operations describes.
func (f *FakeOperations) Complete(_ context.Context, op sandbox.Operation, applyErr error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.status[op.BuildID] != OperationClaimed || op.Generation != f.latest[op.WorkspaceID] {
		return xerrors.Errorf("complete: %w", sandbox.ErrSuperseded)
	}
	if applyErr != nil {
		f.status[op.BuildID] = OperationFailed
		return nil
	}
	f.status[op.BuildID] = OperationDone
	for id, other := range f.builds {
		if other.WorkspaceID == op.WorkspaceID && other.Generation < op.Generation && f.status[id] == OperationSuperseded {
			f.status[id] = OperationReconciled
		}
	}
	return nil
}

// Status returns the status of the build's operation, or "" if it has none.
func (f *FakeOperations) Status(buildID uuid.UUID) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status[buildID]
}
