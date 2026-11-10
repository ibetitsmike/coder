package chatd

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/notifications"
	"github.com/coder/coder/v2/coderd/notifications/notificationstest"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/quartz"
	"github.com/coder/serpent"
)

func TestWakeupCaps(t *testing.T) {
	t.Parallel()
	now := wakeupTestSlot.Add(time.Second)
	recent := wakeupTestSlot.Add(-time.Hour)
	old := now.Add(-wakeupCapWindow - time.Minute)
	cost := func(micros int64) sql.NullInt64 { return sql.NullInt64{Int64: micros, Valid: true} }

	for _, tc := range []struct {
		name string
		seed func(t *testing.T, f *workerTestFixture, chat database.Chat, wakeup forkdb.ChatWakeup)
		// wantReason is the pause reason, or "" when the wake-up delivers.
		wantReason string
	}{
		{
			name: "ChatRunsAtCap",
			seed: func(t *testing.T, f *workerTestFixture, _ database.Chat, wakeup forkdb.ChatWakeup) {
				f.seedDeliveredRuns(t, wakeup, maxChatWakeupRunsPerDay, recent)
			},
			wantReason: wakeupReasonDailyRunCap,
		},
		{
			name: "ChatRunsBelowCap",
			seed: func(t *testing.T, f *workerTestFixture, _ database.Chat, wakeup forkdb.ChatWakeup) {
				f.seedDeliveredRuns(t, wakeup, maxChatWakeupRunsPerDay-1, recent)
				f.seedDeliveredRuns(t, wakeup, 5, old)
			},
		},
		{
			name: "OrganizationRunsAtCap",
			seed: func(t *testing.T, f *workerTestFixture, _ database.Chat, _ forkdb.ChatWakeup) {
				f.seedDeliveredRuns(t, f.insertDoneWakeup(t, f.createWakeupChat(t)), maxOrganizationWakeupRunsPerDay, recent)
			},
			wantReason: wakeupReasonOrgDailyRunCap,
		},
		{
			name: "ChatSpendAtCap",
			seed: func(t *testing.T, f *workerTestFixture, chat database.Chat, _ forkdb.ChatWakeup) {
				f.insertCostMessage(t, chat, cost(maxChatWakeupSpendMicrosPerDay), recent)
			},
			wantReason: wakeupReasonDailySpendCap,
		},
		{
			name: "ChatSpendBelowCap",
			seed: func(t *testing.T, f *workerTestFixture, chat database.Chat, _ forkdb.ChatWakeup) {
				f.insertCostMessage(t, chat, cost(maxChatWakeupSpendMicrosPerDay-1), recent)
				f.insertCostMessage(t, chat, sql.NullInt64{}, recent)
				f.insertCostMessage(t, chat, cost(maxChatWakeupSpendMicrosPerDay), old)
			},
		},
		{
			name: "OrganizationSpendAtCap",
			seed: func(t *testing.T, f *workerTestFixture, _ database.Chat, _ forkdb.ChatWakeup) {
				other := f.createWakeupChat(t)
				f.insertDoneWakeup(t, other)
				f.insertCostMessage(t, other, cost(maxOrganizationWakeupSpendMicrosPerDay), recent)
			},
			wantReason: wakeupReasonOrgDailySpendCap,
		},
		{
			name: "OrganizationSpendOfHumanChatsIgnored",
			seed: func(t *testing.T, f *workerTestFixture, _ database.Chat, _ forkdb.ChatWakeup) {
				f.insertCostMessage(t, f.createWakeupChat(t), cost(maxOrganizationWakeupSpendMicrosPerDay), recent)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newWorkerTestFixture(t)
			ctx := testutil.Context(t, testutil.WaitLong)
			chat := f.createWakeupChat(t)
			wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
				Kind:            forkdb.ChatWakeupKindInterval,
				IntervalSeconds: sql.NullInt32{Int32: 60, Valid: true},
			})
			tc.seed(t, f, chat, wakeup)
			runsBefore := len(f.wakeupRunStatuses(t, wakeup.ID))
			worker := f.newArchiveWorker(t, nil, nil, nil)
			enqueuer := useFakeEnqueuer(worker)

			worker.wakeupOnce(ctx, now)
			got := f.wakeup(t, wakeup.ID)
			sent := enqueuer.Sent(notificationstest.WithTemplateID(notifications.TemplateCustomNotification))
			if tc.wantReason == "" {
				require.Equal(t, 1, f.countPromptMessages(t, chat, wakeup.Prompt))
				require.Equal(t, forkdb.ChatWakeupStatusActive, got.Status)
				require.Empty(t, sent)
				return
			}
			require.Zero(t, f.countPromptMessages(t, chat, wakeup.Prompt))
			require.Len(t, f.wakeupRunStatuses(t, wakeup.ID), runsBefore, "a capped wake-up records no run")
			require.Equal(t, forkdb.ChatWakeupStatusPaused, got.Status)
			require.Equal(t, tc.wantReason, got.StatusReason.String)
			require.False(t, got.NextRunAt.Valid)
			require.False(t, got.LeaseExpiresAt.Valid)
			require.Zero(t, got.RunCount)
			requireWakeupPausedNotification(t, sent, chat, tc.wantReason)
		})
	}

	t.Run("BusyChatAtCapStaysDue", func(t *testing.T) {
		t.Parallel()
		f := newWorkerTestFixture(t)
		ctx := testutil.Context(t, testutil.WaitLong)
		chat := f.createWakeupChat(t)
		forceExecutionState(t, f, chat.ID, database.ChatStatusRunning, false)
		wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
			Kind:            forkdb.ChatWakeupKindInterval,
			IntervalSeconds: sql.NullInt32{Int32: 60, Valid: true},
		})
		f.seedDeliveredRuns(t, wakeup, maxChatWakeupRunsPerDay, recent)
		worker := f.newArchiveWorker(t, nil, nil, nil)
		enqueuer := useFakeEnqueuer(worker)

		worker.wakeupOnce(ctx, now)
		got := f.wakeup(t, wakeup.ID)
		require.Equal(t, forkdb.ChatWakeupStatusActive, got.Status)
		require.True(t, wakeupTestSlot.Equal(got.NextRunAt.Time), "still due: %s", got.NextRunAt.Time)
		require.Empty(t, enqueuer.Sent())
	})

	// The enqueuer dedupes identical payloads per day, so several wake-ups of
	// a capped chat notify its owner once.
	t.Run("WakeupsOfCappedChatNotifyOnce", func(t *testing.T) {
		t.Parallel()
		f := newWorkerTestFixture(t)
		ctx := testutil.Context(t, testutil.WaitLong)
		chat := f.createWakeupChat(t)
		params := forkdb.InsertChatWakeupParams{
			Kind:            forkdb.ChatWakeupKindInterval,
			IntervalSeconds: sql.NullInt32{Int32: 60, Valid: true},
		}
		first := f.insertWakeup(t, chat, params)
		second := f.insertWakeup(t, chat, params)
		f.seedDeliveredRuns(t, first, maxChatWakeupRunsPerDay, recent)
		worker := f.newArchiveWorker(t, nil, nil, nil)
		useStoreEnqueuer(t, f, worker.server, quartz.NewReal())

		worker.wakeupOnce(ctx, now)
		for _, wakeup := range []forkdb.ChatWakeup{first, second} {
			got := f.wakeup(t, wakeup.ID)
			require.Equal(t, forkdb.ChatWakeupStatusPaused, got.Status)
			require.Equal(t, wakeupReasonDailyRunCap, got.StatusReason.String)
		}
		var rows int
		require.NoError(t, f.sqlDB.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM notification_messages WHERE user_id = $1 AND notification_template_id = $2",
			chat.OwnerID, notifications.TemplateCustomNotification).Scan(&rows))
		require.Equal(t, 2, rows, "one notification, stored once per delivery method")
	})
}

