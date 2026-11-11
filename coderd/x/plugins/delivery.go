package plugins

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
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
	if err := r.m.fdb.EnsurePluginChatBindings(sys(ctx), r.plugin.ID); err != nil {
		r.logger.Warn(ctx, "bind chats to plugin", slog.Error(err))
		return
	}
	due, err := r.m.fdb.GetPluginChatBindingsDue(sys(ctx), forkdb.GetPluginChatBindingsDueParams{
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

// deliverToActiveLink sends one event for chat b to the link that follows
// it and reports whether delivery may advance the binding's cursor. A user
// may link several external identities to one plugin, and each follows its
// own active chat; broadcasting would push one identity's conversation to
// all of them.
func (r *runner) deliverToActiveLink(ctx context.Context, b forkdb.GetPluginChatBindingsDueRow, status database.ChatStatus, texts []*pluginproto.ChatText) bool {
	// Host calls move a link's active chat while an export runs under
	// callMu, so hold it from the lookup through the event call; otherwise
	// a concurrent webhook can move the link between the two.
	r.callMu.Lock()
	defer r.callMu.Unlock()
	links, err := r.m.fdb.GetPluginUserLinksByPluginAndUser(sys(ctx), forkdb.GetPluginUserLinksByPluginAndUserParams{
		PluginID: r.plugin.ID,
		UserID:   b.ChatOwnerID,
	})
	if err != nil {
		r.logger.Warn(ctx, "list links of chat owner", slog.Error(err))
		return false
	}
	var target *forkdb.PluginUserLink
	var idle []forkdb.PluginUserLink
	for i, l := range links {
		if !l.ActiveChatID.Valid {
			idle = append(idle, l)
			continue
		}
		if l.ActiveChatID.UUID == b.ChatID {
			target = &links[i]
		}
	}
	if target == nil {
		// No identity follows the chat yet, for example one the user
		// started in Coder. A plugin without the chats create grant can
		// only reach a link through such a chat, so give it to the one
		// idle link. With several idle links any pick is a guess that
		// could expose the chat to the wrong identity, so deliver nothing.
		if len(idle) != 1 {
			r.logger.Debug(ctx, "no single link follows chat",
				slog.F("chat_id", b.ChatID), slog.F("idle_links", len(idle)))
			return true
		}
		target = &idle[0]
		err := r.m.fdb.SetPluginUserLinkActiveChat(sys(ctx), forkdb.SetPluginUserLinkActiveChatParams{
			PluginID:     r.plugin.ID,
			ExternalID:   target.ExternalID,
			ActiveChatID: uuid.NullUUID{UUID: b.ChatID, Valid: true},
		})
		if err != nil {
			r.logger.Warn(ctx, "set active chat of idle link", slog.Error(err))
			return false
		}
	}
	err = r.invokeResultLocked(ctx, "coder_on_event", &pluginproto.ChatEvent{
		ChatId:     b.ChatID.String(),
		ExternalId: target.ExternalID,
		Status:     string(status),
		Messages:   texts,
		Title:      b.ChatTitle,
	})
	if err != nil {
		r.deliveryFailed(ctx, b, err)
		return false
	}
	return true
}

func (r *runner) deliverChat(ctx context.Context, b forkdb.GetPluginChatBindingsDueRow) {
	msgs, err := r.chatMessagesAfter(ctx, b)
	if err != nil {
		r.logger.Warn(ctx, "list chat messages", slog.F("chat_id", b.ChatID), slog.Error(err))
		return
	}
	cursor := b.SentMessageID
	texts := make([]*pluginproto.ChatText, 0, len(msgs))
	for _, msg := range msgs {
		cursor = max(cursor, msg.ID)
		// The page holds every message the user can see. Only assistant
		// replies go out.
		if msg.Role != database.ChatMessageRoleAssistant {
			continue
		}
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
	if send && !r.deliverToActiveLink(ctx, b, status, texts) {
		return
	}
	sentStatus := b.SentStatus
	if len(msgs) < messagesPerEvent {
		sentStatus = database.NullChatStatus{ChatStatus: status, Valid: true}
	}
	err = r.m.fdb.UpdatePluginChatBindingDelivered(sys(ctx), forkdb.UpdatePluginChatBindingDeliveredParams{
		PluginID:      r.plugin.ID,
		ChatID:        b.ChatID,
		SentMessageID: cursor,
		SentStatus:    sentStatus,
	})
	if err != nil {
		r.logger.Warn(ctx, "store delivery cursor", slog.Error(err))
	}
}

func (r *runner) deliveryFailed(ctx context.Context, b forkdb.GetPluginChatBindingsDueRow, cause error) {
	r.logger.Warn(ctx, "deliver chat event", slog.F("chat_id", b.ChatID), slog.F("attempts", b.Attempts+1), slog.Error(cause))
	if ctx.Err() != nil {
		return
	}
	if b.Attempts+1 >= maxDeliveryAttempts {
		// Drop the page so one bad chat cannot stop the plugin.
		r.setLastError("dropped chat event after " + cause.Error())
		msgs, err := r.chatMessagesAfter(ctx, b)
		cursor := b.SentMessageID
		if err == nil {
			for _, m := range msgs {
				cursor = max(cursor, m.ID)
			}
		}
		err = r.m.fdb.UpdatePluginChatBindingDelivered(sys(ctx), forkdb.UpdatePluginChatBindingDeliveredParams{
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
	err := r.m.fdb.UpdatePluginChatBindingFailed(sys(ctx), forkdb.UpdatePluginChatBindingFailedParams{
		PluginID:      r.plugin.ID,
		ChatID:        b.ChatID,
		NextAttemptAt: r.m.clock.Now().Add(backoff),
	})
	if err != nil {
		r.logger.Warn(ctx, "store delivery failure", slog.Error(err))
	}
}

// chatMessagesAfter returns the next page of messages after the delivery
// cursor of a binding.
func (r *runner) chatMessagesAfter(ctx context.Context, b forkdb.GetPluginChatBindingsDueRow) ([]database.ChatMessage, error) {
	return r.m.db.GetChatMessagesByChatIDAscPaginated(sys(ctx), database.GetChatMessagesByChatIDAscPaginatedParams{
		ChatID:   b.ChatID,
		AfterID:  b.SentMessageID,
		LimitVal: messagesPerEvent,
	})
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
