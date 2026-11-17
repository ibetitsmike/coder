package forkdb_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/testutil"
)

// TestWorkflowRunSweepPlan checks that the stale run sweep, which every
// chatd process runs on each heartbeat tick, reads the running runs through
// the partial index instead of scanning every journaled run.
func TestWorkflowRunSweepPlan(t *testing.T) {
	t.Parallel()

	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(t)
	ctx := testutil.Context(t, testutil.WaitSuperLong)
	chat := wakeupTestChat(t, db)

	// The journal keeps ended runs for weeks, so running runs are a small
	// share of the table.
	_, err := sqlDB.ExecContext(ctx, `
		INSERT INTO workflow_runs (id, chat_id, model_config_id, script, status, owner_id, heartbeat_at)
		SELECT gen_random_uuid(), $1, $2, 'run()',
			CASE WHEN i % 100 = 0 THEN 'running'
				ELSE (ARRAY['completed', 'failed', 'stopped', 'interrupted'])[1 + i % 4] END,
			gen_random_uuid(), now()
		FROM generate_series(1, 10000) i`, chat.ID, chat.LastModelConfigID)
	require.NoError(t, err)
	_, err = sqlDB.ExecContext(ctx, "ANALYZE workflow_runs")
	require.NoError(t, err)

	var raw []byte
	require.NoError(t, sqlDB.QueryRowContext(ctx, "EXPLAIN (FORMAT JSON) "+forkdb.MarkStaleWorkflowRunsInterruptedSQL,
		"interrupted", time.Now().Add(-30*time.Second)).Scan(&raw))
	var plan []struct {
		Plan planNode `json:"Plan"`
	}
	require.NoError(t, json.Unmarshal(raw, &plan))
	require.Len(t, plan, 1)
	var seqScans, indexScans int
	plan[0].Plan.walk(func(node planNode) {
		if node.NodeType == "Seq Scan" && node.RelationName == "workflow_runs" {
			seqScans++
		}
		if node.IndexName == "workflow_runs_running_idx" {
			indexScans++
		}
	})
	require.Zero(t, seqScans, "the sweep scans workflow_runs in full:\n%s", raw)
	require.NotZero(t, indexScans, "the sweep does not use workflow_runs_running_idx:\n%s", raw)
}
