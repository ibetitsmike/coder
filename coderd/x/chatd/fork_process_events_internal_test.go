package chatd

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/externalauth/gitprovider"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk/agentconnmock"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/quartz"
)

func TestWaitForEventsProcesses(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := chatdTestContext(t)
	// The turn's snapshot of chat predates its workspace binding.
	chat := f.createWakeupChat(t)
	f.createChildAgent(t, chat, "running", database.ChatStatusRunning)
	_, _, agent := f.bindProcessWorkspace(t, chat)
	unbound := f.createWakeupChat(t)

	mClock := quartz.NewMock(t)
	mClock.Set(wakeupTestSlot)
	server := newUnstartedServer(t, f.pubsub, f.db, withInternalTestServerClock(mClock))
	server.experiments = codersdk.Experiments{codersdk.ExperimentForkChatWakeups}
	conn := processTestConn(t, server, chat, agent)
	conn.set("proc-1", workspacesdk.ForkWatchProcessResponse{Known: true, Running: true, Command: "sleep 60"})
	conn.set("proc-3", workspacesdk.ForkWatchProcessResponse{Known: true, Running: true, Command: "tail -f log"})
	conn.set("proc-4", workspacesdk.ForkWatchProcessResponse{Known: true, Running: true, Command: "sleep 1"})
	conn.set("proc-5", workspacesdk.ForkWatchProcessResponse{Known: true, Running: true, Command: "make test"})

	// Duplicates are watched once, and processes alone watch no children.
	watchID := uuid.New()
	watch, _ := callWaitForEvents(ctx, t, server, chat, watchID, waitForEventsArgs{ProcessIDs: []string{" proc-1 ", "proc-1"}}, false)
	want := []waitForEventsProcess{{ProcessID: "proc-1", Command: "sleep 60"}}
	require.Equal(t, want, watch.Processes)
	require.Empty(t, watch.Children)
	require.Empty(t, watch.Warning)
	row := f.wakeup(t, watchID)
	require.Equal(t, defaultPullRequestWaitPrompt, row.Prompt)
	require.JSONEq(t, fmt.Sprintf(`[{"type":"process_exit","process_id":"proc-1","command":"sleep 60","agent_id":%q}]`, agent.ID), string(f.wakeupEvents(t, row.ID)))
	// The call reads the process, then registers the watch of the
	// committed wake-up.
	require.Equal(t, []processWatchCall{
		{ID: "proc-1"},
		{ID: "proc-1", Req: workspacesdk.ForkWatchProcessRequest{WakeupID: watchID}},
	}, conn.takeCalls())

	// A replayed call does not read the process again, and registers the
	// watch again.
	replayed, _ := callWaitForEvents(ctx, t, server, chat, watchID, waitForEventsArgs{ProcessIDs: []string{"proc-1"}}, false)
	require.Equal(t, want, replayed.Processes)
	require.Equal(t, []processWatchCall{
		{ID: "proc-1", Req: workspacesdk.ForkWatchProcessRequest{WakeupID: watchID}},
	}, conn.takeCalls())

	// An output pattern is stored with the defaults it resolved and
	// registered with the agent.
	patternID := uuid.New()
	patterned, _ := callWaitForEvents(ctx, t, server, chat, patternID, waitForEventsArgs{ProcessIDs: []string{"proc-3"}, ProcessOutputPattern: "ERROR"}, false)
	require.Equal(t, []waitForEventsProcess{{ProcessID: "proc-3", Command: "tail -f log", OutputPattern: "ERROR"}}, patterned.Processes)
	require.JSONEq(t, fmt.Sprintf(`[{"type":"process_exit","process_id":"proc-3","command":"tail -f log","agent_id":%q,"output_pattern":"ERROR","output_cooldown_seconds":60,"output_max_events":10}]`, agent.ID), string(f.wakeupEvents(t, patternID)))
	require.Equal(t, []processWatchCall{
		{ID: "proc-3"},
		{ID: "proc-3", Req: workspacesdk.ForkWatchProcessRequest{WakeupID: patternID, OutputPattern: "ERROR", OutputCooldownSeconds: 60}},
	}, conn.takeCalls())

	// A registration that fails after the commit keeps the wake-up and
	// warns.
	conn.setOnWatch(func(id string, req workspacesdk.ForkWatchProcessRequest) error {
		if req.WakeupID != uuid.Nil {
			return xerrors.New("connection reset")
		}
		return nil
	})
	warnedID := uuid.New()
	warned, _ := callWaitForEvents(ctx, t, server, chat, warnedID, waitForEventsArgs{ProcessIDs: []string{"proc-5"}}, false)
	require.Contains(t, warned.Warning, "did not confirm the watch on proc-5 (connection reset)")
	require.Equal(t, forkdb.ChatWakeupStatusActive, f.wakeup(t, warnedID).Status)
	conn.setOnWatch(nil)
	conn.takeCalls()

	tooMany := make([]string, 0, maxWaitForEventsProcesses+1)
	for i := range maxWaitForEventsProcesses + 1 {
		tooMany = append(tooMany, fmt.Sprintf("proc-%d", i+10))
	}
	fresh := []string{"proc-4"}
	for name, tc := range map[string]struct {
		chat    database.Chat
		args    waitForEventsArgs
		wantErr string
	}{
		"AlreadyWatched":         {chat, waitForEventsArgs{ProcessIDs: []string{"proc-1"}}, "already watched by wake-up " + watchID.String()},
		"NotAProcess":            {chat, waitForEventsArgs{ProcessIDs: []string{"unknown"}}, `process "unknown" is not a process of this chat`},
		"NoAgent":                {unbound, waitForEventsArgs{ProcessIDs: []string{"proc-1"}}, "this chat has no workspace agent"},
		"TooMany":                {chat, waitForEventsArgs{ProcessIDs: tooMany}, "the maximum is 10"},
		"EmptyID":                {chat, waitForEventsArgs{ProcessIDs: []string{" "}}, "must not contain an empty ID"},
		"PatternWithoutProcess":  {chat, waitForEventsArgs{ProcessOutputPattern: "ERROR"}, "process_output_pattern requires process_ids"},
		"CooldownWithoutPattern": {chat, waitForEventsArgs{ProcessIDs: fresh, ProcessOutputCooldownSeconds: new(10)}, "require process_output_pattern"},
		"InvalidPattern":         {chat, waitForEventsArgs{ProcessIDs: fresh, ProcessOutputPattern: "("}, "invalid process_output_pattern"},
		"MaxEventsOutOfRange":    {chat, waitForEventsArgs{ProcessIDs: fresh, ProcessOutputPattern: "ERROR", ProcessOutputMaxEvents: new(0)}, "process_output_max_events must be between 1 and 100"},
	} {
		t.Logf("case %s", name)
		_, content := callWaitForEvents(ctx, t, server, tc.chat, uuid.New(), tc.args, true)
		require.Contains(t, content, tc.wantErr)
	}
	// An expired watch that has not had its final poll still watches.
	expired := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
		Kind:      forkdb.ChatWakeupKindEvents,
		ExpiresAt: sql.NullTime{Time: wakeupTestSlot.Add(-time.Minute), Valid: true},
	}, processExitEvents(t, agent.ID, "proc-2")...)
	_, content := callWaitForEvents(ctx, t, server, chat, uuid.New(), waitForEventsArgs{ProcessIDs: []string{"proc-2"}}, true)
	require.Contains(t, content, "already watched by wake-up "+expired.ID.String())

	// An agent without the process watch API fails the call: it answers
	// the route with a 404, or its connection lacks the method.
	conn.setTooOld(true)
	_, content = callWaitForEvents(ctx, t, server, chat, uuid.New(), waitForEventsArgs{ProcessIDs: fresh}, true)
	require.Contains(t, content, processAgentTooOld)
	conn.setTooOld(false)
	plain := agentconnmock.NewMockAgentConn(gomock.NewController(t))
	connFn := server.agentConnFn
	server.agentConnFn = func(context.Context, uuid.UUID) (workspacesdk.AgentConn, func(), error) {
		return plain, func() {}, nil
	}
	_, content = callWaitForEvents(ctx, t, server, chat, uuid.New(), waitForEventsArgs{ProcessIDs: fresh}, true)
	require.Contains(t, content, processAgentTooOld)
	server.agentConnFn = connFn

	// Parallel calls in one step would all pass the guard before either
	// creates its wake-up.
	tools, err := server.appendForkTools(ctx, nil, chat, chat.LastModelConfigID)
	require.NoError(t, err)
	serial, ok := findToolByName(tools, waitForEventsToolName).(interface{ SerialToolCalls() bool })
	require.True(t, ok && serial.SerialToolCalls(), "wait_for_events calls must run one at a time")
	// Refused process IDs fail the call before it reads the pull request.
	fake := &fakePullRequestActivity{activity: openPullRequest(gitprovider.CheckStatePending)}
	server.forkPullRequestActivity = fake.fetch
	_, content = callWaitForEvents(ctx, t, server, chat, uuid.New(), waitForEventsArgs{ProcessIDs: tooMany, PullRequestURL: testPullRequestURL}, true)
	require.Contains(t, content, "the maximum is 10")
	require.Empty(t, fake.calls)
	fdb, err := forkdb.From(f.db)
	require.NoError(t, err)
	rows, err := fdb.ListChatWakeupsByChatID(ctx, forkdb.ListChatWakeupsByChatIDParams{ChatID: chat.ID})
	require.NoError(t, err)
	require.Len(t, rows, 4, "refused calls create nothing")
}

