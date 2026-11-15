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

func TestListReadyChatWakeupWatches(t *testing.T) {
	t.Parallel()

	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitLong)

	parent := wakeupTestChat(t, db)
	children := make([]database.Chat, 3)
	for i := range children {
		children[i] = dbgen.Chat(t, db, database.Chat{
			OrganizationID:    parent.OrganizationID,
			OwnerID:           parent.OwnerID,
			LastModelConfigID: parent.LastModelConfigID,
			ParentChatID:      uuid.NullUUID{UUID: parent.ID, Valid: true},
			RootChatID:        uuid.NullUUID{UUID: parent.ID, Valid: true},
			Status:            database.ChatStatusRunning,
		})
	}
	wakeup := insertEventsWakeup(ctx, t, fdb, parent)
	watches := make([]forkdb.ChatWakeupWatch, len(children))
	for i, child := range children {
		watches[i] = insertChildWatch(ctx, t, fdb, wakeup, int32(i), child) //nolint:gosec // i < 3
		require.Equal(t, parent.ID, watches[i].ChatID, "the watch wakes the wake-up's chat")
		require.Equal(t, parent.OrganizationID, watches[i].OrganizationID)
	}

	setStatus := func(chat database.Chat, status database.ChatStatus) {
		t.Helper()
		_, err := sqlDB.ExecContext(ctx, "UPDATE chats SET status = $1 WHERE id = $2", status, chat.ID)
		require.NoError(t, err)
	}
	ready := func() []uuid.UUID {
		t.Helper()
		rows, err := fdb.ListReadyChatWakeupWatches(ctx, forkdb.ListReadyChatWakeupWatchesParams{LimitCount: 100})
		require.NoError(t, err)
		ids := make([]uuid.UUID, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		return ids
	}

	require.Empty(t, ready(), "no child is done while all run")

	setStatus(children[0], database.ChatStatusWaiting)
	require.Equal(t, []uuid.UUID{watches[0].ID}, ready(), "exactly the finished child is ready")

	setStatus(parent, database.ChatStatusRunning)
	require.Empty(t, ready(), "nothing is ready while the chat to wake is running")
	setStatus(parent, database.ChatStatusWaiting)

	// The finished child waits for a process of its own.
	own := insertEventsWakeup(ctx, t, fdb, children[0])
	ownWatch, err := fdb.InsertChatWakeupWatch(ctx, forkdb.InsertChatWakeupWatchParams{
		ID:        uuid.New(),
		WakeupID:  own.ID,
		Kind:      forkdb.ChatWakeupWatchKindProcess,
		ProcessID: sql.NullString{String: "proc-1", Valid: true},
		Spec:      json.RawMessage(`{"command": "make test"}`),
		CreatedAt: dbtime.Now(),
	})
	require.NoError(t, err)
	require.Empty(t, ready(), "a child with an open watch of its own is not done")

	// An observed event of the child's own watch is ready for the child.
	_, err = sqlDB.ExecContext(ctx, "UPDATE chat_wakeup_watches SET fired_at = now() WHERE id = $1", ownWatch.ID)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{ownWatch.ID}, ready(), "a fired watch is ready, and its chat still waits")
	_, err = fdb.MarkChatWakeupWatchDelivered(ctx, forkdb.MarkChatWakeupWatchDeliveredParams{
		ID: ownWatch.ID, WakeupID: own.ID, Done: true, Now: dbtime.Now(), SpecPatch: json.RawMessage(`{}`),
	})
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{watches[0].ID}, ready(), "a delivered watch no longer keeps its chat waiting")

	// The second child waits for an answer to its question.
	setStatus(children[1], database.ChatStatusWaiting)
	_, err = fdb.UpsertChatDescendantQuestion(ctx, forkdb.UpsertChatDescendantQuestionParams{
		ChatID:     children[1].ID,
		ToolCallID: "ask-1",
		Questions:  json.RawMessage(`[{"header": "DB"}]`),
	})
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{watches[0].ID}, ready(), "a child with an unanswered question is not done")
	_, err = fdb.RetireChatDescendantQuestion(ctx, forkdb.RetireChatDescendantQuestionParams{ChatID: children[1].ID, Visible: true})
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{watches[0].ID, watches[1].ID}, ready(), "an answered child is done, in watch order")

	// An archived child is done even while it runs.
	_, err = sqlDB.ExecContext(ctx, "UPDATE chats SET archived = true WHERE id = $1", children[2].ID)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{watches[0].ID, watches[1].ID, watches[2].ID}, ready())

	// The filters narrow the list.
	rows, err := fdb.ListReadyChatWakeupWatches(ctx, forkdb.ListReadyChatWakeupWatchesParams{
		WakeupID: uuid.NullUUID{UUID: own.ID, Valid: true}, LimitCount: 100,
	})
	require.NoError(t, err)
	require.Empty(t, rows)
	rows, err = fdb.ListReadyChatWakeupWatches(ctx, forkdb.ListReadyChatWakeupWatchesParams{
		ChatID: uuid.NullUUID{UUID: parent.ID, Valid: true}, WakeupID: uuid.NullUUID{UUID: wakeup.ID, Valid: true}, LimitCount: 1,
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, watches[0].ID, rows[0].ID)

	// A delivery records itself once: a stale count matches no row.
	delivered, err := fdb.MarkChatWakeupWatchDelivered(ctx, forkdb.MarkChatWakeupWatchDeliveredParams{
		ID: watches[0].ID, WakeupID: wakeup.ID, Done: true, Now: dbtime.Now(), SpecPatch: json.RawMessage(`{"x": 1}`),
	})
	require.NoError(t, err)
	require.True(t, delivered.DeliveredAt.Valid)
	require.EqualValues(t, 1, delivered.Deliveries)
	require.JSONEq(t, `{"x": 1}`, string(delivered.Spec))
	_, err = fdb.MarkChatWakeupWatchDelivered(ctx, forkdb.MarkChatWakeupWatchDeliveredParams{
		ID: watches[0].ID, WakeupID: wakeup.ID, Done: true, Now: dbtime.Now(), SpecPatch: json.RawMessage(`{}`),
	})
	require.ErrorIs(t, err, sql.ErrNoRows, "a watch delivers once")
	require.Equal(t, []uuid.UUID{watches[1].ID, watches[2].ID}, ready())

	// A paused wake-up delivers nothing.
	_, err = sqlDB.ExecContext(ctx, "UPDATE chat_wakeups SET status = 'paused', next_run_at = NULL WHERE id = $1", wakeup.ID)
	require.NoError(t, err)
	require.Empty(t, ready())
}