// requireWakeupPausedNotification asserts that sent is exactly one pause
// notification of chat for reason.
func requireWakeupPausedNotification(t *testing.T, sent []*notificationstest.FakeNotification, chat database.Chat, reason string) {
	t.Helper()
	require.Len(t, sent, 1)
	require.Equal(t, chat.OwnerID, sent[0].UserID)
	require.Equal(t, map[string]string{
		"custom_title":   wakeupPausedNotificationTitle,
		"custom_message": `A wake-up of chat "` + chat.Title + `" is paused: ` + wakeupPauseReasonText[reason] + ".",
	}, sent[0].Labels)
	require.Equal(t, map[string]any{"chat_id": chat.ID, "reason": reason}, sent[0].Data)
}

// useFakeEnqueuer routes the notifications of the server of worker to a fake
// enqueuer.
func useFakeEnqueuer(worker *chatWorker) *notificationstest.FakeEnqueuer {
	enqueuer := notificationstest.NewFakeEnqueuer()
	worker.server.chatWorker.opts.NotificationsEnqueuer = enqueuer
	return enqueuer
}

// useStoreEnqueuer routes the notifications of server to a real enqueuer that
// stores an email and an inbox message per notification, stamped by clock.
func useStoreEnqueuer(t *testing.T, f *workerTestFixture, server *Server, clock quartz.Clock) {
	t.Helper()
	enqueuer, err := notifications.NewStoreEnqueuer(codersdk.NotificationsConfig{
		Method: serpent.String(database.NotificationMethodSmtp),
		SMTP:   codersdk.NotificationsEmailConfig{Smarthost: serpent.String("localhost:1337")},
		Inbox:  codersdk.NotificationsInboxConfig{Enabled: serpent.Bool(true)},
	}, f.db, map[string]any{
		"base_url":     func() string { return "http://test.com" },
		"current_year": func() string { return "2026" },
		"logo_url":     func() string { return "https://coder.com/coder-logo-horizontal.png" },
		"app_name":     func() string { return "Coder" },
	}, testutil.Logger(t), clock)
	require.NoError(t, err)
	server.chatWorker.opts.NotificationsEnqueuer = enqueuer
}

