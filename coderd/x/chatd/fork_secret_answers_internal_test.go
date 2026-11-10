package chatd

// This file is fork code: it tests the secret answer wrapper of the turn's
// tools and the secret question of ask_user_question.

import (
	"context"
	"database/sql"
	"encoding/json"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chattool"
	"github.com/coder/coder/v2/coderd/x/chatd/mcpclient"
	"github.com/coder/coder/v2/codersdk"
)

// echoTool records its input and answers with fixed output.
type echoTool struct {
	fantasy.AgentTool
	name   string
	input  *string
	output string
	err    error
}

func (t echoTool) Info() fantasy.ToolInfo { return fantasy.ToolInfo{Name: t.name} }

func (t echoTool) Run(_ context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	*t.input = call.Input
	return fantasy.NewTextResponse(t.output), t.err
}

// mediaTool answers with an image.
type mediaTool struct {
	fantasy.AgentTool
}

func (mediaTool) Info() fantasy.ToolInfo { return fantasy.ToolInfo{Name: "screenshot"} }

func (mediaTool) Run(context.Context, fantasy.ToolCall) (fantasy.ToolResponse, error) {
	resp := fantasy.NewImageResponse([]byte("png bytes"), "image/png")
	resp.Content = "captured"
	return resp, nil
}

// attachTool answers with text and an attached file.
type attachTool struct {
	fantasy.AgentTool
}

func (attachTool) Info() fantasy.ToolInfo { return fantasy.ToolInfo{Name: "attach_file"} }

func (attachTool) Run(context.Context, fantasy.ToolCall) (fantasy.ToolResponse, error) {
	return chattool.WithAttachments(fantasy.NewTextResponse("attached"), chattool.AttachmentMetadata{
		FileID: uuid.New(), MediaType: "text/plain", Name: "token.txt",
	}), nil
}

type echoMCPTool struct {
	echoTool
	id uuid.UUID
}

func (t echoMCPTool) MCPServerConfigID() uuid.UUID { return t.id }

// stepTool implements the optional methods the chat loop asserts on tools.
type stepTool struct {
	echoTool
	observed *[]string
}

func (stepTool) SerialToolCalls() bool                 { return true }
func (stepTool) HaltedToolCallError() (string, bool)   { return "halted", true }
func (t stepTool) ObserveStepToolCalls(names []string) { *t.observed = names }
func (t stepTool) ObserveStepToolResults(names []string, _ []bool) {
	*t.observed = append(*t.observed, names...)
}

