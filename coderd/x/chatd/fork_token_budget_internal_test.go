package chatd

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatloop"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprompt"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
	"github.com/coder/coder/v2/codersdk"
)

// setForkTokenBudget saves the token budget setting of an organization.
func setForkTokenBudget(t *testing.T, db database.Store, organizationID uuid.UUID, strategy codersdk.ForkTokenBudgetStrategy, fallback codersdk.ForkTokenBudgetFallback) {
	t.Helper()
	fdb, err := forkdb.From(db)
	require.NoError(t, err)
	_, err = fdb.UpsertChatOrganizationTokenBudget(chatdTestContext(t), forkdb.UpsertChatOrganizationTokenBudgetParams{
		OrganizationID: organizationID,
		Strategy:       string(strategy),
		Fallback:       string(fallback),
	})
	require.NoError(t, err)
}

func TestTokenBudgetLadder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		limit     int64
		threshold int32
		want      tokenBudget
		off       bool
	}{
		{
			name: "SmallLimit", limit: 1000, threshold: 30,
			want: tokenBudget{limit: 1000, usable: 750, final: 375, handoff: 300},
		},
		{
			// The handoff point never passes the final request.
			name: "HighThreshold", limit: 1000, threshold: 90,
			want: tokenBudget{limit: 1000, usable: 750, final: 375, handoff: 375},
		},
		{
			// A threshold under the minimum counts as the minimum.
			name: "LowThreshold", limit: 1000, threshold: 1,
			want: tokenBudget{limit: 1000, usable: 750, final: 375, handoff: 100},
		},
		{
			name: "LargeLimit", limit: 200000, threshold: 70,
			want: tokenBudget{limit: 200000, usable: 191808, final: 181616, handoff: 140000},
		},
		{name: "Disabled", limit: 1000, threshold: 100, off: true},
		{name: "NoLimit", limit: 0, threshold: 70, off: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := newTokenBudget(tt.limit, tt.threshold)
			require.Equal(t, !tt.off, ok)
			require.Equal(t, tt.want, got)
		})
	}
	budget, _ := newTokenBudget(200000, 70)
	require.Equal(t, int32(95), budget.usablePercent())
}

// tokenBudgetRow builds a prompt row for the pure tests. tokens > 0 gives
// an assistant row that prompt usage.
func tokenBudgetRow(t *testing.T, role database.ChatMessageRole, visibility database.ChatMessageVisibility, tokens int64, parts ...codersdk.ChatMessagePart) database.ChatMessage {
	t.Helper()
	content, err := chatprompt.MarshalParts(parts)
	require.NoError(t, err)
	row := database.ChatMessage{
		Role:           role,
		Visibility:     visibility,
		Content:        content,
		ContentVersion: chatprompt.CurrentContentVersion,
	}
	if tokens > 0 {
		row.InputTokens = sql.NullInt64{Int64: tokens, Valid: true}
	}
	return row
}

