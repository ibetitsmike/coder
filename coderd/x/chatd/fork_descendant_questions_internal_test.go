package chatd

// This file is fork code: it tests the descendant ask_user_question tool and
// the needs_input report of wait_agent behind fork-nested-subagents.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"cdr.dev/slog/v3/sloggers/slogtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbmetrics"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chattool"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/quartz"
)

const descendantQuestionInput = `{"questions":[{"header":"DB","question":"Which database?","options":[{"label":"Use Postgres"},{"label":"Use SQLite"}]}]}`

func TestDescendantQuestionTurnTools(t *testing.T) {
	t.Parallel()

	turn := func(t *testing.T, f nestedSubagentsFixture, chat database.Chat) turnEnvironment {
		t.Helper()
		ctx := chatdTestContext(t)
		messages, err := f.db.GetChatMessagesByChatID(ctx, database.GetChatMessagesByChatIDParams{ChatID: chat.ID})
		require.NoError(t, err)
		env, err := f.server.buildTurnEnvironment(ctx, generationPrepareInput{Chat: chat, Messages: messages})
		require.NoError(t, err)
		t.Cleanup(env.Close)
		return env
	}
	questionTools := func(env turnEnvironment) []fantasy.AgentTool {
		var found []fantasy.AgentTool
		for _, tool := range env.Tools {
			if tool.Info().Name == askUserQuestionToolName {
				found = append(found, tool)
			}
		}
		return found
	}
	upstreamStop := func(chat database.Chat) map[string]struct{} {
		return stopAfterBehaviorTools(chat.PlanMode, chat.Mode, chat.ParentChatID)
	}
	transport := withInternalTestServerTransportFactory(&aibridgeTestFactory{})

	on := newNestedSubagentsFixture(t, codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}, Limits{}, transport)
	child, err := on.spawn(t, on.root, childSubagentChatOptions{})
	require.NoError(t, err)
	env := turn(t, on, child)
	tools := questionTools(env)
	require.Len(t, tools, 1)
	require.IsType(t, descendantQuestionTool{}, tools[0])
	require.Contains(t, env.StopAfterTools, askUserQuestionToolName, "the question ends the child's turn")

	explore, err := on.spawn(t, on.root, childSubagentChatOptions{
		chatMode: database.NullChatMode{ChatMode: database.ChatModeExplore, Valid: true},
	})
	require.NoError(t, err)
	env = turn(t, on, explore)
	require.Empty(t, questionTools(env))
	require.Equal(t, upstreamStop(explore), env.StopAfterTools)

	root := on.root
	root.ClientType = database.ChatClientTypeUi
	env = turn(t, on, root)
	tools = questionTools(env)
	require.Len(t, tools, 1, "a root keeps upstream's tool")
	_, isFork := tools[0].(descendantQuestionTool)
	require.False(t, isFork)
	require.Equal(t, upstreamStop(root), env.StopAfterTools)

	isAsync := func(t *testing.T, tool fantasy.AgentTool) bool {
		t.Helper()
		resp, err := tool.Run(chatdTestContext(t), fantasy.ToolCall{ID: "call", Name: askUserQuestionToolName, Input: descendantQuestionInput})
		require.NoError(t, err)
		return strings.Contains(resp.Content, `"async":true`)
	}
	require.True(t, isAsync(t, tools[0]))

	apiRoot := on.root
	require.Equal(t, database.ChatClientTypeApi, apiRoot.ClientType)
	env = turn(t, on, apiRoot)
	tools = questionTools(env)
	require.Len(t, tools, 1, "an API root gets the async question")
	require.True(t, isAsync(t, tools[0]))
	require.Equal(t, upstreamStop(apiRoot), env.StopAfterTools)

	planRoot := on.root
	planRoot.PlanMode = database.NullChatPlanMode{ChatPlanMode: database.ChatPlanModePlan, Valid: true}
	env = turn(t, on, planRoot)
	tools = questionTools(env)
	require.Len(t, tools, 1, "plan mode keeps the blocking question")
	require.False(t, isAsync(t, tools[0]))

	exploreRoot := on.root
	exploreRoot.Mode = database.NullChatMode{ChatMode: database.ChatModeExplore, Valid: true}
	require.Empty(t, questionTools(turn(t, on, exploreRoot)))

	off := newNestedSubagentsFixture(t, upstreamExperimentsKnown(), Limits{}, transport)
	offChild, err := off.spawn(t, off.root, childSubagentChatOptions{})
	require.NoError(t, err)
	env = turn(t, off, offChild)
	require.Empty(t, questionTools(env))
	require.Equal(t, upstreamStop(offChild), env.StopAfterTools)
	require.Empty(t, questionTools(turn(t, off, off.root)), "an API root keeps upstream's tools")
}

