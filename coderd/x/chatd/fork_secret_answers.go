package chatd

// This file is fork code: it lets a descendant chat of the
// fork-nested-subagents experiment use the secret answers the user gave to
// its ask_user_question calls without the model ever seeing them.
// withSecretAnswers wraps the turn's tools.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf16"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"golang.org/x/xerrors"

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

// secretMediaWithheldNote replaces the image, document or attachments of a
// tool result while the chat holds secret answers.
const secretMediaWithheldNote = "The media output and attachments of this tool are withheld while this chat holds secret answers."

// secretPlaceholder is the text the model writes where a secret answer
// belongs, and the text that replaces the value in tool output.
func secretPlaceholder(toolCallID string) string {
	return "{{secret:" + toolCallID + "}}"
}

// withSecretAnswers wraps every tool of a chat that asks descendant
// questions and holds secret answers, so the tool input gets the answers
// and no tool output returns them to the model. An answer arrives as the
// user message that starts a turn, so the answers read here hold for the
// whole turn.
func (p *Server) withSecretAnswers(ctx context.Context, tools []fantasy.AgentTool, chat database.Chat) []fantasy.AgentTool {
	if !p.descendantQuestionsEnabled(chat) {
		return tools
	}
	answers, err := p.secretAnswers(ctx, chat.ID)
	if err != nil {
		// Each call then reads the answers itself and fails if it cannot,
		// so no value reaches the model unscrubbed.
		p.logger.Warn(ctx, "read secret answers for turn", slog.F("chat_id", chat.ID), slog.Error(err))
	} else if len(answers) == 0 {
		return tools
	}
	for i, tool := range tools {
		wrapped := secretAnswerTool{
			AgentTool:   tool,
			server:      p,
			chatID:      chat.ID,
			answers:     answers,
			readPerCall: err != nil,
			substitute:  slices.Contains(secretInputTools, tool.Info().Name),
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
	server      *Server
	chatID      uuid.UUID
	answers     []forkdb.ChatSecretAnswer
	readPerCall bool
	substitute  bool
}

// The chat loop finds these optional methods by type assertion, so the
// wrapper forwards them and returns what an unimplemented one means.

func (t secretAnswerTool) SerialToolCalls() bool {
	serial, ok := t.AgentTool.(interface{ SerialToolCalls() bool })
	return ok && serial.SerialToolCalls()
}

func (t secretAnswerTool) HaltedToolCallError() (string, bool) {
	halting, ok := t.AgentTool.(interface{ HaltedToolCallError() (string, bool) })
	if !ok {
		return "", false
	}
	return halting.HaltedToolCallError()
}

func (t secretAnswerTool) ObserveStepToolCalls(names []string) {
	if observer, ok := t.AgentTool.(interface{ ObserveStepToolCalls([]string) }); ok {
		observer.ObserveStepToolCalls(names)
	}
}

func (t secretAnswerTool) ObserveStepToolResults(names []string, errored []bool) {
	if observer, ok := t.AgentTool.(interface{ ObserveStepToolResults([]string, []bool) }); ok {
		observer.ObserveStepToolResults(names, errored)
	}
}

type secretAnswerMCPTool struct {
	secretAnswerTool
	mcp mcpclient.MCPToolIdentifier
}

func (t secretAnswerMCPTool) MCPServerConfigID() uuid.UUID {
	return t.mcp.MCPServerConfigID()
}

func (t secretAnswerTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	answers := t.answers
	if t.readPerCall {
		var err error
		answers, err = t.server.secretAnswers(ctx, t.chatID)
		if err != nil {
			t.server.logger.Error(ctx, "read secret answers", slog.F("chat_id", t.chatID), slog.Error(err))
			return fantasy.NewTextErrorResponse("failed to read the secret answers of this chat; try again"), nil
		}
	}
	if len(answers) == 0 {
		return t.AgentTool.Run(ctx, call)
	}
	switch {
	case t.Info().Name == chattool.ExecuteToolName:
		input, err := substituteExecuteSecretAnswers(call.Input, answers)
		if err != nil {
			return fantasy.NewTextErrorResponse(err.Error()), nil
		}
		call.Input = input
	case t.substitute:
		call.Input = substituteSecretAnswers(call.Input, answers)
	}
	resp, err := t.AgentTool.Run(ctx, call)
	resp.Content = scrubSecretAnswers(resp.Content, answers)
	resp.Metadata = scrubSecretAnswers(resp.Metadata, answers)
	// An image, a document or an attached file can show a value that no
	// scrub finds. The prompt of the next step resolves attachments to
	// their bytes.
	withheld := len(resp.Data) > 0
	if withheld {
		resp.Data = nil
		resp.Type = "text"
		resp.MediaType = ""
	}
	if attachments, _ := chattool.AttachmentsFromMetadata(resp.Metadata); len(attachments) > 0 {
		resp.Metadata = ""
		withheld = true
	}
	if withheld {
		resp.Content = strings.TrimSpace(resp.Content + "\n" + secretMediaWithheldNote)
	}
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

// substituteExecuteSecretAnswers rewrites the command of an execute input
// so the shell never parses a value as code: the command starts with a
// shell-quoted assignment of each value it uses, and each placeholder
// becomes an expansion of that variable. Placeholders in the other
// parameters, such as workdir, get the value as substituteSecretAnswers
// writes it.
func substituteExecuteSecretAnswers(input string, answers []forkdb.ChatSecretAnswer) (string, error) {
	var args map[string]any
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return input, nil
	}
	command, ok := args["command"].(string)
	if !ok {
		return input, nil
	}
	var assignments strings.Builder
	var vars []secretShellVariable
	for i, answer := range answers {
		placeholder := secretPlaceholder(answer.ToolCallID)
		if !strings.Contains(command, placeholder) {
			continue
		}
		if secretArithmeticUnsafe(answer.Value) {
			return "", xerrors.Errorf("the value of %s holds [ and then $ or a backtick, which Bash runs as code where it evaluates a variable as arithmetic, so execute does not take it; "+
				"write it to a file with write_file and read it from there", placeholder)
		}
		name := secretShellVariableName(command, i+1)
		vars = append(vars, secretShellVariable{placeholder: placeholder, name: name})
		_, _ = fmt.Fprintf(&assignments, "%s=%s; ", name, shellQuote(answer.Value))
	}
	if len(vars) == 0 {
		return substituteSecretAnswers(input, answers), nil
	}
	args["command"] = assignments.String() + expandSecretShellVariables(command, vars)
	data, err := json.Marshal(args)
	if err != nil {
		return "", xerrors.Errorf("encode the execute input: %w", err)
	}
	return substituteSecretAnswers(string(data), answers), nil
}

// secretArithmeticUnsafe reports whether Bash can run part of value as code
// when it evaluates a variable that holds value as arithmetic, for example
// in $(( )), (( )), let or [[ -eq ]]. Such an evaluation expands only the
// subscripts of array references in value, so a value is safe unless a $
// or a backtick follows a [.
func secretArithmeticUnsafe(value string) bool {
	i := strings.IndexByte(value, '[')
	return i >= 0 && strings.ContainsAny(value[i:], "$`")
}

// secretShellVariableName returns the variable for the nth answer, with
// underscores added until command does not hold the name, so the command
// cannot assign or unset it.
func secretShellVariableName(command string, n int) string {
	name := fmt.Sprintf("CODER_SECRET_ANSWER_%d", n)
	for strings.Contains(command, name) {
		name += "_"
	}
	return name
}

type secretShellVariable struct {
	placeholder string
	name        string
}

// expandSecretShellVariables replaces each placeholder in command with an
// expansion of its variable that keeps the value one word in the quotes
// around it. The quote scan skips backslash escapes but not command
// substitutions or here-documents. A wrong guess there only splits the
// value or keeps the expansion as text; the shell never runs the value.
func expandSecretShellVariables(command string, vars []secretShellVariable) string {
	var out strings.Builder
	var quote byte
	for i := 0; i < len(command); {
		if v, ok := matchSecretShellVariable(command[i:], vars); ok {
			switch quote {
			case '"':
				out.WriteString("${" + v.name + "}")
			case '\'':
				out.WriteString(`'"${` + v.name + `}"'`)
			default:
				out.WriteString(`"${` + v.name + `}"`)
			}
			i += len(v.placeholder)
			continue
		}
		c := command[i]
		switch {
		case c == '\\' && quote != '\'' && i+1 < len(command):
			out.WriteString(command[i : i+2])
			i += 2
			continue
		case c == quote:
			quote = 0
		case quote == 0 && (c == '\'' || c == '"'):
			quote = c
		}
		out.WriteByte(c)
		i++
	}
	return out.String()
}

func matchSecretShellVariable(text string, vars []secretShellVariable) (secretShellVariable, bool) {
	for _, v := range vars {
		if strings.HasPrefix(text, v.placeholder) {
			return v, true
		}
	}
	return secretShellVariable{}, false
}

// shellQuote returns value as one single-quoted POSIX shell word.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// secretFragmentMinBytes is the shortest prefix or suffix of a value that
// scrubSecretAnswers replaces. Tools that cap their output can cut a value,
// so the whole value no longer matches.
const secretFragmentMinBytes = 8

// scrubSecretAnswers replaces each value in text with its placeholder: as
// is, shell-quoted, and as escaped in a JSON string, then each remaining
// prefix or suffix of a form of at least secretFragmentMinBytes. Longer
// values go first, so a value that contains another one is replaced whole.
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
		var forms []string
		for _, value := range []string{answer.Value, shellQuote(answer.Value)} {
			forms = append(forms, value, jsonStringContent(value), jsonStringContentNoHTMLEscape(value),
				jsonStringContentASCII(value, "\\u%04x"), jsonStringContentASCII(value, "\\u%04X"))
		}
		for _, form := range forms {
			text = strings.ReplaceAll(text, form, placeholder)
		}
		for _, form := range forms {
			text = scrubSecretFragments(text, form, placeholder)
		}
	}
	return text
}

