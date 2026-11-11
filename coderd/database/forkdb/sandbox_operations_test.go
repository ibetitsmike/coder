package forkdb_test

import (
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/testutil"
)

// setupWorkspace seeds the organization, owner, and workspace that own
// sandbox operation rows.
func setupWorkspace(t *testing.T, db database.Store) database.WorkspaceTable {
	t.Helper()

	user := dbgen.User(t, db, database.User{})
	org := dbgen.Organization(t, db, database.Organization{})
	_ = dbgen.OrganizationMember(t, db, database.OrganizationMember{OrganizationID: org.ID, UserID: user.ID})
	return dbgen.Workspace(t, db, database.WorkspaceTable{
		OrganizationID: org.ID,
		TemplateID: dbgen.Template(t, db, database.Template{
			OrganizationID: org.ID,
			CreatedBy:      user.ID,
		}).ID,
		OwnerID: user.ID,
	})
}

func TestSandboxOperations(t *testing.T) {
	t.Parallel()

	t.Run("InsertIntent", func(t *testing.T) {
		t.Parallel()

		db, _ := dbtestutil.NewDB(t)
		ws := setupWorkspace(t, db)
		op := dbgen.SandboxOperation(t, db, forkdb.SandboxOperation{
			WorkspaceID:      ws.ID,
			WorkspaceBuildID: uuid.New(),
			Operation:        "create",
			Generation:       2,
		})
		require.Equal(t, "intent", op.Status)
		require.Equal(t, int64(2), op.Generation)

		ctx := testutil.Context(t, testutil.WaitShort)
		fdb := mustFrom(t, db)
		got, err := fdb.GetSandboxOperation(ctx, op.ID)
		require.NoError(t, err)
		require.Equal(t, op, got)
	})

	t.Run("ClaimFlipsToClaimed", func(t *testing.T) {
		t.Parallel()

		db, _ := dbtestutil.NewDB(t)
		ws := setupWorkspace(t, db)
		op := dbgen.SandboxOperation(t, db, forkdb.SandboxOperation{
			WorkspaceID:      ws.ID,
			WorkspaceBuildID: uuid.New(),
			Operation:        "stop",
		})
		fdb := mustFrom(t, db)
		ctx := testutil.Context(t, testutil.WaitShort)

		claimed, err := fdb.ClaimSandboxOperation(ctx, forkdb.ClaimSandboxOperationParams{
			ID:          op.ID,
			WorkspaceID: ws.ID,
		})
		require.NoError(t, err)
		require.Equal(t, "claimed", claimed.Status)

		got, err := fdb.GetSandboxOperation(ctx, op.ID)
		require.NoError(t, err)
		require.Equal(t, "claimed", got.Status)
	})

	t.Run("StaleGenerationCannotClaim", func(t *testing.T) {
		t.Parallel()

		db, _ := dbtestutil.NewDB(t)
		ws := setupWorkspace(t, db)
		fdb := mustFrom(t, db)
		ctx := testutil.Context(t, testutil.WaitShort)

		stale := dbgen.SandboxOperation(t, db, forkdb.SandboxOperation{
			WorkspaceID:      ws.ID,
			WorkspaceBuildID: uuid.New(),
			Operation:        "create",
			Generation:       1,
		})
		dbgen.SandboxOperation(t, db, forkdb.SandboxOperation{
			WorkspaceID:      ws.ID,
			WorkspaceBuildID: uuid.New(),
			Operation:        "create",
			Generation:       2,
		})

		_, err := fdb.ClaimSandboxOperation(ctx, forkdb.ClaimSandboxOperationParams{
			ID:          stale.ID,
			WorkspaceID: ws.ID,
		})
		require.Error(t, err, "a superseded generation must not claim")
		got, err := fdb.GetSandboxOperation(ctx, stale.ID)
		require.NoError(t, err)
		require.Equal(t, "intent", got.Status, "the stale row stays an intent")
	})

	t.Run("CompleteAndDoubleComplete", func(t *testing.T) {
		t.Parallel()

		db, _ := dbtestutil.NewDB(t)
		ws := setupWorkspace(t, db)
		fdb := mustFrom(t, db)
		ctx := testutil.Context(t, testutil.WaitShort)

		op := dbgen.SandboxOperation(t, db, forkdb.SandboxOperation{
			WorkspaceID:      ws.ID,
			WorkspaceBuildID: uuid.New(),
			Operation:        "create",
		})
		_, err := fdb.ClaimSandboxOperation(ctx, forkdb.ClaimSandboxOperationParams{
			ID:          op.ID,
			WorkspaceID: ws.ID,
		})
		require.NoError(t, err)

		done, err := fdb.CompleteSandboxOperation(ctx, forkdb.CompleteSandboxOperationParams{
			ID:     op.ID,
			Status: "done",
		})
		require.NoError(t, err)
		require.Equal(t, "done", done.Status)

		// A claimed row completes once.
		_, err = fdb.CompleteSandboxOperation(ctx, forkdb.CompleteSandboxOperationParams{
			ID:     op.ID,
			Status: "failed",
		})
		require.Error(t, err)
	})

	t.Run("ConcurrentClaimsResolve", func(t *testing.T) {
		t.Parallel()

		db, _ := dbtestutil.NewDB(t)
		ws := setupWorkspace(t, db)
		fdb := mustFrom(t, db)
		ctx := testutil.Context(t, testutil.WaitMedium)

		oldOp := dbgen.SandboxOperation(t, db, forkdb.SandboxOperation{
			WorkspaceID:      ws.ID,
			WorkspaceBuildID: uuid.New(),
			Operation:        "stop",
			Generation:       1,
		})
		newOp := dbgen.SandboxOperation(t, db, forkdb.SandboxOperation{
			WorkspaceID:      ws.ID,
			WorkspaceBuildID: uuid.New(),
			Operation:        "stop",
			Generation:       2,
		})

		var wg sync.WaitGroup
		results := make(chan error, 2)
		for _, op := range []forkdb.SandboxOperation{oldOp, newOp} {
			wg.Add(1)
			go func(op forkdb.SandboxOperation) {
				defer wg.Done()
				_, err := fdb.ClaimSandboxOperation(ctx, forkdb.ClaimSandboxOperationParams{
					ID:          op.ID,
					WorkspaceID: ws.ID,
				})
				results <- err
			}(op)
		}
		wg.Wait()
		close(results)

		var claimedCount int
		for err := range results {
			if err == nil {
				claimedCount++
			}
		}
		// Exactly one claimant wins the workspace's newest generation.
		require.Equal(t, 1, claimedCount)

		gotOld, err := fdb.GetSandboxOperation(ctx, oldOp.ID)
		require.NoError(t, err)
		gotNew, err := fdb.GetSandboxOperation(ctx, newOp.ID)
		require.NoError(t, err)
		if gotOld.Status == "claimed" {
			require.Equal(t, "intent", gotNew.Status)
		} else {
			require.Equal(t, "claimed", gotNew.Status)
		}
	})
}

