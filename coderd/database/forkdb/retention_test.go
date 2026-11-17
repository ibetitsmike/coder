package forkdb_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/testutil"
)

// TestRetentionDeletePlans checks that the dbpurge batches, which run every
// purge tick, read only the rows they may delete: with 10,000 recent ended
// wake-ups and workflow runs, neither plan scans its table in full.
func TestRetentionDeletePlans(t *testing.T) {
	t.Parallel()

	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(t)
	ctx := testutil.Context(t, testutil.WaitSuperLong)
	chat := wakeupTestChat(t, db)

	_, err := sqlDB.ExecContext(ctx, `
		INSERT INTO chat_wakeups (id, chat_id, organization_id, created_by, source, kind, prompt, status, status_reason, expires_at)
		SELECT gen_random_uuid(), $1, $2, $3, 'wait_for_events', 'events', 'p',
			(ARRAY['completed', 'canceled', 'paused']::chat_wakeup_status[])[1 + i % 3], 'done', now() + interval '1 hour'
		FROM generate_series(1, 10000) i`, chat.ID, chat.OrganizationID, chat.OwnerID)
	require.NoError(t, err)
	_, err = sqlDB.ExecContext(ctx, `
		INSERT INTO workflow_runs (id, chat_id, model_config_id, script, status)
		SELECT gen_random_uuid(), $1, $2, 'run()',
			(ARRAY['completed', 'failed', 'stopped', 'interrupted', 'running'])[1 + i % 5]
		FROM generate_series(1, 10000) i`, chat.ID, chat.LastModelConfigID)
	require.NoError(t, err)
	_, err = sqlDB.ExecContext(ctx, "ANALYZE chat_wakeups, workflow_runs")
	require.NoError(t, err)

	for _, q := range []struct {
		name     string
		sql      string
		relation string
	}{
		{"DeleteOldChatWakeups", forkdb.DeleteOldChatWakeupsSQL, "chat_wakeups"},
		{"DeleteOldWorkflowRuns", forkdb.DeleteOldWorkflowRunsSQL, "workflow_runs"},
	} {
		var raw []byte
		require.NoError(t, sqlDB.QueryRowContext(ctx, "EXPLAIN (FORMAT JSON) "+q.sql, 1000).Scan(&raw), q.name)
		var plan []struct {
			Plan planNode `json:"Plan"`
		}
		require.NoError(t, json.Unmarshal(raw, &plan))
		require.Len(t, plan, 1)
		var scans []string
		plan[0].Plan.walk(func(node planNode) {
			if node.NodeType == "Seq Scan" && node.RelationName == q.relation {
				scans = append(scans, node.RelationName)
			}
		})
		require.Empty(t, scans, "%s: sequential scans in plan:\n%s", q.name, raw)
	}
}

// TestRetentionDeleteRechecksLockedRows checks that a dbpurge batch that
// waits for the lock of a row another transaction updates rechecks the row
// once the update commits. The batch picked the row from its snapshot, when
// it was old and ended; the update resumed it, so it must stay.
func TestRetentionDeleteRechecksLockedRows(t *testing.T) {
	t.Parallel()

	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	chat := wakeupTestChat(t, db)
	fdb, err := forkdb.From(db)
	require.NoError(t, err)

	for _, tc := range []struct {
		name   string
		table  string
		insert string
		args   []any
		update string
		purge  func(context.Context, int32) (int64, error)
	}{
		{
			name:  "workflow run resumed",
			table: "workflow_runs",
			insert: `INSERT INTO workflow_runs (id, chat_id, model_config_id, script, status, updated_at)
				VALUES ($1, $2, $3, 'run()', 'interrupted', now() - interval '31 days')`,
			args:   []any{chat.ID, chat.LastModelConfigID},
			update: `UPDATE workflow_runs SET status = 'running', updated_at = now() WHERE id = $1`,
			purge:  fdb.DeleteOldWorkflowRuns,
		},
		{
			name:  "wake-up resumed",
			table: "chat_wakeups",
			insert: `INSERT INTO chat_wakeups (id, chat_id, organization_id, created_by, source, kind, prompt, status, status_reason, expires_at, updated_at)
				VALUES ($1, $2, $3, $4, 'wait_for_events', 'events', 'p', 'canceled', 'done', now() + interval '1 hour', now() - interval '31 days')`,
			args:   []any{chat.ID, chat.OrganizationID, chat.OwnerID},
			update: `UPDATE chat_wakeups SET status = 'paused', updated_at = now() WHERE id = $1`,
			purge:  fdb.DeleteOldChatWakeups,
		},
	} {
		id := uuid.New()
		_, err := sqlDB.ExecContext(ctx, tc.insert, append([]any{id}, tc.args...)...)
		require.NoError(t, err, tc.name)

		tx, err := sqlDB.BeginTx(ctx, nil)
		require.NoError(t, err, tc.name)
		_, err = tx.ExecContext(ctx, tc.update, id)
		require.NoError(t, err, tc.name)

		type result struct {
			n   int64
			err error
		}
		purged := make(chan result, 1)
		go func() {
			n, err := tc.purge(ctx, 100)
			purged <- result{n, err}
		}()
		// The batch reads the old row from its snapshot and waits for the
		// row lock the update holds.
		testutil.Eventually(ctx, t, func(ctx context.Context) bool {
			var waiting int
			err := sqlDB.QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_activity
				WHERE datname = current_database() AND wait_event_type = 'Lock'
					AND query LIKE '%DELETE FROM ' || $1::text || '%'`, tc.table).Scan(&waiting)
			return err == nil && waiting > 0
		}, testutil.IntervalFast, "%s: the batch waits for the row lock", tc.name)
		require.NoError(t, tx.Commit(), tc.name)

		res := testutil.RequireReceive(ctx, t, purged)
		require.NoError(t, res.err, tc.name)
		require.Zero(t, res.n, "%s: the batch deletes nothing", tc.name)
		var kept int
		require.NoError(t, sqlDB.QueryRowContext(ctx, "SELECT count(*) FROM "+tc.table+" WHERE id = $1", id).Scan(&kept), tc.name)
		require.Equal(t, 1, kept, "%s: the resumed row stays", tc.name)
	}
}
