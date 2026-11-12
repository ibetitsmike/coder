package chatd

// This file is fork code: tests for the durable journal of the
// fork-workflows experiment. A "restart" is a second chatd Server on the
// same database: its startup sweep marks the open runs of the first process
// interrupted, and its registry is empty, so only the journal can answer.

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/testutil"
)

func forkWorkflowDB(t *testing.T, db database.Store) forkdb.Store {
	t.Helper()
	fdb, err := forkdb.From(db)
	require.NoError(t, err)
	return fdb
}

// restartWorkflowServer creates the chatd process that replaces f.server on
// the same database.
func (f *forkWorkflowFixture) restartWorkflowServer(t *testing.T) *forkWorkflowFixture {
	t.Helper()
	server := newInternalTestServer(t, f.db, f.ps, chatprovider.ProviderAPIKeys{},
		withInternalTestServerExperiments(codersdk.Experiments{codersdk.ExperimentForkWorkflows}))
	return &forkWorkflowFixture{db: f.db, ps: f.ps, server: server, user: f.user, org: f.org, model: f.model}
}

// journalRunStatus reads the status of the run's journal row.
func journalRunStatus(ctx context.Context, t *testing.T, fdb forkdb.Store, id uuid.UUID) string {
	t.Helper()
	row, err := fdb.GetWorkflowRun(ctx, id)
	require.NoError(t, err)
	return row.Status
}

const journalReviewScript = reviewSchemaStarlark + `
def main(args):
    steps = [spawn("Review %s for bugs." % name, schema = REVIEW, title = name) for name in args["files"]]
    reviews = wait(steps)
    return {"bugs": [b for r in reviews if r != None for b in r["bugs"]]}
`

