package forkdb_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sqlc-dev/pqtype"
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

// TestUpdateWorkflowStepKeepsAReport checks that a reported row of a running
// run keeps its value against a later report and every engine write, so of
// two racing reports the first wins in the journal, while a report to an
// interrupted run replaces the value.
func TestUpdateWorkflowStepKeepsAReport(t *testing.T) {
	t.Parallel()

	db, _ := dbtestutil.NewDB(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	chat := wakeupTestChat(t, db)
	fdb, err := forkdb.From(db)
	require.NoError(t, err)

	owner := uuid.NullUUID{UUID: uuid.New(), Valid: true}
	runID, stepID := uuid.New(), uuid.New()
	require.NoError(t, fdb.InsertWorkflowRun(ctx, forkdb.InsertWorkflowRunParams{
		ID: runID, ChatID: chat.ID, ModelConfigID: chat.LastModelConfigID, Script: "run()",
		Args: json.RawMessage(`{}`), Status: "running", OwnerID: owner,
	}))
	_, err = fdb.InsertWorkflowStep(ctx, forkdb.InsertWorkflowStepParams{
		ID: stepID, RunID: runID, SpecKey: "step", Status: "running", OwnerID: owner,
	})
	require.NoError(t, err)
	report := func(value string, ownerID uuid.NullUUID, status string) int64 {
		n, err := fdb.UpdateWorkflowStep(ctx, forkdb.UpdateWorkflowStepParams{
			ID: stepID, Status: status, OwnerID: ownerID,
			Value: pqtype.NullRawMessage{RawMessage: json.RawMessage(value), Valid: value != ""},
		})
		require.NoError(t, err)
		return n
	}
	value := func() string {
		row, err := fdb.GetWorkflowStep(ctx, stepID)
		require.NoError(t, err)
		return row.Status + " " + string(row.Value.RawMessage)
	}

	require.EqualValues(t, 1, report(`"first"`, uuid.NullUUID{}, "reported"))
	require.EqualValues(t, 0, report(`"second"`, uuid.NullUUID{}, "reported"), "a later report of a running run")
	require.EqualValues(t, 0, report("", owner, "failed"), "the engine fails a reported step")
	require.EqualValues(t, 0, report("", owner, "reminded"), "the engine reminds a reported step")
	require.Equal(t, `reported "first"`, value())

	_, err = fdb.UpdateWorkflowRunStatus(ctx, forkdb.UpdateWorkflowRunStatusParams{
		ID: runID, Status: "interrupted", OwnerID: owner,
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, report(`"latest"`, uuid.NullUUID{}, "reported"), "a report to an interrupted run")
	require.Equal(t, `reported "latest"`, value())
}