// seedDeliveredRuns inserts count delivered runs of wakeup created at
// createdAt, with run keys that no real occurrence uses.
func (f *workerTestFixture) seedDeliveredRuns(t *testing.T, wakeup forkdb.ChatWakeup, count int, createdAt time.Time) {
	t.Helper()
	_, err := f.sqlDB.ExecContext(testutil.Context(t, testutil.WaitShort), `
		INSERT INTO chat_wakeup_runs (id, wakeup_id, chat_id, organization_id, run_key, status, created_at, updated_at)
		SELECT gen_random_uuid(), w.id, w.chat_id, w.organization_id, 'seed:' || gen_random_uuid()::text, 'delivered', $2, $2
		FROM chat_wakeups w, generate_series(1, $3::int)
		WHERE w.id = $1`, wakeup.ID, createdAt, count)
	require.NoError(t, err)
}

// insertDoneWakeup inserts a completed wake-up of chat, which makes chat a
// wake-up chat of its organization without ever being due.
func (f *workerTestFixture) insertDoneWakeup(t *testing.T, chat database.Chat) forkdb.ChatWakeup {
	t.Helper()
	wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{Kind: forkdb.ChatWakeupKindDelay})
	_, err := f.sqlDB.ExecContext(testutil.Context(t, testutil.WaitShort),
		"UPDATE chat_wakeups SET status = 'completed', next_run_at = NULL WHERE id = $1", wakeup.ID)
	require.NoError(t, err)
	return wakeup
}

// insertCostMessage inserts an assistant message of chat that cost
// costMicros at createdAt.
func (f *workerTestFixture) insertCostMessage(t *testing.T, chat database.Chat, costMicros sql.NullInt64, createdAt time.Time) {
	t.Helper()
	msg := dbgen.ChatMessage(t, f.db, database.ChatMessage{ChatID: chat.ID, Role: database.ChatMessageRoleAssistant})
	_, err := f.sqlDB.ExecContext(testutil.Context(t, testutil.WaitShort),
		"UPDATE chat_messages SET total_cost_micros = $1, created_at = $2 WHERE id = $3", costMicros, createdAt, msg.ID)
	require.NoError(t, err)
}
