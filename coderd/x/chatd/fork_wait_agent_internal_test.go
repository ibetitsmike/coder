package chatd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"cdr.dev/slog/v3/sloggers/slogtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprompt"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
	"github.com/coder/coder/v2/codersdk"
)

func TestWaitAgentIncludeToolCalls(t *testing.T) {
	t.Parallel()

	db, ps := dbtestutil.NewDB(t)
	authzDB := dbauthz.New(db, rbac.NewStrictCachingAuthorizer(prometheus.NewRegistry()), slogtest.Make(t, nil), nil)
	server := newInternalTestServer(t, authzDB, ps, chatprovider.ProviderAPIKeys{},
		withInternalTestServerExperiments(codersdk.Experiments{codersdk.ExperimentForkChatTodos}))
	ctx := chatdTestContext(t)
	user, org, model := seedInternalChatDeps(t, db)
	parent, child := createParentChildChats(ctx, t, server, user, org, model)
	require.NoError(t, WaitUntilIdleForTest(ctx, server))

	insert := func(role database.ChatMessageRole, visibility database.ChatMessageVisibility, parts ...codersdk.ChatMessagePart) {
		content, err := chatprompt.MarshalParts(parts)
		require.NoError(t, err)
		dbgen.ChatMessage(t, db, database.ChatMessage{
			ChatID:        child.ID,
			ModelConfigID: uuid.NullUUID{UUID: model.ID, Valid: true},
			Role:          role,
			Visibility:    visibility,
			Content:       content,
		})
	}
	call := func(id, name, args string) codersdk.ChatMessagePart {
		part := codersdk.ChatMessageToolCall(id, name, json.RawMessage(args))
		part.ProviderMetadata = json.RawMessage(`{"anthropic":{"signature":"secret-signature"}}`)
		return part
	}
	result := func(id, name, result string, isError bool) codersdk.ChatMessagePart {
		return codersdk.ChatMessageToolResult(id, name, json.RawMessage(result), isError, false)
	}
	both, userOnly := database.ChatMessageVisibilityBoth, database.ChatMessageVisibilityUser
	insert(database.ChatMessageRoleAssistant, both, call("c1", "read_file", `{"path":"first.go"}`))
	insert(database.ChatMessageRoleTool, both, result("c1", "read_file", `{"content":"one"}`, false))
	insert(database.ChatMessageRoleAssistant, both,
		call("c2", "execute", `{"command":"make"}`),
		call("c3", "write_file", `{"path":"big.go","content":"`+strings.Repeat("x", 3*maxWaitAgentToolCallBytes)+`"}`))
	insert(database.ChatMessageRoleTool, both, result("c2", "execute", `{"output":"failed"}`, true))
	insert(database.ChatMessageRoleAssistant, userOnly, call("c4", "secret_tool", `{}`))
	setChatStatus(ctx, t, db, child.ID, database.ChatStatusWaiting, "")
	insertAssistantMessage(t, db, child.ID, model.ID, "task complete")

	tools, err := server.subagentTools(ctx, func() database.Chat { return parent }, parent.LastModelConfigID)
	require.NoError(t, err)
	tools, err = server.appendForkTools(ctx, tools, parent, parent.LastModelConfigID)
	require.NoError(t, err)
	info := findToolByName(tools, waitAgentToolName).Info()
	require.Contains(t, info.Parameters, "chat_id")
	require.Contains(t, info.Parameters, "include_tool_calls")
	wait := func(args map[string]any, wantError bool) string {
		return runChatTodoTool(ctx, t, tools, waitAgentToolName, args, wantError)
	}

	raw := wait(map[string]any{"chat_id": child.ID.String(), "include_tool_calls": 2}, false)
	require.NotContains(t, raw, "secret")
	var got struct {
		Report    string              `json:"report"`
		ToolCalls []waitAgentToolCall `json:"tool_calls"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &got))
	require.Equal(t, "task complete", got.Report)
	require.Len(t, got.ToolCalls, 2)
	require.Equal(t, "execute", got.ToolCalls[0].ToolName)
	require.JSONEq(t, `{"command":"make"}`, string(got.ToolCalls[0].Args))
	require.JSONEq(t, `{"output":"failed"}`, string(got.ToolCalls[0].Result))
	require.True(t, got.ToolCalls[0].IsError)
	require.Equal(t, "write_file", got.ToolCalls[1].ToolName)
	require.Nil(t, got.ToolCalls[1].Result)
	var truncated string
	require.NoError(t, json.Unmarshal(got.ToolCalls[1].Args, &truncated))
	require.LessOrEqual(t, len(truncated), maxWaitAgentToolCallBytes)
	require.Contains(t, truncated, "Coder truncated")

	all := wait(map[string]any{"chat_id": child.ID.String(), "include_tool_calls": maxWaitAgentToolCalls}, false)
	require.NoError(t, json.Unmarshal([]byte(all), &got))
	require.Len(t, got.ToolCalls, 3)
	require.Equal(t, "read_file", got.ToolCalls[0].ToolName)
	require.JSONEq(t, `{"content":"one"}`, string(got.ToolCalls[0].Result))

	require.NotContains(t, wait(map[string]any{"chat_id": child.ID.String()}, false), "tool_calls")
	wait(map[string]any{"chat_id": child.ID.String(), "include_tool_calls": maxWaitAgentToolCalls + 1}, true)
	wait(map[string]any{"chat_id": uuid.NewString(), "include_tool_calls": 1}, true)
	wait(map[string]any{"chat_id": child.ID.String(), "include_tool_calls": "2"}, true)
}
