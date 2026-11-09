package chatd

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"cdr.dev/slog/v3/sloggers/slogtest"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	coderdpubsub "github.com/coder/coder/v2/coderd/pubsub"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
)

func TestChatTodoTools(t *testing.T) {
	t.Parallel()

	db, ps := dbtestutil.NewDB(t)
	server := newInternalTestServer(t, db, ps, chatprovider.ProviderAPIKeys{},
		withInternalTestServerExperiments(codersdk.Experiments{codersdk.ExperimentForkChatTodos}))
	ctx := chatdTestContext(t)
	user, org, model := seedInternalChatDeps(t, db)
	chat, err := server.CreateChat(ctx, CreateOptions{OrganizationID: org.ID, OwnerID: user.ID, Title: "todos", ModelConfigID: model.ID, InitialUserContent: []codersdk.ChatMessagePart{codersdk.ChatMessageText("hello")}})
	require.NoError(t, err)
	require.NoError(t, WaitUntilIdleForTest(ctx, server))

	tools := server.appendForkTools(ctx, nil, chat)
	require.Equal(t, []string{"todo_write", "todo_read"}, toolNames(tools))

	events := make(chan coderdpubsub.ChatTodosEvent, 4)
	cancel, err := ps.Subscribe(coderdpubsub.ChatTodosEventChannel(chat.ID), func(_ context.Context, message []byte) {
		var event coderdpubsub.ChatTodosEvent
		if json.Unmarshal(message, &event) == nil {
			events <- event
		}
	})
	require.NoError(t, err)
	t.Cleanup(cancel)

	require.JSONEq(t, `{"todos": [], "version": 0}`, runChatTodoTool(ctx, t, tools, "todo_read", struct{}{}, false))

	write := func(todos ...todoWriteItem) string {
		t.Helper()
		return runChatTodoTool(ctx, t, tools, "todo_write", todoWriteArgs{Todos: todos}, false)
	}
	requireEvent := func(version int64) {
		t.Helper()
		event := testutil.RequireReceive(ctx, t, events)
		require.Equal(t, coderdpubsub.ChatTodosEvent{ChatID: chat.ID, Version: version}, event)
	}

	first := `{"todos": [{"id": "plan", "content": "Plan the change", "status": "in_progress"}], "version": 1}`
	require.JSONEq(t, first, write(todoWriteItem{ID: " plan ", Content: " Plan the change ", Status: "in_progress"}))
	requireEvent(1)
	require.JSONEq(t, first, runChatTodoTool(ctx, t, tools, "todo_read", struct{}{}, false))

	second := `{"todos": [
		{"id": "plan", "content": "Plan the change", "status": "done"},
		{"id": "build", "content": "Build it", "status": "in_progress"}
	], "version": 2}`
	require.JSONEq(t, second, write(
		todoWriteItem{ID: "plan", Content: "Plan the change", Status: "done"},
		todoWriteItem{ID: "build", Content: "Build it", Status: "in_progress"},
	))
	requireEvent(2)

	tooMany := make([]todoWriteItem, maxChatTodos+1)
	for i := range tooMany {
		tooMany[i] = todoWriteItem{ID: "todo-" + strconv.Itoa(i), Content: "one", Status: "pending"}
	}

	for name, args := range map[string]todoWriteArgs{
		"DuplicateID": {Todos: []todoWriteItem{
			{ID: "plan", Content: "one", Status: "pending"},
			{ID: " plan", Content: "two", Status: "pending"},
		}},
		"BadStatus":    {Todos: []todoWriteItem{{ID: "plan", Content: "one", Status: "finished"}}},
		"EmptyID":      {Todos: []todoWriteItem{{ID: " ", Content: "one", Status: "pending"}}},
		"EmptyContent": {Todos: []todoWriteItem{{ID: "plan", Content: "  ", Status: "pending"}}},
		"LongID":       {Todos: []todoWriteItem{{ID: strings.Repeat("i", maxChatTodoIDBytes+1), Content: "one", Status: "pending"}}},
		"LongContent":  {Todos: []todoWriteItem{{ID: "plan", Content: strings.Repeat("c", maxChatTodoContentBytes+1), Status: "pending"}}},
		"TooManyItems": {Todos: tooMany},
	} {
		runChatTodoTool(ctx, t, tools, "todo_write", args, true)
		require.JSONEq(t, second, runChatTodoTool(ctx, t, tools, "todo_read", struct{}{}, false), "%s must not change the stored list", name)
	}
}

