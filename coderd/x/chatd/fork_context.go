package chatd

// This file is fork code: it is the only place fork features add model-only
// context to a turn. Upstream's generation.go calls syncForkContext once, so
// fork features never edit that file further and an upstream sync of it stays
// clean.

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/x/chatd/chatloop"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprompt"
	"github.com/coder/coder/v2/coderd/x/chatd/chatstate"
	"github.com/coder/coder/v2/codersdk"
)

// todoSnapshotHeader starts the model-only message that restores the todo
// list, and is the prefix that identifies one already in the prompt. It says
// nothing about why the list was missing, since a prompt can lack it after
// compaction, after an edit that deleted the tool rows, or because a tool call
// raced the write.
const todoSnapshotHeader = "Your todo list for this chat, read from the store. It may differ from what this conversation shows, so treat this as current."

// todoSnapshotVersionLine starts the line that names the stored version the
// snapshot holds, so the prompt can be compared with the store.
const todoSnapshotVersionLine = "Version: "

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
	} else if ok {
		return s.commitForkContextMessage(ctx, machine, input, message)
	}
	message, ok, err = s.server.pinnedSkillsMessage(ctx, chat, history)
	if err != nil {
		s.server.logger.Warn(ctx, "failed to sync chat pinned skills", slog.F("chat_id", chat.ID), slog.Error(err))
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
// list in the prompt, or ok=false when none is due. The model normally sees the
// list in the todo_write and todo_read results it made itself, and those rows
// leave the prompt at compaction or when an edit deletes them, which would
// leave the model working from a plan it can no longer read. A snapshot names
// the stored version and goes out whenever the version the prompt last showed
// differs, so a list that changed while the model was not looking, or that was
// cleared, reaches it too. Like the memory index, the snapshot is an appended
// message so the cached prompt prefix stays intact.
//
// A subagent chat gets a snapshot too: its todo list is its own, so it loses
// its plan the same way.
func (p *Server) todoSnapshotMessage(ctx context.Context, chat database.Chat) (chatstate.Message, bool, error) {
	if !p.experiments.Enabled(codersdk.ExperimentForkChatTodos) {
		return chatstate.Message{}, false, nil
	}
	todos, version, err := p.readChatTodos(ctx, chat.ID)
	if err != nil {
		return chatstate.Message{}, false, xerrors.Errorf("read todo list: %w", err)
	}
	if version == 0 {
		// No list was ever stored, so the prompt cannot show a stale one.
		return chatstate.Message{}, false, nil
	}
	// Prompt rows include the model-only rows this sends, and exclude rows
	// compaction has replaced.
	promptRows, err := p.db.GetChatMessagesForPromptByChatID(ctx, chat.ID)
	if err != nil {
		return chatstate.Message{}, false, xerrors.Errorf("load prompt messages: %w", err)
	}
	if deferred, err := forkContextDeferred(chat, promptRows); err != nil || deferred {
		return chatstate.Message{}, false, err
	}
	shown, found, err := promptTodoVersion(promptRows)
	if err != nil {
		return chatstate.Message{}, false, err
	}
	if found && shown == version {
		// The prompt already shows this exact list.
		return chatstate.Message{}, false, nil
	}
	// The items get the room a todo result gets with the chat's model. A
	// result over that budget is truncated without its version, which makes a
	// snapshot due, and the snapshot must not bring back the size the
	// truncation removed.
	var contextLimit int64
	if chat.LastModelConfigID != uuid.Nil {
		config, err := p.db.GetChatModelConfigByID(ctx, chat.LastModelConfigID)
		if err != nil {
			return chatstate.Message{}, false, xerrors.Errorf("load model config: %w", err)
		}
		contextLimit = config.ContextLimit
	}
	text, err := formatChatTodoSnapshot(todos, version, chatloop.ToolResultByteBudget(contextLimit))
	if err != nil {
		return chatstate.Message{}, false, err
	}
	message, err := forkContextTextMessage(chat, text)
	if err != nil {
		return chatstate.Message{}, false, xerrors.Errorf("marshal todo snapshot: %w", err)
	}
	return message, true, nil
}

// forkContextDeferred reports whether a fork context message must wait. A
// step can commit some tool results while others, such as client-executed
// dynamic tools, are still pending, and providers reject a message between a
// tool call and its result, so it waits until every call is resolved.
func forkContextDeferred(chat database.Chat, promptRows []database.ChatMessage) (bool, error) {
	if currentTurnStepCount(promptRows) == 0 {
		return false, nil
	}
	if lastActiveMessageRole(promptRows) == database.ChatMessageRoleAssistant {
		return true, nil
	}
	localCalls, dynamicCalls, _, err := unresolvedToolCallsFromHistory(promptRows, dynamicToolNamesFromChat(chat))
	if err != nil {
		return false, xerrors.Errorf("find unresolved tool calls: %w", err)
	}
	return len(localCalls) > 0 || len(dynamicCalls) > 0, nil
}

// forkContextTextMessage returns a model-only user message that holds text.
func forkContextTextMessage(chat database.Chat, text string) (chatstate.Message, error) {
	content, err := chatprompt.MarshalParts([]codersdk.ChatMessagePart{
		codersdk.ChatMessageText(text),
	})
	if err != nil {
		return chatstate.Message{}, err
	}
	return chatstate.Message{
		Role:           database.ChatMessageRoleUser,
		Content:        content,
		Visibility:     database.ChatMessageVisibilityModel,
		ModelConfigID:  uuid.NullUUID{UUID: chat.LastModelConfigID, Valid: chat.LastModelConfigID != uuid.Nil},
		ContentVersion: chatprompt.CurrentContentVersion,
	}, nil
}

// promptTodoVersion returns the version of the todo list the prompt last showed
// the model, and whether it showed one at all. The carriers are a snapshot this
// sends, which names the version under its header, and a todo_write or
// todo_read result, whose JSON holds it. Rows compaction replaced and rows an
// edit deleted are absent from messages, so scanning from the end stops at what
// the model can still see. An error result shows nothing, since the model never
// got a list from it.
func promptTodoVersion(messages []database.ChatMessage) (int64, bool, error) {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Deleted || msg.Compressed {
			continue
		}
		// Only a model-only user row can hold a snapshot, and users cannot
		// author one, so snapshot text cannot be forged from chat input.
		isSnapshotRow := msg.Role == database.ChatMessageRoleUser &&
			msg.Visibility == database.ChatMessageVisibilityModel
		if !isSnapshotRow && msg.Role != database.ChatMessageRoleTool {
			continue
		}
		parts, err := chatprompt.ParseContent(msg)
		if err != nil {
			return 0, false, xerrors.Errorf("parse prompt message: %w", err)
		}
		for j := len(parts) - 1; j >= 0; j-- {
			part := parts[j]
			switch {
			case isSnapshotRow && part.Type == codersdk.ChatMessagePartTypeText:
				if version, ok := todoSnapshotVersion(part.Text); ok {
					return version, true, nil
				}
			case msg.Role == database.ChatMessageRoleTool && !part.IsError &&
				part.Type == codersdk.ChatMessagePartTypeToolResult &&
				(part.ToolName == todoWriteToolName || part.ToolName == todoReadToolName):
				// Both todo tools answer with the version they read or wrote,
				// so a result without one is not an answer to show.
				var result struct {
					Version *int64 `json:"version"`
				}
				payloadErr := json.Unmarshal(part.Result, &result)
				if payloadErr != nil || result.Version == nil {
					continue
				}
				return *result.Version, true, nil
			}
		}
	}
	return 0, false, nil
}

