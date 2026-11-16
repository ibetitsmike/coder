package chatd

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/quartz"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/testutil"
)

// advanceMockClockBy advances the mock clock by d, waiting for every timer
// that fires on the way. Same as the helper in chatd_retry_test.go, but for
// the internal package (that one lives in chatd_test).
func advanceMockClockBy(ctx context.Context, t *testing.T, clock *quartz.Mock, d time.Duration) {
	t.Helper()
	for remaining := d; remaining > 0; {
		next, ok := clock.Peek()
		require.True(t, ok, "no pending clock event while advancing %s", remaining)
		if next > remaining {
			clock.Advance(remaining).MustWait(ctx)
			return
		}
		_, waiter := clock.AdvanceNext()
		waiter.MustWait(ctx)
		remaining -= next
	}
}

// TestForkWorkflowLogPacing pins the write pacing of the live-log
// write-through: a script that logs continuously must produce at most one
// journal write per workflowLogFlushInterval, whether the write comes from
// the immediate attempt of a line or from the paced fallback timer. The
// sub-tests run on a mock clock, so no sub-test sleeps on wall time: the
// flush schedule advances only when the test advances the clock.
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
		f, mock := newForkWorkflowFixtureWithClock(t)
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
		run.lastLogFlush = mock.Now().Add(-workflowLogFlushInterval)
		run.journalMu.Unlock()
		run.log("held line")
		testutil.Eventually(ctx, t, func(context.Context) bool {
			run.journalMu.Lock()
			armed := run.logFlushTimer != nil
			run.journalMu.Unlock()
			return armed
		}, testutil.IntervalFast)
		run.journalMu.Lock()
		stampBefore := run.lastLogFlush
		run.journalMu.Unlock()

		// Release, then advance one interval: the fallback timer fires
		// into the free token and writes, which stamps its firing. The
		// journal catches up without another log() call, so the write
		// came from the timer path.
		release()
		advanceMockClockBy(ctx, t, mock, workflowLogFlushInterval)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return len(journalLogs()) == 2
		}, testutil.IntervalFast)
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
		assert.True(t, stamp.After(stampBefore),
			"the timer write must stamp the attempt, so the next line paces behind it")

		run.log("paced line")
		// The paced line must wait out the rest of the interval. Wait for
		// the attempt to settle (the paced path arms a timer of its own),
		// then pin both halves of the contract: the journal still holds
		// two lines, and a timer carries the third. An unpaced path
		// writes the line at once, which shows up as a third journal line
		// and no timer.
		testutil.Eventually(ctx, t, func(context.Context) bool {
			run.journalMu.Lock()
			armed := run.logFlushTimer != nil
			run.journalMu.Unlock()
			return armed || len(journalLogs()) == 3
		}, testutil.IntervalFast)
		assert.Len(t, journalLogs(), 2,
			"the paced line must wait out the interval instead of writing back to back with the timer write")
		run.journalMu.Lock()
		armed := run.logFlushTimer != nil
		run.journalMu.Unlock()
		assert.True(t, armed, "the paced line must leave a timer that carries it")

		// The timer the line armed carries it at the interval's end: the
		// line is not stranded, the journal catches up on its own.
		advanceMockClockBy(ctx, t, mock, workflowLogFlushInterval)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return len(journalLogs()) == 3
		}, testutil.IntervalFast)
		assert.Equal(t, []string{"first line", "held line", "paced line"}, journalLogs())
	})

	// SustainedLogging pins the one-write-per-interval bound end to end:
	// a burst of lines must reach the flush path a bounded number of
	// times, counted in the run's flush attempts, not once per line.
	t.Run("SustainedLoggingWritesOncePerInterval", func(t *testing.T) {
		t.Parallel()
		f, mock := newForkWorkflowFixtureWithClock(t)
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
		// The replay echo is spent on the first line already: drop any
		// remaining credit, or the burst lines would vanish into it.
		run.mu.Lock()
		run.logEchoLeft = 0
		run.mu.Unlock()

		// No token is held: the burst writes through for real, and the
		// test measures how often the write-through attempts the journal,
		// not whether it can write. A stale stamp makes the burst's first
		// attempt go immediate, so the first line cannot hide behind the
		// interval.
		burstStart := mock.Now()
		var staleStamp time.Time
		base := func() int {
			run.journalMu.Lock()
			defer run.journalMu.Unlock()
			staleStamp = mock.Now().Add(-workflowLogFlushInterval)
			run.lastLogFlush = staleStamp
			return run.flushAttempts
		}()
		// Twelve lines, a quarter interval apart on the mock clock: three
		// intervals of sustained logging, with no wall time between the
		// lines. The paced path attempts at most twice per interval (the
		// immediate attempt of a stale stamp, then the fallback round the
		// interval arms), so the bound scales with the mock time the
		// burst spans instead of a fixed count.
		// Other components own tickers and timers on the same mock clock,
		// but their periods (10s, 15s, and the step child's 5m await) are
		// longer than the burst, so none of them reaches its first tick
		// inside the three mock seconds the burst spans: only the paced
		// flush timers of this run fire.
		const steps = 12
		for i := 0; i < steps; i++ {
			run.log(fmt.Sprintf("burst line %d", i))
			advanceMockClockBy(ctx, t, mock, workflowLogFlushInterval/4)
		}
		// Five lines back to back inside one interval: the paced path
		// must fold them into one timer, so the bound below (two attempts
		// per interval) must still hold for the burst as a whole. A path
		// that flushed per line would trip the bound, and the tail lines
		// would also land unjournaled until the next interval instead of
		// arriving with the burst's last write (pinned by the stamp check
		// below).
		for i := 0; i < 5; i++ {
			run.log(fmt.Sprintf("same-interval line %d", i))
		}
		// The stamp of the burst's last window must have moved into the
		// burst window: with it stale, the next burst goes immediate per
		// line and the pacing bound below would not hold for it. Wait out
		// any attempt that could still move the stamp, journal progress
		// included: a flush that writes re-arms or stamps through the
		// same paths this test pins. journalLen feeds the tail asserts.
		var stamp time.Time
		journalLen := 0
		testutil.Eventually(ctx, t, func(context.Context) bool {
			run.journalMu.Lock()
			inFlight := run.logFlushInFlight
			run.journalMu.Unlock()
			if inFlight {
				return false
			}
			journalLen = len(journalRunLogs(ctx, t, fdb, run.id))
			run.journalMu.Lock()
			defer run.journalMu.Unlock()
			stamp = run.lastLogFlush
			return stamp.After(burstStart) || journalLen == 1+steps+5
		}, testutil.IntervalSlow)
		attemptsAfter := func() int {
			run.journalMu.Lock()
			defer run.journalMu.Unlock()
			return run.flushAttempts
		}
		// The goroutine of an immediate attempt counts its own flush when
		// it reaches flushLogs, which can trail the last Advance by a
		// scheduler tick. Wait for the counter to move before reading it.
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return attemptsAfter()-base > 0
		}, testutil.IntervalFast)
		intervals := int((steps+5)*(workflowLogFlushInterval/4)/workflowLogFlushInterval) + 2
		assert.LessOrEqual(t, attemptsAfter()-base, 2*intervals,
			"the write-through must pace to a bounded number of attempts per interval, not one per line")
		// Advance one more interval so the timer the tail armed fires and
		// the tail lines reach the journal without wall-clock waiting.
		advanceMockClockBy(ctx, t, mock, workflowLogFlushInterval)
		// Every line must have reached the journal by the end: pacing
		// bounds the write rate, it must not strand lines.
		var tailLogs []string
		testutil.Eventually(ctx, t, func(context.Context) bool {
			tailLogs = journalRunLogs(ctx, t, fdb, run.id)
			return len(tailLogs) == 1+steps+5
		}, testutil.IntervalSlow)
		assert.Equal(t, fmt.Sprintf("same-interval line %d", 4), tailLogs[len(tailLogs)-1],
			"the tail lines must land in order after the burst")
		// After the journal has caught up, no attempt may be in flight and
		// the stamp must have moved past the stale stamp the burst started
		// from: the write that landed the tail stamped its attempt, and
		// the timer that carried it stamped its firing before that.
		testutil.Eventually(ctx, t, func(context.Context) bool {
			run.journalMu.Lock()
			defer run.journalMu.Unlock()
			if run.logFlushInFlight {
				return false
			}
			stamp = run.lastLogFlush
			return stamp.After(staleStamp)
		}, testutil.IntervalSlow)
	})
}
