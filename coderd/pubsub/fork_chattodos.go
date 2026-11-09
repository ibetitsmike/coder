package pubsub

// This file is fork code: it holds the pubsub event of the fork-chat-todos
// experiment, so upstream's pubsub files stay untouched.

import "github.com/google/uuid"

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
