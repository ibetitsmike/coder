package chatd

// This file is fork code: tests for workflow phases, the phase() built-in
// and the optional top-level PHASES list of the fork-workflows experiment.
// The fixture plays each step child by hand, the way the engine tests do.

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
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

// awaitPhases waits until the run reports the given phase list. It does
// not wait for a current phase: a caller that reads the current phase
// right after the wait must first wait for the step that owns it.
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

		// The first step only exists once phase("plan") has run, so the
		// current phase is settled by the time the step is visible.
		steps := f.steps(ctx, t, run, 1)
		assert.Equal(t, "plan", steps[0].Phase)
		snap := run.snapshot()
		assert.Equal(t, "plan", snap.CurrentPhase)
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

	t.Run("RepeatedPhaseCallSkipsTheJournalWrite", func(t *testing.T) {
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		// A script that re-enters its current phase in a loop is a no-op: it
		// must not journal a write per call.
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: `
PHASES = ["plan"]

def main(args):
    phase("plan")
    for i in range(50):
        phase("plan")
    agent("Plan.", title = "plan")
`})
		steps := f.steps(ctx, t, run, 1)
		assert.Equal(t, "plan", steps[0].Phase)
		f.report(ctx, t, steps[0].ChatID, `{"text":"done"}`)
		res := testutil.RequireReceive(ctx, t, awaitRegistered(ctx, t, run))
		require.Equal(t, workflowStatusCompleted, res.Status, res.Error)
		assert.Equal(t, []string{"plan"}, res.Phases)
		assert.Equal(t, "plan", res.CurrentPhase)
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
		// The run holds the declared list from Init, and no phase() call
		// has run in this script, so the current phase stays empty no
		// matter how far main got.
		awaitPhases(ctx, t, run, []string{"one", "two"})

		// The declared list is journaled before main runs, so the run
		// API shows it while the run is live, before any phase() call.
		fdb := forkWorkflowDB(t, f.db)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			row, err := fdb.GetWorkflowRun(ctx, run.id)
			return err == nil && assert.ObjectsAreEqual([]string{"one", "two"}, row.Phases)
		}, testutil.IntervalFast)

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
			"NULName":    {script: "PHASES = [\"a\\u0000b\"]\n\ndef main(args):\n    pass\n", want: "must not hold control"},
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
		// Each subtest builds its own fixture: parallel subtests that share
		// one context can exhaust its deadline on a slow CI box.
		tooLong := "def main(args):\n    phase(\"x\" * " + strconv.Itoa(maxWorkflowPhaseName+1) + ")\n"
		for name, tc := range map[string]struct {
			script string
			want   string
		}{
			"Empty":     {script: "def main(args):\n    phase(\"  \")\n", want: "must not be empty"},
			"NotString": {script: "def main(args):\n    phase(1)\n", want: "want string"},
			"TooLong":   {script: tooLong, want: "longer than"},
			"NUL":       {script: "def main(args):\n    phase(\"a\\u0000b\")\n", want: "must not hold control"},
			"Control":   {script: "def main(args):\n    phase(\"a\\tb\")\n", want: "must not hold control"},
			"Format":    {script: "def main(args):\n    phase(\"a\u200bb\")\n", want: "must not hold control"},
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				f := newForkWorkflowFixture(t)
				ctx := chatdTestContext(t)
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

	t.Run("SavedWorkflowPhasesSeedTheRun", func(t *testing.T) {
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		// saveUserWorkflow stores the phases plan and work (see the fixture).
		f.saveUserWorkflow(t, "seeded", "def main(args):\n    phase(\"work\")\n    agent(\"Work.\", title = \"work\")\n", greetArgsSchema)
		run := f.start(ctx, t, parent, runWorkflowArgs{Name: "seeded", Args: map[string]any{"topic": "x"}})

		// The saved phases are visible before the script sets a phase of
		// its own, both in memory and in the journal row.
		awaitPhases(ctx, t, run, []string{"plan", "work"})
		fdb := forkWorkflowDB(t, f.db)
		row, err := fdb.GetWorkflowRun(ctx, run.id)
		require.NoError(t, err)
		assert.Equal(t, []string{"plan", "work"}, row.Phases)

		steps := f.steps(ctx, t, run, 1)
		assert.Equal(t, "work", steps[0].Phase)
		f.report(ctx, t, steps[0].ChatID, `{"text":"done"}`)
		res := testutil.RequireReceive(ctx, t, awaitRegistered(ctx, t, run))
		require.Equal(t, workflowStatusCompleted, res.Status, res.Error)
		// phase() does not add a name the saved list already holds.
		assert.Equal(t, []string{"plan", "work"}, res.Phases)
		assert.Equal(t, "work", res.CurrentPhase)
	})

	t.Run("StopKeepsThePhases", func(t *testing.T) {
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: phasePlanScript})
		steps := f.steps(ctx, t, run, 1)
		assert.Equal(t, "plan", steps[0].Phase)

		// stopJournaledRun serves the live-run path of a stop through
		// requestStop and finish, which write the phases themselves. This
		// covers the restart path instead: the row looks like a run left
		// behind by a restart, so the stop sweep goes through
		// stopJournaledRun alone.
		f.server.workflows.remove(run.id)
		require.NoError(t, f.server.ForkStopWorkflowRun(ctx, parent.ID, run.id))

		fdb := forkWorkflowDB(t, f.db)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return journalRunStatus(ctx, t, fdb, run.id) == workflowStatusStopped
		}, testutil.IntervalFast)
		row, err := fdb.GetWorkflowRun(ctx, run.id)
		require.NoError(t, err)
		assert.Equal(t, []string{"plan", "build", "review"}, row.Phases,
			"stopping a run keeps its phases")
		assert.Equal(t, "plan", row.CurrentPhase)
	})

	t.Run("CapCountsSavedAndDeclaredPhasesTogether", func(t *testing.T) {
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		// A saved workflow capped at 32 phases plus a script that declares
		// one more fails the run instead of reaching 64.
		saved := make([]string, maxWorkflowPhases)
		for i := range saved {
			saved[i] = fmt.Sprintf("saved%02d", i)
		}
		_, err := forkWorkflowDB(t, f.db).InsertUserSavedWorkflow(ctx, forkdb.InsertUserSavedWorkflowParams{
			OrganizationID: f.org.ID,
			UserID:         f.user.ID,
			CreatedBy:      f.user.ID,
			Name:           "full",
			Description:    "A workflow with the maximum phases.",
			Script:         "PHASES = [\"one more\"]\n\ndef main(args):\n    pass\n",
			Phases:         saved,
		})
		require.NoError(t, err)
		// The saved script declares one more PHASES name on top of the 32
		// saved phases, so the run fails instead of reaching 64.
		run := f.start(ctx, t, parent, runWorkflowArgs{Name: "full"})
		res := run.await(ctx, testutil.WaitShort)
		require.Equal(t, workflowStatusFailed, res.Status)
		assert.Contains(t, res.Error, "more than 32 phases")
	})

	t.Run("PhaseBuiltinPastTheCapFailsTheRun", func(t *testing.T) {
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		// A script that declares the cap and then observes one more with
		// phase() fails the run, the way a PHASES list past the cap does.
		script := "PHASES = [\"p\" + str(i) for i in range(" + strconv.Itoa(maxWorkflowPhases) + ")]\n\ndef main(args):\n    phase(\"one more\")\n"
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: script})
		res := run.await(ctx, testutil.WaitShort)
		require.Equal(t, workflowStatusFailed, res.Status)
		assert.Contains(t, res.Error, "would exceed the 32-phase cap")
	})

	t.Run("DeclaringPastTheCapLeavesNoPhasesBehind", func(t *testing.T) {
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		// A declaration that fails the cap must not leak its names into
		// the run state a terminal result or journal row exposes.
		script := "PHASES = [\"p\" + str(i) for i in range(" + strconv.Itoa(maxWorkflowPhases+1) + ")]\n\ndef main(args):\n    pass\n"
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: script})
		res := run.await(ctx, testutil.WaitShort)
		require.Equal(t, workflowStatusFailed, res.Status)
		assert.Contains(t, res.Error, "at most 32 are allowed")
		assert.Empty(t, res.Phases)
		fdb := forkWorkflowDB(t, f.db)
		row, err := fdb.GetWorkflowRun(ctx, run.id)
		require.NoError(t, err)
		assert.Empty(t, row.Phases)
	})

	t.Run("DeclaringSavedPhasesPlusOnePastTheCapLeavesOnlySavedPhases", func(t *testing.T) {
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		saved := make([]string, maxWorkflowPhases)
		for i := range saved {
			saved[i] = fmt.Sprintf("saved%02d", i)
		}
		_, err := forkWorkflowDB(t, f.db).InsertUserSavedWorkflow(ctx, forkdb.InsertUserSavedWorkflowParams{
			OrganizationID: f.org.ID,
			UserID:         f.user.ID,
			CreatedBy:      f.user.ID,
			Name:           "full",
			Description:    "A workflow with the maximum phases.",
			Script:         "PHASES = [\"declared\"]\n\ndef main(args):\n    pass\n",
			Phases:         saved,
		})
		require.NoError(t, err)
		run := f.start(ctx, t, parent, runWorkflowArgs{Name: "full"})
		res := run.await(ctx, testutil.WaitShort)
		require.Equal(t, workflowStatusFailed, res.Status)
		assert.Contains(t, res.Error, "more than 32 phases")
		// Only the saved phases stay visible; the failed declaration adds
		// nothing to the snapshot or the journal row.
		assert.Equal(t, saved, res.Phases)
		fdb := forkWorkflowDB(t, f.db)
		row, err := fdb.GetWorkflowRun(ctx, run.id)
		require.NoError(t, err)
		assert.Equal(t, saved, row.Phases)
	})
}
