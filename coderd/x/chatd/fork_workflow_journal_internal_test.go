package chatd

// This file is fork code: tests for the durable journal of the
// fork-workflows experiment. A "restart" is a second chatd Server on the
// same database: its startup sweep marks the open runs of the first process
// interrupted, and its registry is empty, so only the journal can answer.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/sqlc-dev/pqtype"
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

// stepTools returns the dynamic tools column of a workflow step child with
// the default step schema.
func (*forkWorkflowFixture) stepTools(t *testing.T) pqtype.NullRawMessage {
	t.Helper()
	raw, err := workflowStepTools(json.RawMessage(defaultStepSchema))
	require.NoError(t, err)
	return pqtype.NullRawMessage{RawMessage: raw, Valid: true}
}

// journalRunStatus reads the status of the run's journal row.
func journalRunStatus(ctx context.Context, t *testing.T, fdb forkdb.Store, id uuid.UUID) string {
	t.Helper()
	return journalRow(ctx, t, fdb, id).Status
}

// journalRunLogs reads the run's retained log lines from the journal column.
func journalRunLogs(ctx context.Context, t *testing.T, fdb forkdb.Store, id uuid.UUID) []string {
	t.Helper()
	logs, _, _ := restoreWorkflowLogs(journalRow(ctx, t, fdb, id).Logs)
	return logs
}

