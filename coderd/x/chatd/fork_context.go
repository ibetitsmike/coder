package chatd

// This file is fork code: it is the only place fork features add model-only
// context to a turn. Upstream's generation.go calls syncForkContext once, so
// fork features never edit that file further and an upstream sync of it stays
// clean.

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprompt"
	"github.com/coder/coder/v2/coderd/x/chatd/chatstate"
	"github.com/coder/coder/v2/codersdk"
)

// todoSnapshotHeader starts the model-only message that restores the todo
// list, so the prompt can tell the list is already in the conversation.
const todoSnapshotHeader = "Your todo list for this chat, restored after earlier messages were compacted. Keep it current with todo_write."

// syncForkContext commits the model-only messages the fork features want in
// the prompt before this step, if any, and returns the reloaded chat when it
// committed one, which restarts the step so it reads the new history version.
// Fork context is optional: a lookup failure is logged and the turn continues
// without it.
func (s *taskStarter) syncForkContext(
	ctx context.Context,
	machine *chatstate.ChatMachine,
	input chatWorkerTaskStartInput,
	chat database.Chat,
	history []database.ChatMessage,
) (sessionStartResult, error) {
	synced, err := s.syncMemoryIndex(ctx, machine, input, chat, history)
	if err != nil || synced.Chat.ID != uuid.Nil {
		return synced, err
	}
	message, ok, err := s.server.todoSnapshotMessage(ctx, chat)
	if err != nil {
		s.server.logger.Warn(ctx, "failed to sync chat todo snapshot", slog.F("chat_id", chat.ID), slog.Error(err))
		return sessionStartResult{}, nil
	}
	if !ok {
		return sessionStartResult{}, nil
	}
	return s.commitForkContextMessage(ctx, machine, input, message)
}

// commitForkContextMessage stores one model-only message in its own
// transition, the way the memory index does, so a competing change to the chat
// rolls it back instead of storing it against stale state.
func (s *taskStarter) commitForkContextMessage(
	ctx context.Context,
	machine *chatstate.ChatMachine,
	input chatWorkerTaskStartInput,
	message chatstate.Message,
) (sessionStartResult, error) {
	var applied sessionStartResult
	err := machine.Update(ctx, func(tx *chatstate.Tx, store database.Store) error {
		if _, err := loadChatForGeneration(ctx, store, input, generationAttemptNotRequired); err != nil {
			return xerrors.Errorf("load chat for fork context message: %w", err)
		}
		if _, err := tx.CommitStep(chatstate.CommitStepInput{Messages: []chatstate.Message{message}}); err != nil {
			return xerrors.Errorf("insert fork context message: %w", err)
		}
		reloaded, err := store.GetChatByID(ctx, input.ChatID)
		if err != nil {
			return xerrors.Errorf("reload chat after fork context message: %w", err)
		}
		applied.Chat = reloaded
		return nil
	})
	if err != nil {
		return sessionStartResult{}, normalizeTaskTransitionError(err, "apply fork context message")
	}
	return applied, nil
}

// todoSnapshotMessage returns the model-only message that puts the chat's todo
// list back into the prompt, or ok=false when none is due. The model normally
// sees the list in the todo_write and todo_read rows it wrote itself, and
// compaction replaces those rows, which would leave the model working without
// the plan it made. Like the memory index, the snapshot is an appended message
// so the cached prompt prefix stays intact.
//
// A subagent chat gets a snapshot too: its todo list is its own, so compaction
// loses its plan the same way.
func (p *Server) todoSnapshotMessage(ctx context.Context, chat database.Chat) (chatstate.Message, bool, error) {
	if !p.experiments.Enabled(codersdk.ExperimentForkChatTodos) {
		return chatstate.Message{}, false, nil
	}
	todos, _, err := p.readChatTodos(ctx, chat.ID)
	if err != nil {
		return chatstate.Message{}, false, xerrors.Errorf("read todo list: %w", err)
	}
	if len(todos) == 0 {
		return chatstate.Message{}, false, nil
	}
	// Prompt rows include the model-only rows this sends, and exclude rows
	// compaction has replaced.
	promptRows, err := p.db.GetChatMessagesForPromptByChatID(ctx, chat.ID)
	if err != nil {
		return chatstate.Message{}, false, xerrors.Errorf("load prompt messages: %w", err)
	}
	if currentTurnStepCount(promptRows) > 0 {
		if lastActiveMessageRole(promptRows) == database.ChatMessageRoleAssistant {
			return chatstate.Message{}, false, nil
		}
		// A step can commit some tool results while others, such as
		// client-executed dynamic tools, are still pending. Providers reject a
		// message between a tool call and its result, so wait until every call
		// is resolved.
		localCalls, dynamicCalls, _, err := unresolvedToolCallsFromHistory(promptRows, dynamicToolNamesFromChat(chat))
		if err != nil {
			return chatstate.Message{}, false, xerrors.Errorf("find unresolved tool calls: %w", err)
		}
		if len(localCalls) > 0 || len(dynamicCalls) > 0 {
			return chatstate.Message{}, false, nil
		}
	}
	carried, err := promptCarriesChatTodos(promptRows)
	if err != nil {
		return chatstate.Message{}, false, err
	}
	if carried {
		return chatstate.Message{}, false, nil
	}
	content, err := chatprompt.MarshalParts([]codersdk.ChatMessagePart{
		codersdk.ChatMessageText(formatChatTodoSnapshot(todos)),
	})
	if err != nil {
		return chatstate.Message{}, false, xerrors.Errorf("marshal todo snapshot: %w", err)
	}
	return chatstate.Message{
		Role:           database.ChatMessageRoleUser,
		Content:        content,
		Visibility:     database.ChatMessageVisibilityModel,
		ModelConfigID:  uuid.NullUUID{UUID: chat.LastModelConfigID, Valid: chat.LastModelConfigID != uuid.Nil},
		ContentVersion: chatprompt.CurrentContentVersion,
	}, true, nil
}

// promptCarriesChatTodos reports whether the prompt already shows the todo
// list, either as a snapshot this sends or as a todo_write or todo_read call
// the model made itself, whose result holds the list.
func promptCarriesChatTodos(messages []database.ChatMessage) (bool, error) {
	// Users cannot author model-only rows, so snapshot text cannot be forged
	// from chat input.
	for _, text := range memoryIndexTexts(messages) {
		if strings.HasPrefix(text, todoSnapshotHeader) {
			return true, nil
		}
	}
	for _, msg := range messages {
		if msg.Deleted || msg.Compressed || msg.Role != database.ChatMessageRoleAssistant {
			continue
		}
		parts, err := chatprompt.ParseContent(msg)
		if err != nil {
			return false, xerrors.Errorf("parse assistant message: %w", err)
		}
		for _, part := range parts {
			if part.Type != codersdk.ChatMessagePartTypeToolCall {
				continue
			}
			if part.ToolName == todoWriteToolName || part.ToolName == todoReadToolName {
				return true, nil
			}
		}
	}
	return false, nil
}

// formatChatTodoSnapshot renders todos as the snapshot message text.
func formatChatTodoSnapshot(todos []codersdk.ChatTodo) string {
	var sb strings.Builder
	sb.WriteString(todoSnapshotHeader)
	for _, todo := range todos {
		sb.WriteString("\n- [")
		sb.WriteString(string(todo.Status))
		sb.WriteString("] ")
		sb.WriteString(todo.ID)
		sb.WriteString(": ")
		sb.WriteString(todo.Content)
	}
	return sb.String()
}
