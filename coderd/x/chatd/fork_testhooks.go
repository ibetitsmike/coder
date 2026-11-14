package chatd

// This file is fork code: it exposes the fork context message builders to the
// chatd_test package, so the prompt-load tests can insert the rows the next
// step would commit and drive the step with nothing due. It adds no behavior.

import (
	"context"

	"github.com/google/uuid"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/x/chatd/chatstate"
	"github.com/coder/coder/v2/codersdk"
)

// TodoSnapshotMessageForTest returns what todoSnapshotMessage reports for chat
// with the given prompt rows, for external tests that build a prompt state.
func (p *Server) TodoSnapshotMessageForTest(ctx context.Context, chat database.Chat, promptRows []database.ChatMessage) (chatstate.Message, bool, error) {
	return p.todoSnapshotMessage(ctx, chat, promptRows)
}

// PinnedSkillsMessageForTest returns what pinnedSkillsMessage reports for chat
// with the given prompt rows, for external tests that build a prompt state.
func (p *Server) PinnedSkillsMessageForTest(ctx context.Context, chat database.Chat, promptRows []database.ChatMessage) (chatstate.Message, bool, error) {
	return p.pinnedSkillsMessage(ctx, chat, promptRows)
}

// WriteChatTodosForTest stores todos as the todo list of chatID, for external
// tests that seed a chat before its first step.
func (p *Server) WriteChatTodosForTest(ctx context.Context, chatID uuid.UUID, todos []codersdk.ChatTodo) error {
	_, err := p.writeChatTodos(ctx, chatID, todos)
	return err
}