func TestForkWorkflowJournal(t *testing.T) {
	t.Parallel()

	t.Run("RestartReplaysFinishedSteps", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{
			Script: journalReviewScript,
			Args:   map[string]any{"files": []string{"a.go", "b.go"}},
		})
		steps := f.steps(ctx, t, run, 2)
		f.report(ctx, t, steps[0].ChatID, `{"bugs":["nil map write"]}`)
		f.report(ctx, t, steps[1].ChatID, `{"bugs":[]}`)

		fdb := forkWorkflowDB(t, f.db)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			row, err := fdb.GetWorkflowRun(ctx, run.id)
			return err == nil && row.Status == workflowStatusCompleted
		}, testutil.IntervalFast)

		// The restart keeps the terminal row: the sweep only interrupts
		// open runs.
		restarted := f.restartWorkflowServer(t)
		assert.Equal(t, workflowStatusCompleted, journalRunStatus(ctx, t, fdb, run.id))

		// The resume replays the result from the journal. The script and
		// the args are omitted: the journal holds them.
		promptsBefore := map[uuid.UUID]int{}
		for _, step := range steps {
			id := uuid.MustParse(step.ChatID)
			promptsBefore[id] = len(f.userTexts(ctx, t, id))
		}
		resumed, err := restarted.server.startWorkflowRun(ctx, parent, f.model.ID,
			runWorkflowArgs{ResumeRunID: run.id.String()}, uuid.New())
		require.NoError(t, err)
		res := resumed.await(ctx, testutil.WaitShort)
		require.Equal(t, workflowStatusCompleted, res.Status, res.Error)
		assert.JSONEq(t, `{"bugs":["nil map write"]}`, string(res.Result))
		require.Len(t, res.Steps, 2)
		for i, step := range res.Steps {
			assert.True(t, step.Reused, "step %d must be reused", i)
			assert.Equal(t, steps[i].ChatID, step.ChatID)
		}
		// The replay executed nothing: the children keep their messages.
		for _, step := range steps {
			id := uuid.MustParse(step.ChatID)
			assert.Equal(t, promptsBefore[id], len(f.userTexts(ctx, t, id)), "child %s must not run again", step.ChatID)
		}
		stepRows, err := fdb.GetWorkflowStepsByRun(ctx, run.id)
		require.NoError(t, err)
		assert.Len(t, stepRows, 2, "the replay must not journal new steps")
	})

	t.Run("RestartRerunsUnfinishedSteps", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{
			Script: journalReviewScript,
			Args:   map[string]any{"files": []string{"a.go", "b.go"}},
		})
		steps := f.steps(ctx, t, run, 2)
		// No step reported. The restart marks the run interrupted, and the
		// resume re-runs both steps in their child chats.
		restarted := f.restartWorkflowServer(t)
		fdb := forkWorkflowDB(t, f.db)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return journalRunStatus(ctx, t, fdb, run.id) == workflowStatusInterrupted
		}, testutil.IntervalFast)

		resumed, err := restarted.server.startWorkflowRun(ctx, parent, f.model.ID,
			runWorkflowArgs{ResumeRunID: run.id.String()}, uuid.New())
		require.NoError(t, err)
		assert.Equal(t, workflowStatusRunning, resumed.snapshot().Status)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return journalRunStatus(ctx, t, fdb, run.id) == workflowStatusRunning
		}, testutil.IntervalFast)

		// The resume reused the child chats; the test plays both steps in
		// them again.
		f.report(ctx, t, steps[0].ChatID, `{"bugs":["nil map write"]}`)
		f.report(ctx, t, steps[1].ChatID, `{"bugs":[]}`)
		res := testutil.RequireReceive(ctx, t, awaitRegistered(ctx, t, resumed))
		require.Equal(t, workflowStatusCompleted, res.Status, res.Error)
		assert.JSONEq(t, `{"bugs":["nil map write"]}`, string(res.Result))
		require.Len(t, res.Steps, 2)
		for i, step := range res.Steps {
			assert.True(t, step.Reused, "step %d must be reused", i)
			assert.Equal(t, steps[i].ChatID, step.ChatID)
		}
		stepRows, err := fdb.GetWorkflowStepsByRun(ctx, run.id)
		require.NoError(t, err)
		assert.Len(t, stepRows, 2, "the re-run must reuse the journal rows")
		assert.Equal(t, workflowStatusCompleted, journalRunStatus(ctx, t, fdb, run.id))
	})

	t.Run("AwaitReadsAnInterruptedRun", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{
			Script: `def main(args):
    return agent("Say hi.", title = "hi")
`,
		})
		step := f.steps(ctx, t, run, 1)[0]

		restarted := f.restartWorkflowServer(t)
		fdb := forkWorkflowDB(t, f.db)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return journalRunStatus(ctx, t, fdb, run.id) == workflowStatusInterrupted
		}, testutil.IntervalFast)

		tools := restarted.server.forkWorkflowTools(ctx, parent, f.model.ID, nil)
		require.Len(t, tools, 2)
		resp, err := tools[1].Run(ctx, fantasy.ToolCall{Input: fmt.Sprintf(`{"run_id":%q}`, run.id)})
		require.NoError(t, err)
		assert.False(t, resp.IsError, resp.Content)
		var out runWorkflowResult
		require.NoError(t, json.Unmarshal([]byte(resp.Content), &out))
		assert.Equal(t, run.id.String(), out.RunID)
		assert.Equal(t, workflowStatusInterrupted, out.Status)
		require.Len(t, out.Steps, 1)
		assert.Equal(t, step.ChatID, out.Steps[0].ChatID)

		// run_workflow resumes the run by its ID alone.
		resp, err = tools[0].Run(workspacesdk.WithToolCallID(ctx, uuid.New()),
			fantasy.ToolCall{Input: fmt.Sprintf(`{"resume_run_id":%q}`, run.id)})
		require.NoError(t, err)
		assert.False(t, resp.IsError, resp.Content)
		require.NoError(t, json.Unmarshal([]byte(resp.Content), &out))
		assert.Equal(t, workflowStatusRunning, out.Status)

		// The test plays the step in its reused child chat, and await
		// returns the completed result.
		f.report(ctx, t, step.ChatID, `{"text":"hi"}`)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			resp, err := tools[1].Run(ctx, fantasy.ToolCall{Input: fmt.Sprintf(`{"run_id":%q,"wait_seconds":0}`, run.id)})
			if err != nil || resp.IsError {
				return false
			}
			var res runWorkflowResult
			if err := json.Unmarshal([]byte(resp.Content), &res); err != nil {
				return false
			}
			out = res
			return res.Status == workflowStatusCompleted
		}, testutil.IntervalFast)
		assert.JSONEq(t, `{"text":"hi"}`, string(out.Result))
	})
}
