package chatd

// This file is fork code: stop_workflow, the API-side stop of the
// fork-workflows experiment. The coderd fork run API (fork_workflow_runs.go)
// calls ForkStopWorkflowRun; the registry stops the live run of this
// replica, a run that another live process owns is stopped through that
// owner's pubsub channel, and a run whose owner is gone is stopped through
// its journal rows.

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/forkdb"
)

// The sentinel errors of ForkStopWorkflowRun. The API maps them to 404 and
// 409; every other error is a failure of the stop itself.
var (
	// ErrForkWorkflowRunNotFound: no run with this ID is known on this chat.
	ErrForkWorkflowRunNotFound = errors.New("workflow run not found")
	// ErrForkWorkflowRunTerminal: the run already ended. A stopped or
	// completed run cannot stop again, and an interrupted run waits for a
	// resume by the agent instead.
	ErrForkWorkflowRunTerminal = errors.New("workflow run already ended")
)

// workflowStopError is the error of a stopped run and of its open steps.
const workflowStopError = "stopped by user"

// stopTimeout bounds how long the stop waits for the script goroutine to
// unwind after its context is canceled, so the API answers from a snapshot
// that already says stopped in the normal case.
const workflowStopTimeout = 5 * time.Second

// ForkStopWorkflowRun stops the run runID of chatID. The caller authorizes:
// this method runs the stop as chatd, like the engine itself. It returns
// ErrForkWorkflowRunNotFound for a run this chat does not own and
// ErrForkWorkflowRunTerminal for a run that already ended.
func (p *Server) ForkStopWorkflowRun(ctx context.Context, chatID, runID uuid.UUID) error {
	if run := p.workflows.get(runID); run != nil {
		if run.parent.ID != chatID {
			return ErrForkWorkflowRunNotFound
		}
		if err := p.stopHeldRun(ctx, run); !errors.Is(err, errWorkflowRunLost) {
			return err
		}
		// Another process took the run since this one last renewed its
		// heartbeat. The journal names the new owner.
	}

	// The registry does not hold the run: either it ended (the journal says
	// which), another process executes it, or its owner died. The journal is
	// the source of truth.
	row, found, err := p.workflowJournal().getRun(ctx, runID)
	if err != nil {
		return err
	}
	if !found || row.ChatID != chatID {
		return ErrForkWorkflowRunNotFound
	}
	if row.Status != workflowStatusRunning {
		return ErrForkWorkflowRunTerminal
	}
	if p.workflowOwnerLive(row.OwnerID, row.HeartbeatAt.Time, row.HeartbeatAt.Valid) {
		return p.stopRemoteRun(ctx, row.ID, row.OwnerID.UUID)
	}
	held, ok, err := p.stopOwnerlessRun(ctx, row)
	if held != nil {
		// A resume of this process registered the run meanwhile.
		return p.stopHeldRun(ctx, held)
	}
	if err != nil || ok {
		return err
	}
	// The run ended, or a resume elsewhere claimed it, since the read.
	row, found, err = p.workflowJournal().getRun(ctx, runID)
	if err != nil {
		return err
	}
	if !found {
		return ErrForkWorkflowRunNotFound
	}
	if row.Status != workflowStatusRunning || !row.OwnerID.Valid {
		return ErrForkWorkflowRunTerminal
	}
	return p.stopRemoteRun(ctx, row.ID, row.OwnerID.UUID)
}

// stopOwnerlessRun stops a running run whose owner is gone. A resume of
// this process claims the row before it registers the run, so the row can
// name this process while the registry is still empty. The stop takes the
// resume lock: under it the registry is current, and a resume cannot
// claim the row between this claim and the journaled stop. held is the run
// a resume registered meanwhile, which the caller stops instead. ok is
// false when the claim found the run ended or owned elsewhere. Claiming the
// run also fences a late write of the old owner before the stop writes.
func (p *Server) stopOwnerlessRun(ctx context.Context, row forkdb.WorkflowRun) (held *workflowRun, ok bool, err error) {
	p.workflows.resumeMu.Lock()
	defer p.workflows.resumeMu.Unlock()
	if run := p.workflows.get(row.ID); run != nil {
		return run, false, nil
	}
	claimed, ok, err := p.workflowJournal().claimRun(ctx, row.ID)
	if err != nil {
		return nil, false, xerrors.Errorf("claim the workflow run: %w", err)
	}
	if !ok {
		return nil, false, nil
	}
	return nil, true, p.stopJournaledRun(ctx, claimed)
}

