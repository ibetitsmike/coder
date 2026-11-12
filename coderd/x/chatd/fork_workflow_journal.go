package chatd

// This file is fork code: the durable journal of the fork-workflows
// experiment. The registry (fork_workflow.go) holds a run in memory on one
// replica; the workflow_runs and workflow_steps tables (chatdmig node
// 01a1d041) let a restart resume a run and replay its finished steps without
// running them again. The engine writes every state transition through, and
// a resume compiles the journaled script and rehydrates the journaled steps.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/sqlc-dev/pqtype"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/forkdb"
)

// workflowInterruptedError is the error that the startup sweep writes on the
// runs of a dead process.
const workflowInterruptedError = "the coderd process restarted before the run ended"

// jsonbValue wraps an optional JSON document for a nullable jsonb column.
// A nil json.RawMessage is not a driver NULL: database/sql converts it to a
// zero-length []byte, which Postgres rejects as invalid JSON, so it must go
// through NullRawMessage to reach the column as NULL.
func jsonbValue(raw json.RawMessage) pqtype.NullRawMessage {
	return pqtype.NullRawMessage{RawMessage: raw, Valid: len(raw) > 0}
}

// workflowJournal writes the run and step state of one replica through to// workflowJournal writes the run and step state of one replica through to
// the journal. Every method runs as the system-restricted actor: the journal
// is chatd-internal state of chats that chatd manages, and the turn context
// is the chatd daemon subject, which cannot otherwise update a chat (the way
// the owner gate reads the owner row). fork_forkdb.go still authorizes each
// query through the run's chat.
type workflowJournal struct {
	p *Server
}

func (p *Server) workflowJournal() workflowJournal {
	return workflowJournal{p: p}
}

func (j workflowJournal) store(ctx context.Context) (forkdb.Store, error) {
	return forkdb.From(j.p.db)
}

func journalCtx(ctx context.Context) context.Context {
	return dbauthz.AsSystemRestricted(ctx)
}

// insertRun journals a fresh run before its script goroutine starts. A
// failure fails the run_workflow call: an unjournaled run would lose its
// resume state on a restart.
func (j workflowJournal) insertRun(ctx context.Context, run *workflowRun) error {
	fdb, err := j.store(ctx)
	if err != nil {
		return err
	}
	return fdb.InsertWorkflowRun(journalCtx(ctx), forkdb.InsertWorkflowRunParams{
		ID:            run.id,
		ChatID:        run.parent.ID,
		ModelConfigID: run.modelConfigID,
		Script:        run.script,
		Args:          []byte(run.argsJSON),
		Status:        workflowStatusRunning,
	})
}

// getRun reads one journal row. found is false when the run was never
// journaled on this database.
func (j workflowJournal) getRun(ctx context.Context, id uuid.UUID) (run forkdb.WorkflowRun, found bool, err error) {
	fdb, err := j.store(ctx)
	if err != nil {
		return forkdb.WorkflowRun{}, false, err
	}
	run, err = fdb.GetWorkflowRun(journalCtx(ctx), id)
	if errors.Is(err, sql.ErrNoRows) {
		return forkdb.WorkflowRun{}, false, nil
	}
	if err != nil {
		return forkdb.WorkflowRun{}, false, err
	}
	return run, true, nil
}

// updateRunStatus journals a run transition. It is best effort: the
// in-memory state of a live run stays authoritative, and a lost write only
// costs the replay that the journal exists to prevent.
func (j workflowJournal) updateRunStatus(ctx context.Context, id uuid.UUID, status, errText string, result *runWorkflowResult) {
	fdb, err := j.store(ctx)
	if err != nil {
		j.warn(ctx, "open workflow journal store", id, err)
		return
	}
	var raw json.RawMessage
	if result != nil {
		data, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			j.warn(ctx, "marshal workflow run result", id, marshalErr)
			return
		}
		raw = data
	}
	if err := fdb.UpdateWorkflowRunStatus(journalCtx(ctx), forkdb.UpdateWorkflowRunStatusParams{
		ID:     id,
		Status: status,
		Error:  errText,
		Result: jsonbValue(raw),
	}); err != nil {
		j.warn(ctx, "journal workflow run status", id, err)
	}
}

// markInterrupted marks every open run of a dead process interrupted. It
// runs once at chatd startup, before any turn can resume a run.
func (j workflowJournal) markInterrupted(ctx context.Context) (int64, error) {
	fdb, err := j.store(ctx)
	if err != nil {
		return 0, err
	}
	return fdb.MarkOpenWorkflowRunsInterrupted(journalCtx(ctx), workflowInterruptedError)
}

