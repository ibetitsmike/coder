package codersdk

// This file is fork code: it holds the chat todo list types of the
// fork-chat-todos experiment, so upstream's chats.go stays untouched.

import (
	"time"

	"github.com/google/uuid"
)

// ChatTodoStatus is the state of one item in a chat todo list.
type ChatTodoStatus string

// The states of a chat todo list item.
const (
	ChatTodoStatusPending    ChatTodoStatus = "pending"
	ChatTodoStatusInProgress ChatTodoStatus = "in_progress"
	ChatTodoStatusDone       ChatTodoStatus = "done"
	ChatTodoStatusCancelled  ChatTodoStatus = "cancelled"
)

// ChatTodoStatuses lists every valid ChatTodoStatus.
var ChatTodoStatuses = []ChatTodoStatus{
	ChatTodoStatusPending,
	ChatTodoStatusInProgress,
	ChatTodoStatusDone,
	ChatTodoStatusCancelled,
}

// ChatTodo is one item of a chat todo list.
type ChatTodo struct {
	ID      string         `json:"id"`
	Content string         `json:"content"`
	Status  ChatTodoStatus `json:"status"`
}

// ChatTodoList is the todo list of a chat. Version grows by one on every
// write, so a reader can tell which of two lists is newer.
type ChatTodoList struct {
	ChatID    uuid.UUID  `json:"chat_id" format:"uuid"`
	Items     []ChatTodo `json:"items"`
	Version   int64      `json:"version"`
	UpdatedAt time.Time  `json:"updated_at" format:"date-time"`
}
