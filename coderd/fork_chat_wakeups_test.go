package coderd_test

// This file is fork code: it covers the chat wake-up routes of the
// fork-chat-wakeups experiment.

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/audit"
	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/serpent"
)

// newForkChatWakeupsAPI starts coderd with the fork-chat-wakeups experiment
// enabled and the chat worker stopped, so no worker claims a test wake-up. It
// returns the unwrapped store for fixtures.
func newForkChatWakeupsAPI(t *testing.T, enabled bool, auditor audit.Auditor) (*codersdk.Client, database.Store) {
	t.Helper()

	db, ps := dbtestutil.NewDB(t)
	values := coderdtest.DeploymentValues(t)
	if enabled {
		values.Experiments = serpent.StringArray{string(codersdk.ExperimentForkChatWakeups)}
	}
	client, _, _ := coderdtest.NewWithAPI(t, &coderdtest.Options{
		DeploymentValues:   values,
		Database:           db,
		Pubsub:             ps,
		Auditor:            auditor,
		ChatWorkerDisabled: true,
	})
	return client, db
}

// insertForkChatWakeup stores an active hourly wake-up of chat, changed by
// mutate, the way the chat daemon does.
func insertForkChatWakeup(t *testing.T, db database.Store, chat database.Chat, mutate func(*forkdb.InsertChatWakeupParams)) forkdb.ChatWakeup {
	t.Helper()

	now := dbtime.Now()
	params := forkdb.InsertChatWakeupParams{
		ID:              uuid.New(),
		ChatID:          chat.ID,
		CreatedBy:       chat.OwnerID,
		Source:          "schedule_wakeup",
		Kind:            forkdb.ChatWakeupKindInterval,
		Prompt:          "check in",
		IntervalSeconds: sql.NullInt32{Int32: 3600, Valid: true},
		Timezone:        "UTC",
		Events:          json.RawMessage(`[]`),
		Status:          forkdb.ChatWakeupStatusActive,
		NextRunAt:       sql.NullTime{Time: now.Add(time.Hour), Valid: true},
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if mutate != nil {
		mutate(&params)
	}
	fdb, err := forkdb.From(db)
	require.NoError(t, err)
	wakeup, err := fdb.InsertChatWakeup(testutil.Context(t, testutil.WaitShort), params)
	require.NoError(t, err)
	return wakeup
}

func forkChatWakeupPath(chatID, wakeupID uuid.UUID) string {
	return "/api/v2/chats/fork/" + chatID.String() + "/wakeups/" + wakeupID.String()
}

// TestForkChatWakeupsExperimentOff checks that the wake-up routes stay closed
// while the experiment is disabled.
func TestForkChatWakeupsExperimentOff(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	client, db := newForkChatWakeupsAPI(t, false, nil)
	user := coderdtest.CreateFirstUser(t, client)
	chat := newForkChatTodosChat(t, db, user)
	wakeup := insertForkChatWakeup(t, db, chat, nil)

	_, err := client.ChatWakeups(ctx, chat.ID)
	requireForkChatWakeupStatus(t, err, http.StatusForbidden)
	_, err = client.UpdateChatWakeup(ctx, chat.ID, wakeup.ID, codersdk.UpdateChatWakeupRequest{Status: codersdk.ChatWakeupStatusPaused})
	requireForkChatWakeupStatus(t, err, http.StatusForbidden)
}

// TestForkChatWakeups checks that the list returns every wake-up of the chat
// in creation order, with the events of an events wake-up decoded.
func TestForkChatWakeups(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	client, db := newForkChatWakeupsAPI(t, true, nil)
	user := coderdtest.CreateFirstUser(t, client)
	chat := newForkChatTodosChat(t, db, user)

	empty, err := client.ChatWakeups(ctx, chat.ID)
	require.NoError(t, err)
	require.Empty(t, empty)

	interval := insertForkChatWakeup(t, db, chat, nil)
	child := uuid.New()
	events := insertForkChatWakeup(t, db, chat, func(p *forkdb.InsertChatWakeupParams) {
		p.Source = "wait_for_events"
		p.Kind = forkdb.ChatWakeupKindEvents
		p.IntervalSeconds = sql.NullInt32{}
		p.Events = json.RawMessage(`[{"type":"child_done","chat_id":"` + child.String() + `"},` +
			`{"type":"pull_request","url":"https://github.com/coder/coder/pull/1","on":["review"],"head_sha":"abc"}]`)
		p.CreatedAt = p.CreatedAt.Add(time.Second)
	})
	// A wake-up of another chat stays out of the list.
	insertForkChatWakeup(t, db, newForkChatTodosChat(t, db, user), nil)

	got, err := client.ChatWakeups(ctx, chat.ID)
	require.NoError(t, err)
	require.Len(t, got, 2)

	require.Equal(t, interval.ID, got[0].ID)
	require.Equal(t, codersdk.ChatWakeupKindInterval, got[0].Kind)
	require.Equal(t, codersdk.ChatWakeupStatusActive, got[0].Status)
	require.NotNil(t, got[0].IntervalSeconds)
	require.EqualValues(t, 3600, *got[0].IntervalSeconds)
	require.NotNil(t, got[0].NextRunAt)
	require.True(t, interval.NextRunAt.Time.Equal(*got[0].NextRunAt))
	require.NotNil(t, got[0].Events)
	require.Empty(t, got[0].Events)

	require.Equal(t, events.ID, got[1].ID)
	require.Equal(t, codersdk.ChatWakeupKindEvents, got[1].Kind)
	require.Nil(t, got[1].IntervalSeconds)
	require.Equal(t, []codersdk.ChatWakeupEvent{
		{Type: "child_done", ChatID: &child},
		{Type: "pull_request", URL: "https://github.com/coder/coder/pull/1", On: []string{"review"}},
	}, got[1].Events)
}

// TestForkChatWakeupStatus checks every status change of a wake-up through
// the API.
func TestForkChatWakeupStatus(t *testing.T) {
	t.Parallel()

	client, db := newForkChatWakeupsAPI(t, true, nil)
	user := coderdtest.CreateFirstUser(t, client)
	chat := newForkChatTodosChat(t, db, user)

	paused := func(reason string, nextRunAt sql.NullTime) func(*forkdb.InsertChatWakeupParams) {
		return func(p *forkdb.InsertChatWakeupParams) {
			p.Status = forkdb.ChatWakeupStatusPaused
			p.StatusReason = sql.NullString{String: reason, Valid: true}
			p.NextRunAt = nextRunAt
		}
	}
	finished := func(status forkdb.ChatWakeupStatus) func(*forkdb.InsertChatWakeupParams) {
		return func(p *forkdb.InsertChatWakeupParams) {
			p.Status = status
			p.StatusReason = sql.NullString{String: "done", Valid: true}
			p.NextRunAt = sql.NullTime{}
		}
	}
	inHour := sql.NullTime{Time: dbtime.Now().Add(time.Hour), Valid: true}

	for _, tc := range []struct {
		name   string
		seed   func(*forkdb.InsertChatWakeupParams)
		status codersdk.ChatWakeupStatus
		// check runs on the returned wake-up; start is the time before the
		// request.
		check      func(t *testing.T, seeded forkdb.ChatWakeup, got codersdk.ChatWakeup, start time.Time)
		wantStatus int
	}{
		{
			name:   "PauseKeepsRunTime",
			status: codersdk.ChatWakeupStatusPaused,
			check: func(t *testing.T, seeded forkdb.ChatWakeup, got codersdk.ChatWakeup, _ time.Time) {
				require.Equal(t, codersdk.ChatWakeupStatusPaused, got.Status)
				require.Equal(t, "paused_by_user", got.StatusReason)
				require.NotNil(t, got.NextRunAt)
				require.True(t, seeded.NextRunAt.Time.Equal(*got.NextRunAt))
			},
		},
		{
			name:   "PausePausedChangesNothing",
			seed:   paused("daily_run_cap", sql.NullTime{}),
			status: codersdk.ChatWakeupStatusPaused,
			check: func(t *testing.T, seeded forkdb.ChatWakeup, got codersdk.ChatWakeup, _ time.Time) {
				require.Equal(t, codersdk.ChatWakeupStatusPaused, got.Status)
				require.Equal(t, "daily_run_cap", got.StatusReason)
				require.True(t, seeded.UpdatedAt.Equal(got.UpdatedAt))
			},
		},
		{
			name:   "ResumeKeepsRunTimeAhead",
			seed:   paused("paused_by_user", inHour),
			status: codersdk.ChatWakeupStatusActive,
			check: func(t *testing.T, seeded forkdb.ChatWakeup, got codersdk.ChatWakeup, _ time.Time) {
				require.Equal(t, codersdk.ChatWakeupStatusActive, got.Status)
				require.Empty(t, got.StatusReason)
				require.NotNil(t, got.NextRunAt)
				require.True(t, seeded.NextRunAt.Time.Equal(*got.NextRunAt))
			},
		},
		{
			// The worker pauses a capped wake-up without a run time.
			name:   "ResumeAfterCapSchedulesFromNow",
			seed:   paused("daily_run_cap", sql.NullTime{}),
			status: codersdk.ChatWakeupStatusActive,
			check: func(t *testing.T, _ forkdb.ChatWakeup, got codersdk.ChatWakeup, start time.Time) {
				require.Equal(t, codersdk.ChatWakeupStatusActive, got.Status)
				require.NotNil(t, got.NextRunAt)
				require.WithinDuration(t, start.Add(time.Hour), *got.NextRunAt, testutil.WaitShort)
			},
		},
		{
			name: "ResumeOverdueDelayRunsNow",
			seed: func(p *forkdb.InsertChatWakeupParams) {
				p.Kind = forkdb.ChatWakeupKindDelay
				p.IntervalSeconds = sql.NullInt32{}
				paused("paused_by_user", sql.NullTime{Time: dbtime.Now().Add(-time.Hour), Valid: true})(p)
			},
			status: codersdk.ChatWakeupStatusActive,
			check: func(t *testing.T, _ forkdb.ChatWakeup, got codersdk.ChatWakeup, start time.Time) {
				require.Equal(t, codersdk.ChatWakeupStatusActive, got.Status)
				require.NotNil(t, got.NextRunAt)
				require.WithinDuration(t, start, *got.NextRunAt, testutil.WaitShort)
			},
		},
		{
			name: "ResumeExpiredCompletes",
			seed: func(p *forkdb.InsertChatWakeupParams) {
				paused("daily_run_cap", sql.NullTime{})(p)
				p.ExpiresAt = sql.NullTime{Time: dbtime.Now().Add(-time.Minute), Valid: true}
			},
			status: codersdk.ChatWakeupStatusActive,
			check: func(t *testing.T, _ forkdb.ChatWakeup, got codersdk.ChatWakeup, _ time.Time) {
				require.Equal(t, codersdk.ChatWakeupStatusCompleted, got.Status)
				require.Equal(t, "expired", got.StatusReason)
				require.Nil(t, got.NextRunAt)
			},
		},
		{
			name:   "CancelActive",
			status: codersdk.ChatWakeupStatusCanceled,
			check: func(t *testing.T, _ forkdb.ChatWakeup, got codersdk.ChatWakeup, _ time.Time) {
				require.Equal(t, codersdk.ChatWakeupStatusCanceled, got.Status)
				require.Equal(t, "canceled_by_user", got.StatusReason)
				require.Nil(t, got.NextRunAt)
			},
		},
		{
			name:   "CancelPaused",
			seed:   paused("paused_by_user", inHour),
			status: codersdk.ChatWakeupStatusCanceled,
			check: func(t *testing.T, _ forkdb.ChatWakeup, got codersdk.ChatWakeup, _ time.Time) {
				require.Equal(t, codersdk.ChatWakeupStatusCanceled, got.Status)
				require.Nil(t, got.NextRunAt)
			},
		},
		{
			name:   "CancelCanceledChangesNothing",
			seed:   finished(forkdb.ChatWakeupStatusCanceled),
			status: codersdk.ChatWakeupStatusCanceled,
			check: func(t *testing.T, _ forkdb.ChatWakeup, got codersdk.ChatWakeup, _ time.Time) {
				require.Equal(t, codersdk.ChatWakeupStatusCanceled, got.Status)
				require.Equal(t, "done", got.StatusReason)
			},
		},
		{
			name:       "ResumeCompletedConflicts",
			seed:       finished(forkdb.ChatWakeupStatusCompleted),
			status:     codersdk.ChatWakeupStatusActive,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "PauseCanceledConflicts",
			seed:       finished(forkdb.ChatWakeupStatusCanceled),
			status:     codersdk.ChatWakeupStatusPaused,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "CompletedIsNotAControl",
			status:     codersdk.ChatWakeupStatusCompleted,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "UnknownStatus",
			status:     "sleeping",
			wantStatus: http.StatusBadRequest,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := testutil.Context(t, testutil.WaitLong)
			seeded := insertForkChatWakeup(t, db, chat, tc.seed)
			start := time.Now()
			got, err := client.UpdateChatWakeup(ctx, chat.ID, seeded.ID, codersdk.UpdateChatWakeupRequest{Status: tc.status})
			if tc.wantStatus != 0 {
				requireForkChatWakeupStatus(t, err, tc.wantStatus)
				return
			}
			require.NoError(t, err)
			require.Equal(t, seeded.ID, got.ID)
			tc.check(t, seeded, got, start)

			// The response is the stored wake-up.
			listed, err := client.ChatWakeups(ctx, chat.ID)
			require.NoError(t, err)
			found := false
			for _, wakeup := range listed {
				if wakeup.ID == got.ID {
					require.Equal(t, got, wakeup)
					found = true
				}
			}
			require.True(t, found)
		})
	}
}

