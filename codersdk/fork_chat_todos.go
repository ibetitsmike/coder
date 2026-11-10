package codersdk

// This file is fork code: it holds the chat todo list types and client
// methods of the fork-chat-todos experiment, so upstream's chats.go stays
// untouched.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// ChatTodoStatus is the state of one item in a chat todo list.
type ChatTodoStatus string

// The states of a chat todo list item. A trailing comment would become a
// swagger enum description, so the directive covers the block.
//
//nolint:misspell // "cancelled" is a stored API value; changing it breaks existing todo lists.
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

// ChatTodoList is the whole todo list of a chat. Version is 0 while the chat
// has no stored list, and increases by one with every write.
type ChatTodoList struct {
	Todos []ChatTodo `json:"todos"`
	// Version is the revision of Todos. It lets a watcher drop an event for
	// a version it has already sent.
	Version int64 `json:"version"`
}

// DecodeChatTodoItems decodes the items of a stored chat todo list. The
// result is never nil, so a response never writes todos as null.
func DecodeChatTodoItems(items []byte) ([]ChatTodo, error) {
	todos := []ChatTodo{}
	if len(items) > 0 {
		if err := json.Unmarshal(items, &todos); err != nil {
			return nil, xerrors.Errorf("decode todo list: %w", err)
		}
		if todos == nil {
			todos = []ChatTodo{}
		}
	}
	return todos, nil
}

// ChatTodos returns the todo list of a chat. A chat without a list has an
// empty list at version 0.
func (c *Client) ChatTodos(ctx context.Context, chatID uuid.UUID) (ChatTodoList, error) {
	return requestJSON[ChatTodoList](ctx, c, http.MethodGet, fmt.Sprintf("/api/v2/chats/fork/%s/todos", chatID), http.StatusOK, nil)
}

// WatchChatTodos streams the todo list of a chat. The returned channel
// receives the current list first, and then the latest list after writes to
// it. Writes close together can arrive as one list, so a version can be
// skipped. Callers must close the returned io.Closer to release the websocket
// connection when done.
func (c *Client) WatchChatTodos(ctx context.Context, chatID uuid.UUID) (<-chan ChatTodoList, io.Closer, error) {
	conn, err := c.Dial(
		ctx,
		fmt.Sprintf("/api/v2/chats/fork/%s/todos/watch", chatID),
		&websocket.DialOptions{CompressionMode: websocket.CompressionDisabled},
	)
	if err != nil {
		return nil, nil, err
	}
	conn.SetReadLimit(1 << 20) // 1MiB

	streamCtx, streamCancel := context.WithCancel(ctx)
	lists := make(chan ChatTodoList, 32)

	go func() {
		defer close(lists)
		defer streamCancel()
		defer func() {
			_ = conn.Close(websocket.StatusNormalClosure, "")
		}()

		for {
			var list ChatTodoList
			if err := wsjson.Read(streamCtx, conn, &list); err != nil {
				return
			}
			select {
			case <-streamCtx.Done():
				return
			case lists <- list:
			}
		}
	}()

	return lists, closeFunc(func() error {
		streamCancel()
		return nil
	}), nil
}
