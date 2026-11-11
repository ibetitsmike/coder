package chatd

// This file is fork code: it holds the daily budget caps of the
// fork-chat-wakeups experiment and the notification that tells a chat owner a
// wake-up paused. The wake-up worker checks the caps right before each send.

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/notifications"
	"github.com/coder/coder/v2/codersdk"
)

// Default daily caps of wake-ups, over the rolling 24 hours before a send. An
// organization can replace each one in chat_organization_wakeup_caps. Spend
// counts the total_cost_micros of chat messages, which is NULL and counts as
// zero for models without prices.
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

// wakeupPauseReasonText explains each reason the worker pauses a wake-up for
// that is not a cap.
var wakeupPauseReasonText = map[string]string{
	"hook_denied":    "a prompt hook denied the wake-up prompt",
	"invalid_cron":   "its cron schedule is invalid",
	"invalid_events": "its events are invalid",
}

// wakeupCaps are the daily wake-up caps in force in an organization.
type wakeupCaps codersdk.ForkWakeupCapValues

var defaultWakeupCaps = wakeupCaps(EffectiveWakeupCaps(forkdb.ChatOrganizationWakeupCap{}))

// EffectiveWakeupCaps returns the default wake-up caps, replaced by each cap
// that row sets.
func EffectiveWakeupCaps(row forkdb.ChatOrganizationWakeupCap) codersdk.ForkWakeupCapValues {
	caps := codersdk.ForkWakeupCapValues{
		MaxChatRunsPerDay:                maxChatWakeupRunsPerDay,
		MaxOrganizationRunsPerDay:        maxOrganizationWakeupRunsPerDay,
		MaxChatSpendMicrosPerDay:         maxChatWakeupSpendMicrosPerDay,
		MaxOrganizationSpendMicrosPerDay: maxOrganizationWakeupSpendMicrosPerDay,
	}
	if row.MaxChatRunsPerDay.Valid {
		caps.MaxChatRunsPerDay = int64(row.MaxChatRunsPerDay.Int32)
	}
	if row.MaxOrganizationRunsPerDay.Valid {
		caps.MaxOrganizationRunsPerDay = int64(row.MaxOrganizationRunsPerDay.Int32)
	}
	if row.MaxChatSpendMicrosPerDay.Valid {
		caps.MaxChatSpendMicrosPerDay = row.MaxChatSpendMicrosPerDay.Int64
	}
	if row.MaxOrganizationSpendMicrosPerDay.Valid {
		caps.MaxOrganizationSpendMicrosPerDay = row.MaxOrganizationSpendMicrosPerDay.Int64
	}
	return caps
}

// organizationWakeupCaps returns the wake-up caps in force in the
// organization.
func organizationWakeupCaps(ctx context.Context, fdb forkdb.Store, organizationID uuid.UUID) (wakeupCaps, error) {
	//nolint:gocritic // Chat owners cannot read organization config, so chatd reads it.
	row, err := fdb.GetChatOrganizationWakeupCaps(dbauthz.AsChatd(ctx), organizationID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return wakeupCaps{}, xerrors.Errorf("get wake-up caps of organization %s: %w", organizationID, err)
	}
	return wakeupCaps(EffectiveWakeupCaps(row)), nil
}

// pauseText explains reason with the caps in force.
func (caps wakeupCaps) pauseText(reason string) string {
	switch reason {
	case wakeupReasonDailyRunCap:
		return fmt.Sprintf("it reached the daily limit of %d runs", caps.MaxChatRunsPerDay)
	case wakeupReasonOrgDailyRunCap:
		return fmt.Sprintf("the organization reached its daily limit of %d wake-up runs", caps.MaxOrganizationRunsPerDay)
	case wakeupReasonDailySpendCap:
		return "the chat reached its daily spend limit of " + formatWakeupSpend(caps.MaxChatSpendMicrosPerDay)
	case wakeupReasonOrgDailySpendCap:
		return "the organization reached its daily wake-up spend limit of " + formatWakeupSpend(caps.MaxOrganizationSpendMicrosPerDay)
	}
	return cmp.Or(wakeupPauseReasonText[reason], reason)
}