// A watched process wakes the chat once, when its agent reports the exit,
// with the end of its output. The delivery does not dial the agent.
func TestWakeupEventsProcessExit(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	chat := f.createWakeupChat(t)
	_, _, agent := f.bindProcessWorkspace(t, chat)
	wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
		Kind: forkdb.ChatWakeupKindEvents,
	}, processExitEvents(t, agent.ID, "proc-1")...)
	worker := f.newArchiveWorker(t, nil, nil, nil)
	dials := failingAgentConn(worker.server)
	now := wakeupTestSlot
	tick := func() {
		t.Helper()
		now = now.Add(eventsPollInterval + time.Second)
		worker.wakeupOnce(ctx, now)
	}

	tick()
	require.Empty(t, f.wakeMessages(t, chat, wakeup.Prompt), "the process still runs")
	require.Equal(t, forkdb.ChatWakeupStatusActive, f.wakeup(t, wakeup.ID).Status)

	// Another process of the chat, or the process on another agent,
	// fires nothing.
	require.Empty(t, f.fireProcessExit(t, chat, agent.ID, "proc-2", 0, "", now))
	require.Empty(t, f.fireProcessExit(t, chat, uuid.New(), "proc-1", 0, "", now))
	require.Equal(t, []uuid.UUID{chat.ID}, f.fireProcessExit(t, chat, agent.ID, "proc-1", 0, "ok\nDONE\n", now))
	tick()
	require.Equal(t, []string{
		wakeup.Prompt + "\n\nBackground process proc-1 (`sleep 600`) exited with code 0.\n\nLast 2 lines of output:\n```\nok\nDONE\n```",
	}, f.wakeMessages(t, chat, wakeup.Prompt))
	require.Equal(t, []string{"proc-1"}, f.deliveredWatches(t, wakeup.ID))
	got := f.wakeup(t, wakeup.ID)
	require.Equal(t, forkdb.ChatWakeupStatusCompleted, got.Status)
	require.Equal(t, "done", got.StatusReason.String)

	// The agent sends an exit again when it missed the ack.
	require.Empty(t, f.fireProcessExit(t, chat, agent.ID, "proc-1", 0, "ok\nDONE\n", now))
	forceExecutionState(t, f, chat.ID, database.ChatStatusWaiting, false)
	tick()
	require.Len(t, f.wakeMessages(t, chat, wakeup.Prompt), 1)
	require.Zero(t, dials.Load(), "a delivery does not dial the agent")
}

// An exit that the agent reports while the wake-up is paused is kept, and
// the wake-up delivers it once it is resumed.
func TestWakeupEventsProcessExitWhilePaused(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	chat := f.createWakeupChat(t)
	_, _, agent := f.bindProcessWorkspace(t, chat)
	wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
		Kind: forkdb.ChatWakeupKindEvents,
	}, processExitEvents(t, agent.ID, "proc-1")...)
	worker := f.newArchiveWorker(t, nil, nil, nil)
	failingAgentConn(worker.server)
	now := wakeupTestSlot
	tick := func() {
		t.Helper()
		now = now.Add(eventsPollInterval + time.Second)
		worker.wakeupOnce(ctx, now)
	}

	_, paused, err := worker.server.SetChatWakeupStatus(ctx, chat, wakeup.ID, forkdb.ChatWakeupStatusPaused)
	require.NoError(t, err)
	require.Equal(t, forkdb.ChatWakeupStatusPaused, paused.Status)
	require.Equal(t, []uuid.UUID{chat.ID}, f.fireProcessExit(t, chat, agent.ID, "proc-1", 0, "ok\nDONE\n", now))
	tick()
	require.Empty(t, f.wakeMessages(t, chat, wakeup.Prompt), "a paused wake-up delivers nothing")

	_, resumed, err := worker.server.SetChatWakeupStatus(ctx, chat, wakeup.ID, forkdb.ChatWakeupStatusActive)
	require.NoError(t, err)
	require.Equal(t, forkdb.ChatWakeupStatusActive, resumed.Status)
	tick()
	require.Equal(t, []string{
		wakeup.Prompt + "\n\nBackground process proc-1 (`sleep 600`) exited with code 0.\n\nLast 2 lines of output:\n```\nok\nDONE\n```",
	}, f.wakeMessages(t, chat, wakeup.Prompt))
	require.Equal(t, forkdb.ChatWakeupStatusCompleted, f.wakeup(t, wakeup.ID).Status)
}