func TestChatWakeupWatchRows(t *testing.T) {
	t.Parallel()

	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitShort)

	parent := wakeupTestChat(t, db)
	child := dbgen.Chat(t, db, database.Chat{
		OrganizationID:    parent.OrganizationID,
		OwnerID:           parent.OwnerID,
		LastModelConfigID: parent.LastModelConfigID,
		ParentChatID:      uuid.NullUUID{UUID: parent.ID, Valid: true},
		RootChatID:        uuid.NullUUID{UUID: parent.ID, Valid: true},
		Status:            database.ChatStatusRunning,
	})
	wakeup := insertEventsWakeup(ctx, t, fdb, parent)

	_, err := fdb.InsertChatWakeupWatch(ctx, forkdb.InsertChatWakeupWatchParams{
		ID: uuid.New(), WakeupID: wakeup.ID, Kind: forkdb.ChatWakeupWatchKindChildDone, Spec: json.RawMessage(`{}`), CreatedAt: dbtime.Now(),
	})
	require.Error(t, err, "a child_done watch names its child")
	_, err = fdb.InsertChatWakeupWatch(ctx, forkdb.InsertChatWakeupWatchParams{
		ID: uuid.New(), WakeupID: uuid.New(), Kind: forkdb.ChatWakeupWatchKindChildDone,
		ChildChatID: uuid.NullUUID{UUID: child.ID, Valid: true}, Spec: json.RawMessage(`{}`), CreatedAt: dbtime.Now(),
	})
	require.ErrorIs(t, err, sql.ErrNoRows, "a watch needs its wake-up")

	childWatch := insertChildWatch(ctx, t, fdb, wakeup, 0, child)
	process, err := fdb.InsertChatWakeupWatch(ctx, forkdb.InsertChatWakeupWatchParams{
		ID:        uuid.New(),
		WakeupID:  wakeup.ID,
		Kind:      forkdb.ChatWakeupWatchKindProcess,
		Ordinal:   1,
		AgentID:   uuid.NullUUID{UUID: uuid.New(), Valid: true},
		ProcessID: sql.NullString{String: "proc-1", Valid: true},
		Spec:      json.RawMessage(`{"output_pattern": "FAIL"}`),
		CreatedAt: dbtime.Now(),
	})
	require.NoError(t, err)

	// A child that waits for events of its own shows up for its parent.
	own := insertEventsWakeup(ctx, t, fdb, child)
	_, err = fdb.InsertChatWakeupWatch(ctx, forkdb.InsertChatWakeupWatchParams{
		ID: uuid.New(), WakeupID: own.ID, Kind: forkdb.ChatWakeupWatchKindPullRequest,
		PullRequestUrl: sql.NullString{String: "https://github.com/o/r/pull/1", Valid: true}, Spec: json.RawMessage(`{}`), CreatedAt: dbtime.Now(),
	})
	require.NoError(t, err)
	ids, err := fdb.ListChildChatIDsWithOpenWakeupWatches(ctx, parent.ID)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{child.ID}, ids)

	// An output line delivers without ending the watch, and a stale count
	// loses.
	failedAt := dbtime.Now()
	require.NoError(t, fdb.MarkChatWakeupWatchFailed(ctx, forkdb.MarkChatWakeupWatchFailedParams{ID: process.ID, WakeupID: wakeup.ID, Now: failedAt}))
	require.NoError(t, fdb.MarkChatWakeupWatchFailed(ctx, forkdb.MarkChatWakeupWatchFailedParams{ID: process.ID, WakeupID: wakeup.ID, Now: failedAt.Add(time.Minute)}))
	line, err := fdb.MarkChatWakeupWatchDelivered(ctx, forkdb.MarkChatWakeupWatchDeliveredParams{
		ID: process.ID, WakeupID: wakeup.ID, Deliveries: 0, Now: dbtime.Now(), SpecPatch: json.RawMessage(`{"output_cursor": 7}`),
	})
	require.NoError(t, err)
	require.False(t, line.DeliveredAt.Valid, "an output line keeps the watch open")
	require.False(t, line.FailedAt.Valid, "a delivery clears the failure")
	require.EqualValues(t, 1, line.Deliveries)
	require.JSONEq(t, `{"output_pattern": "FAIL", "output_cursor": 7}`, string(line.Spec))
	_, err = fdb.MarkChatWakeupWatchDelivered(ctx, forkdb.MarkChatWakeupWatchDeliveredParams{
		ID: process.ID, WakeupID: wakeup.ID, Deliveries: 0, Now: dbtime.Now(), SpecPatch: json.RawMessage(`{}`),
	})
	require.ErrorIs(t, err, sql.ErrNoRows)
	_, err = fdb.MarkChatWakeupWatchDelivered(ctx, forkdb.MarkChatWakeupWatchDeliveredParams{
		ID: process.ID, WakeupID: own.ID, Deliveries: 1, Now: dbtime.Now(), SpecPatch: json.RawMessage(`{}`),
	})
	require.ErrorIs(t, err, sql.ErrNoRows, "a watch belongs to its wake-up")

	open, err := fdb.ListOpenChatWakeupWatchesByChatID(ctx, parent.ID)
	require.NoError(t, err)
	require.Len(t, open, 2)
	require.Equal(t, childWatch.ID, open[0].ChatWakeupWatch.ID)
	require.Equal(t, process.ID, open[1].ChatWakeupWatch.ID)
	require.Equal(t, wakeup.ExpiresAt, open[1].WakeupExpiresAt)

	// A deleted child takes its watch with it.
	_, err = sqlDB.ExecContext(ctx, "DELETE FROM chats WHERE id = $1", child.ID)
	require.NoError(t, err)
	all, err := fdb.ListChatWakeupWatchesByWakeupID(ctx, wakeup.ID)
	require.NoError(t, err)
	require.Len(t, all, 1)
	require.Equal(t, process.ID, all[0].ID)
	byChat, err := fdb.ListChatWakeupWatchesByChatID(ctx, parent.ID)
	require.NoError(t, err)
	require.Equal(t, all, byChat)
}