// scrubSecretFragments replaces each run of text that starts like form, and
// then each run that ends like it, for at least secretFragmentMinBytes, with
// placeholder.
func scrubSecretFragments(text, form, placeholder string) string {
	if len(form) < secretFragmentMinBytes {
		return text
	}
	head := form[:secretFragmentMinBytes]
	var out strings.Builder
	for {
		i := strings.Index(text, head)
		if i < 0 {
			out.WriteString(text)
			break
		}
		n := len(head)
		for n < len(form) && i+n < len(text) && text[i+n] == form[n] {
			n++
		}
		out.WriteString(text[:i])
		out.WriteString(placeholder)
		text = text[i+n:]
	}

	text = out.String()
	tail := form[len(form)-secretFragmentMinBytes:]
	out.Reset()
	start := 0
	for {
		j := strings.Index(text[start:], tail)
		if j < 0 {
			out.WriteString(text[start:])
			break
		}
		end := start + j + len(tail)
		n := len(tail)
		for n < len(form) && end-n-1 >= start && text[end-n-1] == form[len(form)-1-n] {
			n++
		}
		out.WriteString(text[start : end-n])
		out.WriteString(placeholder)
		start = end
	}
	return out.String()
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

// jsonStringContentASCII is jsonStringContentNoHTMLEscape for an encoder
// that writes every non-ASCII character as a \u escape in format, such as
// Python's default json.dumps. A character outside the Basic Multilingual
// Plane becomes a UTF-16 surrogate pair.
func jsonStringContentASCII(value, format string) string {
	var out strings.Builder
	for _, r := range jsonStringContentNoHTMLEscape(value) {
		if r < 0x7f {
			out.WriteRune(r)
			continue
		}
		if r1, r2 := utf16.EncodeRune(r); r1 != unicode.ReplacementChar {
			_, _ = fmt.Fprintf(&out, format+format, r1, r2)
			continue
		}
		_, _ = fmt.Fprintf(&out, format, r)
	}
	return out.String()
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
