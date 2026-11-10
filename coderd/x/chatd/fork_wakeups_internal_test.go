package chatd

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"cdr.dev/slog/v3/sloggers/slogtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/agenthooks/dispatch"
	"github.com/coder/coder/v2/coderd/x/chatd/chathooks"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprompt"
	"github.com/coder/coder/v2/coderd/x/chatd/chatstate"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/quartz"
)

var wakeupTestSlot = time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)

func TestWakeupDeliversIntervalWithoutReplay(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	chat := f.createWakeupChat(t)
	wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
		Kind:            forkdb.ChatWakeupKindInterval,
		IntervalSeconds: sql.NullInt32{Int32: 60, Valid: true},
	})
	worker := f.newArchiveWorker(t, nil, nil, nil)

	worker.wakeupOnce(ctx, wakeupTestSlot.Add(10*time.Second))
	require.Equal(t, 1, f.countPromptMessages(t, chat, wakeup.Prompt))
	got := f.wakeup(t, wakeup.ID)
	require.Equal(t, forkdb.ChatWakeupStatusActive, got.Status)
	require.EqualValues(t, 1, got.RunCount)
	require.True(t, wakeupTestSlot.Add(time.Minute).Equal(got.NextRunAt.Time), "on schedule: %s", got.NextRunAt.Time)
	require.False(t, got.LeaseExpiresAt.Valid)
	require.Equal(t, []string{"delivered"}, f.wakeupRunStatuses(t, wakeup.ID))

	// The turn ends, then the server is down for three and a half slots.
	forceExecutionState(t, f, chat.ID, database.ChatStatusWaiting, false)
	late := wakeupTestSlot.Add(time.Minute + 210*time.Second)
	worker.wakeupOnce(ctx, late)
	require.Equal(t, 2, f.countPromptMessages(t, chat, wakeup.Prompt), "missed slots fire once")
	got = f.wakeup(t, wakeup.ID)
	require.EqualValues(t, 2, got.RunCount)
	// The worker's real clock adds the time spent since the claim.
	require.WithinDuration(t, late.Add(time.Minute), got.NextRunAt.Time, time.Second, "advance from now")
}

func TestWakeupSkipsBusyChat(t *testing.T) {
	t.Parallel()
	for _, status := range []database.ChatStatus{
		database.ChatStatusRunning,
		database.ChatStatusInterrupting,
		database.ChatStatusRequiresAction,
	} {
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()
			f := newWorkerTestFixture(t)
			ctx := testutil.Context(t, testutil.WaitLong)
			chat := f.createWakeupChat(t)
			forceExecutionState(t, f, chat.ID, status, false)
			wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
				Kind:            forkdb.ChatWakeupKindInterval,
				IntervalSeconds: sql.NullInt32{Int32: 60, Valid: true},
			})
			worker := f.newArchiveWorker(t, nil, nil, nil)

			worker.wakeupOnce(ctx, wakeupTestSlot.Add(5*time.Second))
			require.Zero(t, f.countPromptMessages(t, chat, wakeup.Prompt))
			queued, err := f.db.GetChatQueuedMessages(ctx, chat.ID)
			require.NoError(t, err)
			require.Empty(t, queued, "a wake-up never queues behind a busy chat")
			require.Empty(t, f.wakeupRunStatuses(t, wakeup.ID))
			got := f.wakeup(t, wakeup.ID)
			require.Equal(t, forkdb.ChatWakeupStatusActive, got.Status)
			require.True(t, wakeupTestSlot.Equal(got.NextRunAt.Time), "still due: %s", got.NextRunAt.Time)
			require.False(t, got.LeaseExpiresAt.Valid, "the next tick can claim it")
			require.Zero(t, got.RunCount)

			forceExecutionState(t, f, chat.ID, database.ChatStatusWaiting, false)
			worker.wakeupOnce(ctx, wakeupTestSlot.Add(20*time.Second))
			require.Equal(t, 1, f.countPromptMessages(t, chat, wakeup.Prompt))
			require.Equal(t, []string{"delivered"}, f.wakeupRunStatuses(t, wakeup.ID))
		})
	}
}

