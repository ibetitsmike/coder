package chatd

// This file is fork code: tests for workflow phases, the phase() built-in
// and the optional top-level PHASES list of the fork-workflows experiment.
// The fixture plays each step child by hand, the way the engine tests do.

import (
	"context"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/testutil"
)

// phasePlanScript declares three phases and walks them with one step each.
// The third step never reports, so the run stays in its last phase while
// the test reads the journal.
const phasePlanScript = `
PHASES = ["plan", "build", "review"]

def main(args):
    phase("plan")
    agent("Plan.", title = "plan")
    phase("build")
    agent("Build.", title = "build")
    phase("review")
    agent("Review.", title = "review")
`

// awaitPhases waits until the run reports the given phase list.
func awaitPhases(ctx context.Context, t *testing.T, run *workflowRun, want []string) {
	t.Helper()
	testutil.Eventually(ctx, t, func(context.Context) bool {
		snap := run.snapshot()
		if len(snap.Phases) != len(want) {
			return false
		}
		for i := range want {
			if snap.Phases[i] != want[i] {
				return false
			}
		}
		return true
	}, testutil.IntervalFast)
}

func TestForkWorkflowPhases(t *testing.T) {
	t.Parallel()

	t.Run("PhaseTagsStepsAndJournal", func(t *testing.T) {
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: phasePlanScript})

		// The declared phases are visible before the first step starts.
		awaitPhases(ctx, t, run, []string{"plan", "build", "review"})
		snap := run.snapshot()
		assert.Equal(t, "plan", snap.CurrentPhase)

		steps := f.steps(ctx, t, run, 1)
		assert.Equal(t, "plan", steps[0].Phase)
		f.report(ctx, t, steps[0].ChatID, `{"text":"plan"}`)
		steps = f.steps(ctx, t, run, 2)
		assert.Equal(t, "build", steps[1].Phase)
		f.report(ctx, t, steps[1].ChatID, `{"text":"build"}`)
		steps = f.steps(ctx, t, run, 3)
		assert.Equal(t, "review", steps[2].Phase)
		// Step three stays open: the run keeps current_phase review.

		fdb := forkWorkflowDB(t, f.db)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			row, err := fdb.GetWorkflowRun(ctx, run.id)
			return err == nil && row.CurrentPhase == "review" && len(row.Phases) == 3
		}, testutil.IntervalFast)
		stepRows, err := fdb.GetWorkflowStepsByRun(ctx, run.id)
		require.NoError(t, err)
		require.Len(t, stepRows, 3)
		assert.Equal(t, "plan", stepRows[0].Phase)
		assert.Equal(t, "build", stepRows[1].Phase)
		assert.Equal(t, "review", stepRows[2].Phase)
	})

	t.Run("UndeclaredNameIsAcceptedAndAppended", func(t *testing.T) {
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: `
PHASES = ["plan"]

def main(args):
    phase("plan")
    agent("Plan.", title = "plan")
    phase("surprise")
    agent("Surprise.", title = "surprise")
`})
		steps := f.steps(ctx, t, run, 1)
		assert.Equal(t, "plan", steps[0].Phase)
		f.report(ctx, t, steps[0].ChatID, `{"text":"plan"}`)
		steps = f.steps(ctx, t, run, 2)
		assert.Equal(t, "surprise", steps[1].Phase)

		// The second step never reports: the run stays open while the
		// journal already holds the undeclared name.
		fdb := forkWorkflowDB(t, f.db)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			row, err := fdb.GetWorkflowRun(ctx, run.id)
			return err == nil && row.CurrentPhase == "surprise"
		}, testutil.IntervalFast)
		row, err := fdb.GetWorkflowRun(ctx, run.id)
		require.NoError(t, err)
		assert.Equal(t, []string{"plan", "surprise"}, row.Phases,
			"the undeclared name joins the declared one, in first-reach order")
	})

	t.Run("DeclaredListIsShownBeforeTheFirstStep", func(t *testing.T) {
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: `
PHASES = ["one", "two"]

def main(args):
    agent("Only step.", title = "solo")
`})
		// The run holds the declared list from Init. Read the list the moment
		// it appears; main can already have started the first step by then.
		awaitPhases(ctx, t, run, []string{"one", "two"})

		step := f.steps(ctx, t, run, 1)[0]
		assert.Empty(t, step.Phase, "a step outside any phase has no phase")
		f.report(ctx, t, step.ChatID, `{"text":"done"}`)
	})

	t.Run("BadDeclarationsFailTheRun", func(t *testing.T) {
		t.Parallel()
		tooMany := "PHASES = [\"p\" + str(i) for i in range(" + strconv.Itoa(maxWorkflowPhases+1) + ")]\n\ndef main(args):\n    pass\n"
		for name, tc := range map[string]struct {
			script string
			want   string
		}{
			"NotAList":   {script: "PHASES = \"plan\"\n\ndef main(args):\n    pass\n", want: "PHASES must be a list"},
			"NotStrings": {script: "PHASES = [1]\n\ndef main(args):\n    pass\n", want: "PHASES[0] must be a string"},
			"EmptyName":  {script: "PHASES = [\" \"]\n\ndef main(args):\n    pass\n", want: "PHASES[0] must not be empty"},
			"TooMany":    {script: tooMany, want: "at most"},
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				f := newForkWorkflowFixture(t)
				ctx := chatdTestContext(t)
				parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
				run := f.start(ctx, t, parent, runWorkflowArgs{Script: tc.script})
				res := run.await(ctx, testutil.WaitShort)
				require.Equal(t, workflowStatusFailed, res.Status, "a broken PHASES declaration fails the run")
				assert.Contains(t, res.Error, tc.want)
			})
		}
	})

	t.Run("PhaseBuiltinsRejectBadNames", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		tooLong := "def main(args):\n    phase(\"x\" * " + strconv.Itoa(maxWorkflowPhaseName+1) + ")\n"
		for name, tc := range map[string]struct {
			script string
			want   string
		}{
			"Empty":     {script: "def main(args):\n    phase(\"  \")\n", want: "must not be empty"},
			"NotString": {script: "def main(args):\n    phase(1)\n", want: "want string"},
			"TooLong":   {script: tooLong, want: "longer than"},
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
				run := f.start(ctx, t, parent, runWorkflowArgs{Script: tc.script})
				res := run.await(ctx, testutil.WaitShort)
				require.Equal(t, workflowStatusFailed, res.Status)
				assert.Contains(t, res.Error, tc.want)
			})
		}
	})

	t.Run("ResumeKeepsPhases", func(t *testing.T) {
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: phasePlanScript})
		steps := f.steps(ctx, t, run, 1)
		assert.Equal(t, "plan", steps[0].Phase)
		f.report(ctx, t, steps[0].ChatID, `{"text":"plan"}`)
		steps = f.steps(ctx, t, run, 2)
		assert.Equal(t, "build", steps[1].Phase)
		f.report(ctx, t, steps[1].ChatID, `{"text":"build"}`)
		steps = f.steps(ctx, t, run, 3)
		assert.Equal(t, "review", steps[2].Phase)

		// The restart interrupts the open run; the resume re-runs the open
		// steps. The phase list and the current phase come back from the
		// journal.
		restarted := f.restartWorkflowServer(t)
		fdb := forkWorkflowDB(t, f.db)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return journalRunStatus(ctx, t, fdb, run.id) == workflowStatusInterrupted
		}, testutil.IntervalFast)

		resumed, err := restarted.server.startWorkflowRun(ctx, parent, f.model.ID,
			runWorkflowArgs{ResumeRunID: run.id.String()}, uuid.New())
		require.NoError(t, err)
		awaitPhases(ctx, t, resumed, []string{"plan", "build", "review"})

		resumedSteps := f.steps(ctx, t, resumed, 3)
		for i, step := range resumedSteps {
			assert.Equal(t, steps[i].Phase, step.Phase, "re-run step %d keeps its phase", i)
		}
		// The re-run reports one chat at a time, in run order: a chat
		// reports only when the test ends its turn, and an earlier chat
		// must report before the script starts a later one.
		for i := range resumedSteps {
			f.report(ctx, t, resumedSteps[i].ChatID, `{"text":"done"}`)
		}
		res := testutil.RequireReceive(ctx, t, awaitRegistered(ctx, t, resumed))
		require.Equal(t, workflowStatusCompleted, res.Status, res.Error)
		assert.Equal(t, []string{"plan", "build", "review"}, res.Phases)
		assert.Equal(t, "review", res.CurrentPhase)
	})

	t.Run("RunWithoutPhasesLooksLikeToday", func(t *testing.T) {
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: `
def main(args):
    return agent("Say hi.", title = "hi")
`})
		step := f.steps(ctx, t, run, 1)[0]
		assert.Empty(t, step.Phase)
		f.report(ctx, t, step.ChatID, `{"text":"hi"}`)
		res := testutil.RequireReceive(ctx, t, awaitRegistered(ctx, t, run))
		require.Equal(t, workflowStatusCompleted, res.Status, res.Error)
		assert.Empty(t, res.Phases, "a script without phases reports none")
		assert.Empty(t, res.CurrentPhase)
	})
}