func TestTokenBudgetRequestDue(t *testing.T) {
	t.Parallel()

	budget := tokenBudget{limit: 1000, usable: 750, final: 375, handoff: 300}
	user := func(text string) database.ChatMessage {
		return tokenBudgetRow(t, database.ChatMessageRoleUser, database.ChatMessageVisibilityBoth, 0, codersdk.ChatMessageText(text))
	}
	request := func(header string) database.ChatMessage {
		return tokenBudgetRow(t, database.ChatMessageRoleUser, database.ChatMessageVisibilityModel, 0,
			codersdk.ChatMessageText(formatTokenBudgetRequest(header, budget, 1)))
	}
	assistant := func(tokens int64) database.ChatMessage {
		return tokenBudgetRow(t, database.ChatMessageRoleAssistant, database.ChatMessageVisibilityBoth, tokens, codersdk.ChatMessageText("ok"))
	}
	newContextResult := func(isError bool) database.ChatMessage {
		return tokenBudgetRow(t, database.ChatMessageRoleTool, database.ChatMessageVisibilityBoth, 0,
			codersdk.ChatMessageToolResult("call", newContextToolName, json.RawMessage(`{}`), isError, false))
	}

	tests := []struct {
		name   string
		rows   []database.ChatMessage
		header string
	}{
		{name: "NoUsage", rows: []database.ChatMessage{user("hi")}},
		{name: "UnderHandoff", rows: []database.ChatMessage{user("hi"), assistant(100), assistant(299)}},
		{name: "Handoff", rows: []database.ChatMessage{user("hi"), assistant(100), assistant(300)}, header: tokenBudgetHandoffHeader},
		{name: "HandoffOnce", rows: []database.ChatMessage{user("hi"), assistant(100), assistant(300), request(tokenBudgetHandoffHeader), assistant(320)}},
		{name: "FinalAfterHandoff", rows: []database.ChatMessage{user("hi"), assistant(100), assistant(300), request(tokenBudgetHandoffHeader), assistant(400)}, header: tokenBudgetFinalHeader},
		{name: "FinalSkipsHandoff", rows: []database.ChatMessage{user("hi"), assistant(100), assistant(400)}, header: tokenBudgetFinalHeader},
		{name: "FinalOnce", rows: []database.ChatMessage{user("hi"), assistant(100), assistant(400), request(tokenBudgetFinalHeader), assistant(500)}},
		{name: "AtUsable", rows: []database.ChatMessage{user("hi"), assistant(100), assistant(750)}},
		{name: "StartedOverHandoff", rows: []database.ChatMessage{user("hi"), assistant(300), assistant(400)}},
		{name: "NewContextAccepted", rows: []database.ChatMessage{user("hi"), assistant(100), assistant(400), newContextResult(false)}},
		{name: "NewContextRefused", rows: []database.ChatMessage{user("hi"), assistant(100), assistant(400), newContextResult(true)}, header: tokenBudgetFinalHeader},
		{
			// A request a user typed is not a request the window got.
			name:   "UserTypedHeader",
			rows:   []database.ChatMessage{user(tokenBudgetHandoffHeader), assistant(100), assistant(300)},
			header: tokenBudgetHandoffHeader,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			header, _, ok := tokenBudgetRequestDue(budget, tt.rows)
			require.Equal(t, tt.header != "", ok)
			require.Equal(t, tt.header, header)
		})
	}
	text := formatTokenBudgetRequest(tokenBudgetHandoffHeader, budget, 320)
	require.True(t, strings.HasPrefix(text, tokenBudgetHandoffHeader))
	require.True(t, strings.HasSuffix(text, tokenBudgetFooter))
	require.Contains(t, text, chatCheckpointNotePath)
	require.Contains(t, text, newContextToolName)
}

