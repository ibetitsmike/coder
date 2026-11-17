package chatd

// This file is fork code: the run ownership of the fork-workflows
// experiment. Several chatd processes can share one database: replicas, and
// the old and new process of a rolling restart. Each journaled run names
// the process that executes its script (owner_id, the chatd worker ID) and
// carries the owner's last heartbeat (heartbeat_at). The owner renews the
// heartbeat of its live runs every workflowHeartbeatInterval. A running run
// whose heartbeat is older than workflowStaleAfter belongs to a dead
// process, so any process may mark it interrupted, and a resume may claim
// it. The engine's journal writes carry the owner, so a process that lost a
// run cannot overwrite the state of the new owner.

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"

	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/quartz"
)

const (
	// workflowHeartbeatInterval is how often the owner renews the
	// heartbeat of its live runs and sweeps the runs of dead owners.
	workflowHeartbeatInterval = 10 * time.Second
	// workflowStaleAfter is the age of a heartbeat after which the run's
	// owner counts as dead. It spans three heartbeats, so one slow write
	// does not hand a live run to another process.
	workflowStaleAfter = 30 * time.Second
	// workflowReleaseTimeout bounds the release write of Close.
	workflowReleaseTimeout = 5 * time.Second
	// workflowStopPollInterval is how often a stop that another process
	// carries out reads the run row for its end.
	workflowStopPollInterval = 250 * time.Millisecond
	// workflowLostError is the error of a run that this process lost to
	// another process.
	workflowLostError = "another chatd process took over the run"
	// workflowOwnerEventStop asks the owner of a run to stop it.
	workflowOwnerEventStop = "stop"
)

// errWorkflowRunLost fails a journal write that the owner fence rejected.
var errWorkflowRunLost = xerrors.New(workflowLostError)

// workflowOwnerChannel is the pubsub channel of one chatd process. Other
// processes send it the events of the runs it owns.
func workflowOwnerChannel(owner uuid.UUID) string {
	return "fork_workflow_owner:" + owner.String()
}

// workflowOwnerEvent is one message on an owner channel.
type workflowOwnerEvent struct {
	Kind  string    `json:"kind"`
	RunID uuid.UUID `json:"run_id"`
}

// workflowOwnership is the owner identity of this process and its
// heartbeat loop. id is set once, before any run starts, and never changes.
type workflowOwnership struct {
	id uuid.UUID

	mu          sync.Mutex
	cancel      context.CancelFunc
	waiter      quartz.Waiter
	unsubscribe func()
	closed      bool
}

// workflowRunElsewhereError is the answer of a resume that found the run
// live in another process. run_workflow returns the snapshot as its result
// instead of an error.
type workflowRunElsewhereError struct {
	result runWorkflowResult
}

func (e *workflowRunElsewhereError) Error() string {
	return "another chatd process runs the workflow run " + e.result.RunID
}

// liveRuns returns the runs whose script this process still executes, and
// the ended runs whose terminal journal write is still pending: their rows
// stay running until the write lands.
func (g *workflowRegistry) liveRuns() []*workflowRun {
	g.mu.Lock()
	runs := make([]*workflowRun, 0, len(g.runs))
	for _, run := range g.runs {
		runs = append(runs, run)
	}
	g.mu.Unlock()
	live := runs[:0]
	for _, run := range runs {
		run.mu.Lock()
		if (run.result == nil && !run.lost) || run.terminalPending {
			live = append(live, run)
		}
		run.mu.Unlock()
	}
	return live
}

// retryTerminalWrite journals the end of a run whose terminal write
// failed. A fenced write clears the flag too: the row belongs to another
// process now.
func (r *workflowRun) retryTerminalWrite(ctx context.Context) {
	r.mu.Lock()
	if !r.terminalPending {
		r.mu.Unlock()
		return
	}
	res := *r.result
	r.mu.Unlock()
	_, err := r.p.workflowJournal().writeRunStatus(ctx, r.id, res.Status, res.Error, &res, r.phasesSnapshot())
	if err != nil {
		r.p.logger.Warn(ctx, "journal the end of a workflow run", slog.F("workflow_id", r.id), slog.Error(err))
		return
	}
	r.mu.Lock()
	r.terminalPending = false
	r.mu.Unlock()
}

// removeRun drops run from the registry unless another run took its place.
func (g *workflowRegistry) removeRun(run *workflowRun) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.runs[run.id] == run {
		delete(g.runs, run.id)
	}
}

