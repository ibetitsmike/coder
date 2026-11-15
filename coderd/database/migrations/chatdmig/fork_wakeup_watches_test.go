package chatdmig_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/migrations"
	"github.com/coder/coder/v2/coderd/database/migrations/chatdmig"
)

// TestWakeupWatchesBackfill runs the node that moves the events of live
// events wake-ups into watch rows on a database with wake-ups and runs. Each
// entry becomes one row with its baselines, the run keys that recorded a
// delivery become delivered_at, and the wake-ups that are no longer live keep
// no watches.
func TestWakeupWatchesBackfill(t *testing.T) {
	t.Parallel()
	admin := adminURL(t)
	g, err := migrations.LoadGraph()
	require.NoError(t, err)
	var node *chatdmig.Node
	for _, n := range g.Nodes {
		if strings.HasSuffix(n.Name, "_chat_wakeup_watches_backfill") {
			node = n
		}
	}
	require.NotNil(t, node)

	_, sqlDB := createDB(t, admin)
	require.NoError(t, migrations.UpGraph(sqlDB, withoutNode(g, node)))
	db := database.New(sqlDB)

	model := dbgen.ChatModelConfig(t, db, database.ChatModelConfig{})
	seed := database.Chat{
		OrganizationID:    dbgen.Organization(t, db, database.Organization{}).ID,
		OwnerID:           dbgen.User(t, db, database.User{}).ID,
		LastModelConfigID: model.ID,
	}
	parent := dbgen.Chat(t, db, seed)
	seed.ParentChatID = uuid.NullUUID{UUID: parent.ID, Valid: true}
	delivered := dbgen.Chat(t, db, seed)
	waiting := dbgen.Chat(t, db, seed)
	agentID := uuid.New()
	created := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	const prURL = "https://github.com/o/r/pull/7"

	insertWakeup := func(status, events string) uuid.UUID {
		t.Helper()
		id := uuid.New()
		var next any
		if status == "active" {
			next = created.Add(time.Minute)
		}
		_, err := sqlDB.Exec(`INSERT INTO chat_wakeups (id, chat_id, organization_id, created_by, source, kind, prompt, events, status, next_run_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4, 'wait_for_events', 'events', 'wake', $5, $6, $7, $8, $8)`,
			id, parent.ID, parent.OrganizationID, parent.OwnerID, events, status, next, created)
		require.NoError(t, err)
		return id
	}
	insertRun := func(wakeupID uuid.UUID, key string, status string, at time.Time) {
		t.Helper()
		_, err := sqlDB.Exec(`INSERT INTO chat_wakeup_runs (id, wakeup_id, chat_id, organization_id, run_key, status, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $7)`, uuid.New(), wakeupID, parent.ID, parent.OrganizationID, key, status, at)
		require.NoError(t, err)
	}

	events := fmt.Sprintf(`[
		{"type": "child_done", "chat_id": %q},
		{"type": "child_done", "chat_id": %q},
		{"type": "process_exit", "process_id": "proc-1", "command": "make test", "agent_id": %q, "output_pattern": "FAIL", "output_cooldown_seconds": 60, "output_max_events": 10},
		{"type": "pull_request", "url": %q, "on": ["review", "checks"], "since": "2026-11-01T11:00:00Z", "head_sha": "abc", "checks_state": "pending", "seen": ["%s#issuecomment-1"]},
		{"type": "pr_merged", "url": %q},
		{"type": "child_done", "chat_id": "not-a-uuid"},
		{"type": "child_done", "chat_id": %q}
	]`, delivered.ID, waiting.ID, agentID, prURL, prURL, prURL, uuid.New())
	active := insertWakeup("active", events)
	deliveredAt := created.Add(10 * time.Minute)
	insertRun(active, "child_done:"+delivered.ID.String(), "delivered", deliveredAt)
	// A failed send of the waiting child, and two line messages and a failed
	// one of the process, which does not count.
	failedAt := created.Add(11 * time.Minute)
	insertRun(active, "child_done:"+waiting.ID.String()+":failed", "failed", failedAt)
	insertRun(active, "process_output:proc-1:120", "delivered", created.Add(2*time.Minute))
	insertRun(active, "process_output:proc-1:480", "delivered", created.Add(3*time.Minute))
	insertRun(active, "process_output:proc-1:900:failed", "failed", created.Add(4*time.Minute))
	// The run keys of other wake-ups and occurrences stay with them.
	insertRun(active, "2026-11-01T12:00:00Z", "delivered", created)

	paused := insertWakeup("paused", fmt.Sprintf(`[{"type": "pull_request", "url": %q, "on": ["closed"]}]`, prURL))
	insertRun(paused, "pull_request:"+prURL, "delivered", deliveredAt)
	completed := insertWakeup("completed", fmt.Sprintf(`[{"type": "child_done", "chat_id": %q}]`, waiting.ID))
	insertRun(completed, "child_done:"+waiting.ID.String(), "delivered", deliveredAt)
	canceled := insertWakeup("canceled", fmt.Sprintf(`[{"type": "child_done", "chat_id": %q}]`, waiting.ID))
	notArray := insertWakeup("active", `{"type": "child_done"}`)
	// Events that give no watch: only unknown or malformed entries pause the
	// wake-up, as an unreadable events value did. An empty list, or only
	// children that no longer exist, completed before and still does.
	unknownOnly := insertWakeup("active", fmt.Sprintf(`[{"type": "pr_merged", "url": %q}, {"type": "child_done", "chat_id": "not-a-uuid"}]`, prURL))
	empty := insertWakeup("active", `[]`)
	goneOnly := insertWakeup("active", fmt.Sprintf(`[{"type": "child_done", "chat_id": %q}]`, uuid.New()))
	pausedUnknown := insertWakeup("paused", `[{"type": "pr_merged"}]`)

	statuses := func() map[uuid.UUID]string {
		t.Helper()
		rows, err := sqlDB.Query(`SELECT id, status || ':' || COALESCE(status_reason, '') || ':' || run_count || ':' || COALESCE(next_run_at::text, '') FROM chat_wakeups`)
		require.NoError(t, err)
		defer rows.Close()
		got := map[uuid.UUID]string{}
		for rows.Next() {
			var id uuid.UUID
			var s string
			require.NoError(t, rows.Scan(&id, &s))
			got[id] = s
		}
		require.NoError(t, rows.Err())
		return got
	}
	before := statuses()

	require.NoError(t, migrations.UpAll(sqlDB))
	require.NoError(t, migrations.EnsureCleanAll(sqlDB))

	after := statuses()
	for _, id := range []uuid.UUID{notArray, unknownOnly} {
		require.Equal(t, "paused:invalid_events:0:", after[id], "an active wake-up whose events give no watch pauses")
		after[id] = before[id]
	}
	require.Equal(t, before, after, "the node changes no other wake-up")
	require.Contains(t, before[empty], "active")
	require.Contains(t, before[goneOnly], "active")
	require.Contains(t, before[pausedUnknown], "paused::")
	var hasEvents bool
	require.NoError(t, sqlDB.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'chat_wakeups' AND column_name = 'events')`).Scan(&hasEvents))
	require.False(t, hasEvents, "the events column is gone")

	type watch struct {
		Kind        string
		Ordinal     int
		Child       uuid.NullUUID
		Agent       uuid.NullUUID
		Process     sql.NullString
		URL         sql.NullString
		Spec        map[string]any
		DeliveredAt sql.NullTime
		Deliveries  int
		FailedAt    sql.NullTime
		ChatID      uuid.UUID
		CreatedAt   time.Time
	}
	watches := func(wakeupID uuid.UUID) []watch {
		t.Helper()
		rows, err := sqlDB.Query(`SELECT kind, ordinal, child_chat_id, agent_id, process_id, pull_request_url, spec, delivered_at, deliveries, failed_at, chat_id, created_at
			FROM chat_wakeup_watches WHERE wakeup_id = $1 ORDER BY ordinal`, wakeupID)
		require.NoError(t, err)
		defer rows.Close()
		var got []watch
		for rows.Next() {
			var w watch
			var spec []byte
			require.NoError(t, rows.Scan(&w.Kind, &w.Ordinal, &w.Child, &w.Agent, &w.Process, &w.URL, &spec, &w.DeliveredAt, &w.Deliveries, &w.FailedAt, &w.ChatID, &w.CreatedAt))
			require.NoError(t, json.Unmarshal(spec, &w.Spec))
			got = append(got, w)
		}
		require.NoError(t, rows.Err())
		return got
	}

	got := watches(active)
	// The unknown type, the invalid ID and the child that does not exist are
	// left out.
	require.Len(t, got, 4)
	for i, w := range got {
		require.Equal(t, parent.ID, w.ChatID)
		require.True(t, created.Equal(w.CreatedAt))
		require.Equal(t, i, w.Ordinal, "entries keep their order")
	}

	require.Equal(t, "child_done", got[0].Kind)
	require.Equal(t, delivered.ID, got[0].Child.UUID)
	require.Empty(t, got[0].Spec)
	require.True(t, got[0].DeliveredAt.Valid, "the run key recorded the delivery")
	require.True(t, deliveredAt.Equal(got[0].DeliveredAt.Time))
	require.Equal(t, 1, got[0].Deliveries)
	require.False(t, got[0].FailedAt.Valid)

	require.Equal(t, "child_done", got[1].Kind)
	require.Equal(t, waiting.ID, got[1].Child.UUID)
	require.False(t, got[1].DeliveredAt.Valid, "only the delivered entry is delivered")
	require.Zero(t, got[1].Deliveries)
	require.True(t, got[1].FailedAt.Valid)
	require.True(t, failedAt.Equal(got[1].FailedAt.Time))

	require.Equal(t, "process", got[2].Kind)
	require.Equal(t, "proc-1", got[2].Process.String)
	require.Equal(t, agentID, got[2].Agent.UUID)
	require.False(t, got[2].DeliveredAt.Valid)
	require.Equal(t, 2, got[2].Deliveries, "two line messages; the failed one does not count")
	require.Equal(t, map[string]any{
		"command": "make test", "output_pattern": "FAIL", "output_cooldown_seconds": float64(60), "output_max_events": float64(10),
		"output_cursor": float64(480),
	}, got[2].Spec)

	require.Equal(t, "pull_request", got[3].Kind)
	require.Equal(t, prURL, got[3].URL.String)
	require.False(t, got[3].DeliveredAt.Valid)
	require.Equal(t, map[string]any{
		"on": []any{"review", "checks"}, "since": "2026-11-01T11:00:00Z", "head_sha": "abc", "checks_state": "pending",
		"seen": []any{prURL + "#issuecomment-1"},
	}, got[3].Spec)

	pausedWatches := watches(paused)
	require.Len(t, pausedWatches, 1, "a paused wake-up can resume, so it keeps its watches")
	require.True(t, pausedWatches[0].DeliveredAt.Valid)

	// Finished wake-ups keep their watches, so the wake-up list still shows
	// what they watched. Every watch query reads active wake-ups only.
	completedWatches := watches(completed)
	require.Len(t, completedWatches, 1)
	require.Equal(t, waiting.ID, completedWatches[0].Child.UUID)
	require.True(t, completedWatches[0].DeliveredAt.Valid)
	canceledWatches := watches(canceled)
	require.Len(t, canceledWatches, 1)
	require.Equal(t, waiting.ID, canceledWatches[0].Child.UUID)
	require.False(t, canceledWatches[0].DeliveredAt.Valid)
	require.Empty(t, watches(notArray))
	require.Empty(t, watches(unknownOnly))
	require.Empty(t, watches(empty))
	require.Empty(t, watches(goneOnly))
	require.Empty(t, watches(pausedUnknown))

	var open int
	require.NoError(t, sqlDB.QueryRow(`SELECT count(*) FROM chat_wakeup_watches x JOIN chat_wakeups w ON w.id = x.wakeup_id
		WHERE x.child_chat_id = $1 AND x.delivered_at IS NULL AND w.status = 'active'`, waiting.ID).Scan(&open))
	require.Equal(t, 1, open, "only the active wake-up still waits for the child")
}
