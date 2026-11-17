package chatd

// This file is fork code: the durable journal of the fork-workflows
// experiment. The registry (fork_workflow.go) holds a run in memory on one
// replica; the workflow_runs and workflow_steps tables (chatdmig node
// 01a1d041) let a restart resume a run and replay its finished steps without
// running them again. The engine writes every state transition through, and
// a resume compiles the journaled script and rehydrates the journaled steps.
// Each run names its owner process (fork_workflow_owner.go), and every
// engine write carries that owner, so the write of a process that lost the
// run changes nothing.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/sqlc-dev/pqtype"

	"cdr.dev/slog/v3"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/forkdb"
)

// workflowInterruptedError is the error that the startup sweep writes on the
// runs of a dead process.
const workflowInterruptedError = "the coderd process restarted before the run ended"

// workflowStepsPkey is the primary key of workflow_steps. A step ID derives
// from the run ID and the step spec, so two runners of one run insert the
// same key.
const workflowStepsPkey database.UniqueConstraint = "workflow_steps_pkey"

// jsonbValue wraps an optional JSON document for a nullable jsonb column.
// A nil json.RawMessage is not a driver NULL: database/sql converts it to a
// zero-length []byte, which Postgres rejects as invalid JSON, so it must go
// through NullRawMessage to reach the column as NULL.
func jsonbValue(raw json.RawMessage) pqtype.NullRawMessage {
	return pqtype.NullRawMessage{RawMessage: raw, Valid: len(raw) > 0}
}

// workflowJournal writes the run and step state of one replica through to
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

// owner is the owner fence of the engine writes: the worker ID of this
// process. A server that never ran forkInitWorkflowJournal has none, and a
// NULL owner skips the fence.
func (j workflowJournal) owner() uuid.NullUUID {
	id := j.p.workflows.owner.id
	return uuid.NullUUID{UUID: id, Valid: id != uuid.Nil}
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
		Phases:        append([]string(nil), run.phases...),
		CurrentPhase:  run.currentPhase,
		OwnerID:       j.owner(),
		HeartbeatAt:   sql.NullTime{Time: j.p.clock.Now(), Valid: true},
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
// costs the replay that the journal exists to prevent. phases carries the
// run's phase list and current phase, the state phase() changes while the
// script runs; a nil phases clears them to empty, so a caller outside the
// script goroutine must pass the run's snapshot instead. Every caller
// passes it. fenced is true when the write reached the database but the
// owner fence rejected it: this process no longer owns the run.
func (j workflowJournal) updateRunStatus(
	ctx context.Context, id uuid.UUID, status, errText string, result *runWorkflowResult,
	phases *workflowPhases,
) (fenced bool) {
	fenced, err := j.writeRunStatus(ctx, id, status, errText, result, phases)
	if err != nil {
		j.warn(ctx, "journal workflow run status", id, err)
	}
	return fenced
}

// writeRunStatus is updateRunStatus with the error of a failed write, for
// the terminal write of a run, which the heartbeat retries
// (fork_workflow_owner.go).
func (j workflowJournal) writeRunStatus(
	ctx context.Context, id uuid.UUID, status, errText string, result *runWorkflowResult,
	phases *workflowPhases,
) (fenced bool, err error) {
	fdb, err := j.store(ctx)
	if err != nil {
		return false, xerrors.Errorf("open workflow journal store: %w", err)
	}
	var raw json.RawMessage
	if result != nil {
		data, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			return false, xerrors.Errorf("marshal workflow run result: %w", marshalErr)
		}
		raw = data
	}
	params := forkdb.UpdateWorkflowRunStatusParams{
		ID:      id,
		Status:  status,
		Error:   errText,
		Result:  jsonbValue(raw),
		OwnerID: j.owner(),
	}
	if phases != nil {
		params.Phases = phases.names
		params.CurrentPhase = phases.current
	}
	rows, err := fdb.UpdateWorkflowRunStatus(journalCtx(ctx), params)
	if err != nil {
		return false, err
	}
	return rows == 0, nil
}

