package chatd

// This file is fork code: it delivers the due chat wake-ups of the
// fork-chat-wakeups experiment. startForkLoops starts its loop in every chat
// worker, so every replica claims and delivers wake-ups.

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/agenthooks/dispatch"
	"github.com/coder/coder/v2/coderd/x/chatd/chathooks"
	"github.com/coder/coder/v2/coderd/x/chatd/chatstate"
	"github.com/coder/coder/v2/codersdk"
)

const (
	wakeupPollInterval  = 15 * time.Second
	wakeupLease         = 2 * time.Minute
	wakeupClaimBatch    = 20
	wakeupEventsBackoff = time.Minute
)

// startForkLoops starts the worker loops of fork experiments.
func (w *chatWorker) startForkLoops(ctx context.Context) {
	if !w.server.experiments.Enabled(codersdk.ExperimentForkChatWakeups) {
		return
	}
	w.wg.Go(func() {
		w.wakeupLoop(ctx)
	})
}

func (w *chatWorker) wakeupLoop(ctx context.Context) {
	run := func(now time.Time) {
		w.wakeupOnce(ctx, dbtime.Time(now).UTC())
	}
	run(w.opts.Clock.Now("chatworker", "wakeups"))

	ticker := w.opts.Clock.NewTicker(wakeupPollInterval, "chatworker", "wakeups")
	defer ticker.Stop("chatworker", "wakeups")
	for {
		select {
		case tick := <-ticker.C:
			ticker.Stop("chatworker", "wakeups")
			run(tick)
			ticker.Reset(wakeupPollInterval, "chatworker", "wakeups")
		case <-ctx.Done():
			return
		}
	}
}

// wakeupOnce claims the due wake-ups and delivers them. A claim holds a row
// until its lease expires, and the claim query skips locked rows, so two
// replicas never deliver the same wake-up.
func (w *chatWorker) wakeupOnce(ctx context.Context, now time.Time) {
	//nolint:gocritic // The wake-up worker delivers the wake-ups of every chat.
	ctx = dbauthz.AsChatd(ctx)
	fdb, err := forkdb.From(w.opts.Store)
	if err != nil {
		w.opts.Logger.Warn(ctx, "chatworker wake-up fork queries unavailable", slogError(err))
		return
	}
	rows, err := fdb.ClaimDueChatWakeups(ctx, forkdb.ClaimDueChatWakeupsParams{
		Now:            now,
		LeaseExpiresAt: now.Add(wakeupLease),
		LimitCount:     wakeupClaimBatch,
	})
	if err != nil {
		if ctx.Err() == nil {
			w.opts.Logger.Warn(ctx, "chatworker wake-up claim failed", slogError(err))
		}
		return
	}
	for _, row := range rows {
		if err := w.deliverWakeupSafely(ctx, fdb, row, now); err != nil {
			if ctx.Err() != nil {
				return
			}
			w.opts.Logger.Warn(ctx, "chatworker wake-up delivery failed",
				slog.F("wakeup_id", row.ID),
				slog.F("chat_id", row.ChatID),
				slog.Error(err),
			)
		}
	}
}

func (w *chatWorker) deliverWakeupSafely(ctx context.Context, fdb forkdb.Store, wakeup forkdb.ChatWakeup, now time.Time) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = xerrors.Errorf("chatworker wake-up panic: %v", recovered)
		}
	}()
	return w.server.deliverWakeup(ctx, fdb, wakeup, now)
}

// wakeupFinish is how a delivery attempt ends the claim of a wake-up.
type wakeupFinish struct {
	status    forkdb.ChatWakeupStatus
	reason    string
	nextRunAt sql.NullTime
	runDelta  int32
}

