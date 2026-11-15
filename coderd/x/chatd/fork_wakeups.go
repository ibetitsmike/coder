package chatd

// This file is fork code: it delivers the due chat wake-ups of the
// fork-chat-wakeups experiment. startForkLoops starts the waker of the Server
// in its chat worker, so every replica delivers wake-ups.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chathooks"
	"github.com/coder/coder/v2/coderd/x/chatd/chatstate"
	"github.com/coder/coder/v2/codersdk"
)

const (
	wakeupPollInterval = 15 * time.Second

	wakeupReasonArchived = "archived"
)

// startForkLoops starts the worker loops of fork experiments.
func (w *chatWorker) startForkLoops(ctx context.Context) {
	if !w.server.experiments.Enabled(codersdk.ExperimentForkChatWakeups) {
		return
	}
	waker := w.server.forkWaker()
	if waker == nil {
		// A Server built without New has no pokes, only the timer and the
		// sweep.
		waker = newForkWaker()
	}
	w.wg.Go(func() {
		waker.run(ctx, w.server, w.opts.Store, w.opts.Clock, w.opts.Logger)
	})
}

// wakeupFinish is how a delivery attempt ends an occurrence of a wake-up.
type wakeupFinish struct {
	status    forkdb.ChatWakeupStatus
	reason    string
	nextRunAt sql.NullTime
	runDelta  int32
	// pauseText, when set, explains reason with the caps that paused the
	// wake-up.
	pauseText string
}

// deliverWakeup sends the prompt of a due wake-up to its chat as the chat
// owner and records the occurrence. A busy chat gets nothing and nothing is
// written: the wake-up stays due, and the commit that ends the turn pokes the
// waker.
func (p *Server) deliverWakeup(ctx context.Context, fdb forkdb.Store, wakeup forkdb.ChatWakeup, now time.Time) error {
	chat, err := p.db.GetChatByID(ctx, wakeup.ChatID)
	if err != nil {
		return xerrors.Errorf("load chat: %w", err)
	}

	switch {
	case chat.Archived:
		return p.finishWakeup(ctx, chat, wakeup, now, wakeupFinish{status: forkdb.ChatWakeupStatusCanceled, reason: wakeupReasonArchived})
	case wakeup.ExpiresAt.Valid && !wakeup.ExpiresAt.Time.After(now) && !eventsFinalPoll(wakeup, now):
		return p.finishWakeup(ctx, chat, wakeup, now, wakeupFinish{status: forkdb.ChatWakeupStatusCompleted, reason: "expired"})
	case chat.Status == database.ChatStatusRunning,
		chat.Status == database.ChatStatusInterrupting,
		chat.Status == database.ChatStatusRequiresAction:
		p.logger.Debug(ctx, "chat wake-up waits for a busy chat",
			slog.F("wakeup_id", wakeup.ID),
			slog.F("chat_id", chat.ID),
			slog.F("chat_status", chat.Status),
		)
		return nil
	case wakeup.Kind == forkdb.ChatWakeupKindEvents:
		return p.deliverEventsWakeup(ctx, fdb, chat, wakeup, now)
	}
	if paused, err := p.pauseCappedWakeup(ctx, fdb, chat, wakeup, now); paused || err != nil {
		return err
	}

	delivery := wakeupDelivery{
		text:   wakeup.Prompt,
		runKey: wakeupRunKey(wakeup),
		finish: func(admittedAt time.Time) wakeupFinish { return nextWakeupRun(wakeup, admittedAt, 1) },
	}
	err = p.sendWakeupPrompt(ctx, chat, wakeup, now, delivery)
	return p.finishWakeupSend(ctx, chat, wakeup, now, delivery, err)
}