// A watch with an output pattern wakes the chat for the matching lines that
// its agent reports, collects the lines of the reports before a delivery,
// stops after its maximum and still reports the exit.
func TestWakeupEventsProcessOutput(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	chat := f.createWakeupChat(t)
	_, _, agent := f.bindProcessWorkspace(t, chat)
	events := []wakeupEvent{{
		Type: wakeupEventProcessExit, ProcessID: "proc-1", Command: "sleep 600", AgentID: agent.ID,
		OutputPattern: "ERROR", OutputCooldownSeconds: 60, OutputMaxEvents: 2,
	}}
	wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{Kind: forkdb.ChatWakeupKindEvents}, events...)
	// A watch without a pattern takes no lines.
	plainChat := f.createWakeupChat(t)
	plain := f.insertWakeup(t, plainChat, forkdb.InsertChatWakeupParams{Kind: forkdb.ChatWakeupKindEvents},
		processExitEvents(t, agent.ID, "proc-1")...)
	worker := f.newArchiveWorker(t, nil, nil, nil)
	now := wakeupTestSlot
	tick := func(step time.Duration) {
		t.Helper()
		forceExecutionState(t, f, chat.ID, database.ChatStatusWaiting, false)
		now = now.Add(step)
		worker.wakeupOnce(ctx, now)
	}
	prefix := wakeup.Prompt + "\n\nBackground process proc-1 (`sleep 600`) "
	firstMessage := prefix + "is still running and printed 1 new line matching `ERROR`.\n\nMatching lines:\n```\nERROR one\n```"
	secondMessage := prefix + "is still running and printed 2 new lines matching `ERROR`.\n\nMatching lines:\n```\npartial ERROR two\nERROR three\n```" +
		"\n\nThis is the last message for matching lines of this process. Its exit still wakes this chat."

	require.Equal(t, []uuid.UUID{chat.ID}, f.fireProcessLines(t, chat, agent.ID, "proc-1", wakeup.ID, "ERROR one"))
	require.Empty(t, f.fireProcessLines(t, plainChat, agent.ID, "proc-1", plain.ID, "ERROR one"))
	tick(time.Second)
	require.Equal(t, []string{firstMessage}, f.wakeMessages(t, chat, wakeup.Prompt))
	require.Equal(t, forkdb.ChatWakeupStatusActive, f.wakeup(t, wakeup.ID).Status, "the process still runs")

	tick(eventsPollInterval + time.Second)
	require.Len(t, f.wakeMessages(t, chat, wakeup.Prompt), 1, "nothing new")

	// Two reports before a delivery make one message.
	require.NotEmpty(t, f.fireProcessLines(t, chat, agent.ID, "proc-1", wakeup.ID, "partial ERROR two"))
	require.NotEmpty(t, f.fireProcessLines(t, chat, agent.ID, "proc-1", wakeup.ID, "ERROR three"))
	tick(time.Minute)
	require.Equal(t, []string{firstMessage, secondMessage}, f.wakeMessages(t, chat, wakeup.Prompt))

	require.Empty(t, f.fireProcessLines(t, chat, agent.ID, "proc-1", wakeup.ID, "ERROR four"), "no lines after the maximum")
	tick(2 * time.Minute)
	require.Len(t, f.wakeMessages(t, chat, wakeup.Prompt), 2)

	require.NotEmpty(t, f.fireProcessExit(t, chat, agent.ID, "proc-1", 1, "ERROR four\n", now))
	tick(time.Second)
	messages := f.wakeMessages(t, chat, wakeup.Prompt)
	require.Len(t, messages, 3)
	require.True(t, strings.HasPrefix(messages[2], prefix+"exited with code 1."), messages[2])
	require.Equal(t, []string{"proc-1"}, f.deliveredWatches(t, wakeup.ID))
	fdb, err := forkdb.From(f.db)
	require.NoError(t, err)
	watches, err := fdb.ListChatWakeupWatchesByWakeupID(ctx, wakeup.ID)
	require.NoError(t, err)
	require.Len(t, watches, 1)
	require.EqualValues(t, 3, watches[0].Deliveries, "two line messages and the exit")
	got := f.wakeup(t, wakeup.ID)
	require.Equal(t, forkdb.ChatWakeupStatusCompleted, got.Status)
	require.Equal(t, "done", got.StatusReason.String)
}

// Output lines that fire during the cool-down after the last message of the
// wake-up wait for its end: the wake-up is not due before it, and its run
// time is the end of the cool-down.
func TestWakeupEventsProcessOutputCooldown(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	chat := f.createWakeupChat(t)
	_, _, agent := f.bindProcessWorkspace(t, chat)
	wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
		Kind:      forkdb.ChatWakeupKindEvents,
		ExpiresAt: sql.NullTime{Time: wakeupTestSlot.Add(time.Hour), Valid: true},
	}, wakeupEvent{
		Type: wakeupEventProcessExit, ProcessID: "proc-1", Command: "sleep 600", AgentID: agent.ID,
		OutputPattern: "ERROR", OutputCooldownSeconds: 60, OutputMaxEvents: 10,
	})
	worker := f.newArchiveWorker(t, nil, nil, nil)
	fdb, err := forkdb.From(f.db)
	require.NoError(t, err)
	now := wakeupTestSlot
	tick := func(step time.Duration) {
		t.Helper()
		forceExecutionState(t, f, chat.ID, database.ChatStatusWaiting, false)
		now = now.Add(step)
		worker.wakeupOnce(ctx, now)
	}
	due := func() bool {
		t.Helper()
		rows, err := fdb.ListDueChatWakeups(ctx, forkdb.ListDueChatWakeupsParams{
			ChatID: uuid.NullUUID{UUID: chat.ID, Valid: true}, Now: now, LimitCount: 10,
		})
		require.NoError(t, err)
		for _, row := range rows {
			if row.ChatWakeup.ID == wakeup.ID && row.Due {
				return true
			}
		}
		return false
	}

	require.NotEmpty(t, f.fireProcessLines(t, chat, agent.ID, "proc-1", wakeup.ID, "ERROR one"))
	tick(time.Second)
	require.Len(t, f.wakeMessages(t, chat, wakeup.Prompt), 1)
	first := f.wakeup(t, wakeup.ID)
	require.True(t, first.LastRunAt.Valid)
	cooldownEnd := first.LastRunAt.Time.Add(time.Minute)
	require.WithinDuration(t, cooldownEnd, first.NextRunAt.Time, 0, "the next run is the end of the cool-down")

	// Lines that fire 10 seconds after the message wait.
	now = now.Add(10 * time.Second)
	require.NotEmpty(t, f.fireProcessLines(t, chat, agent.ID, "proc-1", wakeup.ID, "ERROR two"))
	require.False(t, due(), "fired lines in the cool-down do not make the wake-up due")
	tick(0)
	require.Len(t, f.wakeMessages(t, chat, wakeup.Prompt), 1, "the cool-down runs")

	now = cooldownEnd.Add(-time.Second)
	require.False(t, due())
	tick(time.Second)
	messages := f.wakeMessages(t, chat, wakeup.Prompt)
	require.Len(t, messages, 2)
	require.Contains(t, messages[1], "ERROR two")
}