// TestForkChatWakeupStatusRefused checks the changes the API refuses without
// touching the wake-up.
func TestForkChatWakeupStatusRefused(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	client, db := newForkChatWakeupsAPI(t, true, nil)
	user := coderdtest.CreateFirstUser(t, client)
	chat := newForkChatTodosChat(t, db, user)
	wakeup := insertForkChatWakeup(t, db, chat, nil)
	pause := codersdk.UpdateChatWakeupRequest{Status: codersdk.ChatWakeupStatusPaused}

	// The wake-up of another chat is not found through this one.
	other := newForkChatTodosChat(t, db, user)
	_, err := client.UpdateChatWakeup(ctx, other.ID, wakeup.ID, pause)
	requireForkChatWakeupStatus(t, err, http.StatusNotFound)
	_, err = client.UpdateChatWakeup(ctx, chat.ID, uuid.New(), pause)
	requireForkChatWakeupStatus(t, err, http.StatusNotFound)

	// A member that cannot read the chat cannot see or change its wake-ups.
	member, _ := coderdtest.CreateAnotherUser(t, client, user.OrganizationID)
	_, err = member.ChatWakeups(ctx, chat.ID)
	requireForkChatWakeupStatus(t, err, http.StatusNotFound)
	_, err = member.UpdateChatWakeup(ctx, chat.ID, wakeup.ID, pause)
	requireForkChatWakeupStatus(t, err, http.StatusNotFound)

	archived := newForkChatTodosChat(t, db, user)
	archivedWakeup := insertForkChatWakeup(t, db, archived, nil)
	_, err = db.ArchiveChatByID(ctx, archived.ID)
	require.NoError(t, err)
	_, err = client.UpdateChatWakeup(ctx, archived.ID, archivedWakeup.ID, pause)
	requireForkChatWakeupStatus(t, err, http.StatusBadRequest)

	for _, c := range []struct {
		chatID uuid.UUID
		id     uuid.UUID
	}{{chat.ID, wakeup.ID}, {archived.ID, archivedWakeup.ID}} {
		listed, err := client.ChatWakeups(ctx, c.chatID)
		require.NoError(t, err)
		require.Len(t, listed, 1)
		require.Equal(t, c.id, listed[0].ID)
		require.Equal(t, codersdk.ChatWakeupStatusActive, listed[0].Status)
	}
}

