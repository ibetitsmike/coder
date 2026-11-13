package chatd

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
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
	conn.EXPECT().ProcessOutput(gomock.Any(), "proc-1", nil).
		Return(workspacesdk.ProcessOutputResponse{Running: true, Command: "sleep 60"}, nil)
	conn.EXPECT().ProcessOutput(gomock.Any(), "unknown", nil).
		Return(workspacesdk.ProcessOutputResponse{}, codersdk.NewError(http.StatusNotFound, codersdk.Response{Message: "Process not found."}))
	waitCall := func(chat database.Chat, callID uuid.UUID, args waitForEventsArgs, wantError bool) (waitForEventsResult, string) {
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

	// Duplicates are watched once, and processes alone watch no children.
	watchID := uuid.New()
	watch, _ := waitCall(chat, watchID, waitForEventsArgs{ProcessIDs: []string{" proc-1 ", "proc-1"}}, false)
	want := []waitForEventsProcess{{ProcessID: "proc-1", Command: "sleep 60"}}
	require.Equal(t, want, watch.Processes)
	require.Empty(t, watch.Children)
	row := f.wakeup(t, watchID)
	require.Equal(t, defaultPullRequestWaitPrompt, row.Prompt)
	require.JSONEq(t, fmt.Sprintf(`[{"type":"process_exit","process_id":"proc-1","command":"sleep 60","agent_id":%q}]`, agent.ID), string(row.Events))

	// A replayed call does not read the process again.
	replayed, _ := waitCall(chat, watchID, waitForEventsArgs{ProcessIDs: []string{"proc-1"}}, false)
	require.Equal(t, want, replayed.Processes)

	tooMany := make([]string, 0, maxWaitForEventsProcesses+1)
	for i := range maxWaitForEventsProcesses + 1 {
		tooMany = append(tooMany, fmt.Sprintf("proc-%d", i+10))
	}
	for name, tc := range map[string]struct {
		chat    database.Chat
		ids     []string
		wantErr string
	}{
		"AlreadyWatched": {chat, []string{"proc-1"}, "already watched by wake-up " + watchID.String()},
		"NotAProcess":    {chat, []string{"unknown"}, `process "unknown" is not a process of this chat`},
		"NoAgent":        {unbound, []string{"proc-1"}, "this chat has no workspace agent"},
		"TooMany":        {chat, tooMany, "the maximum is 10"},
		"EmptyID":        {chat, []string{" "}, "must not contain an empty ID"},
	} {
		t.Logf("case %s", name)
		_, content := waitCall(tc.chat, uuid.New(), waitForEventsArgs{ProcessIDs: tc.ids}, true)
		require.Contains(t, content, tc.wantErr)
	}
	// Refused process IDs fail the call before it reads the pull request.
	fake := &fakePullRequestActivity{activity: openPullRequest(gitprovider.CheckStatePending)}
	server.forkPullRequestActivity = fake.fetch
	_, content := waitCall(chat, uuid.New(), waitForEventsArgs{ProcessIDs: tooMany, PullRequestURL: testPullRequestURL}, true)
	require.Contains(t, content, "the maximum is 10")
	require.Empty(t, fake.calls)
	fdb, err := forkdb.From(f.db)
	require.NoError(t, err)
	rows, err := fdb.ListChatWakeupsByChatID(ctx, chat.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1, "refused calls create nothing")
}

