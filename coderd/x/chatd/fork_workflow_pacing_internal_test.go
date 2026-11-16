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

	// PacingPerInterval holds the token, waits for the fallback timer to
	// carry a burst (the timer path of the write-through), releases, and
	// then logs one more line. The new line must wait out the rest of the
	// interval instead of writing back to back with the timer write: at
	// most one write per interval, whichever path produced the last one.
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
		// Re-take the token, so the next paced attempt cannot write: the
		// cleanup keeps ownership and the test observes the pacing only.
		holdJournalWriteToken(t, run)

		// The timer write just stamped its attempt. A line appended now
		// sits well inside the same interval, so its immediate attempt
		// must not start a second write back to back: it must arm a
		// paced timer instead of flushing now.
		run.journalMu.Lock()
		stamp := run.lastLogFlush
		run.journalMu.Unlock()
		assert.False(t, stamp.Before(time.Now().Add(-workflowLogFlushInterval)),
			"the timer write must stamp the attempt, so the next line paces behind it")
		release()
		run.journalMu.Lock()
		after := run.flushAttempts
		run.journalMu.Unlock()
		run.log("paced line")
		// The immediate attempt of the paced line runs on its own
		// goroutine, so wait for it with a bounded loop, then give the
		// interval a moment: an unpaced path would flush the paced line
		// at once (a second write inside the same interval), a paced path
		// arms the timer and writes at the interval's end.
		deadline := time.Now().Add(10 * time.Second)
		for {
			run.journalMu.Lock()
			now := run.flushAttempts
			run.journalMu.Unlock()
			if now > after {
				break
			}
			if !time.Now().Before(deadline) {
				t.Fatalf("the paced line never reached the flush path")
			}
			time.Sleep(testutil.IntervalFast)
		}
		time.Sleep(300 * time.Millisecond)
		run.journalMu.Lock()
		attempts := run.flushAttempts - after
		armed := run.logFlushTimer != nil
		run.journalMu.Unlock()
		// Pacing: the immediate attempt plus at most the fallback round it
		// arms (300ms is far inside the interval, so a paced path cannot
		// have fired a second round). An unpaced path would have written
		// the line at once, which shows as an attempt whose journal write
		// lands inside the window.
		assert.LessOrEqual(t, attempts, 2,
			"the paced line must not write back to back with the timer write")
		// The decisive pin: the journal must still hold two lines during
		// the interval, because the paced line waits for the interval to
		// end before its write lands.
		assert.Equal(t, []string{"first line", "held line"}, journalLogs(),
			"the paced line must wait out the interval before writing")
		// The fallback round the paced line armed must still be pending
		// (the interval is not over), so the line is not stranded.
		if !armed {
			// The write may already have landed if the interval elapsed
			// between the sleep and this read; only the journal check
			// above is load-bearing, so a disarmed timer here means the
			// write landed, which the next check confirms.
			testutil.Eventually(ctx, t, func(context.Context) bool {
				return len(journalLogs()) == 3
			}, testutil.IntervalSlow)
		}
	})

	// SustainedLogging pins the one-write-per-interval bound end to end:
	// a script that logs one line per fast tick must produce journal
	// writes at most once per interval, counted in the journal row's
	// updated_at, not per line.
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

		// The token is held for the burst, so every attempt is a dropped
		// one and the journal cannot move: the test measures pacing, not
		// throughput. A stale stamp makes the burst's first attempt go
		// immediate, so the first attempt cannot hide behind the interval.
		base := func() int {
			run.journalMu.Lock()
			defer run.journalMu.Unlock()
			run.lastLogFlush = time.Now().Add(-workflowLogFlushInterval)
			return run.flushAttempts
		}()
		for i := 0; i < 40; i++ {
			run.log("burst line")
			time.Sleep(testutil.IntervalFast)
		}
		// 40 lines at one attempt per line would be 40 attempts in the 2s
		// window. The paced path attempts at most once per interval: the
		// burst window is one interval, so at most two attempts can land
		// (the immediate attempt, and the fallback round it arms).
		attempts := func() int {
			run.journalMu.Lock()
			defer run.journalMu.Unlock()
			return run.flushAttempts
		}()
		assert.LessOrEqual(t, attempts-base, 2,
			"the write-through must pace to at most one write per interval")
		assert.Greater(t, attempts-base, 0, "the burst must have reached the flush path")
	})
}
