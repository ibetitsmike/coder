package forkdb_test

import (
	"encoding/json"
	"testing"

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