func TestSecretAnswerTools(t *testing.T) {
	t.Parallel()

	const value = "s3cret-value"
	f := newNestedSubagentsFixture(t, codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}, Limits{})
	ctx := chatdTestContext(t)
	child, err := f.spawn(t, f.root, childSubagentChatOptions{})
	require.NoError(t, err)
	require.NoError(t, f.fdb.InsertChatSecretAnswer(ctx, forkdb.InsertChatSecretAnswerParams{
		ChatID: child.ID, ToolCallID: "call_s", Value: value,
	}))

	var spawnInput, mcpInput, executeInput string
	var observed []string
	mcpID := uuid.New()
	tools := f.server.withSecretAnswers(ctx, []fantasy.AgentTool{
		echoTool{name: spawnAgentToolName, input: &spawnInput, output: "echo " + value, err: xerrors.New("failed with " + value)},
		echoMCPTool{echoTool: echoTool{name: "mcp_lookup", input: &mcpInput, output: "{}"}, id: mcpID},
		stepTool{echoTool: echoTool{name: "computer", input: new(string)}, observed: &observed},
		mediaTool{},
		echoTool{name: chattool.ExecuteToolName, input: &executeInput, output: "ok"},
		attachTool{},
	}, child)

	// A tool that passes its input to another model keeps the placeholder,
	// and its output and error never return the value.
	resp, err := tools[0].Run(ctx, fantasy.ToolCall{Input: `{"prompt":"use {{secret:call_s}}"}`})
	require.Equal(t, `{"prompt":"use {{secret:call_s}}"}`, spawnInput)
	require.Equal(t, "echo {{secret:call_s}}", resp.Content)
	require.EqualError(t, err, "failed with {{secret:call_s}}")

	mcp, ok := tools[1].(mcpclient.MCPToolIdentifier)
	require.True(t, ok, "turn setup still finds the MCP server of a wrapped tool")
	require.Equal(t, mcpID, mcp.MCPServerConfigID())

	// The chat loop still finds the optional methods of a wrapped tool.
	step, ok := tools[2].(interface {
		SerialToolCalls() bool
		HaltedToolCallError() (string, bool)
		ObserveStepToolCalls([]string)
		ObserveStepToolResults([]string, []bool)
	})
	require.True(t, ok)
	require.True(t, step.SerialToolCalls())
	text, halts := step.HaltedToolCallError()
	require.True(t, halts)
	require.Equal(t, "halted", text)
	step.ObserveStepToolCalls([]string{"a"})
	step.ObserveStepToolResults([]string{"b"}, []bool{false})
	require.Equal(t, []string{"a", "b"}, observed)
	plain, ok := tools[0].(interface{ SerialToolCalls() bool })
	require.True(t, ok)
	require.False(t, plain.SerialToolCalls(), "a tool without the method keeps its default")

	// An image or a document can show the value, so only its text remains.
	resp, err = tools[3].Run(ctx, fantasy.ToolCall{Input: "{}"})
	require.NoError(t, err)
	require.Empty(t, resp.Data)
	require.Empty(t, resp.MediaType)
	require.Equal(t, "captured\n"+secretMediaWithheldNote, resp.Content)
	resp, err = tools[5].Run(ctx, fantasy.ToolCall{Input: "{}"})
	require.NoError(t, err)
	require.Empty(t, resp.Metadata, "the next prompt would show the attached file to the model")
	require.Equal(t, "attached\n"+secretMediaWithheldNote, resp.Content)

	_, err = tools[4].Run(ctx, fantasy.ToolCall{Input: `{"command":"use {{secret:call_s}}"}`})
	require.NoError(t, err)
	require.Contains(t, executeInput, "CODER_SECRET_ANSWER_1=", "execute gets the value as a shell variable")

	// A chat without secret answers, and the root chat, which asks no
	// descendant questions, keep their tools as they are.
	unanswered, err := f.spawn(t, f.root, childSubagentChatOptions{})
	require.NoError(t, err)
	for _, chat := range []database.Chat{unanswered, f.root} {
		plainTools := []fantasy.AgentTool{echoTool{name: "execute", input: new(string)}}
		require.IsType(t, echoTool{}, f.server.withSecretAnswers(ctx, plainTools, chat)[0])
	}
}

func TestSubstituteSecretAnswers(t *testing.T) {
	t.Parallel()

	answers := []forkdb.ChatSecretAnswer{{ToolCallID: "call_s", Value: `a"b\c`}}
	input := substituteSecretAnswers(`{"command":"echo {{secret:call_s}} {{secret:call_other}}"}`, answers)
	var decoded struct {
		Command string `json:"command"`
	}
	require.NoError(t, json.Unmarshal([]byte(input), &decoded), "the input stays valid JSON")
	require.Equal(t, `echo a"b\c {{secret:call_other}}`, decoded.Command)
}

