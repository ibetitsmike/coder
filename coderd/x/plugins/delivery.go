package plugins

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprompt"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/x/pluginsdk/pluginproto"
)

const (
	deliveryBatch       = 50
	messagesPerEvent    = 20
	maxDeliveryAttempts = 10
	maxDeliveryBackoff  = 5 * time.Minute
)

// delivers reports whether the plugin gets chat events at all.
func (r *runner) delivers() bool {
	return r.hasChats(codersdk.PluginChatRead) && len(r.grants.Events) > 0
}

// deliver sends new assistant text and status changes of idle bound chats
// to the plugin. The cursor in plugin_chat_bindings, not pubsub, decides
// what goes out, so a lost event or a restart never loses a reply. A
// replay after a crash can send a message again: ChatText.id lets the
// plugin drop copies.
func (r *runner) deliver(ctx context.Context) {
	if !r.delivers() {
		return
	}
	if err := r.m.db.EnsurePluginChatBindings(sys(ctx), r.plugin.ID); err != nil {
		r.logger.Warn(ctx, "bind chats to plugin", slog.Error(err))
		return
	}
	due, err := r.m.db.GetPluginChatBindingsDue(sys(ctx), database.GetPluginChatBindingsDueParams{
		PluginID:   r.plugin.ID,
		LimitCount: deliveryBatch,
	})
	if err != nil {
		r.logger.Warn(ctx, "list chats due for delivery", slog.Error(err))
		return
	}
	for _, b := range due {
		if ctx.Err() != nil {
			return
		}
		r.deliverChat(ctx, b)
	}
	if len(due) == deliveryBatch {
		r.wakeDelivery()
	}
}

func (r *runner) deliverChat(ctx context.Context, b database.GetPluginChatBindingsDueRow) {
	msgs, err := r.m.db.GetPluginChatMessagesAfter(sys(ctx), database.GetPluginChatMessagesAfterParams{
		ChatID:     b.ChatID,
		AfterID:    b.SentMessageID,
		LimitCount: messagesPerEvent,
	})
	if err != nil {
		r.logger.Warn(ctx, "list chat messages", slog.F("chat_id", b.ChatID), slog.Error(err))
		return
	}
	cursor := b.SentMessageID
	texts := make([]*pluginproto.ChatText, 0, len(msgs))
	for _, msg := range msgs {
		cursor = max(cursor, msg.ID)
		if text := messageText(msg); text != "" {
			texts = append(texts, &pluginproto.ChatText{Id: msg.ID, Text: text})
		}
	}
	// More messages can follow this page: keep the old status, so the
	// binding stays due until the last page is out.
	status := b.ChatStatus
	statusChanged := !b.SentStatus.Valid || b.SentStatus.ChatStatus != status
	if len(msgs) == messagesPerEvent {
		statusChanged = false
	}

	send := (len(texts) > 0 && slices.Contains(r.grants.Events, codersdk.PluginEventChatMessagesAdded)) ||
		(statusChanged && slices.Contains(r.grants.Events, codersdk.PluginEventChatStatusChanged))
	if send {
		links, err := r.m.db.GetPluginUserLinksByPluginAndUser(sys(ctx), database.GetPluginUserLinksByPluginAndUserParams{
			PluginID: r.plugin.ID,
			UserID:   b.ChatOwnerID,
		})
		if err != nil {
			r.logger.Warn(ctx, "list links of chat owner", slog.Error(err))
			return
		}
		for _, l := range links {
			err := r.invokeResult(ctx, "coder_on_event", &pluginproto.ChatEvent{
				ChatId:     b.ChatID.String(),
				ExternalId: l.ExternalID,
				Status:     string(status),
				Messages:   texts,
				Title:      b.ChatTitle,
			})
			if err != nil {
				r.deliveryFailed(ctx, b, err)
				return
			}
		}
		if len(texts) > 0 && len(links) > 0 {
			err := r.m.db.SetPluginUserLinksActiveChatByUser(sys(ctx), database.SetPluginUserLinksActiveChatByUserParams{
				PluginID:     r.plugin.ID,
				UserID:       b.ChatOwnerID,
				ActiveChatID: uuidNull(b.ChatID),
			})
			if err != nil {
				r.logger.Warn(ctx, "set active plugin chat", slog.Error(err))
			}
		}
	}
	sentStatus := b.SentStatus
	if len(msgs) < messagesPerEvent {
		sentStatus = database.NullChatStatus{ChatStatus: status, Valid: true}
	}
	err = r.m.db.UpdatePluginChatBindingDelivered(sys(ctx), database.UpdatePluginChatBindingDeliveredParams{
		PluginID:      r.plugin.ID,
		ChatID:        b.ChatID,
		SentMessageID: cursor,
		SentStatus:    sentStatus,
	})
	if err != nil {
		r.logger.Warn(ctx, "store delivery cursor", slog.Error(err))
	}
}

func (r *runner) deliveryFailed(ctx context.Context, b database.GetPluginChatBindingsDueRow, cause error) {
	r.logger.Warn(ctx, "deliver chat event", slog.F("chat_id", b.ChatID), slog.F("attempts", b.Attempts+1), slog.Error(cause))
	if ctx.Err() != nil {
		return
	}
	if b.Attempts+1 >= maxDeliveryAttempts {
		// Drop the page so one bad chat cannot stop the plugin.
		r.setLastError("dropped chat event after " + cause.Error())
		msgs, err := r.m.db.GetPluginChatMessagesAfter(sys(ctx), database.GetPluginChatMessagesAfterParams{
			ChatID: b.ChatID, AfterID: b.SentMessageID, LimitCount: messagesPerEvent,
		})
		cursor := b.SentMessageID
		if err == nil {
			for _, m := range msgs {
				cursor = max(cursor, m.ID)
			}
		}
		err = r.m.db.UpdatePluginChatBindingDelivered(sys(ctx), database.UpdatePluginChatBindingDeliveredParams{
			PluginID:      r.plugin.ID,
			ChatID:        b.ChatID,
			SentMessageID: cursor,
			SentStatus:    database.NullChatStatus{ChatStatus: b.ChatStatus, Valid: true},
		})
		if err != nil {
			r.logger.Warn(ctx, "drop chat event for plugin", slog.Error(err))
		}
		return
	}
	backoff := min(time.Second<<b.Attempts, maxDeliveryBackoff)
	err := r.m.db.UpdatePluginChatBindingFailed(sys(ctx), database.UpdatePluginChatBindingFailedParams{
		PluginID:      r.plugin.ID,
		ChatID:        b.ChatID,
		NextAttemptAt: r.m.clock.Now().Add(backoff),
	})
	if err != nil {
		r.logger.Warn(ctx, "store delivery failure", slog.Error(err))
	}
}

// messageText joins the text parts of a message that the user can see.
func messageText(msg database.ChatMessage) string {
	parts, err := chatprompt.ParseContent(msg)
	if err != nil {
		return ""
	}
	var texts []string
	for _, p := range parts {
		if p.Type == codersdk.ChatMessagePartTypeText && strings.TrimSpace(p.Text) != "" {
			texts = append(texts, strings.TrimSpace(p.Text))
		}
	}
	return strings.Join(texts, "\n\n")
}

func uuidNull(id uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: id, Valid: true}
}