// insertStep journals a step after its child chat was created. A failure
// fails the step and the run, and stops the child: a step that the journal
// lost must not keep consuming model capacity.
func (j workflowJournal) insertStep(ctx context.Context, runID uuid.UUID, step *workflowStep) error {
	fdb, err := j.store(ctx)
	if err != nil {
		return err
	}
	return fdb.InsertWorkflowStep(journalCtx(ctx), forkdb.InsertWorkflowStepParams{
		ID:         step.id,
		RunID:      runID,
		SpecKey:    step.specKey,
		Occurrence: int32(step.occurrence),
		Ordinal:    int32(step.ordinal),
		Title:      step.title,
		Status:     step.status,
		Value:      jsonbValue(step.value),
	})
}

// updateStep journals a step transition, best effort like updateRunStatus.
func (j workflowJournal) updateStep(ctx context.Context, step *workflowStep) {
	fdb, err := j.store(ctx)
	if err != nil {
		j.warn(ctx, "open workflow journal store", step.id, err)
		return
	}
	if err := fdb.UpdateWorkflowStep(journalCtx(ctx), forkdb.UpdateWorkflowStepParams{
		ID:     step.id,
		Status: step.status,
		Value:  jsonbValue(step.value),
		Error:  step.err,
	}); err != nil {
		j.warn(ctx, "journal workflow step status", step.id, err)
	}
}

func (j workflowJournal) getSteps(ctx context.Context, runID uuid.UUID) ([]forkdb.WorkflowStep, error) {
	fdb, err := j.store(ctx)
	if err != nil {
		return nil, err
	}
	return fdb.GetWorkflowStepsByRun(journalCtx(ctx), runID)
}

// forkInitWorkflowJournal marks the open runs of previous processes
// interrupted. chatd runs it once at startup, before any turn can resume a
// run.
func (p *Server) forkInitWorkflowJournal() {
	ctx := journalCtx(p.ctx)
	n, err := p.workflowJournal().markInterrupted(ctx)
	if err != nil {
		p.logger.Warn(p.ctx, "mark workflow runs interrupted", slog.Error(err))
		return
	}
	if n > 0 {
		p.logger.Info(p.ctx, "marked workflow runs interrupted", slog.F("count", n))
	}
}

// resumeWorkflowRun continues a run that the registry lost: the journaled
// run of this chat. A terminal row returns its result snapshot at once; an
// open row rehydrates and re-executes the journaled script, where finished
// steps replay their journaled values and unfinished steps re-run in their
// child chats.
func (p *Server) resumeWorkflowRun(
	ctx context.Context,
	chat database.Chat,
	row forkdb.WorkflowRun,
) (*workflowRun, error) {
	if row.Status == workflowStatusCompleted || row.Status == workflowStatusFailed || row.Status == workflowStatusStopped {
		run, err := terminalWorkflowRun(p, chat, row)
		if err != nil {
			return nil, err
		}
		if _, added := p.workflows.add(run); !added {
			return p.workflows.get(row.ID), nil
		}
		return run, nil
	}
	run, err := p.rehydrateWorkflowRun(ctx, chat, row)
	if err != nil {
		return nil, err
	}
	if _, added := p.workflows.add(run); !added {
		// Another caller resumed the same run first.
		return p.workflows.get(row.ID), nil
	}
	// The run is live again before its script starts, so a crash during the
	// resume leaves the row resumable.
	p.workflowJournal().updateRunStatus(ctx, row.ID, workflowStatusRunning, "", nil)
	runCtx, stop := p.inflightChatContext(dbauthz.AsChatd(context.Background()), chat) //nolint:gocritic // see startWorkflowRun
	err = p.goInflight(func() {
		defer stop()
		run.finish(runCtx, run.execute(runCtx))
	})
	if err != nil {
		stop()
		p.workflows.remove(row.ID)
		p.workflowJournal().updateRunStatus(ctx, row.ID, workflowStatusFailed, "resume the run: "+err.Error(), nil)
		return nil, xerrors.Errorf("resume workflow run: %w", err)
	}
	return run, nil
}