func TestWakeupConcurrentWorkersDeliverOnce(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	chat := f.createWakeupChat(t)
	wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
		Kind:            forkdb.ChatWakeupKindInterval,
		IntervalSeconds: sql.NullInt32{Int32: 60, Valid: true},
	})
	workers := []*chatWorker{
		f.newArchiveWorker(t, nil, nil, nil),
		f.newArchiveWorker(t, nil, nil, nil),
	}

	now := wakeupTestSlot.Add(time.Second)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, worker := range workers {
		wg.Go(func() {
			<-start
			worker.wakeupOnce(ctx, now)
		})
	}
	close(start)
	wg.Wait()

	require.Equal(t, 1, f.countPromptMessages(t, chat, wakeup.Prompt))
	// The first delivery starts a turn, so a duplicate send would queue.
	queued, err := f.db.GetChatQueuedMessages(ctx, chat.ID)
	require.NoError(t, err)
	require.Empty(t, queued)
	require.Equal(t, []string{"delivered"}, f.wakeupRunStatuses(t, wakeup.ID))
	require.EqualValues(t, 1, f.wakeup(t, wakeup.ID).RunCount)
}

// A replica that sent an occurrence and died before it ended its claim leaves
// the run row behind; the replica that takes over must not send it again.
func TestWakeupOccurrenceDeliversOnceAfterTakeover(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	chat := f.createWakeupChat(t)
	wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
		Kind:            forkdb.ChatWakeupKindInterval,
		IntervalSeconds: sql.NullInt32{Int32: 60, Valid: true},
	})
	worker := f.newArchiveWorker(t, nil, nil, nil)
	worker.wakeupOnce(ctx, wakeupTestSlot.Add(time.Second))
	require.Equal(t, 1, f.countPromptMessages(t, chat, wakeup.Prompt))

	_, err := f.sqlDB.ExecContext(ctx, "UPDATE chat_wakeups SET next_run_at = $1, run_count = 0 WHERE id = $2", wakeupTestSlot, wakeup.ID)
	require.NoError(t, err)
	forceExecutionState(t, f, chat.ID, database.ChatStatusWaiting, false)
	late := wakeupTestSlot.Add(5 * time.Minute)
	worker.wakeupOnce(ctx, late)

	require.Equal(t, 1, f.countPromptMessages(t, chat, wakeup.Prompt))
	require.Equal(t, []string{"delivered"}, f.wakeupRunStatuses(t, wakeup.ID))
	got := f.wakeup(t, wakeup.ID)
	require.Zero(t, got.RunCount)
	require.WithinDuration(t, late.Add(time.Minute), got.NextRunAt.Time, time.Second, "the schedule still advances")
}