func TestSubstituteExecuteSecretAnswers(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("execute runs commands under a POSIX shell")
	}
	// Every character here ends or escapes a shell word or runs code when
	// the value is spliced into the command.
	const value = "a'b\"c$(echo INJECTED);d`echo INJECTED`|e&f*\ng\\h"
	answers := []forkdb.ChatSecretAnswer{{ToolCallID: "call_other", Value: "unused-value"}, {ToolCallID: "call_s", Value: value}}
	// The model writes the placeholder bare, in double quotes and in
	// single quotes.
	input, err := substituteExecuteSecretAnswers(
		`{"command":"printf '%s|' {{secret:call_s}} \"[{{secret:call_s}}]\" '<{{secret:call_s}}>' '{{secret:call_unknown}}'","timeout":"5s"}`, answers)
	require.NoError(t, err)
	var decoded struct {
		Command string `json:"command"`
		Timeout string `json:"timeout"`
	}
	require.NoError(t, json.Unmarshal([]byte(input), &decoded))
	require.Equal(t, "5s", decoded.Timeout, "the other parameters stay")
	require.NotContains(t, decoded.Command, "unused-value", "only the values the command uses are assigned")

	//nolint:gosec // The test runs the command the wrapper wrote.
	out, err := exec.CommandContext(chatdTestContext(t), "sh", "-c", decoded.Command).Output()
	require.NoError(t, err, decoded.Command)
	require.Equal(t, value+"|["+value+"]|<"+value+">|{{secret:call_unknown}}|", string(out))
	require.Contains(t, scrubSecretAnswers(decoded.Command, answers), "CODER_SECRET_ANSWER_2={{secret:call_s}}; ",
		"the assignment the wrapper writes is scrubbed")

	// The command cannot change the variable the wrapper assigns.
	input, err = substituteExecuteSecretAnswers(
		`{"command":"CODER_SECRET_ANSWER_2=other; unset CODER_SECRET_ANSWER_2_; printf '%s' {{secret:call_s}}","workdir":"/tmp/{{secret:call_s}}"}`, answers)
	require.NoError(t, err)
	var withWorkdir struct {
		Command string `json:"command"`
		Workdir string `json:"workdir"`
	}
	require.NoError(t, json.Unmarshal([]byte(input), &withWorkdir))
	require.Equal(t, "/tmp/"+value, withWorkdir.Workdir, "a placeholder in another parameter gets the value")
	//nolint:gosec // The test runs the command the wrapper wrote.
	out, err = exec.CommandContext(chatdTestContext(t), "sh", "-c", withWorkdir.Command).Output()
	require.NoError(t, err, withWorkdir.Command)
	require.Equal(t, value, string(out))

	input, err = substituteExecuteSecretAnswers(`{"command":"echo hi","workdir":"{{secret:call_other}}"}`, answers)
	require.NoError(t, err)
	require.Equal(t, `{"command":"echo hi","workdir":"unused-value"}`, input)

	// A value that holds its own placeholder is substituted once.
	self := []forkdb.ChatSecretAnswer{{ToolCallID: "call_s", Value: "{{secret:call_s}}'; echo INJECTED; #"}}
	input, err = substituteExecuteSecretAnswers(`{"command":"printf '%s' {{secret:call_s}}","workdir":"/tmp"}`, self)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(input), &decoded))
	//nolint:gosec // The test runs the command the wrapper wrote.
	out, err = exec.CommandContext(chatdTestContext(t), "sh", "-c", decoded.Command).Output()
	require.NoError(t, err, decoded.Command)
	require.Equal(t, self[0].Value, string(out))

	// Bash runs the subscript of an array reference in a value that it
	// evaluates as arithmetic, as in $(( )).
	arithmetic := []forkdb.ChatSecretAnswer{{ToolCallID: "call_a", Value: "a[$(echo INJECTED)0]"}, {ToolCallID: "call_b", Value: "p[w]`d`"}}
	_, err = substituteExecuteSecretAnswers(`{"command":"echo $(( {{secret:call_a}} ))"}`, arithmetic)
	require.ErrorContains(t, err, "the value of {{secret:call_a}} holds [ and then $ or a backtick")
	_, err = substituteExecuteSecretAnswers(`{"command":"echo {{secret:call_b}}"}`, arithmetic)
	require.Error(t, err)
	require.False(t, secretArithmeticUnsafe("p$w[d]"), "a $ before the subscript is not expanded")

	unchanged := `{"command":"echo hi"}`
	input, err = substituteExecuteSecretAnswers(unchanged, answers)
	require.NoError(t, err)
	require.Equal(t, unchanged, input)
}

