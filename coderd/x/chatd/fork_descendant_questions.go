package chatd

// This file is fork code: it gives descendant chats of the
// fork-nested-subagents experiment a blocking ask_user_question tool and
// makes wait_agent report a child's unanswered question. appendForkTools and
// forkStopAfterTools add them to a turn.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"maps"
	"time"

	"charm.land/fantasy"
	"github.com/google/uuid"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chattool"
)

const (
	askUserQuestionToolName        = "ask_user_question"
	descendantQuestionPollInterval = 2 * time.Second
	descendantQuestionNote         = "The child asked the user a question and waits for the answer, which the user gives in the root chat. " +
		"Call wait_agent again to wait until the child resumes and finishes, or answer it yourself with message_agent."
)

// descendantQuestionsEnabled reports whether chat gets the blocking
// ask_user_question tool: a delegated chat that is not an explore subagent.
func (p *Server) descendantQuestionsEnabled(chat database.Chat) bool {
	return p.nestedSubagentsEnabled() && chat.ParentChatID.Valid && !isExploreSubagentMode(chat.Mode)
}

// forkStopAfterTools returns stop plus ask_user_question when chat asks
// descendant questions, so the call ends the turn.
func (p *Server) forkStopAfterTools(stop map[string]struct{}, chat database.Chat) map[string]struct{} {
	if !p.descendantQuestionsEnabled(chat) {
		return stop
	}
	stop = maps.Clone(stop)
	if stop == nil {
		stop = map[string]struct{}{}
	}
	stop[askUserQuestionToolName] = struct{}{}
	return stop
}

// descendantQuestionTool is the blocking ask_user_question tool that also
// records the question, so the parent and the root chat can see it.
type descendantQuestionTool struct {
	fantasy.AgentTool
	server *Server
	chat   database.Chat
}

func (p *Server) newDescendantQuestionTool(chat database.Chat) descendantQuestionTool {
	return descendantQuestionTool{
		AgentTool: chattool.NewAskUserQuestionTool(chattool.AskUserQuestionOptions{}),
		server:    p,
		chat:      chat,
	}
}

func (t descendantQuestionTool) Info() fantasy.ToolInfo {
	info := t.AgentTool.Info()
	info.Description = "Ask the user one or more structured questions. The user sees them in the root chat of this chat's family. " +
		"Your turn ends after the call, and the answer arrives as your next user message. " +
		"Ask only for decisions that depend on the user's context or preference; if one option is clearly best, proceed with it and state the assumption. " +
		"Each question needs a short header, the question itself, and 2-4 options. " +
		"Write each option as one short, complete answer the user could send as their reply."
	return info
}

func (t descendantQuestionTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	resp, err := t.AgentTool.Run(ctx, call)
	if err != nil || resp.IsError {
		return resp, err
	}
	var result struct {
		Questions json.RawMessage `json:"questions"`
	}
	if err := json.Unmarshal([]byte(resp.Content), &result); err != nil {
		return fantasy.NewTextErrorResponse("decode questions: " + err.Error()), nil
	}
	if err := t.server.recordDescendantQuestion(ctx, t.chat.ID, call.ID, result.Questions); err != nil {
		t.server.logger.Error(ctx, "record descendant question", slog.F("chat_id", t.chat.ID), slog.Error(err))
		return fantasy.NewTextErrorResponse("failed to record the question; try again"), nil
	}
	return toolJSONResponse(map[string]any{
		"questions": result.Questions,
		"status":    "The questions are shown to the user. Your turn ends now; the answer arrives as your next user message.",
	}), nil
}

func (p *Server) recordDescendantQuestion(ctx context.Context, chatID uuid.UUID, callID string, questions json.RawMessage) error {
	fdb, err := forkdb.From(p.db)
	if err != nil {
		return err
	}
	_, err = fdb.UpsertChatDescendantQuestion(ctx, forkdb.UpsertChatDescendantQuestionParams{
		ChatID:     chatID,
		ToolCallID: callID,
		Questions:  questions,
	})
	return err
}

// pendingDescendantQuestion returns the unanswered question of chatID, or
// nil when there is none.
func (p *Server) pendingDescendantQuestion(ctx context.Context, chatID uuid.UUID) (json.RawMessage, error) {
	question, err := p.pendingDescendantQuestionRow(ctx, chatID)
	if err != nil || question == nil {
		return nil, err
	}
	return question.Questions, nil
}