func TestWakeupEndsSchedule(t *testing.T) {
	t.Parallel()

	t.Run("ArchivedChat", func(t *testing.T) {
		t.Parallel()
		f := newWorkerTestFixture(t)
		ctx := testutil.Context(t, testutil.WaitLong)
		chat := f.createWakeupChat(t)
		wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
			Kind:            forkdb.ChatWakeupKindInterval,
			IntervalSeconds: sql.NullInt32{Int32: 60, Valid: true},
		})
		forceExecutionState(t, f, chat.ID, database.ChatStatusWaiting, true)

		f.newArchiveWorker(t, nil, nil, nil).wakeupOnce(ctx, wakeupTestSlot.Add(time.Second))
		require.Zero(t, f.countPromptMessages(t, chat, wakeup.Prompt))
		require.Empty(t, f.wakeupRunStatuses(t, wakeup.ID))
		got := f.wakeup(t, wakeup.ID)
		require.Equal(t, forkdb.ChatWakeupStatusCanceled, got.Status)
		require.Equal(t, "archived", got.StatusReason.String)
		require.False(t, got.NextRunAt.Valid)
	})

	t.Run("Expired", func(t *testing.T) {
		t.Parallel()
		f := newWorkerTestFixture(t)
		ctx := testutil.Context(t, testutil.WaitLong)
		chat := f.createWakeupChat(t)
		wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
			Kind:            forkdb.ChatWakeupKindInterval,
			IntervalSeconds: sql.NullInt32{Int32: 60, Valid: true},
			ExpiresAt:       sql.NullTime{Time: wakeupTestSlot, Valid: true},
		})

		f.newArchiveWorker(t, nil, nil, nil).wakeupOnce(ctx, wakeupTestSlot.Add(time.Second))
		require.Zero(t, f.countPromptMessages(t, chat, wakeup.Prompt))
		require.Empty(t, f.wakeupRunStatuses(t, wakeup.ID))
		got := f.wakeup(t, wakeup.ID)
		require.Equal(t, forkdb.ChatWakeupStatusCompleted, got.Status)
		require.Equal(t, "expired", got.StatusReason.String)
		require.False(t, got.NextRunAt.Valid)
	})

	t.Run("HookDenied", func(t *testing.T) {
		t.Parallel()
		f := newWorkerTestFixture(t)
		ctx := testutil.Context(t, testutil.WaitLong)
		chat := f.createWakeupChat(t)
		wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
			Kind:            forkdb.ChatWakeupKindInterval,
			IntervalSeconds: sql.NullInt32{Int32: 60, Valid: true},
		})
		worker := f.newArchiveWorker(t, nil, nil, nil)
		useUserPromptHook(t, worker, func(rw http.ResponseWriter) {
			_, _ = rw.Write([]byte(`{"permission": {"decision": "deny", "reason": "blocked"}, "user_message": "not allowed"}`))
		})

		worker.wakeupOnce(ctx, wakeupTestSlot.Add(time.Second))
		require.Zero(t, f.countPromptMessages(t, chat, wakeup.Prompt))
		require.Equal(t, []string{"failed"}, f.wakeupRunStatuses(t, wakeup.ID))
		got := f.wakeup(t, wakeup.ID)
		require.Equal(t, forkdb.ChatWakeupStatusPaused, got.Status, "a denied prompt must not retry every interval")
		require.Equal(t, "hook_denied", got.StatusReason.String)
		require.False(t, got.NextRunAt.Valid)
		require.Zero(t, got.RunCount)
	})

	t.Run("Delay", func(t *testing.T) {
		t.Parallel()
		f := newWorkerTestFixture(t)
		ctx := testutil.Context(t, testutil.WaitLong)
		chat := f.createWakeupChat(t)
		wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{Kind: forkdb.ChatWakeupKindDelay})

		f.newArchiveWorker(t, nil, nil, nil).wakeupOnce(ctx, wakeupTestSlot.Add(time.Second))
		require.Equal(t, 1, f.countPromptMessages(t, chat, wakeup.Prompt))
		got := f.wakeup(t, wakeup.ID)
		require.Equal(t, forkdb.ChatWakeupStatusCompleted, got.Status)
		require.Equal(t, "done", got.StatusReason.String)
		require.EqualValues(t, 1, got.RunCount)
		require.False(t, got.NextRunAt.Valid)
	})

	t.Run("MaxRuns", func(t *testing.T) {
		t.Parallel()
		f := newWorkerTestFixture(t)
		ctx := testutil.Context(t, testutil.WaitLong)
		chat := f.createWakeupChat(t)
		wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
			Kind:            forkdb.ChatWakeupKindInterval,
			IntervalSeconds: sql.NullInt32{Int32: 60, Valid: true},
			MaxRuns:         sql.NullInt32{Int32: 2, Valid: true},
		})
		worker := f.newArchiveWorker(t, nil, nil, nil)

		worker.wakeupOnce(ctx, wakeupTestSlot.Add(time.Second))
		require.Equal(t, forkdb.ChatWakeupStatusActive, f.wakeup(t, wakeup.ID).Status)
		forceExecutionState(t, f, chat.ID, database.ChatStatusWaiting, false)
		worker.wakeupOnce(ctx, wakeupTestSlot.Add(time.Minute+time.Second))

		require.Equal(t, 2, f.countPromptMessages(t, chat, wakeup.Prompt))
		got := f.wakeup(t, wakeup.ID)
		require.Equal(t, forkdb.ChatWakeupStatusCompleted, got.Status)
		require.Equal(t, "max_runs", got.StatusReason.String)
		require.EqualValues(t, 2, got.RunCount)
		require.False(t, got.NextRunAt.Valid)
	})
}

