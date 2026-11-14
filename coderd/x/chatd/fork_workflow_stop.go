package chatd

// This file is fork code: stop_workflow, the API-side stop of the
// fork-workflows experiment. The coderd fork run API (fork_workflow_runs.go)
// calls ForkStopWorkflowRun; the registry stops the live run of this
// replica, and a run that this replica does not hold is stopped through its
// journal rows.

import (
	"context"
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
	ErrForkWorkflowRunNotFound = xerrors.New("workflow run not found")
	// ErrForkWorkflowRunTerminal: the run already ended. A stopped or
	// completed run cannot stop again, and an interrupted run waits for a
	// resume by the agent instead.
	ErrForkWorkflowRunTerminal = xerrors.New("workflow run already ended")
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

	// The registry does not hold the run: either it ended (the journal says
	// which) or it is mid-resume. The journal is the source of truth.
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
	return p.stopJournaledRun(ctx, row)
}

// stopJournaledRun stops a run that this replica does not hold but the
// journal calls running: mid-resume at worst. It fails the open steps and
// interrupts their children, then journals the stop. A live script that
// still unwinds elsewhere would journal its own end over this state, which
// single-replica chatd does not produce.
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
	p.workflowJournal().updateRunStatus(ictx, row.ID, workflowStatusStopped, workflowStopError, nil)
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