// finishWakeupSend ends the occurrence of wakeup after sendWakeupPrompt returned
// err for delivery, and records a failed send as a run. The failed sends of a
// watch share one run, which only the first one records, and leave the run
// key of the delivery free for a retry.
func (p *Server) finishWakeupSend(ctx context.Context, chat database.Chat, wakeup forkdb.ChatWakeup, now time.Time, delivery wakeupDelivery, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, errWakeupNotDue):
		return err
	case errors.Is(err, errWakeupChatBusy), errors.Is(err, chatstate.ErrMessageQueueFull):
		// The chat turned busy after the status check. The wake-up stays
		// due for the end of the turn.
		return nil
	case errors.Is(err, errWakeupOccurrenceRan):
		return p.finishWakeup(ctx, chat, wakeup, now, nextWakeupRun(wakeup, now, 0))
	}

	sendErr := err
	runStatus, finish := p.wakeupFailure(ctx, wakeup, now, sendErr)
	runKey := delivery.runKey
	if delivery.watch != nil {
		runKey = failedRunKey(runKey)
	}
	err = p.inWakeupTx(ctx, wakeup, func(ftx forkdb.Store) error {
		// A wake-up that changed since it was listed belongs to whoever
		// changed it, so inWakeupTx records nothing for it.
		if _, err := ftx.UpdateChatWakeupRun(ctx, wakeupRunParams(wakeup, now, finish)); err != nil {
			return err
		}
		// A failed message that would end the watch keeps the final poll
		// after the expiry from leaving its event out.
		if delivery.watch != nil && delivery.done {
			if err := ftx.MarkChatWakeupWatchFailed(ctx, forkdb.MarkChatWakeupWatchFailedParams{
				ID:       delivery.watch.ID,
				WakeupID: wakeup.ID,
				Now:      now,
			}); err != nil {
				return err
			}
		}
		_, err := ftx.InsertChatWakeupRun(ctx, forkdb.InsertChatWakeupRunParams{
			ID:        uuid.New(),
			WakeupID:  wakeup.ID,
			RunKey:    runKey,
			Status:    runStatus,
			Error:     sql.NullString{String: sendErr.Error(), Valid: true},
			CreatedAt: now,
			UpdatedAt: now,
		})
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	if errors.Is(err, errWakeupNotDue) {
		return err
	}
	if err != nil {
		return xerrors.Errorf("record failed wake-up run: %w", err)
	}
	if finish.status == forkdb.ChatWakeupStatusPaused {
		p.notifyWakeupPaused(ctx, chat, finish)
	}
	return nil
}

var (
	errWakeupChatBusy      = xerrors.New("chat wake-up: chat is busy")
	errWakeupOccurrenceRan = xerrors.New("chat wake-up: occurrence already ran")
)

// wakeupRunKey names the occurrence, so no occurrence is delivered twice.
func wakeupRunKey(wakeup forkdb.ChatWakeup) string {
	return wakeup.NextRunAt.Time.UTC().Format(time.RFC3339Nano)
}

// wakeupDelivery is one occurrence of a wake-up: the message text, the run
// key that keeps it from being delivered twice, and how a delivery admitted
// at a given time ends the occurrence. A delivery of an events wake-up also sets
// watch, the watch it delivers for, done when the message ends the watch,
// and specPatch, which merges into the spec of the watch. Its run only
// counts against the caps: the watch row keeps it from being delivered
// twice.
type wakeupDelivery struct {
	text      string
	runKey    string
	finish    func(admittedAt time.Time) wakeupFinish
	watch     *forkdb.ChatWakeupWatch
	done      bool
	specPatch json.RawMessage
}

// sendWakeupPrompt sends the text of delivery as the chat owner. The delivered
// run, the delivered watch and the next run time commit in the send's
// transaction, under the chat lock: a chat that turned busy, an occurrence
// that already ran, or a wake-up that changed since it was listed rolls the
// message back.
func (p *Server) sendWakeupPrompt(ctx context.Context, chat database.Chat, wakeup forkdb.ChatWakeup, now time.Time, delivery wakeupDelivery) error {
	owner, err := p.chatOwnerSubject(ctx, chat.OwnerID)
	if err != nil {
		return err
	}
	//nolint:gocritic // A wake-up prompt is sent as the chat owner.
	ownerCtx := dbauthz.As(ctx, owner)
	// A wake-up resumes a finished child on its own, like message_agent, so
	// it counts against the family live subagent limit.
	ownerCtx = withSubagentResume(ownerCtx)
	sendStart := p.clock.Now("chatd", "wakeups")
	// The finish of the admitted attempt, which commits with the message.
	var finish wakeupFinish
	_, err = p.SendMessage(ownerCtx, SendMessageOptions{
		ChatID:    chat.ID,
		CreatedBy: chat.OwnerID,
		Content:   []codersdk.ChatMessagePart{codersdk.ChatMessageText(delivery.text)},
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
			// Hooks and the chat lock can delay admission past the next
			// slot, so the schedule continues from the admission time.
			admittedAt := now.Add(p.clock.Since(sendStart, "chatd", "wakeups"))
			//nolint:gocritic // The wake-up worker records its own bookkeeping.
			ctx = dbauthz.AsChatd(ctx)
			finish, err = admitWakeupDelivery(ctx, txdb, wakeup, delivery, admittedAt)
			return err
		},
	})
	if err == nil && finish.status == forkdb.ChatWakeupStatusPaused {
		p.notifyWakeupPaused(ctx, chat, finish)
	}
	return err
}

