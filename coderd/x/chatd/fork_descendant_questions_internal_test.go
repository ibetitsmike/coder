package chatd

// This file is fork code: it tests the descendant ask_user_question tool and
// the needs_input report of wait_agent behind fork-nested-subagents.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/forkdb"
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

	off := newNestedSubagentsFixture(t, upstreamExperimentsKnown(), Limits{}, transport)
	offChild, err := off.spawn(t, off.root, childSubagentChatOptions{})
	require.NoError(t, err)
	env = turn(t, off, offChild)
	require.Empty(t, questionTools(env))
	require.Equal(t, upstreamStop(offChild), env.StopAfterTools)
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
	require.Empty(t, answers, "a new question drops the secret answers of earlier ones")
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

	// An answer during the wait lets the call wait on the child for the
	// rest of the timeout, and the answered question is no longer reported.
	subagentAwaitTrap := mClock.Trap().NewTimer("chatd", "subagent_await")
	defer subagentAwaitTrap.Close()
	subagentPollTrap := mClock.Trap().NewTicker("chatd", "subagent_poll")
	defer subagentPollTrap.Close()
	done = waitAsync(child.ID, 10)
	awaitTrap.MustWait(ctx).MustRelease(ctx)
	pollTrap.MustWait(ctx).MustRelease(ctx)
	dbgen.ChatMessage(t, f.db, database.ChatMessage{
		ChatID:        child.ID,
		ModelConfigID: uuid.NullUUID{UUID: child.LastModelConfigID, Valid: true},
		Role:          database.ChatMessageRoleUser,
	})
	setChatStatus(ctx, t, f.db, child.ID, database.ChatStatusRunning, "")
	mClock.Advance(descendantQuestionPollInterval).MustWait(ctx)
	call := subagentAwaitTrap.MustWait(ctx)
	require.Equal(t, 8*time.Second, call.Duration)
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
	dbgen.ChatMessage(t, f.db, database.ChatMessage{
		ChatID:        child.ID,
		ModelConfigID: uuid.NullUUID{UUID: child.LastModelConfigID, Valid: true},
		Role:          database.ChatMessageRoleUser,
	})
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
	// poll instead of at the timeout.
	setChatStatus(ctx, t, f.db, child.ID, database.ChatStatusWaiting, "")
	done = waitAsync(child.ID, 10)
	awaitTrap.MustWait(ctx).MustRelease(ctx)
	pollTrap.MustWait(ctx).MustRelease(ctx)
	resp, err = f.server.newDescendantQuestionTool(child).Run(ctx,
		fantasy.ToolCall{ID: uuid.NewString(), Name: askUserQuestionToolName, Input: descendantQuestionInput})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	mClock.Advance(descendantQuestionPollInterval).MustWait(ctx)
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
