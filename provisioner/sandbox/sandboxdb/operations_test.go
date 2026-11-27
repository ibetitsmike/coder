package sandboxdb_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3/sloggers/slogtest"

	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/dbfake"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/provisioner/sandbox"
	"github.com/coder/coder/v2/provisioner/sandbox/sandboxdb"
	"github.com/coder/coder/v2/provisioner/sandbox/sandboxtest"
	"github.com/coder/coder/v2/testutil"
)

var errFailed = xerrors.New("apply failed")

// harness is one implementation of sandbox.Operations with a way to add
// builds and read the status of an operation.
type harness struct {
	ops          sandbox.Operations
	newWorkspace func() uuid.UUID
	newBuild     func(workspaceID uuid.UUID) uuid.UUID
	status       func(op sandbox.Operation) string
}

func fakeHarness(*testing.T) harness {
	fake := sandboxtest.NewFakeOperations()
	return harness{
		ops:          fake,
		newWorkspace: uuid.New,
		newBuild:     fake.NewBuild,
		status:       func(op sandbox.Operation) string { return fake.Status(op.BuildID) },
	}
}

func databaseHarness(t *testing.T) harness {
	store, _ := dbtestutil.NewDB(t)
	db := dbauthz.New(store, rbac.NewAuthorizer(prometheus.NewRegistry()), slogtest.Make(t, nil), coderdtest.AccessControlStorePointer())
	ops, err := sandboxdb.New(db)
	require.NoError(t, err)
	fdb, err := forkdb.From(store)
	require.NoError(t, err)
	org := dbgen.Organization(t, store, database.Organization{})
	user := dbgen.User(t, store, database.User{})
	workspaces := map[uuid.UUID]database.WorkspaceTable{}
	builds := map[uuid.UUID]int32{}
	// dbfake creates a workspace with its first build, which the first
	// newBuild call of the workspace returns.
	first := map[uuid.UUID]uuid.UUID{}
	return harness{
		ops: ops,
		newWorkspace: func() uuid.UUID {
			resp := dbfake.WorkspaceBuild(t, store, database.WorkspaceTable{OrganizationID: org.ID, OwnerID: user.ID}).Do()
			workspaces[resp.Workspace.ID] = resp.Workspace
			builds[resp.Workspace.ID] = resp.Build.BuildNumber
			first[resp.Workspace.ID] = resp.Build.ID
			return resp.Workspace.ID
		},
		newBuild: func(workspaceID uuid.UUID) uuid.UUID {
			if id, ok := first[workspaceID]; ok {
				delete(first, workspaceID)
				return id
			}
			builds[workspaceID]++
			return dbfake.WorkspaceBuild(t, store, workspaces[workspaceID]).Seed(database.WorkspaceBuild{
				BuildNumber: builds[workspaceID],
			}).Do().Build.ID
		},
		status: func(op sandbox.Operation) string {
			row, err := fdb.GetSandboxOperation(testutil.Context(t, testutil.WaitShort), op.ID)
			require.NoError(t, err)
			return row.Status
		},
	}
}

// TestOperations runs the same contract against the database and the fake
// that the engine tests use, so the fake cannot drift from the queries.
func TestOperations(t *testing.T) {
	t.Parallel()

	for name, newHarness := range map[string]func(*testing.T) harness{
		"Fake":     fakeHarness,
		"Database": databaseHarness,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			t.Run("BeginThenComplete", func(t *testing.T) {
				t.Parallel()
				h := newHarness(t)
				ctx := testutil.Context(t, testutil.WaitShort)
				ws := h.newWorkspace()
				build := h.newBuild(ws)

				op, err := h.ops.Begin(ctx, ws, build)
				require.NoError(t, err)
				require.Equal(t, int64(1), op.Generation)
				require.False(t, op.Abandoned)
				require.Equal(t, sandboxtest.OperationClaimed, h.status(op))
				_, err = h.ops.Begin(ctx, ws, build)
				require.ErrorIs(t, err, sandbox.ErrSuperseded, "a second worker for the build must not claim")

				require.NoError(t, h.ops.Complete(ctx, op, errFailed))
				require.Equal(t, sandboxtest.OperationFailed, h.status(op))
				require.ErrorIs(t, h.ops.Complete(ctx, op, nil), sandbox.ErrSuperseded)
			})

			t.Run("UnknownOrForeignBuild", func(t *testing.T) {
				t.Parallel()
				h := newHarness(t)
				ctx := testutil.Context(t, testutil.WaitShort)
				wsA, wsB := h.newWorkspace(), h.newWorkspace()
				buildB := h.newBuild(wsB)

				_, err := h.ops.Begin(ctx, wsA, uuid.New())
				require.ErrorContains(t, err, "does not exist")
				_, err = h.ops.Begin(ctx, wsA, buildB)
				require.ErrorContains(t, err, "another workspace")
				require.NotErrorIs(t, err, sandbox.ErrSuperseded)
			})

			// A newer build row fences an older worker before and after
			// it begins.
			t.Run("NewerBuildFences", func(t *testing.T) {
				t.Parallel()
				h := newHarness(t)
				ctx := testutil.Context(t, testutil.WaitShort)
				ws := h.newWorkspace()
				op1, err := h.ops.Begin(ctx, ws, h.newBuild(ws))
				require.NoError(t, err)
				stale := h.newBuild(ws)
				latest := h.newBuild(ws)

				require.ErrorIs(t, h.ops.Complete(ctx, op1, nil), sandbox.ErrSuperseded)
				_, err = h.ops.Begin(ctx, ws, stale)
				require.ErrorIs(t, err, sandbox.ErrSuperseded, "a stale build must not begin")

				op3, err := h.ops.Begin(ctx, ws, latest)
				require.NoError(t, err)
				require.True(t, op3.Abandoned, "build 1 left its operation unfinished")
				require.Equal(t, sandboxtest.OperationSuperseded, h.status(op1))
			})

			// A build that finds an abandoned operation and fails leaves it
			// to the next build, until one completes as done.
			t.Run("FailedRecoveryKeepsAbandoned", func(t *testing.T) {
				t.Parallel()
				h := newHarness(t)
				ctx := testutil.Context(t, testutil.WaitShort)
				ws := h.newWorkspace()
				op1, err := h.ops.Begin(ctx, ws, h.newBuild(ws))
				require.NoError(t, err)
				op2, err := h.ops.Begin(ctx, ws, h.newBuild(ws))
				require.NoError(t, err)
				require.True(t, op2.Abandoned)
				require.NoError(t, h.ops.Complete(ctx, op2, errFailed))

				op3, err := h.ops.Begin(ctx, ws, h.newBuild(ws))
				require.NoError(t, err)
				require.True(t, op3.Abandoned, "build 2 failed to recover build 1")
				require.NoError(t, h.ops.Complete(ctx, op3, nil))
				require.Equal(t, sandboxtest.OperationReconciled, h.status(op1))
				require.Equal(t, sandboxtest.OperationFailed, h.status(op2))

				op4, err := h.ops.Begin(ctx, ws, h.newBuild(ws))
				require.NoError(t, err)
				require.False(t, op4.Abandoned, "build 3 reconciled the older operations")
			})
		})
	}
}