func TestTokenBudgetTriggers(t *testing.T) {
	t.Parallel()

	db, ps := dbtestutil.NewDB(t)
	on := newInternalTestServer(t, db, ps, chatprovider.ProviderAPIKeys{},
		withInternalTestServerExperiments(codersdk.Experiments{codersdk.ExperimentForkTokenBudget}))
	off := newInternalTestServer(t, db, ps, chatprovider.ProviderAPIKeys{})
	chat := database.Chat{ID: uuid.New()}
	tools := []fantasy.AgentTool{on.chatNotesTool(chat), on.chatHistoryTool(chat), on.newContextTool(chat)}

	chatTrigger := compactionTrigger{thresholdPercent: 30, contextLimit: 1000}
	orgTrigger := compactionTrigger{thresholdPercent: 80, contextLimit: 500}
	rows := func(tokens int64, extra ...database.ChatMessage) []database.ChatMessage {
		return append([]database.ChatMessage{
			tokenBudgetRow(t, database.ChatMessageRoleAssistant, database.ChatMessageVisibilityBoth, tokens, codersdk.ChatMessageText("ok")),
		}, extra...)
	}
	accepted := tokenBudgetRow(t, database.ChatMessageRoleTool, database.ChatMessageVisibilityBoth, 0,
		codersdk.ChatMessageToolResult("call", newContextToolName, json.RawMessage(`{}`), false, false))

	t.Run("ExperimentOff", func(t *testing.T) {
		t.Parallel()
		binding, source, other, needed := off.tokenBudgetTriggers(tools, rows(900, accepted), chatTrigger, orgTrigger, compactionTriggerSourceOrganization, chatTrigger, true)
		require.Equal(t, orgTrigger, binding)
		require.Equal(t, compactionTriggerSourceOrganization, source)
		require.Equal(t, chatTrigger, other)
		require.True(t, needed)
	})
	t.Run("ToolsDropped", func(t *testing.T) {
		t.Parallel()
		binding, source, other, needed := on.tokenBudgetTriggers(tools[:1], rows(100), chatTrigger, orgTrigger, compactionTriggerSourceOrganization, chatTrigger, true)
		require.Equal(t, orgTrigger, binding)
		require.Equal(t, compactionTriggerSourceOrganization, source)
		require.Equal(t, chatTrigger, other)
		require.True(t, needed)
	})
	t.Run("UnderUsable", func(t *testing.T) {
		t.Parallel()
		binding, source, other, needed := on.tokenBudgetTriggers(tools, rows(749), chatTrigger, orgTrigger, compactionTriggerSourceOrganization, chatTrigger, true)
		require.Equal(t, compactionTrigger{thresholdPercent: 75, contextLimit: 1000}, binding)
		require.Equal(t, compactionTriggerSourceChat, source)
		require.Equal(t, compactionTrigger{}, other)
		require.False(t, needed)
	})
	t.Run("AtUsable", func(t *testing.T) {
		t.Parallel()
		_, _, _, needed := on.tokenBudgetTriggers(tools, rows(750), chatTrigger, chatTrigger, compactionTriggerSourceChat, compactionTrigger{}, false)
		require.True(t, needed)
	})
	t.Run("NewContext", func(t *testing.T) {
		t.Parallel()
		_, _, _, needed := on.tokenBudgetTriggers(tools, rows(100, accepted), chatTrigger, chatTrigger, compactionTriggerSourceChat, compactionTrigger{}, false)
		require.True(t, needed)
	})
	t.Run("DisabledThreshold", func(t *testing.T) {
		t.Parallel()
		disabled := compactionTrigger{thresholdPercent: 100, contextLimit: 1000}
		binding, _, _, needed := on.tokenBudgetTriggers(tools, rows(990), disabled, disabled, compactionTriggerSourceChat, compactionTrigger{}, false)
		require.Equal(t, disabled, binding)
		require.False(t, needed, "a disabled threshold does not force a rollover")
		_, _, _, needed = on.tokenBudgetTriggers(tools, rows(990, accepted), disabled, disabled, compactionTriggerSourceChat, compactionTrigger{}, false)
		require.True(t, needed, "new_context still works with a disabled threshold")
	})
}

func TestTokenBudgetEligibility(t *testing.T) {
	t.Parallel()

	db, ps := dbtestutil.NewDB(t)
	server := newInternalTestServer(t, db, ps, chatprovider.ProviderAPIKeys{},
		withInternalTestServerExperiments(codersdk.Experiments{codersdk.ExperimentForkTokenBudget}))
	ctx := chatdTestContext(t)

	root := database.Chat{ID: uuid.New()}
	eligible, err := server.tokenBudgetChatEligible(ctx, root)
	require.NoError(t, err)
	require.True(t, eligible)

	child := database.Chat{ID: uuid.New(), ParentChatID: uuid.NullUUID{UUID: uuid.New(), Valid: true}}
	eligible, err = server.tokenBudgetChatEligible(ctx, child)
	require.NoError(t, err)
	require.True(t, eligible, "a general subagent works in its own windows")

	plan := database.Chat{ID: uuid.New(), PlanMode: database.NullChatPlanMode{ChatPlanMode: database.ChatPlanModePlan, Valid: true}}
	eligible, err = server.tokenBudgetChatEligible(ctx, plan)
	require.NoError(t, err)
	require.False(t, eligible)

	explore := database.Chat{ID: uuid.New(), Mode: database.NullChatMode{ChatMode: database.ChatModeExplore, Valid: true}}
	eligible, err = server.tokenBudgetChatEligible(ctx, explore)
	require.NoError(t, err)
	require.False(t, eligible)

	tools := []fantasy.AgentTool{server.chatNotesTool(root), server.chatHistoryTool(root), server.newContextTool(root)}
	require.True(t, server.tokenBudgetEligible(tools))
	// The lead-in points at chat_history, so a policy that drops it keeps
	// summary compaction.
	noHistory, _, _ := customAgentToolPolicy{Tools: []string{chatNotesToolName, newContextToolName}}.filter(tools, nil, nil, nil)
	require.Len(t, noHistory, 2)
	require.False(t, server.tokenBudgetEligible(noHistory))
	readonly, _, _ := customAgentToolPolicy{ReadOnly: true}.filter(tools, nil, nil, nil)
	require.False(t, server.tokenBudgetEligible(readonly))
	allowlist, _, _ := customAgentToolPolicy{Tools: []string{chatNotesToolName}}.filter(tools, nil, nil, nil)
	require.False(t, server.tokenBudgetEligible(allowlist))
	require.False(t, server.tokenBudgetEligible(filterToolsForTurn(tools, plan.PlanMode, uuid.NullUUID{}, nil)))
}

