package forkdb_test

import (
	"context"
	"database/sql"
	"encoding/json"
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

func TestClaimDueChatWakeups(t *testing.T) {
	t.Parallel()

	db, _ := dbtestutil.NewDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitShort)
	chat := wakeupTestChat(t, db)
	now := dbtime.Now()

	older := insertDelayWakeup(ctx, t, fdb, chat, forkdb.ChatWakeupStatusActive, now.Add(-2*time.Minute))
	due := insertDelayWakeup(ctx, t, fdb, chat, forkdb.ChatWakeupStatusActive, now.Add(-time.Minute))
	insertDelayWakeup(ctx, t, fdb, chat, forkdb.ChatWakeupStatusPaused, now.Add(-time.Minute))
	insertDelayWakeup(ctx, t, fdb, chat, forkdb.ChatWakeupStatusActive, now.Add(time.Hour))

	claim := func(at time.Time) []uuid.UUID {
		rows, err := fdb.ClaimDueChatWakeups(ctx, forkdb.ClaimDueChatWakeupsParams{
			Now:            at,
			LeaseExpiresAt: at.Add(time.Minute),
			LimitCount:     10,
		})
		require.NoError(t, err)
		return wakeupIDs(rows)
	}

	require.ElementsMatch(t, []uuid.UUID{older.ID, due.ID}, claim(now), "only due active wake-ups are claimed")
	require.Empty(t, claim(now), "a held lease is not claimed again")
	require.ElementsMatch(t, []uuid.UUID{older.ID, due.ID}, claim(now.Add(2*time.Minute)), "an expired lease is claimable again")
}

func TestClaimDueChatWakeupsSkipsLockedRows(t *testing.T) {
	t.Parallel()

	db, _ := dbtestutil.NewDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitShort)
	chat := wakeupTestChat(t, db)
	now := dbtime.Now()
	for range 4 {
		insertDelayWakeup(ctx, t, fdb, chat, forkdb.ChatWakeupStatusActive, now.Add(-time.Minute))
	}
	arg := forkdb.ClaimDueChatWakeupsParams{Now: now, LeaseExpiresAt: now.Add(time.Minute), LimitCount: 2}

	// The first claim keeps its transaction open, so its rows stay locked and
	// unclaimed to everyone else until the second claim returns.
	firstClaimed := make(chan []uuid.UUID, 1)
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- forkdb.InTx(db, func(_ database.Store, ftx forkdb.Store) error {
			rows, err := ftx.ClaimDueChatWakeups(ctx, arg)
			firstClaimed <- wakeupIDs(rows)
			<-release
			return err
		}, nil)
	}()
	first := testutil.RequireReceive(ctx, t, firstClaimed)

	arg.LimitCount = 10
	second, err := fdb.ClaimDueChatWakeups(ctx, arg)
	close(release)
	require.NoError(t, err, "the second claim must not wait for the first")
	require.NoError(t, testutil.RequireReceive(ctx, t, firstDone))

	require.Len(t, first, 2)
	require.Len(t, second, 2)
	for _, id := range wakeupIDs(second) {
		require.NotContains(t, first, id, "two claims returned the same wake-up")
	}
}

func TestFinishChatWakeupClaim(t *testing.T) {
	t.Parallel()

	db, _ := dbtestutil.NewDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitShort)
	chat := wakeupTestChat(t, db)
	now := dbtime.Now()
	wakeup := insertDelayWakeup(ctx, t, fdb, chat, forkdb.ChatWakeupStatusActive, now.Add(-time.Minute))

	claimOne := func(at time.Time) time.Time {
		rows, err := fdb.ClaimDueChatWakeups(ctx, forkdb.ClaimDueChatWakeupsParams{
			Now:            at,
			LeaseExpiresAt: at.Add(time.Minute),
			LimitCount:     1,
		})
		require.NoError(t, err)
		require.Len(t, rows, 1)
		return rows[0].LeaseExpiresAt.Time
	}
	staleLease := claimOne(now)
	// A second worker takes over after the first lease expired.
	currentLease := claimOne(now.Add(2 * time.Minute))

	finish := func(lease time.Time) (forkdb.ChatWakeup, error) {
		return fdb.FinishChatWakeupClaim(ctx, forkdb.FinishChatWakeupClaimParams{
			ID:             wakeup.ID,
			LeaseExpiresAt: lease,
			Status:         forkdb.ChatWakeupStatusCompleted,
			RunCountDelta:  1,
			LastRunAt:      sql.NullTime{Time: now, Valid: true},
			UpdatedAt:      now,
		})
	}
	_, err := finish(staleLease)
	require.ErrorIs(t, err, sql.ErrNoRows, "a worker that lost its lease cannot finish the claim")

	finished, err := finish(currentLease)
	require.NoError(t, err)
	require.Equal(t, forkdb.ChatWakeupStatusCompleted, finished.Status)
	require.EqualValues(t, 1, finished.RunCount)
	require.False(t, finished.LeaseExpiresAt.Valid, "finishing ends the claim")
}