// The user prompt hook runs after the worker saw an idle chat and before the
// send takes the chat lock, so it can change the world in between.
func TestWakeupAdmissionRaces(t *testing.T) {
	t.Parallel()

	t.Run("ChatTurnedBusy", func(t *testing.T) {
		t.Parallel()
		f := newWorkerTestFixture(t)
		ctx := testutil.Context(t, testutil.WaitLong)
		chat := f.createWakeupChat(t)
		wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
			Kind:            forkdb.ChatWakeupKindInterval,
			IntervalSeconds: sql.NullInt32{Int32: 60, Valid: true},
		})
		worker := f.newArchiveWorker(t, nil, nil, nil)
		useUserPromptHook(t, worker, func(rw http.ResponseWriter) {
			_, err := f.sqlDB.ExecContext(ctx, "UPDATE chats SET status = 'running' WHERE id = $1", chat.ID)
			assert.NoError(t, err)
			_, _ = rw.Write([]byte(`{}`))
		})

		worker.wakeupOnce(ctx, wakeupTestSlot.Add(time.Second))
		require.Zero(t, f.countPromptMessages(t, chat, wakeup.Prompt))
		queued, err := f.db.GetChatQueuedMessages(ctx, chat.ID)
		require.NoError(t, err)
		require.Empty(t, queued, "a wake-up never queues behind a busy chat")
		require.Empty(t, f.wakeupRunStatuses(t, wakeup.ID))
		got := f.wakeup(t, wakeup.ID)
		require.Equal(t, forkdb.ChatWakeupStatusActive, got.Status)
		require.True(t, wakeupTestSlot.Equal(got.NextRunAt.Time), "still due: %s", got.NextRunAt.Time)
		require.False(t, got.LeaseExpiresAt.Valid)
	})

	t.Run("ClaimCanceled", func(t *testing.T) {
		t.Parallel()
		f := newWorkerTestFixture(t)
		ctx := testutil.Context(t, testutil.WaitLong)
		chat := f.createWakeupChat(t)
		wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
			Kind:            forkdb.ChatWakeupKindInterval,
			IntervalSeconds: sql.NullInt32{Int32: 60, Valid: true},
		})
		worker := f.newArchiveWorker(t, nil, nil, nil)
		useUserPromptHook(t, worker, func(rw http.ResponseWriter) {
			// What UpdateChatWakeupStatus does when the user pauses it.
			_, err := f.sqlDB.ExecContext(ctx, "UPDATE chat_wakeups SET status = 'paused', next_run_at = NULL, lease_expires_at = NULL WHERE id = $1", wakeup.ID)
			assert.NoError(t, err)
			_, _ = rw.Write([]byte(`{}`))
		})

		worker.wakeupOnce(ctx, wakeupTestSlot.Add(time.Second))
		require.Zero(t, f.countPromptMessages(t, chat, wakeup.Prompt), "a canceled claim delivers nothing")
		require.Empty(t, f.wakeupRunStatuses(t, wakeup.ID))
		got := f.wakeup(t, wakeup.ID)
		require.Equal(t, forkdb.ChatWakeupStatusPaused, got.Status)
		require.Zero(t, got.RunCount)
	})

	t.Run("HookDispatchFailed", func(t *testing.T) {
		t.Parallel()
		f := newWorkerTestFixture(t)
		ctx := testutil.Context(t, testutil.WaitLong)
		chat := f.createWakeupChat(t)
		wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
			Kind:            forkdb.ChatWakeupKindInterval,
			IntervalSeconds: sql.NullInt32{Int32: 60, Valid: true},
		})
		worker := f.newArchiveWorker(t, nil, nil, nil)
		useUserPromptHook(t, worker, func(rw http.ResponseWriter) {
			rw.WriteHeader(http.StatusInternalServerError)
		})

		worker.wakeupOnce(ctx, wakeupTestSlot.Add(time.Second))
		require.Zero(t, f.countPromptMessages(t, chat, wakeup.Prompt))
		require.Equal(t, []string{"failed"}, f.wakeupRunStatuses(t, wakeup.ID))
		got := f.wakeup(t, wakeup.ID)
		require.Equal(t, forkdb.ChatWakeupStatusActive, got.Status, "a hook outage must not pause the schedule")
		require.True(t, wakeupTestSlot.Add(time.Minute).Equal(got.NextRunAt.Time), "next slot: %s", got.NextRunAt.Time)
		require.Zero(t, got.RunCount)
	})

	t.Run("QueueFilled", func(t *testing.T) {
		t.Parallel()
		f := newWorkerTestFixture(t)
		ctx := testutil.Context(t, testutil.WaitLong)
		chat := f.createWakeupChat(t)
		wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
			Kind:            forkdb.ChatWakeupKindInterval,
			IntervalSeconds: sql.NullInt32{Int32: 60, Valid: true},
		})
		worker := f.newArchiveWorker(t, nil, nil, nil)
		worker.server.chatLimits.MaxQueuedMessagesPerChat = 1
		content, err := chatprompt.MarshalParts([]codersdk.ChatMessagePart{codersdk.ChatMessageText("queued")})
		require.NoError(t, err)
		useUserPromptHook(t, worker, func(rw http.ResponseWriter) {
			_, err := f.sqlDB.ExecContext(ctx, "UPDATE chats SET status = 'running' WHERE id = $1", chat.ID)
			assert.NoError(t, err)
			_, err = f.db.InsertChatQueuedMessage(ctx, database.InsertChatQueuedMessageParams{
				ChatID:        chat.ID,
				Content:       content.RawMessage,
				ModelConfigID: uuid.NullUUID{UUID: f.model.ID, Valid: true},
			})
			assert.NoError(t, err)
			_, _ = rw.Write([]byte(`{}`))
		})

		worker.wakeupOnce(ctx, wakeupTestSlot.Add(time.Second))
		require.Zero(t, f.countPromptMessages(t, chat, wakeup.Prompt))
		require.Empty(t, f.wakeupRunStatuses(t, wakeup.ID), "a full queue keeps the occurrence")
		got := f.wakeup(t, wakeup.ID)
		require.Equal(t, forkdb.ChatWakeupStatusActive, got.Status)
		require.True(t, wakeupTestSlot.Equal(got.NextRunAt.Time), "still due: %s", got.NextRunAt.Time)
		require.False(t, got.LeaseExpiresAt.Valid)
	})

	t.Run("LeaseTakenOverThenFailed", func(t *testing.T) {
		t.Parallel()
		f := newWorkerTestFixture(t)
		ctx := testutil.Context(t, testutil.WaitLong)
		chat := f.createWakeupChat(t)
		wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
			Kind:            forkdb.ChatWakeupKindInterval,
			IntervalSeconds: sql.NullInt32{Int32: 60, Valid: true},
		})
		worker := f.newArchiveWorker(t, nil, nil, nil)
		otherLease := wakeupTestSlot.Add(10 * time.Minute)
		useUserPromptHook(t, worker, func(rw http.ResponseWriter) {
			// Another replica claims the row after this worker's lease ran out.
			_, err := f.sqlDB.ExecContext(ctx, "UPDATE chat_wakeups SET lease_expires_at = $2 WHERE id = $1", wakeup.ID, otherLease)
			assert.NoError(t, err)
			rw.WriteHeader(http.StatusInternalServerError)
		})

		worker.wakeupOnce(ctx, wakeupTestSlot.Add(time.Second))
		require.Empty(t, f.wakeupRunStatuses(t, wakeup.ID), "the stale worker records no run for the new owner's occurrence")
		got := f.wakeup(t, wakeup.ID)
		require.True(t, otherLease.Equal(got.LeaseExpiresAt.Time), "the new owner keeps its lease: %s", got.LeaseExpiresAt.Time)
		require.True(t, wakeupTestSlot.Equal(got.NextRunAt.Time), "still due: %s", got.NextRunAt.Time)
	})
}