func TestScrubSecretAnswers(t *testing.T) {
	t.Parallel()

	answers := []forkdb.ChatSecretAnswer{
		{ToolCallID: "call_short", Value: "abc"},
		{ToolCallID: "call_long", Value: `abc<"d"`},
	}
	require.Equal(t,
		`{{secret:call_long}} {{secret:call_long}} {{secret:call_long}} {{secret:call_short}}`,
		scrubSecretAnswers(`abc<"d" abc\u003c\"d\" abc<\"d\" abc`, answers),
		"every JSON form of the longer value is replaced whole, then the shorter one")

	// A tool that caps its output can cut a value, so a prefix or a suffix
	// of 8 or more bytes is replaced too, and a shorter one is not.
	long := []forkdb.ChatSecretAnswer{{ToolCallID: "call_s", Value: "0123456789abcdef"}}
	require.Equal(t, "line {{secret:call_s}}... [truncated]", scrubSecretAnswers("line 0123456789ab... [truncated]", long))
	require.Equal(t, "[truncated] ...{{secret:call_s}} end", scrubSecretAnswers("[truncated] ...456789abcdef end", long))
	require.Equal(t, "0123456 9abcdef", scrubSecretAnswers("0123456 9abcdef", long))
	quoted := []forkdb.ChatSecretAnswer{{ToolCallID: "call_q", Value: "it's-a-secret"}}
	require.Equal(t, "X={{secret:call_q}}; run", scrubSecretAnswers(`X='it'\''s-a-secret'; run`, quoted),
		"the shell-quoted form is replaced whole")
	unicodeValue := []forkdb.ChatSecretAnswer{{ToolCallID: "call_u", Value: "p\u00e4ss-\U0001F511-word"}}
	require.Equal(t, "{{secret:call_u}} {{secret:call_u}}",
		scrubSecretAnswers(`p\u00e4ss-\ud83d\udd11-word p\u00E4ss-\uD83D\uDD11-word`, unicodeValue),
		"an ASCII-only JSON encoder escapes each non-ASCII character")
	upper := []forkdb.ChatSecretAnswer{{ToolCallID: "call_x", Value: "abc<defg"}, {ToolCallID: "call_y", Value: "abc\x1bdefg-\u00e4"}}
	require.Equal(t, "{{secret:call_x}} {{secret:call_y}} {{secret:call_y}}",
		scrubSecretAnswers(`abc\u003Cdefg abc\u001Bdefg-\u00E4 abc\u001Bdefg-ä`, upper),
		"a JSON encoder can write uppercase hex in the escape of an ASCII character")
	goQuoted := []forkdb.ChatSecretAnswer{{ToolCallID: "call_g", Value: "abc\x01defg-\u00e4"}}
	require.Equal(t, `open "{{secret:call_g}}": "{{secret:call_g}}"`,
		scrubSecretAnswers(`open "abc\x01defg-ä": "abc\x01defg-\u00e4"`, goQuoted),
		"Go's %q and %+q write a control character as a \\x escape")
	slash := []forkdb.ChatSecretAnswer{{ToolCallID: "call_p", Value: "a/bcdefg"}}
	require.Equal(t, `{"p":"{{secret:call_p}}"}`, scrubSecretAnswers(`{"p":"a\/bcdefg"}`, slash),
		"a JSON encoder can escape /")
	control := []forkdb.ChatSecretAnswer{{ToolCallID: "call_c", Value: "abc\x01def\tg'h\\i\x1bj-k\u00e4"}}
	require.Equal(t, `$'{{secret:call_c}}' $'{{secret:call_c}}'`,
		scrubSecretAnswers(`$'abc\001def\tg\'h\\i\Ej-kä' $'abc\001def\tg\'h\\i\Ej-k\303\244'`, control),
		"Bash writes a word with a control character in $'...'")
	shell := []forkdb.ChatSecretAnswer{{ToolCallID: "call_b", Value: "ab cd$efg"}, {ToolCallID: "call_t", Value: "~/x=~y z#"}}
	require.Equal(t, `{{secret:call_b}} x="{{secret:call_b}}" {{secret:call_t}} {{secret:call_t}}`,
		scrubSecretAnswers(`ab\ cd\$efg x="ab cd\$efg" ~/x=~y\ z# \~/x=\~y\ z#`, shell),
		"Bash's printf %q and declare -p escape a shell character with a backslash")
	if bash, err := exec.LookPath("bash"); err == nil {
		for _, answer := range shell {
			//nolint:gosec // The test quotes a fixed value.
			out, err := exec.CommandContext(chatdTestContext(t), bash, "-c", `x=$1; printf '%q\n' "$x"; declare -p x`, "_", answer.Value).CombinedOutput()
			require.NoError(t, err)
			scrubbed := scrubSecretAnswers(string(out), shell)
			require.Equal(t, 2, strings.Count(scrubbed, secretPlaceholder(answer.ToolCallID)), string(out))
		}

		//nolint:gosec // The test traces a fixed command.
		trace, err := exec.CommandContext(chatdTestContext(t), bash, "-c", `set -x; : "$1"`, "_", control[0].Value).CombinedOutput()
		require.NoError(t, err)
		scrubbed := scrubSecretAnswers(string(trace), control)
		require.Contains(t, scrubbed, "{{secret:call_c}}", string(trace))
		require.NotContains(t, scrubbed, "abc", string(trace))
		require.NotContains(t, scrubbed, "def", string(trace))
	}
	likePlaceholder := []forkdb.ChatSecretAnswer{{ToolCallID: "call_s", Value: "{{secret:my-token}}"}}
	require.Equal(t, "x {{secret:call_s}} y", scrubSecretAnswers("x {{secret:my-token}} y", likePlaceholder),
		"a value that starts like its placeholder leaves the placeholder whole")
}

