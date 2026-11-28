package forkdb_test

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/testutil"
)

func TestUpsertChatAPNsDeviceKeepsTheNewest(t *testing.T) {
	t.Parallel()

	db, _ := dbtestutil.NewDB(t)
	fdb := mustFrom(t, db)

	userA := dbgen.User(t, db, database.User{})
	userB := dbgen.User(t, db, database.User{})

	// fakeToken builds a distinct 64 hex char token.
	fakeToken := func(i int) string {
		return fmt.Sprintf("%064x", i)
	}

	base := dbtime.Now().Add(-time.Hour)
	upsert := func(ctx context.Context, t *testing.T, userID uuid.UUID, token string, at time.Time) {
		t.Helper()
		err := fdb.UpsertChatAPNsDevice(ctx, forkdb.UpsertChatAPNsDeviceParams{
			DeviceToken: token,
			UserID:      userID,
			Environment: "sandbox",
			Now:         at,
			MaxDevices:  3,
		})
		require.NoError(t, err)
	}

	tokens := func(ctx context.Context, t *testing.T, userID uuid.UUID) []string {
		t.Helper()
		devices, err := fdb.GetChatAPNsDevicesByUserID(ctx, userID)
		require.NoError(t, err)
		got := make([]string, 0, len(devices))
		for _, d := range devices {
			got = append(got, d.DeviceToken)
		}
		// GetChatAPNsDevicesByUserID orders by created_at, which the upsert
		// does not change, so sort and compare as sets.
		sort.Strings(got)
		return got
	}

	t.Run("KeepsOnlyTheNewestMaxDevices", func(t *testing.T) {
		t.Parallel()
		// The subtest starts after the parent returns and a parallel slot
		// frees up, so its deadline starts here, not in the parent.
		ctx := testutil.Context(t, testutil.WaitShort)

		// User B first, so its rows must survive user A's registrations.
		upsert(ctx, t, userB.ID, fakeToken(101), base.Add(2*time.Minute))
		upsert(ctx, t, userB.ID, fakeToken(102), base.Add(3*time.Minute))

		for i := 0; i < 4; i++ {
			upsert(ctx, t, userA.ID, fakeToken(i), base.Add(time.Duration(i+1)*time.Minute))
		}

		// 0 is the oldest, so it went. 1, 2, 3 stay.
		require.Equal(t, []string{fakeToken(1), fakeToken(2), fakeToken(3)},
			tokens(ctx, t, userA.ID))

		// User B's rows are untouched.
		require.Equal(t, []string{fakeToken(101), fakeToken(102)},
			tokens(ctx, t, userB.ID))

		// Re-register the oldest remaining token with a newer time, then add
		// a new token. The re-registered token 1 stays, and the next oldest,
		// 2, goes.
		upsert(ctx, t, userA.ID, fakeToken(1), base.Add(9*time.Minute))
		upsert(ctx, t, userA.ID, fakeToken(4), base.Add(10*time.Minute))
		require.Equal(t, []string{fakeToken(1), fakeToken(3), fakeToken(4)},
			tokens(ctx, t, userA.ID))
		// A token moving from B to A must not delete B's other row. A's set
		// was 1, 3, 4, so the oldest, 3, drops and 102 joins.
		upsert(ctx, t, userA.ID, fakeToken(102), base.Add(11*time.Minute))
		require.Equal(t, []string{fakeToken(1), fakeToken(4), fakeToken(102)},
			tokens(ctx, t, userA.ID))
		require.Equal(t, []string{fakeToken(101)}, tokens(ctx, t, userB.ID))
	})
}