// deliverWakeup sends the prompt of a claimed wake-up to its chat as the chat
// owner and ends the claim. A busy chat gets nothing: the wake-up stays due
// and the next tick retries it.
func (p *Server) deliverWakeup(ctx context.Context, fdb forkdb.Store, wakeup forkdb.ChatWakeup, now time.Time) error {
	chat, err := p.db.GetChatByID(ctx, wakeup.ChatID)
	if err != nil {
		return xerrors.Errorf("load chat: %w", err)
	}

	switch {
	case chat.Archived:
		return p.finishWakeupClaim(ctx, fdb, wakeup, now, wakeupFinish{status: forkdb.ChatWakeupStatusCanceled, reason: "archived"})
	case wakeup.ExpiresAt.Valid && !wakeup.ExpiresAt.Time.After(now):
		return p.finishWakeupClaim(ctx, fdb, wakeup, now, wakeupFinish{status: forkdb.ChatWakeupStatusCompleted, reason: "expired"})
	case wakeup.Kind == forkdb.ChatWakeupKindEvents:
		// TODO(ibetitsmike): deliver events wake-ups (W5 PR4).
		return p.finishWakeupClaim(ctx, fdb, wakeup, now, wakeupFinish{
			status:    forkdb.ChatWakeupStatusActive,
			nextRunAt: sql.NullTime{Time: now.Add(wakeupEventsBackoff), Valid: true},
		})
	case chat.Status == database.ChatStatusRunning,
		chat.Status == database.ChatStatusInterrupting,
		chat.Status == database.ChatStatusRequiresAction:
		p.logger.Debug(ctx, "chat wake-up waits for a busy chat",
			slog.F("wakeup_id", wakeup.ID),
			slog.F("chat_id", chat.ID),
			slog.F("chat_status", chat.Status),
		)
		return p.finishWakeupClaim(ctx, fdb, wakeup, now, wakeupFinish{
			status:    wakeup.Status,
			reason:    wakeup.StatusReason.String,
			nextRunAt: wakeup.NextRunAt,
		})
	}

	// The run key names the occurrence, so a replica that took over an
	// expired lease never delivers an occurrence twice.
	run, err := fdb.InsertChatWakeupRun(ctx, forkdb.InsertChatWakeupRunParams{
		ID:        uuid.New(),
		WakeupID:  wakeup.ID,
		RunKey:    wakeup.NextRunAt.Time.UTC().Format(time.RFC3339Nano),
		Status:    forkdb.ChatWakeupRunStatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return p.finishWakeupClaim(ctx, fdb, wakeup, now, nextWakeupRun(wakeup, now, 0))
	}
	if err != nil {
		return xerrors.Errorf("insert wake-up run: %w", err)
	}

	sendErr := p.sendWakeupPrompt(ctx, chat, wakeup.Prompt)
	runStatus, finish := p.wakeupOutcome(ctx, wakeup, now, sendErr)
	var runError sql.NullString
	if sendErr != nil {
		runError = sql.NullString{String: sendErr.Error(), Valid: true}
	}
	if _, err := fdb.UpdateChatWakeupRunStatus(ctx, forkdb.UpdateChatWakeupRunStatusParams{
		ID:        run.ID,
		Status:    runStatus,
		Error:     runError,
		UpdatedAt: now,
	}); err != nil {
		// Still end the claim: the prompt may already be in the chat.
		p.logger.Warn(ctx, "chat wake-up run status update failed",
			slog.F("wakeup_id", wakeup.ID),
			slog.F("run_id", run.ID),
			slog.Error(err),
		)
	}
	return p.finishWakeupClaim(ctx, fdb, wakeup, now, finish)
}

func (p *Server) sendWakeupPrompt(ctx context.Context, chat database.Chat, prompt string) error {
	owner, err := p.chatOwnerSubject(ctx, chat.OwnerID)
	if err != nil {
		return err
	}
	//nolint:gocritic // A wake-up prompt is sent as the chat owner.
	ownerCtx := dbauthz.As(ctx, owner)
	// The chat was idle when it was loaded. Queue only covers a chat that
	// turned busy since then; a wake-up never steers or interrupts.
	_, err = p.SendMessage(ownerCtx, SendMessageOptions{
		ChatID:       chat.ID,
		CreatedBy:    chat.OwnerID,
		Content:      []codersdk.ChatMessagePart{codersdk.ChatMessageText(prompt)},
		BusyBehavior: SendMessageBusyBehaviorQueue,
	})
	return err
}

// wakeupOutcome maps the result of a send to the status of the run and to how
// the claim ends.
func (p *Server) wakeupOutcome(ctx context.Context, wakeup forkdb.ChatWakeup, now time.Time, sendErr error) (forkdb.ChatWakeupRunStatus, wakeupFinish) {
	_, denied := errors.AsType[*chathooks.UserPromptDeniedError](sendErr)
	_, dispatchFailed := errors.AsType[*dispatch.Error](sendErr)
	switch {
	case sendErr == nil:
		return forkdb.ChatWakeupRunStatusDelivered, nextWakeupRun(wakeup, now, 1)
	case denied || dispatchFailed:
		// TODO(ibetitsmike): notify the owner of the paused wake-up (W5 PR5).
		return forkdb.ChatWakeupRunStatusFailed, wakeupFinish{status: forkdb.ChatWakeupStatusPaused, reason: "hook_denied"}
	case errors.Is(sendErr, chatstate.ErrMessageQueueFull):
		return forkdb.ChatWakeupRunStatusSkipped, nextWakeupRun(wakeup, now, 0)
	case errors.Is(sendErr, ErrChatArchived), errors.Is(sendErr, chatstate.ErrChatFamilyArchived):
		return forkdb.ChatWakeupRunStatusSkipped, wakeupFinish{status: forkdb.ChatWakeupStatusCanceled, reason: "archived"}
	default:
		p.logger.Warn(ctx, "chat wake-up send failed",
			slog.F("wakeup_id", wakeup.ID),
			slog.F("chat_id", wakeup.ChatID),
			slog.Error(sendErr),
		)
		return forkdb.ChatWakeupRunStatusFailed, nextWakeupRun(wakeup, now, 0)
	}
}

// nextWakeupRun computes how the claim of wakeup ends after an occurrence that
// adds runDelta runs. Missed interval and cron slots are never replayed: the
// occurrence counts once and the schedule continues from now.
func nextWakeupRun(wakeup forkdb.ChatWakeup, now time.Time, runDelta int32) wakeupFinish {
	finish := wakeupFinish{runDelta: runDelta}
	var next time.Time
	switch wakeup.Kind {
	case forkdb.ChatWakeupKindInterval:
		interval := time.Duration(wakeup.IntervalSeconds.Int32) * time.Second
		next = wakeup.NextRunAt.Time.Add(interval)
		if !next.After(now) {
			next = now.Add(interval)
		}
	case forkdb.ChatWakeupKindCron:
		schedule, err := cron.ParseStandard("CRON_TZ=" + wakeup.Timezone + " " + wakeup.Cron.String)
		if !wakeup.Cron.Valid || err != nil {
			finish.status, finish.reason = forkdb.ChatWakeupStatusPaused, "invalid_cron"
			return finish
		}
		next = schedule.Next(now)
	default:
		finish.status, finish.reason = forkdb.ChatWakeupStatusCompleted, "done"
		return finish
	}
	switch {
	case wakeup.MaxRuns.Valid && wakeup.RunCount+runDelta >= wakeup.MaxRuns.Int32:
		finish.status, finish.reason = forkdb.ChatWakeupStatusCompleted, "max_runs"
	case wakeup.ExpiresAt.Valid && !next.Before(wakeup.ExpiresAt.Time):
		finish.status, finish.reason = forkdb.ChatWakeupStatusCompleted, "expired"
	default:
		finish.status = forkdb.ChatWakeupStatusActive
		finish.nextRunAt = sql.NullTime{Time: next, Valid: true}
	}
	return finish
}

// finishWakeupClaim ends the claim of wakeup. It does nothing when the lease
// expired and another replica claimed the wake-up again.
func (p *Server) finishWakeupClaim(ctx context.Context, fdb forkdb.Store, wakeup forkdb.ChatWakeup, now time.Time, finish wakeupFinish) error {
	params := forkdb.FinishChatWakeupClaimParams{
		ID:             wakeup.ID,
		LeaseExpiresAt: wakeup.LeaseExpiresAt.Time,
		Status:         finish.status,
		StatusReason:   sql.NullString{String: finish.reason, Valid: finish.reason != ""},
		NextRunAt:      finish.nextRunAt,
		RunCountDelta:  finish.runDelta,
		UpdatedAt:      now,
	}
	if finish.runDelta > 0 {
		params.LastRunAt = sql.NullTime{Time: now, Valid: true}
	}
	_, err := fdb.FinishChatWakeupClaim(ctx, params)
	if errors.Is(err, sql.ErrNoRows) {
		p.logger.Debug(ctx, "chat wake-up claim was taken over",
			slog.F("wakeup_id", wakeup.ID),
		)
		return nil
	}
	if err != nil {
		return xerrors.Errorf("finish wake-up claim: %w", err)
	}
	return nil
}
