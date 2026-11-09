package pubsub

// This file is fork code: it holds the pubsub event of the fork-chat-todos
// experiment, so upstream's pubsub files stay untouched.

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"golang.org/x/xerrors"
)

// ChatTodosEventChannel returns the pubsub channel on which every write of the
// todo list of chatID publishes a ChatTodosEvent.
func ChatTodosEventChannel(chatID uuid.UUID) string {
	return "chat_todos:" + chatID.String()
}

// ChatTodosEvent announces a new version of a chat todo list. It carries only
// the version because NOTIFY payloads cap at 8000 bytes, so subscribers re-read
// the list.
type ChatTodosEvent struct {
	ChatID  uuid.UUID `json:"chat_id"`
	Version int64     `json:"version"`
}

// HandleChatTodosEvent wraps a typed callback for ChatTodosEvent messages
// delivered over pubsub.
func HandleChatTodosEvent(cb func(ctx context.Context, payload ChatTodosEvent, err error)) func(ctx context.Context, message []byte, err error) {
	return func(ctx context.Context, message []byte, err error) {
		if err != nil {
			cb(ctx, ChatTodosEvent{}, xerrors.Errorf("chat todos event pubsub: %w", err))
			return
		}
		var payload ChatTodosEvent
		if err := json.Unmarshal(message, &payload); err != nil {
			cb(ctx, ChatTodosEvent{}, xerrors.Errorf("unmarshal chat todos event: %w", err))
			return
		}
		cb(ctx, payload, nil)
	}
}