// Busy chats are not claimed, so a full batch of them cannot starve a due
// wake-up of an idle chat.
func TestWakeupBusyChatsDoNotStarveIdleChats(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	for range wakeupClaimBatch {
		busy := f.createWakeupChat(t)
		forceExecutionState(t, f, busy.ID, database.ChatStatusRunning, false)
		f.insertWakeup(t, busy, forkdb.InsertChatWakeupParams{
			Kind:            forkdb.ChatWakeupKindInterval,
			IntervalSeconds: sql.NullInt32{Int32: 60, Valid: true},
		})
	}
	idle := f.createWakeupChat(t)
	wakeup := f.insertWakeup(t, idle, forkdb.InsertChatWakeupParams{
		Kind:            forkdb.ChatWakeupKindInterval,
		IntervalSeconds: sql.NullInt32{Int32: 60, Valid: true},
	})
	_, err := f.sqlDB.ExecContext(ctx, "UPDATE chat_wakeups SET next_run_at = $2 WHERE id = $1", wakeup.ID, wakeupTestSlot.Add(time.Second))
	require.NoError(t, err)
	worker := f.newArchiveWorker(t, nil, nil, nil)

	worker.wakeupOnce(ctx, wakeupTestSlot.Add(5*time.Second))
	require.Equal(t, 1, f.countPromptMessages(t, idle, wakeup.Prompt))
}

