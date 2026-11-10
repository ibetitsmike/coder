package chatd

// This file is fork code: it lets a descendant chat of the
// fork-nested-subagents experiment use the secret answers the user gave to
// its ask_user_question calls without the model ever seeing them.
// withSecretAnswers wraps the turn's tools.

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"charm.land/fantasy"
	"github.com/google/uuid"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chattool"
	"github.com/coder/coder/v2/coderd/x/chatd/mcpclient"
)

// secretInputTools get the value in place of its placeholder. Tools that
// pass their input to another model, such as spawn_agent and message_agent,
// keep the placeholder.
var secretInputTools = []string{chattool.ExecuteToolName, chattool.WriteFileToolName, chattool.EditFilesToolName}

// secretPlaceholder is the text the model writes where a secret answer
// belongs, and the text that replaces the value in tool output.
func secretPlaceholder(toolCallID string) string {
	return "{{secret:" + toolCallID + "}}"
}

// withSecretAnswers wraps every tool of a chat that asks descendant
// questions, so the tool input gets the chat's secret answers and no tool
// output returns them to the model.
func (p *Server) withSecretAnswers(tools []fantasy.AgentTool, chat database.Chat) []fantasy.AgentTool {
	if !p.descendantQuestionsEnabled(chat) {
		return tools
	}
	for i, tool := range tools {
		wrapped := secretAnswerTool{
			AgentTool:  tool,
			server:     p,
			chatID:     chat.ID,
			substitute: slices.Contains(secretInputTools, tool.Info().Name),
		}
		// Turn setup reads the MCP server of a tool through this interface.
		if mcp, ok := tool.(mcpclient.MCPToolIdentifier); ok {
			tools[i] = secretAnswerMCPTool{secretAnswerTool: wrapped, mcp: mcp}
			continue
		}
		tools[i] = wrapped
	}
	return tools
}

type secretAnswerTool struct {
	fantasy.AgentTool
	server     *Server
	chatID     uuid.UUID
	substitute bool
}

type secretAnswerMCPTool struct {
	secretAnswerTool
	mcp mcpclient.MCPToolIdentifier
}

func (t secretAnswerMCPTool) MCPServerConfigID() uuid.UUID {
	return t.mcp.MCPServerConfigID()
}

func (t secretAnswerTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	answers, err := t.server.secretAnswers(ctx, t.chatID)
	if err != nil {
		t.server.logger.Error(ctx, "read secret answers", slog.F("chat_id", t.chatID), slog.Error(err))
		return fantasy.NewTextErrorResponse("failed to read the secret answers of this chat; try again"), nil
	}
	if len(answers) == 0 {
		return t.AgentTool.Run(ctx, call)
	}
	if t.substitute {
		call.Input = substituteSecretAnswers(call.Input, answers)
	}
	resp, err := t.AgentTool.Run(ctx, call)
	resp.Content = scrubSecretAnswers(resp.Content, answers)
	resp.Metadata = scrubSecretAnswers(resp.Metadata, answers)
	if err != nil {
		if scrubbed := scrubSecretAnswers(err.Error(), answers); scrubbed != err.Error() {
			err = errors.New(scrubbed)
		}
	}
	return resp, err
}

func (p *Server) secretAnswers(ctx context.Context, chatID uuid.UUID) ([]forkdb.ChatSecretAnswer, error) {
	fdb, err := forkdb.From(p.db)
	if err != nil {
		return nil, err
	}
	return fdb.GetChatSecretAnswersByChatID(ctx, chatID)
}

// substituteSecretAnswers replaces each placeholder in input, a JSON
// object, with its value escaped for a JSON string, so the input stays
// valid JSON.
func substituteSecretAnswers(input string, answers []forkdb.ChatSecretAnswer) string {
	for _, answer := range answers {
		input = strings.ReplaceAll(input, secretPlaceholder(answer.ToolCallID), jsonStringContent(answer.Value))
	}
	return input
}

// scrubSecretAnswers replaces each value in text, as is and as escaped in a
// JSON string, with its placeholder. Longer values go first, so a value that
// contains another one is replaced whole.
func scrubSecretAnswers(text string, answers []forkdb.ChatSecretAnswer) string {
	if text == "" {
		return text
	}
	answers = slices.Clone(answers)
	slices.SortFunc(answers, func(a, b forkdb.ChatSecretAnswer) int {
		return len(b.Value) - len(a.Value)
	})
	for _, answer := range answers {
		if answer.Value == "" {
			continue
		}
		placeholder := secretPlaceholder(answer.ToolCallID)
		for _, form := range []string{answer.Value, jsonStringContent(answer.Value), jsonStringContentNoHTMLEscape(answer.Value)} {
			text = strings.ReplaceAll(text, form, placeholder)
		}
	}
	return text
}

// jsonStringContent returns value as json.Marshal writes it inside a JSON
// string.
func jsonStringContent(value string) string {
	data, err := json.Marshal(value)
	if err != nil {
		return value
	}
	return string(data[1 : len(data)-1])
}

// jsonStringContentNoHTMLEscape is jsonStringContent for an encoder that
// leaves <, > and & as they are.
func jsonStringContentNoHTMLEscape(value string) string {
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return value
	}
	data := strings.TrimSuffix(buf.String(), "\n")
	return data[1 : len(data)-1]
}