// journalRow reads the run's journal row.
func journalRow(ctx context.Context, t *testing.T, fdb forkdb.Store, id uuid.UUID) forkdb.WorkflowRun {
	t.Helper()
	row, err := fdb.GetWorkflowRun(ctx, id)
	require.NoError(t, err)
	return row
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
		require.Len(t, tools, 3)
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

// TestForkWorkflowLiveLogs covers the log write-through of a live run: the
// journal keeps the lines the script printed so far, a burst lands in one
// paced write, a restart keeps them, and the terminal snapshot replaces the
// column.
func TestForkWorkflowLiveLogs(t *testing.T) {
	t.Parallel()

	t.Run("WriteThroughWhileRunning", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: `
def main(args):
    log("first line")
    log("second line")
    agent("Never reports.")
`})
		fdb := forkWorkflowDB(t, f.db)
		journalLogs := func() []string {
			row, err := fdb.GetWorkflowRun(ctx, run.id)
			require.NoError(t, err)
			logs, _, _ := restoreWorkflowLogs(row.Logs)
			return logs
		}
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return len(journalLogs()) == 2
		}, testutil.IntervalFast)
		assert.Equal(t, []string{"first line", "second line"}, journalLogs())
		assert.Equal(t, workflowStatusRunning, journalRunStatus(ctx, t, fdb, run.id))

		// The write-through paces itself: the next burst waits for the
		// interval instead of writing one row per line.
		before, err := fdb.GetWorkflowRun(ctx, run.id)
		require.NoError(t, err)
		run.log("third line")
		row, err := fdb.GetWorkflowRun(ctx, run.id)
		require.NoError(t, err)
		assert.Equal(t, before.UpdatedAt, row.UpdatedAt, "a line inside the interval does not write yet")

		// The paced flush of the third line lands within the interval, and
		// a restart keeps the live lines: the sweep only flips the status.
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return len(journalLogs()) == 3
		}, testutil.IntervalFast)
		f.restartWorkflowServer(t)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return journalRunStatus(ctx, t, fdb, run.id) == workflowStatusInterrupted
		}, testutil.IntervalFast)
		assert.Equal(t, []string{"first line", "second line", "third line"}, journalLogs(),
			"a restart keeps the live lines")

		// The run resumes and completes; the terminal snapshot carries the
		// lines the script printed, live and replayed ones alike.
		resumed, err := f.server.startWorkflowRun(ctx, parent, f.model.ID,
			runWorkflowArgs{ResumeRunID: run.id.String()}, uuid.New())
		require.NoError(t, err)
		step := f.steps(ctx, t, resumed, 1)[0]
		f.report(ctx, t, step.ChatID, `{"text":"hi"}`)
		res := testutil.RequireReceive(ctx, t, awaitRegistered(ctx, t, resumed))
		require.Equal(t, workflowStatusCompleted, res.Status, res.Error)
		assert.Equal(t, []string{"first line", "second line", "third line"}, res.Logs,
			"the replayed lines echo the baseline instead of duplicating it")
		row, err = fdb.GetWorkflowRun(ctx, run.id)
		require.NoError(t, err)
		require.True(t, row.Result.Valid)
		var snapshot struct {
			Logs []string `json:"logs"`
		}
		require.NoError(t, json.Unmarshal(row.Result.RawMessage, &snapshot))
		assert.Equal(t, []string{"first line", "second line", "third line"}, snapshot.Logs)
		logs, _, _ := restoreWorkflowLogs(row.Logs)
		assert.Equal(t, []string{"first line", "second line", "third line"}, logs,
			"the journal column holds one copy of the replayed history")
	})

	// A line that lands while a flush is in flight drops its own request:
	// the completed flush re-arms one for it, so the line reaches the
	// journal without another log call and without waiting for the end.
	t.Run("RearmsAfterAnInFlightFlush", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: `
def main(args):
    log("first line")
    log("second line")
    agent("Never reports.")
`})
		fdb := forkWorkflowDB(t, f.db)
		journalLogs := func() []string {
			row, err := fdb.GetWorkflowRun(ctx, run.id)
			require.NoError(t, err)
			logs, _, _ := restoreWorkflowLogs(row.Logs)
			return logs
		}
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return len(journalLogs()) == 2
		}, testutil.IntervalFast)

		run.journalMu.Lock()
		run.logFlushInFlight = true
		run.journalMu.Unlock()
		run.log("dropped line")
		run.journalMu.Lock()
		run.logFlushInFlight = false
		run.journalMu.Unlock()
		run.rearmLogFlush()
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return len(journalLogs()) == 3
		}, testutil.IntervalFast)
		assert.Equal(t, []string{"first line", "second line", "dropped line"}, journalLogs())
	})

	// A paced attempt that finds the write token taken must not spin:
	// the attempt stamps lastLogFlush, so the next attempt waits out the
	// interval instead of rescheduling immediately until the holder
	// finishes. A holder that does not set logFlushInFlight (a timer
	// flush, a stop's or the end's terminal flush) used to produce a
	// tight re-arm loop for the whole write.
	t.Run("HeldTokenDoesNotSpin", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: `
def main(args):
    log("first line")
    agent("Never reports.")
`})
		fdb := forkWorkflowDB(t, f.db)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			logs, _, _ := restoreWorkflowLogs(journalRow(ctx, t, fdb, run.id).Logs)
			return len(logs) == 1
		}, testutil.IntervalFast)

		// A plain token holder stands in for a slow write: the paced
		// attempts during the hold must pace themselves, not spin.
		<-run.journalLogWrite
		run.log("held line")
		spins := 0
		stop := make(chan struct{})
		go func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				run.journalMu.Lock()
				if run.logFlushInFlight {
					spins++
				}
				run.journalMu.Unlock()
				time.Sleep(time.Millisecond)
			}
		}()
		time.Sleep(200 * time.Millisecond)
		close(stop)
		run.journalLogWrite <- struct{}{}
		t.Logf("paced attempts during the hold: %d", spins)
		// The interval is 2s, so a spin loop would see hundreds of
		// attempts in 200ms; pacing allows at most a handful.
		assert.LessOrEqual(t, spins, 3, "the flush attempts must pace while the write token is held")
	})

	// await_workflow for an interrupted run this replica does not hold
	// reads the journaled live lines, the way a held run reports them.
	t.Run("AwaitReadsTheJournalLogs", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: `
def main(args):
    log("first line")
    log("second line")
    agent("Never reports.")
`})
		fdb := forkWorkflowDB(t, f.db)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			row, err := fdb.GetWorkflowRun(ctx, run.id)
			if err != nil {
				return false
			}
			logs, _, _ := restoreWorkflowLogs(row.Logs)
			return len(logs) == 2
		}, testutil.IntervalFast)

		restarted := f.restartWorkflowServer(t)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return journalRunStatus(ctx, t, fdb, run.id) == workflowStatusInterrupted
		}, testutil.IntervalFast)
		tools := restarted.server.forkWorkflowTools(ctx, parent, f.model.ID, nil)
		require.Len(t, tools, 3, "run_workflow, await_workflow and list_workflows")
		resp, err := tools[1].Run(ctx, fantasy.ToolCall{Input: fmt.Sprintf(`{"run_id":%q}`, run.id)})
		require.NoError(t, err)
		assert.False(t, resp.IsError, resp.Content)
		var out runWorkflowResult
		require.NoError(t, json.Unmarshal([]byte(resp.Content), &out))
		assert.Equal(t, workflowStatusInterrupted, out.Status)
		assert.Equal(t, []string{"first line", "second line"}, out.Logs)
	})

	// A restart of a capped run replays the script from its first line, but
	// the journal holds only the retained tail: the replayed prefix that the
	// tail does not contain must not drop it.
	t.Run("ReplayKeepsACappedTail", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: `
def main(args):
    for i in range(80):
        log("line %d" % i)
    agent("Never reports.")
`})
		fdb := forkWorkflowDB(t, f.db)
		journalLogs := func() []string {
			row, err := fdb.GetWorkflowRun(ctx, run.id)
			require.NoError(t, err)
			logs, _, _ := restoreWorkflowLogs(row.Logs)
			return logs
		}
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return len(journalLogs()) == maxWorkflowLogs
		}, testutil.IntervalFast)

		// The restart interrupts the run, and the resume replays it from
		// the first line, which the capped tail no longer contains. The
		// resume must go through the new server: the old one still holds the
		// run in its registry and would return it without a replay.
		restarted := f.restartWorkflowServer(t)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return journalRunStatus(ctx, t, fdb, run.id) == workflowStatusInterrupted
		}, testutil.IntervalFast)
		assert.Equal(t, "line 30", journalLogs()[0], "the restart keeps the tail")
		resumed, err := restarted.server.startWorkflowRun(ctx, parent, f.model.ID,
			runWorkflowArgs{ResumeRunID: run.id.String()}, uuid.New())
		require.NoError(t, err)

		// While the replay runs, the journal keeps the tail: the replayed
		// prefix never replaces it.
		step := f.steps(ctx, t, resumed, 1)[0]
		assert.Equal(t, "line 30", journalLogs()[0],
			"the replay does not move the journal backward")
		assert.Len(t, journalLogs(), maxWorkflowLogs)
		f.report(ctx, t, step.ChatID, `{"text":"hi"}`)
		res := testutil.RequireReceive(ctx, t, awaitRegistered(ctx, t, resumed))
		require.Equal(t, workflowStatusCompleted, res.Status, res.Error)
		require.Len(t, res.Logs, maxWorkflowLogs)
		assert.Equal(t, "line 30", res.Logs[0],
			"the replayed lines that the tail dropped append after it")
		assert.Equal(t, "line 79", res.Logs[49])
	})

	// The terminal flush of a run waits for an in-flight writer instead of
	// dropping the write, so the journal column ends with the final lines
	// even when the end of the run races a paced flush.
	t.Run("TerminalFlushWaitsForAnInFlightWriter", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: `
def main(args):
    log("before")
    agent("Never reports.")
`})
		fdb := forkWorkflowDB(t, f.db)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			row, err := fdb.GetWorkflowRun(ctx, run.id)
			if err != nil {
				return false
			}
			logs, _, _ := restoreWorkflowLogs(row.Logs)
			return len(logs) == 1
		}, testutil.IntervalFast)

		// Waiting for the first journal line can take most of the
		// sub-test budget on a slow CI runner, so the stop and the
		// post-stop checks run on a fresh context.
		ctx = chatdTestContext(t)

		// A held write token stands in for a flush that is in flight when
		// the stop lands. The terminal flush must wait for the token and
		// then write, instead of dropping the final lines.
		run.journalMu.Lock()
		run.logFlushInFlight = true
		run.journalMu.Unlock()
		<-run.journalLogWrite
		// The step row must exist before the stop lands: the stop fails
		// the open steps it finds, and a stop that races the insert would
		// leave the step to fail on its own context instead.
		f.steps(ctx, t, run, 1)
		// The stop waits for the unwinding run, whose terminal flush waits
		// for the held token, so the answer cannot be prompt in this test.
		// The stop still answers: it must return, not hang, and the request
		// must already be recorded so the unwinding run journals a stop.
		stopDone := make(chan error, 1)
		go func() { stopDone <- f.server.ForkStopWorkflowRun(ctx, parent.ID, run.id) }()
		select {
		case err := <-stopDone:
			require.NoError(t, err)
		case <-time.After(2 * workflowStopTimeout):
			t.Fatal("the stop did not answer after the bounded wait")
		}
		run.journalMu.Lock()
		run.logFlushInFlight = false
		run.journalMu.Unlock()
		run.journalLogWrite <- struct{}{}

		res := run.await(ctx, testutil.WaitShort)
		require.Equal(t, workflowStatusStopped, res.Status, res.Error)
		// The terminal flush races the snapshot: the write of seq 2 lands
		// just after this await returns, so the journal read waits for it.
		testutil.Eventually(ctx, t, func(context.Context) bool {
			row, err := fdb.GetWorkflowRun(ctx, run.id)
			if err != nil {
				return false
			}
			logs, _, _ := restoreWorkflowLogs(row.Logs)
			return len(logs) == 2
		}, testutil.IntervalFast)
		row, err := fdb.GetWorkflowRun(ctx, run.id)
		require.NoError(t, err)
		logs, _, _ := restoreWorkflowLogs(row.Logs)
		assert.Equal(t, []string{"before", `step "Never reports." failed: ` + workflowStopError}, logs,
			"the terminal flush landed the stopped snapshot lines")
	})

	// A capped run whose baseline holds a failure line the replay never
	// prints again: the echo suppresses by count, so the re-emitted failure
	// line of the journaled step keeps the count aligned and every line
	// lands once.
	t.Run("ReplaySkipsAnUnmatchedBaselineLine", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: `
def main(args):
    log("A")
    step = spawn("Fail.")
    wait([step])
    log("B")
    for i in range(3):
        log("NEW %d" % i)
    agent("Never reports.")
`})
		f.steps(ctx, t, run, 1)
		step := run.snapshot().Steps[0]
		childID := uuid.MustParse(step.ChatID)
		f.stopWithoutReport(ctx, t, step.ChatID)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return strings.Contains(strings.Join(f.userTexts(ctx, t, childID), "\n"), workflowReminder)
		}, testutil.IntervalFast)
		f.stopWithoutReport(ctx, t, step.ChatID)

		// The failure of the first step returns None from the wait, the
		// script logs on, and the trailing agent opens the second step and
		// holds the run: the passive fixture never runs a child chat. The
		// journal ends with the failure line of the step and the lines the
		// script printed after the wait.
		failLine := `step "Fail." failed: the step stopped without a workflow_report call`
		want := []string{"A", failLine, "B", "NEW 0", "NEW 1", "NEW 2"}
		fdb := forkWorkflowDB(t, f.db)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return assert.ObjectsAreEqual(want, journalRunLogs(ctx, t, fdb, run.id))
		}, testutil.IntervalFast)
		f.steps(ctx, t, run, 2)

		// The restart interrupts the run, and the resume replays the script
		// from its first line. The replay re-emits the failure line of the
		// journaled step, so the echo count covers every pre-restart line
		// without matching content. The replay then blocks in the trailing
		// agent, whose open step the stop fails.
		restarted := f.restartWorkflowServer(t)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return journalRunStatus(ctx, t, fdb, run.id) == workflowStatusInterrupted
		}, testutil.IntervalFast)
		resumed, err := restarted.server.startWorkflowRun(ctx, parent, f.model.ID,
			runWorkflowArgs{ResumeRunID: run.id.String()}, uuid.New())
		require.NoError(t, err)
		// The stop must land after the replay has consumed the echo count:
		// a stop that races the replay would suppress the failure line of
		// the open step as a replayed line. Reaching the trailing agent
		// means every baseline line already echoed.
		f.steps(ctx, t, resumed, 2)
		stopLine := `step "Never reports." failed: ` + workflowStopError
		wantStopped := append(append([]string(nil), want...), stopLine)
		require.NoError(t, restarted.server.ForkStopWorkflowRun(ctx, parent.ID, run.id))
		res := resumed.await(ctx, testutil.WaitShort)
		require.Equal(t, workflowStatusStopped, res.Status, res.Error)
		assert.Equal(t, wantStopped, res.Logs,
			"the replayed lines echo the baseline once and the failure lines land once")
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return assert.ObjectsAreEqual(wantStopped, journalRunLogs(ctx, t, fdb, run.id))
		}, testutil.IntervalFast)

		// A second restart keeps the terminal row, and its resume replays
		// the stored snapshot: the history stays one copy.
		restarted2 := restarted.restartWorkflowServer(t)
		require.Equal(t, workflowStatusStopped, journalRunStatus(ctx, t, fdb, run.id))
		terminal, err := restarted2.server.startWorkflowRun(ctx, parent, f.model.ID,
			runWorkflowArgs{ResumeRunID: run.id.String()}, uuid.New())
		require.NoError(t, err)
		out := terminal.await(ctx, testutil.WaitShort)
		require.Equal(t, workflowStatusStopped, out.Status, out.Error)
		assert.Equal(t, wantStopped, out.Logs,
			"the terminal replay must not duplicate the failure lines")
		assert.Equal(t, wantStopped, journalRunLogs(ctx, t, fdb, run.id))
	})

	// A capped run whose dropped prefix repeats the first retained line: the
	// echo suppresses by count, so the repeated line cannot consume the
	// wrong entries and the replay keeps one copy of every line.
	t.Run("ReplayKeepsARepeatedPrefix", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		// The journal caps at maxWorkflowLogs lines: lines 0..29 drop, the
		// retained tail starts at "line 30", and the dropped prefix holds
		// earlier copies of that line.
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: `
def main(args):
    for i in range(80):
        log("line %d" % i)
    agent("Never reports.")
`})
		fdb := forkWorkflowDB(t, f.db)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			logs := journalRunLogs(ctx, t, fdb, run.id)
			return len(logs) == maxWorkflowLogs
		}, testutil.IntervalFast)
		assert.Equal(t, "line 30", journalRunLogs(ctx, t, fdb, run.id)[0],
			"the retained lines are the last ones")

		restarted := f.restartWorkflowServer(t)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return journalRunStatus(ctx, t, fdb, run.id) == workflowStatusInterrupted
		}, testutil.IntervalFast)
		resumed, err := restarted.server.startWorkflowRun(ctx, parent, f.model.ID,
			runWorkflowArgs{ResumeRunID: run.id.String()}, uuid.New())
		require.NoError(t, err)
		step := f.steps(ctx, t, resumed, 1)[0]
		assert.Equal(t, "line 30", journalRunLogs(ctx, t, fdb, run.id)[0],
			"the replay does not move the journal backward")
		assert.Len(t, journalRunLogs(ctx, t, fdb, run.id), maxWorkflowLogs)
		f.report(ctx, t, step.ChatID, `{"text":"hi"}`)
		res := testutil.RequireReceive(ctx, t, awaitRegistered(ctx, t, resumed))
		require.Equal(t, workflowStatusCompleted, res.Status, res.Error)
		require.Len(t, res.Logs, maxWorkflowLogs)
		assert.Equal(t, "line 30", res.Logs[0],
			"the repeated prefix did not consume the baseline early")
		assert.Equal(t, "line 79", res.Logs[maxWorkflowLogs-1],
			"the last replayed line is live once")
	})

	// Identical failure lines across a resume: the replay re-emits the
	// journaled failure line, the echo suppresses it once, and the stop
	// appends exactly one more line.
	t.Run("ReplayReEmitsAFailureLineOnce", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: `
def main(args):
    step = spawn("Fail.")
    wait([step])
    agent("Never reports.")
`})
		f.steps(ctx, t, run, 1)
		step := run.snapshot().Steps[0]
		childID := uuid.MustParse(step.ChatID)
		f.stopWithoutReport(ctx, t, step.ChatID)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return strings.Contains(strings.Join(f.userTexts(ctx, t, childID), "\n"), workflowReminder)
		}, testutil.IntervalFast)
		f.stopWithoutReport(ctx, t, step.ChatID)

		failLine := `step "Fail." failed: the step stopped without a workflow_report call`
		fdb := forkWorkflowDB(t, f.db)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			logs := journalRunLogs(ctx, t, fdb, run.id)
			return len(logs) == 1 && logs[0] == failLine
		}, testutil.IntervalFast)

		restarted := f.restartWorkflowServer(t)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return journalRunStatus(ctx, t, fdb, run.id) == workflowStatusInterrupted
		}, testutil.IntervalFast)
		resumed, err := restarted.server.startWorkflowRun(ctx, parent, f.model.ID,
			runWorkflowArgs{ResumeRunID: run.id.String()}, uuid.New())
		require.NoError(t, err)
		f.steps(ctx, t, resumed, 2)
		stopLine := `step "Never reports." failed: ` + workflowStopError
		require.NoError(t, restarted.server.ForkStopWorkflowRun(ctx, parent.ID, run.id))
		res := resumed.await(ctx, testutil.WaitShort)
		require.Equal(t, workflowStatusStopped, res.Status, res.Error)
		assert.Equal(t, []string{failLine, stopLine}, res.Logs,
			"the identical failure line lands once per failure")
		assert.Equal(t, []string{failLine, stopLine}, journalRunLogs(ctx, t, fdb, run.id))
	})

	// A script that logs in a loop keeps a single write in flight, so its
	// journal column ends capped like the result.
	t.Run("CappedLikeTheResult", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: `
def main(args):
    for i in range(80):
        log("line %d" % i)
    return {}
`})
		fdb := forkWorkflowDB(t, f.db)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			row, err := fdb.GetWorkflowRun(ctx, run.id)
			return err == nil && row.Status == workflowStatusCompleted
		}, testutil.IntervalFast)
		row, err := fdb.GetWorkflowRun(ctx, run.id)
		require.NoError(t, err)
		logs, _, _ := restoreWorkflowLogs(row.Logs)
		require.Len(t, logs, maxWorkflowLogs, "the journal keeps the capped lines")
		assert.Equal(t, "line 30", logs[0], "the retained lines are the last ones")
		assert.Equal(t, "line 79", logs[len(logs)-1])
	})
}

