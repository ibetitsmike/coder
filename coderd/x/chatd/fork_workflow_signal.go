package chatd

// This file is fork code: how a waiting workflow step of the fork-workflows
// experiment learns of its report. workflow_report hands an accepted value
// to the live run of this process, which wakes the step at once. A report
// that another process accepts is journaled there and announced on the
// owner channel of the run (fork_workflow_owner.go), and the step reads its
// journal row. The step waits for the child chat's status only to send the
// reminder or fail the step, so the wait reads no chat history. The scan of
// a child's history (workflowStepReport) runs once per open step on a
// resume, for a report that reached the history but not the journal.

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	coderdpubsub "github.com/coder/coder/v2/coderd/pubsub"
)

// workflowOwnerEventReport tells the owner of a run that a step report
// landed in the journal.
const workflowOwnerEventReport = "report"

// stepSignals lives in a workflowStep and wakes its waitStep. reported is
// closed when the step gets its first report; poked receives when another
// process journaled a report. workflowRun.mu guards the fields.
type stepSignals struct {
	reported       chan struct{}
	reportedClosed bool
	poked          chan struct{}
}

// signalsLocked returns the wake channels of step, made on first use.
func (s *workflowStep) signalsLocked() (reported, poked <-chan struct{}) {
	if s.signals.reported == nil {
		s.signals.reported = make(chan struct{})
		s.signals.poked = make(chan struct{}, 1)
	}
	if s.status == stepStatusReported && !s.signals.reportedClosed {
		close(s.signals.reported)
		s.signals.reportedClosed = true
	}
	return s.signals.reported, s.signals.poked
}

// setReportedLocked records value as the step's report and wakes its wait.
func (s *workflowStep) setReportedLocked(value json.RawMessage) {
	s.status, s.value = stepStatusReported, value
	_, _ = s.signalsLocked()
}

// addStepLocked appends step to the run and applies a report that arrived
// before the step was in the run. It returns true when it applied one,
// which the caller journals after it unlocks.
func (r *workflowRun) addStepLocked(step *workflowStep) bool {
	r.steps = append(r.steps, step)
	value, ok := r.earlyReports[step.chatID]
	if !ok {
		return false
	}
	delete(r.earlyReports, step.chatID)
	if step.status == stepStatusFailed {
		return false
	}
	step.setReportedLocked(value)
	return true
}

// journalEarlyReport journals a report that addStepLocked applied.
func (r *workflowRun) journalEarlyReport(ctx context.Context, step *workflowStep) {
	if err := r.journalStepState(ctx, step, r.writeReport); err != nil {
		r.p.logger.Warn(ctx, "journal an early workflow step report",
			slog.F("workflow_id", r.id), slog.F("step_id", step.id), slog.Error(err))
	}
}

// journalStepState writes the state of step with write and writes again
// while a report changed the step during the write. Reports arrive on the
// child's goroutine, so the journal row ends on the state the run holds
// whatever order the writes land in.
func (r *workflowRun) journalStepState(
	ctx context.Context,
	step *workflowStep,
	write func(context.Context, *workflowStep) error,
) error {
	r.mu.Lock()
	snap := *step
	r.mu.Unlock()
	for {
		if err := write(ctx, &snap); err != nil {
			return err
		}
		r.mu.Lock()
		changed := step.status != snap.status || !bytes.Equal(step.value, snap.value)
		snap = *step
		r.mu.Unlock()
		if !changed {
			return nil
		}
	}
}

