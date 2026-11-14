package chatstate

// This file is fork code: it stores that a descendant question of the
// fork-nested-subagents experiment is answered, in the transaction that
// inserts the user message that answers or retires it. insertMessages calls
// it.

import (
	"encoding/json"
	"strings"

	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
)

// QuestionAnswerPrefix starts the user message that answers an
// ask_user_question call. The tool tells the model to expect it, and only a
// message with it answers a secret question.
const QuestionAnswerPrefix = "Answer to ask_user_question call "

// forkRetireDescendantQuestion marks the pending question of the chat
// answered when messages hold a user message that answers or retires it.
func (tx *Tx) forkRetireDescendantQuestion(messages []Message) error {
	arg := forkdb.RetireChatDescendantQuestionParams{ChatID: tx.chatID}
	for _, m := range messages {
		if m.Role != database.ChatMessageRoleUser {
			continue
		}
		if m.Compressed {
			arg.Compressed = true
		}
		if m.Visibility == database.ChatMessageVisibilityModel {
			continue
		}
		arg.Visible = true
		arg.Answers = append(arg.Answers, questionAnswers(m)...)
	}
	if !arg.Compressed && !arg.Visible {
		return nil
	}
	fdb, err := forkdb.From(tx.store)
	if err != nil {
		return xerrors.Errorf("fork queries: %w", err)
	}
	if _, err := fdb.RetireChatDescendantQuestion(tx.ctx, arg); err != nil {
		return xerrors.Errorf("retire descendant question: %w", err)
	}
	return nil
}

// questionAnswers returns, for each text part of m that starts with
// QuestionAnswerPrefix, the text after the prefix.
func questionAnswers(m Message) []string {
	if !m.Content.Valid {
		return nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	// Content that is not an array of parts holds no answer.
	if json.Unmarshal(m.Content.RawMessage, &parts) != nil {
		return nil
	}
	var answers []string
	for _, part := range parts {
		if part.Type != "text" {
			continue
		}
		if rest, ok := strings.CutPrefix(part.Text, QuestionAnswerPrefix); ok {
			answers = append(answers, rest)
		}
	}
	return answers
}
