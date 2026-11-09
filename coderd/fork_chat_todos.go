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
// @Description list first, then the new list after every write to it.
func (api *API) watchChatTodos(rw http.ResponseWriter, r *http.Request) {
	chat := httpmw.ChatParam(r)
	logger := api.Logger.Named("chat_todos_watcher")

	// Subscribe before accepting the websocket so the subscription is active
	// when the client's Dial returns.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	var (
		encoder      *json.Encoder
		encoderReady = make(chan struct{})
		// Captured before WebsocketNetConn and wsWatcher reassign ctx. The
		// callback reads both only after encoderReady closes, which the
		// handler closes after every value the callback needs is set.
		ctxDone  = ctx.Done()
		readCtx  = ctx
		lastSent int64
	)

	cancelSubscribe, err := api.Pubsub.SubscribeWithErr(pubsub.ChatTodosEventChannel(chat.ID),
		pubsub.HandleChatTodosEvent(func(cbCtx context.Context, payload pubsub.ChatTodosEvent, err error) {
			if err != nil {
				logger.Error(cbCtx, "chat todo list event", slog.F("chat_id", chat.ID), slog.Error(err))
				return
			}
			select {
			case <-encoderReady:
			case <-ctxDone:
				return
			case <-cbCtx.Done():
				return
			}
			// encoderReady may close with encoder still nil on error paths.
			if encoder == nil {
				return
			}
			// The event carries no list, and an older write can be delivered
			// after a newer one, so only a newer version is worth reading.
			if payload.Version <= lastSent {
				return
			}
			list, err := readForkChatTodos(readCtx, api.Database, chat.ID)
			if err != nil {
				logger.Error(cbCtx, "read chat todo list", slog.F("chat_id", chat.ID), slog.Error(err))
				return
			}
			if err := encoder.Encode(list); err != nil {
				logger.Debug(cbCtx, "send chat todo list", slog.Error(err))
				cancel()
				return
			}
			lastSent = list.Version
		}))
	if err != nil {
		close(encoderReady)
		logger.Error(ctx, "subscribe to chat todo list events", slog.F("chat_id", chat.ID), slog.Error(err))
		httpapi.Write(ctx, rw, http.StatusInternalServerError, codersdk.Response{
			Message: "Failed to subscribe to chat todo list events.",
			Detail:  err.Error(),
		})
		return
	}
	defer cancelSubscribe()

	// Read before accepting, so a failed read answers as an HTTP error.
	initial, err := readForkChatTodos(ctx, api.Database, chat.ID)
	if err != nil {
		close(encoderReady)
		logger.Error(ctx, "read chat todo list", slog.F("chat_id", chat.ID), slog.Error(err))
		httpapi.Write(ctx, rw, http.StatusInternalServerError, codersdk.Response{
			Message: "Failed to read the chat todo list.",
			Detail:  err.Error(),
		})
		return
	}

	conn, err := websocket.Accept(rw, r, nil)
	if err != nil {
		close(encoderReady)
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

	// The handler writes the encoder until encoderReady closes, and from then
	// on only the serially delivered pubsub callback writes it.
	encoder = json.NewEncoder(wsNetConn)
	if err := encoder.Encode(initial); err != nil {
		logger.Debug(ctx, "send chat todo list", slog.Error(err))
		close(encoderReady)
		cancel()
		return
	}
	lastSent = initial.Version
	close(encoderReady)

	<-ctx.Done()
}