func (p *Server) pendingDescendantQuestionRow(ctx context.Context, chatID uuid.UUID) (*forkdb.ChatDescendantQuestion, error) {
	fdb, err := forkdb.From(p.db)
	if err != nil {
		return nil, err
	}
	question, err := fdb.GetPendingChatDescendantQuestion(ctx, chatID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &question, nil
}

// descendantQuestionWaitTool is wait_agent that reports needs_input while
// the child has an unanswered question, and that waits for the answer
// before it waits for the child.
type descendantQuestionWaitTool struct {
	fantasy.AgentTool
	server *Server
	chat   database.Chat
}

// withDescendantQuestionWait wraps the wait_agent tool in tools, if there is
// one, with descendantQuestionWaitTool.
func (p *Server) withDescendantQuestionWait(tools []fantasy.AgentTool, chat database.Chat) []fantasy.AgentTool {
	for i, tool := range tools {
		if tool.Info().Name == waitAgentToolName {
			tools[i] = descendantQuestionWaitTool{AgentTool: tool, server: p, chat: chat}
		}
	}
	return tools
}

func (t descendantQuestionWaitTool) Info() fantasy.ToolInfo {
	info := t.AgentTool.Info()
	info.Description += " needs_input true means the child asked the user a question and waits for the answer."
	return info
}

func (t descendantQuestionWaitTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	var args waitAgentArgs
	var input map[string]any
	if json.Unmarshal([]byte(call.Input), &args) != nil || json.Unmarshal([]byte(call.Input), &input) != nil {
		return t.AgentTool.Run(ctx, call)
	}
	childID, err := parseSubagentToolChatID(args.ChatID)
	if err != nil {
		return t.AgentTool.Run(ctx, call)
	}
	if ok, err := t.server.isSubagentTarget(ctx, t.chat.ID, childID); err != nil || !ok {
		return t.AgentTool.Run(ctx, call)
	}
	timeout := defaultSubagentWaitTimeout
	if args.TimeoutSeconds != nil && *args.TimeoutSeconds > 0 {
		timeout = time.Duration(*args.TimeoutSeconds) * time.Second
	}
	deadline := t.server.clock.Now().Add(timeout)

	pending, err := t.server.pendingDescendantQuestionRow(ctx, childID)
	var questions json.RawMessage
	if err == nil && pending != nil {
		questions, err = t.server.awaitDescendantAnswer(ctx, childID, pending.ToolCallID, timeout)
		if err == nil {
			// An unanswered child gets a short wait that collects its status
			// and report; an answered one is waited on for the rest of the
			// timeout, rounded up to whole seconds.
			remaining := 1
			if questions == nil {
				remaining = max(1, int((deadline.Sub(t.server.clock.Now())+time.Second-1)/time.Second))
			}
			input["timeout_seconds"] = remaining
			data, err := json.Marshal(input)
			if err != nil {
				return fantasy.NewTextErrorResponse("encode wait parameters: " + err.Error()), nil
			}
			call.Input = string(data)
		}
	}
	if err != nil {
		return fantasy.NewTextErrorResponse("read the child's question: " + err.Error()), nil
	}

	resp, err := t.AgentTool.Run(ctx, call)
	if err != nil || resp.IsError {
		return resp, err
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(resp.Content), &result); err != nil {
		return resp, nil
	}
	questions, err = t.server.pendingDescendantQuestion(ctx, childID)
	if err != nil {
		return fantasy.NewTextErrorResponse("read the child's question: " + err.Error()), nil
	}
	if questions == nil {
		return resp, nil
	}
	result["needs_input"] = true
	result["questions"] = questions
	result["note"] = descendantQuestionNote
	return toolJSONResponse(result), nil
}

// awaitDescendantAnswer polls until the question toolCallID of childID is
// answered or replaced. It returns the pending question, if any, when it
// stops.
func (p *Server) awaitDescendantAnswer(ctx context.Context, childID uuid.UUID, toolCallID string, timeout time.Duration) (json.RawMessage, error) {
	timer := p.clock.NewTimer(timeout, "chatd", "descendant_question_await")
	defer timer.Stop()
	ticker := p.clock.NewTicker(descendantQuestionPollInterval, "chatd", "descendant_question_poll")
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
		case <-timer.C:
			return p.pendingDescendantQuestion(ctx, childID)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		question, err := p.pendingDescendantQuestionRow(ctx, childID)
		if err != nil || question == nil {
			return nil, err
		}
		if question.ToolCallID != toolCallID {
			return question.Questions, nil
		}
	}
}
