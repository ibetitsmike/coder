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
	require.Equal(t, []uuid.UUID{chat.ID}, fire(forkdb.FireChatProcessWatchesParams{ProcessID: "p2", FiredAt: t1, Matched: 2, Lines: "ERR a\nERR b"}))
	first := get(lineWatch.ID)
	require.JSONEq(t, `{"matched": 2, "lines": "ERR a\nERR b", "last": false}`, string(first.FiredPayload.RawMessage))
	require.Equal(t, []uuid.UUID{chat.ID}, fire(forkdb.FireChatProcessWatchesParams{ProcessID: "p2", FiredAt: t2, Matched: 1, Lines: "ERR c", Last: true}))
	second := get(lineWatch.ID)
	require.JSONEq(t, `{"matched": 3, "lines": "ERR a\nERR b\nERR c", "last": true}`, string(second.FiredPayload.RawMessage))
	require.WithinDuration(t, t1, second.FiredAt.Time, time.Millisecond)

	// An event for another wake-up fires nothing.
	require.Empty(t, fire(forkdb.FireChatProcessWatchesParams{
		ProcessID: "p2", FiredAt: t2, Matched: 1, Lines: "ERR d",
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
	require.Empty(t, fire(forkdb.FireChatProcessWatchesParams{ProcessID: "p2", FiredAt: t2, Matched: 1, Lines: "ERR late"}))
	var afterExit forkdb.ChatProcessFired
	require.NoError(t, json.Unmarshal(get(lineWatch.ID).FiredPayload.RawMessage, &afterExit))
	require.NotNil(t, afterExit.Exit)
	require.Zero(t, afterExit.Matched)

	// The watches of a wake-up that is not active do not fire.
	_, err = sqlDB.ExecContext(ctx, "UPDATE chat_wakeups SET status = 'paused', next_run_at = NULL WHERE id = $1", wakeup.ID)
	require.NoError(t, err)
	require.Empty(t, fire(forkdb.FireChatProcessWatchesParams{ProcessID: "p1", AgentID: otherAgentID, FiredAt: t2, Ended: ended}))
	require.False(t, get(otherAgent.ID).FiredAt.Valid)
}