// A failed send for matching lines keeps the lines in the payload, and an
// exit that fires while they are held back folds them into the exit message.
// The watch ends with that one message and no line message is sent twice.
func TestWakeupEventsProcessLinesOnExit(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	chat := f.createWakeupChat(t)
	_, _, agent := f.bindProcessWorkspace(t, chat)
	events := []wakeupEvent{{
		Type: wakeupEventProcessExit, ProcessID: "proc-1", Command: "sleep 600", AgentID: agent.ID,
		OutputPattern: "ERROR", OutputCooldownSeconds: 60, OutputMaxEvents: 3,
	}}
	wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{Kind: forkdb.ChatWakeupKindEvents}, events...)
	worker := f.newArchiveWorker(t, nil, nil, nil)
	now := wakeupTestSlot
	tick := func(step time.Duration) {
		t.Helper()
		forceExecutionState(t, f, chat.ID, database.ChatStatusWaiting, false)
		now = now.Add(step)
		worker.wakeupOnce(ctx, now)
	}
	prefix := wakeup.Prompt + "\n\nBackground process proc-1 (`sleep 600`) "
	linesMessage := prefix + "is still running and printed 1 new line matching `ERROR`.\n\nMatching lines:\n```\nERROR one\n```"
	exitMessage := prefix + "exited with code 1." +
		"\n\nLast 1 lines of output:\n```\ndone\n```" +
		"\n\nBefore it exited, it printed 1 new line matching `ERROR`.\n\nMatching lines:\n```\nERROR two\n```"

	// The first line delivers.
	require.NotEmpty(t, f.fireProcessLines(t, chat, agent.ID, "proc-1", wakeup.ID, "ERROR one"))
	tick(time.Second)
	require.Equal(t, []string{linesMessage}, f.wakeMessages(t, chat, wakeup.Prompt))
	first := f.wakeup(t, wakeup.ID)
	require.True(t, first.LastRunAt.Valid)

	// A second line fires, but every send of its message fails. Lines that
	// fire in the cool-down after the last message wait for its end, so the
	// failing pass runs one cool-down after the delivered one.
	require.NotEmpty(t, f.fireProcessLines(t, chat, agent.ID, "proc-1", wakeup.ID, "ERROR two"))
	var prompts atomic.Int32
	useUserPromptHook(t, worker, func(rw http.ResponseWriter) {
		if prompts.Add(1) == 1 {
			rw.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = rw.Write([]byte(`{}`))
	})
	tick(first.LastRunAt.Time.Add(time.Minute + time.Second).Sub(now))
	require.Len(t, f.wakeMessages(t, chat, wakeup.Prompt), 1, "the send failed")
	require.Equal(t, []string{"delivered", "failed"}, f.wakeupRunStatuses(t, wakeup.ID))

	// The process exits while the second line is still held back. After the
	// failed send the wake-up runs again eventsPollInterval later, and that
	// retry delivers the exit and folds the line in.
	useUserPromptHook(t, worker, func(rw http.ResponseWriter) {
		_, _ = rw.Write([]byte(`{}`))
	})
	require.NotEmpty(t, f.fireProcessExit(t, chat, agent.ID, "proc-1", 1, "done\n", now))
	tick(eventsPollInterval + time.Second)
	messages := f.wakeMessages(t, chat, wakeup.Prompt)
	require.Equal(t, []string{linesMessage, exitMessage}, messages)
	require.Equal(t, []string{"delivered", "failed", "delivered"}, f.wakeupRunStatuses(t, wakeup.ID))
	require.NotContains(t, messages[1], "Last 2 lines of output", "the exit tail and the folded lines stay apart")

	require.Equal(t, []string{"proc-1"}, f.deliveredWatches(t, wakeup.ID))
	got := f.wakeup(t, wakeup.ID)
	require.Equal(t, forkdb.ChatWakeupStatusCompleted, got.Status)
	require.Equal(t, "done", got.StatusReason.String)
	fdb, err := forkdb.From(f.db)
	require.NoError(t, err)
	watches, err := fdb.ListChatWakeupWatchesByWakeupID(ctx, wakeup.ID)
	require.NoError(t, err)
	require.Len(t, watches, 1)
	require.EqualValues(t, 2, watches[0].Deliveries, "the line message and the exit message")

	// A resent exit delivers nothing again.
	require.Empty(t, f.fireProcessExit(t, chat, agent.ID, "proc-1", 1, "done\n", now))
	forceExecutionState(t, f, chat.ID, database.ChatStatusWaiting, false)
	tick(eventsPollInterval + time.Second)
	require.Len(t, f.wakeMessages(t, chat, wakeup.Prompt), 2)
}

// A watch whose process already exited or that the agent does not know
// fires when it is registered, and one whose workspace stopped or whose
// agent restarted fires on the sweep.
func TestWakeupEventsProcessOutcomes(t *testing.T) {
	t.Parallel()
	exitedAt := wakeupTestSlot.Add(-time.Minute).Unix()
	for _, tc := range []struct {
		name string
		// stopped adds a stop build after the build that ran the process.
		stopped bool
		// agentStarted, when not zero, is when the agent started, after the
		// wake-up was created. A positive one is a restart of the agent.
		agentStarted time.Duration
		process      workspacesdk.ForkWatchProcessResponse
		// wantText is the message after the prompt, or "" for none.
		wantText string
	}{
		{
			name:     "Running",
			process:  workspacesdk.ForkWatchProcessResponse{Known: true, Running: true, Command: "sleep 600"},
			wantText: "",
		},
		{
			name:     "AlreadyExited",
			process:  workspacesdk.ForkWatchProcessResponse{Known: true, Command: "sleep 600", ExitCode: new(3), ExitedAt: &exitedAt, OutputTail: "bye\n"},
			wantText: "Background process proc-1 (`sleep 600`) exited with code 3.\n\nLast 1 lines of output:\n```\nbye\n```",
		},
		{
			name:     "NoExitCode",
			process:  workspacesdk.ForkWatchProcessResponse{Known: true, Command: "sleep 600"},
			wantText: "Background process proc-1 (`sleep 600`) exited without an exit code (killed by a signal).",
		},
		{
			name:     "KilledBySignal",
			process:  workspacesdk.ForkWatchProcessResponse{Known: true, Command: "sleep 600", ExitCode: new(-1)},
			wantText: "Background process proc-1 (`sleep 600`) exited without an exit code (killed by a signal).",
		},
		{
			name:     "UnknownToAgent",
			wantText: "Lost the watch on background process proc-1 (`sleep 600`): the workspace agent no longer knows it (the agent restarted or the process exited more than an hour ago).",
		},
		{
			name:     "WorkspaceStopped",
			stopped:  true,
			wantText: "Lost the watch on background process proc-1 (`sleep 600`): the workspace stopped or was rebuilt, so the process is gone.",
		},
		{
			name:         "AgentRestarted",
			agentStarted: 30 * time.Minute,
			wantText:     "Lost the watch on background process proc-1 (`sleep 600`): the workspace agent restarted, so it no longer runs the process.",
		},
		{
			name:         "AgentStartedBefore",
			agentStarted: -time.Minute,
			process:      workspacesdk.ForkWatchProcessResponse{Known: true, Running: true, Command: "sleep 600"},
			wantText:     "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newWorkerTestFixture(t)
			ctx := testutil.Context(t, testutil.WaitLong)
			chat := f.createWakeupChat(t)
			workspace, build, agent := f.bindProcessWorkspace(t, chat)
			wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
				Kind: forkdb.ChatWakeupKindEvents,
			}, processExitEvents(t, agent.ID, "proc-1")...)
			worker := f.newArchiveWorker(t, nil, nil, nil)
			now := wakeupTestSlot.Add(time.Second)
			if tc.agentStarted != 0 {
				_, err := f.sqlDB.ExecContext(ctx, "UPDATE workspace_agents SET started_at = $1 WHERE id = $2",
					wakeup.CreatedAt.Add(tc.agentStarted), agent.ID)
				require.NoError(t, err)
			}
			switch {
			case tc.stopped:
				f.stopProcessWorkspace(t, workspace, build, time.Time{})
				f.sweepProcessWatches(t, worker.server, now)
			case tc.agentStarted > 0:
				f.sweepProcessWatches(t, worker.server, now)
			default:
				conn := processTestConn(t, worker.server, chat, agent)
				conn.set("proc-1", tc.process)
				require.Empty(t, worker.server.registerProcessWatches(dbauthz.AsChatd(ctx), chat, wakeup))
				// The sweep leaves a watch on the running build alone.
				f.sweepProcessWatches(t, worker.server, now)
			}

			worker.wakeupOnce(ctx, now)
			got := f.wakeup(t, wakeup.ID)
			if tc.wantText == "" {
				require.Empty(t, f.wakeMessages(t, chat, wakeup.Prompt))
				require.Equal(t, forkdb.ChatWakeupStatusActive, got.Status)
				return
			}
			require.Equal(t, []string{wakeup.Prompt + "\n\n" + tc.wantText}, f.wakeMessages(t, chat, wakeup.Prompt))
			require.Equal(t, []string{"proc-1"}, f.deliveredWatches(t, wakeup.ID))
			require.Equal(t, forkdb.ChatWakeupStatusCompleted, got.Status)
		})
	}
}