// updateRunPhases journals the phase state of a live run, best effort like
// updateRunStatus. It touches only the phase columns, so it never overwrites
// the status write of a run that ends at the same time.
func (j workflowJournal) updateRunPhases(ctx context.Context, id uuid.UUID, phases *workflowPhases) {
	if phases == nil {
		return
	}
	fdb, err := j.store(ctx)
	if err != nil {
		j.warn(ctx, "open workflow journal store", id, err)
		return
	}
	if err := fdb.UpdateWorkflowRunPhases(journalCtx(ctx), forkdb.UpdateWorkflowRunPhasesParams{
		ID:           id,
		Phases:       phases.names,
		CurrentPhase: phases.current,
		OwnerID:      j.owner(),
	}); err != nil {
		j.warn(ctx, "journal workflow run phases", id, err)
	}
}

// workflowPhases is the journaled phase state of a run: the display list
// and the current name. phase() changes it on the script goroutine, so a
// journal write reads it through the script goroutine's own pointer.
type workflowPhases struct {
	names   []string
	current string
}

// phasesSnapshot copies the run's phase state for a journal write. It runs
// on the script goroutine, so its unlocked read cannot race with phase(),
// which runs on the same goroutine.
func (r *workflowRun) phasesSnapshot() *workflowPhases {
	return &workflowPhases{names: append([]string(nil), r.phases...), current: r.currentPhase}
}

// markStale marks the running runs of dead owners interrupted: the runs
// whose heartbeat is older than workflowStaleAfter, or that have none. It
// runs at chatd startup and on every heartbeat tick.
func (j workflowJournal) markStale(ctx context.Context) (int64, error) {
	fdb, err := j.store(ctx)
	if err != nil {
		return 0, err
	}
	return fdb.MarkStaleWorkflowRunsInterrupted(journalCtx(ctx), forkdb.MarkStaleWorkflowRunsInterruptedParams{
		ErrText:     workflowInterruptedError,
		StaleBefore: j.p.clock.Now().Add(-workflowStaleAfter),
	})
}

// heartbeat renews the heartbeat of the runs ids and returns the ones this
// process still owns.
func (j workflowJournal) heartbeat(ctx context.Context, ids []uuid.UUID) ([]uuid.UUID, error) {
	fdb, err := j.store(ctx)
	if err != nil {
		return nil, err
	}
	return fdb.HeartbeatWorkflowRuns(journalCtx(ctx), forkdb.HeartbeatWorkflowRunsParams{
		Now:     j.p.clock.Now(),
		Ids:     slices.Clone(ids),
		OwnerID: j.owner(),
	})
}