// TestForkWorkflowReusedJournal covers the per-step reuse flag that the
// journal rows carry: a first-process run marks its steps not reused, and a
// resume that replays them marks the rows and the await result reused.
func TestForkWorkflowReusedJournal(t *testing.T) {
	t.Parallel()

	t.Run("FreshRunJournalsFalse", func(t *testing.T) {
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
		f.report(ctx, t, step.ChatID, `{"text":"hi"}`)
		res := run.await(ctx, testutil.WaitLong)
		require.Equal(t, workflowStatusCompleted, res.Status, res.Error)
		assert.False(t, res.Steps[0].Reused)
		fdb := forkWorkflowDB(t, f.db)
		stepRow, err := fdb.GetWorkflowStep(ctx, uuid.MustParse(step.ChatID))
		require.NoError(t, err)
		assert.False(t, stepRow.Reused)
	})

	t.Run("ResumeJournalsTheFlag", func(t *testing.T) {
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
		res := testutil.RequireReceive(ctx, t, awaitRegistered(ctx, t, run))
		require.Equal(t, workflowStatusCompleted, res.Status, res.Error)

		fdb := forkWorkflowDB(t, f.db)
		stepRows, err := fdb.GetWorkflowStepsByRun(ctx, run.id)
		require.NoError(t, err)
		require.Len(t, stepRows, 2)
		for _, srow := range stepRows {
			assert.False(t, srow.Reused, "a first-process step is not reused")
		}

		// A resume over the completed run replays every step and journals
		// the flag, so the run detail reads it without the snapshot.
		restarted := f.restartWorkflowServer(t)
		resumed, err := restarted.server.startWorkflowRun(ctx, parent, f.model.ID,
			runWorkflowArgs{ResumeRunID: run.id.String()}, uuid.New())
		require.NoError(t, err)
		out := resumed.await(ctx, testutil.WaitShort)
		require.Equal(t, workflowStatusCompleted, out.Status, out.Error)
		require.Len(t, out.Steps, 2)
		for i, step := range out.Steps {
			assert.True(t, step.Reused, "step %d must be reused", i)
		}
		stepRows, err = fdb.GetWorkflowStepsByRun(ctx, run.id)
		require.NoError(t, err)
		require.Len(t, stepRows, 2)
		for i, srow := range stepRows {
			assert.True(t, srow.Reused, "journaled step %d must read reused after the resume", i)
		}
	})

	t.Run("ReportDoesNotClearTheFlag", func(t *testing.T) {
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
		chatID := uuid.MustParse(step.ChatID)

		// A restart marks the open run interrupted, and the resume of the
		// new server re-runs the open step in its child chat, which
		// journals the reused flag of the row. The script blocks in the
		// wait for the child, which the passive fixture never wakes.
		restarted := f.restartWorkflowServer(t)
		fdb := forkWorkflowDB(t, f.db)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return journalRunStatus(ctx, t, fdb, run.id) == workflowStatusInterrupted
		}, testutil.IntervalFast)
		_, err := restarted.server.startWorkflowRun(ctx, parent, f.model.ID,
			runWorkflowArgs{ResumeRunID: run.id.String()}, uuid.New())
		require.NoError(t, err)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			row, err := fdb.GetWorkflowStep(ctx, chatID)
			return err == nil && row.Reused
		}, testutil.IntervalFast, "the re-run of the step must journal the flag")

		// A second restart interrupts the run again while its step row
		// already reads reused. The recovered child of the step then
		// reports through the journal, whose write passes Reused false:
		// the write must keep the flag, or the run detail loses the mark
		// the resume wrote.
		restarted2 := restarted.restartWorkflowServer(t)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return journalRunStatus(ctx, t, fdb, run.id) == workflowStatusInterrupted
		}, testutil.IntervalFast)
		child, err := f.db.GetChatByID(ctx, chatID)
		require.NoError(t, err)
		tools, err := restarted2.server.appendWorkflowReportTool(nil, child)
		require.NoError(t, err)
		require.Len(t, tools, 1)
		resp, err := tools[0].Run(ctx, fantasy.ToolCall{Input: `{"text":"hi again"}`})
		require.NoError(t, err)
		assert.False(t, resp.IsError, resp.Content)
		stepRow, err := fdb.GetWorkflowStep(ctx, chatID)
		require.NoError(t, err)
		assert.True(t, stepRow.Reused,
			"a late report on a reused step must not clear the reused flag")
		require.True(t, stepRow.Value.Valid)
		assert.JSONEq(t, `{"text":"hi again"}`, string(stepRow.Value.RawMessage))
	})
}

