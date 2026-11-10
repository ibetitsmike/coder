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
	start := w.opts.Clock.Now("chatworker", "wakeups")
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
		// Rows are delivered one by one, so each uses its own time, which
		// includes the time the claim took.
		rowNow := now.Add(w.opts.Clock.Since(start, "chatworker", "wakeups"))
		if !rowNow.Before(row.LeaseExpiresAt.Time) {
			// Another replica may hold the row now; it is due again anyway.
			continue
		}
		if err := w.deliverWakeupSafely(ctx, fdb, row, rowNow); err != nil {
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
		return p.finishWakeupClaim(ctx, fdb, wakeup, now, wakeupStaysDue(wakeup))
	}

	err = p.sendWakeupPrompt(ctx, chat, wakeup, now)
	switch {
	case err == nil, errors.Is(err, errWakeupClaimLost):
		return nil
	case errors.Is(err, errWakeupChatBusy), errors.Is(err, chatstate.ErrMessageQueueFull):
		// The chat turned busy after the status check.
		return p.finishWakeupClaim(ctx, fdb, wakeup, now, wakeupStaysDue(wakeup))
	case errors.Is(err, errWakeupOccurrenceRan):
		return p.finishWakeupClaim(ctx, fdb, wakeup, now, nextWakeupRun(wakeup, now, 0))
	}

	runStatus, finish := p.wakeupFailure(ctx, wakeup, now, err)
	// End the claim first: a worker whose lease ran out during the send must
	// not record a run for an occurrence that another worker now owns.
	_, finishErr := fdb.FinishChatWakeupClaim(ctx, finishWakeupClaimParams(wakeup, now, finish))
	if errors.Is(finishErr, sql.ErrNoRows) {
		return nil
	}
	if finishErr != nil {
		return xerrors.Errorf("finish wake-up claim: %w", finishErr)
	}
	_, insertErr := fdb.InsertChatWakeupRun(ctx, forkdb.InsertChatWakeupRunParams{
		ID:        uuid.New(),
		WakeupID:  wakeup.ID,
		RunKey:    wakeupRunKey(wakeup),
		Status:    runStatus,
		Error:     sql.NullString{String: err.Error(), Valid: true},
		CreatedAt: now,
		UpdatedAt: now,
	})
	if insertErr != nil && !errors.Is(insertErr, sql.ErrNoRows) {
		p.logger.Warn(ctx, "chat wake-up run record failed",
			slog.F("wakeup_id", wakeup.ID),
			slog.Error(insertErr),
		)
	}
	return nil
}

var (
	errWakeupChatBusy      = xerrors.New("chat wake-up: chat is busy")
	errWakeupOccurrenceRan = xerrors.New("chat wake-up: occurrence already ran")
	errWakeupClaimLost     = xerrors.New("chat wake-up: claim lost")
)

// wakeupStaysDue ends the claim without changing the schedule, so the next
// tick retries the same occurrence.
func wakeupStaysDue(wakeup forkdb.ChatWakeup) wakeupFinish {
	return wakeupFinish{status: wakeup.Status, reason: wakeup.StatusReason.String, nextRunAt: wakeup.NextRunAt}
}

// wakeupRunKey names the occurrence, so no occurrence is delivered twice.
func wakeupRunKey(wakeup forkdb.ChatWakeup) string {
	return wakeup.NextRunAt.Time.UTC().Format(time.RFC3339Nano)
}

// sendWakeupPrompt sends the prompt as the chat owner. The delivered run and
// the end of the claim commit in the send's transaction, under the chat lock:
// a chat that turned busy, an occurrence that already ran, or a claim that was
// canceled or taken over rolls the message back.
func (p *Server) sendWakeupPrompt(ctx context.Context, chat database.Chat, wakeup forkdb.ChatWakeup, now time.Time) error {
	owner, err := p.chatOwnerSubject(ctx, chat.OwnerID)
	if err != nil {
		return err
	}
	//nolint:gocritic // A wake-up prompt is sent as the chat owner.
	ownerCtx := dbauthz.As(ctx, owner)
	_, err = p.SendMessage(ownerCtx, SendMessageOptions{
		ChatID:    chat.ID,
		CreatedBy: chat.OwnerID,
		Content:   []codersdk.ChatMessagePart{codersdk.ChatMessageText(wakeup.Prompt)},
		// Queue admits the message on an idle chat; forkAdmit rejects a
		// send that would queue.
		BusyBehavior: SendMessageBusyBehaviorQueue,
		forkAdmit: func(ctx context.Context, store database.Store, sent chatstate.SendMessageResult) error {
			if sent.QueuedMessage != nil {
				return errWakeupChatBusy
			}
			txdb, err := forkdb.From(store)
			if err != nil {
				return err
			}
			//nolint:gocritic // The wake-up worker records its own bookkeeping.
			ctx = dbauthz.AsChatd(ctx)
			_, err = txdb.InsertChatWakeupRun(ctx, forkdb.InsertChatWakeupRunParams{
				ID:        uuid.New(),
				WakeupID:  wakeup.ID,
				RunKey:    wakeupRunKey(wakeup),
				Status:    forkdb.ChatWakeupRunStatusDelivered,
				CreatedAt: now,
				UpdatedAt: now,
			})
			if errors.Is(err, sql.ErrNoRows) {
				return errWakeupOccurrenceRan
			}
			if err != nil {
				return xerrors.Errorf("insert wake-up run: %w", err)
			}
			_, err = txdb.FinishChatWakeupClaim(ctx, finishWakeupClaimParams(wakeup, now, nextWakeupRun(wakeup, now, 1)))
			if errors.Is(err, sql.ErrNoRows) {
				return errWakeupClaimLost
			}
			if err != nil {
				return xerrors.Errorf("finish wake-up claim: %w", err)
			}
			return nil
		},
	})
	return err
}

// wakeupFailure maps a failed send to the status of its run and to how the
// claim ends.
func (p *Server) wakeupFailure(ctx context.Context, wakeup forkdb.ChatWakeup, now time.Time, sendErr error) (forkdb.ChatWakeupRunStatus, wakeupFinish) {
	if _, denied := errors.AsType[*chathooks.UserPromptDeniedError](sendErr); denied {
		// TODO(ibetitsmike): notify the owner of the paused wake-up (W5 PR5).
		return forkdb.ChatWakeupRunStatusFailed, wakeupFinish{status: forkdb.ChatWakeupStatusPaused, reason: "hook_denied"}
	}
	switch {
	case errors.Is(sendErr, ErrChatArchived), errors.Is(sendErr, chatstate.ErrChatFamilyArchived):
		return forkdb.ChatWakeupRunStatusSkipped, wakeupFinish{status: forkdb.ChatWakeupStatusCanceled, reason: "archived"}
	default:
		// Includes hook dispatch failures, which are transient: the next
		// occurrence tries again.
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
	_, err := fdb.FinishChatWakeupClaim(ctx, finishWakeupClaimParams(wakeup, now, finish))
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

// finishWakeupClaimParams ends the claim only for its current lease holder.
func finishWakeupClaimParams(wakeup forkdb.ChatWakeup, now time.Time, finish wakeupFinish) forkdb.FinishChatWakeupClaimParams {
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
	return params
}