// TestListReadyChatWakeupWatchesPlan checks that the poll of the whole
// deployment stays cheap when nothing waits: with 10,000 finished wake-ups
// and no open watch, the plan reads no table in full.
func TestListReadyChatWakeupWatchesPlan(t *testing.T) {
	t.Parallel()

	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(t)
	ctx := testutil.Context(t, testutil.WaitSuperLong)

	template := wakeupTestChat(t, db)
	dbgen.ChatMessage(t, db, database.ChatMessage{ChatID: template.ID, Role: database.ChatMessageRoleAssistant})
	// One chat per wake-up, each with a message, copied from the template.
	_, err := sqlDB.ExecContext(ctx, `
		INSERT INTO chats
		SELECT (jsonb_populate_record(c, jsonb_build_object('id', gen_random_uuid()))).*
		FROM chats c, generate_series(1, 10000)
		WHERE c.id = $1`, template.ID)
	require.NoError(t, err)
	_, err = sqlDB.ExecContext(ctx, `
		INSERT INTO chat_messages
		SELECT (jsonb_populate_record(m, jsonb_build_object(
			'id', nextval(pg_get_serial_sequence('chat_messages', 'id')),
			'chat_id', c.id,
			'revision', NULL))).*
		FROM chats c, chat_messages m
		WHERE m.chat_id = $1 AND c.id <> $1`, template.ID)
	require.NoError(t, err)
	_, err = sqlDB.ExecContext(ctx, `
		INSERT INTO chat_wakeups (id, chat_id, organization_id, created_by, source, kind, prompt, status, status_reason, expires_at)
		SELECT gen_random_uuid(), c.id, c.organization_id, c.owner_id, 'wait_for_events', 'events', 'p', 'completed', 'done', now()
		FROM chats c
		WHERE c.id <> $1`, template.ID)
	require.NoError(t, err)
	_, err = sqlDB.ExecContext(ctx, `
		INSERT INTO chat_wakeup_watches (id, wakeup_id, chat_id, organization_id, kind, ordinal, child_chat_id, delivered_at, deliveries)
		SELECT gen_random_uuid(), w.id, w.chat_id, w.organization_id, 'child_done', 0, $1, now(), 1
		FROM chat_wakeups w`, template.ID)
	require.NoError(t, err)
	_, err = sqlDB.ExecContext(ctx, "ANALYZE chats, chat_messages, chat_wakeups, chat_wakeup_watches")
	require.NoError(t, err)

	var count int
	require.NoError(t, sqlDB.QueryRowContext(ctx, "SELECT count(*) FROM chat_wakeups WHERE status = 'completed'").Scan(&count))
	require.Equal(t, 10000, count)

	var raw []byte
	require.NoError(t, sqlDB.QueryRowContext(ctx, "EXPLAIN (FORMAT JSON) "+forkdb.ListReadyChatWakeupWatchesSQL, nil, nil, 100).Scan(&raw))
	var plan []struct {
		Plan planNode `json:"Plan"`
	}
	require.NoError(t, json.Unmarshal(raw, &plan))
	require.Len(t, plan, 1)
	// chat_descendant_questions stays empty here, so a scan of it costs
	// nothing and the planner may pick one.
	var scans []string
	plan[0].Plan.walk(func(node planNode) {
		if node.NodeType != "Seq Scan" {
			return
		}
		switch node.RelationName {
		case "chat_wakeups", "chats", "chat_messages", "chat_wakeup_watches":
			scans = append(scans, node.RelationName)
		}
	})
	require.Empty(t, scans, "sequential scans in plan:\n%s", raw)
}