func TestChatWakeupRuns(t *testing.T) {
	t.Parallel()

	db, _ := dbtestutil.NewDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitShort)
	chat := wakeupTestChat(t, db)
	now := dbtime.Now()
	wakeup := insertDelayWakeup(ctx, t, fdb, chat, forkdb.ChatWakeupStatusActive, now)

	insertRun := func(runKey string, status forkdb.ChatWakeupRunStatus) (forkdb.ChatWakeupRun, error) {
		return fdb.InsertChatWakeupRun(ctx, forkdb.InsertChatWakeupRunParams{
			ID:        uuid.New(),
			WakeupID:  wakeup.ID,
			RunKey:    runKey,
			Status:    status,
			CreatedAt: now,
			UpdatedAt: now,
		})
	}
	first, err := insertRun("2026-01-01T00:00:00Z", forkdb.ChatWakeupRunStatusPending)
	require.NoError(t, err)
	require.Equal(t, chat.ID, first.ChatID, "the run belongs to the wake-up's chat")
	require.Equal(t, chat.OrganizationID, first.OrganizationID)
	_, err = insertRun("2026-01-01T00:00:00Z", forkdb.ChatWakeupRunStatusPending)
	require.ErrorIs(t, err, sql.ErrNoRows, "an occurrence runs once")

	_, err = fdb.UpdateChatWakeupRunStatus(ctx, forkdb.UpdateChatWakeupRunStatusParams{
		ID:        first.ID,
		Status:    forkdb.ChatWakeupRunStatusDelivered,
		UpdatedAt: now,
	})
	require.NoError(t, err)
	_, err = insertRun("2026-01-02T00:00:00Z", forkdb.ChatWakeupRunStatusSkipped)
	require.NoError(t, err)

	require.NoError(t, fdb.DeleteChatWakeupByID(ctx, wakeup.ID))

	since := now.Add(-time.Hour)
	count, err := fdb.CountChatWakeupRunsSince(ctx, forkdb.CountChatWakeupRunsSinceParams{ChatID: chat.ID, Since: since})
	require.NoError(t, err)
	require.EqualValues(t, 1, count, "delivered runs count after their wake-up is deleted")
	count, err = fdb.CountOrganizationChatWakeupRunsSince(ctx, forkdb.CountOrganizationChatWakeupRunsSinceParams{OrganizationID: chat.OrganizationID, Since: since})
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}

func wakeupTestChat(t *testing.T, db database.Store) database.Chat {
	t.Helper()
	return dbgen.Chat(t, db, database.Chat{
		OrganizationID:    dbgen.Organization(t, db, database.Organization{}).ID,
		OwnerID:           dbgen.User(t, db, database.User{}).ID,
		LastModelConfigID: dbgen.ChatModelConfig(t, db, database.ChatModelConfig{}).ID,
	})
}

func insertDelayWakeup(ctx context.Context, t *testing.T, fdb forkdb.Store, chat database.Chat, status forkdb.ChatWakeupStatus, nextRunAt time.Time) forkdb.ChatWakeup {
	t.Helper()
	now := dbtime.Now()
	wakeup, err := fdb.InsertChatWakeup(ctx, forkdb.InsertChatWakeupParams{
		ID:        uuid.New(),
		ChatID:    chat.ID,
		CreatedBy: chat.OwnerID,
		Source:    "schedule_wakeup",
		Kind:      forkdb.ChatWakeupKindDelay,
		Prompt:    "check the build",
		Timezone:  "UTC",
		Events:    json.RawMessage(`[]`),
		Status:    status,
		NextRunAt: sql.NullTime{Time: nextRunAt, Valid: true},
		CreatedAt: now,
		UpdatedAt: now,
	})
	require.NoError(t, err)
	require.Equal(t, chat.OrganizationID, wakeup.OrganizationID, "the wake-up takes the chat's organization")
	return wakeup
}

func wakeupIDs(rows []forkdb.ChatWakeup) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}
