package chatd

// This file is fork code: it holds the owner controls of the chat wake-ups of
// the fork-chat-wakeups experiment, which the chat wake-ups API serves.

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/database/forkdb"
)

// ErrChatWakeupFinished reports a change to a wake-up that already completed
// or was canceled.
var ErrChatWakeupFinished = xerrors.New("chat wake-up already finished")

const (
	wakeupReasonPausedByUser   = "paused_by_user"
	wakeupReasonCanceledByUser = "canceled_by_user"
)

// SetChatWakeupStatus pauses (paused), resumes (active) or cancels (canceled)
// the wake-up wakeupID of chat as the actor in ctx, and returns the wake-up
// before and after the change. Setting the status a wake-up already has
// changes nothing, and resuming a wake-up with no run left completes it.
// It returns sql.ErrNoRows when chat has no wake-up wakeupID, and
// ErrChatWakeupFinished with the finished wake-up as before when the wake-up
// already completed or was canceled.
func (p *Server) SetChatWakeupStatus(ctx context.Context, chat database.Chat, wakeupID uuid.UUID, status forkdb.ChatWakeupStatus) (before, after forkdb.ChatWakeup, err error) {
	// The row lock makes a worker that finishes its claim in between wait,
	// and the update clears the lease, so the worker's finish then changes
	// nothing and a control never overwrites a run.
	err = forkdb.InTx(p.db, func(_ database.Store, ftx forkdb.Store) error {
		wakeup, err := ftx.GetChatWakeupByIDForUpdate(ctx, wakeupID)
		if err != nil {
			return err
		}
		if wakeup.ChatID != chat.ID {
			return sql.ErrNoRows
		}
		before, after = wakeup, wakeup
		if wakeup.Status == status {
			return nil
		}
		if wakeup.Status == forkdb.ChatWakeupStatusCompleted || wakeup.Status == forkdb.ChatWakeupStatusCanceled {
			return ErrChatWakeupFinished
		}
		now := dbtime.Time(p.clock.Now("chatd", "wakeups")).UTC()
		finish, err := controlWakeupFinish(wakeup, status, now)
		if err != nil {
			return err
		}
		after, err = ftx.UpdateChatWakeupStatus(ctx, forkdb.UpdateChatWakeupStatusParams{
			ID:           wakeup.ID,
			Status:       finish.status,
			StatusReason: sql.NullString{String: finish.reason, Valid: finish.reason != ""},
			NextRunAt:    finish.nextRunAt,
			UpdatedAt:    now,
		})
		return err
	}, nil)
	if errors.Is(err, ErrChatWakeupFinished) {
		return before, forkdb.ChatWakeup{}, err
	}
	if err != nil {
		return forkdb.ChatWakeup{}, forkdb.ChatWakeup{}, err
	}
	return before, after, nil
}

// controlWakeupFinish returns how setting status at now changes wakeup, which
// is active or paused and has another status.
func controlWakeupFinish(wakeup forkdb.ChatWakeup, status forkdb.ChatWakeupStatus, now time.Time) (wakeupFinish, error) {
	switch status {
	case forkdb.ChatWakeupStatusPaused:
		// The run time stays, so a resume before it keeps the schedule.
		return wakeupFinish{status: forkdb.ChatWakeupStatusPaused, reason: wakeupReasonPausedByUser, nextRunAt: wakeup.NextRunAt}, nil
	case forkdb.ChatWakeupStatusCanceled:
		return wakeupFinish{status: forkdb.ChatWakeupStatusCanceled, reason: wakeupReasonCanceledByUser}, nil
	case forkdb.ChatWakeupStatusActive:
		return resumedWakeupRun(wakeup, now), nil
	default:
		return wakeupFinish{}, xerrors.Errorf("unsupported wake-up status %q", status)
	}
}

// resumedWakeupRun returns how a paused wakeup resumes at now. A run time
// still ahead stays. Otherwise a repeating wake-up continues its schedule from
// now and a delay runs at once, unless no run is left.
func resumedWakeupRun(wakeup forkdb.ChatWakeup, now time.Time) wakeupFinish {
	if wakeup.NextRunAt.Valid && wakeup.NextRunAt.Time.After(now) {
		return wakeupFinish{status: forkdb.ChatWakeupStatusActive, nextRunAt: wakeup.NextRunAt}
	}
	if wakeup.Kind != forkdb.ChatWakeupKindDelay {
		// An interval counts from its last run time, which is overdue here.
		wakeup.NextRunAt = sql.NullTime{Time: now, Valid: true}
		return nextWakeupRun(wakeup, now, 0)
	}
	switch {
	case wakeup.MaxRuns.Valid && wakeup.RunCount >= wakeup.MaxRuns.Int32:
		return wakeupFinish{status: forkdb.ChatWakeupStatusCompleted, reason: "max_runs"}
	case wakeup.ExpiresAt.Valid && !now.Before(wakeup.ExpiresAt.Time):
		return wakeupFinish{status: forkdb.ChatWakeupStatusCompleted, reason: "expired"}
	default:
		return wakeupFinish{status: forkdb.ChatWakeupStatusActive, nextRunAt: sql.NullTime{Time: now, Valid: true}}
	}
}
