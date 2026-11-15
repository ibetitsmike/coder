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
	"github.com/coder/coder/v2/coderd/notifications"
	"github.com/coder/coder/v2/testutil"
)

func TestListDueChatWakeups(t *testing.T) {
	t.Parallel()

	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitShort)
	chat := wakeupTestChat(t, db)
	now := dbtime.Now()

	older := insertDelayWakeup(ctx, t, fdb, chat, forkdb.ChatWakeupStatusActive, now.Add(-2*time.Minute))
	due := insertDelayWakeup(ctx, t, fdb, chat, forkdb.ChatWakeupStatusActive, now.Add(-time.Minute))
	insertDelayWakeup(ctx, t, fdb, chat, forkdb.ChatWakeupStatusPaused, now.Add(-time.Minute))
	next := insertDelayWakeup(ctx, t, fdb, chat, forkdb.ChatWakeupStatusActive, now.Add(time.Hour))
	insertDelayWakeup(ctx, t, fdb, chat, forkdb.ChatWakeupStatusActive, now.Add(2*time.Hour))

	busy := wakeupTestChat(t, db)
	_, err := sqlDB.ExecContext(ctx, "UPDATE chats SET status = 'running' WHERE id = $1", busy.ID)
	require.NoError(t, err)
	insertDelayWakeup(ctx, t, fdb, busy, forkdb.ChatWakeupStatusActive, now.Add(-time.Minute))

	archived := wakeupTestChat(t, db)
	_, err = sqlDB.ExecContext(ctx, "UPDATE chats SET status = 'running', archived = true WHERE id = $1", archived.ID)
	require.NoError(t, err)
	archivedDue := insertDelayWakeup(ctx, t, fdb, archived, forkdb.ChatWakeupStatusActive, now.Add(-time.Minute))

	list := func(arg forkdb.ListDueChatWakeupsParams) (dueIDs []uuid.UUID, upcoming []uuid.UUID) {
		t.Helper()
		if arg.LimitCount == 0 {
			arg.LimitCount = 10
		}
		arg.Now = now
		rows, err := fdb.ListDueChatWakeups(ctx, arg)
		require.NoError(t, err)
		for _, row := range rows {
			if row.Due {
				dueIDs = append(dueIDs, row.ChatWakeup.ID)
			} else {
				upcoming = append(upcoming, row.ChatWakeup.ID)
			}
		}
		return dueIDs, upcoming
	}

	dueIDs, upcoming := list(forkdb.ListDueChatWakeupsParams{})
	require.ElementsMatch(t, []uuid.UUID{older.ID, due.ID, archivedDue.ID}, dueIDs,
		"due active wake-ups of idle and archived chats are due, those of busy chats are not")
	require.Equal(t, []uuid.UUID{next.ID}, upcoming, "only the earliest wake-up that is not due comes back")

	dueIDs, upcoming = list(forkdb.ListDueChatWakeupsParams{ChatID: uuid.NullUUID{UUID: archived.ID, Valid: true}})
	require.Equal(t, []uuid.UUID{archivedDue.ID}, dueIDs, "chat_id limits the list to one chat")
	require.Empty(t, upcoming)

	// Listing reads only: the same rows are due again.
	dueIDs, _ = list(forkdb.ListDueChatWakeupsParams{ChatID: uuid.NullUUID{UUID: chat.ID, Valid: true}, LimitCount: 1})
	require.Equal(t, []uuid.UUID{older.ID}, dueIDs, "the due rows come in run time order")
	dueIDs, upcoming = list(forkdb.ListDueChatWakeupsParams{
		ChatID:     uuid.NullUUID{UUID: chat.ID, Valid: true},
		AfterRunAt: older.NextRunAt.Time,
		AfterID:    older.ID,
		LimitCount: 1,
	})
	require.Equal(t, []uuid.UUID{due.ID}, dueIDs, "the next page starts after the cursor")
	require.Equal(t, []uuid.UUID{next.ID}, upcoming)
}