// todoSnapshotVersion reads the version a snapshot records under its header.
func todoSnapshotVersion(text string) (int64, bool) {
	rest, ok := strings.CutPrefix(text, todoSnapshotHeader)
	if !ok {
		return 0, false
	}
	line, _, _ := strings.Cut(strings.TrimPrefix(rest, "\n"), "\n")
	value, ok := strings.CutPrefix(line, todoSnapshotVersionLine)
	if !ok {
		return 0, false
	}
	version, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || version < 0 {
		return 0, false
	}
	return version, true
}

// formatChatTodoSnapshot renders todos and the version they are stored at as
// the snapshot message text, one JSON object per item so a line break inside
// an item cannot read as another item. An empty list says so, so a model
// holding a plan that was cleared drops it instead of finishing a task nobody
// asked for, including one the compaction summary still mentions. maxBytes
// bounds the item lines, which take no more bytes than the items of a todo
// tool result, so they are cut only when that result was cut too.
func formatChatTodoSnapshot(todos []codersdk.ChatTodo, version int64, maxBytes int) (string, error) {
	items := "(empty)"
	if len(todos) > 0 {
		var sb strings.Builder
		for i, todo := range todos {
			item, err := json.Marshal(todo)
			if err != nil {
				return "", xerrors.Errorf("marshal todo item: %w", err)
			}
			if i > 0 {
				sb.WriteString("\n")
			}
			sb.Write(item)
		}
		items, _ = chatloop.TruncateToolResultText(sb.String(), maxBytes)
	}
	return todoSnapshotHeader + "\n" + todoSnapshotVersionLine + strconv.FormatInt(version, 10) + "\n" + items, nil
}
