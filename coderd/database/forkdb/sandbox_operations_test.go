package forkdb_test

import (
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbfake"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/testutil"
)

// sandboxWorkspace seeds a workspace with its first build and returns the
// workspace and that build.
func sandboxWorkspace(t *testing.T, db database.Store) (database.WorkspaceTable, database.WorkspaceBuild) {
	t.Helper()
	user := dbgen.User(t, db, database.User{})
	org := dbgen.Organization(t, db, database.Organization{})
	_ = dbgen.OrganizationMember(t, db, database.OrganizationMember{OrganizationID: org.ID, UserID: user.ID})
	resp := dbfake.WorkspaceBuild(t, db, database.WorkspaceTable{
		OrganizationID: org.ID,
		OwnerID:        user.ID,
	}).Do()
	return resp.Workspace, resp.Build
}

// nextSandboxBuild adds build number buildNumber to the workspace.
func nextSandboxBuild(t *testing.T, db database.Store, ws database.WorkspaceTable, buildNumber int32, transition database.WorkspaceTransition) database.WorkspaceBuild {
	t.Helper()
	return dbfake.WorkspaceBuild(t, db, ws).Seed(database.WorkspaceBuild{
		BuildNumber: buildNumber,
		Transition:  transition,
	}).Do().Build
}