// An events wake-up before its expiry is due as soon as a child it watches
// is done, before its run time.
func TestListDueChatWakeupsReadyEvents(t *testing.T) {
	t.Parallel()

	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitLong)
	now := dbtime.Now()
	parent := wakeupTestChat(t, db)
	child := func(status database.ChatStatus) database.Chat {
		t.Helper()
		return dbgen.Chat(t, db, database.Chat{
			OrganizationID:    parent.OrganizationID,
			OwnerID:           parent.OwnerID,
			LastModelConfigID: parent.LastModelConfigID,
			ParentChatID:      uuid.NullUUID{UUID: parent.ID, Valid: true},
			RootChatID:        uuid.NullUUID{UUID: parent.ID, Valid: true},
			Status:            status,
		})
	}
	// watching inserts an events wake-up of parent that runs at runAt and
	// expires at expiresAt, with one child_done watch on c.
	watching := func(c database.Chat, runAt, expiresAt time.Time) (forkdb.ChatWakeup, forkdb.ChatWakeupWatch) {
		t.Helper()
		wakeup := insertEventsWakeup(ctx, t, fdb, parent)
		_, err := sqlDB.ExecContext(ctx, "UPDATE chat_wakeups SET next_run_at = $2, expires_at = $3 WHERE id = $1", wakeup.ID, runAt, expiresAt)
		require.NoError(t, err)
		return wakeup, insertChildWatch(ctx, t, fdb, wakeup, 0, c)
	}

	done := child(database.ChatStatusWaiting)
	ready, _ := watching(done, now.Add(30*time.Minute), now.Add(2*time.Hour))
	running := child(database.ChatStatusRunning)
	waiting, _ := watching(running, now.Add(40*time.Minute), now.Add(2*time.Hour))
	_, failed := watching(done, now.Add(time.Hour), now.Add(2*time.Hour))
	_, err := sqlDB.ExecContext(ctx, "UPDATE chat_wakeup_watches SET failed_at = $2 WHERE id = $1", failed.ID, now)
	require.NoError(t, err)
	watching(done, now.Add(time.Hour), now.Add(-time.Minute))

	list := func(chatID uuid.NullUUID) (dueIDs []uuid.UUID, upcoming []uuid.UUID) {
		t.Helper()
		rows, err := fdb.ListDueChatWakeups(ctx, forkdb.ListDueChatWakeupsParams{ChatID: chatID, Now: now, LimitCount: 10})
		require.NoError(t, err)
		for _, row := range rows {
			if row.Due {
				dueIDs = append(dueIDs, row.ChatWakeup.ID)
			} else {
				upcoming = append(upcoming, row.ChatWakeup.ID)
			}
		}
		return dueIDs, upcoming
	}

	dueIDs, upcoming := list(uuid.NullUUID{})
	require.Equal(t, []uuid.UUID{ready.ID}, dueIDs,
		"only the wake-up with a done child is due: not the running child, the failed send or the expired wake-up")
	require.Equal(t, []uuid.UUID{waiting.ID}, upcoming, "a due wake-up does not set the timer")

	dueIDs, upcoming = list(uuid.NullUUID{UUID: done.ID, Valid: true})
	require.Equal(t, []uuid.UUID{ready.ID}, dueIDs, "chat_id also lists the wake-ups that watch it as a child")
	require.Empty(t, upcoming, "the timer follows the wake-ups of the chat itself")

	dueIDs, _ = list(uuid.NullUUID{UUID: running.ID, Valid: true})
	require.Empty(t, dueIDs, "a running child makes nothing due")

	dueIDs, upcoming = list(uuid.NullUUID{UUID: parent.ID, Valid: true})
	require.Equal(t, []uuid.UUID{ready.ID}, dueIDs)
	require.Equal(t, []uuid.UUID{waiting.ID}, upcoming)

	_, err = sqlDB.ExecContext(ctx, "UPDATE chats SET status = 'running' WHERE id = $1", parent.ID)
	require.NoError(t, err)
	dueIDs, _ = list(uuid.NullUUID{})
	require.Empty(t, dueIDs, "nothing is due while the parent is busy")
}

func TestLockActiveChatWakeupSkipsLockedRows(t *testing.T) {
	t.Parallel()

	db, _ := dbtestutil.NewDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitShort)
	chat := wakeupTestChat(t, db)
	now := dbtime.Now()
	wakeup := insertDelayWakeup(ctx, t, fdb, chat, forkdb.ChatWakeupStatusActive, now.Add(-time.Minute))
	paused := insertDelayWakeup(ctx, t, fdb, chat, forkdb.ChatWakeupStatusPaused, now.Add(-time.Minute))

	_, err := fdb.LockActiveChatWakeup(ctx, paused.ID)
	require.ErrorIs(t, err, sql.ErrNoRows, "only active wake-ups are delivered")

	// The first delivery keeps its transaction open, so its row stays locked
	// until the second one returns.
	locked := make(chan error, 1)
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- forkdb.InTx(db, func(_ database.Store, ftx forkdb.Store) error {
			_, err := ftx.LockActiveChatWakeup(ctx, wakeup.ID)
			locked <- err
			<-release
			return err
		}, nil)
	}()
	require.NoError(t, testutil.RequireReceive(ctx, t, locked))

	_, err = fdb.LockActiveChatWakeup(ctx, wakeup.ID)
	close(release)
	require.ErrorIs(t, err, sql.ErrNoRows, "a second delivery skips the locked wake-up without waiting")
	require.NoError(t, testutil.RequireReceive(ctx, t, firstDone))

	_, err = fdb.LockActiveChatWakeup(ctx, wakeup.ID)
	require.NoError(t, err, "the wake-up is free again after the first delivery")
}