func TestSecretQuestionRefusesSubagentMessage(t *testing.T) {
	t.Parallel()

	f := newNestedSubagentsFixture(t, codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}, Limits{})
	ctx := chatdTestContext(t)
	child, err := f.spawn(t, f.root, childSubagentChatOptions{})
	require.NoError(t, err)
	ask := func(questions string) string {
		callID := uuid.NewString()
		_, err := f.fdb.UpsertChatDescendantQuestion(ctx, forkdb.UpsertChatDescendantQuestionParams{
			ChatID: child.ID, ToolCallID: callID, Questions: json.RawMessage(questions),
		})
		require.NoError(t, err)
		return callID
	}

	// A message would answer the secret question without the value.
	callID := ask(`[{"header":"Token","question":"Paste the token.","secret":true}]`)
	_, err = f.server.sendSubagentMessage(ctx, f.root.ID, child.ID, "the token is x")
	require.ErrorContains(t, err, "it waits for a secret answer")
	// A chat API send checks before SendMessage, so SendMessage checks
	// again under the chat lock, and only the report of the answer passes.
	send := func(ctx context.Context) error {
		_, err := f.server.SendMessage(ctx, SendMessageOptions{
			ChatID: child.ID, CreatedBy: child.OwnerID,
			Content:      []codersdk.ChatMessagePart{codersdk.ChatMessageText("the token is x")},
			BusyBehavior: SendMessageBusyBehaviorQueue,
		})
		return err
	}
	require.ErrorIs(t, send(ctx), ErrSecretQuestionPending)
	messages, err := f.db.GetChatMessagesByChatID(ctx, database.GetChatMessagesByChatIDParams{ChatID: child.ID})
	require.NoError(t, err)
	userMessage := slices.IndexFunc(messages, func(m database.ChatMessage) bool { return m.Role == database.ChatMessageRoleUser })
	require.GreaterOrEqual(t, userMessage, 0)
	_, err = f.server.EditMessage(ctx, EditMessageOptions{
		ChatID: child.ID, CreatedBy: child.OwnerID, EditedMessageID: messages[userMessage].ID,
		Content: []codersdk.ChatMessagePart{codersdk.ChatMessageText("the token is x")},
	})
	require.ErrorIs(t, err, ErrSecretQuestionPending, "an edit would answer the question too")
	// The report passes only for the pending question, with its answer
	// stored.
	require.ErrorIs(t, send(WithSecretAnswerReport(ctx, callID)), ErrSecretAnswerNotPending)
	require.NoError(t, f.fdb.InsertChatSecretAnswer(ctx, forkdb.InsertChatSecretAnswerParams{
		ChatID: child.ID, ToolCallID: callID, Value: "a-secret-value",
	}))
	require.NoError(t, send(WithSecretAnswerReport(ctx, callID)))
	// A late report of the answer must not answer a newer question.
	ask(`[{"header":"Key","question":"Paste the key.","secret":true}]`)
	require.ErrorIs(t, send(WithSecretAnswerReport(ctx, callID)), ErrSecretAnswerNotPending)

	ask(`[{"header":"DB","question":"Which one?","options":[{"label":"a"},{"label":"b"}]}]`)
	_, err = f.server.sendSubagentMessage(ctx, f.root.ID, child.ID, "a")
	require.NoError(t, err, "the parent may answer an ordinary question")
}