func TestSandboxOperations(t *testing.T) {
	t.Parallel()

	t.Run("InsertDerivesFromBuild", func(t *testing.T) {
		t.Parallel()
		db, _ := dbtestutil.NewDB(t)
		ws, _ := sandboxWorkspace(t, db)
		build := nextSandboxBuild(t, db, ws, 3, database.WorkspaceTransitionStop)

		op := dbgen.SandboxOperation(t, db, build.ID)
		require.Equal(t, ws.ID, op.WorkspaceID)
		require.Equal(t, build.ID, op.WorkspaceBuildID)
		require.Equal(t, int64(3), op.Generation)
		require.Equal(t, database.WorkspaceTransitionStop, op.Transition)
		require.Equal(t, forkdb.SandboxOperationStatusIntent, op.Status)
	})

	t.Run("InsertReturnsExistingRow", func(t *testing.T) {
		t.Parallel()
		db, _ := dbtestutil.NewDB(t)
		_, build := sandboxWorkspace(t, db)

		first := dbgen.SandboxOperation(t, db, build.ID)
		second := dbgen.SandboxOperation(t, db, build.ID)
		require.Equal(t, first.ID, second.ID, "a replayed build reuses its intent")
	})

	t.Run("InsertUnknownBuild", func(t *testing.T) {
		t.Parallel()
		db, _ := dbtestutil.NewDB(t)
		ctx := testutil.Context(t, testutil.WaitShort)
		fdb := mustFrom(t, db)

		_, err := fdb.InsertSandboxOperation(ctx, forkdb.InsertSandboxOperationParams{
			ID:               uuid.New(),
			WorkspaceBuildID: uuid.New(),
		})
		require.ErrorIs(t, err, sql.ErrNoRows)
	})

	t.Run("ClaimThenComplete", func(t *testing.T) {
		t.Parallel()
		db, _ := dbtestutil.NewDB(t)
		ctx := testutil.Context(t, testutil.WaitShort)
		fdb := mustFrom(t, db)
		ws, build := sandboxWorkspace(t, db)
		op := dbgen.SandboxOperation(t, db, build.ID)

		claimed, err := fdb.ClaimSandboxOperation(ctx, forkdb.ClaimSandboxOperationParams{ID: op.ID, WorkspaceID: ws.ID})
		require.NoError(t, err)
		require.Equal(t, forkdb.SandboxOperationStatusClaimed, claimed.Status)

		_, err = fdb.ClaimSandboxOperation(ctx, forkdb.ClaimSandboxOperationParams{ID: op.ID, WorkspaceID: ws.ID})
		require.ErrorIs(t, err, sql.ErrNoRows, "a second claim must fail")

		_, err = fdb.CompleteSandboxOperation(ctx, forkdb.CompleteSandboxOperationParams{ID: op.ID, WorkspaceID: ws.ID, Status: forkdb.SandboxOperationStatusIntent})
		require.ErrorIs(t, err, sql.ErrNoRows, "complete accepts only done or failed")

		done, err := fdb.CompleteSandboxOperation(ctx, forkdb.CompleteSandboxOperationParams{ID: op.ID, WorkspaceID: ws.ID, Status: forkdb.SandboxOperationStatusDone})
		require.NoError(t, err)
		require.Equal(t, forkdb.SandboxOperationStatusDone, done.Status)

		_, err = fdb.CompleteSandboxOperation(ctx, forkdb.CompleteSandboxOperationParams{ID: op.ID, WorkspaceID: ws.ID, Status: forkdb.SandboxOperationStatusFailed})
		require.ErrorIs(t, err, sql.ErrNoRows, "a second complete must fail")
	})

	// A caller authorized for workspace A passes A's ID. It must not claim
	// or complete the operation of workspace B, even when both are at the
	// same generation.
	t.Run("OtherWorkspaceCannotClaimOrComplete", func(t *testing.T) {
		t.Parallel()
		db, _ := dbtestutil.NewDB(t)
		ctx := testutil.Context(t, testutil.WaitShort)
		fdb := mustFrom(t, db)
		wsA, buildA := sandboxWorkspace(t, db)
		wsB, buildB := sandboxWorkspace(t, db)
		opA := dbgen.SandboxOperation(t, db, buildA.ID)
		opB := dbgen.SandboxOperation(t, db, buildB.ID)
		require.Equal(t, opA.Generation, opB.Generation)

		_, err := fdb.ClaimSandboxOperation(ctx, forkdb.ClaimSandboxOperationParams{ID: opB.ID, WorkspaceID: wsA.ID})
		require.ErrorIs(t, err, sql.ErrNoRows)
		got, err := fdb.GetSandboxOperation(ctx, opB.ID)
		require.NoError(t, err)
		require.Equal(t, forkdb.SandboxOperationStatusIntent, got.Status, "B's operation stays unclaimed")

		_, err = fdb.ClaimSandboxOperation(ctx, forkdb.ClaimSandboxOperationParams{ID: opB.ID, WorkspaceID: wsB.ID})
		require.NoError(t, err)
		_, err = fdb.CompleteSandboxOperation(ctx, forkdb.CompleteSandboxOperationParams{ID: opB.ID, WorkspaceID: wsA.ID, Status: forkdb.SandboxOperationStatusDone})
		require.ErrorIs(t, err, sql.ErrNoRows)
		got, err = fdb.GetSandboxOperation(ctx, opB.ID)
		require.NoError(t, err)
		require.Equal(t, forkdb.SandboxOperationStatusClaimed, got.Status, "B's operation stays claimed")
	})

	// The newest build row fences, even before its worker writes an intent.
	t.Run("NewerBuildWithoutIntentFences", func(t *testing.T) {
		t.Parallel()
		db, _ := dbtestutil.NewDB(t)
		ctx := testutil.Context(t, testutil.WaitShort)
		fdb := mustFrom(t, db)
		ws, build1 := sandboxWorkspace(t, db)
		op1 := dbgen.SandboxOperation(t, db, build1.ID)
		op2 := dbgen.SandboxOperation(t, db, nextSandboxBuild(t, db, ws, 2, database.WorkspaceTransitionStop).ID)
		_, err := fdb.ClaimSandboxOperation(ctx, forkdb.ClaimSandboxOperationParams{ID: op2.ID, WorkspaceID: ws.ID})
		require.NoError(t, err)

		_ = nextSandboxBuild(t, db, ws, 3, database.WorkspaceTransitionStart)
		_, err = fdb.CompleteSandboxOperation(ctx, forkdb.CompleteSandboxOperationParams{ID: op2.ID, WorkspaceID: ws.ID, Status: forkdb.SandboxOperationStatusDone})
		require.ErrorIs(t, err, sql.ErrNoRows, "a newer build must fence the older worker")
		_, err = fdb.ClaimSandboxOperation(ctx, forkdb.ClaimSandboxOperationParams{ID: op1.ID, WorkspaceID: ws.ID})
		require.ErrorIs(t, err, sql.ErrNoRows)
	})

	// A newer build fences an older worker: the older operation can neither
	// claim nor complete once a newer generation exists, and the newer build
	// learns what the older one left unfinished.
	t.Run("NewerGenerationFencesOlder", func(t *testing.T) {
		t.Parallel()
		db, _ := dbtestutil.NewDB(t)
		ctx := testutil.Context(t, testutil.WaitShort)
		fdb := mustFrom(t, db)
		ws, build1 := sandboxWorkspace(t, db)
		op1 := dbgen.SandboxOperation(t, db, build1.ID)
		_, err := fdb.ClaimSandboxOperation(ctx, forkdb.ClaimSandboxOperationParams{ID: op1.ID, WorkspaceID: ws.ID})
		require.NoError(t, err)

		build2 := nextSandboxBuild(t, db, ws, 2, database.WorkspaceTransitionStart)
		op2 := dbgen.SandboxOperation(t, db, build2.ID)
		build3 := nextSandboxBuild(t, db, ws, 3, database.WorkspaceTransitionDelete)
		op3 := dbgen.SandboxOperation(t, db, build3.ID)

		_, err = fdb.CompleteSandboxOperation(ctx, forkdb.CompleteSandboxOperationParams{ID: op1.ID, WorkspaceID: ws.ID, Status: forkdb.SandboxOperationStatusDone})
		require.ErrorIs(t, err, sql.ErrNoRows, "a fenced worker must not complete")
		_, err = fdb.ClaimSandboxOperation(ctx, forkdb.ClaimSandboxOperationParams{ID: op2.ID, WorkspaceID: ws.ID})
		require.ErrorIs(t, err, sql.ErrNoRows, "a stale intent must not claim")

		superseded, err := fdb.SupersedeSandboxOperations(ctx, forkdb.SupersedeSandboxOperationsParams{WorkspaceID: ws.ID, Generation: op3.Generation})
		require.NoError(t, err)
		require.ElementsMatch(t, []string{op1.ID.String(), op2.ID.String()}, operationIDs(superseded))
		for _, op := range superseded {
			require.Equal(t, forkdb.SandboxOperationStatusSuperseded, op.Status)
		}

		claimed, err := fdb.ClaimSandboxOperation(ctx, forkdb.ClaimSandboxOperationParams{ID: op3.ID, WorkspaceID: ws.ID})
		require.NoError(t, err)
		require.Equal(t, forkdb.SandboxOperationStatusClaimed, claimed.Status)

		again, err := fdb.SupersedeSandboxOperations(ctx, forkdb.SupersedeSandboxOperationsParams{WorkspaceID: ws.ID, Generation: op3.Generation})
		require.NoError(t, err)
		require.Empty(t, again, "finished rows are not superseded twice")
	})

	t.Run("SupersedeKeepsFinishedAndOtherWorkspaces", func(t *testing.T) {
		t.Parallel()
		db, _ := dbtestutil.NewDB(t)
		ctx := testutil.Context(t, testutil.WaitShort)
		fdb := mustFrom(t, db)
		ws, build1 := sandboxWorkspace(t, db)
		other, otherBuild := sandboxWorkspace(t, db)
		op1 := dbgen.SandboxOperation(t, db, build1.ID)
		otherOp := dbgen.SandboxOperation(t, db, otherBuild.ID)
		_, err := fdb.ClaimSandboxOperation(ctx, forkdb.ClaimSandboxOperationParams{ID: op1.ID, WorkspaceID: ws.ID})
		require.NoError(t, err)
		_, err = fdb.CompleteSandboxOperation(ctx, forkdb.CompleteSandboxOperationParams{ID: op1.ID, WorkspaceID: ws.ID, Status: forkdb.SandboxOperationStatusFailed})
		require.NoError(t, err)

		superseded, err := fdb.SupersedeSandboxOperations(ctx, forkdb.SupersedeSandboxOperationsParams{WorkspaceID: ws.ID, Generation: 2})
		require.NoError(t, err)
		require.Empty(t, superseded)
		got, err := fdb.GetSandboxOperation(ctx, otherOp.ID)
		require.NoError(t, err)
		require.Equal(t, other.ID, got.WorkspaceID)
		require.Equal(t, forkdb.SandboxOperationStatusIntent, got.Status)
	})
}

func operationIDs(ops []forkdb.SandboxOperation) []string {
	ids := make([]string, 0, len(ops))
	for _, op := range ops {
		ids = append(ids, op.ID.String())
	}
	return ids
}