// The started worker runs the wake-up loop when the experiment is enabled:
// once at start, then on every poll tick.
func TestWakeupLoopRunsInStartedWorker(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	mClock := quartz.NewMock(t)
	mClock.Set(wakeupTestSlot.Add(time.Second))
	first := f.createWakeupChat(t)
	firstWakeup := f.insertWakeup(t, first, forkdb.InsertChatWakeupParams{Kind: forkdb.ChatWakeupKindDelay})

	opts := testOptions(t, f, nil)
	opts.Clock = mClock
	server := newUnstartedServer(t, f.pubsub, f.db)
	server.experiments = codersdk.Experiments{codersdk.ExperimentForkChatWakeups}
	worker, err := newChatWorker(server, opts)
	require.NoError(t, err)
	tickerTrap := mClock.Trap().NewTicker("chatworker", "wakeups")
	defer tickerTrap.Close()
	require.NoError(t, worker.Start(context.Background()))
	t.Cleanup(func() { require.NoError(t, worker.Close()) })

	// The loop runs the first pass before it creates its ticker.
	tickerTrap.MustWait(ctx).MustRelease(ctx)
	require.Equal(t, 1, f.countPromptMessages(t, first, firstWakeup.Prompt))

	second := f.createWakeupChat(t)
	secondWakeup := f.insertWakeup(t, second, forkdb.InsertChatWakeupParams{Kind: forkdb.ChatWakeupKindDelay})
	mClock.Advance(wakeupPollInterval).MustWait(ctx)
	testutil.Eventually(ctx, t, func(context.Context) bool {
		return f.countPromptMessages(t, second, secondWakeup.Prompt) == 1
	}, testutil.IntervalFast, "the poll tick must deliver the second wake-up")
}

