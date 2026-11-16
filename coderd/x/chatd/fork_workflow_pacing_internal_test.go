package chatd

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/testutil"
)

// TestForkWorkflowLogPacing pins the write pacing of the live-log
// write-through: a script that logs continuously must produce at most one
// journal write per workflowLogFlushInterval, whether the write comes from
// the immediate attempt of a line or from the paced fallback timer.
func TestForkWorkflowLogPacing(t *testing.T) {
	t.Parallel()

	// TimerWritePacesTheNextLine holds the token, waits for the fallback
	// timer to carry a burst (the timer path of the write-through),
	// releases, and then logs one more line. The new line must wait out
	// the rest of the interval instead of writing back to back with the
	// timer write: at most one write per interval, whichever path
	// produced the last one.
	t.Run("TimerWritePacesTheNextLine", func(t *testing.T) {
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
		journalLogs := func() []string {
			row, err := fdb.GetWorkflowRun(ctx, run.id)
			require.NoError(t, err)
			logs, _, _ := restoreWorkflowLogs(row.Logs)
			return logs
		}
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return len(journalLogs()) == 1
		}, testutil.IntervalFast)

		// A stale stamp makes the attempt of the held line go immediate,
		// and the held token makes it drop, so the paced fallback timer
		// becomes the writer of the held line.
		release := holdJournalWriteToken(t, run)
		run.journalMu.Lock()
		run.lastLogFlush = time.Now().Add(-workflowLogFlushInterval)
		run.journalMu.Unlock()
		run.log("held line")
		testutil.Eventually(ctx, t, func(context.Context) bool {
			run.journalMu.Lock()
			armed := run.logFlushTimer != nil
			run.journalMu.Unlock()
			return armed
		}, testutil.IntervalFast)

		// Release: the fallback timer fires into the free token and
		// writes, which stamps its firing. The journal catches up without
		// another log() call, so the write came from the timer path.
		release()
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return len(journalLogs()) == 2
		}, testutil.IntervalSlow)
		assert.Equal(t, []string{"first line", "held line"}, journalLogs())
		// The timer write must have stamped its firing, and the token
		// stays free for the rest of the sub-test: the stamp, not a held
		// token, is what must pace the next line. A line logged now sits
		// inside the same interval, so its immediate attempt must not
		// start a second write back to back: it must arm a paced timer
		// instead of flushing now.
		run.journalMu.Lock()
		stamp := run.lastLogFlush
		run.journalMu.Unlock()
		assert.False(t, stamp.Before(time.Now().Add(-workflowLogFlushInterval)),
			"the timer write must stamp the attempt, so the next line paces behind it")

		run.log("paced line")
		// The paced line must wait out the rest of the interval, so the
		// journal must still hold two lines half a second after the log():
		// an unpaced path writes the line at once, which this fixed probe
		// observes as a third line inside the same interval. The probe
		// window is far under the interval, so a paced path cannot have
		// fired its timer yet and this cannot fail for a slow runner.
		time.Sleep(500 * time.Millisecond)
		assert.Equal(t, []string{"first line", "held line"}, journalLogs(),
			"the paced line must wait out the interval instead of writing back to back with the timer write")
		// The timer the line armed carries it at the interval's end: the
		// line is not stranded, the journal catches up on its own.
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return len(journalLogs()) == 3
		}, testutil.IntervalSlow)
		assert.Equal(t, []string{"first line", "held line", "paced line"}, journalLogs())
	})

	// SustainedLogging pins the one-write-per-interval bound end to end:
	// a burst of lines one per fast tick must reach the flush path a
	// bounded number of times, counted in the run's flush attempts, not
	// once per line.
	t.Run("SustainedLoggingWritesOncePerInterval", func(t *testing.T) {
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
			return len(journalRunLogs(ctx, t, fdb, run.id)) == 1
		}, testutil.IntervalFast)

		// No token is held: the burst writes through for real, and the
		// test measures how often the write-through attempts the journal,
		// not whether it can write. A stale stamp makes the burst's first
		// attempt go immediate, so the first line cannot hide behind the
		// interval.
		base := func() int {
			run.journalMu.Lock()
			defer run.journalMu.Unlock()
			run.lastLogFlush = time.Now().Add(-workflowLogFlushInterval)
			return run.flushAttempts
		}()
		started := time.Now()
		for i := 0; i < 40; i++ {
			run.log("burst line")
			time.Sleep(testutil.IntervalFast)
		}
		// 40 lines at one attempt per line would be 40 attempts. The paced
		// path attempts at most twice per interval (the immediate attempt
		// of a stale stamp, then the fallback round each interval arms),
		// so the bound scales with however long the burst actually took:
		// a slow runner stretches the burst over more intervals instead of
		// tripping a fixed bound.
		attempts := func() int {
			run.journalMu.Lock()
			defer run.journalMu.Unlock()
			return run.flushAttempts
		}()
		intervals := int(time.Since(started)/workflowLogFlushInterval) + 1
		assert.LessOrEqual(t, attempts-base, 2*intervals,
			"the write-through must pace to a bounded number of attempts per interval, not one per line")
		assert.Greater(t, attempts-base, 0, "the burst must have reached the flush path")
	})
}