// claimRun makes this process the owner of an interrupted run, of a
// running run whose owner is stale, or of a run it already owns. found is
// false when a live process owns the run or the run ended.
func (j workflowJournal) claimRun(ctx context.Context, id uuid.UUID) (forkdb.WorkflowRun, bool, error) {
	fdb, err := j.store(ctx)
	if err != nil {
		return forkdb.WorkflowRun{}, false, err
	}
	now := j.p.clock.Now()
	row, err := fdb.ClaimWorkflowRun(journalCtx(ctx), forkdb.ClaimWorkflowRunParams{
		OwnerID:     j.owner(),
		Now:         now,
		ID:          id,
		StaleBefore: now.Add(-workflowStaleAfter),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return forkdb.WorkflowRun{}, false, nil
	}
	if err != nil {
		return forkdb.WorkflowRun{}, false, err
	}
	return row, true, nil
}

// releaseRuns marks the runs this process still owns interrupted. Close
// runs it, so the next process resumes them at once.
func (j workflowJournal) releaseRuns(ctx context.Context) (int64, error) {
	owner := j.owner()
	if !owner.Valid {
		return 0, nil
	}
	fdb, err := j.store(ctx)
	if err != nil {
		return 0, err
	}
	return fdb.ReleaseWorkflowRuns(journalCtx(ctx), forkdb.ReleaseWorkflowRunsParams{
		ErrText: workflowInterruptedError,
		OwnerID: owner,
	})
}

// insertStep journals a step after its child chat was created. A failure
// fails the step and the run, and stops the child: a step that the journal
// lost must not keep consuming model capacity. errWorkflowRunLost means
// this process no longer owns the run; the child then belongs to the new
// owner, which adopts it.
func (j workflowJournal) insertStep(ctx context.Context, runID uuid.UUID, step *workflowStep) error {
	fdb, err := j.store(ctx)
	if err != nil {
		return err
	}
	rows, err := fdb.InsertWorkflowStep(journalCtx(ctx), forkdb.InsertWorkflowStepParams{
		ID:         step.id,
		RunID:      runID,
		SpecKey:    step.specKey,
		Occurrence: int32(step.occurrence),
		Ordinal:    int32(step.ordinal),
		Title:      step.title,
		Status:     step.status,
		Value:      jsonbValue(step.value),
		Reused:     step.reused,
		Phase:      step.phase,
		OwnerID:    j.owner(),
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return errWorkflowRunLost
	}
	return nil
}

// failOpenSteps journals the open steps of a run that is being stopped as
// failed. The caller interrupts each child chat first, so the row state and
// the child state land together.
func (j workflowJournal) failOpenSteps(ctx context.Context, runID uuid.UUID) (int64, error) {
	fdb, err := j.store(ctx)
	if err != nil {
		return 0, err
	}
	return fdb.FailOpenWorkflowStepsOfRun(journalCtx(ctx), forkdb.FailOpenWorkflowStepsOfRunParams{
		RunID:   runID,
		ErrText: workflowStopError,
		OwnerID: j.owner(),
	})
}

// updateStep journals a step transition, best effort like updateRunStatus.
func (j workflowJournal) updateStep(ctx context.Context, step *workflowStep) {
	fdb, err := j.store(ctx)
	if err != nil {
		j.warn(ctx, "open workflow journal store", step.id, err)
		return
	}
	if _, err := fdb.UpdateWorkflowStep(journalCtx(ctx), forkdb.UpdateWorkflowStepParams{
		ID:      step.id,
		Status:  step.status,
		Value:   jsonbValue(step.value),
		Error:   step.err,
		Reused:  step.reused,
		OwnerID: j.owner(),
	}); err != nil {
		j.warn(ctx, "journal workflow step status", step.id, err)
	}
}

// markStepReused journals the reuse flag of a replayed step. A finished row
// keeps its status, value and finished_at; only the flag flips. Best effort
// like updateStep. The flag only records that a resume replayed the step,
// so the write carries no owner fence.
func (j workflowJournal) markStepReused(ctx context.Context, id uuid.UUID) {
	fdb, err := j.store(ctx)
	if err != nil {
		j.warn(ctx, "open workflow journal store", id, err)
		return
	}
	if err := fdb.MarkWorkflowStepReused(journalCtx(ctx), id); err != nil {
		j.warn(ctx, "journal workflow step reused", id, err)
	}
}

// markStepsReused journals the reused flag of every step of a run. The
// resume of a terminal run replays its whole snapshot, so the rows can carry
// the flag the result already reports. Best effort like updateStep.
func (j workflowJournal) markStepsReused(ctx context.Context, runID uuid.UUID) {
	fdb, err := j.store(ctx)
	if err != nil {
		j.warn(ctx, "open workflow journal store", runID, err)
		return
	}
	if err := fdb.MarkWorkflowStepsReusedByRun(journalCtx(ctx), runID); err != nil {
		j.warn(ctx, "journal workflow steps reused", runID, err)
	}
}

// getStep loads one journaled step. The authorizer of GetWorkflowStep reads
// the step to find its run.
func (j workflowJournal) getStep(ctx context.Context, id uuid.UUID) (forkdb.WorkflowStep, error) {
	fdb, err := j.store(ctx)
	if err != nil {
		return forkdb.WorkflowStep{}, err
	}
	return fdb.GetWorkflowStep(journalCtx(ctx), id)
}

// reportStep journals a report that no live run will read from the chat
// history: the step child of an interrupted run recovered through chat
// recovery and reported before anyone resumed the run. Unlike updateStep it
// is not best effort: the tool's answer tells the model whether the value
// reached the journal, and a lost write would answer "accepted" for a value
// that only the next report's retry can save. It carries no owner fence:
// a report reaches the row whichever process owns the run. landed is false
// when the row already holds a report of a running run, which wins.
func (j workflowJournal) reportStep(ctx context.Context, step *workflowStep) (landed bool, err error) {
	fdb, err := j.store(ctx)
	if err != nil {
		return false, err
	}
	n, err := fdb.UpdateWorkflowStep(journalCtx(ctx), forkdb.UpdateWorkflowStepParams{
		ID:     step.id,
		Status: step.status,
		Value:  jsonbValue(step.value),
		Error:  step.err,
		Reused: step.reused,
	})
	return n > 0, err
}

// writeReport journals the report of step for journalStepState, which only
// needs to know that the write ran.
func (j workflowJournal) writeReport(ctx context.Context, step *workflowStep) error {
	_, err := j.reportStep(ctx, step)
	return err
}

// setLogs writes the retained log lines of a live run through to the journal,
// best effort like updateRunStatus. seq orders the write: the query replaces
// the column only when the stored sequence is not ahead, so a stalled write
// of an older snapshot cannot replace newer lines. The cap holds because the
// engine always writes the retained tail.
func (j workflowJournal) setLogs(ctx context.Context, runID uuid.UUID, seq int64, logs []string) {
	fdb, err := j.store(ctx)
	if err != nil {
		j.warn(ctx, "open workflow journal store", runID, err)
		return
	}
	stored := struct {
		ForkSeq int64    `json:"fork_seq"`
		Lines   []string `json:"lines"`
	}{ForkSeq: seq, Lines: logs}
	raw, err := json.Marshal(stored)
	if err != nil {
		j.warn(ctx, "marshal workflow run logs", runID, err)
		return
	}
	if err := fdb.SetWorkflowRunLogs(journalCtx(ctx), forkdb.SetWorkflowRunLogsParams{
		ID:      runID,
		Lines:   raw,
		ForkSeq: seq,
		OwnerID: j.owner(),
	}); err != nil {
		j.warn(ctx, "journal workflow run logs", runID, err)
	}
}

// RestoreWorkflowRunLogs decodes the retained lines of a live run for
// readers outside chatd, for example the run detail route.
func RestoreWorkflowRunLogs(raw json.RawMessage) ([]string, int64, int) {
	return restoreWorkflowLogs(raw)
}

// restoreWorkflowLogs decodes the retained lines of a live run with their
// journal sequence. It returns nil for a column that holds no payload, for
// example on a row written before the live-logs node.
func restoreWorkflowLogs(raw json.RawMessage) ([]string, int64, int) {
	if len(raw) == 0 || !json.Valid(raw) {
		return nil, 0, 0
	}
	var stored struct {
		ForkSeq int64    `json:"fork_seq"`
		Lines   []string `json:"lines"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		// A plain array holds the backfill of the node that introduced the
		// column, before the envelope carried the sequence.
		var legacy []string
		if err := json.Unmarshal(raw, &legacy); err == nil && legacy != nil {
			bytes := 0
			for _, line := range legacy {
				bytes += len(line)
			}
			return legacy, 0, bytes
		}
		return nil, 0, 0
	}
	if stored.Lines == nil {
		return nil, 0, 0
	}
	bytes := 0
	for _, line := range stored.Lines {
		bytes += len(line)
	}
	return stored.Lines, stored.ForkSeq, bytes
}

func (j workflowJournal) getSteps(ctx context.Context, runID uuid.UUID) ([]forkdb.WorkflowStep, error) {
	fdb, err := j.store(ctx)
	if err != nil {
		return nil, err
	}
	return fdb.GetWorkflowStepsByRun(journalCtx(ctx), runID)
}

// resumeWorkflowRun continues a run that the registry lost: the journaled
// run of this chat. A terminal row returns its result snapshot at once; an
// open row rehydrates and re-executes the journaled script, where finished
// steps replay their journaled values and unfinished steps re-run in their
// child chats. A resume first claims the run, so a run that another live
// process executes is not executed twice: the resume answers with a
// *workflowRunElsewhereError that carries the journal state.
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
		// The replayed steps mark reused in the journal rows too, so the
		// run detail reads the flag without the snapshot. The flush is
		// best effort and leaves the terminal snapshot untouched. The first
		// caller alone journals it: a caller that lost the race replayed
		// nothing, so it must not write.
		if _, added := p.workflows.add(run); !added {
			return p.workflows.get(row.ID), nil
		}
		p.workflowJournal().markStepsReused(ctx, row.ID)
		return run, nil
	}
	// The claim makes the row running and owned by this process before the
	// script starts, so a crash during the resume leaves the row resumable
	// once the heartbeat goes stale.
	p.workflows.resumeMu.Lock()
	claimed, ok, err := p.workflowJournal().claimRun(ctx, row.ID)
	if err != nil {
		p.workflows.resumeMu.Unlock()
		return nil, xerrors.Errorf("claim the workflow run: %w", err)
	}
	if !ok {
		p.workflows.resumeMu.Unlock()
		return p.resumeUnclaimedWorkflowRun(ctx, chat, row.ID)
	}
	row = claimed
	p.workflows.setClaiming(row.ID)
	run, err := p.rehydrateWorkflowRun(ctx, chat, row)
	if err != nil {
		p.workflows.setClaiming(uuid.Nil)
		// The row stays resumable for a later process that can compile it,
		// unless another caller of this process resumed the run meanwhile.
		if p.workflows.get(row.ID) == nil {
			p.workflowJournal().updateRunStatus(ctx, row.ID, workflowStatusInterrupted, "resume the run: "+err.Error(), nil,
				&workflowPhases{names: row.Phases, current: row.CurrentPhase})
		}
		p.workflows.resumeMu.Unlock()
		return nil, err
	}
	_, added := p.workflows.add(run)
	p.workflows.setClaiming(uuid.Nil)
	p.workflows.resumeMu.Unlock()
	if !added {
		// Another caller resumed the same run first.
		return p.workflows.get(row.ID), nil
	}
	runCtx, stop := p.inflightChatContext(dbauthz.AsChatd(context.Background()), chat) //nolint:gocritic // see startWorkflowRun
	run.mu.Lock()
	run.cancel = stop
	run.mu.Unlock()
	err = p.goInflight(func() {
		defer stop()
		run.finish(runCtx, run.execute(runCtx))
	})
	if err != nil {
		stop()
		p.workflows.remove(row.ID)
		p.workflowJournal().updateRunStatus(ctx, row.ID, workflowStatusFailed, "resume the run: "+err.Error(), nil,
			&workflowPhases{names: row.Phases, current: row.CurrentPhase})
		return nil, xerrors.Errorf("resume workflow run: %w", err)
	}
	return run, nil
}

// resumeUnclaimedWorkflowRun answers a resume whose claim found no row: the
// run ended since the read, a caller of this process resumed it, or another
// live process executes it.
func (p *Server) resumeUnclaimedWorkflowRun(ctx context.Context, chat database.Chat, id uuid.UUID) (*workflowRun, error) {
	if run := p.workflows.get(id); run != nil {
		return run, nil
	}
	row, found, err := p.workflowJournal().getRun(ctx, id)
	if err != nil {
		return nil, xerrors.Errorf("load the workflow journal: %w", err)
	}
	if !found {
		return nil, xerrors.Errorf("the workflow run %s is no longer in the journal", id)
	}
	if row.Status == workflowStatusCompleted || row.Status == workflowStatusFailed || row.Status == workflowStatusStopped {
		return p.resumeWorkflowRun(ctx, chat, row)
	}
	res, _, err := p.workflowResultFromJournal(ctx, chat.ID, id)
	if err != nil {
		return nil, err
	}
	return nil, &workflowRunElsewhereError{result: res}
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
		// A resumed run keeps the phases of the first process: the declared
		// list and every name the script had reached, and the phase that
		// was current when the process died. The re-executed script reads
		// PHASES again, which only appends names it does not hold yet.
		phases:       row.Phases,
		currentPhase: row.CurrentPhase,
		done:         make(chan struct{}),
	}
	// The replay keeps the retained pre-restart lines as its baseline: the
	// view and the journal start from them, and appendLogLocked suppresses
	// the first logEchoLeft replayed lines by count, not by content, so
	// the history holds one copy. The count covers every line the journal
	// already accepted, past the cap when fork_seq is above it, so a
	// capped baseline does not turn the replayed prefix live.
	// The counters start at the sequence the payload carries, which is past
	// the retained lines when the run already logged more than the cap: a
	// new line must write past the stored sequence, not behind it.
	var seq int64
	run.logs, seq, run.logBytes = restoreWorkflowLogs(row.Logs)
	run.logSeq = max(len(run.logs), int(seq))
	run.logEchoLeft = max(int(seq), len(run.logs))
	// The journal holds the lines at the sequence its payload carries, so a
	// replayed line that matches the baseline does not write again.
	run.journalLogWrite = make(chan struct{}, 1)
	// The write token starts in the channel, so the first flush of the
	// resumed run takes it at once.
	run.journalLogWrite <- struct{}{}
	run.journaledLogSeq = max(len(run.logs), int(seq))
	for _, srow := range stepRows {
		run.steps = append(run.steps, journaledWorkflowStep(srow))
	}
	return run, nil
}

// journaledWorkflowStep rebuilds a step from its journal row.
func journaledWorkflowStep(srow forkdb.WorkflowStep) *workflowStep {
	step := &workflowStep{
		id:         srow.ID,
		chatID:     srow.ID,
		specKey:    srow.SpecKey,
		occurrence: int(srow.Occurrence),
		ordinal:    int(srow.Ordinal),
		title:      srow.Title,
		// A step read from the journal is replayed, never run again, so the
		// journal marks it reused and the detail reads the flag.
		reused: true,
		phase:  srow.Phase,
		status: srow.Status,
		err:    srow.Error,
		// The previous process may already have sent the one reminder.
		sentReminder: srow.Status == stepStatusReminded,
	}
	if srow.Value.Valid {
		step.value = srow.Value.RawMessage
	}
	return step
}

// terminalWorkflowRun rebuilds a run that ended before the restart, so
// run_workflow and await_workflow return the same result without executing
// anything.
func terminalWorkflowRun(p *Server, chat database.Chat, row forkdb.WorkflowRun) (*workflowRun, error) {
	res := runWorkflowResult{
		RunID:        row.ID.String(),
		Status:       row.Status,
		Error:        row.Error,
		Phases:       append([]string{}, row.Phases...),
		CurrentPhase: row.CurrentPhase,
		Steps:        make([]stepSummary, 0),
	}
	if row.Result.Valid {
		if err := json.Unmarshal(row.Result.RawMessage, &res); err != nil {
			return nil, xerrors.Errorf("unmarshal the journal result of run %s: %w", row.ID, err)
		}
		// The journaled result of an older run carries no phase state; the
		// row does.
		if res.Phases == nil {
			res.Phases = append([]string{}, row.Phases...)
			res.CurrentPhase = row.CurrentPhase
		}
	}
	// A journal-only stop wrote no result snapshot, so the row's live lines
	// are the only history the run has.
	if !row.Result.Valid {
		res.Logs, _, _ = restoreWorkflowLogs(row.Logs)
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
	// The journal holds the live lines of an open run, so await_workflow
	// reports them the way a run this replica holds does.
	logs, _, _ := restoreWorkflowLogs(row.Logs)
	res = runWorkflowResult{
		RunID:        row.ID.String(),
		Status:       row.Status,
		Error:        row.Error,
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
			// A running row has a live owner, whose row carries the flag; a
			// resume of an interrupted run replays every step.
			Reused: srow.Reused || row.Status != workflowStatusRunning,
			Phase:  srow.Phase,
			Error:  srow.Error,
		})
	}
	return res, true, nil
}

func (j workflowJournal) warn(ctx context.Context, msg string, id uuid.UUID, err error) {
	j.p.logger.Warn(ctx, msg, slog.F("workflow_id", id), slog.Error(err))
}
