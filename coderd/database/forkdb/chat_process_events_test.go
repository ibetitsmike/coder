package forkdb_test

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/testutil"
)

func TestFireChatProcessWatches(t *testing.T) {
	t.Parallel()

	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitShort)

	chat := wakeupTestChat(t, db)
	wakeup := insertEventsWakeup(ctx, t, fdb, chat)
	agentID, otherAgentID := uuid.New(), uuid.New()
	insertWatch := func(ordinal int32, agentID uuid.UUID, processID, spec string) forkdb.ChatWakeupWatch {
		t.Helper()
		watch, err := fdb.InsertChatWakeupWatch(ctx, forkdb.InsertChatWakeupWatchParams{
			ID:        uuid.New(),
			WakeupID:  wakeup.ID,
			Kind:      forkdb.ChatWakeupWatchKindProcess,
			Ordinal:   ordinal,
			AgentID:   uuid.NullUUID{UUID: agentID, Valid: true},
			ProcessID: sql.NullString{String: processID, Valid: true},
			Spec:      json.RawMessage(spec),
			CreatedAt: dbtime.Now(),
		})
		require.NoError(t, err)
		return watch
	}
	exitWatch := insertWatch(0, agentID, "p1", `{}`)
	lineWatch := insertWatch(1, agentID, "p2", `{"output_pattern": "ERR", "output_max_events": 2}`)
	otherAgent := insertWatch(2, otherAgentID, "p1", `{}`)
	get := func(id uuid.UUID) forkdb.ChatWakeupWatch {
		t.Helper()
		watches, err := fdb.ListChatWakeupWatchesByWakeupID(ctx, wakeup.ID)
		require.NoError(t, err)
		for _, watch := range watches {
			if watch.ID == id {
				return watch
			}
		}
		t.Fatalf("watch %s not found", id)
		return forkdb.ChatWakeupWatch{}
	}
	fire := func(arg forkdb.FireChatProcessWatchesParams) []uuid.UUID {
		t.Helper()
		arg.ChatID = chat.ID
		if arg.AgentID == uuid.Nil {
			arg.AgentID = agentID
		}
		chats, err := fdb.FireChatProcessWatches(ctx, arg)
		require.NoError(t, err)
		return chats
	}
	t1 := dbtime.Now()
	t2 := t1.Add(time.Minute)

	// Lines fire only a watch with an output pattern.
	require.Empty(t, fire(forkdb.FireChatProcessWatchesParams{ProcessID: "p1", FiredAt: t1, Matched: 1, Lines: "ERR"}))
	require.False(t, get(exitWatch.ID).FiredAt.Valid)

	// Lines add up until a delivery, and the first event keeps its time.
	require.Equal(t, []uuid.UUID{chat.ID}, fire(forkdb.FireChatProcessWatchesParams{ProcessID: "p2", FiredAt: t1, OutputAt: t1, Matched: 2, Lines: "ERR a\nERR b"}))
	first := get(lineWatch.ID)
	require.JSONEq(t, `{"matched": 2, "lines": "ERR a\nERR b", "last": false}`, string(first.FiredPayload.RawMessage))
	// A report that the agent sends again changes nothing.
	require.Empty(t, fire(forkdb.FireChatProcessWatchesParams{ProcessID: "p2", FiredAt: t1, OutputAt: t1, Matched: 2, Lines: "ERR a\nERR b"}))
	require.Equal(t, first.FiredPayload, get(lineWatch.ID).FiredPayload)
	require.Equal(t, []uuid.UUID{chat.ID}, fire(forkdb.FireChatProcessWatchesParams{ProcessID: "p2", FiredAt: t2, OutputAt: t2, Matched: 1, Lines: "ERR c", Last: true}))
	second := get(lineWatch.ID)
	require.JSONEq(t, `{"matched": 3, "lines": "ERR a\nERR b\nERR c", "last": true}`, string(second.FiredPayload.RawMessage))
	require.WithinDuration(t, t1, second.FiredAt.Time, time.Millisecond)

	// An event for another wake-up fires nothing.
	require.Empty(t, fire(forkdb.FireChatProcessWatchesParams{
		ProcessID: "p2", FiredAt: t2, OutputAt: t2.Add(time.Second), Matched: 1, Lines: "ERR d",
		WakeupID: uuid.NullUUID{UUID: uuid.New(), Valid: true},
	}))

	// A delivery of a payload that a later event changed loses.
	_, err := fdb.MarkChatWakeupWatchDelivered(ctx, forkdb.MarkChatWakeupWatchDeliveredParams{
		ID: lineWatch.ID, WakeupID: wakeup.ID, Deliveries: 0, Now: t2, SpecPatch: json.RawMessage(`{}`),
		FiredPayload: first.FiredPayload,
	})
	require.ErrorIs(t, err, sql.ErrNoRows)
	delivered, err := fdb.MarkChatWakeupWatchDelivered(ctx, forkdb.MarkChatWakeupWatchDeliveredParams{
		ID: lineWatch.ID, WakeupID: wakeup.ID, Deliveries: 0, Now: t2, SpecPatch: json.RawMessage(`{}`),
		FiredPayload: second.FiredPayload,
	})
	require.NoError(t, err)
	require.False(t, delivered.FiredAt.Valid)
	require.False(t, delivered.FiredPayload.Valid)
	// A report that the agent sends again after a delivery fires nothing,
	// so it does not count against output_max_events.
	require.Empty(t, fire(forkdb.FireChatProcessWatchesParams{ProcessID: "p2", FiredAt: t2, OutputAt: t2, Matched: 1, Lines: "ERR c", Last: true}))
	require.False(t, get(lineWatch.ID).FiredAt.Valid)

	// An exit fires the watches on its agent only.
	ended, err := forkdb.ChatProcessExitEnded(forkdb.ChatProcessExit{Code: 3, Tail: "bye\n", At: t2})
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{chat.ID}, fire(forkdb.FireChatProcessWatchesParams{ProcessID: "p1", FiredAt: t2, Ended: ended}))
	var fired forkdb.ChatProcessFired
	require.NoError(t, json.Unmarshal(get(exitWatch.ID).FiredPayload.RawMessage, &fired))
	require.NotNil(t, fired.Exit)
	require.Equal(t, 3, fired.Exit.Code)
	require.Equal(t, "bye\n", fired.Exit.Tail)
	require.False(t, get(otherAgent.ID).FiredAt.Valid)

	// Lines after the exit of a process fire nothing.
	require.Equal(t, []uuid.UUID{chat.ID}, fire(forkdb.FireChatProcessWatchesParams{ProcessID: "p2", FiredAt: t2, Ended: ended}))
	require.Empty(t, fire(forkdb.FireChatProcessWatchesParams{ProcessID: "p2", FiredAt: t2, OutputAt: t2.Add(2 * time.Second), Matched: 1, Lines: "ERR late"}))
	var afterExit forkdb.ChatProcessFired
	require.NoError(t, json.Unmarshal(get(lineWatch.ID).FiredPayload.RawMessage, &afterExit))
	require.NotNil(t, afterExit.Exit)
	require.Zero(t, afterExit.Matched)

	// The watches of a paused wake-up fire, since the agent drops what
	// coderd acked. Those of a wake-up that ended do not.
	_, err = sqlDB.ExecContext(ctx, "UPDATE chat_wakeups SET status = 'paused', next_run_at = NULL WHERE id = $1", wakeup.ID)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{chat.ID}, fire(forkdb.FireChatProcessWatchesParams{ProcessID: "p1", AgentID: otherAgentID, FiredAt: t2, Ended: ended}))
	require.True(t, get(otherAgent.ID).FiredAt.Valid)

	canceled := insertEventsWakeup(ctx, t, fdb, chat)
	canceledWatch, err := fdb.InsertChatWakeupWatch(ctx, forkdb.InsertChatWakeupWatchParams{
		ID:        uuid.New(),
		WakeupID:  canceled.ID,
		Kind:      forkdb.ChatWakeupWatchKindProcess,
		AgentID:   uuid.NullUUID{UUID: agentID, Valid: true},
		ProcessID: sql.NullString{String: "p3", Valid: true},
		Spec:      json.RawMessage(`{}`),
		CreatedAt: dbtime.Now(),
	})
	require.NoError(t, err)
	_, err = sqlDB.ExecContext(ctx, "UPDATE chat_wakeups SET status = 'canceled', next_run_at = NULL WHERE id = $1", canceled.ID)
	require.NoError(t, err)
	require.Empty(t, fire(forkdb.FireChatProcessWatchesParams{ProcessID: "p3", FiredAt: t2, Ended: ended}))
	watches, err := fdb.ListChatWakeupWatchesByWakeupID(ctx, canceled.ID)
	require.NoError(t, err)
	require.Len(t, watches, 1)
	require.Equal(t, canceledWatch.ID, watches[0].ID)
	require.False(t, watches[0].FiredAt.Valid)
}