func TestNewContextTool(t *testing.T) {
	t.Parallel()

	db, ps := dbtestutil.NewDB(t)
	server := newInternalTestServer(t, db, ps, chatprovider.ProviderAPIKeys{},
		withInternalTestServerExperiments(codersdk.Experiments{codersdk.ExperimentForkTokenBudget}))
	ctx := chatdTestContext(t)
	user, org, model := seedInternalChatDeps(t, db)
	chat := dbgen.Chat(t, db, database.Chat{OrganizationID: org.ID, OwnerID: user.ID, LastModelConfigID: model.ID})
	setForkTokenBudget(t, db, org.ID, codersdk.ForkTokenBudgetStrategyTokenBudget, codersdk.ForkTokenBudgetFallbackNewWindow)

	tools, err := server.appendForkTools(ctx, nil, chat, chat.LastModelConfigID)
	require.NoError(t, err)
	tool := findToolByName(tools, newContextToolName)
	require.NotNil(t, tool)
	serial, ok := tool.(interface{ SerialToolCalls() bool })
	require.True(t, ok && serial.SerialToolCalls(), "new_context must run after the notes calls of its step")
	require.Empty(t, filterToolsForTurn([]fantasy.AgentTool{tool}, database.NullChatPlanMode{ChatPlanMode: database.ChatPlanModePlan, Valid: true}, uuid.NullUUID{}, nil))
	require.False(t, exploreToolAllowed(tool))
	require.False(t, customAgentReadOnlyToolAllowed(tool))

	run := func(wantError bool) string {
		t.Helper()
		return runChatTodoTool(ctx, t, tools, newContextToolName, struct{}{}, wantError)
	}
	writeNote := func() {
		t.Helper()
		runChatTodoTool(ctx, t, tools, chatNotesToolName, chatNotesArgs{Action: "write", Path: chatCheckpointNotePath, Content: "goal"}, false)
	}

	require.Contains(t, run(true), chatCheckpointNotePath)
	writeNote()
	require.Contains(t, run(false), "accepted")

	// After a rollover, the note written in the earlier window no longer
	// counts.
	built, err := buildCompactionMessages(buildCompactionMessagesInput{
		modelConfigID: model.ID,
		toolCallID:    uuid.NewString(),
		compaction:    chatloop.CompactionResult{SystemSummary: "lead-in", SummaryReport: "report"},
	})
	require.NoError(t, err)
	params := database.InsertChatMessagesParams{ChatID: chat.ID}
	for _, m := range built.Messages {
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
	_, err = db.InsertChatMessages(ctx, params)
	require.NoError(t, err)

	require.Contains(t, run(true), chatCheckpointNotePath)
	writeNote()
	require.Contains(t, run(false), "accepted")
}

func TestContextWindowLeadIn(t *testing.T) {
	t.Parallel()

	leadIn := formatContextWindowLeadIn("w:2", []forkdb.ListChatNotesRow{
		{Path: chatCheckpointNotePath, Bytes: 120},
		{Path: "plan/steps.md", Bytes: 40},
	}, "")
	require.True(t, strings.HasPrefix(leadIn, `<context_window id="w:2">`))
	require.True(t, strings.HasSuffix(leadIn, "</context_window>"))
	require.Contains(t, leadIn, "- checkpoint.md (120 bytes)")
	require.Contains(t, leadIn, "- plan/steps.md (40 bytes)")
	require.Contains(t, leadIn, "Read checkpoint.md")
	require.Contains(t, leadIn, chatHistoryToolName)

	empty := formatContextWindowLeadIn("w:3", nil, "")
	require.Contains(t, empty, "This chat has no notes.")
	require.Contains(t, empty, "There is no checkpoint.md")
	require.NotContains(t, empty, "pre_compact")
}

// A rollover keeps the model context of a pre_compact hook, which a summary
// would carry.
func TestContextWindowRolloverHookContext(t *testing.T) {
	t.Parallel()

	db, ps := dbtestutil.NewDB(t)
	server := newInternalTestServer(t, db, ps, chatprovider.ProviderAPIKeys{},
		withInternalTestServerExperiments(codersdk.Experiments{codersdk.ExperimentForkTokenBudget}))
	ctx := chatdTestContext(t)
	user, org, model := seedInternalChatDeps(t, db)
	chat := dbgen.Chat(t, db, database.Chat{OrganizationID: org.ID, OwnerID: user.ID, LastModelConfigID: model.ID})

	result, err := server.contextWindowRollover(ctx, chat, chatloop.GenerateCompactionOptions{
		Source:      chatloop.CompactionSourceAutomatic,
		SummaryHint: "Keep the release branch frozen.",
	})
	require.NoError(t, err)
	require.Contains(t, result.SystemSummary, "Context from a pre_compact hook:\nKeep the release branch frozen.\n</context_window>")
}

func TestContextWindowRolloverStreamsWindow(t *testing.T) {
	t.Parallel()

	db, ps := dbtestutil.NewDB(t)
	server := newInternalTestServer(t, db, ps, chatprovider.ProviderAPIKeys{},
		withInternalTestServerExperiments(codersdk.Experiments{codersdk.ExperimentForkTokenBudget}))
	ctx := chatdTestContext(t)
	user, org, model := seedInternalChatDeps(t, db)
	chat := dbgen.Chat(t, db, database.Chat{OrganizationID: org.ID, OwnerID: user.ID, LastModelConfigID: model.ID})

	var streamed []codersdk.ChatMessagePart
	result, err := server.contextWindowRollover(ctx, chat, chatloop.GenerateCompactionOptions{
		Source:     chatloop.CompactionSourceAutomatic,
		ToolCallID: "call-1",
		ToolName:   "chat_summarized",
		PublishMessagePart: func(role codersdk.ChatMessageRole, part codersdk.ChatMessagePart) {
			if role == codersdk.ChatMessageRoleTool {
				streamed = append(streamed, part)
			}
		},
	})
	require.NoError(t, err)
	require.Equal(t, &chatloop.ContextWindowBoundary{ID: "w:2", Trigger: chatloop.ContextWindowTriggerContextLimit}, result.ContextWindow)
	require.Len(t, streamed, 1)
	var got struct {
		ContextWindow *chatloop.ContextWindowBoundary `json:"context_window"`
	}
	require.NoError(t, json.Unmarshal(streamed[0].Result, &got))
	require.Equal(t, result.ContextWindow, got.ContextWindow)
}

func TestWithContextWindowResult(t *testing.T) {
	t.Parallel()

	window := &chatloop.ContextWindowBoundary{ID: "w:2", Trigger: chatloop.ContextWindowTriggerNewContext}
	tests := []struct {
		name   string
		result string
		window *chatloop.ContextWindowBoundary
		want   string
	}{
		{name: "NoWindow", result: `{"summary":"s"}`, want: `{"summary":"s"}`},
		{name: "Adds", result: `{"summary":"s"}`, window: window, want: `{"context_window":{"id":"w:2","trigger":"new_context"},"summary":"s"}`},
		{name: "Replaces", result: `{"context_window":1}`, window: window, want: `{"context_window":{"id":"w:2","trigger":"new_context"}}`},
		{name: "Null", result: `null`, window: window, want: `null`},
		{name: "NotAnObject", result: `"s"`, window: window, want: `"s"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, string(withContextWindowResult(json.RawMessage(tt.result), tt.window)))
		})
	}
}