// formatWakeupSpend formats micros as dollars, with cents only when the
// amount has them.
func formatWakeupSpend(micros int64) string {
	if micros%1_000_000 == 0 {
		return fmt.Sprintf("$%d", micros/1_000_000)
	}
	return fmt.Sprintf("$%.2f", float64(micros)/1_000_000)
}

// wakeupCapReached returns the reason of the first daily cap that chat or its
// organization reached at now, or "" when a wake-up of chat may run. The
// check does not hold the chat lock, so concurrent replicas can overshoot the
// organization caps by a few runs.
func (*Server) wakeupCapReached(ctx context.Context, fdb forkdb.Store, chat database.Chat, now time.Time) (string, wakeupCaps, error) {
	caps, err := organizationWakeupCaps(ctx, fdb, chat.OrganizationID)
	if err != nil {
		return "", caps, err
	}
	since := now.Add(-wakeupCapWindow)
	runs, err := fdb.CountChatWakeupRunsSince(ctx, forkdb.CountChatWakeupRunsSinceParams{ChatID: chat.ID, Since: since})
	if err != nil {
		return "", caps, xerrors.Errorf("count chat wake-up runs: %w", err)
	}
	if runs >= caps.MaxChatRunsPerDay {
		return wakeupReasonDailyRunCap, caps, nil
	}
	runs, err = fdb.CountOrganizationChatWakeupRunsSince(ctx, forkdb.CountOrganizationChatWakeupRunsSinceParams{OrganizationID: chat.OrganizationID, Since: since})
	if err != nil {
		return "", caps, xerrors.Errorf("count organization wake-up runs: %w", err)
	}
	if runs >= caps.MaxOrganizationRunsPerDay {
		return wakeupReasonOrgDailyRunCap, caps, nil
	}
	spend, err := fdb.SumChatCostMicrosSince(ctx, forkdb.SumChatCostMicrosSinceParams{ChatID: chat.ID, Since: since})
	if err != nil {
		return "", caps, xerrors.Errorf("sum chat spend: %w", err)
	}
	if spend >= caps.MaxChatSpendMicrosPerDay {
		return wakeupReasonDailySpendCap, caps, nil
	}
	spend, err = fdb.SumOrganizationWakeupChatCostMicrosSince(ctx, forkdb.SumOrganizationWakeupChatCostMicrosSinceParams{OrganizationID: chat.OrganizationID, Since: since})
	if err != nil {
		return "", caps, xerrors.Errorf("sum organization wake-up spend: %w", err)
	}
	if spend >= caps.MaxOrganizationSpendMicrosPerDay {
		return wakeupReasonOrgDailySpendCap, caps, nil
	}
	return "", caps, nil
}

// pauseCappedWakeup pauses wakeup when chat or its organization reached a
// daily cap, and reports whether it did. It records no run.
func (p *Server) pauseCappedWakeup(ctx context.Context, fdb forkdb.Store, chat database.Chat, wakeup forkdb.ChatWakeup, now time.Time) (bool, error) {
	reason, caps, err := p.wakeupCapReached(ctx, fdb, chat, now)
	if err != nil || reason == "" {
		return false, err
	}
	return true, p.finishWakeupClaim(ctx, fdb, chat, wakeup, now, wakeupFinish{status: forkdb.ChatWakeupStatusPaused, reason: reason, pauseText: caps.pauseText(reason)})
}

// notifyWakeupPaused tells the chat owner that a wake-up of chat paused for
// reason. The payload has no dedupe bypass, so the enqueuer sends at most one
// notification per chat and reason a day, however many wake-ups pause. A
// failure only logs: it must not change the outcome of the wake-up.
func (p *Server) notifyWakeupPaused(ctx context.Context, chat database.Chat, finish wakeupFinish) {
	if p.chatWorker == nil {
		return
	}
	reason := finish.reason
	message := fmt.Sprintf("A wake-up of chat %q is paused: %s.", chat.Title, cmp.Or(finish.pauseText, defaultWakeupCaps.pauseText(reason)))
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