func TestUpdateChatWakeupRun(t *testing.T) {
	t.Parallel()

	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitShort)
	chat := wakeupTestChat(t, db)
	now := dbtime.Now()
	wakeup := insertDelayWakeup(ctx, t, fdb, chat, forkdb.ChatWakeupStatusActive, now.Add(-time.Minute))
	// A replica of an older version claimed the wake-up.
	_, err := sqlDB.ExecContext(ctx, "UPDATE chat_wakeups SET lease_expires_at = $2 WHERE id = $1", wakeup.ID, now.Add(time.Minute))
	require.NoError(t, err)

	finished, err := fdb.UpdateChatWakeupRun(ctx, forkdb.UpdateChatWakeupRunParams{
		ID:            wakeup.ID,
		Status:        forkdb.ChatWakeupStatusCompleted,
		RunCountDelta: 1,
		LastRunAt:     sql.NullTime{Time: now, Valid: true},
		UpdatedAt:     now,
	})
	require.NoError(t, err)
	require.Equal(t, forkdb.ChatWakeupStatusCompleted, finished.Status)
	require.EqualValues(t, 1, finished.RunCount)
	require.False(t, finished.NextRunAt.Valid)
	require.True(t, finished.LastRunAt.Valid)
	require.False(t, finished.LeaseExpiresAt.Valid, "the run clears the older claim, so its finish writes nothing")

	again, err := fdb.UpdateChatWakeupRun(ctx, forkdb.UpdateChatWakeupRunParams{
		ID:        wakeup.ID,
		Status:    forkdb.ChatWakeupStatusCompleted,
		UpdatedAt: now.Add(time.Second),
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, again.RunCount, "a zero delta keeps the run count")
	require.True(t, again.LastRunAt.Valid, "a NULL last_run_at keeps the last run")
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

	_, err = insertRun("2026-01-02T00:00:00Z", forkdb.ChatWakeupRunStatusSkipped)
	require.NoError(t, err)
	_, err = insertRun("2026-01-03T00:00:00Z:failed", forkdb.ChatWakeupRunStatusFailed)
	require.NoError(t, err)

	_, err = insertRun("2026-01-04T00:00:00Z", forkdb.ChatWakeupRunStatusDelivered)
	require.NoError(t, err)

	since := now.Add(-time.Hour)
	count, err := fdb.CountChatWakeupRunsSince(ctx, forkdb.CountChatWakeupRunsSinceParams{ChatID: chat.ID, Since: since})
	require.NoError(t, err)
	require.EqualValues(t, 1, count, "only delivered runs count")
	count, err = fdb.CountOrganizationChatWakeupRunsSince(ctx, forkdb.CountOrganizationChatWakeupRunsSinceParams{OrganizationID: chat.OrganizationID, Since: since})
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}

func TestSumChatCostMicrosSince(t *testing.T) {
	t.Parallel()

	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitShort)
	now := dbtime.Now()
	since := now.Add(-24 * time.Hour)

	looping := wakeupTestChat(t, db)
	insertDelayWakeup(ctx, t, fdb, looping, forkdb.ChatWakeupStatusCompleted, now)
	insertCostMessage(ctx, t, db, sqlDB, looping, sql.NullInt64{Int64: 300, Valid: true}, now.Add(-time.Hour), false)
	insertCostMessage(ctx, t, db, sqlDB, looping, sql.NullInt64{Int64: 200, Valid: true}, now.Add(-2*time.Hour), true)
	insertCostMessage(ctx, t, db, sqlDB, looping, sql.NullInt64{}, now.Add(-time.Hour), false)
	insertCostMessage(ctx, t, db, sqlDB, looping, sql.NullInt64{Int64: 5000, Valid: true}, since.Add(-time.Minute), false)

	// A chat of the same organization that only a human drives.
	human := dbgen.Chat(t, db, database.Chat{
		OrganizationID:    looping.OrganizationID,
		OwnerID:           looping.OwnerID,
		LastModelConfigID: looping.LastModelConfigID,
	})
	insertCostMessage(ctx, t, db, sqlDB, human, sql.NullInt64{Int64: 7000, Valid: true}, now.Add(-time.Hour), false)

	spend, err := fdb.SumChatCostMicrosSince(ctx, forkdb.SumChatCostMicrosSinceParams{ChatID: looping.ID, Since: since})
	require.NoError(t, err)
	require.EqualValues(t, 500, spend, "deleted messages count, NULL costs and older messages do not")
	spend, err = fdb.SumOrganizationWakeupChatCostMicrosSince(ctx, forkdb.SumOrganizationWakeupChatCostMicrosSinceParams{OrganizationID: looping.OrganizationID, Since: since})
	require.NoError(t, err)
	require.EqualValues(t, 500, spend, "only chats with a wake-up count")

	empty := wakeupTestChat(t, db)
	spend, err = fdb.SumChatCostMicrosSince(ctx, forkdb.SumChatCostMicrosSinceParams{ChatID: empty.ID, Since: since})
	require.NoError(t, err)
	require.Zero(t, spend)
}