// forkInitWorkflowJournal records the owner identity of this process,
// marks the stale runs of dead processes interrupted, and starts the
// heartbeat loop. chatd runs it once at startup, before any turn can resume
// a run. owner is the chatd worker ID, which is new for every process.
func (p *Server) forkInitWorkflowJournal(owner uuid.UUID) {
	p.workflows.owner.id = owner
	n, err := p.workflowJournal().markStale(journalCtx(p.ctx))
	if err != nil {
		p.logger.Warn(p.ctx, "mark workflow runs interrupted", slog.Error(err))
	} else if n > 0 {
		p.logger.Info(p.ctx, "marked workflow runs interrupted", slog.F("count", n))
	}
	if !p.experiments.Enabled(codersdk.ExperimentForkWorkflows) {
		return
	}
	o := &p.workflows.owner
	ctx, cancel := context.WithCancel(p.ctx)
	unsubscribe, err := p.pubsub.SubscribeWithErr(workflowOwnerChannel(owner),
		func(_ context.Context, msg []byte, err error) {
			if err != nil {
				return
			}
			var event workflowOwnerEvent
			if json.Unmarshal(msg, &event) != nil || event.Kind != workflowOwnerEventStop {
				return
			}
			if run := p.workflows.get(event.RunID); run != nil {
				run.requestStop()
			}
		})
	if err != nil {
		// The runs still execute; a stop from another process then waits
		// out its timeout without reaching them.
		p.logger.Warn(p.ctx, "subscribe to the workflow owner channel", slog.Error(err))
		unsubscribe = nil
	}
	waiter := p.clock.TickerFunc(ctx, workflowHeartbeatInterval, func() error {
		p.workflowHeartbeatTick(ctx)
		return nil
	}, "chatd", "workflow_heartbeat")
	o.mu.Lock()
	o.cancel, o.waiter, o.unsubscribe = cancel, waiter, unsubscribe
	o.mu.Unlock()
}

// forkCloseWorkflowJournal stops the heartbeat loop and marks the runs
// this process still owns interrupted, so the next process resumes them
// without waiting out workflowStaleAfter. Close runs it after the script
// goroutines have drained.
func (p *Server) forkCloseWorkflowJournal() {
	o := &p.workflows.owner
	o.mu.Lock()
	if o.closed || o.cancel == nil {
		o.closed = true
		o.mu.Unlock()
		return
	}
	o.closed = true
	cancel, waiter, unsubscribe := o.cancel, o.waiter, o.unsubscribe
	o.mu.Unlock()
	cancel()
	if err := waiter.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		p.logger.Warn(p.ctx, "workflow heartbeat loop ended", slog.Error(err))
	}
	if unsubscribe != nil {
		unsubscribe()
	}
	//nolint:gocritic // The release runs after the server context ended.
	ctx, stop := context.WithTimeout(dbauthz.AsSystemRestricted(context.Background()), workflowReleaseTimeout)
	defer stop()
	// A run that ended but could not journal it gets one more try: the
	// release would leave it open for a resume to execute again.
	for _, run := range p.workflows.liveRuns() {
		run.retryTerminalWrite(ctx)
	}
	n, err := p.workflowJournal().releaseRuns(ctx)
	if err != nil {
		p.logger.Warn(ctx, "release workflow runs", slog.Error(err))
		return
	}
	if n > 0 {
		p.logger.Info(ctx, "released workflow runs", slog.F("count", n))
	}
}

// workflowHeartbeatTick renews the heartbeat of the live runs, cancels the
// runs that another process took, and sweeps the runs of dead owners.
func (p *Server) workflowHeartbeatTick(ctx context.Context) {
	j := p.workflowJournal()
	jctx := journalCtx(ctx)
	if runs := p.workflows.liveRuns(); len(runs) > 0 {
		ids := make([]uuid.UUID, 0, len(runs))
		for _, run := range runs {
			ids = append(ids, run.id)
		}
		kept, err := j.heartbeat(jctx, ids)
		if err != nil {
			p.logger.Warn(ctx, "renew workflow run heartbeats", slog.Error(err))
		} else {
			owned := make(map[uuid.UUID]struct{}, len(kept))
			for _, id := range kept {
				owned[id] = struct{}{}
			}
			for _, run := range runs {
				if _, ok := owned[run.id]; !ok {
					_, _ = run.recheckOwnership(jctx)
					continue
				}
				run.retryTerminalWrite(jctx)
			}
		}
	}
	n, err := j.markStale(jctx)
	if err != nil {
		p.logger.Warn(ctx, "mark stale workflow runs interrupted", slog.Error(err))
		return
	}
	if n > 0 {
		p.logger.Info(ctx, "marked stale workflow runs interrupted", slog.F("count", n))
	}
}