func TestForkListAgents(t *testing.T) {
	t.Parallel()

	db, ps := dbtestutil.NewDB(t)
	// The authorizing store proves the child todo lists are readable as the
	// owner of the parent chat.
	authzDB := dbauthz.New(db, rbac.NewStrictCachingAuthorizer(prometheus.NewRegistry()), slogtest.Make(t, nil), nil)
	server := newInternalTestServer(t, authzDB, ps, chatprovider.ProviderAPIKeys{},
		withInternalTestServerExperiments(codersdk.Experiments{codersdk.ExperimentForkChatTodos}))
	ctx := chatdTestContext(t)
	user, org, model := seedInternalChatDeps(t, db)
	parent, err := server.CreateChat(ctx, CreateOptions{OrganizationID: org.ID, OwnerID: user.ID, Title: "parent", ModelConfigID: model.ID, InitialUserContent: []codersdk.ChatMessagePart{codersdk.ChatMessageText("hello")}})
	require.NoError(t, err)
	child, err := server.CreateChat(ctx, CreateOptions{OrganizationID: org.ID, OwnerID: user.ID, ParentChatID: uuid.NullUUID{UUID: parent.ID, Valid: true}, RootChatID: uuid.NullUUID{UUID: parent.ID, Valid: true}, Title: "child", ModelConfigID: model.ID, InitialUserContent: []codersdk.ChatMessagePart{codersdk.ChatMessageText("work")}})
	require.NoError(t, err)
	require.NoError(t, WaitUntilIdleForTest(ctx, server))

	runChatTodoTool(ctx, t, server.appendForkTools(ctx, nil, child), "todo_write", todoWriteArgs{Todos: []todoWriteItem{
		{ID: "explore", Content: "Read the code", Status: "in_progress"},
	}}, false)

	resp := runSubagentTool(ctx, t, server, parent, parent.LastModelConfigID, "list_agents", listAgentsArgs{})
	require.False(t, resp.IsError, resp.Content)
	var on forkListAgentsResult
	require.NoError(t, json.Unmarshal([]byte(resp.Content), &on))
	require.Len(t, on.Agents, 1)
	require.Equal(t, child.ID.String(), on.Agents[0].ChatID)
	require.Equal(t, []codersdk.ChatTodo{{ID: "explore", Content: "Read the code", Status: codersdk.ChatTodoStatusInProgress}}, on.Agents[0].Todos)

	// With the experiment off, even with every upstream experiment on, the
	// output is upstream's.
	upstream := slices.DeleteFunc(slices.Clone(codersdk.ExperimentsKnown), func(e codersdk.Experiment) bool {
		return slices.Contains(codersdk.ForkExperiments, e)
	})
	offServer := newInternalTestServer(t, authzDB, ps, chatprovider.ProviderAPIKeys{}, withInternalTestServerExperiments(upstream))
	resp = runSubagentTool(ctx, t, offServer, parent, parent.LastModelConfigID, "list_agents", listAgentsArgs{})
	require.False(t, resp.IsError, resp.Content)
	var off struct {
		Agents []map[string]json.RawMessage `json:"agents"`
	}
	require.NoError(t, json.Unmarshal([]byte(resp.Content), &off))
	require.Len(t, off.Agents, 1)
	require.NotContains(t, off.Agents[0], "todos")
}

func runChatTodoTool(ctx context.Context, t *testing.T, tools []fantasy.AgentTool, name string, args any, wantError bool) string {
	t.Helper()

	tool := findToolByName(tools, name)
	require.NotNil(t, tool, "%s tool must be present", name)
	input, err := json.Marshal(args)
	require.NoError(t, err)
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: uuid.NewString(), Name: name, Input: string(input)})
	require.NoError(t, err)
	require.Equal(t, wantError, resp.IsError, resp.Content)
	return resp.Content
}

func toolNames(tools []fantasy.AgentTool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Info().Name)
	}
	return names
}
