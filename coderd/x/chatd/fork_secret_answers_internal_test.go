package chatd

// This file is fork code: it tests the secret answer wrapper of the turn's
// tools and the secret question of ask_user_question.

import (
	"context"
	"encoding/json"
	"testing"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database/forkdb"
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

type echoMCPTool struct {
	echoTool
	id uuid.UUID
}

func (t echoMCPTool) MCPServerConfigID() uuid.UUID { return t.id }

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

	var spawnInput, mcpInput string
	mcpID := uuid.New()
	tools := f.server.withSecretAnswers([]fantasy.AgentTool{
		echoTool{name: spawnAgentToolName, input: &spawnInput, output: "echo " + value, err: xerrors.New("failed with " + value)},
		echoMCPTool{echoTool: echoTool{name: "mcp_lookup", input: &mcpInput, output: "{}"}, id: mcpID},
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

	// The root chat asks no descendant questions, so its tools stay as
	// they are.
	rootTools := []fantasy.AgentTool{echoTool{name: "execute", input: new(string)}}
	require.IsType(t, echoTool{}, f.server.withSecretAnswers(rootTools, f.root)[0])
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

	off := f.server.newDescendantQuestionTool(child)
	require.NotContains(t, itemProperties(off.Info()), "secret", "the model cannot ask a secret question until the UI takes one")
	resp, err := off.Run(ctx, fantasy.ToolCall{ID: "call_off", Input: secretInput})
	require.NoError(t, err)
	require.True(t, resp.IsError, "without options the call is an ordinary invalid question")

	on := off
	on.secret = true
	require.Contains(t, itemProperties(on.Info()), "secret")
	for _, input := range []string{
		`{"questions":[{"header":"Token","question":"Paste it.","secret":true,"options":[{"label":"a"},{"label":"b"}]}]}`,
		`{"questions":[{"header":"Token","question":"Paste it.","secret":true},{"header":"DB","question":"Which?","options":[{"label":"a"},{"label":"b"}]}]}`,
	} {
		resp, err := on.Run(ctx, fantasy.ToolCall{ID: "call_bad", Input: input})
		require.NoError(t, err)
		require.True(t, resp.IsError, input)
	}
	resp, err = on.Run(ctx, fantasy.ToolCall{ID: "call_on", Input: secretInput})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	require.Contains(t, resp.Content, "{{secret:call_on}}")
	row, err := f.fdb.GetPendingChatDescendantQuestion(ctx, child.ID)
	require.NoError(t, err)
	require.Equal(t, "call_on", row.ToolCallID)
	require.True(t, row.Secret())
}