// confirmOwnership renews the heartbeat of one run and reports whether this
// process still owns it. A failed read answers false without marking the
// run lost: the caller skips work that would touch child chats another
// owner may hold by now.
func (r *workflowRun) confirmOwnership(ctx context.Context) bool {
	kept, err := r.p.workflowJournal().heartbeat(journalCtx(ctx), []uuid.UUID{r.id})
	if err != nil {
		return false
	}
	if len(kept) == 1 {
		return true
	}
	owned, err := r.recheckOwnership(journalCtx(ctx))
	return err == nil && owned
}

// recheckOwnership reads the row of a run that the heartbeat missed. The
// run is lost when another process owns it or a sweep interrupted it. A
// terminal row of this owner is the run's own end, which landed before the
// result did.
func (r *workflowRun) recheckOwnership(ctx context.Context) (bool, error) {
	row, found, err := r.p.workflowJournal().getRun(ctx, r.id)
	if err != nil {
		return false, err
	}
	if !found {
		return true, nil
	}
	if row.OwnerID.Valid && row.OwnerID.UUID == r.p.workflows.owner.id &&
		row.Status != workflowStatusInterrupted {
		if row.Status != workflowStatusRunning {
			// The terminal write landed after all.
			r.mu.Lock()
			r.terminalPending = false
			r.mu.Unlock()
		}
		return true, nil
	}
	r.markLost()
	r.mu.Lock()
	r.terminalPending = false
	ended := r.result != nil && !r.lost
	r.mu.Unlock()
	if ended {
		// The copy ended before its end reached the row, and the new owner
		// may run the script again: await_workflow reads the journal now.
		r.p.workflows.removeRun(r)
	}
	return false, nil
}

// markLost cancels the script of a run that another process took over or
// that a sweep interrupted. finish then ends the run without writing.
func (r *workflowRun) markLost() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.result != nil || r.lost {
		return
	}
	r.lost = true
	if r.cancel != nil {
		r.cancel()
	}
	r.p.logger.Info(context.Background(), "workflow run lost to another chatd process",
		slog.F("workflow_id", r.id))
}

func (r *workflowRun) isLost() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lost
}

// finishLost ends a run that this process lost. It writes nothing, sends
// no end message and drops the run from the registry, so await_workflow
// and the run API read the journal, which the new owner writes. A waiting
// await_workflow gets the journal state.
func (r *workflowRun) finishLost() {
	//nolint:gocritic // The lost run reads the journal after its turn context ended.
	ctx, stop := r.p.inflightContext(dbauthz.AsSystemRestricted(context.Background()))
	defer stop()
	res, found, err := r.p.workflowResultFromJournal(ctx, r.parent.ID, r.id)
	if err != nil || !found {
		r.mu.Lock()
		res = r.snapshotLocked()
		r.mu.Unlock()
		res.Status = workflowStatusInterrupted
		res.Error = workflowLostError
	}
	r.mu.Lock()
	r.lost = true
	r.result = &res
	r.notified = true
	r.mu.Unlock()
	r.cancelLogFlush()
	close(r.done)
	r.p.workflows.removeRun(r)
}

// workflowOwnerLive reports whether another process owns the running row
// and renewed its heartbeat inside the stale window.
func (p *Server) workflowOwnerLive(ownerID uuid.NullUUID, heartbeatAt time.Time, heartbeatValid bool) bool {
	return ownerID.Valid && ownerID.UUID != p.workflows.owner.id && heartbeatValid &&
		!heartbeatAt.Before(p.clock.Now().Add(-workflowStaleAfter))
}

// stopRemoteRun asks the live owner of a run to stop it, then waits
// briefly for the owner to journal the end, the way a stop of a held run
// waits for its script to unwind.
func (p *Server) stopRemoteRun(ctx context.Context, runID, owner uuid.UUID) error {
	msg, err := json.Marshal(workflowOwnerEvent{Kind: workflowOwnerEventStop, RunID: runID})
	if err != nil {
		return xerrors.Errorf("marshal the stop event: %w", err)
	}
	if err := p.pubsub.Publish(workflowOwnerChannel(owner), msg); err != nil {
		return xerrors.Errorf("ask the owner of run %s to stop it: %w", runID, err)
	}
	timer := p.clock.NewTimer(workflowStopTimeout, "chatd", "workflow_stop")
	defer timer.Stop()
	ticker := p.clock.NewTicker(workflowStopPollInterval, "chatd", "workflow_stop_poll")
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		case <-ticker.C:
			row, found, err := p.workflowJournal().getRun(ctx, runID)
			if err == nil && (!found || row.Status != workflowStatusRunning) {
				return nil
			}
		}
	}
}