func TestMarkChatProcessWatchesReported(t *testing.T) {
	t.Parallel()

	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitShort)

	chat := wakeupTestChat(t, db)
	active := insertEventsWakeup(ctx, t, fdb, chat)
	paused := insertEventsWakeup(ctx, t, fdb, chat)
	_, err := sqlDB.ExecContext(ctx, "UPDATE chat_wakeups SET status = 'paused', next_run_at = NULL WHERE id = $1", paused.ID)
	require.NoError(t, err)
	canceled := insertEventsWakeup(ctx, t, fdb, chat)
	_, err = sqlDB.ExecContext(ctx, "UPDATE chat_wakeups SET status = 'canceled', next_run_at = NULL WHERE id = $1", canceled.ID)
	require.NoError(t, err)
	agentID, otherAgentID := uuid.New(), uuid.New()
	insertWatch := func(wakeupID uuid.UUID, ordinal int32, agentID uuid.UUID) forkdb.ChatWakeupWatch {
		t.Helper()
		watch, err := fdb.InsertChatWakeupWatch(ctx, forkdb.InsertChatWakeupWatchParams{
			ID:        uuid.New(),
			WakeupID:  wakeupID,
			Kind:      forkdb.ChatWakeupWatchKindProcess,
			Ordinal:   ordinal,
			AgentID:   uuid.NullUUID{UUID: agentID, Valid: true},
			ProcessID: sql.NullString{String: "p", Valid: true},
			Spec:      json.RawMessage(`{"output_pattern": "ERR"}`),
			CreatedAt: dbtime.Now(),
		})
		require.NoError(t, err)
		return watch
	}
	open := insertWatch(active.ID, 0, agentID)
	exited := insertWatch(active.ID, 1, agentID)
	delivered := insertWatch(active.ID, 2, agentID)
	otherAgent := insertWatch(active.ID, 3, otherAgentID)
	pausedWatch := insertWatch(paused.ID, 0, agentID)
	inactive := insertWatch(canceled.ID, 0, agentID)
	_, err = sqlDB.ExecContext(ctx, `UPDATE chat_wakeup_watches SET fired_at = now(), fired_payload = '{"exit": {"code": 0}}' WHERE id = $1`, exited.ID)
	require.NoError(t, err)
	_, err = sqlDB.ExecContext(ctx, "UPDATE chat_wakeup_watches SET delivered_at = now() WHERE id = $1", delivered.ID)
	require.NoError(t, err)

	reportedAt := dbtime.Now()
	n, err := fdb.MarkChatProcessWatchesReported(ctx, forkdb.MarkChatProcessWatchesReportedParams{AgentID: agentID, ReportedAt: reportedAt})
	require.NoError(t, err)
	require.EqualValues(t, 2, n)

	specs := make(map[uuid.UUID]map[string]any)
	for _, wakeupID := range []uuid.UUID{active.ID, paused.ID, canceled.ID} {
		watches, err := fdb.ListChatWakeupWatchesByWakeupID(ctx, wakeupID)
		require.NoError(t, err)
		for _, watch := range watches {
			var spec map[string]any
			require.NoError(t, json.Unmarshal(watch.Spec, &spec))
			specs[watch.ID] = spec
		}
	}
	require.Equal(t, "ERR", specs[open.ID]["output_pattern"])
	for _, id := range []uuid.UUID{open.ID, pausedWatch.ID} {
		got, err := time.Parse(time.RFC3339Nano, specs[id]["reported_at"].(string))
		require.NoError(t, err)
		require.WithinDuration(t, reportedAt, got, time.Millisecond)
	}
	for _, id := range []uuid.UUID{exited.ID, delivered.ID, otherAgent.ID, inactive.ID} {
		require.NotContains(t, specs[id], "reported_at")
	}
}