func TestCountChatUserNotificationsSince(t *testing.T) {
	t.Parallel()

	db, _ := dbtestutil.NewDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitShort)
	chat := wakeupTestChat(t, db)
	now := dbtime.Now()

	enqueue := func(templateID uuid.UUID, chatID uuid.UUID, message string, createdAt time.Time, methods ...database.NotificationMethod) {
		payload, err := json.Marshal(map[string]any{
			"labels": map[string]string{"custom_message": message},
			"data":   map[string]any{"chat_id": chatID.String()},
		})
		require.NoError(t, err)
		for i, method := range methods {
			require.NoError(t, db.EnqueueNotificationMessage(ctx, database.EnqueueNotificationMessageParams{
				ID:                     uuid.New(),
				NotificationTemplateID: templateID,
				UserID:                 chat.OwnerID,
				Method:                 method,
				Payload:                payload,
				CreatedBy:              "test",
				// The enqueuer stamps each method's row separately.
				CreatedAt: createdAt.Add(time.Duration(i) * time.Microsecond),
			}))
		}
	}
	custom := notifications.TemplateCustomNotification
	enqueue(custom, chat.ID, "one", now.Add(-10*time.Minute), database.NotificationMethodSmtp, database.NotificationMethodInbox)
	enqueue(custom, chat.ID, "two", now.Add(-5*time.Minute), database.NotificationMethodInbox)
	enqueue(custom, chat.ID, "old", now.Add(-2*time.Hour), database.NotificationMethodInbox)
	enqueue(custom, uuid.New(), "other chat", now.Add(-time.Minute), database.NotificationMethodInbox)
	enqueue(notifications.TemplateWorkspaceDeleted, chat.ID, "other template", now.Add(-time.Minute), database.NotificationMethodInbox)

	count, err := fdb.CountChatUserNotificationsSince(ctx, forkdb.CountChatUserNotificationsSinceParams{
		UserID:                 chat.OwnerID,
		NotificationTemplateID: custom,
		ChatID:                 chat.ID.String(),
		Since:                  now.Add(-time.Hour),
	})
	require.NoError(t, err)
	require.EqualValues(t, 2, count, "one enqueue counts once across its delivery methods")

	// The dedupe resets each UTC day, so the same payload enqueued again
	// after midnight is a second notification.
	midnight := now.UTC().Truncate(24 * time.Hour)
	otherChatID := uuid.New()
	enqueue(custom, otherChatID, "same", midnight.Add(-time.Minute), database.NotificationMethodInbox)
	enqueue(custom, otherChatID, "same", midnight.Add(time.Minute), database.NotificationMethodInbox)
	count, err = fdb.CountChatUserNotificationsSince(ctx, forkdb.CountChatUserNotificationsSinceParams{
		UserID:                 chat.OwnerID,
		NotificationTemplateID: custom,
		ChatID:                 otherChatID.String(),
		Since:                  midnight.Add(-time.Hour),
	})
	require.NoError(t, err)
	require.EqualValues(t, 2, count, "an identical payload on another UTC day counts again")
}

// insertCostMessage inserts a message of chat that cost costMicros at
// createdAt.
func insertCostMessage(ctx context.Context, t *testing.T, db database.Store, sqlDB *sql.DB, chat database.Chat, costMicros sql.NullInt64, createdAt time.Time, deleted bool) {
	t.Helper()
	msg := dbgen.ChatMessage(t, db, database.ChatMessage{ChatID: chat.ID, Role: database.ChatMessageRoleAssistant})
	_, err := sqlDB.ExecContext(ctx, "UPDATE chat_messages SET total_cost_micros = $1, created_at = $2, deleted = $3 WHERE id = $4",
		costMicros, createdAt, deleted, msg.ID)
	require.NoError(t, err)
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
		Status:    status,
		NextRunAt: sql.NullTime{Time: nextRunAt, Valid: true},
		CreatedAt: now,
		UpdatedAt: now,
	})
	require.NoError(t, err)
	require.Equal(t, chat.OrganizationID, wakeup.OrganizationID, "the wake-up takes the chat's organization")
	return wakeup
}
