package chatd

// This file is fork code: it holds the notify_user chat tool of the
// fork-chat-wakeups experiment. appendForkTools adds the tool to a turn.

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/fantasy"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/notifications"
	"github.com/coder/coder/v2/codersdk"
)

const (
	notifyUserToolName              = "notify_user"
	maxChatUserNotificationsPerHour = 10
	// maxNotificationTitleBytes is the title limit of
	// codersdk.CustomNotificationContent.Validate.
	maxNotificationTitleBytes = 120
)

type notifyUserArgs struct {
	Message string `json:"message" description:"The notification text."`
	Title   string `json:"title,omitempty" description:"The notification title. Defaults to the chat title."`
}

type notifyUserResult struct {
	Sent bool `json:"sent"`
}

// notifyUserTool returns the notify_user tool, which sends a Coder
// notification to the owner of chat.
func (p *Server) notifyUserTool(chat database.Chat) fantasy.AgentTool {
	return serialNotifyUserTool{AgentTool: fantasy.NewAgentTool(
		notifyUserToolName,
		"Send a notification to the user who owns this chat through Coder "+
			"notifications (inbox, and email, webhook or push where configured). "+
			"Use it sparingly, for results or blockers the user must see while "+
			"they are away from this chat. "+
			fmt.Sprintf("At most %d notifications per chat per hour.", maxChatUserNotificationsPerHour),
		func(ctx context.Context, args notifyUserArgs, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if err := p.notifyChatOwner(ctx, chat, args); err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}
			return toolJSONResponse(notifyUserResult{Sent: true}), nil
		},
	)}
}

// serialNotifyUserTool runs the notify_user calls of a step one at a time, so
// each call counts the notifications of the calls before it. A chat runs one
// turn at a time, so this keeps the hourly limit.
type serialNotifyUserTool struct {
	fantasy.AgentTool
}

func (serialNotifyUserTool) SerialToolCalls() bool { return true }

// notifyChatOwner sends a custom notification to the owner of chat, at most
// maxChatUserNotificationsPerHour per chat in the rolling hour. The limit
// counts the enqueued notifications, so it holds across replicas and
// restarts.
func (p *Server) notifyChatOwner(ctx context.Context, chat database.Chat, args notifyUserArgs) error {
	content := codersdk.CustomNotificationContent{
		Title:   strings.TrimSpace(args.Title),
		Message: strings.TrimSpace(args.Message),
	}
	if content.Message == "" {
		return xerrors.New("message is required")
	}
	if content.Title == "" {
		content.Title = truncateUTF8Bytes(chat.Title, maxNotificationTitleBytes)
	}
	if err := content.Validate(); err != nil {
		return err
	}

	// System users cannot receive custom notifications, as in
	// postCustomNotification.
	owner, err := p.chatOwnerSubject(ctx, chat.OwnerID)
	if err != nil {
		return err
	}
	//nolint:gocritic // The chat owner reads its own user.
	user, err := p.db.GetUserByID(dbauthz.As(ctx, owner), chat.OwnerID)
	if err != nil {
		return xerrors.Errorf("get chat owner: %w", err)
	}
	if user.IsSystem {
		return xerrors.New("chats owned by a system user cannot send notifications")
	}

	//nolint:gocritic // Notifications are enqueued and counted as the notifier subject.
	notifierCtx := dbauthz.AsNotifier(ctx)
	fdb, err := forkdb.From(p.db)
	if err != nil {
		return xerrors.Errorf("fork queries: %w", err)
	}
	now := p.clock.Now().UTC()
	sent, err := fdb.CountChatUserNotificationsSince(notifierCtx, forkdb.CountChatUserNotificationsSinceParams{
		UserID:                 chat.OwnerID,
		NotificationTemplateID: notifications.TemplateCustomNotification,
		ChatID:                 chat.ID.String(),
		Since:                  now.Add(-time.Hour),
	})
	if err != nil {
		return xerrors.Errorf("count chat notifications: %w", err)
	}
	if sent >= maxChatUserNotificationsPerHour {
		return xerrors.Errorf("notification limit reached: at most %d per chat per hour", maxChatUserNotificationsPerHour)
	}
	if p.chatWorker == nil {
		return xerrors.New("notifications are unavailable")
	}
	_, err = p.chatWorker.opts.NotificationsEnqueuer.EnqueueWithData(
		notifierCtx,
		chat.OwnerID,
		notifications.TemplateCustomNotification,
		map[string]string{
			"custom_title":   content.Title,
			"custom_message": content.Message,
		},
		map[string]any{
			"chat_id": chat.ID,
			// Like postCustomNotification: a minute bucket bypasses the
			// per-day dedupe, so identical content can be sent again later
			// in the day.
			"dedupe_bypass_ts": now.Truncate(time.Minute),
		},
		notifyUserToolName,
	)
	if err != nil {
		return xerrors.Errorf("send notification: %w", err)
	}
	return nil
}

// truncateUTF8Bytes cuts s to at most maxBytes bytes without splitting a rune.
func truncateUTF8Bytes(s string, maxBytes int) string {
	for len(s) > maxBytes {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	return s
}
