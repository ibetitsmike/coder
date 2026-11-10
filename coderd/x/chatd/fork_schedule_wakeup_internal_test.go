package chatd

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/quartz"
)

func TestScheduleWakeupTool(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := chatdTestContext(t)
	chat := f.createWakeupChat(t)
	mClock := quartz.NewMock(t)
	mClock.Set(wakeupTestSlot)
	server := newUnstartedServer(t, f.pubsub, f.db, withInternalTestServerClock(mClock))
	server.experiments = codersdk.Experiments{codersdk.ExperimentForkChatWakeups}
	tools, err := server.appendForkTools(ctx, nil, chat, chat.LastModelConfigID)
	require.NoError(t, err)
	fdb, err := forkdb.From(f.db)
	require.NoError(t, err)
	wakeups := func() []forkdb.ChatWakeup {
		t.Helper()
		rows, err := fdb.ListChatWakeupsByChatID(testutil.Context(t, testutil.WaitShort), chat.ID)
		require.NoError(t, err)
		return rows
	}
	schedule := func(args scheduleWakeupArgs, wantError bool) scheduleWakeupResult {
		t.Helper()
		content := runChatTodoTool(workspacesdk.WithToolCallID(ctx, uuid.New()), t, tools, scheduleWakeupToolName, args, wantError)
		var result scheduleWakeupResult
		if !wantError {
			require.NoError(t, json.Unmarshal([]byte(content), &result), content)
		}
		return result
	}

	delay := schedule(scheduleWakeupArgs{Prompt: " check the deploy ", DelaySeconds: new(90), MaxRuns: new(1)}, false)
	rows := wakeups()
	require.Len(t, rows, 1)
	require.Equal(t, delay.ID, rows[0].ID)
	require.Equal(t, forkdb.ChatWakeupKindDelay, rows[0].Kind)
	require.Equal(t, wakeupSourceTool, rows[0].Source)
	require.Equal(t, "check the deploy", rows[0].Prompt)
	require.Equal(t, chat.OwnerID, rows[0].CreatedBy)
	require.True(t, wakeupTestSlot.Add(90*time.Second).Equal(rows[0].NextRunAt.Time), "next run %s", rows[0].NextRunAt.Time)
	require.True(t, rows[0].NextRunAt.Time.Equal(delay.NextRunAt))
	require.Equal(t, new(int32(1)), delay.MaxRuns)
	require.Nil(t, delay.ExpiresAt)

	warsaw, err := time.LoadLocation("Europe/Warsaw")
	require.NoError(t, err)
	cron := schedule(scheduleWakeupArgs{Prompt: "standup", Cron: new("0 9 * * *"), Timezone: new("Europe/Warsaw")}, false)
	// 12:00 UTC is 14:00 in Warsaw, so the next 09:00 is tomorrow.
	wantCron := time.Date(2026, 5, 30, 9, 0, 0, 0, warsaw)
	require.True(t, wantCron.Equal(cron.NextRunAt), "next run %s, want %s", cron.NextRunAt, wantCron)
	require.Equal(t, forkdb.ChatWakeupKindCron, cron.Kind)
	require.Len(t, wakeups(), 2)

	for name, args := range map[string]scheduleWakeupArgs{
		"TwoKinds":        {Prompt: "x", DelaySeconds: new(60), IntervalSeconds: new(60)},
		"ShortInterval":   {Prompt: "x", IntervalSeconds: new(30)},
		"UnknownTimezone": {Prompt: "x", Cron: new("0 9 * * *"), Timezone: new("Mars/Olympus_Mons")},
		"CronNeverRuns":   {Prompt: "x", Cron: new("0 0 31 2 *")},
	} {
		t.Logf("case %s", name)
		schedule(args, true)
	}
	require.Len(t, wakeups(), 2, "refused calls create nothing")

	tool := findToolByName(tools, scheduleWakeupToolName)
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: uuid.NewString(), Name: scheduleWakeupToolName, Input: `{"prompt":"x","delay_seconds":60}`})
	require.NoError(t, err)
	require.True(t, resp.IsError, "a call without an occurrence ID cannot be replayed safely")
	require.Len(t, wakeups(), 2)

	// A replayed tool call returns the wake-up it created the first time.
	callCtx := workspacesdk.WithToolCallID(ctx, uuid.New())
	call := fantasy.ToolCall{ID: uuid.NewString(), Name: scheduleWakeupToolName, Input: `{"prompt":"replayed","interval_seconds":60}`}
	var replayed [2]scheduleWakeupResult
	for i := range replayed {
		resp, err := tool.Run(callCtx, call)
		require.NoError(t, err)
		require.False(t, resp.IsError, resp.Content)
		require.NoError(t, json.Unmarshal([]byte(resp.Content), &replayed[i]))
	}
	require.Equal(t, replayed[0].ID, replayed[1].ID)
	require.Len(t, wakeups(), 3)

	// That holds after the first run time has passed too: at 12:45 the next
	// 12:30 is tomorrow, past the expiry.
	spec := wakeupSpec{source: wakeupSourceTool, kind: forkdb.ChatWakeupKindCron, prompt: "daily", cron: "30 12 * * *", expiresInSeconds: 3600}
	id := uuid.New()
	created, err := server.createChatWakeup(ctx, f.db, chat, id, chat.OwnerID, spec, wakeupTestSlot)
	require.NoError(t, err)
	again, err := server.createChatWakeup(ctx, f.db, chat, id, chat.OwnerID, spec, wakeupTestSlot.Add(45*time.Minute))
	require.NoError(t, err)
	require.Equal(t, created.ID, again.ID)
	require.Len(t, wakeups(), 4)

	for i := range maxChatWakeups - 4 {
		schedule(scheduleWakeupArgs{Prompt: fmt.Sprintf("poll %d", i), IntervalSeconds: new(60)}, false)
	}
	schedule(scheduleWakeupArgs{Prompt: "one too many", IntervalSeconds: new(60)}, true)
	require.Len(t, wakeups(), maxChatWakeups)
}