// acceptReport hands a valid report to a live run of this process. ok is
// false when the run ended or another process took it, and the caller
// answers through the journal. A report whose step the run has not added
// yet waits in earlyReports: the child can report before startStep returns.
// The first early report wins there, as it does in the journal.
func (r *workflowRun) acceptReport(ctx context.Context, chatID uuid.UUID, input string) (fantasy.ToolResponse, bool) {
	value := json.RawMessage(input)
	r.mu.Lock()
	if r.result != nil || r.lost {
		r.mu.Unlock()
		return fantasy.ToolResponse{}, false
	}
	var step *workflowStep
	for _, s := range r.steps {
		if s.chatID == chatID {
			step = s
			break
		}
	}
	if step == nil {
		if _, ok := r.earlyReports[chatID]; ok {
			r.mu.Unlock()
			return fantasy.NewTextResponse(reportKeptText), true
		}
		if r.earlyReports == nil {
			r.earlyReports = map[uuid.UUID]json.RawMessage{}
		}
		r.earlyReports[chatID] = value
		r.mu.Unlock()
		return fantasy.NewTextResponse(reportAcceptedText), true
	}
	if step.status == stepStatusFailed {
		reason := step.err
		r.mu.Unlock()
		return stepFailedResponse(reason), true
	}
	if step.status == stepStatusReported {
		// The wait woke on the first report, so the script may hold that
		// value already. The write repeats the first value, for a report
		// from the history or an early report whose journal write failed.
		r.mu.Unlock()
		if err := r.journalStepState(ctx, step, r.writeReport); err != nil {
			return reportNotRecordedResponse(err), true
		}
		return fantasy.NewTextResponse(reportKeptText), true
	}
	snap := *step
	r.mu.Unlock()
	// The journal decides which report is first: another call, on this
	// process or another one, may write the row between the read above and
	// this write. Only a report that landed wakes the wait, so the script
	// and the journal hold the same value.
	snap.status, snap.value = stepStatusReported, value
	landed, err := r.p.workflowJournal().reportStep(ctx, r.id, &snap)
	if err != nil {
		return reportNotRecordedResponse(err), true
	}
	if !landed {
		r.readJournaledReport(ctx, step)
		return fantasy.NewTextResponse(reportKeptText), true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	switch step.status {
	case stepStatusFailed:
		return stepFailedResponse(step.err), true
	case stepStatusReported:
		return fantasy.NewTextResponse(reportKeptText), true
	}
	step.setReportedLocked(value)
	return fantasy.NewTextResponse(reportAcceptedText), true
}

func stepFailedResponse(reason string) fantasy.ToolResponse {
	return fantasy.NewTextErrorResponse("this step already failed (" + reason +
		"); the workflow no longer waits for it. Stop working.")
}

func reportNotRecordedResponse(err error) fantasy.ToolResponse {
	return fantasy.NewTextErrorResponse("the report could not be recorded: " + err.Error() +
		". Call workflow_report again.")
}

// pokeStep wakes the wait of the step whose report another process
// journaled. The pubsub callback calls it, so it does not block.
func (r *workflowRun) pokeStep(stepID uuid.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.steps {
		if s.id != stepID {
			continue
		}
		_, _ = s.signalsLocked()
		select {
		case s.signals.poked <- struct{}{}:
		default:
		}
		return
	}
}

// publishStepReport tells the live owner of a run that the report of step
// landed in the journal. It is best effort: the owner also reads the step
// row when the child stops.
func (p *Server) publishStepReport(ctx context.Context, owner, runID, stepID uuid.UUID) {
	msg, err := json.Marshal(workflowOwnerEvent{Kind: workflowOwnerEventReport, RunID: runID, StepID: stepID})
	if err == nil {
		err = p.pubsub.Publish(workflowOwnerChannel(owner), msg)
	}
	if err != nil {
		p.logger.Warn(ctx, "announce a workflow step report to the run owner",
			slog.F("workflow_id", runID), slog.F("step_id", stepID), slog.Error(err))
	}
}

// readJournaledReport takes the report of step from its journal row: a
// report that another process accepted. It returns true when the step has
// a value now.
func (r *workflowRun) readJournaledReport(ctx context.Context, step *workflowStep) bool {
	row, err := r.p.workflowJournal().getStep(ctx, step.id)
	if err != nil || row.Status != stepStatusReported || !row.Value.Valid {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if step.status == stepStatusFailed {
		return false
	}
	if step.status != stepStatusReported {
		step.setReportedLocked(row.Value.RawMessage)
	}
	return true
}

// scanJournaledStepHistory reads the history of the child chat of an open
// step that a resume found in the journal, once. A report that reached the
// history but not the journal, from a process that crashed before it
// journaled or from a version that journaled reports only at the end of
// the child's turn, becomes the step's value. A reminder in the history
// counts as sent. A failed read leaves historyPending set, and waitStep
// reads again before it treats the child as unreported.
func (r *workflowRun) scanJournaledStepHistory(ctx context.Context, step *workflowStep) error {
	r.mu.Lock()
	open := step.status == stepStatusRunning || step.status == stepStatusReminded
	step.historyPending = open
	r.mu.Unlock()
	if !open {
		return nil
	}
	value, reminded, err := workflowStepReport(ctx, r.p.db, step.chatID)
	if err != nil {
		r.p.logger.Warn(ctx, "scan the history of a resumed workflow step",
			slog.F("workflow_id", r.id), slog.F("step_id", step.id), slog.Error(err))
		return err
	}
	r.mu.Lock()
	step.historyPending = false
	if reminded {
		step.sentReminder = true
	}
	if value == nil || step.status == stepStatusFailed || step.status == stepStatusReported {
		r.mu.Unlock()
		return nil
	}
	step.setReportedLocked(value)
	r.mu.Unlock()
	if err := r.journalStepState(ctx, step, r.writeReport); err != nil {
		r.p.logger.Warn(ctx, "journal a workflow step report from the history",
			slog.F("workflow_id", r.id), slog.F("step_id", step.id), slog.Error(err))
	}
	return nil
}

func (r *workflowRun) stepHistoryPending(step *workflowStep) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return step.historyPending
}

// retryStepHistory reads the history that the resume of step could not
// read, before the wait treats the child as unreported. A failed read waits
// one poll interval, bounded by deadline, before the caller loops.
func (r *workflowRun) retryStepHistory(ctx context.Context, step *workflowStep, deadline time.Time) error {
	if r.scanJournaledStepHistory(ctx, step) == nil {
		return nil
	}
	wait := min(subagentAwaitPollInterval, deadline.Sub(r.p.clock.Now()))
	if wait <= 0 {
		return nil
	}
	timer := r.p.clock.NewTimer(wait, "chatd", "workflow_step_history_retry")
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// awaitStepChild waits until the step has a report, the child chat stops
// its turn, or deadline passes. stopped is true when the child stopped
// without a report this wait saw. It subscribes to the child's state
// before it reads the child, so a child that stops in between wakes the
// wait. A child in the error status returns a *subagentStatusError, and
// deadline returns ErrSubagentWaitTimeout.
func (r *workflowRun) awaitStepChild(ctx context.Context, step *workflowStep, deadline time.Time) (stopped bool, err error) {
	r.mu.Lock()
	reported, poked := step.signalsLocked()
	r.mu.Unlock()
	changed := make(chan struct{}, 1)
	pollInterval := subagentAwaitFallbackPoll
	unsubscribe, err := r.p.pubsub.SubscribeWithErr(coderdpubsub.ChatStateUpdateChannel(step.chatID),
		func(context.Context, []byte, error) {
			select {
			case changed <- struct{}{}:
			default:
			}
		})
	if err != nil {
		// Without the state channel the wait polls fast.
		pollInterval = subagentAwaitPollInterval
	} else {
		defer unsubscribe()
	}
	timeout := deadline.Sub(r.p.clock.Now())
	if timeout <= 0 {
		return false, ErrSubagentWaitTimeout
	}
	timer := r.p.clock.NewTimer(timeout, "chatd", "workflow_step_wait")
	defer timer.Stop()
	ticker := r.p.clock.NewTicker(pollInterval, "chatd", "workflow_step_poll")
	defer ticker.Stop()
	for {
		// A report that came in while the last read ran wins over a stop.
		select {
		case <-reported:
			return false, nil
		default:
		}
		stopped, err := r.stepChildStopped(ctx, step.chatID)
		if err != nil || stopped {
			return stopped, err
		}
		select {
		case <-reported:
			return false, nil
		case <-poked:
			if r.readJournaledReport(ctx, step) {
				return false, nil
			}
		case <-changed:
		case <-ticker.C:
		case <-timer.C:
			return false, ErrSubagentWaitTimeout
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
}

// stepChildStopped reads the status of a step's child chat. interrupting
// and paused are not stopped: the child settles or resumes first. A child
// in the error status fails the step; only that path reads the child's
// last message, for the reason.
func (r *workflowRun) stepChildStopped(ctx context.Context, chatID uuid.UUID) (bool, error) {
	chat, err := r.p.db.GetChatByID(ctx, chatID)
	if err != nil {
		return false, xerrors.Errorf("get chat: %w", err)
	}
	switch chat.Status {
	case database.ChatStatusRunning, database.ChatStatusInterrupting, database.ChatStatusPaused:
		return false, nil
	case database.ChatStatusError:
		report, err := latestSubagentAssistantMessage(ctx, r.p.db, chatID)
		if err != nil {
			return false, err
		}
		_, _, err = handleSubagentDone(chat, report)
		return false, err
	default:
		return true, nil
	}
}
