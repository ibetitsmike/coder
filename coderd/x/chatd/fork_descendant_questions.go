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
	"slices"
	"strings"
	"time"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"golang.org/x/xerrors"

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
	// descendantSecretQuestionNote leaves out message_agent, which would
	// answer the question without the secret value, and forbids asking for
	// the value in chat, which would put it in model context.
	descendantSecretQuestionNote = "The child asked the user for a secret value and waits for it. " +
		"The user enters it in the question panel above the root chat's input, which keeps it out of every model's context. " +
		"Never ask the user to send the value as a chat message or to give it to you. " +
		"Call wait_agent again to wait until the child resumes and finishes."
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

// secretAllowed reports whether the tool offers secret questions. A
// computer-use chat gets none, because its screenshots reach the model
// without the secret answer scrub.
func (t descendantQuestionTool) secretAllowed() bool {
	return !t.chat.Mode.Valid || t.chat.Mode.ChatMode != database.ChatModeComputerUse
}

func (t descendantQuestionTool) Info() fantasy.ToolInfo {
	info := t.AgentTool.Info()
	info.Description = "Ask the user one or more structured questions. The user sees them in the root chat of this chat's family. " +
		"Your turn ends after the call, and the answer arrives as your next user message. " +
		"Ask only for decisions that depend on the user's context or preference; if one option is clearly best, proceed with it and state the assumption. " +
		"Each question needs a short header, the question itself, and 2-4 options. " +
		"Write each option as one short, complete answer the user could send as their reply."
	if !t.secretAllowed() {
		return info
	}
	info.Description += " To ask for a value you must not see, such as a token or a password, send one question with secret true and no options. " +
		"The answer never reaches you: write " + secretPlaceholder("<this call's ID>") + " in execute, write_file or edit_files input where the value belongs. " +
		"Those tools get the value, and tool output shows the placeholder instead of it."
	questions, _ := info.Parameters["questions"].(map[string]any)
	items, _ := questions["items"].(map[string]any)
	properties, _ := items["properties"].(map[string]any)
	options, _ := properties["options"].(map[string]any)
	if options == nil {
		return info
	}
	properties["secret"] = map[string]any{
		"type":        "boolean",
		"description": "Ask for a secret value. A secret question is the only question of its call and has no options.",
	}
	// Calls are validated against this schema before Run, so only the
	// secret variant may leave out the 2-4 options.
	minOptions := options["minItems"]
	delete(options, "minItems")
	items["required"] = []string{"header", "question"}
	items["anyOf"] = []any{
		map[string]any{
			"required":   []string{"options"},
			"properties": map[string]any{"options": map[string]any{"minItems": minOptions}},
		},
		map[string]any{
			"required": []string{"secret"},
			"properties": map[string]any{
				"secret":  map[string]any{"const": true},
				"options": map[string]any{"maxItems": 0},
			},
		},
	}
	return info
}

// secretQuestionArgs is the input of an ask_user_question call that may hold
// a secret question. The upstream tool would drop the secret flag.
type secretQuestionArgs struct {
	Questions []secretQuestionItem `json:"questions"`
}

type secretQuestionItem struct {
	Header   string            `json:"header"`
	Question string            `json:"question"`
	Options  []json.RawMessage `json:"options"`
	Secret   bool              `json:"secret"`
}

// runSecretQuestion records a call that holds a secret question. handled
// is false when the call holds none, so the upstream tool runs it.
func (t descendantQuestionTool) runSecretQuestion(ctx context.Context, call fantasy.ToolCall) (resp fantasy.ToolResponse, handled bool) {
	var args secretQuestionArgs
	if err := json.Unmarshal([]byte(call.Input), &args); err != nil {
		return fantasy.ToolResponse{}, false
	}
	if !slices.ContainsFunc(args.Questions, func(q secretQuestionItem) bool { return q.Secret }) {
		return fantasy.ToolResponse{}, false
	}
	question := args.Questions[0]
	switch {
	case !t.secretAllowed():
		return fantasy.NewTextErrorResponse("a computer-use chat cannot ask secret questions"), true
	case len(args.Questions) != 1:
		return fantasy.NewTextErrorResponse("a secret question must be the only question of its call"), true
	case strings.TrimSpace(question.Header) == "" || strings.TrimSpace(question.Question) == "":
		return fantasy.NewTextErrorResponse("a secret question needs a header and a question"), true
	case len(question.Options) > 0:
		return fantasy.NewTextErrorResponse("a secret question takes no options"), true
	}
	questions, err := json.Marshal([]map[string]any{{
		"header":   question.Header,
		"question": question.Question,
		"secret":   true,
	}})
	if err != nil {
		return fantasy.NewTextErrorResponse("encode the question: " + err.Error()), true
	}
	err = t.server.recordDescendantQuestion(ctx, t.chat.ID, call.ID, questions, true)
	if errors.Is(err, errSecretQuestionQueued) {
		return fantasy.NewTextErrorResponse(errSecretQuestionQueued.Error()), true
	}
	if err != nil {
		t.server.logger.Error(ctx, "record descendant question", slog.F("chat_id", t.chat.ID), slog.Error(err))
		return fantasy.NewTextErrorResponse("failed to record the question; try again"), true
	}
	return toolJSONResponse(map[string]any{
		"questions": json.RawMessage(questions),
		"status": "The question is shown to the user. Your turn ends now; your next user message says when the user answered. " +
			"Write " + secretPlaceholder(call.ID) + " where the value belongs in execute, write_file or edit_files input.",
	}), true
}