// stopHeldRun stops a run of the registry. It confirms the ownership of a
// live run first: a run that another process claimed since the last
// heartbeat returns errWorkflowRunLost, and the caller stops it through
// the journal and its new owner.
func (p *Server) stopHeldRun(ctx context.Context, run *workflowRun) error {
	run.mu.Lock()
	live := run.result == nil && !run.lost
	run.mu.Unlock()
	if live && !run.confirmOwnership(ctx) && !run.isLost() {
		return xerrors.New("confirm the ownership of the workflow run: the journal could not be read")
	}
	if run.isLost() {
		return errWorkflowRunLost
	}
	if !run.requestStop() {
		return ErrForkWorkflowRunTerminal
	}
	// The script goroutine journals the stopped status, fails the open
	// steps and interrupts their children as it unwinds. Wait briefly,
	// so the API answers a snapshot that already says stopped.
	select {
	case <-run.done:
	case <-p.clock.NewTimer(workflowStopTimeout, "chatd", "workflow_stop").C:
	}
	return nil
}

// stopJournaledRun stops a run that this replica claimed from a dead owner.
// It fails the open steps and interrupts their children, then journals the
// stop. The claim fences the writes of the old owner, so a late write of
// its script cannot replace this state.
func (p *Server) stopJournaledRun(ctx context.Context, row forkdb.WorkflowRun) error {
	ictx, stop := p.inflightContext(dbauthz.AsSystemRestricted(context.Background()))
	defer stop()

	steps, err := p.workflowJournal().getSteps(ictx, row.ID)
	if err != nil {
		return err
	}
	for _, srow := range steps {
		if srow.Status == stepStatusRunning || srow.Status == stepStatusReminded {
			p.interruptWorkflowChild(ictx, row.ID, srow.ID)
		}
	}
	if _, err := p.workflowJournal().failOpenSteps(ictx, row.ID); err != nil {
		return xerrors.Errorf("fail the open steps of run %s: %w", row.ID, err)
	}
	// The stop carries a snapshot of the failed steps and the retained
	// lines, so a later await and terminal replay read the same result a
	// stop of a held run produces. The lines come from the journal: the
	// interrupted steps' failure lines were appended by a live flush, and
	// an interrupted run this replica does not hold cannot append more.
	stepRows, err := p.workflowJournal().getSteps(ictx, row.ID)
	if err != nil {
		return xerrors.Errorf("load the workflow steps of run %s: %w", row.ID, err)
	}
	logs, _, _ := restoreWorkflowLogs(row.Logs)
	res := runWorkflowResult{
		RunID:        row.ID.String(),
		Status:       workflowStatusStopped,
		Error:        workflowStopError,
		Phases:       append([]string{}, row.Phases...),
		CurrentPhase: row.CurrentPhase,
		Steps:        make([]stepSummary, 0, len(stepRows)),
		Logs:         logs,
	}
	for _, srow := range stepRows {
		res.Steps = append(res.Steps, stepSummary{
			ChatID: srow.ID.String(),
			Title:  srow.Title,
			Status: srow.Status,
			Reused: srow.Reused,
			Phase:  srow.Phase,
			Error:  srow.Error,
		})
	}
	// The claimed run is in no registry, so nothing retries a failed
	// terminal write: the caller learns of it and the user can stop again.
	fenced, err := p.workflowJournal().writeRunStatus(ictx, row.ID, workflowStatusStopped, workflowStopError, &res,
		&workflowPhases{names: row.Phases, current: row.CurrentPhase})
	if err != nil {
		return xerrors.Errorf("journal the stop of run %s: %w", row.ID, err)
	}
	if fenced {
		return xerrors.Errorf("journal the stop of run %s: another process claimed the run", row.ID)
	}
	return nil
}

// interruptWorkflowChild stops the child chat of one step row. It is the
// journal-row form of the engine's interruptStepChild.
func (p *Server) interruptWorkflowChild(ctx context.Context, runID, chatID uuid.UUID) {
	child, err := p.db.GetChatByID(ctx, chatID)
	if err != nil {
		p.logger.Warn(ctx, "load workflow step chat",
			slog.F("chat_id", chatID), slog.F("run_id", runID), slog.Error(err))
		return
	}
	if child.Status == database.ChatStatusWaiting || child.Status == database.ChatStatusError ||
		child.Status == database.ChatStatusPaused {
		return // nothing runs in the child
	}
	if _, _, err := p.InterruptChat(ctx, child); err != nil {
		p.logger.Warn(ctx, "interrupt workflow step chat",
			slog.F("chat_id", chatID), slog.F("run_id", runID), slog.Error(err))
	}
}
