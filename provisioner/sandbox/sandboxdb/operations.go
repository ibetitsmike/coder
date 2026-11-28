// Package sandboxdb stores the operation log of the sandbox engine in the
// sandbox_operations table of the fork-sandbox-provisioner experiment.
package sandboxdb

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/provisioner/sandbox"
)

// Operations implements sandbox.Operations over the fork queries. It acts
// as the provisioner daemon, because the engine runs a job that coderd
// already authorized and the session context carries no actor.
type Operations struct {
	db forkdb.Store
}

var _ sandbox.Operations = (*Operations)(nil)

// New returns the operation log of db, which must be a store that dbauthz
// wraps.
func New(db database.Store) (*Operations, error) {
	fdb, err := forkdb.From(db)
	if err != nil {
		return nil, xerrors.Errorf("fork queries: %w", err)
	}
	return &Operations{db: fdb}, nil
}

// Begin writes the intent of the build, supersedes the unfinished
// operations of older builds and claims the intent.
func (o *Operations) Begin(ctx context.Context, workspaceID, buildID uuid.UUID) (sandbox.Operation, error) {
	//nolint:gocritic // The engine runs a job that coderd already authorized.
	ctx = dbauthz.AsProvisionerd(ctx)
	row, err := o.db.InsertSandboxOperation(ctx, forkdb.InsertSandboxOperationParams{
		ID:               uuid.New(),
		WorkspaceBuildID: buildID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return sandbox.Operation{}, xerrors.Errorf("build %s does not exist", buildID)
	}
	if err != nil {
		return sandbox.Operation{}, xerrors.Errorf("insert intent: %w", err)
	}
	if row.WorkspaceID != workspaceID {
		return sandbox.Operation{}, xerrors.Errorf("build %s belongs to another workspace", buildID)
	}
	superseded, err := o.db.SupersedeSandboxOperations(ctx, forkdb.SupersedeSandboxOperationsParams{
		WorkspaceID: workspaceID,
		Generation:  row.Generation,
	})
	if err != nil {
		return sandbox.Operation{}, xerrors.Errorf("supersede older operations: %w", err)
	}
	_, err = o.db.ClaimSandboxOperation(ctx, forkdb.ClaimSandboxOperationParams{ID: row.ID, WorkspaceID: workspaceID})
	if errors.Is(err, sql.ErrNoRows) {
		return sandbox.Operation{}, xerrors.Errorf("claim: %w", sandbox.ErrSuperseded)
	}
	if err != nil {
		return sandbox.Operation{}, xerrors.Errorf("claim: %w", err)
	}
	return sandbox.Operation{
		ID:          row.ID,
		WorkspaceID: workspaceID,
		BuildID:     buildID,
		Generation:  row.Generation,
		Abandoned:   len(superseded) > 0,
	}, nil
}

// Complete records the result of a claimed operation.
func (o *Operations) Complete(ctx context.Context, op sandbox.Operation, applyErr error) error {
	status := forkdb.SandboxOperationStatusDone
	if applyErr != nil {
		status = forkdb.SandboxOperationStatusFailed
	}
	//nolint:gocritic // The engine runs a job that coderd already authorized.
	_, err := o.db.CompleteSandboxOperation(dbauthz.AsProvisionerd(ctx), forkdb.CompleteSandboxOperationParams{
		ID:          op.ID,
		WorkspaceID: op.WorkspaceID,
		Status:      status,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return xerrors.Errorf("complete: %w", sandbox.ErrSuperseded)
	}
	if err != nil {
		return xerrors.Errorf("complete: %w", err)
	}
	return nil
}
