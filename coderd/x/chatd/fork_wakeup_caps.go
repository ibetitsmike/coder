package chatd

// This file is fork code: it holds the daily budget caps of the
// fork-chat-wakeups experiment and the notification that tells a chat owner a
// wake-up paused. The wake-up worker checks the caps right before each send.

import (
	"cmp"
	"context"
	"fmt"
	"time"

	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/notifications"
)

// Default daily caps of wake-ups, over the rolling 24 hours before a send.
// Spend counts the total_cost_micros of chat messages, which is NULL and
// counts as zero for models without prices.
const (
	wakeupCapWindow = 24 * time.Hour
	// A 5-minute loop around the clock.
	maxChatWakeupRunsPerDay                = 288
	maxOrganizationWakeupRunsPerDay        = 2880
	maxChatWakeupSpendMicrosPerDay         = 20_000_000
	maxOrganizationWakeupSpendMicrosPerDay = 200_000_000

	wakeupReasonDailyRunCap       = "daily_run_cap"
	wakeupReasonOrgDailyRunCap    = "org_daily_run_cap"
	wakeupReasonDailySpendCap     = "daily_spend_cap"
	wakeupReasonOrgDailySpendCap  = "org_daily_spend_cap"
	wakeupPausedNotificationTitle = "Chat wake-up paused"
)

// wakeupPauseReasonText explains each reason the worker pauses a wake-up for.
var wakeupPauseReasonText = map[string]string{
	wakeupReasonDailyRunCap:      fmt.Sprintf("it reached the daily limit of %d runs", maxChatWakeupRunsPerDay),
	wakeupReasonOrgDailyRunCap:   fmt.Sprintf("the organization reached its daily limit of %d wake-up runs", maxOrganizationWakeupRunsPerDay),
	wakeupReasonDailySpendCap:    fmt.Sprintf("the chat reached its daily spend limit of $%d", maxChatWakeupSpendMicrosPerDay/1_000_000),
	wakeupReasonOrgDailySpendCap: fmt.Sprintf("the organization reached its daily wake-up spend limit of $%d", maxOrganizationWakeupSpendMicrosPerDay/1_000_000),
	"hook_denied":                "a prompt hook denied the wake-up prompt",
	"invalid_cron":               "its cron schedule is invalid",
	"invalid_events":             "its events are invalid",
}

// wakeupCapReached returns the reason of the first daily cap that chat or its
// organization reached at now, or "" when a wake-up of chat may run. The
// check does not hold the chat lock, so concurrent replicas can overshoot the
// organization caps by a few runs.
func (*Server) wakeupCapReached(ctx context.Context, fdb forkdb.Store, chat database.Chat, now time.Time) (string, error) {
	since := now.Add(-wakeupCapWindow)
	runs, err := fdb.CountChatWakeupRunsSince(ctx, forkdb.CountChatWakeupRunsSinceParams{ChatID: chat.ID, Since: since})
	if err != nil {
		return "", xerrors.Errorf("count chat wake-up runs: %w", err)
	}
	if runs >= maxChatWakeupRunsPerDay {
		return wakeupReasonDailyRunCap, nil
	}
	runs, err = fdb.CountOrganizationChatWakeupRunsSince(ctx, forkdb.CountOrganizationChatWakeupRunsSinceParams{OrganizationID: chat.OrganizationID, Since: since})
	if err != nil {
		return "", xerrors.Errorf("count organization wake-up runs: %w", err)
	}
	if runs >= maxOrganizationWakeupRunsPerDay {
		return wakeupReasonOrgDailyRunCap, nil
	}
	spend, err := fdb.SumChatCostMicrosSince(ctx, forkdb.SumChatCostMicrosSinceParams{ChatID: chat.ID, Since: since})
	if err != nil {
		return "", xerrors.Errorf("sum chat spend: %w", err)
	}
	if spend >= maxChatWakeupSpendMicrosPerDay {
		return wakeupReasonDailySpendCap, nil
	}
	spend, err = fdb.SumOrganizationWakeupChatCostMicrosSince(ctx, forkdb.SumOrganizationWakeupChatCostMicrosSinceParams{OrganizationID: chat.OrganizationID, Since: since})
	if err != nil {
		return "", xerrors.Errorf("sum organization wake-up spend: %w", err)
	}
	if spend >= maxOrganizationWakeupSpendMicrosPerDay {
		return wakeupReasonOrgDailySpendCap, nil
	}
	return "", nil
}

// pauseCappedWakeup pauses wakeup when chat or its organization reached a
// daily cap, and reports whether it did. It records no run.
func (p *Server) pauseCappedWakeup(ctx context.Context, fdb forkdb.Store, chat database.Chat, wakeup forkdb.ChatWakeup, now time.Time) (bool, error) {
	reason, err := p.wakeupCapReached(ctx, fdb, chat, now)
	if err != nil || reason == "" {
		return false, err
	}
	return true, p.finishWakeupClaim(ctx, fdb, chat, wakeup, now, wakeupFinish{status: forkdb.ChatWakeupStatusPaused, reason: reason})
}

// notifyWakeupPaused tells the chat owner that a wake-up of chat paused for
// reason. The payload has no dedupe bypass, so the enqueuer sends at most one
// notification per chat and reason a day, however many wake-ups pause. A
// failure only logs: it must not change the outcome of the wake-up.
func (p *Server) notifyWakeupPaused(ctx context.Context, chat database.Chat, reason string) {
	if p.chatWorker == nil {
		return
	}
	message := fmt.Sprintf("A wake-up of chat %q is paused: %s.", chat.Title, cmp.Or(wakeupPauseReasonText[reason], reason))
	//nolint:gocritic // Background wake-up notifications run as the notifier subject.
	if _, err := p.chatWorker.opts.NotificationsEnqueuer.EnqueueWithData(
		dbauthz.AsNotifier(ctx),
		chat.OwnerID,
		notifications.TemplateCustomNotification,
		map[string]string{
			"custom_title":   wakeupPausedNotificationTitle,
			"custom_message": message,
		},
		map[string]any{
			"chat_id": chat.ID,
			"reason":  reason,
		},
		"chat_wakeups",
	); err != nil {
		p.logger.Warn(ctx, "failed to notify the owner of a paused chat wake-up",
			slog.F("chat_id", chat.ID),
			slog.F("reason", reason),
			slog.Error(err),
		)
	}
}