func TestNextWakeupRun(t *testing.T) {
	t.Parallel()
	slot := wakeupTestSlot
	interval := func(mutate func(*forkdb.ChatWakeup)) forkdb.ChatWakeup {
		w := forkdb.ChatWakeup{
			Kind:            forkdb.ChatWakeupKindInterval,
			IntervalSeconds: sql.NullInt32{Int32: 60, Valid: true},
			NextRunAt:       sql.NullTime{Time: slot, Valid: true},
			Timezone:        "UTC",
		}
		if mutate != nil {
			mutate(&w)
		}
		return w
	}
	tests := []struct {
		name       string
		wakeup     forkdb.ChatWakeup
		now        time.Time
		wantStatus forkdb.ChatWakeupStatus
		wantReason string
		wantNext   time.Time
	}{
		{
			name:       "IntervalOnTime",
			wakeup:     interval(nil),
			now:        slot.Add(10 * time.Second),
			wantStatus: forkdb.ChatWakeupStatusActive,
			wantNext:   slot.Add(time.Minute),
		},
		{
			name:       "IntervalMissedSlots",
			wakeup:     interval(nil),
			now:        slot.Add(210 * time.Second),
			wantStatus: forkdb.ChatWakeupStatusActive,
			wantNext:   slot.Add(270 * time.Second),
		},
		{
			name: "CronInTimezone",
			wakeup: forkdb.ChatWakeup{
				Kind:     forkdb.ChatWakeupKindCron,
				Cron:     sql.NullString{String: "0 9 * * *", Valid: true},
				Timezone: "America/New_York",
			},
			// 10:00 in New York, so 09:00 today has passed.
			now:        time.Date(2026, 5, 29, 14, 0, 0, 0, time.UTC),
			wantStatus: forkdb.ChatWakeupStatusActive,
			wantNext:   time.Date(2026, 5, 30, 13, 0, 0, 0, time.UTC),
		},
		{
			name: "InvalidCron",
			wakeup: forkdb.ChatWakeup{
				Kind:     forkdb.ChatWakeupKindCron,
				Cron:     sql.NullString{String: "not a schedule", Valid: true},
				Timezone: "UTC",
			},
			now:        slot,
			wantStatus: forkdb.ChatWakeupStatusPaused,
			wantReason: "invalid_cron",
		},
		{
			name: "NextRunAfterExpiry",
			wakeup: interval(func(w *forkdb.ChatWakeup) {
				w.ExpiresAt = sql.NullTime{Time: slot.Add(30 * time.Second), Valid: true}
			}),
			now:        slot.Add(10 * time.Second),
			wantStatus: forkdb.ChatWakeupStatusCompleted,
			wantReason: "expired",
		},
		{
			name: "MaxRunsReached",
			wakeup: interval(func(w *forkdb.ChatWakeup) {
				w.MaxRuns = sql.NullInt32{Int32: 2, Valid: true}
				w.RunCount = 1
			}),
			now:        slot.Add(10 * time.Second),
			wantStatus: forkdb.ChatWakeupStatusCompleted,
			wantReason: "max_runs",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := nextWakeupRun(tc.wakeup, tc.now, 1)
			require.Equal(t, tc.wantStatus, got.status)
			require.Equal(t, tc.wantReason, got.reason)
			require.EqualValues(t, 1, got.runDelta)
			if tc.wantNext.IsZero() {
				require.False(t, got.nextRunAt.Valid)
				return
			}
			require.True(t, tc.wantNext.Equal(got.nextRunAt.Time), "next run %s, want %s", got.nextRunAt.Time, tc.wantNext)
		})
	}
}

// useUserPromptHook routes the worker's user prompt hook to respond.
func useUserPromptHook(t *testing.T, worker *chatWorker, respond func(rw http.ResponseWriter)) {
	t.Helper()
	consumer := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		respond(rw)
	}))
	t.Cleanup(consumer.Close)
	worker.server.hooks = chathooks.NewTrigger(dispatch.New(
		slogtest.Make(t, &slogtest.Options{IgnoreErrors: true}),
		consumer.Client(),
		consumer.URL,
		false,
		"test-hook-secret-32-bytes-minimum!!",
		time.Second,
		"test-deployment",
		"test-version",
		prometheus.NewRegistry(),
	))
}