// A watched process wakes the chat once, when it exits, with the end of its
// output.
func TestWakeupEventsProcessExit(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	chat := f.createWakeupChat(t)
	_, _, agent := f.bindProcessWorkspace(t, chat)
	wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
		Kind:   forkdb.ChatWakeupKindEvents,
		Events: processExitEvents(t, agent.ID, "proc-1"),
	})
	worker := f.newArchiveWorker(t, nil, nil, nil)
	conn := processTestConn(t, worker.server, chat, agent)
	gomock.InOrder(
		conn.EXPECT().ProcessOutput(gomock.Any(), "proc-1", nil).
			Return(workspacesdk.ProcessOutputResponse{Running: true, Command: "sleep 600", Output: "ok\n"}, nil),
		conn.EXPECT().ProcessOutput(gomock.Any(), "proc-1", nil).
			Return(workspacesdk.ProcessOutputResponse{Command: "sleep 600", Output: "ok\nDONE\n", ExitCode: new(0)}, nil),
	)
	now := wakeupTestSlot
	tick := func() {
		t.Helper()
		now = now.Add(wakeupPollInterval + time.Second)
		worker.wakeupOnce(ctx, now)
	}

	tick()
	require.Empty(t, f.wakeMessages(t, chat, wakeup.Prompt), "the process still runs")
	got := f.wakeup(t, wakeup.ID)
	require.Equal(t, forkdb.ChatWakeupStatusActive, got.Status)
	require.WithinDuration(t, now.Add(wakeupPollInterval), got.NextRunAt.Time, time.Second, "polls again")

	tick()
	require.Equal(t, []string{
		wakeup.Prompt + "\n\nBackground process proc-1 (`sleep 600`) exited with code 0.\n\nLast 2 lines of output:\n```\nok\nDONE\n```",
	}, f.wakeMessages(t, chat, wakeup.Prompt))
	require.Equal(t, []string{processExitRunKey("proc-1")}, f.wakeupRunKeys(t, wakeup.ID))
	got = f.wakeup(t, wakeup.ID)
	require.Equal(t, forkdb.ChatWakeupStatusCompleted, got.Status)
	require.Equal(t, "done", got.StatusReason.String)

	forceExecutionState(t, f, chat.ID, database.ChatStatusWaiting, false)
	tick()
	require.Len(t, f.wakeMessages(t, chat, wakeup.Prompt), 1)
}

// The first poll reports a process that is gone or already exited, and a
// failed read leaves the wake-up waiting for a later poll.
func TestWakeupEventsProcessOutcomes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		// stopped adds a stop build after the build that ran the process.
		stopped bool
		output  workspacesdk.ProcessOutputResponse
		err     error
		// wantText is the message after the prompt, or "" for none.
		wantText string
		wantNext time.Duration
	}{
		{
			name:     "AlreadyExited",
			output:   workspacesdk.ProcessOutputResponse{Command: "sleep 600"},
			wantText: "Background process proc-1 (`sleep 600`) exited without an exit code (killed by a signal).",
		},
		{
			name:     "UnknownToAgent",
			err:      codersdk.NewError(http.StatusNotFound, codersdk.Response{Message: "Process not found."}),
			wantText: "Lost the watch on background process proc-1 (`sleep 600`): the workspace agent no longer knows it (the agent restarted or the process exited more than an hour ago).",
		},
		{
			name:     "WorkspaceStopped",
			stopped:  true,
			wantText: "Lost the watch on background process proc-1 (`sleep 600`): the workspace stopped or was rebuilt, so the process is gone.",
		},
		{
			name:     "ReadFails",
			err:      xerrors.New("connection reset"),
			wantNext: processErrorRetry,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newWorkerTestFixture(t)
			ctx := testutil.Context(t, testutil.WaitLong)
			chat := f.createWakeupChat(t)
			workspace, build, agent := f.bindProcessWorkspace(t, chat)
			if tc.stopped {
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
				})
			}
			wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
				Kind:   forkdb.ChatWakeupKindEvents,
				Events: processExitEvents(t, agent.ID, "proc-1"),
			})
			worker := f.newArchiveWorker(t, nil, nil, nil)
			conn := processTestConn(t, worker.server, chat, agent)
			if !tc.stopped {
				conn.EXPECT().ProcessOutput(gomock.Any(), "proc-1", nil).Return(tc.output, tc.err)
			}

			now := wakeupTestSlot.Add(time.Second)
			worker.wakeupOnce(ctx, now)
			got := f.wakeup(t, wakeup.ID)
			if tc.wantText == "" {
				require.Empty(t, f.wakeMessages(t, chat, wakeup.Prompt))
				require.Empty(t, f.wakeupRunStatuses(t, wakeup.ID))
				require.Equal(t, forkdb.ChatWakeupStatusActive, got.Status)
				require.WithinDuration(t, now.Add(tc.wantNext), got.NextRunAt.Time, time.Second)
				return
			}
			require.Equal(t, []string{wakeup.Prompt + "\n\n" + tc.wantText}, f.wakeMessages(t, chat, wakeup.Prompt))
			require.Equal(t, []string{processExitRunKey("proc-1")}, f.wakeupRunKeys(t, wakeup.ID))
			require.Equal(t, forkdb.ChatWakeupStatusCompleted, got.Status)
		})
	}
}

