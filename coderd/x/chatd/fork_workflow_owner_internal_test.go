package chatd

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3/sloggers/slogtest"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/quartz"
)

// stopWorkflowHeartbeat stops the heartbeat loop and the owner subscription
// of s, the way a killed process stops them. Close then releases nothing.
// The script goroutines keep running.
func stopWorkflowHeartbeat(s *Server) {
	o := &s.workflows.owner
	o.mu.Lock()
	cancel, waiter, unsubscribe := o.cancel, o.waiter, o.unsubscribe
	o.closed = true
	o.mu.Unlock()
	if cancel != nil {
		cancel()
		_ = waiter.Wait()
	}
	if unsubscribe != nil {
		unsubscribe()
	}
}

// ageWorkflowHeartbeats moves the heartbeat of the runs s owns past the
// stale window, as if s stopped renewing it long ago.
func ageWorkflowHeartbeats(t *testing.T, s *Server) {
	t.Helper()
	runs := s.workflows.liveRuns()
	if len(runs) == 0 {
		return
	}
	ids := make([]uuid.UUID, 0, len(runs))
	for _, run := range runs {
		ids = append(ids, run.id)
	}
	fdb, err := forkdb.From(s.db)
	require.NoError(t, err)
	_, err = fdb.HeartbeatWorkflowRuns(journalCtx(s.ctx), forkdb.HeartbeatWorkflowRunsParams{
		Now:     s.clock.Now().Add(-time.Hour),
		Ids:     ids,
		OwnerID: uuid.NullUUID{UUID: s.workflows.owner.id, Valid: true},
	})
	require.NoError(t, err)
}

// crashWorkflowServer leaves the state a killed chatd process leaves
// behind: the heartbeat loop stops, Close releases nothing, and the
// heartbeats of the runs s owns age past the stale window. The script
// goroutines keep running, so a test can show that the owner fence keeps
// their writes out of the journal.
func crashWorkflowServer(t *testing.T, s *Server) {
	t.Helper()
	stopWorkflowHeartbeat(s)
	ageWorkflowHeartbeats(t, s)
}

// peerServer starts another chatd process on the fixture's database and
// pubsub, on clock.
func (f *forkWorkflowFixture) peerServer(t *testing.T, clock quartz.Clock) *Server {
	t.Helper()
	return newInternalTestServer(t, f.db, f.ps, chatprovider.ProviderAPIKeys{},
		withInternalTestServerExperiments(codersdk.Experiments{codersdk.ExperimentForkWorkflows}),
		withInternalTestServerClock(clock))
}

// failingStatusStore fails the terminal status writes of runs while fail
// is set, and the heartbeats while failHeartbeat is set, the way a journal
// outage fails them.
type failingStatusStore struct {
	forkdb.Store
	fail          atomic.Bool
	failHeartbeat atomic.Bool
}

func (s *failingStatusStore) HeartbeatWorkflowRuns(
	ctx context.Context, arg forkdb.HeartbeatWorkflowRunsParams,
) ([]uuid.UUID, error) {
	if s.failHeartbeat.Load() {
		return nil, xerrors.New("journal outage")
	}
	return s.Store.HeartbeatWorkflowRuns(ctx, arg)
}

func (s *failingStatusStore) UpdateWorkflowRunStatus(
	ctx context.Context, arg forkdb.UpdateWorkflowRunStatusParams,
) (int64, error) {
	if arg.Status != workflowStatusRunning && s.fail.Load() {
		return 0, xerrors.New("journal outage")
	}
	return s.Store.UpdateWorkflowRunStatus(ctx, arg)
}

// failStatusWrites installs a failingStatusStore as the journal store of
// server's db. Tests install it before the run starts, while no goroutine
// reads server.db.
func failStatusWrites(t *testing.T, server *Server) *failingStatusStore {
	t.Helper()
	inner, err := forkdb.From(server.db)
	require.NoError(t, err)
	failing := &failingStatusStore{Store: inner}
	server.db = dbauthz.New(
		&forkDBSwap{Store: server.db, fdb: failing},
		rbac.NewStrictCachingAuthorizer(prometheus.NewRegistry()),
		slogtest.Make(t, &slogtest.Options{IgnoreErrors: true}),
		nil,
	)
	return failing
}