type planNode struct {
	NodeType     string     `json:"Node Type"`
	RelationName string     `json:"Relation Name"`
	Plans        []planNode `json:"Plans"`
}

func (n planNode) walk(visit func(planNode)) {
	visit(n)
	for _, child := range n.Plans {
		child.walk(visit)
	}
}

func insertEventsWakeup(ctx context.Context, t *testing.T, fdb forkdb.Store, chat database.Chat) forkdb.ChatWakeup {
	t.Helper()
	now := dbtime.Now()
	wakeup, err := fdb.InsertChatWakeup(ctx, forkdb.InsertChatWakeupParams{
		ID:        uuid.New(),
		ChatID:    chat.ID,
		CreatedBy: chat.OwnerID,
		Source:    "wait_for_events",
		Kind:      forkdb.ChatWakeupKindEvents,
		Prompt:    "continue",
		Timezone:  "UTC",
		Events:    json.RawMessage(`[]`),
		Status:    forkdb.ChatWakeupStatusActive,
		NextRunAt: sql.NullTime{Time: now, Valid: true},
		ExpiresAt: sql.NullTime{Time: now.Add(time.Hour), Valid: true},
		CreatedAt: now,
		UpdatedAt: now,
	})
	require.NoError(t, err)
	return wakeup
}

func insertChildWatch(ctx context.Context, t *testing.T, fdb forkdb.Store, wakeup forkdb.ChatWakeup, ordinal int32, child database.Chat) forkdb.ChatWakeupWatch {
	t.Helper()
	watch, err := fdb.InsertChatWakeupWatch(ctx, forkdb.InsertChatWakeupWatchParams{
		ID:          uuid.New(),
		WakeupID:    wakeup.ID,
		Kind:        forkdb.ChatWakeupWatchKindChildDone,
		Ordinal:     ordinal,
		ChildChatID: uuid.NullUUID{UUID: child.ID, Valid: true},
		Spec:        json.RawMessage(`{}`),
		CreatedAt:   dbtime.Now(),
	})
	require.NoError(t, err)
	return watch
}
