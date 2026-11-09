package chatd

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprompt"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
	"github.com/coder/coder/v2/codersdk"
)

func TestTodoSnapshotMessage(t *testing.T) {
	t.Parallel()

	db, ps := dbtestutil.NewDB(t)
	server := newInternalTestServer(t, db, ps, chatprovider.ProviderAPIKeys{},
		withInternalTestServerExperiments(codersdk.Experiments{codersdk.ExperimentForkChatTodos}))
	upstream := slices.DeleteFunc(slices.Clone(codersdk.ExperimentsKnown), func(e codersdk.Experiment) bool {
		return slices.Contains(codersdk.ForkExperiments, e)
	})
	offServer := newInternalTestServer(t, db, ps, chatprovider.ProviderAPIKeys{},
		withInternalTestServerExperiments(upstream))
	ctx := chatdTestContext(t)
	user, org, model := seedInternalChatDeps(t, db)

	todos := []codersdk.ChatTodo{
		{ID: "plan", Content: "Plan the change", Status: codersdk.ChatTodoStatusDone},
		{ID: "build", Content: "Build it", Status: codersdk.ChatTodoStatusInProgress},
	}
	snapshot := todoSnapshotHeader + "\n- [done] plan: Plan the change\n- [in_progress] build: Build it"

	type row struct {
		role       database.ChatMessageRole
		visibility database.ChatMessageVisibility
		compressed bool
		parts      []codersdk.ChatMessagePart
	}
	visible := func(role database.ChatMessageRole, parts ...codersdk.ChatMessagePart) row {
		return row{role: role, visibility: database.ChatMessageVisibilityBoth, parts: parts}
	}
	modelOnly := func(role database.ChatMessageRole, parts ...codersdk.ChatMessagePart) row {
		return row{role: role, visibility: database.ChatMessageVisibilityModel, parts: parts}
	}
	// compacted stands in for a message compaction replaced.
	compacted := func(role database.ChatMessageRole, visibility database.ChatMessageVisibility, parts ...codersdk.ChatMessagePart) row {
		return row{role: role, visibility: visibility, compressed: true, parts: parts}
	}
	toolCall := func(id, name string) codersdk.ChatMessagePart {
		return codersdk.ChatMessageToolCall(id, name, json.RawMessage(`{}`))
	}
	toolResult := func(id, name string) codersdk.ChatMessagePart {
		return codersdk.ChatMessageToolResult(id, name, json.RawMessage(`{}`), false, false)
	}
	text := func(s string) codersdk.ChatMessagePart { return codersdk.ChatMessageText(s) }
	summary := func() row {
		return modelOnly(database.ChatMessageRoleUser, text("Compacted summary."))
	}

	newChat := func(t *testing.T, parent uuid.NullUUID) database.Chat {
		t.Helper()
		chat, err := server.CreateChat(ctx, CreateOptions{
			OrganizationID:     org.ID,
			OwnerID:            user.ID,
			ParentChatID:       parent,
			RootChatID:         parent,
			Title:              "todos",
			ModelConfigID:      model.ID,
			InitialUserContent: []codersdk.ChatMessagePart{text("hello")},
		})
		require.NoError(t, err)
		return chat
	}
	rootChat := func(t *testing.T) database.Chat {
		t.Helper()
		return newChat(t, uuid.NullUUID{})
	}
	// writeTodos stores the todo list, which is what a todo_write call leaves
	// behind.
	writeTodos := func(t *testing.T, chatID uuid.UUID) {
		t.Helper()
		_, err := server.writeChatTodos(ctx, chatID, todos)
		require.NoError(t, err)
	}
	// insert appends messages outside a turn, so a test can hold the prompt
	// the next turn starts from.
	insert := func(t *testing.T, chatID uuid.UUID, rows ...row) {
		t.Helper()
		params := database.InsertChatMessagesParams{ChatID: chatID}
		for _, r := range rows {
			content, err := chatprompt.MarshalParts(r.parts)
			require.NoError(t, err)
			params.CreatedBy = append(params.CreatedBy, user.ID)
			params.ModelConfigID = append(params.ModelConfigID, model.ID)
			params.ReasoningEffort = append(params.ReasoningEffort, "")
			params.Role = append(params.Role, r.role)
			params.Content = append(params.Content, string(content.RawMessage))
			params.ContentVersion = append(params.ContentVersion, chatprompt.CurrentContentVersion)
			params.Visibility = append(params.Visibility, r.visibility)
			params.InputTokens = append(params.InputTokens, 0)
			params.OutputTokens = append(params.OutputTokens, 0)
			params.TotalTokens = append(params.TotalTokens, 0)
			params.ReasoningTokens = append(params.ReasoningTokens, 0)
			params.CacheCreationTokens = append(params.CacheCreationTokens, 0)
			params.CacheReadTokens = append(params.CacheReadTokens, 0)
			params.ContextLimit = append(params.ContextLimit, 0)
			params.Compressed = append(params.Compressed, r.compressed)
			params.RuntimeMs = append(params.RuntimeMs, 0)
		}
		_, err := db.InsertChatMessages(ctx, params)
		require.NoError(t, err)
	}

	// snapshotText returns the text of the message todoSnapshotMessage reports
	// as due.
	snapshotText := func(t *testing.T, srv *Server, chat database.Chat) (string, bool) {
		t.Helper()
		message, ok, err := srv.todoSnapshotMessage(ctx, chat)
		require.NoError(t, err)
		if !ok {
			return "", false
		}
		require.Equal(t, database.ChatMessageRoleUser, message.Role)
		require.Equal(t, database.ChatMessageVisibilityModel, message.Visibility)
		require.Equal(t, chat.LastModelConfigID, message.ModelConfigID.UUID)
		texts := memoryIndexTexts([]database.ChatMessage{{
			Role:           message.Role,
			Visibility:     message.Visibility,
			Content:        message.Content,
			ContentVersion: message.ContentVersion,
		}})
		require.Len(t, texts, 1)
		return texts[0], true
	}

	t.Run("RestoredAfterCompaction", func(t *testing.T) {
		t.Parallel()
		// The todo_write call compaction replaced, the summary that replaced
		// it, and the user prompt that follows.
		chat := rootChat(t)
		writeTodos(t, chat.ID)
		insert(t, chat.ID,
			compacted(database.ChatMessageRoleAssistant, database.ChatMessageVisibilityBoth, toolCall("call-1", todoWriteToolName)),
			summary(),
		)
		got, ok := snapshotText(t, server, chat)
		require.True(t, ok)
		require.Equal(t, snapshot, got)
	})

	t.Run("SubagentChat", func(t *testing.T) {
		t.Parallel()
		parent := rootChat(t)
		chat := newChat(t, uuid.NullUUID{UUID: parent.ID, Valid: true})
		writeTodos(t, chat.ID)
		insert(t, chat.ID, summary())
		got, ok := snapshotText(t, server, chat)
		require.True(t, ok)
		require.Equal(t, snapshot, got)
	})

	t.Run("AlreadyInPrompt", func(t *testing.T) {
		t.Parallel()
		// A todo_write or todo_read call the model made itself, whose result
		// holds the list, and a snapshot an earlier turn sent.
		cases := []struct {
			name string
			rows []row
		}{
			{
				name: "ToolCall",
				rows: []row{
					visible(database.ChatMessageRoleAssistant, toolCall("call-1", todoWriteToolName)),
					modelOnly(database.ChatMessageRoleTool, toolResult("call-1", todoWriteToolName)),
				},
			},
			{
				name: "Snapshot",
				rows: []row{modelOnly(database.ChatMessageRoleUser, text(snapshot))},
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				chat := rootChat(t)
				writeTodos(t, chat.ID)
				insert(t, chat.ID, tc.rows...)
				_, ok := snapshotText(t, server, chat)
				require.False(t, ok)
			})
		}
	})

	t.Run("NoListOrExperimentOff", func(t *testing.T) {
		t.Parallel()
		chat := rootChat(t)
		_, ok := snapshotText(t, server, chat)
		require.False(t, ok, "a chat without a list has nothing to restore")

		stored := rootChat(t)
		writeTodos(t, stored.ID)
		_, ok = snapshotText(t, offServer, stored)
		require.False(t, ok, "with the experiment off the prompt is upstream's")
	})

	t.Run("NothingWhileToolCallsArePending", func(t *testing.T) {
		t.Parallel()
		// One result of the step is committed and another is pending, so
		// nothing may go between a call and its result.
		chat := rootChat(t)
		writeTodos(t, chat.ID)
		insert(t, chat.ID,
			visible(database.ChatMessageRoleAssistant, toolCall("call-1", "bash"), toolCall("call-2", "bash")),
			modelOnly(database.ChatMessageRoleTool, toolResult("call-1", "bash")),
		)
		_, ok := snapshotText(t, server, chat)
		require.False(t, ok)

		insert(t, chat.ID, modelOnly(database.ChatMessageRoleTool, toolResult("call-2", "bash")))
		got, ok := snapshotText(t, server, chat)
		require.True(t, ok, "the snapshot goes out once every call has a result")
		require.Equal(t, snapshot, got)
	})
}