// rehydrateWorkflowRun rebuilds an open run from its journal rows. seen
// starts at zero per spec key, so the re-executed script maps its k-th call
// of a spec to the journaled step with occurrence k-1, the way the first
// process numbered them.
func (p *Server) rehydrateWorkflowRun(
	ctx context.Context,
	chat database.Chat,
	row forkdb.WorkflowRun,
) (*workflowRun, error) {
	program, err := compileWorkflow(row.Script)
	if err != nil {
		return nil, xerrors.Errorf("the journaled script of run %s no longer compiles: %w", row.ID, err)
	}
	stepRows, err := p.workflowJournal().getSteps(ctx, row.ID)
	if err != nil {
		return nil, xerrors.Errorf("load the workflow steps of run %s: %w", row.ID, err)
	}
	run := &workflowRun{
		p:             p,
		id:            row.ID,
		parent:        chat,
		modelConfigID: row.ModelConfigID,
		program:       program,
		script:        row.Script,
		argsJSON:      string(row.Args),
		seen:          map[string]int{},
		done:          make(chan struct{}),
	}
	for _, srow := range stepRows {
		step := &workflowStep{
			id:         srow.ID,
			chatID:     srow.ID,
			specKey:    srow.SpecKey,
			occurrence: int(srow.Occurrence),
			ordinal:    int(srow.Ordinal),
			title:      srow.Title,
			reused:     true,
			status:     srow.Status,
			err:        srow.Error,
			// The previous process may already have sent the one reminder.
			sentReminder: srow.Status == stepStatusReminded,
		}
		if srow.Value.Valid {
			step.value = json.RawMessage(srow.Value.RawMessage)
		}
		run.steps = append(run.steps, step)
	}
	return run, nil
}

// terminalWorkflowRun rebuilds a run that ended before the restart, so
// run_workflow and await_workflow return the same result without executing
// anything.
func terminalWorkflowRun(p *Server, chat database.Chat, row forkdb.WorkflowRun) (*workflowRun, error) {
	res := runWorkflowResult{RunID: row.ID.String(), Status: row.Status, Error: row.Error}
	if row.Result.Valid {
		if err := json.Unmarshal(row.Result.RawMessage, &res); err != nil {
			return nil, xerrors.Errorf("unmarshal the journal result of run %s: %w", row.ID, err)
		}
	}
	// A run reaches this constructor only after the registry lost it, so
	// every step in the stored result is replayed, never re-executed.
	for i := range res.Steps {
		res.Steps[i].Reused = true
	}
	run := &workflowRun{
		p:        p,
		id:       row.ID,
		parent:   chat,
		script:   row.Script,
		argsJSON: string(row.Args),
		result:   &res,
		notified: true,
		done:     make(chan struct{}),
	}
	close(run.done)
	return run, nil
}

// workflowResultFromJournal answers await_workflow for a run that this
// replica does not hold. found is false for a run this database does not
// know or that belongs to another chat.
func (p *Server) workflowResultFromJournal(
	ctx context.Context,
	chatID uuid.UUID,
	runID uuid.UUID,
) (res runWorkflowResult, found bool, err error) {
	row, found, err := p.workflowJournal().getRun(ctx, runID)
	if err != nil || !found {
		return runWorkflowResult{}, false, err
	}
	if row.ChatID != chatID {
		return runWorkflowResult{}, false, nil
	}
	if row.Status == workflowStatusCompleted || row.Status == workflowStatusFailed || row.Status == workflowStatusStopped {
		run, err := terminalWorkflowRun(p, database.Chat{ID: chatID}, row)
		if err != nil {
			return runWorkflowResult{}, false, err
		}
		return *run.result, true, nil
	}
	stepRows, err := p.workflowJournal().getSteps(ctx, row.ID)
	if err != nil {
		return runWorkflowResult{}, false, xerrors.Errorf("load the workflow steps of run %s: %w", row.ID, err)
	}
	res = runWorkflowResult{
		RunID:  row.ID.String(),
		Status: row.Status,
		Error:  row.Error,
		Steps:  make([]stepSummary, 0, len(stepRows)),
	}
	for _, srow := range stepRows {
		res.Steps = append(res.Steps, stepSummary{
			ChatID: srow.ID.String(),
			Title:  srow.Title,
			Status: srow.Status,
			Reused: true,
			Error:  srow.Error,
		})
	}
	return res, true, nil
}

func (j workflowJournal) warn(ctx context.Context, msg string, id uuid.UUID, err error) {
	j.p.logger.Warn(ctx, msg, slog.F("workflow_id", id), slog.Error(err))
}
