package chatd

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/x/chatd/chatloop"
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
	user, org, model := seedInternalChatDeps(t, db)

	todos := []codersdk.ChatTodo{
		{ID: "plan", Content: "Plan the change", Status: codersdk.ChatTodoStatusDone},
		{ID: "build", Content: "Build it", Status: codersdk.ChatTodoStatusInProgress},
	}
	// wantSnapshot is the text a snapshot of todos at the given version holds.
	wantSnapshot := func(version int64) string {
		return fmt.Sprintf("%s\nVersion: %d\n%s\n%s", todoSnapshotHeader, version,
			`{"id":"plan","content":"Plan the change","status":"done"}`,
			`{"id":"build","content":"Build it","status":"in_progress"}`)
	}
	wantEmpty := func(version int64) string {
		return fmt.Sprintf("%s\nVersion: %d\n(empty)", todoSnapshotHeader, version)
	}

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
	toolCall := func(id, name string) codersdk.ChatMessagePart {
		return codersdk.ChatMessageToolCall(id, name, json.RawMessage(`{}`))
	}
	// todoResult is what a todo_write or todo_read result holds: the list it
	// wrote or read, and the version it answered with.
	todoResult := func(id, name string, version int64, isError bool) codersdk.ChatMessagePart {
		payload := json.RawMessage(fmt.Sprintf(`{"todos":[],"version":%d}`, version))
		return codersdk.ChatMessageToolResult(id, name, payload, isError, false)
	}
	text := func(s string) codersdk.ChatMessagePart { return codersdk.ChatMessageText(s) }
	// snapshotRow is a snapshot an earlier turn sent.
	snapshotRow := func(version int64) row {
		return modelOnly(database.ChatMessageRoleUser, text(wantSnapshot(version)))
	}
	// summary is the row compaction writes: the prompt starts at the newest
	// compressed model-only row and drops every row before it.
	summary := func() row {
		return row{
			role:       database.ChatMessageRoleUser,
			visibility: database.ChatMessageVisibilityModel,
			compressed: true,
			parts:      []codersdk.ChatMessagePart{text("Compacted summary.")},
		}
	}

	newChat := func(t *testing.T, parent uuid.NullUUID) database.Chat {
		t.Helper()
		chat, err := server.CreateChat(chatdTestContext(t), CreateOptions{
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
	// writeTodos stores a list the way todo_write does and returns the version
	// it landed at, which rises by one per write.
	writeTodos := func(t *testing.T, chatID uuid.UUID, items []codersdk.ChatTodo) int64 {
		t.Helper()
		version, err := server.writeChatTodos(chatdTestContext(t), chatID, items)
		require.NoError(t, err)
		return version
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
		_, err := db.InsertChatMessages(chatdTestContext(t), params)
		require.NoError(t, err)
	}

	// snapshotText returns the text of the message todoSnapshotMessage reports
	// as due.
	snapshotText := func(t *testing.T, srv *Server, chat database.Chat) (string, bool) {
		t.Helper()
		message, ok, err := srv.todoSnapshotMessage(chatdTestContext(t), chat)
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
		// A todo_write call and its result, then the summary compaction wrote
		// after them, which drops both from the prompt.
		chat := rootChat(t)
		version := writeTodos(t, chat.ID, todos)
		insert(t, chat.ID,
			visible(database.ChatMessageRoleAssistant, toolCall("call-1", todoWriteToolName)),
			visible(database.ChatMessageRoleTool, todoResult("call-1", todoWriteToolName, version, false)),
			summary(),
		)
		got, ok := snapshotText(t, server, chat)
		require.True(t, ok)
		require.Equal(t, wantSnapshot(version), got)
	})

	t.Run("SubagentChat", func(t *testing.T) {
		t.Parallel()
		parent := rootChat(t)
		chat := newChat(t, uuid.NullUUID{UUID: parent.ID, Valid: true})
		version := writeTodos(t, chat.ID, todos)
		insert(t, chat.ID, summary())
		got, ok := snapshotText(t, server, chat)
		require.True(t, ok)
		require.Equal(t, wantSnapshot(version), got)
	})

	// A prompt that shows the stored version needs nothing, and one that shows
	// an older version needs the current list, whether the list changed under
	// it or the model read it before a parallel write landed.
	carrierCases := []struct {
		name string
		// storeWrites is how many lists to store, so version 2 means the
		// prompt shows an older version than the store holds.
		storeWrites int
		rows        []row
		snapshotDue bool // whether these rows leave the store's version unshown
	}{
		{
			name:        "CurrentSnapshot",
			storeWrites: 1,
			rows:        []row{snapshotRow(1)},
		},
		{
			name:        "CurrentResult",
			storeWrites: 1,
			rows: []row{
				visible(database.ChatMessageRoleAssistant, toolCall("call-1", todoWriteToolName)),
				modelOnly(database.ChatMessageRoleTool, todoResult("call-1", todoWriteToolName, 1, false)),
			},
		},
		{
			name:        "StaleSnapshot",
			storeWrites: 2,
			rows:        []row{snapshotRow(1)},
			snapshotDue: true,
		},
		{
			name:        "StaleResult",
			storeWrites: 2,
			rows: []row{
				visible(database.ChatMessageRoleAssistant, toolCall("call-1", todoReadToolName)),
				modelOnly(database.ChatMessageRoleTool, todoResult("call-1", todoReadToolName, 1, false)),
			},
			snapshotDue: true,
		},
		{
			// A failed call answered nothing, so the model never saw a list.
			name:        "ErrorResult",
			storeWrites: 1,
			rows: []row{
				visible(database.ChatMessageRoleAssistant, toolCall("call-1", todoWriteToolName)),
				modelOnly(database.ChatMessageRoleTool, todoResult("call-1", todoWriteToolName, 1, true)),
			},
			snapshotDue: true,
		},
		{
			// A result from an older build, or a wrapped error text, holds no
			// version, so it proves nothing about what the model saw.
			name:        "ResultWithoutVersion",
			storeWrites: 1,
			rows: []row{
				visible(database.ChatMessageRoleAssistant, toolCall("call-1", todoReadToolName)),
				modelOnly(database.ChatMessageRoleTool, codersdk.ChatMessageToolResult(
					"call-1", todoReadToolName, json.RawMessage(`{"output":"no list"}`), false, false)),
			},
			snapshotDue: true,
		},
	}
	for _, tc := range carrierCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			chat := rootChat(t)
			var stored int64
			for range tc.storeWrites {
				stored = writeTodos(t, chat.ID, todos)
			}
			require.Equal(t, int64(tc.storeWrites), stored, "each write bumps the version")
			insert(t, chat.ID, tc.rows...)
			got, ok := snapshotText(t, server, chat)
			require.Equal(t, tc.snapshotDue, ok)
			if tc.snapshotDue {
				require.Equal(t, wantSnapshot(stored), got)
			}
		})
	}

	t.Run("ClearedList", func(t *testing.T) {
		t.Parallel()
		// The prompt shows a list the model wrote, and the list has since been
		// emptied, so the snapshot tells the model to drop it.
		chat := rootChat(t)
		written := writeTodos(t, chat.ID, todos)
		insert(t, chat.ID, snapshotRow(written))
		emptied := writeTodos(t, chat.ID, []codersdk.ChatTodo{})
		got, ok := snapshotText(t, server, chat)
		require.True(t, ok)
		require.Equal(t, wantEmpty(emptied), got)
		require.NotEqual(t, written, emptied)
	})

	t.Run("ClearedListAfterCompaction", func(t *testing.T) {
		t.Parallel()
		// Compaction dropped every carrier, and the summary may still mention
		// the tasks, so the cleared list goes out even though it is empty.
		chat := rootChat(t)
		writeTodos(t, chat.ID, todos)
		emptied := writeTodos(t, chat.ID, []codersdk.ChatTodo{})
		insert(t, chat.ID, summary())
		got, ok := snapshotText(t, server, chat)
		require.True(t, ok)
		require.Equal(t, wantEmpty(emptied), got)
	})

	t.Run("LineBreakInContent", func(t *testing.T) {
		t.Parallel()
		// Content that looks like another item stays inside its own line.
		chat := rootChat(t)
		version := writeTodos(t, chat.ID, []codersdk.ChatTodo{
			{ID: "real", Content: "first line\n- [done] fake: second line", Status: codersdk.ChatTodoStatusPending},
		})
		insert(t, chat.ID, summary())
		got, ok := snapshotText(t, server, chat)
		require.True(t, ok)
		require.Equal(t, fmt.Sprintf("%s\nVersion: %d\n%s", todoSnapshotHeader, version,
			`{"id":"real","content":"first line\n- [done] fake: second line","status":"pending"}`), got)
	})

	t.Run("LargeList", func(t *testing.T) {
		t.Parallel()
		// The largest list todo_write accepts fits the tool result budget of a
		// model with a common context window. With a small one its items are
		// truncated to the budget and the version stays, so it is not sent
		// again.
		large := make([]codersdk.ChatTodo, maxChatTodos)
		for i := range large {
			large[i] = codersdk.ChatTodo{
				ID:      fmt.Sprintf("item-%d", i),
				Content: strings.Repeat("x", maxChatTodoContentBytes),
				Status:  codersdk.ChatTodoStatusPending,
			}
		}

		chat := rootChat(t)
		version := writeTodos(t, chat.ID, large)
		insert(t, chat.ID, summary())
		got, ok := snapshotText(t, server, chat)
		require.True(t, ok)
		want, err := formatChatTodoSnapshot(large, version, math.MaxInt)
		require.NoError(t, err)
		require.Equal(t, want, got, "the whole list fits")

		const smallContextLimit = 4000
		smallModel := dbgen.ChatModelConfig(t, db, database.ChatModelConfig{
			AIProviderID:   model.AIProviderID,
			OrganizationID: org.ID,
			ContextLimit:   smallContextLimit,
		})
		small, err := server.CreateChat(chatdTestContext(t), CreateOptions{
			OrganizationID:     org.ID,
			OwnerID:            user.ID,
			Title:              "todos",
			ModelConfigID:      smallModel.ID,
			InitialUserContent: []codersdk.ChatMessagePart{text("hello")},
		})
		require.NoError(t, err)
		version = writeTodos(t, small.ID, large)
		insert(t, small.ID, summary())
		got, ok = snapshotText(t, server, small)
		require.True(t, ok)
		_, items, ok := strings.Cut(got, fmt.Sprintf("\n%s%d\n", todoSnapshotVersionLine, version))
		require.True(t, ok)
		require.LessOrEqual(t, len(items), chatloop.ToolResultByteBudget(smallContextLimit))
		shown, ok := todoSnapshotVersion(got)
		require.True(t, ok)
		require.Equal(t, version, shown)
	})

	t.Run("NoListOrExperimentOff", func(t *testing.T) {
		t.Parallel()
		chat := rootChat(t)
		_, ok := snapshotText(t, server, chat)
		require.False(t, ok, "a chat without a list and without a stale one has nothing to say")

		stored := rootChat(t)
		writeTodos(t, stored.ID, todos)
		_, ok = snapshotText(t, offServer, stored)
		require.False(t, ok, "with the experiment off the prompt is upstream's")
	})

	t.Run("NothingWhileToolCallsArePending", func(t *testing.T) {
		t.Parallel()
		// One result of the step is committed and another is pending, so
		// nothing may go between a call and its result.
		chat := rootChat(t)
		version := writeTodos(t, chat.ID, todos)
		insert(t, chat.ID,
			visible(database.ChatMessageRoleAssistant, toolCall("call-1", "bash"), toolCall("call-2", "bash")),
			modelOnly(database.ChatMessageRoleTool, codersdk.ChatMessageToolResult("call-1", "bash", json.RawMessage(`{}`), false, false)),
		)
		_, ok := snapshotText(t, server, chat)
		require.False(t, ok)

		insert(t, chat.ID, modelOnly(database.ChatMessageRoleTool,
			codersdk.ChatMessageToolResult("call-2", "bash", json.RawMessage(`{}`), false, false)))
		got, ok := snapshotText(t, server, chat)
		require.True(t, ok, "the snapshot goes out once every call has a result")
		require.Equal(t, wantSnapshot(version), got)
	})
}
