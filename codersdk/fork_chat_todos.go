package codersdk

// This file is fork code: it holds the chat todo list types of the
// fork-chat-todos experiment, so upstream's chats.go stays untouched.

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