func (f *workerTestFixture) createWakeupChat(t *testing.T) database.Chat {
	t.Helper()
	res, err := chatstate.CreateChat(testutil.Context(t, testutil.WaitShort), f.db, f.pubsub, chatstate.CreateChatInput{
		OrganizationID:    f.org.ID,
		OwnerID:           f.user.ID,
		LastModelConfigID: f.model.ID,
		Title:             "wake-up",
		ClientType:        database.ChatClientTypeApi,
		InitialStatus:     database.ChatStatusWaiting,
		InitialMessages: []chatstate.Message{
			userTextMessage(t, "hello", f.user.ID, f.model.ID),
		},
	})
	require.NoError(t, err)
	return res.Chat
}

// insertWakeup inserts a wake-up of chat that is due at wakeupTestSlot. arg
// sets the schedule.
func (f *workerTestFixture) insertWakeup(t *testing.T, chat database.Chat, arg forkdb.InsertChatWakeupParams) forkdb.ChatWakeup {
	t.Helper()
	fdb, err := forkdb.From(f.db)
	require.NoError(t, err)
	arg.ID = uuid.New()
	arg.ChatID = chat.ID
	arg.CreatedBy = chat.OwnerID
	arg.Source = "schedule_wakeup"
	arg.Prompt = "wake up " + arg.ID.String()
	arg.Timezone = "UTC"
	arg.Events = json.RawMessage(`[]`)
	arg.Status = forkdb.ChatWakeupStatusActive
	arg.NextRunAt = sql.NullTime{Time: wakeupTestSlot, Valid: true}
	arg.CreatedAt = wakeupTestSlot.Add(-time.Hour)
	arg.UpdatedAt = arg.CreatedAt
	wakeup, err := fdb.InsertChatWakeup(testutil.Context(t, testutil.WaitShort), arg)
	require.NoError(t, err)
	return wakeup
}

func (f *workerTestFixture) wakeup(t *testing.T, id uuid.UUID) forkdb.ChatWakeup {
	t.Helper()
	fdb, err := forkdb.From(f.db)
	require.NoError(t, err)
	wakeup, err := fdb.GetChatWakeupByID(testutil.Context(t, testutil.WaitShort), id)
	require.NoError(t, err)
	return wakeup
}

func (f *workerTestFixture) wakeupRunStatuses(t *testing.T, wakeupID uuid.UUID) []string {
	t.Helper()
	rows, err := f.sqlDB.QueryContext(testutil.Context(t, testutil.WaitShort),
		"SELECT status FROM chat_wakeup_runs WHERE wakeup_id = $1 ORDER BY created_at, id", wakeupID)
	require.NoError(t, err)
	defer rows.Close()
	var statuses []string
	for rows.Next() {
		var status string
		require.NoError(t, rows.Scan(&status))
		statuses = append(statuses, status)
	}
	require.NoError(t, rows.Err())
	return statuses
}

// countPromptMessages counts the user messages from the chat owner whose text
// is prompt.
func (f *workerTestFixture) countPromptMessages(t *testing.T, chat database.Chat, prompt string) int {
	t.Helper()
	messages, err := f.db.GetChatMessagesByChatID(testutil.Context(t, testutil.WaitShort), database.GetChatMessagesByChatIDParams{ChatID: chat.ID})
	require.NoError(t, err)
	count := 0
	for _, msg := range messages {
		if msg.Role != database.ChatMessageRoleUser || msg.CreatedBy.UUID != chat.OwnerID {
			continue
		}
		parts, err := chatprompt.ParseContent(msg)
		require.NoError(t, err)
		var text strings.Builder
		for _, part := range parts {
			text.WriteString(part.Text)
		}
		if text.String() == prompt {
			count++
		}
	}
	return count
}