// A final poll well after the expiry delivers only a process that provably
// ended by the expiry, and no final poll delivers output lines. A final poll
// with a process that has not ended waits until its agent said, a margin
// after the expiry, that it holds no unacked event, or until the report
// window passes.
func TestWakeupFinalPollProcesses(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		late time.Duration
		// exitedAt, when set, is when the agent reported the exit, relative
		// to the expiry.
		exitedAt *time.Duration
		// lines fires matching lines.
		lines bool
		// stoppedAt, when set, is when a stop build was created, relative to
		// the expiry.
		stoppedAt *time.Duration
		// reportedAt, when set, is when the agent last said that it holds
		// no unacked event, relative to the expiry.
		reportedAt *time.Duration
		// reportExit, when set, is the exit time, relative to the expiry,
		// that the agent reports after the first poll, which a second poll
		// eventsPollInterval later delivers.
		reportExit  *time.Duration
		wantMessage bool
		// wantReason is the reason the wake-up completed with, or "" when it
		// stays active for another poll.
		wantReason string
	}{
		{name: "OnTime", late: time.Second, exitedAt: new(time.Second), wantMessage: true, wantReason: "done"},
		{name: "OnTimeLines", late: time.Second, lines: true},
		{name: "LateLines", late: time.Minute, lines: true, reportedAt: new(processReportMargin), wantReason: "expired"},
		{name: "LateExitedAfterExpiry", late: time.Minute, exitedAt: new(30 * time.Second), wantReason: "expired"},
		{name: "LateExitedBeforeExpiry", late: time.Minute, exitedAt: new(-30 * time.Second), wantMessage: true, wantReason: "done"},
		{name: "LateExitedInExpirySecond", late: time.Minute, exitedAt: new(time.Duration(0)), wantReason: "expired"},
		{name: "LateNotFired", late: time.Minute, reportedAt: new(processReportMargin), wantReason: "expired"},
		{name: "LateNotFiredNeverReported", late: time.Minute},
		{name: "LateNotFiredReportedBeforeExpiry", late: time.Minute, reportedAt: new(-time.Second)},
		{name: "LateNotFiredReportedInMargin", late: time.Minute, reportedAt: new(processReportMargin - time.Second)},
		{name: "PastReportWindow", late: processFinalReportWindow, reportedAt: new(-time.Minute), wantReason: "expired"},
		{name: "ReportAfterRetry", late: time.Minute, reportedAt: new(-time.Second), reportExit: new(-30 * time.Second), wantMessage: true, wantReason: "done"},
		{name: "LateStoppedAfterExpiry", late: time.Minute, stoppedAt: new(30 * time.Second), wantReason: "expired"},
		{name: "LateStoppedBeforeExpiry", late: time.Minute, stoppedAt: new(-30 * time.Second), wantMessage: true, wantReason: "done"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newWorkerTestFixture(t)
			ctx := testutil.Context(t, testutil.WaitLong)
			chat := f.createWakeupChat(t)
			workspace, build, agent := f.bindProcessWorkspace(t, chat)
			// insertWakeup schedules the run at wakeupTestSlot, so this is the
			// final poll.
			wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
				Kind:      forkdb.ChatWakeupKindEvents,
				ExpiresAt: sql.NullTime{Time: wakeupTestSlot, Valid: true},
			}, wakeupEvent{
				Type: wakeupEventProcessExit, ProcessID: "proc-1", Command: "sleep 600", AgentID: agent.ID,
				OutputPattern: "ERROR", OutputCooldownSeconds: 60, OutputMaxEvents: 10,
			})
			worker := f.newArchiveWorker(t, nil, nil, nil)
			now := wakeupTestSlot.Add(tc.late)
			if tc.reportedAt != nil {
				f.markProcessReported(t, agent.ID, wakeupTestSlot.Add(*tc.reportedAt))
			}
			if tc.exitedAt != nil {
				require.NotEmpty(t, f.fireProcessExit(t, chat, agent.ID, "proc-1", 0, "", wakeupTestSlot.Add(*tc.exitedAt)))
			}
			if tc.lines {
				require.NotEmpty(t, f.fireProcessLines(t, chat, agent.ID, "proc-1", wakeup.ID, "ERROR"))
			}
			if tc.stoppedAt != nil {
				f.stopProcessWorkspace(t, workspace, build, wakeupTestSlot.Add(*tc.stoppedAt))
				f.sweepProcessWatches(t, worker.server, now)
			}

			worker.wakeupOnce(ctx, now)
			if tc.reportExit != nil {
				// The first poll waits for the agent, which reports the exit
				// once a retry of its report succeeds.
				require.Empty(t, f.wakeMessages(t, chat, wakeup.Prompt))
				got := f.wakeup(t, wakeup.ID)
				require.Equal(t, forkdb.ChatWakeupStatusActive, got.Status)
				require.NotEmpty(t, f.fireProcessExit(t, chat, agent.ID, "proc-1", 0, "", wakeupTestSlot.Add(*tc.reportExit)))
				// The pass adds the time it took to now, so the next poll is
				// due just after now plus eventsPollInterval.
				now = now.Add(eventsPollInterval + time.Second)
				worker.wakeupOnce(ctx, now)
			}
			if tc.wantMessage {
				require.Len(t, f.wakeMessages(t, chat, wakeup.Prompt), 1)
			} else {
				require.Empty(t, f.wakeMessages(t, chat, wakeup.Prompt))
			}
			got := f.wakeup(t, wakeup.ID)
			if tc.wantReason == "" {
				require.Equal(t, forkdb.ChatWakeupStatusActive, got.Status)
				require.WithinDuration(t, now.Add(eventsPollInterval), got.NextRunAt.Time, time.Second)
				return
			}
			require.Equal(t, forkdb.ChatWakeupStatusCompleted, got.Status)
			require.Equal(t, tc.wantReason, got.StatusReason.String)
		})
	}
}

