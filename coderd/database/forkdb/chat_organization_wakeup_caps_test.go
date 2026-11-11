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

func TestUpsertChatOrganizationWakeupCaps(t *testing.T) {
	t.Parallel()

	db, _ := dbtestutil.NewDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitShort)
	org := dbgen.Organization(t, db, database.Organization{})

	_, err := fdb.GetChatOrganizationWakeupCaps(ctx, org.ID)
	require.ErrorIs(t, err, sql.ErrNoRows, "an organization starts without caps")

	_, err = fdb.UpsertChatOrganizationWakeupCaps(ctx, forkdb.UpsertChatOrganizationWakeupCapsParams{
		OrganizationID:                   org.ID,
		MaxChatRunsPerDay:                sql.NullInt32{Int32: 1440, Valid: true},
		MaxOrganizationRunsPerDay:        sql.NullInt32{Int32: 10, Valid: true},
		MaxChatSpendMicrosPerDay:         sql.NullInt64{Int64: 5_000_000_000, Valid: true},
		MaxOrganizationSpendMicrosPerDay: sql.NullInt64{Int64: 1, Valid: true},
	})
	require.NoError(t, err)

	got, err := fdb.UpsertChatOrganizationWakeupCaps(ctx, forkdb.UpsertChatOrganizationWakeupCapsParams{
		OrganizationID:           org.ID,
		MaxChatSpendMicrosPerDay: sql.NullInt64{Int64: 5_000_000_000, Valid: true},
	})
	require.NoError(t, err)
	require.Equal(t, sql.NullInt64{Int64: 5_000_000_000, Valid: true}, got.MaxChatSpendMicrosPerDay, "spend above int32 fits")
	got, err = fdb.GetChatOrganizationWakeupCaps(ctx, org.ID)
	require.NoError(t, err)
	require.False(t, got.MaxChatRunsPerDay.Valid, "a second upsert sets an omitted cap back to NULL")
	require.False(t, got.MaxOrganizationRunsPerDay.Valid)
	require.False(t, got.MaxOrganizationSpendMicrosPerDay.Valid)

	_, err = fdb.UpsertChatOrganizationWakeupCaps(ctx, forkdb.UpsertChatOrganizationWakeupCapsParams{
		OrganizationID:    org.ID,
		MaxChatRunsPerDay: sql.NullInt32{Int32: 0, Valid: true},
	})
	require.True(t, database.IsCheckViolation(err), "a cap of 0 is rejected: %v", err)
}
