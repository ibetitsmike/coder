package forkdb_test

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/testutil"
)

func TestUpsertChatOrganizationSubagentLimits(t *testing.T) {
	t.Parallel()

	db, _ := dbtestutil.NewDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitShort)
	org := dbgen.Organization(t, db, database.Organization{})

	_, err := fdb.GetChatOrganizationSubagentLimits(ctx, org.ID)
	require.ErrorIs(t, err, sql.ErrNoRows, "an organization starts without limits")

	first, err := fdb.UpsertChatOrganizationSubagentLimits(ctx, forkdb.UpsertChatOrganizationSubagentLimitsParams{
		OrganizationID:         org.ID,
		MaxDepth:               sql.NullInt32{Int32: 3, Valid: true},
		MaxConcurrent:          sql.NullInt32{Int32: 10, Valid: true},
		MaxConcurrentPerFamily: sql.NullInt32{Int32: 4, Valid: true},
		MaxLivePerFamily:       sql.NullInt32{Int32: 20, Valid: true},
	})
	require.NoError(t, err)
	require.Equal(t, sql.NullInt32{Int32: 3, Valid: true}, first.MaxDepth)

	_, err = fdb.UpsertChatOrganizationSubagentLimits(ctx, forkdb.UpsertChatOrganizationSubagentLimitsParams{
		OrganizationID: org.ID,
		MaxDepth:       sql.NullInt32{Int32: 2, Valid: true},
	})
	require.NoError(t, err)

	got, err := fdb.GetChatOrganizationSubagentLimits(ctx, org.ID)
	require.NoError(t, err)
	require.Equal(t, sql.NullInt32{Int32: 2, Valid: true}, got.MaxDepth)
	require.False(t, got.MaxConcurrent.Valid, "a second upsert sets an omitted limit back to NULL")
	require.False(t, got.MaxConcurrentPerFamily.Valid)
	require.False(t, got.MaxLivePerFamily.Valid)

	for _, depth := range []int32{0, 17} {
		_, err = fdb.UpsertChatOrganizationSubagentLimits(ctx, forkdb.UpsertChatOrganizationSubagentLimitsParams{
			OrganizationID: org.ID,
			MaxDepth:       sql.NullInt32{Int32: depth, Valid: true},
		})
		require.True(t, database.IsCheckViolation(err), "max_depth %d is outside 1 to 16: %v", depth, err)
	}
}