// An agent that cannot be dialed delays no wake-up: the waker delivers the
// interval wake-up of another chat in its first pass, and a reported exit
// at once, without a dial.
func TestWakeupProcessAgentUnreachable(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	mClock := quartz.NewMock(t)
	mClock.Set(wakeupTestSlot.Add(time.Second))
	watching := f.createWakeupChat(t)
	_, _, agent := f.bindProcessWorkspace(t, watching)
	processWakeup := f.insertWakeup(t, watching, forkdb.InsertChatWakeupParams{
		Kind:      forkdb.ChatWakeupKindEvents,
		NextRunAt: sql.NullTime{Time: wakeupTestSlot.Add(-time.Minute), Valid: true},
	}, processExitEvents(t, agent.ID, "proc-1")...)
	other := f.createWakeupChat(t)
	interval := f.insertWakeup(t, other, forkdb.InsertChatWakeupParams{
		Kind:            forkdb.ChatWakeupKindInterval,
		IntervalSeconds: sql.NullInt32{Int32: 60, Valid: true},
	})

	server := newUnstartedServer(t, f.pubsub, f.db)
	server.experiments = codersdk.Experiments{codersdk.ExperimentForkChatWakeups}
	dials := failingAgentConn(server)
	opts := testOptions(t, f, nil)
	opts.Clock = mClock
	opts.Pubsub = server.pubsub
	worker, err := newChatWorker(server, opts)
	require.NoError(t, err)
	sweepTrap := mClock.Trap().NewTicker("chatd", "waker", "sweep")
	defer sweepTrap.Close()
	require.NoError(t, worker.Start(context.Background()))
	t.Cleanup(func() { require.NoError(t, worker.Close()) })

	sweepTrap.MustWait(ctx).MustRelease(ctx)
	testutil.Eventually(ctx, t, func(context.Context) bool {
		return f.countPromptMessages(t, other, interval.Prompt) == 1
	}, testutil.IntervalFast, "the first pass must deliver the interval wake-up")
	require.Empty(t, f.wakeMessages(t, watching, processWakeup.Prompt))

	// The report of the agent fires the watch and pokes the waker.
	require.NotEmpty(t, f.fireProcessExit(t, watching, agent.ID, "proc-1", 0, "", mClock.Now()))
	server.ForkPokeChatWakeups(watching.ID)
	testutil.Eventually(ctx, t, func(context.Context) bool {
		return len(f.wakeMessages(t, watching, processWakeup.Prompt)) == 1
	}, testutil.IntervalFast, "the poke must deliver the exit")
	require.Zero(t, dials.Load(), "the waker never dials an agent")
}

// Whatever the order of the commit of the watch row, the exit of the
// process and the read of its state, the chat gets one message for the
// exit.
func TestWaitForEventsProcessExitOrder(t *testing.T) {
	t.Parallel()
	running := workspacesdk.ForkWatchProcessResponse{Known: true, Running: true, Command: "make"}
	exitedAt := wakeupTestSlot.Unix()
	exited := workspacesdk.ForkWatchProcessResponse{Known: true, Command: "make", ExitCode: new(0), ExitedAt: &exitedAt}
	for _, tc := range []struct {
		name string
		// exitBeforeCall reports the exit before the call commits the watch
		// row, when the report fires no watch.
		exitBeforeCall bool
		// exitInRegister reports the exit after the commit and before the
		// registration reads the state.
		exitInRegister bool
		// exitAfterCall reports the exit after the registration read the
		// state.
		exitAfterCall bool
	}{
		{name: "ExitBeforeCommit", exitBeforeCall: true},
		{name: "ExitAfterCommitBeforeRead", exitInRegister: true},
		{name: "ExitAfterRead", exitAfterCall: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newWorkerTestFixture(t)
			ctx := chatdTestContext(t)
			chat := f.createWakeupChat(t)
			_, _, agent := f.bindProcessWorkspace(t, chat)
			worker := f.newArchiveWorker(t, nil, nil, nil)
			worker.server.experiments = codersdk.Experiments{codersdk.ExperimentForkChatWakeups}
			mClock := quartz.NewMock(t)
			mClock.Set(wakeupTestSlot)
			worker.server.clock = mClock
			conn := processTestConn(t, worker.server, chat, agent)
			conn.set("proc-1", running)
			exit := func() []uuid.UUID {
				conn.set("proc-1", exited)
				return f.fireProcessExit(t, chat, agent.ID, "proc-1", 0, "", time.Unix(exitedAt, 0).UTC())
			}
			if tc.exitBeforeCall {
				require.Empty(t, exit(), "no watch row yet")
			}
			if tc.exitInRegister {
				conn.setOnWatch(func(_ string, req workspacesdk.ForkWatchProcessRequest) error {
					if req.WakeupID != uuid.Nil {
						require.NotEmpty(t, exit(), "the watch row is committed")
					}
					return nil
				})
			}
			callID := uuid.New()
			result, _ := callWaitForEvents(ctx, t, worker.server, chat, callID, waitForEventsArgs{ProcessIDs: []string{"proc-1"}}, false)
			require.Empty(t, result.Warning)
			if tc.exitAfterCall {
				require.NotEmpty(t, exit())
			}
			prompt := f.wakeup(t, callID).Prompt

			now := wakeupTestSlot.Add(time.Second)
			worker.wakeupOnce(ctx, now)
			forceExecutionState(t, f, chat.ID, database.ChatStatusWaiting, false)
			worker.wakeupOnce(ctx, now.Add(time.Minute))
			require.Equal(t, []string{
				prompt + "\n\nBackground process proc-1 (`make`) exited with code 0.",
			}, f.wakeMessages(t, chat, prompt))
			require.Equal(t, []string{"proc-1"}, f.deliveredWatches(t, callID))
			require.Equal(t, forkdb.ChatWakeupStatusCompleted, f.wakeup(t, callID).Status)
		})
	}
}