func (t descendantQuestionTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	if resp, handled := t.runSecretQuestion(ctx, call); handled {
		return resp, nil
	}
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
	err = t.server.recordDescendantQuestion(ctx, t.chat.ID, call.ID, result.Questions, false)
	if errors.Is(err, errSecretAnswerQueued) {
		return fantasy.NewTextErrorResponse(errSecretAnswerQueued.Error()), nil
	}
	if err != nil {
		t.server.logger.Error(ctx, "record descendant question", slog.F("chat_id", t.chat.ID), slog.Error(err))
		return fantasy.NewTextErrorResponse("failed to record the question; try again"), nil
	}
	return toolJSONResponse(map[string]any{
		"questions": result.Questions,
		"status":    "The questions are shown to the user. Your turn ends now; the answer arrives as your next user message.",
	}), nil
}

// errSecretQuestionQueued refuses a secret question while messages wait in
// the queue of the chat. The end of the turn would deliver them after the
// question, and the first one would answer it without the value.
var errSecretQuestionQueued = xerrors.New("messages wait in the queue of this chat, and they would answer a secret question without the value; end your turn to read them, then ask again")

// errSecretAnswerQueued refuses a question that would replace a pending
// secret question while the report of its answer waits in the queue of the
// chat. The end of the turn would deliver the report, which would answer the
// new question.
var errSecretAnswerQueued = xerrors.New("the user answered your secret question, and the message that says so waits in the queue of this chat; end your turn to read it, then ask again")

// recordDescendantQuestion replaces the question of chatID. The secret
// answers of earlier questions stay until their TTL, so tool output that
// shows a value a tool persisted, such as a file read back, stays scrubbed.
// It takes the row lock of the chat, which SendMessage holds while it refuses
// messages to a chat with a pending secret question, so no message can reach
// the queue between the check of the queue and the question. Only the report
// of an answer can reach the queue while a secret question is pending, so a
// queued message then refuses any new question.
func (p *Server) recordDescendantQuestion(ctx context.Context, chatID uuid.UUID, callID string, questions json.RawMessage, secret bool) error {
	return forkdb.InTx(p.db, func(tx database.Store, ftx forkdb.Store) error {
		if _, err := tx.GetChatByIDForUpdate(ctx, chatID); err != nil {
			return xerrors.Errorf("lock chat: %w", err)
		}
		queued, err := tx.CountChatQueuedMessages(ctx, chatID)
		if err != nil {
			return xerrors.Errorf("count queued messages: %w", err)
		}
		if queued > 0 && secret {
			return errSecretQuestionQueued
		}
		if queued > 0 {
			pending, err := ftx.GetPendingChatDescendantQuestion(ctx, chatID)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return xerrors.Errorf("get pending question: %w", err)
			}
			if err == nil && pending.Secret() {
				return errSecretAnswerQueued
			}
		}
		_, err = ftx.UpsertChatDescendantQuestion(ctx, forkdb.UpsertChatDescendantQuestionParams{
			ChatID:     chatID,
			ToolCallID: callID,
			Questions:  questions,
		})
		return err
	}, nil)
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
	pending, err = t.server.pendingDescendantQuestionRow(ctx, childID)
	if err != nil {
		return fantasy.NewTextErrorResponse("read the child's question: " + err.Error()), nil
	}
	if pending == nil {
		return resp, nil
	}
	result["needs_input"] = true
	result["questions"] = pending.Questions
	result["note"] = descendantQuestionNote
	if pending.Secret() {
		result["note"] = descendantSecretQuestionNote
	}
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
