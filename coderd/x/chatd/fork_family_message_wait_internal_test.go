package chatd

import (
	"context"
	"encoding/json"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/codersdk"
)

// blockingWaitAgent is a wait_agent tool that waits until its context ends,
// like a wait for a child that never finishes. It signals started on each
// call.
func blockingWaitAgent(started chan<- struct{}) fantasy.AgentTool {
	return fantasy.NewAgentTool(waitAgentToolName, "wait",
		func(ctx context.Context, _ waitAgentArgs, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			select {
			case started <- struct{}{}:
			default:
			}
			<-ctx.Done()
			return fantasy.NewTextErrorResponse(ctx.Err().Error()), nil
		})
}

func TestFamilyMessageWaitPending(t *testing.T) {
	t.Parallel()
	ft := newFamilyMessageTest(t)
	setChatStatus(ft.ctx, t, ft.f.db, ft.root.ID, database.ChatStatusRunning, "")
	pending := func() bool {
		t.Helper()
		got, err := ft.server.pendingFamilyMessage(ft.ctx, ft.root.ID)
		require.NoError(t, err)
		return got
	}

	// A steer from the user is not a family message.
	_, err := ft.server.SendMessage(ft.ctx, SendMessageOptions{
		ChatID:       ft.root.ID,
		CreatedBy:    ft.f.user.ID,
		Content:      []codersdk.ChatMessagePart{codersdk.ChatMessageText("user steer")},
		BusyBehavior: SendMessageBusyBehaviorSteer,
	})
	require.NoError(t, err)
	require.False(t, pending())

	ft.send(t, ft.a, ft.root.ID, "I need X before I can finish", false)
	require.True(t, pending())

	// A steer message under edit holds the whole lane, so nothing is
	// delivered and the wait goes on.
	queued, err := ft.f.db.GetChatQueuedMessages(ft.ctx, ft.root.ID)
	require.NoError(t, err)
	require.Len(t, queued, 2)
	_, err = ft.f.sqlDB.ExecContext(ft.ctx, "UPDATE chat_queued_messages SET editing_since = now() WHERE id = $1", queued[0].ID)
	require.NoError(t, err)
	require.False(t, pending())
	_, err = ft.f.sqlDB.ExecContext(ft.ctx, "UPDATE chat_queued_messages SET editing_since = NULL WHERE id = $1", queued[0].ID)
	require.NoError(t, err)
	require.True(t, pending())
}

func TestFamilyMessageWaitTool(t *testing.T) {
	t.Parallel()
	ft := newFamilyMessageTest(t)
	setChatStatus(ft.ctx, t, ft.f.db, ft.root.ID, database.ChatStatusRunning, "")
	setChatStatus(ft.ctx, t, ft.f.db, ft.a.ID, database.ChatStatusRunning, "")

	// With the experiment off, wait_agent stays the upstream tool.
	off := newUnstartedServer(t, ft.f.pubsub, ft.f.db)
	tools, err := off.appendForkTools(ft.ctx, []fantasy.AgentTool{blockingWaitAgent(make(chan struct{}, 1))}, ft.root, ft.root.LastModelConfigID)
	require.NoError(t, err)
	_, wrapped := findToolByName(tools, waitAgentToolName).(familyMessageWaitTool)
	require.False(t, wrapped)

	started := make(chan struct{}, 1)
	tools, err = ft.server.appendForkTools(ft.ctx, []fantasy.AgentTool{blockingWaitAgent(started)}, ft.root, ft.root.LastModelConfigID)
	require.NoError(t, err)
	waitTool := findToolByName(tools, waitAgentToolName)
	_, wrapped = waitTool.(familyMessageWaitTool)
	require.True(t, wrapped)

	type runResult struct {
		resp fantasy.ToolResponse
		err  error
	}
	done := make(chan runResult, 1)
	go func() {
		input, _ := json.Marshal(waitAgentArgs{ChatID: ft.a.ID.String()})
		resp, err := waitTool.Run(ft.ctx, fantasy.ToolCall{ID: "call", Name: waitAgentToolName, Input: string(input)})
		done <- runResult{resp: resp, err: err}
	}()
	<-started
	// A steer from the user ahead of the family message does not hide it.
	_, err = ft.server.SendMessage(ft.ctx, SendMessageOptions{
		ChatID:       ft.root.ID,
		CreatedBy:    ft.f.user.ID,
		Content:      []codersdk.ChatMessagePart{codersdk.ChatMessageText("user steer")},
		BusyBehavior: SendMessageBusyBehaviorSteer,
	})
	require.NoError(t, err)
	ft.send(t, ft.b, ft.root.ID, "I need X before I can finish", false)

	var run runResult
	select {
	case run = <-done:
	case <-ft.ctx.Done():
		t.Fatal("wait_agent did not end for the family message")
	}
	require.NoError(t, run.err)
	require.False(t, run.resp.IsError, run.resp.Content)
	early := familyMessageWaitResult{
		ChatID:               ft.a.ID.String(),
		FamilyMessagePending: true,
		Note:                 familyMessageWaitNote,
		Status:               string(database.ChatStatusRunning),
		Title:                ft.a.Title,
	}
	var result familyMessageWaitResult
	require.NoError(t, json.Unmarshal([]byte(run.resp.Content), &result), run.resp.Content)
	require.Equal(t, early, result)

	// The wait leaves the queue to the next delivery point.
	queued, err := ft.f.db.GetChatQueuedMessages(ft.ctx, ft.root.ID)
	require.NoError(t, err)
	require.Len(t, queued, 2)

	// A wait that starts while the message is queued, such as a replayed
	// call, ends at once.
	result = familyMessageWaitResult{}
	require.NoError(t, json.Unmarshal([]byte(runChatTodoTool(ft.ctx, t, tools, waitAgentToolName, waitAgentArgs{ChatID: ft.a.ID.String()}, false)), &result))
	require.Equal(t, early, result)

	// A wrapper canceled after the child finished loses the child's result,
	// so the wait runs again for it.
	setChatStatus(ft.ctx, t, ft.f.db, ft.a.ID, database.ChatStatusWaiting, "")
	calls := 0
	canceledOnce := fantasy.NewAgentTool(waitAgentToolName, "wait",
		func(ctx context.Context, _ waitAgentArgs, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			calls++
			if calls == 1 {
				<-ctx.Done()
				return fantasy.NewTextErrorResponse("read the child's question: " + ctx.Err().Error()), nil
			}
			return fantasy.NewTextResponse(`{"report":"done"}`), nil
		})
	tools, err = ft.server.appendForkTools(ft.ctx, []fantasy.AgentTool{canceledOnce}, ft.root, ft.root.LastModelConfigID)
	require.NoError(t, err)
	require.JSONEq(t, `{"report":"done"}`, runChatTodoTool(ft.ctx, t, tools, waitAgentToolName, waitAgentArgs{ChatID: ft.a.ID.String()}, false))
	require.Equal(t, 2, calls)

	// A wait that completes keeps its result while a family message waits.
	finished := fantasy.NewAgentTool(waitAgentToolName, "wait",
		func(context.Context, waitAgentArgs, fantasy.ToolCall) (fantasy.ToolResponse, error) {
			return fantasy.NewTextResponse(`{"report":"done"}`), nil
		})
	tools, err = ft.server.appendForkTools(ft.ctx, []fantasy.AgentTool{finished}, ft.root, ft.root.LastModelConfigID)
	require.NoError(t, err)
	require.JSONEq(t, `{"report":"done"}`, runChatTodoTool(ft.ctx, t, tools, waitAgentToolName, waitAgentArgs{ChatID: ft.a.ID.String()}, false))
}
