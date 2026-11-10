package codersdk

// This file is fork code: it holds the descendant question types and client
// methods of the fork-nested-subagents experiment, so upstream's chats.go
// stays untouched.

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// ChatDescendantQuestion is an unanswered ask_user_question call of a
// descendant chat. The user answers it with a message to that chat.
type ChatDescendantQuestion struct {
	ChatID       uuid.UUID      `json:"chat_id" format:"uuid"`
	ParentChatID uuid.UUID      `json:"parent_chat_id" format:"uuid"`
	ChatTitle    string         `json:"chat_title"`
	ToolCallID   string         `json:"tool_call_id"`
	Questions    []ChatQuestion `json:"questions"`
	CreatedAt    time.Time      `json:"created_at" format:"date-time"`
}

// ChatQuestion is one question of an ask_user_question call.
type ChatQuestion struct {
	Header   string               `json:"header"`
	Question string               `json:"question"`
	Options  []ChatQuestionOption `json:"options"`
}

// ChatQuestionOption is an answer the user can pick for a ChatQuestion.
type ChatQuestionOption struct {
	Label string `json:"label"`
}

// ChatDescendantQuestions lists the unanswered questions of the descendants
// in the family of chatID, oldest first.
func (c *Client) ChatDescendantQuestions(ctx context.Context, chatID uuid.UUID) ([]ChatDescendantQuestion, error) {
	return requestJSON[[]ChatDescendantQuestion](ctx, c, http.MethodGet, fmt.Sprintf("/api/v2/chats/fork/%s/questions", chatID), http.StatusOK, nil)
}