func TestProcessFiredText(t *testing.T) {
	t.Parallel()
	event := wakeupEvent{Type: wakeupEventProcessExit, ProcessID: "proc-1", Command: "make", OutputPattern: "ERROR", OutputMaxEvents: 10}
	wakeup := func(expired bool) forkdb.ChatWakeup {
		w := forkdb.ChatWakeup{Kind: forkdb.ChatWakeupKindEvents}
		if expired {
			w.ExpiresAt = sql.NullTime{Time: wakeupTestSlot, Valid: true}
		}
		return w
	}
	watch := func(fired string, deliveries int32) forkdb.ChatWakeupWatch {
		w := forkdb.ChatWakeupWatch{Deliveries: max(deliveries, 1)}
		w.FiredPayload.RawMessage, w.FiredPayload.Valid = json.RawMessage(fired), true
		return w
	}
	prefix := "Background process proc-1 (`make`) "
	for _, tc := range []struct {
		name       string
		fired      string
		deliveries int32
		expired    bool
		wantText   string
		wantDone   bool
	}{
		{name: "Lines", fired: `{"matched":1,"lines":"ERROR a"}`, wantText: prefix + "is still running and printed 1 new line matching `ERROR`.\n\nMatching lines:\n```\nERROR a\n```"},
		{
			name: "LastMessage", fired: `{"matched":3,"lines":"ERROR b\nERROR c"}`, deliveries: 9,
			wantText: prefix + "is still running and printed 3 new lines matching `ERROR`.\n\nLast 2 matching lines:\n```\nERROR b\nERROR c\n```" +
				"\n\nThis is the last message for matching lines of this process. Its exit still wakes this chat.",
		},
		{name: "ExitOverLines", fired: `{"matched":1,"lines":"ERROR a","exit":{"code":2,"at":"2026-05-29T12:00:00Z"}}`,
			wantText: prefix + "exited with code 2.\n\nBefore it exited, it printed 1 new line matching `ERROR`.\n\nMatching lines:\n```\nERROR a\n```", wantDone: true},
		{
			name: "ExitOverManyLines", fired: `{"matched":3,"lines":"ERROR a\nERROR b\nERROR c","exit":{"code":2,"at":"2026-05-29T12:00:00Z"}}`,
			wantText: prefix + "exited with code 2.\n\nBefore it exited, it printed 3 new lines matching `ERROR`.\n\nMatching lines:\n```\nERROR a\nERROR b\nERROR c\n```", wantDone: true,
		},
		{
			// A folded batch is one delivery either way, so it carries no
			// last-message text even when it is the last one.
			name: "ExitOverLinesOnLastDelivery", fired: `{"matched":3,"lines":"ERROR a\nERROR b","exit":{"code":2,"at":"2026-05-29T12:00:00Z"}}`, deliveries: 9,
			wantText: prefix + "exited with code 2.\n\nBefore it exited, it printed 3 new lines matching `ERROR`.\n\nLast 2 matching lines:\n```\nERROR a\nERROR b\n```", wantDone: true,
		},
		{
			// A held-back batch delivered on the final poll goes out with the
			// exit or loss that fired it.
			name: "ExitOverLinesOnFinalPoll", fired: `{"matched":1,"lines":"ERROR a","exit":{"code":2,"at":"2026-05-29T12:00:00Z"}}`, expired: true,
			wantText: prefix + "exited with code 2.\n\nBefore it exited, it printed 1 new line matching `ERROR`.\n\nMatching lines:\n```\nERROR a\n```", wantDone: true,
		},
		{name: "LostOverLines", fired: `{"matched":2,"lines":"ERROR a\nERROR b","lost":{"reason":"gone","at":"2026-05-29T12:00:00Z"}}`,
			wantText: "Lost the watch on background process proc-1 (`make`): gone.\n\nBefore the watch was lost, it printed 2 new lines matching `ERROR`.\n\nMatching lines:\n```\nERROR a\nERROR b\n```", wantDone: true},
		{name: "Lost", fired: `{"lost":{"reason":"gone","at":"2026-05-29T12:00:00Z"}}`, wantText: "Lost the watch on background process proc-1 (`make`): gone.", wantDone: true},
		{name: "Invalid", fired: `[]`, wantText: "Lost the watch on background process proc-1 (`make`): its event could not be read.", wantDone: true},
		{name: "Empty", fired: `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			text, done := processFiredText(wakeup(tc.expired), watch(tc.fired, tc.deliveries), event, wakeupTestSlot, false)
			require.Equal(t, tc.wantText, text)
			require.Equal(t, tc.wantDone, done)
		})
	}
}

func TestProcessOutputTail(t *testing.T) {
	t.Parallel()
	numbered := func(count, width int) []string {
		lines := make([]string, count)
		for i := range lines {
			lines[i] = fmt.Sprintf("%0*d", width, i)
		}
		return lines
	}
	many := numbered(50, 2)
	wide := numbered(10, 1000)
	long := strings.Repeat("é", 3000) + "z"
	for _, tc := range []struct {
		name      string
		output    string
		wantTail  string
		wantLines int
	}{
		{name: "Empty", output: "\n"},
		{name: "TrailingNewline", output: "a\n\nb\n\n", wantTail: "a\n\nb", wantLines: 3},
		{name: "LineCap", output: strings.Join(many, "\n"), wantTail: strings.Join(many[10:], "\n"), wantLines: 40},
		{name: "ByteCapAtLineStart", output: strings.Join(wide, "\n"), wantTail: strings.Join(wide[6:], "\n"), wantLines: 4},
		{name: "LongLine", output: "first\n" + long, wantTail: long[len(long)-4095:], wantLines: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tail, lines := processOutputTail(tc.output)
			require.Equal(t, tc.wantTail, tail)
			require.Equal(t, tc.wantLines, lines)
			require.True(t, utf8.ValidString(tail))
			require.LessOrEqual(t, len(tail), maxProcessTailBytes)
		})
	}
}

// callWaitForEvents runs the wait_for_events tool of server for chat with
// the tool-call ID callID.
func callWaitForEvents(ctx context.Context, t *testing.T, server *Server, chat database.Chat, callID uuid.UUID, args waitForEventsArgs, wantError bool) (waitForEventsResult, string) {
	t.Helper()
	tools, err := server.appendForkTools(ctx, nil, chat, chat.LastModelConfigID)
	require.NoError(t, err)
	content := runChatTodoTool(workspacesdk.WithToolCallID(ctx, callID), t, tools, waitForEventsToolName, args, wantError)
	var result waitForEventsResult
	if !wantError {
		require.NoError(t, json.Unmarshal([]byte(content), &result), content)
	}
	return result, content
}

// bindProcessWorkspace binds chat to a new workspace whose latest build
// starts it with one agent.
func (f *workerTestFixture) bindProcessWorkspace(t *testing.T, chat database.Chat) (database.WorkspaceTable, database.WorkspaceBuild, database.WorkspaceAgent) {
	t.Helper()
	workspace, build, agent := seedWorkspaceBinding(t, f.db, f.user.ID)
	_, err := f.sqlDB.ExecContext(testutil.Context(t, testutil.WaitShort),
		"UPDATE chats SET workspace_id = $1, build_id = $2, agent_id = $3 WHERE id = $4", workspace.ID, build.ID, agent.ID, chat.ID)
	require.NoError(t, err)
	return workspace, build, agent
}

// stopProcessWorkspace adds a stop build of workspace after build, created at
// createdAt, or now when zero.
func (f *workerTestFixture) stopProcessWorkspace(t *testing.T, workspace database.WorkspaceTable, build database.WorkspaceBuild, createdAt time.Time) {
	t.Helper()
	job := dbgen.ProvisionerJob(t, f.db, nil, database.ProvisionerJob{
		InitiatorID:    f.user.ID,
		OrganizationID: workspace.OrganizationID,
	})
	dbgen.WorkspaceBuild(t, f.db, database.WorkspaceBuild{
		WorkspaceID:       workspace.ID,
		JobID:             job.ID,
		TemplateVersionID: build.TemplateVersionID,
		BuildNumber:       build.BuildNumber + 1,
		Transition:        database.WorkspaceTransitionStop,
		CreatedAt:         createdAt,
	})
}

// fireProcessExit fires the process watches of chat on process processID of
// agentID for an exit, like the agent's report does, and returns the chats
// to poke.
func (f *workerTestFixture) fireProcessExit(t *testing.T, chat database.Chat, agentID uuid.UUID, processID string, code int, tail string, at time.Time) []uuid.UUID {
	t.Helper()
	ended, err := forkdb.ChatProcessExitEnded(forkdb.ChatProcessExit{Code: code, Tail: tail, At: at})
	require.NoError(t, err)
	return f.fireProcess(t, forkdb.FireChatProcessWatchesParams{
		Ended: ended, ChatID: chat.ID, AgentID: agentID, ProcessID: processID,
	})
}

// processLinesMatched counts the match times of fireProcessLines, so each
// call reports later lines than the one before.
var processLinesMatched atomic.Int64

// fireProcessLines fires the process watch of wakeupID for matching output
// lines, like the agent's report does.
func (f *workerTestFixture) fireProcessLines(t *testing.T, chat database.Chat, agentID uuid.UUID, processID string, wakeupID uuid.UUID, lines ...string) []uuid.UUID {
	t.Helper()
	return f.fireProcess(t, forkdb.FireChatProcessWatchesParams{
		Matched: int32(len(lines)), Lines: strings.Join(lines, "\n"), //nolint:gosec // A test fires a few lines.
		OutputAt: wakeupTestSlot.Add(time.Duration(processLinesMatched.Add(1)) * time.Millisecond),
		ChatID:   chat.ID, AgentID: agentID, ProcessID: processID,
		WakeupID: uuid.NullUUID{UUID: wakeupID, Valid: true},
	})
}

// markProcessReported records that agentID said at at that it holds no
// unacked event, like its report without events does.
func (f *workerTestFixture) markProcessReported(t *testing.T, agentID uuid.UUID, at time.Time) {
	t.Helper()
	fdb, err := forkdb.From(f.db)
	require.NoError(t, err)
	n, err := fdb.MarkChatProcessWatchesReported(testutil.Context(t, testutil.WaitShort), forkdb.MarkChatProcessWatchesReportedParams{
		AgentID: agentID, ReportedAt: at,
	})
	require.NoError(t, err)
	require.NotZero(t, n)
}

func (f *workerTestFixture) fireProcess(t *testing.T, arg forkdb.FireChatProcessWatchesParams) []uuid.UUID {
	t.Helper()
	fdb, err := forkdb.From(f.db)
	require.NoError(t, err)
	arg.FiredAt = wakeupTestSlot
	chats, err := fdb.FireChatProcessWatches(testutil.Context(t, testutil.WaitShort), arg)
	require.NoError(t, err)
	return chats
}

// sweepProcessWatches runs the process sweep of a waker of server at now.
func (f *workerTestFixture) sweepProcessWatches(t *testing.T, server *Server, now time.Time) {
	t.Helper()
	fdb, err := forkdb.From(f.db)
	require.NoError(t, err)
	mClock := quartz.NewMock(t)
	mClock.Set(now)
	k := newForkWaker()
	k.bind(server, f.db, mClock, slog.Make())
	//nolint:gocritic // The sweep covers the watches of every chat.
	k.sweepProcessWatches(dbauthz.AsChatd(testutil.Context(t, testutil.WaitShort)), fdb)
}

type processWatchCall struct {
	ID  string
	Req workspacesdk.ForkWatchProcessRequest
}

// processFakeConn is an agent connection with the process watch API, which
// answers with the processes set on it.
type processFakeConn struct {
	*agentconnmock.MockAgentConn

	mu        sync.Mutex
	processes map[string]workspacesdk.ForkWatchProcessResponse
	calls     []processWatchCall
	tooOld    bool
	onWatch   func(id string, req workspacesdk.ForkWatchProcessRequest) error
}

func (c *processFakeConn) ForkWatchProcess(_ context.Context, id string, req workspacesdk.ForkWatchProcessRequest) (workspacesdk.ForkWatchProcessResponse, error) {
	c.mu.Lock()
	c.calls = append(c.calls, processWatchCall{ID: id, Req: req})
	onWatch, tooOld := c.onWatch, c.tooOld
	c.mu.Unlock()
	if tooOld {
		return workspacesdk.ForkWatchProcessResponse{}, codersdk.NewError(http.StatusNotFound, codersdk.Response{Message: "Route not found."})
	}
	if onWatch != nil {
		if err := onWatch(id, req); err != nil {
			return workspacesdk.ForkWatchProcessResponse{}, err
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.processes[id], nil
}

func (c *processFakeConn) set(id string, process workspacesdk.ForkWatchProcessResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.processes[id] = process
}

func (c *processFakeConn) setOnWatch(fn func(id string, req workspacesdk.ForkWatchProcessRequest) error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onWatch = fn
}

func (c *processFakeConn) setTooOld(tooOld bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tooOld = tooOld
}

func (c *processFakeConn) takeCalls() []processWatchCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	calls := c.calls
	c.calls = nil
	return calls
}

// processTestConn makes server dial a fake connection for agent that
// expects the process requests of chat.
func processTestConn(t *testing.T, server *Server, chat database.Chat, agent database.WorkspaceAgent) *processFakeConn {
	t.Helper()
	mock := agentconnmock.NewMockAgentConn(gomock.NewController(t))
	mock.EXPECT().SetExtraHeaders(http.Header{workspacesdk.CoderChatIDHeader: {chat.ID.String()}}).AnyTimes()
	conn := &processFakeConn{MockAgentConn: mock, processes: map[string]workspacesdk.ForkWatchProcessResponse{}}
	server.agentConnFn = func(_ context.Context, agentID uuid.UUID) (workspacesdk.AgentConn, func(), error) {
		if agentID != agent.ID {
			return nil, nil, xerrors.Errorf("no connection for agent %s", agentID)
		}
		return conn, func() {}, nil
	}
	return conn
}

// failingAgentConn makes every dial of server fail, like an offline agent,
// and counts the dials.
func failingAgentConn(server *Server) *atomic.Int32 {
	var dials atomic.Int32
	server.agentConnFn = func(context.Context, uuid.UUID) (workspacesdk.AgentConn, func(), error) {
		dials.Add(1)
		return nil, nil, xerrors.New("agent is offline")
	}
	return &dials
}

func processExitEvents(t *testing.T, agentID uuid.UUID, processIDs ...string) []wakeupEvent {
	t.Helper()
	events := make([]wakeupEvent, 0, len(processIDs))
	for _, id := range processIDs {
		events = append(events, wakeupEvent{Type: wakeupEventProcessExit, ProcessID: id, Command: "sleep 600", AgentID: agentID})
	}
	return events
}
