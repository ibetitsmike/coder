// This file is fork code: the durable operation log the sandbox engine
// writes before it touches the container runtime.
package sandbox

import (
	"context"

	"github.com/google/uuid"
	"golang.org/x/xerrors"
)

// ErrSuperseded reports that a newer build of the workspace exists, so this
// build may not change the workspace's container or record a result.
var ErrSuperseded = xerrors.New("a newer build of the workspace superseded this build")

// Operation is the claimed operation of one build.
type Operation struct {
	ID          uuid.UUID
	WorkspaceID uuid.UUID
	BuildID     uuid.UUID
	Generation  int64
	// Abandoned reports that older builds of the workspace left operations
	// unfinished, for example because their worker crashed, and no newer
	// build completed as done since. The container of the workspace may then
	// exist without being recorded in the state.
	Abandoned bool
}

// Operations is the durable intent log of the engine. Begin runs before any
// runtime call and Complete after the last one, so a worker that a newer
// build superseded can neither start nor finish its operation.
type Operations interface {
	// Begin records the intent of the build, supersedes the unfinished
	// operations of older builds of the workspace and claims the intent. It
	// returns an error that errors.Is matches with ErrSuperseded when a newer
	// build exists or the intent was already claimed, and another error when
	// the build does not exist or belongs to another workspace.
	Begin(ctx context.Context, workspaceID, buildID uuid.UUID) (Operation, error)
	// Complete records the result of a claimed operation: failed when
	// applyErr is not nil, else done. It returns an
	// error that errors.Is matches with ErrSuperseded when a newer build
	// exists. Done also reconciles the superseded operations of older builds.
	Complete(ctx context.Context, op Operation, applyErr error) error
}