// blockingStepsStore holds the step reads until release is closed, the
// way database contention stalls them. entered receives once when a read
// is held. A store with armed set holds only the reads after it is set.
type blockingStepsStore struct {
	forkdb.Store
	entered chan struct{}
	release chan struct{}
	gated   bool
	armed   atomic.Bool
}

func (s *blockingStepsStore) GetWorkflowStepsByRun(ctx context.Context, runID uuid.UUID) ([]forkdb.WorkflowStep, error) {
	if s.gated && !s.armed.Load() {
		return s.Store.GetWorkflowStepsByRun(ctx, runID)
	}
	select {
	case s.entered <- struct{}{}:
	default:
	}
	<-s.release
	return s.Store.GetWorkflowStepsByRun(ctx, runID)
}

const ownerNeverReportsScript = `
def main(args):
    return agent("Never reports.")
`

// TestForkWorkflowOwnership covers the run ownership of several chatd
// processes on one database: a live owner keeps its run, a dead owner's run
// is swept and resumed once, two resumes race for one claim, a stop reaches
// the owner over pubsub, and a process that lost a run cannot write it.
func TestForkWorkflowOwnership(t *testing.T) {
	t.Parallel()

	// A process that starts while another one runs a workflow must not
	// interrupt that run: the startup sweep only takes stale runs.
	t.Run("NewProcessKeepsALiveRun", func(t *testing.T) {
		t.Parallel()
		f, mock := newForkWorkflowFixtureWithClock(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: ownerNeverReportsScript})
		f.steps(ctx, t, run, 1)
		fdb := forkWorkflowDB(t, f.db)

		_ = f.peerServer(t, mock)
		peer := f.peerServer(t, mock)
		// One heartbeat later every process has swept once more.
		advanceMockClockBy(ctx, t, mock, workflowHeartbeatInterval)

		row := journalRow(ctx, t, fdb, run.id)
		assert.Equal(t, workflowStatusRunning, row.Status)
		require.True(t, row.OwnerID.Valid)
		assert.Equal(t, f.server.workflows.owner.id, row.OwnerID.UUID, "the first process keeps the run")
		assert.Equal(t, workflowStatusRunning, run.snapshot().Status, "the script keeps running")

		// A resume on another process answers with the journal, which
		// says what the owner says: its first attempt reused no step.
		_, err := peer.startWorkflowRun(ctx, parent, f.model.ID,
			runWorkflowArgs{ResumeRunID: run.id.String()}, uuid.New())
		var elsewhere *workflowRunElsewhereError
		require.ErrorAs(t, err, &elsewhere)
		require.Len(t, elsewhere.result.Steps, 1)
		assert.False(t, elsewhere.result.Steps[0].Reused, "the snapshot of a live run marks no step reused")
	})

	// A run whose owner stopped its heartbeat goes interrupted once the
	// heartbeat is older than the stale window, and a resume on another
	// process executes it there. The old owner's late writes are fenced.
	t.Run("StaleRunResumesOnAnotherProcess", func(t *testing.T) {
		t.Parallel()
		f, mock := newForkWorkflowFixtureWithClock(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: ownerNeverReportsScript})
		step := f.steps(ctx, t, run, 1)[0]
		fdb := forkWorkflowDB(t, f.db)

		stopWorkflowHeartbeat(f.server)
		second := f.peerServer(t, mock)
		third := f.peerServer(t, mock)
		advanceMockClockBy(ctx, t, mock, workflowStaleAfter-workflowHeartbeatInterval)
		assert.Equal(t, workflowStatusRunning, journalRunStatus(ctx, t, fdb, run.id),
			"a heartbeat inside the stale window keeps the run")
		advanceMockClockBy(ctx, t, mock, 2*workflowHeartbeatInterval)
		row := journalRow(ctx, t, fdb, run.id)
		require.Equal(t, workflowStatusInterrupted, row.Status, "the sweep interrupts the stale run")
		assert.Equal(t, workflowInterruptedError, row.Error)

		resumed, err := second.startWorkflowRun(ctx, parent, f.model.ID,
			runWorkflowArgs{ResumeRunID: run.id.String()}, uuid.New())
		require.NoError(t, err)
		require.NotSame(t, run, resumed)
		row = journalRow(ctx, t, fdb, run.id)
		assert.Equal(t, workflowStatusRunning, row.Status)
		assert.Equal(t, second.workflows.owner.id, row.OwnerID.UUID, "the resume claims the run")

		// A second resume on the same process returns the run it executes,
		// and a resume on another live process answers with the journal.
		again, err := second.startWorkflowRun(ctx, parent, f.model.ID,
			runWorkflowArgs{ResumeRunID: run.id.String()}, uuid.New())
		require.NoError(t, err)
		assert.Same(t, resumed, again)
		_, err = third.startWorkflowRun(ctx, parent, f.model.ID,
			runWorkflowArgs{ResumeRunID: run.id.String()}, uuid.New())
		var elsewhere *workflowRunElsewhereError
		require.ErrorAs(t, err, &elsewhere)
		assert.Equal(t, workflowStatusRunning, elsewhere.result.Status)
		assert.Nil(t, third.workflows.get(run.id), "the third process executes nothing")

		// The first process lost the run: its journal writes reach no row.
		assert.True(t, f.server.workflowJournal().updateRunStatus(ctx, run.id, workflowStatusCompleted, "", nil, nil),
			"the old owner's terminal write is fenced")
		err = f.server.workflowJournal().insertStep(ctx, run.id, &workflowStep{
			id: uuid.New(), specKey: "late", status: stepStatusRunning,
		})
		require.ErrorIs(t, err, errWorkflowRunLost)
		row = journalRow(ctx, t, fdb, run.id)
		assert.Equal(t, workflowStatusRunning, row.Status, "the fenced write left the new owner's row")
		assert.Equal(t, second.workflows.owner.id, row.OwnerID.UUID)

		// The step reports once in its reused child. The new owner ends the
		// run; the old owner's script sees the report too, finds its write
		// fenced, and drops the run without touching the journal.
		f.report(ctx, t, step.ChatID, `{"text":"hi"}`)
		res := testutil.RequireReceive(ctx, t, awaitRegistered(ctx, t, resumed))
		require.Equal(t, workflowStatusCompleted, res.Status, res.Error)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return f.server.workflows.get(run.id) == nil
		}, testutil.IntervalFast)
		row = journalRow(ctx, t, fdb, run.id)
		assert.Equal(t, workflowStatusCompleted, row.Status)
		assert.Equal(t, second.workflows.owner.id, row.OwnerID.UUID)
	})

	// A resume that rehydrates for longer than the stale window keeps the
	// run it claimed: the heartbeat renews the claimed row before the run
	// is registered, so the sweep leaves it running.
	t.Run("HeartbeatRenewsAClaimWhileItRehydrates", func(t *testing.T) {
		t.Parallel()
		f, mock := newForkWorkflowFixtureWithClock(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: ownerNeverReportsScript})
		f.steps(ctx, t, run, 1)
		fdb := forkWorkflowDB(t, f.db)

		crashWorkflowServer(t, f.server)
		second := f.peerServer(t, mock)
		require.Equal(t, workflowStatusInterrupted, journalRunStatus(ctx, t, fdb, run.id))
		inner, err := forkdb.From(second.db)
		require.NoError(t, err)
		blocking := &blockingStepsStore{Store: inner, entered: make(chan struct{}, 1), release: make(chan struct{})}
		second.db = dbauthz.New(
			&forkDBSwap{Store: second.db, fdb: blocking},
			rbac.NewStrictCachingAuthorizer(prometheus.NewRegistry()),
			slogtest.Make(t, &slogtest.Options{IgnoreErrors: true}),
			nil,
		)

		type resumed struct {
			run *workflowRun
			err error
		}
		done := make(chan resumed, 1)
		go func() {
			r, err := second.startWorkflowRun(ctx, parent, f.model.ID,
				runWorkflowArgs{ResumeRunID: run.id.String()}, uuid.New())
			done <- resumed{run: r, err: err}
		}()
		testutil.RequireReceive(ctx, t, blocking.entered)
		advanceMockClockBy(ctx, t, mock, workflowStaleAfter+workflowHeartbeatInterval)
		row := journalRow(ctx, t, fdb, run.id)
		require.Equal(t, workflowStatusRunning, row.Status, "the sweep left the claimed run")
		assert.Equal(t, second.workflows.owner.id, row.OwnerID.UUID)

		close(blocking.release)
		res := testutil.RequireReceive(ctx, t, done)
		require.NoError(t, res.err)
		require.Same(t, res.run, second.workflows.get(run.id))
		assert.Equal(t, uuid.Nil, second.workflows.claimingRun(), "the registration ends the claim")
		assert.Equal(t, workflowStatusRunning, journalRunStatus(ctx, t, fdb, run.id))
	})

	// Two processes resume the same interrupted run at once. The claim lets
	// one execute it; the other answers with the run's journal state.
	t.Run("ConcurrentResumeExecutesOnce", func(t *testing.T) {
		t.Parallel()
		f, mock := newForkWorkflowFixtureWithClock(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: ownerNeverReportsScript})
		f.steps(ctx, t, run, 1)
		fdb := forkWorkflowDB(t, f.db)

		crashWorkflowServer(t, f.server)
		peers := []*Server{f.peerServer(t, mock), f.peerServer(t, mock)}
		require.Equal(t, workflowStatusInterrupted, journalRunStatus(ctx, t, fdb, run.id),
			"the startup sweep interrupts the crashed process's run")

		type outcome struct {
			run *workflowRun
			err error
		}
		outcomes := make([]outcome, len(peers))
		var start, done sync.WaitGroup
		start.Add(1)
		for i, peer := range peers {
			done.Add(1)
			go func() {
				defer done.Done()
				start.Wait()
				r, err := peer.startWorkflowRun(ctx, parent, f.model.ID,
					runWorkflowArgs{ResumeRunID: run.id.String()}, uuid.New())
				outcomes[i] = outcome{run: r, err: err}
			}()
		}
		start.Done()
		done.Wait()

		executed, answered := -1, -1
		for i, o := range outcomes {
			var elsewhere *workflowRunElsewhereError
			switch {
			case o.err == nil:
				require.NotNil(t, o.run)
				require.Equal(t, -1, executed, "only one resume may execute the run")
				executed = i
			case errors.As(o.err, &elsewhere):
				assert.Equal(t, run.id.String(), elsewhere.result.RunID)
				answered = i
			default:
				t.Fatalf("resume %d failed: %v", i, o.err)
			}
		}
		require.NotEqual(t, -1, executed, "one resume executes the run")
		require.NotEqual(t, -1, answered, "the other resume answers with the run state")
		assert.Nil(t, peers[answered].workflows.get(run.id))
		row := journalRow(ctx, t, fdb, run.id)
		assert.Equal(t, workflowStatusRunning, row.Status)
		assert.Equal(t, peers[executed].workflows.owner.id, row.OwnerID.UUID)
	})

	// A stop on a process that does not own the run reaches the owner over
	// its pubsub channel. The owner's script unwinds and journals the stop.
	t.Run("StopFromAnotherProcessReachesTheOwner", func(t *testing.T) {
		t.Parallel()
		f, mock := newForkWorkflowFixtureWithClock(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: ownerNeverReportsScript})
		f.steps(ctx, t, run, 1)
		fdb := forkWorkflowDB(t, f.db)
		other := f.peerServer(t, mock)

		poll := mock.Trap().NewTicker("chatd", "workflow_stop_poll")
		defer poll.Close()
		errc := make(chan error, 1)
		go func() { errc <- other.ForkStopWorkflowRun(ctx, parent.ID, run.id) }()
		poll.MustWait(ctx).MustRelease(ctx)

		// The owner received the event: its run ends as stopped.
		waitWorkflowDone(ctx, t, run)
		assert.Equal(t, workflowStatusStopped, run.snapshot().Status)
		row := journalRow(ctx, t, fdb, run.id)
		assert.Equal(t, workflowStatusStopped, row.Status)
		assert.Equal(t, f.server.workflows.owner.id, row.OwnerID.UUID, "the owner journaled the stop")

		// The stopping process sees the end on its next poll.
		mock.Advance(workflowStopPollInterval).MustWait(ctx)
		require.NoError(t, testutil.RequireReceive(ctx, t, errc))
	})

	// A live process whose run another process claimed learns it on its
	// next heartbeat: it cancels the script and drops the run without
	// writing the journal.
	t.Run("HeartbeatNoticesALostRun", func(t *testing.T) {
		t.Parallel()
		f, mock := newForkWorkflowFixtureWithClock(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: ownerNeverReportsScript})
		f.steps(ctx, t, run, 1)
		fdb := forkWorkflowDB(t, f.db)

		// The owner's heartbeat writes stalled long enough for another
		// process to claim the run.
		ageWorkflowHeartbeats(t, f.server)
		other := f.peerServer(t, mock)
		require.Equal(t, workflowStatusInterrupted, journalRunStatus(ctx, t, fdb, run.id))
		_, ok, err := other.workflowJournal().claimRun(ctx, run.id)
		require.NoError(t, err)
		require.True(t, ok)

		advanceMockClockBy(ctx, t, mock, workflowHeartbeatInterval)
		waitWorkflowDone(ctx, t, run)
		assert.Nil(t, f.server.workflows.get(run.id), "the lost run leaves the registry")
		row := journalRow(ctx, t, fdb, run.id)
		assert.Equal(t, workflowStatusRunning, row.Status, "the lost owner wrote nothing")
		assert.Equal(t, other.workflows.owner.id, row.OwnerID.UUID)
		assert.Empty(t, row.Error)
	})

	// A run whose terminal write fails keeps its row alive: the heartbeat
	// renews it and retries the write, so no sweep reopens a run that
	// already ended.
	t.Run("TerminalWriteRetries", func(t *testing.T) {
		t.Parallel()
		f, mock := newForkWorkflowFixtureWithClock(t)
		ctx := chatdTestContext(t)
		failing := failStatusWrites(t, f.server)
		failing.fail.Store(true)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: `
def main(args):
    return "done"
`})
		waitWorkflowDone(ctx, t, run)
		fdb := forkWorkflowDB(t, f.db)
		require.Equal(t, workflowStatusRunning, journalRunStatus(ctx, t, fdb, run.id),
			"the failed write left the row running")

		// The heartbeat keeps the row fresh while the journal is down, so
		// another process's sweep leaves it alone.
		advanceMockClockBy(ctx, t, mock, workflowStaleAfter+workflowHeartbeatInterval)
		_ = f.peerServer(t, mock)
		require.Equal(t, workflowStatusRunning, journalRunStatus(ctx, t, fdb, run.id),
			"the heartbeat keeps the ended run from the sweep")

		failing.fail.Store(false)
		advanceMockClockBy(ctx, t, mock, workflowHeartbeatInterval)
		row := journalRow(ctx, t, fdb, run.id)
		assert.Equal(t, workflowStatusCompleted, row.Status, "the heartbeat retried the terminal write")
		assert.Equal(t, f.server.workflows.owner.id, row.OwnerID.UUID)
		assert.Empty(t, f.server.workflows.liveRuns(), "the run stops heartbeating once its end landed")
	})

	// A run whose cleanup cannot confirm its ownership keeps its open steps
	// and does not end: it retries the check, then fails the steps and
	// ends once the journal answers.
	t.Run("CleanupRetriesAFailedOwnershipCheck", func(t *testing.T) {
		t.Parallel()
		f, mock := newForkWorkflowFixtureWithClock(t)
		ctx := chatdTestContext(t)
		failing := failStatusWrites(t, f.server)
		failing.failHeartbeat.Store(true)
		retry := mock.Trap().NewTimer("chatd", "workflow_confirm_owner")
		defer retry.Close()
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: `
def main(args):
    spawn("Unwaited.")
    return {}
`})
		retry.MustWait(ctx).MustRelease(ctx)
		select {
		case <-run.done:
			t.Fatal("the run ended while its ownership was unconfirmed")
		default:
		}
		snap := run.snapshot()
		require.Len(t, snap.Steps, 1)
		assert.Equal(t, stepStatusRunning, snap.Steps[0].Status, "the step stays open")

		failing.failHeartbeat.Store(false)
		advanceMockClockBy(ctx, t, mock, workflowHeartbeatInterval)
		waitWorkflowDone(ctx, t, run)
		res := run.snapshot()
		require.Equal(t, workflowStatusCompleted, res.Status, res.Error)
		require.Len(t, res.Steps, 1)
		assert.Equal(t, stepStatusFailed, res.Steps[0].Status)
		assert.Contains(t, res.Steps[0].Error, "ended before the step reported")
	})

	// An ended run whose terminal write never landed and whose row another
	// process claimed leaves the registry: the new owner may run the
	// script again, so await_workflow must read the journal.
	t.Run("AnEndedRunThatLostItsRowLeavesTheRegistry", func(t *testing.T) {
		t.Parallel()
		f, mock := newForkWorkflowFixtureWithClock(t)
		ctx := chatdTestContext(t)
		failing := failStatusWrites(t, f.server)
		failing.fail.Store(true)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: `
def main(args):
    return "done"
`})
		waitWorkflowDone(ctx, t, run)
		fdb := forkWorkflowDB(t, f.db)

		ageWorkflowHeartbeats(t, f.server)
		other := f.peerServer(t, mock)
		require.Equal(t, workflowStatusInterrupted, journalRunStatus(ctx, t, fdb, run.id))
		_, ok, err := other.workflowJournal().claimRun(ctx, run.id)
		require.NoError(t, err)
		require.True(t, ok)

		advanceMockClockBy(ctx, t, mock, workflowHeartbeatInterval)
		assert.Nil(t, f.server.workflows.get(run.id), "the ended copy left the registry")
		row := journalRow(ctx, t, fdb, run.id)
		assert.Equal(t, workflowStatusRunning, row.Status, "the old copy wrote nothing")
		assert.Equal(t, other.workflows.owner.id, row.OwnerID.UUID)
	})

	// A stop that finds the row running and owned by this process while
	// the registry is empty waits for the resume of this process that
	// claimed it, then stops the run that resume registered.
	t.Run("StopWaitsForAResumeOfThisProcess", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: ownerNeverReportsScript})
		f.steps(ctx, t, run, 1)
		fdb := forkWorkflowDB(t, f.db)

		// A resume between its claim and its registration: it holds the
		// resume lock, and the registry does not hold the run yet.
		f.server.workflows.resumeMu.Lock()
		f.server.workflows.remove(run.id)
		errc := make(chan error, 1)
		go func() { errc <- f.server.ForkStopWorkflowRun(ctx, parent.ID, run.id) }()
		testutil.Eventually(ctx, t, func(context.Context) bool {
			select {
			case err := <-errc:
				t.Fatalf("the stop went past the resume: %v", err)
			default:
			}
			return goroutineBlockedOnLockIn("(*Server).stopOwnerlessRun")
		}, testutil.IntervalFast)
		_, added := f.server.workflows.add(run)
		require.True(t, added)
		f.server.workflows.resumeMu.Unlock()

		require.NoError(t, testutil.RequireReceive(ctx, t, errc))
		waitWorkflowDone(ctx, t, run)
		assert.Equal(t, workflowStatusStopped, run.snapshot().Status, "the stop reached the registered run")
		assert.Equal(t, workflowStatusStopped, journalRunStatus(ctx, t, fdb, run.id))
	})

	// A stop through the journal whose terminal write fails says so: the
	// claimed run is in no registry, so nothing would retry the write.
	t.Run("JournaledStopReturnsAFailedWrite", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		failing := failStatusWrites(t, f.server)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: ownerNeverReportsScript})
		f.steps(ctx, t, run, 1)
		fdb := forkWorkflowDB(t, f.db)

		// The registry drop sends the stop through the journal.
		f.server.workflows.remove(run.id)
		failing.fail.Store(true)
		require.Error(t, f.server.ForkStopWorkflowRun(ctx, parent.ID, run.id))
		require.Equal(t, workflowStatusRunning, journalRunStatus(ctx, t, fdb, run.id))

		failing.fail.Store(false)
		require.NoError(t, f.server.ForkStopWorkflowRun(ctx, parent.ID, run.id))
		assert.Equal(t, workflowStatusStopped, journalRunStatus(ctx, t, fdb, run.id))
	})

	// A journal-only stop that takes longer than the stale window keeps the
	// run it claimed: the heartbeat renews the row until the stop lands.
	t.Run("HeartbeatRenewsAClaimWhileAJournaledStopRuns", func(t *testing.T) {
		t.Parallel()
		f, mock := newForkWorkflowFixtureWithClock(t)
		inner, err := forkdb.From(f.server.db)
		require.NoError(t, err)
		blocking := &blockingStepsStore{Store: inner, entered: make(chan struct{}, 1), release: make(chan struct{}), gated: true}
		f.server.db = dbauthz.New(
			&forkDBSwap{Store: f.server.db, fdb: blocking},
			rbac.NewStrictCachingAuthorizer(prometheus.NewRegistry()),
			slogtest.Make(t, &slogtest.Options{IgnoreErrors: true}),
			nil,
		)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: ownerNeverReportsScript})
		f.steps(ctx, t, run, 1)
		fdb := forkWorkflowDB(t, f.db)

		// The registry drop sends the stop through the journal.
		f.server.workflows.remove(run.id)
		blocking.armed.Store(true)
		errc := make(chan error, 1)
		go func() { errc <- f.server.ForkStopWorkflowRun(ctx, parent.ID, run.id) }()
		testutil.RequireReceive(ctx, t, blocking.entered)
		advanceMockClockBy(ctx, t, mock, workflowStaleAfter+workflowHeartbeatInterval)
		row := journalRow(ctx, t, fdb, run.id)
		require.Equal(t, workflowStatusRunning, row.Status, "the sweep left the claimed run")
		assert.Equal(t, f.server.workflows.owner.id, row.OwnerID.UUID)

		close(blocking.release)
		require.NoError(t, testutil.RequireReceive(ctx, t, errc))
		assert.Equal(t, uuid.Nil, f.server.workflows.claimingRun(), "the stop ends the claim")
		assert.Equal(t, workflowStatusStopped, journalRunStatus(ctx, t, fdb, run.id))
	})

	// A stop on the process that held a run another process claimed since
	// the last heartbeat must not stop the local copy: it confirms the
	// ownership first and reaches the new owner over pubsub.
	t.Run("StopOnALostRunReachesTheNewOwner", func(t *testing.T) {
		t.Parallel()
		f, mock := newForkWorkflowFixtureWithClock(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: ownerNeverReportsScript})
		f.steps(ctx, t, run, 1)
		fdb := forkWorkflowDB(t, f.db)

		ageWorkflowHeartbeats(t, f.server)
		other := f.peerServer(t, mock)
		require.Equal(t, workflowStatusInterrupted, journalRunStatus(ctx, t, fdb, run.id))
		resumed, err := other.startWorkflowRun(ctx, parent, f.model.ID,
			runWorkflowArgs{ResumeRunID: run.id.String()}, uuid.New())
		require.NoError(t, err)
		f.steps(ctx, t, resumed, 1)

		poll := mock.Trap().NewTicker("chatd", "workflow_stop_poll")
		defer poll.Close()
		errc := make(chan error, 1)
		go func() { errc <- f.server.ForkStopWorkflowRun(ctx, parent.ID, run.id) }()
		poll.MustWait(ctx).MustRelease(ctx)

		waitWorkflowDone(ctx, t, resumed)
		assert.Equal(t, workflowStatusStopped, resumed.snapshot().Status)
		row := journalRow(ctx, t, fdb, run.id)
		assert.Equal(t, workflowStatusStopped, row.Status)
		assert.Equal(t, other.workflows.owner.id, row.OwnerID.UUID, "the new owner journaled the stop")

		mock.Advance(workflowStopPollInterval).MustWait(ctx)
		require.NoError(t, testutil.RequireReceive(ctx, t, errc))
		waitWorkflowDone(ctx, t, run)
		assert.Nil(t, f.server.workflows.get(run.id), "the old copy left the registry as lost")
	})

	// Close releases the runs the process owns: they read interrupted at
	// once, so the next process resumes them without the stale window.
	t.Run("CloseReleasesTheRuns", func(t *testing.T) {
		t.Parallel()
		f, mock := newForkWorkflowFixtureWithClock(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: ownerNeverReportsScript})
		f.steps(ctx, t, run, 1)
		fdb := forkWorkflowDB(t, f.db)

		require.NoError(t, f.server.Close())
		row := journalRow(ctx, t, fdb, run.id)
		assert.Equal(t, workflowStatusInterrupted, row.Status)
		assert.Equal(t, workflowInterruptedError, row.Error)

		next := f.peerServer(t, mock)
		resumed, err := next.startWorkflowRun(ctx, parent, f.model.ID,
			runWorkflowArgs{ResumeRunID: run.id.String()}, uuid.New())
		require.NoError(t, err)
		assert.Equal(t, workflowStatusRunning, resumed.snapshot().Status)
		assert.Equal(t, next.workflows.owner.id, journalRow(ctx, t, fdb, run.id).OwnerID.UUID)
	})
}

// goroutineBlockedOnLockIn reports whether a goroutine waits for a mutex
// inside fn.
func goroutineBlockedOnLockIn(fn string) bool {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, "sync.(*Mutex).Lock") && strings.Contains(g, fn) {
			return true
		}
	}
	return false
}

// waitWorkflowDone waits until run closed its done channel.
func waitWorkflowDone(ctx context.Context, t *testing.T, run *workflowRun) {
	t.Helper()
	select {
	case <-run.done:
	case <-ctx.Done():
		t.Fatalf("the run did not end: %v", ctx.Err())
	}
}
