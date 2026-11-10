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
	// Secret questions take a free-text answer through
	// AnswerChatSecretQuestion, which never writes it to a chat message.
	Secret bool `json:"secret,omitempty"`
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

// ChatSecretAnswerRequest answers a secret question of a descendant chat.
// The tool call ID travels in the body because coderd routes on the
// unescaped path, where an ID with a "/" would not match the route.
type ChatSecretAnswerRequest struct {
	ToolCallID string `json:"tool_call_id"`
	Value      string `json:"value"`
	// Declined answers without a value, for a value the user cannot or
	// will not give as a secret, such as one too short to hide. Value must
	// be empty.
	Declined bool `json:"declined,omitempty"`
}

// AnswerChatSecretQuestion answers the pending secret question req.ToolCallID of
// chatID. The server keeps the value out of chat messages and tells the chat
// only that the user answered or declined.
func (c *Client) AnswerChatSecretQuestion(ctx context.Context, chatID uuid.UUID, req ChatSecretAnswerRequest) error {
	return requestNoContent(ctx, c, http.MethodPost, fmt.Sprintf("/api/v2/chats/fork/%s/questions/secret-answer", chatID), req)
}