// Calls are validated against the tool schema before Run, so the schema has to
// admit a secret question without options and still require them otherwise.
func TestDescendantQuestionSchema(t *testing.T) {
	t.Parallel()

	tool := descendantQuestionTool{AgentTool: chattool.NewAskUserQuestionTool(chattool.AskUserQuestionOptions{})}
	prepared := turnEnvironment{
		Tools:            []fantasy.AgentTool{tool},
		BuiltinToolNames: map[string]bool{askUserQuestionToolName: true},
	}
	for _, tt := range []struct {
		name  string
		input string
		valid bool
	}{
		{name: "OrdinaryWithOptions", input: descendantQuestionInput, valid: true},
		{name: "SecretWithoutOptions", input: `{"questions":[{"header":"Token","question":"Paste the token.","secret":true}]}`, valid: true},
		{name: "SecretWithEmptyOptions", input: `{"questions":[{"header":"Token","question":"Paste the token.","secret":true,"options":[]}]}`, valid: true},
		{name: "OrdinaryWithoutOptions", input: `{"questions":[{"header":"DB","question":"Which database?"}]}`},
		{name: "NotSecretWithoutOptions", input: `{"questions":[{"header":"DB","question":"Which database?","secret":false}]}`},
		{name: "OrdinaryWithOneOption", input: `{"questions":[{"header":"DB","question":"Which database?","options":[{"label":"Use Postgres"}]}]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			call := fantasy.ToolCallContent{ToolName: askUserQuestionToolName, Input: tt.input}
			allowed, _, rejected := partitionInvalidToolCalls(prepared, []fantasy.ToolCallContent{call})
			if tt.valid {
				require.Len(t, allowed, 1)
				require.Empty(t, rejected)
				return
			}
			require.Empty(t, allowed)
			require.Len(t, rejected, 1)
		})
	}
}

func TestDescendantQuestionToolRecordsQuestion(t *testing.T) {
	t.Parallel()

	f := newNestedSubagentsFixture(t, codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}, Limits{})
	ctx := chatdTestContext(t)
	child, err := f.spawn(t, f.root, childSubagentChatOptions{})
	require.NoError(t, err)
	tool := f.server.newDescendantQuestionTool(child)

	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "call-invalid", Input: `{"questions":[{"header":"DB","question":"Which?","options":[{"label":"only one"}]}]}`})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	_, err = f.fdb.GetPendingChatDescendantQuestion(ctx, child.ID)
	require.ErrorIs(t, err, sql.ErrNoRows, "an invalid question is not recorded")
	require.NoError(t, f.fdb.InsertChatSecretAnswer(ctx, forkdb.InsertChatSecretAnswerParams{
		ChatID: child.ID, ToolCallID: "call-0", Value: "earlier secret",
	}))

	resp, err = tool.Run(ctx, fantasy.ToolCall{ID: "call-1", Input: descendantQuestionInput})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	var result map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(resp.Content), &result))
	require.Contains(t, result, "status")
	require.NotContains(t, result, "async", "the UI treats a question without async as blocking")
	row, err := f.fdb.GetPendingChatDescendantQuestion(ctx, child.ID)
	require.NoError(t, err)
	require.Equal(t, "call-1", row.ToolCallID)
	require.JSONEq(t, string(result["questions"]), string(row.Questions))
	answers, err := f.fdb.GetChatSecretAnswersByChatID(ctx, child.ID)
	require.NoError(t, err)
	require.Len(t, answers, 1, "a new question keeps the secret answers of earlier ones, which still scrub tool output")
	require.Equal(t, "call-0", answers[0].ToolCallID)
}

func TestDescendantQuestionWaitAgent(t *testing.T) {
	t.Parallel()

	mClock := quartz.NewMock(t)
	// fork-chat-todos adds include_tool_calls, which the question wrapper
	// must keep.
	f := newNestedSubagentsFixture(t, codersdk.Experiments{codersdk.ExperimentForkNestedSubagents, codersdk.ExperimentForkChatTodos}, Limits{},
		withInternalTestServerClock(mClock))
	ctx := chatdTestContext(t)
	root := f.root
	child, err := f.spawn(t, root, childSubagentChatOptions{})
	require.NoError(t, err)
	grandchild, err := f.spawn(t, child, childSubagentChatOptions{})
	require.NoError(t, err)
	for _, asker := range []database.Chat{child, grandchild} {
		resp, err := f.server.newDescendantQuestionTool(asker).Run(ctx,
			fantasy.ToolCall{ID: uuid.NewString(), Name: askUserQuestionToolName, Input: descendantQuestionInput})
		require.NoError(t, err)
		require.False(t, resp.IsError, resp.Content)
		setChatStatus(ctx, t, f.db, asker.ID, database.ChatStatusWaiting, "")
	}

	tools, err := f.server.subagentTools(ctx, func() database.Chat { return root }, root.LastModelConfigID)
	require.NoError(t, err)
	tools, err = f.server.appendForkTools(ctx, tools, root, root.LastModelConfigID)
	require.NoError(t, err)
	wait := findToolByName(tools, waitAgentToolName)
	require.IsType(t, descendantQuestionWaitTool{}, wait)
	waitAsync := func(chatID uuid.UUID, timeoutSeconds int) <-chan fantasy.ToolResponse {
		ch := make(chan fantasy.ToolResponse, 1)
		go func() {
			input := fmt.Sprintf(`{"chat_id":%q,"timeout_seconds":%d,"include_tool_calls":1}`, chatID, timeoutSeconds)
			resp, err := wait.Run(ctx, fantasy.ToolCall{ID: uuid.NewString(), Name: waitAgentToolName, Input: input})
			if err != nil {
				resp = fantasy.NewTextErrorResponse(err.Error())
			}
			ch <- resp
		}()
		return ch
	}
	decode := func(resp fantasy.ToolResponse) map[string]any {
		require.False(t, resp.IsError, resp.Content)
		var result map[string]any
		require.NoError(t, json.Unmarshal([]byte(resp.Content), &result))
		return result
	}

	// The question table is never read for a chat that is not a child, so
	// the grandchild's question cannot block the call or leak.
	resp := testutil.RequireReceive(ctx, t, waitAsync(grandchild.ID, 1))
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, ErrSubagentNotDescendant.Error())

	awaitTrap := mClock.Trap().NewTimer("chatd", "descendant_question_await")
	defer awaitTrap.Close()
	pollTrap := mClock.Trap().NewTicker("chatd", "descendant_question_poll")
	defer pollTrap.Close()

	// A call made while the question is pending waits for the answer and
	// reports needs_input with the child's status when the timeout elapses
	// first.
	done := waitAsync(child.ID, 1)
	awaitTrap.MustWait(ctx).MustRelease(ctx)
	pollTrap.MustWait(ctx).MustRelease(ctx)
	mClock.Advance(time.Second).MustWait(ctx)
	result := decode(testutil.RequireReceive(ctx, t, done))
	require.Equal(t, true, result["needs_input"])
	require.Equal(t, string(database.ChatStatusWaiting), result["status"])
	require.NotEmpty(t, result["questions"])
	require.Contains(t, result, "tool_calls")
	require.Equal(t, descendantQuestionNote, result["note"])

	sendAnswer := func() {
		t.Helper()
		_, err := f.server.SendMessage(ctx, SendMessageOptions{
			ChatID: child.ID, CreatedBy: child.OwnerID,
			Content:      []codersdk.ChatMessagePart{codersdk.ChatMessageText("Use Postgres")},
			BusyBehavior: SendMessageBusyBehaviorQueue,
		})
		require.NoError(t, err)
	}

	// An answer during the wait wakes the call on the child's state
	// channel, without a clock advance, and the call waits on the child for
	// the rest of the timeout. The answered question is no longer reported.
	subagentAwaitTrap := mClock.Trap().NewTimer("chatd", "subagent_await")
	defer subagentAwaitTrap.Close()
	subagentPollTrap := mClock.Trap().NewTicker("chatd", "subagent_poll")
	defer subagentPollTrap.Close()
	done = waitAsync(child.ID, 10)
	awaitTrap.MustWait(ctx).MustRelease(ctx)
	pollTrap.MustWait(ctx).MustRelease(ctx)
	sendAnswer()
	call := subagentAwaitTrap.MustWait(ctx)
	require.Equal(t, 10*time.Second, call.Duration, "no time passed before the call woke")
	call.MustRelease(ctx)
	subagentPollTrap.MustWait(ctx).MustRelease(ctx)
	setChatStatus(ctx, t, f.db, child.ID, database.ChatStatusWaiting, "")
	insertAssistantMessage(t, f.db, child.ID, child.LastModelConfigID, "answered")
	mClock.Advance(subagentAwaitFallbackPoll).MustWait(ctx)
	result = decode(testutil.RequireReceive(ctx, t, done))
	require.Equal(t, "answered", result["report"])
	require.NotContains(t, result, "needs_input")

	// A question the child asks while the call waits on it is reported
	// with the child's result.
	setChatStatus(ctx, t, f.db, child.ID, database.ChatStatusRunning, "")
	done = waitAsync(child.ID, 10)
	subagentAwaitTrap.MustWait(ctx).MustRelease(ctx)
	subagentPollTrap.MustWait(ctx).MustRelease(ctx)
	resp, err = f.server.newDescendantQuestionTool(child).Run(ctx,
		fantasy.ToolCall{ID: uuid.NewString(), Name: askUserQuestionToolName, Input: descendantQuestionInput})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	setChatStatus(ctx, t, f.db, child.ID, database.ChatStatusWaiting, "")
	insertAssistantMessage(t, f.db, child.ID, child.LastModelConfigID, "asked again")
	// Server tickers can fire before the poll, and Advance cannot pass them.
	for elapsed := time.Duration(0); elapsed < subagentAwaitFallbackPoll; {
		d, w := mClock.AdvanceNext()
		w.MustWait(ctx)
		elapsed += d
	}
	result = decode(testutil.RequireReceive(ctx, t, done))
	require.Equal(t, "asked again", result["report"])
	require.Equal(t, true, result["needs_input"])
	require.NotEmpty(t, result["questions"])

	// A question the child asks before the wait times out is reported even
	// though the child is still running.
	sendAnswer()
	setChatStatus(ctx, t, f.db, child.ID, database.ChatStatusRunning, "")
	done = waitAsync(child.ID, 2)
	subagentAwaitTrap.MustWait(ctx).MustRelease(ctx)
	subagentPollTrap.MustWait(ctx).MustRelease(ctx)
	resp, err = f.server.newDescendantQuestionTool(child).Run(ctx,
		fantasy.ToolCall{ID: uuid.NewString(), Name: askUserQuestionToolName, Input: descendantQuestionInput})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	for elapsed := time.Duration(0); elapsed < 2*time.Second; {
		d, w := mClock.AdvanceNext()
		w.MustWait(ctx)
		elapsed += d
	}
	result = decode(testutil.RequireReceive(ctx, t, done))
	require.Equal(t, true, result["timed_out"])
	require.Equal(t, true, result["needs_input"])

	// A question that replaces the awaited one ends the wait at the next
	// fallback tick instead of at the timeout: a new question commits
	// outside the child's state machine and publishes nothing.
	setChatStatus(ctx, t, f.db, child.ID, database.ChatStatusWaiting, "")
	done = waitAsync(child.ID, 10)
	awaitTrap.MustWait(ctx).MustRelease(ctx)
	pollTrap.MustWait(ctx).MustRelease(ctx)
	resp, err = f.server.newDescendantQuestionTool(child).Run(ctx,
		fantasy.ToolCall{ID: uuid.NewString(), Name: askUserQuestionToolName, Input: descendantQuestionInput})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	for elapsed := time.Duration(0); elapsed < subagentAwaitFallbackPoll; {
		d, w := mClock.AdvanceNext()
		w.MustWait(ctx)
		elapsed += d
	}
	result = decode(testutil.RequireReceive(ctx, t, done))
	require.Equal(t, true, result["needs_input"])
	require.NotEmpty(t, result["questions"])

	// The note of a secret question leaves out message_agent, which would
	// answer it without the value.
	_, err = f.fdb.UpsertChatDescendantQuestion(ctx, forkdb.UpsertChatDescendantQuestionParams{
		ChatID: child.ID, ToolCallID: uuid.NewString(),
		Questions: json.RawMessage(`[{"header":"Token","question":"Paste the token.","secret":true}]`),
	})
	require.NoError(t, err)
	done = waitAsync(child.ID, 1)
	awaitTrap.MustWait(ctx).MustRelease(ctx)
	pollTrap.MustWait(ctx).MustRelease(ctx)
	for elapsed := time.Duration(0); elapsed < time.Second; {
		d, w := mClock.AdvanceNext()
		w.MustWait(ctx)
		elapsed += d
	}
	result = decode(testutil.RequireReceive(ctx, t, done))
	require.Equal(t, true, result["needs_input"])
	require.Equal(t, descendantSecretQuestionNote, result["note"])
}

// TestDescendantQuestionWaitReads checks that a wait_agent call on a child
// with an unanswered question reads the question once at the start, once
// per fallback tick and once after the short wait for the child, and not
// on a short interval.
func TestDescendantQuestionWaitReads(t *testing.T) {
	t.Parallel()

	const query = "GetPendingChatDescendantQuestion"
	raw, ps, sqlDB := dbtestutil.NewDBWithSQLDB(t)
	reg := prometheus.NewRegistry()
	db := dbmetrics.NewQueryMetrics(raw, slogtest.Make(t, nil), reg)
	reads := func() int {
		families, err := reg.Gather()
		require.NoError(t, err)
		var n float64
		for _, family := range families {
			if family.GetName() != "coderd_db_query_counts_total" {
				continue
			}
			for _, m := range family.GetMetric() {
				for _, l := range m.GetLabel() {
					if l.GetName() == "query" && l.GetValue() == query {
						n += m.GetCounter().GetValue()
					}
				}
			}
		}
		return int(n)
	}

	mClock := quartz.NewMock(t)
	f := newNestedSubagentsFixtureOn(t, db, ps, sqlDB, codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}, Limits{},
		withInternalTestServerClock(mClock))
	ctx := chatdTestContext(t)
	child, err := f.spawn(t, f.root, childSubagentChatOptions{})
	require.NoError(t, err)
	resp, err := f.server.newDescendantQuestionTool(child).Run(ctx,
		fantasy.ToolCall{ID: uuid.NewString(), Name: askUserQuestionToolName, Input: descendantQuestionInput})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	setChatStatus(ctx, t, f.db, child.ID, database.ChatStatusWaiting, "")

	tools, err := f.server.subagentTools(ctx, func() database.Chat { return f.root }, f.root.LastModelConfigID)
	require.NoError(t, err)
	tools, err = f.server.appendForkTools(ctx, tools, f.root, f.root.LastModelConfigID)
	require.NoError(t, err)
	wait := findToolByName(tools, waitAgentToolName)

	awaitTrap := mClock.Trap().NewTimer("chatd", "descendant_question_await")
	defer awaitTrap.Close()
	pollTrap := mClock.Trap().NewTicker("chatd", "descendant_question_poll")
	defer pollTrap.Close()

	before := reads()
	done := make(chan fantasy.ToolResponse, 1)
	go func() {
		input := fmt.Sprintf(`{"chat_id":%q,"timeout_seconds":60}`, child.ID)
		resp, err := wait.Run(ctx, fantasy.ToolCall{ID: uuid.NewString(), Name: waitAgentToolName, Input: input})
		if err != nil {
			resp = fantasy.NewTextErrorResponse(err.Error())
		}
		done <- resp
	}()
	awaitTrap.MustWait(ctx).MustRelease(ctx)
	pollTrap.MustWait(ctx).MustRelease(ctx)
	require.Equal(t, 1, reads()-before, "the initial read")

	// Server tickers can fire between the fallback ticks, and Advance
	// cannot pass them.
	advance := func(d time.Duration) {
		for elapsed := time.Duration(0); elapsed < d; {
			step, w := mClock.AdvanceNext()
			w.MustWait(ctx)
			elapsed += step
		}
	}
	for tick := 1; tick*int(subagentAwaitFallbackPoll) < int(time.Minute); tick++ {
		advance(subagentAwaitFallbackPoll)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return reads()-before >= 1+tick
		}, testutil.IntervalFast, "fallback tick %d reads the question", tick)
	}
	advance(subagentAwaitFallbackPoll)
	// The child waits, so the short wait for it returns at once.
	result := testutil.RequireReceive(ctx, t, done)
	require.False(t, result.IsError, result.Content)
	require.Contains(t, result.Content, `"needs_input":true`)
	t.Logf("question reads in 60s: %d", reads()-before)
	require.LessOrEqual(t, reads()-before, 13, "one read per fallback tick plus the initial read and the final one")
}