func TestForkWorkflowStop(t *testing.T) {
	t.Parallel()

	t.Run("StopsALiveRun", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{
			Script: journalReviewScript,
			Args:   map[string]any{"files": []string{"a.go", "b.go"}},
		})
		f.steps(ctx, t, run, 2)

		require.NoError(t, f.server.ForkStopWorkflowRun(ctx, parent.ID, run.id))

		// The run reads stopped, and its snapshot carries the failed steps.
		res := run.await(ctx, testutil.WaitShort)
		assert.Equal(t, workflowStatusStopped, res.Status, res.Error)
		assert.Equal(t, workflowStopError, res.Error)
		require.Len(t, res.Steps, 2)
		for i, step := range res.Steps {
			assert.Equal(t, stepStatusFailed, step.Status, "step %d", i)
			assert.Equal(t, workflowStopError, step.Error, "step %d", i)
		}

		// The journal says stopped with the snapshot, so a resume replays it
		// instead of re-running anything.
		fdb := forkWorkflowDB(t, f.db)
		assert.Equal(t, workflowStatusStopped, journalRunStatus(ctx, t, fdb, run.id))
		row, err := fdb.GetWorkflowRun(ctx, run.id)
		require.NoError(t, err)
		require.True(t, row.Result.Valid, "a stopped run keeps its snapshot")
		assert.True(t, row.FinishedAt.Valid, "a stopped run keeps its end time")

		terminal, err := f.server.startWorkflowRun(ctx, parent, f.model.ID,
			runWorkflowArgs{ResumeRunID: run.id.String()}, uuid.New())
		require.NoError(t, err)
		out := terminal.await(ctx, testutil.WaitShort)
		assert.Equal(t, workflowStatusStopped, out.Status)

		// A stopped run cannot stop again.
		assert.ErrorIs(t, f.server.ForkStopWorkflowRun(ctx, parent.ID, run.id), ErrForkWorkflowRunTerminal)
		// An unknown run of the chat is not found, and neither is another
		// chat's run.
		assert.ErrorIs(t, f.server.ForkStopWorkflowRun(ctx, parent.ID, uuid.New()), ErrForkWorkflowRunNotFound)
		assert.ErrorIs(t, f.server.ForkStopWorkflowRun(ctx, uuid.New(), run.id), ErrForkWorkflowRunNotFound)
	})

	// A stop of an interrupted run this replica does not hold journals a
	// snapshot with the retained lines, so await and a terminal replay read
	// the same history a stop of a held run produces.
	t.Run("JournalOnlyStopCarriesTheLogs", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: `
def main(args):
    log("first line")
    log("second line")
    agent("Never reports.")
`})
		fdb := forkWorkflowDB(t, f.db)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			logs := journalRunLogs(ctx, t, fdb, run.id)
			return len(logs) == 2
		}, testutil.IntervalFast)

		// The registry drop makes the stop go through stopJournaledRun on a
		// row that still says running: the journal-only path of a live run.
		f.server.workflows.remove(run.id)
		require.NoError(t, f.server.ForkStopWorkflowRun(ctx, parent.ID, run.id))

		row, err := fdb.GetWorkflowRun(ctx, run.id)
		require.NoError(t, err)
		require.Equal(t, workflowStatusStopped, row.Status)
		require.True(t, row.Result.Valid, "a journal-only stop keeps a snapshot")
		var snapshot runWorkflowResult
		require.NoError(t, json.Unmarshal(row.Result.RawMessage, &snapshot))
		assert.Equal(t, workflowStatusStopped, snapshot.Status)
		assert.Equal(t, []string{"first line", "second line"}, snapshot.Logs,
			"the stop snapshot carries the retained live lines")

		// await_workflow on the terminal row and a terminal replay read the
		// same logs.
		res, found, err := f.server.workflowResultFromJournal(ctx, parent.ID, run.id)
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, []string{"first line", "second line"}, res.Logs,
			"await on a journal-only stop reads the logs")
		terminal, err := f.server.startWorkflowRun(ctx, parent, f.model.ID,
			runWorkflowArgs{ResumeRunID: run.id.String()}, uuid.New())
		require.NoError(t, err)
		out := terminal.await(ctx, testutil.WaitShort)
		require.Equal(t, workflowStatusStopped, out.Status, out.Error)
		assert.Equal(t, []string{"first line", "second line"}, out.Logs,
			"a terminal replay reads the logs of a journal-only stop")
	})

	// A stop whose run already failed a step keeps one copy of the failure
	// line: the steps the stop itself interrupted log their lines, the ones
	// that failed earlier already sat in the logs.
	t.Run("StopAfterAFailedStepDoesNotDuplicateTheLine", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: `
def main(args):
    step = spawn("Fail.")
    wait([step])
    agent("Never reports.")
`})
		f.steps(ctx, t, run, 1)
		step := run.snapshot().Steps[0]
		childID := uuid.MustParse(step.ChatID)
		f.stopWithoutReport(ctx, t, step.ChatID)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return strings.Contains(strings.Join(f.userTexts(ctx, t, childID), "\n"), workflowReminder)
		}, testutil.IntervalFast)
		f.stopWithoutReport(ctx, t, step.ChatID)

		// The failed step logged its line, and the trailing agent opens the
		// second step and holds the run: the passive fixture never runs a
		// child chat. The stop then fails only that open step, so the
		// failure line of the first step must stay one.
		f.steps(ctx, t, run, 2)
		want := `step "Fail." failed: the step stopped without a workflow_report call`
		stopLine := `step "Never reports." failed: ` + workflowStopError
		require.NoError(t, f.server.ForkStopWorkflowRun(ctx, parent.ID, run.id))
		res := run.await(ctx, testutil.WaitShort)
		require.Equal(t, workflowStatusStopped, res.Status, res.Error)
		require.Len(t, res.Steps, 2)
		require.Equal(t, stepStatusFailed, res.Steps[0].Status)
		require.Equal(t, stepStatusFailed, res.Steps[1].Status)
		require.Equal(t, []string{want, stopLine}, res.Logs,
			"the failed step logs its line once and the stop logs only its own step")
		fdb := forkWorkflowDB(t, f.db)
		require.Equal(t, workflowStatusStopped, journalRunStatus(ctx, t, fdb, run.id))
		require.Equal(t, []string{want, stopLine}, journalRunLogs(ctx, t, fdb, run.id))

		// The restart keeps the terminal row, and its resume replays the
		// stored snapshot: the failure lines stay one copy.
		restarted := f.restartWorkflowServer(t)
		require.Equal(t, workflowStatusStopped, journalRunStatus(ctx, t, fdb, run.id))
		terminal, err := restarted.server.startWorkflowRun(ctx, parent, f.model.ID,
			runWorkflowArgs{ResumeRunID: run.id.String()}, uuid.New())
		require.NoError(t, err)
		out := terminal.await(ctx, testutil.WaitShort)
		require.Equal(t, workflowStatusStopped, out.Status, out.Error)
		assert.Equal(t, []string{want, stopLine}, out.Logs,
			"the failure line of the step that failed earlier must not log twice")
		assert.Equal(t, []string{want, stopLine}, journalRunLogs(ctx, t, fdb, run.id))
	})

	t.Run("InterruptedRunIsNotStoppable", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{
			Script: journalReviewScript,
			Args:   map[string]any{"files": []string{"a.go", "b.go"}},
		})
		// Wait for the steps to be journaled before the restart: the sweep
		// of the new server only needs the run row, and the step rows must
		// exist for the open-step assertions below.
		f.steps(ctx, t, run, 2)
		// The restart marks the run interrupted.
		restarted := f.restartWorkflowServer(t)
		fdb := forkWorkflowDB(t, f.db)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return journalRunStatus(ctx, t, fdb, run.id) == workflowStatusInterrupted
		}, testutil.IntervalFast)

		// The new server cannot stop an interrupted run: the agent resumes it.
		assert.ErrorIs(t, restarted.server.ForkStopWorkflowRun(ctx, parent.ID, run.id), ErrForkWorkflowRunTerminal)
		// The steps stay open for the resume.
		stepRows, err := fdb.GetWorkflowStepsByRun(ctx, run.id)
		require.NoError(t, err)
		require.Len(t, stepRows, 2)
		for _, srow := range stepRows {
			assert.NotEqual(t, stepStatusFailed, srow.Status, "an interrupted run keeps its open steps")
		}
	})
}