// admitWakeupDelivery records delivery, admitted at admittedAt, in the
// transaction of its message, which holds the chat lock, and returns how the
// occurrence ended. It locks the wake-up row first, so the run and the watch
// are recorded under that lock, and only while the wake-up is still the one
// that was listed as due.
func admitWakeupDelivery(ctx context.Context, txdb forkdb.Store, wakeup forkdb.ChatWakeup, delivery wakeupDelivery, admittedAt time.Time) (wakeupFinish, error) {
	if err := lockDueWakeup(ctx, txdb, wakeup); err != nil {
		return wakeupFinish{}, err
	}
	insertRun := func() error {
		_, err := txdb.InsertChatWakeupRun(ctx, forkdb.InsertChatWakeupRunParams{
			ID:        uuid.New(),
			WakeupID:  wakeup.ID,
			RunKey:    delivery.runKey,
			Status:    forkdb.ChatWakeupRunStatusDelivered,
			CreatedAt: admittedAt,
			UpdatedAt: admittedAt,
		})
		if errors.Is(err, sql.ErrNoRows) {
			return errWakeupOccurrenceRan
		}
		if err != nil {
			return xerrors.Errorf("insert wake-up run: %w", err)
		}
		return nil
	}
	if delivery.watch == nil {
		if err := insertRun(); err != nil {
			return wakeupFinish{}, err
		}
	}
	finish := delivery.finish(admittedAt)
	_, err := txdb.UpdateChatWakeupRun(ctx, wakeupRunParams(wakeup, admittedAt, finish))
	if err != nil {
		return wakeupFinish{}, xerrors.Errorf("update wake-up run: %w", err)
	}
	if delivery.watch == nil {
		return finish, nil
	}
	patch := delivery.specPatch
	if len(patch) == 0 {
		patch = json.RawMessage(`{}`)
	}
	_, err = txdb.MarkChatWakeupWatchDelivered(ctx, forkdb.MarkChatWakeupWatchDeliveredParams{
		Done:       delivery.done,
		Now:        admittedAt,
		SpecPatch:  patch,
		ID:         delivery.watch.ID,
		WakeupID:   wakeup.ID,
		Deliveries: delivery.watch.Deliveries,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return wakeupFinish{}, errWakeupOccurrenceRan
	}
	if err != nil {
		return wakeupFinish{}, xerrors.Errorf("mark wake-up watch delivered: %w", err)
	}
	if err := insertRun(); err != nil {
		return wakeupFinish{}, err
	}
	return finish, nil
}

// wakeupFailure maps a failed send to the status of its run and to how the
// occurrence ends.
func (p *Server) wakeupFailure(ctx context.Context, wakeup forkdb.ChatWakeup, now time.Time, sendErr error) (forkdb.ChatWakeupRunStatus, wakeupFinish) {
	if _, denied := errors.AsType[*chathooks.UserPromptDeniedError](sendErr); denied {
		return forkdb.ChatWakeupRunStatusFailed, wakeupFinish{status: forkdb.ChatWakeupStatusPaused, reason: "hook_denied"}
	}
	switch {
	case errors.Is(sendErr, ErrChatArchived), errors.Is(sendErr, chatstate.ErrChatFamilyArchived):
		return forkdb.ChatWakeupRunStatusSkipped, wakeupFinish{status: forkdb.ChatWakeupStatusCanceled, reason: wakeupReasonArchived}
	default:
		// Includes hook dispatch failures, which are transient: the next
		// occurrence tries again, and a final events poll is retried.
		p.logger.Warn(ctx, "chat wake-up send failed",
			slog.F("wakeup_id", wakeup.ID),
			slog.F("chat_id", wakeup.ChatID),
			slog.Error(sendErr),
		)
		if retry, ok := eventsFinalRetry(wakeup, now, now.Add(pullRequestErrorRetry)); ok {
			return forkdb.ChatWakeupRunStatusFailed, retry
		}
		return forkdb.ChatWakeupRunStatusFailed, nextWakeupRun(wakeup, now, 0)
	}
}

// nextWakeupRun computes how wakeup continues after an occurrence that
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
	case forkdb.ChatWakeupKindEvents:
		return eventsWakeupRunAt(wakeup, now, runDelta, now.Add(wakeupPollInterval))
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

// finishWakeup records how an occurrence of wakeup of chat ended, without a
// message, and notifies the owner when it paused the wake-up. It returns
// errWakeupNotDue and writes nothing when the wake-up changed since it was
// listed as due.
func (p *Server) finishWakeup(ctx context.Context, chat database.Chat, wakeup forkdb.ChatWakeup, now time.Time, finish wakeupFinish) error {
	err := p.inWakeupTx(ctx, wakeup, func(ftx forkdb.Store) error {
		_, err := ftx.UpdateChatWakeupRun(ctx, wakeupRunParams(wakeup, now, finish))
		return err
	})
	if errors.Is(err, errWakeupNotDue) {
		return err
	}
	if err != nil {
		return xerrors.Errorf("update wake-up run: %w", err)
	}
	if finish.status == forkdb.ChatWakeupStatusPaused {
		p.notifyWakeupPaused(ctx, chat, finish)
	}
	return nil
}

// inWakeupTx runs fn in a transaction that holds the lock of the chat of
// wakeup and then the lock of wakeup, the order every writer of a wake-up
// takes them in. It returns errWakeupNotDue when wakeup changed since it was
// listed as due, or another transaction holds it.
func (p *Server) inWakeupTx(ctx context.Context, wakeup forkdb.ChatWakeup, fn func(ftx forkdb.Store) error) error {
	return forkdb.InTx(p.db, func(tx database.Store, ftx forkdb.Store) error {
		if _, err := tx.GetChatByIDForUpdate(ctx, wakeup.ChatID); err != nil {
			return xerrors.Errorf("lock chat: %w", err)
		}
		if err := lockDueWakeup(ctx, ftx, wakeup); err != nil {
			return err
		}
		return fn(ftx)
	}, nil)
}

// lockDueWakeup locks wakeup for its delivery. It returns errWakeupNotDue
// when another transaction holds the wake-up, or when the wake-up is no
// longer active or changed since it was listed as due: a pause, a resume, a
// cancel or a delivery by another replica all change it.
func lockDueWakeup(ctx context.Context, ftx forkdb.Store, wakeup forkdb.ChatWakeup) error {
	locked, err := ftx.LockActiveChatWakeup(ctx, wakeup.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return errWakeupNotDue
	}
	if err != nil {
		return xerrors.Errorf("lock wake-up: %w", err)
	}
	if locked.RunCount != wakeup.RunCount ||
		locked.NextRunAt.Valid != wakeup.NextRunAt.Valid ||
		!locked.NextRunAt.Time.Equal(wakeup.NextRunAt.Time) ||
		!locked.UpdatedAt.Equal(wakeup.UpdatedAt) {
		return errWakeupNotDue
	}
	return nil
}

// cancelArchivedWakeups cancels the active and paused wake-ups of the chats of
// an archived family.
func cancelArchivedWakeups(ctx context.Context, fdb forkdb.Store, chats []database.Chat, now time.Time) error {
	for _, chat := range chats {
		_, err := fdb.CancelChatWakeupsByChatID(ctx, forkdb.CancelChatWakeupsByChatIDParams{
			ChatID:    chat.ID,
			Reason:    wakeupReasonArchived,
			UpdatedAt: now,
		})
		if err != nil {
			return xerrors.Errorf("cancel wake-ups of archived chat %s: %w", chat.ID, err)
		}
	}
	return nil
}

// wakeupRunParams records finish of an occurrence of wakeup at now.
func wakeupRunParams(wakeup forkdb.ChatWakeup, now time.Time, finish wakeupFinish) forkdb.UpdateChatWakeupRunParams {
	params := forkdb.UpdateChatWakeupRunParams{
		ID:            wakeup.ID,
		Status:        finish.status,
		StatusReason:  sql.NullString{String: finish.reason, Valid: finish.reason != ""},
		NextRunAt:     finish.nextRunAt,
		RunCountDelta: finish.runDelta,
		UpdatedAt:     now,
	}
	if finish.runDelta > 0 {
		params.LastRunAt = sql.NullTime{Time: now, Valid: true}
	}
	return params
}
