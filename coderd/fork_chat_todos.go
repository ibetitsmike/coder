package coderd

// This file is fork code: it serves the chat todo list of the
// fork-chat-todos experiment. registerForkChatRoutes mounts these handlers.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/coderd/httpmw"
	"github.com/coder/coder/v2/coderd/pubsub"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/websocket"
)

// readForkChatTodos reads the todo list of chatID as the actor in ctx, so
// dbauthz enforces read access to the owning chat. A chat without a stored
// list has an empty list at version 0.
func readForkChatTodos(ctx context.Context, db database.Store, chatID uuid.UUID) (codersdk.ChatTodoList, error) {
	fdb, err := forkdb.From(db)
	if err != nil {
		return codersdk.ChatTodoList{}, xerrors.Errorf("fork queries: %w", err)
	}
	row, err := fdb.GetChatTodosByChatID(ctx, chatID)
	if errors.Is(err, sql.ErrNoRows) {
		return codersdk.ChatTodoList{Todos: []codersdk.ChatTodo{}}, nil
	}
	if err != nil {
		return codersdk.ChatTodoList{}, xerrors.Errorf("read todo list: %w", err)
	}
	todos, err := codersdk.DecodeChatTodoItems(row.Items)
	if err != nil {
		return codersdk.ChatTodoList{}, err
	}
	return codersdk.ChatTodoList{Todos: todos, Version: row.Version}, nil
}

// @Summary Get the chat todo list
// @ID get-the-chat-todo-list
// @Security CoderSessionToken
// @Produce json
// @Tags Chats
// @Param chat path string true "Chat ID" format(uuid)
// @Success 200 {object} codersdk.ChatTodoList
// @Router /api/v2/chats/fork/{chat}/todos [get]
// @x-apidocgen {"skip": true}
// @Description Returns the todo list that the todo_write tool keeps for this
// @Description chat, or an empty list at version 0 when the chat has none.
func (api *API) getChatTodos(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	chat := httpmw.ChatParam(r)

	list, err := readForkChatTodos(ctx, api.Database, chat.ID)
	if err != nil {
		httpapi.Write(ctx, rw, http.StatusInternalServerError, codersdk.Response{
			Message: "Failed to read the chat todo list.",
			Detail:  err.Error(),
		})
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, list)
}

// @Summary Watch the chat todo list
// @ID watch-the-chat-todo-list
// @Security CoderSessionToken
// @Produce json
// @Tags Chats
// @Param chat path string true "Chat ID" format(uuid)
// @Success 200 {object} codersdk.ChatTodoList
// @Router /api/v2/chats/fork/{chat}/todos/watch [get]
// @x-apidocgen {"skip": true}
// @Description Streams the todo list of the chat over a websocket: the current
// @Description list first, then the latest list after writes to it. Writes close
// @Description together can arrive as one list.
func (api *API) watchChatTodos(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	chat := httpmw.ChatParam(r)
	logger := api.Logger.Named("chat_todos_watcher")

	// The callback only wakes the send loop, so reads and sends stay on this
	// goroutine even when a pubsub delivers callbacks concurrently, and writes
	// that land close together coalesce into one send of the latest list. A
	// dropped-messages error wakes it too, because the missed event may be the
	// last write.
	changed := make(chan struct{}, 1)
	cancelSubscribe, err := api.Pubsub.SubscribeWithErr(pubsub.ChatTodosEventChannel(chat.ID), func(cbCtx context.Context, _ []byte, err error) {
		if err != nil {
			logger.Warn(cbCtx, "chat todo list events", slog.F("chat_id", chat.ID), slog.Error(err))
		}
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	if err != nil {
		httpapi.Write(ctx, rw, http.StatusInternalServerError, codersdk.Response{
			Message: "Failed to subscribe to chat todo list events.",
			Detail:  err.Error(),
		})
		return
	}
	defer cancelSubscribe()

	// Read before accepting, so a failed read answers as an HTTP error. The
	// subscription is already active, so no write after this read is missed.
	list, err := readForkChatTodos(ctx, api.Database, chat.ID)
	if err != nil {
		httpapi.Write(ctx, rw, http.StatusInternalServerError, codersdk.Response{
			Message: "Failed to read the chat todo list.",
			Detail:  err.Error(),
		})
		return
	}

	conn, err := websocket.Accept(rw, r, nil)
	if err != nil {
		httpapi.Write(ctx, rw, http.StatusInternalServerError, codersdk.Response{
			Message: "Failed to open chat todo list stream.",
			Detail:  err.Error(),
		})
		return
	}
	_ = conn.CloseRead(context.Background())

	ctx, wsNetConn := codersdk.WebsocketNetConn(ctx, conn, websocket.MessageText)
	defer wsNetConn.Close()
	ctx = api.wsWatcher.Watch(ctx, logger, conn)

	encoder := json.NewEncoder(wsNetConn)
	if err := encoder.Encode(list); err != nil {
		logger.Debug(ctx, "send chat todo list", slog.Error(err))
		return
	}
	lastSent := list.Version
	for {
		select {
		case <-ctx.Done():
			return
		case <-api.ctx.Done():
			return
		case <-changed:
		}
		list, err := readForkChatTodos(ctx, api.Database, chat.ID)
		if err != nil {
			logger.Error(ctx, "read chat todo list", slog.F("chat_id", chat.ID), slog.Error(err))
			continue
		}
		if list.Version <= lastSent {
			continue
		}
		if err := encoder.Encode(list); err != nil {
			logger.Debug(ctx, "send chat todo list", slog.Error(err))
			return
		}
		lastSent = list.Version
	}
}