// TestForkChatWakeupAudit checks that every change request is audited as one
// write of the chat that names the wake-up and its statuses.
func TestForkChatWakeupAudit(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	auditor := audit.NewMock()
	client, db := newForkChatWakeupsAPI(t, true, auditor)
	user := coderdtest.CreateFirstUser(t, client)
	chat := newForkChatTodosChat(t, db, user)
	wakeup := insertForkChatWakeup(t, db, chat, nil)

	_, err := client.UpdateChatWakeup(ctx, chat.ID, wakeup.ID, codersdk.UpdateChatWakeupRequest{Status: codersdk.ChatWakeupStatusCanceled})
	require.NoError(t, err)
	_, err = client.UpdateChatWakeup(ctx, chat.ID, wakeup.ID, codersdk.UpdateChatWakeupRequest{Status: codersdk.ChatWakeupStatusActive})
	requireForkChatWakeupStatus(t, err, http.StatusConflict)
	// Listing is a read and records nothing.
	_, err = client.ChatWakeups(ctx, chat.ID)
	require.NoError(t, err)

	var entries []database.AuditLog
	for _, entry := range auditor.AuditLogs() {
		if entry.ResourceID == chat.ID {
			entries = append(entries, entry)
		}
	}
	require.Len(t, entries, 2)
	for i, want := range []struct {
		statusCode int32
		fields     string
	}{
		{http.StatusOK, `{"wakeup_id":"` + wakeup.ID.String() + `","old_status":"active","new_status":"canceled"}`},
		{http.StatusConflict, `{"wakeup_id":"` + wakeup.ID.String() + `","new_status":"active"}`},
	} {
		require.Equal(t, database.ResourceTypeChat, entries[i].ResourceType)
		require.Equal(t, database.AuditActionWrite, entries[i].Action)
		require.Equal(t, user.UserID, entries[i].UserID)
		require.Equal(t, user.OrganizationID, entries[i].OrganizationID)
		require.Equal(t, want.statusCode, entries[i].StatusCode)
		require.JSONEq(t, want.fields, string(entries[i].AdditionalFields))
	}
}

func requireForkChatWakeupStatus(t *testing.T, err error, status int) {
	t.Helper()

	sdkErr, ok := codersdk.AsError(err)
	require.True(t, ok, "expected a codersdk error, got %v", err)
	require.Equal(t, status, sdkErr.StatusCode())
}
