package chatd

// This file is fork code: it holds the todo_write and todo_read chat tools of
// the fork-chat-todos experiment. appendForkTools adds them to a turn.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	coderdpubsub "github.com/coder/coder/v2/coderd/pubsub"
	"github.com/coder/coder/v2/codersdk"
)

const (
	todoWriteToolName       = "todo_write"
	todoReadToolName        = "todo_read"
	maxChatTodos            = 50
	maxChatTodoIDBytes      = 64
	maxChatTodoContentBytes = 500
)

type todoWriteArgs struct {
	Todos []todoWriteItem `json:"todos" description:"The whole todo list in order. It replaces the stored list; an empty list clears it."`
}

type todoWriteItem struct {
	ID      string `json:"id" description:"A short identifier, unique in the list, that stays the same while the item exists."`
	Content string `json:"content" description:"What the item is about, in one line."`
	Status  string `json:"status" enum:"pending,in_progress,done,cancelled"`
}

type chatTodosResult struct {
	Todos   []codersdk.ChatTodo `json:"todos"`
	Version int64               `json:"version"`
}

// chatTodoTools returns the todo_write and todo_read tools, which edit and
// read the todo list of chat.
func (p *Server) chatTodoTools(chat database.Chat) []fantasy.AgentTool {
	return []fantasy.AgentTool{
		fantasy.NewAgentTool(
			todoWriteToolName,
			"Replace the todo list of this chat. Use it to plan multi-step "+
				"work and to track progress. Send the full list every time, "+
				"since items you leave out are removed. Keep exactly one item "+
				"in_progress while you work, and mark an item done as soon as "+
				"it is finished. Returns the stored list and its version.",
			func(ctx context.Context, args todoWriteArgs, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
				todos, err := validateChatTodos(args.Todos)
				if err != nil {
					return fantasy.NewTextErrorResponse(err.Error()), nil
				}
				version, err := p.writeChatTodos(ctx, chat.ID, todos)
				if err != nil {
					return fantasy.NewTextErrorResponse(err.Error()), nil
				}
				return toolJSONResponse(chatTodosResult{Todos: todos, Version: version}), nil
			},
		),
		fantasy.NewAgentTool(
			todoReadToolName,
			"Read the todo list of this chat and its version. The version is "+
				"0 when the chat has no list yet.",
			func(ctx context.Context, _ struct{}, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
				todos, version, err := p.readChatTodos(ctx, chat.ID)
				if err != nil {
					return fantasy.NewTextErrorResponse(err.Error()), nil
				}
				return toolJSONResponse(chatTodosResult{Todos: todos, Version: version}), nil
			},
		),
	}
}

// validateChatTodos checks a todo_write list and returns it with trimmed IDs
// and contents.
func validateChatTodos(items []todoWriteItem) ([]codersdk.ChatTodo, error) {
	if len(items) > maxChatTodos {
		return nil, xerrors.Errorf("a todo list holds at most %d items, got %d", maxChatTodos, len(items))
	}
	todos := make([]codersdk.ChatTodo, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for i, item := range items {
		todo := codersdk.ChatTodo{
			ID:      strings.TrimSpace(item.ID),
			Content: strings.TrimSpace(item.Content),
			Status:  codersdk.ChatTodoStatus(item.Status),
		}
		switch {
		case todo.ID == "":
			return nil, xerrors.Errorf("todo %d: id is required", i)
		case len(todo.ID) > maxChatTodoIDBytes:
			return nil, xerrors.Errorf("todo %q: id is longer than %d bytes", todo.ID, maxChatTodoIDBytes)
		case todo.Content == "":
			return nil, xerrors.Errorf("todo %q: content is required", todo.ID)
		case len(todo.Content) > maxChatTodoContentBytes:
			return nil, xerrors.Errorf("todo %q: content is longer than %d bytes", todo.ID, maxChatTodoContentBytes)
		case !slices.Contains(codersdk.ChatTodoStatuses, todo.Status):
			return nil, xerrors.Errorf("todo %q: status must be one of %s, got %q", todo.ID, chatTodoStatusList(), item.Status)
		}
		if _, ok := seen[todo.ID]; ok {
			return nil, xerrors.Errorf("todo %q: id appears more than once", todo.ID)
		}
		seen[todo.ID] = struct{}{}
		todos = append(todos, todo)
	}
	return todos, nil
}

func chatTodoStatusList() string {
	names := make([]string, 0, len(codersdk.ChatTodoStatuses))
	for _, status := range codersdk.ChatTodoStatuses {
		names = append(names, string(status))
	}
	return strings.Join(names, ", ")
}

// writeChatTodos stores todos as the todo list of chatID, publishes the new
// version and returns it.
func (p *Server) writeChatTodos(ctx context.Context, chatID uuid.UUID, todos []codersdk.ChatTodo) (int64, error) {
	items, err := json.Marshal(todos)
	if err != nil {
		return 0, xerrors.Errorf("encode todo list: %w", err)
	}
	fdb, err := forkdb.From(p.db)
	if err != nil {
		return 0, xerrors.Errorf("fork queries: %w", err)
	}
	row, err := fdb.UpsertChatTodos(ctx, forkdb.UpsertChatTodosParams{
		ChatID:    chatID,
		Items:     items,
		UpdatedAt: dbtime.Now(),
	})
	if err != nil {
		return 0, xerrors.Errorf("store todo list: %w", err)
	}
	// The list is stored, so a lost event only delays subscribers until
	// their next read; it does not fail the write.
	payload, err := json.Marshal(coderdpubsub.ChatTodosEvent{ChatID: chatID, Version: row.Version})
	if err == nil {
		err = p.pubsub.Publish(coderdpubsub.ChatTodosEventChannel(chatID), payload)
	}
	if err != nil {
		p.logger.Warn(ctx, "publish chat todos event",
			slog.F("chat_id", chatID), slog.F("version", row.Version), slog.Error(err))
	}
	return row.Version, nil
}

// readChatTodos returns the todo list of chatID and its version. A chat
// without a list has an empty list at version 0.
func (p *Server) readChatTodos(ctx context.Context, chatID uuid.UUID) ([]codersdk.ChatTodo, int64, error) {
	fdb, err := forkdb.From(p.db)
	if err != nil {
		return nil, 0, xerrors.Errorf("fork queries: %w", err)
	}
	row, err := fdb.GetChatTodosByChatID(ctx, chatID)
	if errors.Is(err, sql.ErrNoRows) {
		return []codersdk.ChatTodo{}, 0, nil
	}
	if err != nil {
		return nil, 0, xerrors.Errorf("read todo list: %w", err)
	}
	todos := []codersdk.ChatTodo{}
	if err := json.Unmarshal(row.Items, &todos); err != nil {
		return nil, 0, xerrors.Errorf("decode todo list: %w", err)
	}
	return todos, row.Version, nil
}
