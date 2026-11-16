package chatd

import (
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/testutil"
)

func TestWakeupRunCounts(t *testing.T) {
	t.Parallel()
	intervalWakeup := forkdb.InsertChatWakeupParams{
		Kind:            forkdb.ChatWakeupKindInterval,
		IntervalSeconds: sql.NullInt32{Int32: 60, Valid: true},
	}

	t.Run("EachDeliveryCounts", func(t *testing.T) {
		t.Parallel()
		f := newWorkerTestFixture(t)
		ctx := testutil.Context(t, testutil.WaitLong)
		chat := f.createWakeupChat(t)
		wakeup := f.insertWakeup(t, chat, intervalWakeup)
		worker := f.newArchiveWorker(t, nil, nil, nil)

		const deliveries = 3
		for i := range deliveries {
			worker.wakeupOnce(ctx, wakeupTestSlot.Add(time.Duration(i)*time.Minute+10*time.Second))
			require.Equal(t, i+1, f.countPromptMessages(t, chat, wakeup.Prompt))
			forceExecutionState(t, f, chat.ID, database.ChatStatusWaiting, false)
		}
		require.EqualValues(t, deliveries, f.wakeupRunCount(t, chat.ID))
	})

	t.Run("BusyChatDoesNotCount", func(t *testing.T) {
		t.Parallel()
		f := newWorkerTestFixture(t)
		ctx := testutil.Context(t, testutil.WaitLong)
		chat := f.createWakeupChat(t)
		wakeup := f.insertWakeup(t, chat, intervalWakeup)
		worker := f.newArchiveWorker(t, nil, nil, nil)
		useUserPromptHook(t, worker, func(rw http.ResponseWriter) {
			_, err := f.sqlDB.ExecContext(ctx, "UPDATE chats SET status = 'running' WHERE id = $1", chat.ID)
			assert.NoError(t, err)
			_, _ = rw.Write([]byte(`{}`))
		})

		worker.wakeupOnce(ctx, wakeupTestSlot.Add(time.Second))
		require.Zero(t, f.countPromptMessages(t, chat, wakeup.Prompt))
		require.Zero(t, f.wakeupRunCount(t, chat.ID), "a delivery that rolled back does not count")
	})

	// The count commits with the rest of the admission or not at all.
	t.Run("RolledBackAdmissionDoesNotCount", func(t *testing.T) {
		t.Parallel()
		f := newWorkerTestFixture(t)
		chat := f.createWakeupChat(t)
		wakeup := f.insertWakeup(t, chat, intervalWakeup)
		delivery := wakeupDelivery{
			text:   wakeup.Prompt,
			runKey: wakeupRunKey(wakeup),
			finish: func(admittedAt time.Time) wakeupFinish { return nextWakeupRun(wakeup, admittedAt, 1) },
		}
		admit := func(rollback bool) error {
			return forkdb.InTx(f.db, func(_ database.Store, ftx forkdb.Store) error {
				ctx := testutil.Context(t, testutil.WaitShort)
				if _, err := admitWakeupDelivery(ctx, ftx, wakeup, delivery, wakeupTestSlot.Add(time.Second)); err != nil {
					return err
				}
				require.EqualValues(t, 1, f.wakeupRunCountIn(t, ftx, chat), "the count is written in the transaction")
				if rollback {
					return xerrors.New("roll back")
				}
				return nil
			}, nil)
		}
		require.Error(t, admit(true))
		require.Zero(t, f.wakeupRunCount(t, chat.ID))
		require.NoError(t, admit(false))
		require.EqualValues(t, 1, f.wakeupRunCount(t, chat.ID))
	})

	t.Run("OrganizationRunCapStopsThirdChat", func(t *testing.T) {
		t.Parallel()
		f := newWorkerTestFixture(t)
		ctx := testutil.Context(t, testutil.WaitLong)
		fdb, err := forkdb.From(f.db)
		require.NoError(t, err)
		_, err = fdb.UpsertChatOrganizationWakeupCaps(ctx, forkdb.UpsertChatOrganizationWakeupCapsParams{
			OrganizationID:            f.org.ID,
			MaxOrganizationRunsPerDay: sql.NullInt32{Int32: 2, Valid: true},
		})
		require.NoError(t, err)
		worker := f.newArchiveWorker(t, nil, nil, nil)
		useFakeEnqueuer(worker)

		var wakeups []forkdb.ChatWakeup
		for i := range 3 {
			chat := f.createWakeupChat(t)
			wakeup := f.insertWakeup(t, chat, intervalWakeup)
			wakeups = append(wakeups, wakeup)
			// One due wake-up per pass, so the passes deliver in order.
			worker.wakeupOnce(ctx, wakeupTestSlot.Add(time.Duration(i+1)*time.Second))
		}
		for _, wakeup := range wakeups[:2] {
			got := f.wakeup(t, wakeup.ID)
			require.Equal(t, forkdb.ChatWakeupStatusActive, got.Status)
			require.EqualValues(t, 1, got.RunCount)
		}
		got := f.wakeup(t, wakeups[2].ID)
		require.Equal(t, forkdb.ChatWakeupStatusPaused, got.Status)
		require.Equal(t, wakeupReasonOrgDailyRunCap, got.StatusReason.String)
		require.Zero(t, got.RunCount)
	})
}

// wakeupRunCount sums the counted wake-up runs of chat over all hours.
func (f *workerTestFixture) wakeupRunCount(t *testing.T, chatID uuid.UUID) int64 {
	t.Helper()
	var runs int64
	require.NoError(t, f.sqlDB.QueryRowContext(testutil.Context(t, testutil.WaitShort),
		"SELECT COALESCE(SUM(runs), 0) FROM chat_wakeup_run_counts WHERE chat_id = $1", chatID).Scan(&runs))
	return runs
}

// wakeupRunCountIn sums the counted wake-up runs of chat through fdb, which
// sees an open transaction.
func (*workerTestFixture) wakeupRunCountIn(t *testing.T, fdb forkdb.Store, chat database.Chat) int64 {
	t.Helper()
	runs, err := fdb.SumChatWakeupRunCountsSince(testutil.Context(t, testutil.WaitShort), forkdb.SumChatWakeupRunCountsSinceParams{
		ChatID:         chat.ID,
		OrganizationID: chat.OrganizationID,
		Since:          time.Time{},
	})
	require.NoError(t, err)
	return runs.ChatRuns
}