// TestForkWorkflowReportJournal covers workflow_report on a step child
// whose run is no longer live: after a restart, chat recovery re-runs the
// child while its run sits interrupted in the journal.
func TestForkWorkflowReportJournal(t *testing.T) {
	t.Parallel()

	t.Run("RecoveredStepChildJournalsItsReport", func(t *testing.T) {
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

		// The recovered turn builds workflow_report from the chat, the way
		// the turn environment does.
		chatID := uuid.MustParse(step.ChatID)
		child, err := f.db.GetChatByID(ctx, chatID)
		require.NoError(t, err)
		tools, err := restarted.server.appendWorkflowReportTool(nil, child)
		require.NoError(t, err)
		require.Len(t, tools, 1)
		resp, err := tools[0].Run(ctx, fantasy.ToolCall{Input: `{"text":"hi"}`})
		require.NoError(t, err)
		assert.False(t, resp.IsError, resp.Content)

		// The journal holds the value while the run stays interrupted.
		stepRow, err := fdb.GetWorkflowStep(ctx, chatID)
		require.NoError(t, err)
		assert.Equal(t, stepStatusReported, stepRow.Status)
		require.True(t, stepRow.Value.Valid)
		assert.JSONEq(t, `{"text":"hi"}`, string(stepRow.Value.RawMessage))
		assert.Equal(t, workflowStatusInterrupted, journalRunStatus(ctx, t, fdb, run.id))

		// The resume replays the journaled value without running the child
		// again.
		promptsBefore := len(f.userTexts(ctx, t, chatID))
		resumed, err := restarted.server.startWorkflowRun(ctx, parent, f.model.ID,
			runWorkflowArgs{ResumeRunID: run.id.String()}, uuid.New())
		require.NoError(t, err)
		res := testutil.RequireReceive(ctx, t, awaitRegistered(ctx, t, resumed))
		require.Equal(t, workflowStatusCompleted, res.Status, res.Error)
		assert.JSONEq(t, `{"text":"hi"}`, string(res.Result))
		require.Len(t, res.Steps, 1)
		assert.True(t, res.Steps[0].Reused)
		assert.Equal(t, promptsBefore, len(f.userTexts(ctx, t, chatID)),
			"the resumed run must not re-run the reported child")
	})

	t.Run("ReportOnEndedRunFails", func(t *testing.T) {
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
		f.report(ctx, t, step.ChatID, `{"text":"hi"}`)
		res := testutil.RequireReceive(ctx, t, awaitRegistered(ctx, t, run))
		require.Equal(t, workflowStatusCompleted, res.Status, res.Error)

		chatID := uuid.MustParse(step.ChatID)
		child, err := f.db.GetChatByID(ctx, chatID)
		require.NoError(t, err)
		tools, err := f.server.appendWorkflowReportTool(nil, child)
		require.NoError(t, err)
		require.Len(t, tools, 1)
		resp, err := tools[0].Run(ctx, fantasy.ToolCall{Input: `{"text":"late"}`})
		require.NoError(t, err)
		assert.True(t, resp.IsError, resp.Content)
		assert.Contains(t, resp.Content, "already ended")

		fdb := forkWorkflowDB(t, f.db)
		stepRow, err := fdb.GetWorkflowStep(ctx, chatID)
		require.NoError(t, err)
		assert.JSONEq(t, `{"text":"hi"}`, string(stepRow.Value.RawMessage),
			"a late report must not overwrite the journaled value")
	})

	t.Run("ReportWithoutJournalRowFails", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)

		// A step child whose run row the journal lost cannot answer a
		// report with acceptance: there is nothing to record the value on.
		tools, err := f.server.appendWorkflowReportTool(nil, database.Chat{
			ID:           uuid.New(),
			ParentChatID: uuid.NullUUID{UUID: uuid.New(), Valid: true},
			Labels:       database.StringMap{workflowRunLabel: uuid.NewString()},
			DynamicTools: f.stepTools(t),
		})
		require.NoError(t, err)
		require.Len(t, tools, 1)
		resp, err := tools[0].Run(ctx, fantasy.ToolCall{Input: `{"text":"hi"}`})
		require.NoError(t, err)
		assert.True(t, resp.IsError, resp.Content)
		assert.Contains(t, resp.Content, "not in the journal")
	})
}