func TestDescendantSecretQuestion(t *testing.T) {
	t.Parallel()

	f := newNestedSubagentsFixture(t, codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}, Limits{})
	ctx := chatdTestContext(t)
	child, err := f.spawn(t, f.root, childSubagentChatOptions{})
	require.NoError(t, err)
	const secretInput = `{"questions":[{"header":"Token","question":"Paste the token.","secret":true}]}`
	itemProperties := func(info fantasy.ToolInfo) map[string]any {
		questions, _ := info.Parameters["questions"].(map[string]any)
		items, _ := questions["items"].(map[string]any)
		properties, _ := items["properties"].(map[string]any)
		return properties
	}

	on := f.server.newDescendantQuestionTool(child)
	require.Contains(t, itemProperties(on.Info()), "secret")

	// Screenshots of a computer-use chat reach the model unscrubbed.
	computerUse := on
	computerUse.chat.Mode = database.NullChatMode{ChatMode: database.ChatModeComputerUse, Valid: true}
	require.NotContains(t, itemProperties(computerUse.Info()), "secret")
	resp, err := computerUse.Run(ctx, fantasy.ToolCall{ID: "call_computer", Input: secretInput})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "a computer-use chat cannot ask secret questions")
	for _, input := range []string{
		`{"questions":[{"header":"Token","question":"Paste it.","secret":true,"options":[{"label":"a"},{"label":"b"}]}]}`,
		`{"questions":[{"header":"Token","question":"Paste it.","secret":true},{"header":"DB","question":"Which?","options":[{"label":"a"},{"label":"b"}]}]}`,
	} {
		resp, err := on.Run(ctx, fantasy.ToolCall{ID: "call_bad", Input: input})
		require.NoError(t, err)
		require.True(t, resp.IsError, input)
	}
	// The end of the turn would deliver a queued message after the
	// question, and the message would answer it without the value.
	_, err = f.db.InsertChatQueuedMessage(ctx, database.InsertChatQueuedMessageParams{
		ChatID: child.ID, Content: json.RawMessage(`[{"type":"text","text":"queued"}]`),
	})
	require.NoError(t, err)
	resp, err = on.Run(ctx, fantasy.ToolCall{ID: "call_queued", Input: secretInput})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Equal(t, errSecretQuestionQueued.Error(), resp.Content)
	_, err = f.fdb.GetPendingChatDescendantQuestion(ctx, child.ID)
	require.ErrorIs(t, err, sql.ErrNoRows)
	require.NoError(t, f.db.DeleteAllChatQueuedMessages(ctx, child.ID))

	resp, err = on.Run(ctx, fantasy.ToolCall{ID: "call_on", Input: secretInput})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	require.Contains(t, resp.Content, "{{secret:call_on}}")
	row, err := f.fdb.GetPendingChatDescendantQuestion(ctx, child.ID)
	require.NoError(t, err)
	require.Equal(t, "call_on", row.ToolCallID)
	require.True(t, row.Secret())

	// The end of the turn would deliver the queued report of the answer
	// after an ordinary question of the same step, and the report would
	// answer that question.
	_, err = f.db.InsertChatQueuedMessage(ctx, database.InsertChatQueuedMessageParams{
		ChatID: child.ID, Content: json.RawMessage(`[{"type":"text","text":"Answer to ask_user_question call call_on:\nThe user gave a secret answer."}]`),
	})
	require.NoError(t, err)
	resp, err = on.Run(ctx, fantasy.ToolCall{ID: "call_ordinary", Input: descendantQuestionInput})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Equal(t, errSecretAnswerQueued.Error(), resp.Content)
	row, err = f.fdb.GetPendingChatDescendantQuestion(ctx, child.ID)
	require.NoError(t, err)
	require.Equal(t, "call_on", row.ToolCallID)
}