// A final poll well after the expiry leaves out a process that exited after
// the expiry.
func TestWakeupFinalPollProcesses(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		late time.Duration
		// exitedAt is when the agent lists the process as exited, relative to
		// the expiry.
		exitedAt    time.Duration
		wantMessage bool
		wantReason  string
	}{
		{name: "OnTime", late: time.Second, exitedAt: time.Second, wantMessage: true, wantReason: "done"},
		{name: "LateExitedAfterExpiry", late: time.Minute, exitedAt: 30 * time.Second, wantReason: "expired"},
		{name: "LateExitedBeforeExpiry", late: time.Minute, exitedAt: -30 * time.Second, wantMessage: true, wantReason: "done"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newWorkerTestFixture(t)
			ctx := testutil.Context(t, testutil.WaitLong)
			chat := f.createWakeupChat(t)
			_, _, agent := f.bindProcessWorkspace(t, chat)
			// insertWakeup schedules the run at wakeupTestSlot, so this is the
			// final poll.
			wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
				Kind:      forkdb.ChatWakeupKindEvents,
				Events:    processExitEvents(t, agent.ID, "proc-1"),
				ExpiresAt: sql.NullTime{Time: wakeupTestSlot, Valid: true},
			})
			worker := f.newArchiveWorker(t, nil, nil, nil)
			conn := processTestConn(t, worker.server, chat, agent)
			conn.EXPECT().ProcessOutput(gomock.Any(), "proc-1", nil).
				Return(workspacesdk.ProcessOutputResponse{Command: "sleep 600", ExitCode: new(0)}, nil)
			exitedAt := wakeupTestSlot.Add(tc.exitedAt).Unix()
			conn.EXPECT().ListProcesses(gomock.Any()).
				Return(workspacesdk.ListProcessesResponse{Processes: []workspacesdk.ProcessInfo{{ID: "proc-1", ExitedAt: &exitedAt}}}, nil).
				AnyTimes()

			worker.wakeupOnce(ctx, wakeupTestSlot.Add(tc.late))
			if tc.wantMessage {
				require.Len(t, f.wakeMessages(t, chat, wakeup.Prompt), 1)
			} else {
				require.Empty(t, f.wakeMessages(t, chat, wakeup.Prompt))
			}
			got := f.wakeup(t, wakeup.ID)
			require.Equal(t, forkdb.ChatWakeupStatusCompleted, got.Status)
			require.Equal(t, tc.wantReason, got.StatusReason.String)
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

// processTestConn makes server dial a mock connection for agent that expects
// the process requests of chat.
func processTestConn(t *testing.T, server *Server, chat database.Chat, agent database.WorkspaceAgent) *agentconnmock.MockAgentConn {
	t.Helper()
	conn := agentconnmock.NewMockAgentConn(gomock.NewController(t))
	conn.EXPECT().SetExtraHeaders(http.Header{workspacesdk.CoderChatIDHeader: {chat.ID.String()}}).AnyTimes()
	server.agentConnFn = func(_ context.Context, agentID uuid.UUID) (workspacesdk.AgentConn, func(), error) {
		if agentID != agent.ID {
			return nil, nil, xerrors.Errorf("no connection for agent %s", agentID)
		}
		return conn, func() {}, nil
	}
	return conn
}

func processExitEvents(t *testing.T, agentID uuid.UUID, processIDs ...string) json.RawMessage {
	t.Helper()
	events := make([]wakeupEvent, 0, len(processIDs))
	for _, id := range processIDs {
		events = append(events, wakeupEvent{Type: wakeupEventProcessExit, ProcessID: id, Command: "sleep 600", AgentID: agentID})
	}
	raw, err := json.Marshal(events)
	require.NoError(t, err)
	return raw
}