// BenchmarkClaimSandboxOperation runs one round of eight goroutines claiming
// eight generations of one workspace: seven rejections and one claim. The
// op time bounds the latency of a fenced claim on the workspace Postgres.
func BenchmarkClaimSandboxOperation(b *testing.B) {
	db, _ := dbtestutil.NewDB(b)
	user := dbgen.User(b, db, database.User{})
	org := dbgen.Organization(b, db, database.Organization{})
	_ = dbgen.OrganizationMember(b, db, database.OrganizationMember{OrganizationID: org.ID, UserID: user.ID})
	ws := dbgen.Workspace(b, db, database.WorkspaceTable{
		OrganizationID: org.ID,
		TemplateID: dbgen.Template(b, db, database.Template{
			OrganizationID: org.ID,
			CreatedBy:      user.ID,
		}).ID,
		OwnerID: user.ID,
	})
	fdb, err := forkdb.From(db)
	require.NoError(b, err, "fork queries")
	ctx := testutil.Context(b, testutil.WaitMedium)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ids := make([]uuid.UUID, 8)
		for gen := int64(1); gen <= 8; gen++ {
			op := dbgen.SandboxOperation(b, db, forkdb.SandboxOperation{
				WorkspaceID:      ws.ID,
				WorkspaceBuildID: uuid.New(),
				Operation:        "create",
				Generation:       gen,
			})
			ids[gen-1] = op.ID
		}
		var wg sync.WaitGroup
		for gen := int64(1); gen <= 8; gen++ {
			wg.Add(1)
			go func(gen int64, id uuid.UUID) {
				defer wg.Done()
				_, _ = fdb.ClaimSandboxOperation(ctx, forkdb.ClaimSandboxOperationParams{
					ID:          id,
					WorkspaceID: ws.ID,
				})
			}(gen, ids[gen-1])
		}
		wg.Wait()
	}
}
