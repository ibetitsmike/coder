package chatd

import (
	"encoding/json"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"cdr.dev/slog/v3/sloggers/slogtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/coderd/x/chatd/chatloop"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprompt"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
	"github.com/coder/coder/v2/coderd/x/chatd/chatstate"
	"github.com/coder/coder/v2/codersdk"
)

func TestChatHistoryTool(t *testing.T) {
	t.Parallel()

	db, ps := dbtestutil.NewDB(t)
	// The authorizing store proves the history queries pass dbauthz as chatd.
	authzDB := dbauthz.New(db, rbac.NewStrictCachingAuthorizer(prometheus.NewRegistry()), slogtest.Make(t, nil), nil)
	server := newInternalTestServer(t, authzDB, ps, chatprovider.ProviderAPIKeys{},
		withInternalTestServerExperiments(codersdk.Experiments{codersdk.ExperimentForkTokenBudget}))
	user, org, model := seedInternalChatDeps(t, db)

	newChat := func(t *testing.T) database.Chat {
		t.Helper()
		return dbgen.Chat(t, db, database.Chat{OrganizationID: org.ID, OwnerID: user.ID, LastModelConfigID: model.ID})
	}
	message := func(role database.ChatMessageRole, visibility database.ChatMessageVisibility, parts ...codersdk.ChatMessagePart) chatstate.Message {
		content, err := chatprompt.MarshalParts(parts)
		if err != nil {
			panic(err)
		}
		return baseMessage(role, visibility, model.ID, chatprompt.CurrentContentVersion, content)
	}
	userText := func(text string) chatstate.Message {
		return message(database.ChatMessageRoleUser, database.ChatMessageVisibilityBoth, codersdk.ChatMessageText(text))
	}
	assistantText := func(text string) chatstate.Message {
		return message(database.ChatMessageRoleAssistant, database.ChatMessageVisibilityBoth, codersdk.ChatMessageText(text))
	}
	compaction := func(t *testing.T) []chatstate.Message {
		t.Helper()
		built, err := buildCompactionMessages(buildCompactionMessagesInput{
			modelConfigID: model.ID,
			toolCallID:    uuid.NewString(),
			compaction:    chatloop.CompactionResult{SystemSummary: "secret-summary-prompt", SummaryReport: "secret-summary-report"},
		})
		require.NoError(t, err)
		return built.Messages
	}
	clearMessages := func(t *testing.T) []chatstate.Message {
		t.Helper()
		messages, err := buildClearMessages(buildClearMessagesInput{modelConfigID: model.ID, toolCallID: uuid.NewString()})
		require.NoError(t, err)
		return messages
	}
	// insert stores messages in order and returns their IDs.
	insert := func(t *testing.T, chatID uuid.UUID, messages ...chatstate.Message) []int64 {
		t.Helper()
		params := database.InsertChatMessagesParams{ChatID: chatID}
		for _, m := range messages {
			params.CreatedBy = append(params.CreatedBy, user.ID)
			params.ModelConfigID = append(params.ModelConfigID, model.ID)
			params.ReasoningEffort = append(params.ReasoningEffort, "")
			params.Role = append(params.Role, m.Role)
			params.Content = append(params.Content, string(m.Content.RawMessage))
			params.ContentVersion = append(params.ContentVersion, m.ContentVersion)
			params.Visibility = append(params.Visibility, m.Visibility)
			params.InputTokens = append(params.InputTokens, 0)
			params.OutputTokens = append(params.OutputTokens, 0)
			params.TotalTokens = append(params.TotalTokens, 0)
			params.ReasoningTokens = append(params.ReasoningTokens, 0)
			params.CacheCreationTokens = append(params.CacheCreationTokens, 0)
			params.CacheReadTokens = append(params.CacheReadTokens, 0)
			params.ContextLimit = append(params.ContextLimit, 0)
			params.Compressed = append(params.Compressed, m.Compressed)
			params.RuntimeMs = append(params.RuntimeMs, 0)
		}
		rows, err := db.InsertChatMessages(chatdTestContext(t), params)
		require.NoError(t, err)
		ids := make([]int64, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		return ids
	}
	// run calls the tool the way a turn does and checks the size cap.
	run := func(t *testing.T, chat database.Chat, args chatHistoryArgs, wantError bool) string {
		t.Helper()
		tools, err := server.appendForkTools(chatdTestContext(t), nil, chat, chat.LastModelConfigID)
		require.NoError(t, err)
		raw := runChatTodoTool(chatdTestContext(t), t, tools, chatHistoryToolName, args, wantError)
		require.LessOrEqual(t, len(raw), maxChatHistoryBytes)
		return raw
	}
	decode := func(t *testing.T, chat database.Chat, args chatHistoryArgs, out any) {
		t.Helper()
		require.NoError(t, json.Unmarshal([]byte(run(t, chat, args, false)), out))
	}
	itemIDs := func(result chatHistoryItemsResult) []int64 {
		ids := make([]int64, 0, len(result.Items))
		for _, item := range result.Items {
			ids = append(ids, item.ID)
		}
		return ids
	}

	t.Run("FindsAndReadsEarlierWindows", func(t *testing.T) {
		t.Parallel()
		chat := newChat(t)
		// The arguments are in the form jsonb stores, so sizes and previews
		// match exactly.
		first := insert(t, chat.ID,
			userText("Where does the Zebra config live?"),
			message(database.ChatMessageRoleAssistant, database.ChatMessageVisibilityBoth,
				codersdk.ChatMessageText("Let me look."),
				codersdk.ChatMessageToolCall("call-1", "execute", json.RawMessage(`{"command": "echo \"quoted\""}`))),
			message(database.ChatMessageRoleTool, database.ChatMessageVisibilityBoth,
				codersdk.ChatMessageToolResult("call-1", "execute", json.RawMessage(`{"output":"zebra.yaml"}`), false, false)),
			message(database.ChatMessageRoleUser, database.ChatMessageVisibilityModel, codersdk.ChatMessageText("secret-model-only")),
		)
		boundary := insert(t, chat.ID, compaction(t)...)
		second := insert(t, chat.ID,
			userText("second question"),
			message(database.ChatMessageRoleAssistant, database.ChatMessageVisibilityBoth,
				codersdk.ChatMessageToolCall("call-2", chatHistoryToolName, json.RawMessage(`{"action":"search","query":"zebra"}`))),
			message(database.ChatMessageRoleTool, database.ChatMessageVisibilityBoth,
				codersdk.ChatMessageToolResult("call-2", chatHistoryToolName, json.RawMessage(`{"matches":"secret-earlier-result"}`), false, false)),
			assistantText("second answer"),
		)

		var windows chatHistoryWindowsResult
		decode(t, chat, chatHistoryArgs{Action: "list_windows"}, &windows)
		require.Len(t, windows.Windows, 2)
		require.Equal(t, "w:1", windows.Windows[0].ID)
		require.Equal(t, "chat_start", windows.Windows[0].StartedBy)
		require.Equal(t, 3, windows.Windows[0].Messages)
		require.Equal(t, first[0], windows.Windows[0].FirstID)
		require.Equal(t, first[2], windows.Windows[0].LastID)
		require.False(t, windows.Windows[0].Current)
		require.Equal(t, "w:2", windows.Windows[1].ID)
		require.Equal(t, "chat_summarized", windows.Windows[1].StartedBy)
		// The chat_history call and result do not count.
		require.Equal(t, 2, windows.Windows[1].Messages)
		require.Equal(t, second[0], windows.Windows[1].FirstID)
		require.Equal(t, second[3], windows.Windows[1].LastID)
		require.True(t, windows.Windows[1].Current)
		require.Zero(t, windows.OmittedWindows)

		// list_items pages through one window and stops at its end.
		var page chatHistoryItemsResult
		decode(t, chat, chatHistoryArgs{Action: "list_items", Window: "w:1", Limit: 2}, &page)
		require.Equal(t, first[:2], itemIDs(page))
		require.True(t, page.HasMore)
		require.Equal(t, "Where does the Zebra config live?", page.Items[0].Preview)
		require.Contains(t, page.Items[1].Preview, `call execute {"command": "echo \"quoted\""}`)
		raw := run(t, chat, chatHistoryArgs{Action: "list_items", Window: "w:1", AfterID: page.NextAfterID}, false)
		require.NotContains(t, raw, "secret-")
		require.NoError(t, json.Unmarshal([]byte(raw), &page))
		require.Equal(t, first[2:3], itemIDs(page))
		require.False(t, page.HasMore)
		raw = run(t, chat, chatHistoryArgs{Action: "list_items", Window: "w:2"}, false)
		require.NotContains(t, raw, "secret-")
		require.NoError(t, json.Unmarshal([]byte(raw), &page))
		require.Equal(t, []int64{second[0], second[3]}, itemIDs(page))

		// search finds a message of the earlier window in any case, and
		// in tool calls, whose arguments are JSON.
		var found chatHistorySearchResult
		decode(t, chat, chatHistoryArgs{Action: "search", Query: "ZEBRA"}, &found)
		require.Len(t, found.Matches, 2)
		require.Equal(t, first[2], found.Matches[0].ID)
		require.Equal(t, first[0], found.Matches[1].ID)
		require.Equal(t, "w:1", found.Matches[1].Window)
		require.Equal(t, 1, found.Matches[1].Part)
		require.Contains(t, found.Matches[1].Snippet, "Zebra config")
		require.False(t, found.HasMore)
		decode(t, chat, chatHistoryArgs{Action: "search", Query: `"quoted"`}, &found)
		require.Len(t, found.Matches, 1)
		require.Equal(t, first[1], found.Matches[0].ID)
		require.Equal(t, 2, found.Matches[0].Part)
		// A search in one window stays in it, and JSON keys, model-only
		// rows, boundaries and history calls never match.
		decode(t, chat, chatHistoryArgs{Action: "search", Query: "zebra", Window: "w:2"}, &found)
		require.Empty(t, found.Matches)
		for _, query := range []string{"tool_name", "secret-model-only", "secret-summary", "secret-earlier-result"} {
			decode(t, chat, chatHistoryArgs{Action: "search", Query: query}, &found)
			require.Empty(t, found.Matches, query)
		}
		// before_id pages to older matches.
		decode(t, chat, chatHistoryArgs{Action: "search", Query: "zebra", BeforeID: first[2]}, &found)
		require.Len(t, found.Matches, 1)
		require.Equal(t, first[0], found.Matches[0].ID)

		// read_item reads a message of the earlier window.
		var read chatHistoryMessageResult
		decode(t, chat, chatHistoryArgs{Action: "read_item", ID: first[1]}, &read)
		require.Equal(t, "w:1", read.Window)
		require.Len(t, read.Parts, 2)
		require.Equal(t, "Let me look.", read.Parts[0].Text)
		require.Equal(t, "execute", read.Parts[1].ToolName)
		require.Equal(t, []int{len("Let me look."), len(`{"command": "echo \"quoted\""}`)}, read.PartBytes)

		require.Contains(t, run(t, chat, chatHistoryArgs{Action: "read_item", ID: boundary[0]}, true), "no readable content")
		require.Contains(t, run(t, chat, chatHistoryArgs{Action: "read_item", ID: first[3]}, true), "no readable content")
		require.Contains(t, run(t, chat, chatHistoryArgs{Action: "read_item", ID: first[1], Part: 3}, true), "has 2 parts")
	})

	t.Run("ReadsLongPartsInSlices", func(t *testing.T) {
		t.Parallel()
		chat := newChat(t)
		// Multi-byte runes and quotes check the slice boundaries and the
		// JSON growth.
		long := strings.Repeat(`aé"`, 15000)
		ids := insert(t, chat.ID, userText(long), assistantText("short"))

		var read chatHistoryMessageResult
		decode(t, chat, chatHistoryArgs{Action: "read_item", ID: ids[0]}, &read)
		require.Equal(t, []int{len(long)}, read.PartBytes)
		require.Contains(t, read.Parts[0].Text, "Coder truncated")

		var text strings.Builder
		offset, calls := 0, 0
		for {
			var part chatHistoryPartResult
			decode(t, chat, chatHistoryArgs{Action: "read_item", ID: ids[0], Part: 1, Offset: offset}, &part)
			require.Equal(t, offset, part.Offset)
			require.Equal(t, len(long), part.TotalBytes)
			_, _ = text.WriteString(part.Text)
			calls++
			if !part.HasMore {
				break
			}
			require.Greater(t, part.NextOffset, offset)
			offset = part.NextOffset
		}
		require.Equal(t, long, text.String())
		require.Greater(t, calls, 3)

		require.Contains(t, run(t, chat, chatHistoryArgs{Action: "read_item", ID: ids[0], Part: 1, Offset: len(long) + 1}, true), "past the end")
	})

	t.Run("StopsAtAClear", func(t *testing.T) {
		t.Parallel()
		chat := newChat(t)
		before := insert(t, chat.ID, userText("zebra before the summary"))
		insert(t, chat.ID, compaction(t)...)
		insert(t, chat.ID, userText("zebra before the clear"))
		insert(t, chat.ID, clearMessages(t)...)
		after := insert(t, chat.ID, userText("zebra after the clear"))

		var windows chatHistoryWindowsResult
		decode(t, chat, chatHistoryArgs{Action: "list_windows"}, &windows)
		require.Len(t, windows.Windows, 1)
		require.Equal(t, "w:1", windows.Windows[0].ID)
		require.Equal(t, "chat_cleared", windows.Windows[0].StartedBy)
		require.Equal(t, 1, windows.Windows[0].Messages)
		require.True(t, windows.Windows[0].Current)

		var found chatHistorySearchResult
		decode(t, chat, chatHistoryArgs{Action: "search", Query: "zebra"}, &found)
		require.Len(t, found.Matches, 1)
		require.Equal(t, after[0], found.Matches[0].ID)
		require.Contains(t, run(t, chat, chatHistoryArgs{Action: "read_item", ID: before[0]}, true), "before the chat was cleared")
	})

	t.Run("BoundsTheResult", func(t *testing.T) {
		t.Parallel()
		chat := newChat(t)
		var messages []chatstate.Message
		for range 300 {
			messages = append(messages, userText("turn"))
			messages = append(messages, compaction(t)...)
		}
		insert(t, chat.ID, messages...)
		var windows chatHistoryWindowsResult
		decode(t, chat, chatHistoryArgs{Action: "list_windows"}, &windows)
		require.Positive(t, windows.OmittedWindows)
		require.Equal(t, 301, windows.OmittedWindows+len(windows.Windows))
		last := windows.Windows[len(windows.Windows)-1]
		require.Equal(t, "w:301", last.ID)
		require.True(t, last.Current)

		wide := newChat(t)
		var rows []chatstate.Message
		for range 40 {
			rows = append(rows, userText(strings.Repeat("w", 1000)))
		}
		insert(t, wide.ID, rows...)
		var page chatHistoryItemsResult
		decode(t, wide, chatHistoryArgs{Action: "list_items", Window: "w:1", Limit: maxHistoryItems}, &page)
		require.Len(t, page.Items, 40)
		for _, item := range page.Items {
			require.LessOrEqual(t, len(item.Preview), historyPreviewBytes)
		}

		// read_item fits the whole result, the part sizes too, when a
		// message has many parts or a part that JSON escaping grows.
		parts := make([]codersdk.ChatMessagePart, 0, 3000)
		for range 3000 {
			parts = append(parts, codersdk.ChatMessageText("p"))
		}
		crowded := newChat(t)
		ids := insert(t, crowded.ID,
			message(database.ChatMessageRoleUser, database.ChatMessageVisibilityBoth, parts...),
			userText(strings.Repeat("\x01", 20000)),
		)
		var many chatHistoryMessageResult
		decode(t, crowded, chatHistoryArgs{Action: "read_item", ID: ids[0]}, &many)
		require.Positive(t, many.OmittedParts)
		require.Len(t, many.PartBytes, len(many.Parts))
		require.Equal(t, 3000, len(many.Parts)+many.OmittedParts)
		var escaped chatHistoryMessageResult
		decode(t, crowded, chatHistoryArgs{Action: "read_item", ID: ids[1]}, &escaped)
		require.Len(t, escaped.Parts, 1)
		require.Equal(t, []int{20000}, escaped.PartBytes)
		require.Contains(t, escaped.Parts[0].Text, "Coder truncated")
	})

	t.Run("RejectsInvalidArguments", func(t *testing.T) {
		t.Parallel()
		chat := newChat(t)
		other := newChat(t)
		otherIDs := insert(t, other.ID, userText("private"))
		insert(t, chat.ID, userText("hello"))
		for _, tc := range []struct {
			args chatHistoryArgs
			want string
		}{
			{chatHistoryArgs{Action: "delete"}, "unknown action"},
			{chatHistoryArgs{Action: "list_items"}, "needs a window"},
			{chatHistoryArgs{Action: "list_items", Window: "w:9"}, "unknown window"},
			// run checks that an error that repeats a long argument still
			// fits the 16 KiB result.
			{chatHistoryArgs{Action: strings.Repeat("a", 20000)}, "unknown action"},
			{chatHistoryArgs{Action: "list_items", Window: strings.Repeat("w", 20000)}, "unknown window"},
			{chatHistoryArgs{Action: "list_items", Window: "w:1", Limit: maxHistoryItems + 1}, "limit must be"},
			{chatHistoryArgs{Action: "search", Query: "x"}, "query must be"},
			{chatHistoryArgs{Action: "search", Query: strings.Repeat("x", maxHistoryQueryBytes+1)}, "query must be"},
			{chatHistoryArgs{Action: "search", Query: "hello", BeforeID: -1}, "must not be negative"},
			{chatHistoryArgs{Action: "read_item"}, "needs the id"},
			{chatHistoryArgs{Action: "read_item", ID: otherIDs[0]}, "does not exist in this chat"},
			{chatHistoryArgs{Action: "read_item", ID: 1 << 40}, "does not exist in this chat"},
			{chatHistoryArgs{Action: "read_item", ID: otherIDs[0], Part: -1}, "must not be negative"},
		} {
			raw := run(t, chat, tc.args, true)
			require.Contains(t, raw, tc.want, tc.args)
			require.NotContains(t, raw, "private")
		}
	})

	t.Run("SkipsPlanExploreAndReadOnly", func(t *testing.T) {
		t.Parallel()
		chat := newChat(t)
		tools, err := server.appendForkTools(chatdTestContext(t), nil, chat, chat.LastModelConfigID)
		require.NoError(t, err)
		tool := findToolByName(tools, chatHistoryToolName)
		require.NotNil(t, tool)
		require.Empty(t, filterToolsForTurn([]fantasy.AgentTool{tool}, database.NullChatPlanMode{ChatPlanMode: database.ChatPlanModePlan, Valid: true}, uuid.NullUUID{}, nil))
		require.False(t, exploreToolAllowed(tool))
		require.False(t, customAgentReadOnlyToolAllowed(tool))
	})
}
