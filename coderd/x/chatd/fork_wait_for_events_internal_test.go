package chatd

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprompt"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/quartz"
)

func TestWaitForEventsTool(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := chatdTestContext(t)
	coordinator := f.createWakeupChat(t)
	running := f.createChildAgent(t, coordinator, "running", database.ChatStatusRunning)
	paused := f.createChildAgent(t, coordinator, "paused", database.ChatStatusPaused)
	waiting := f.createChildAgent(t, coordinator, "waiting", database.ChatStatusWaiting)
	stranger := f.createWakeupChat(t)
	// Watches nothing yet, so the arguments decide whether a call is refused.
	fresh := f.createWakeupChat(t)
	f.createChildAgent(t, fresh, "fresh", database.ChatStatusRunning)

	mClock := quartz.NewMock(t)
	mClock.Set(wakeupTestSlot)
	server := newUnstartedServer(t, f.pubsub, f.db, withInternalTestServerClock(mClock))
	server.experiments = codersdk.Experiments{codersdk.ExperimentForkChatWakeups}
	fdb, err := forkdb.From(f.db)
	require.NoError(t, err)
	wakeups := func(chat database.Chat) []forkdb.ChatWakeup {
		t.Helper()
		rows, err := fdb.ListChatWakeupsByChatID(testutil.Context(t, testutil.WaitShort), chat.ID)
		require.NoError(t, err)
		return rows
	}
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
	wait := func(chat database.Chat, args waitForEventsArgs, wantError bool) (waitForEventsResult, string) {
		t.Helper()
		return waitCall(chat, uuid.New(), args, wantError)
	}
	childIDs := func(result waitForEventsResult) []uuid.UUID {
		ids := make([]uuid.UUID, 0, len(result.Children))
		for _, child := range result.Children {
			ids = append(ids, child.ChatID)
		}
		return ids
	}

	// By default, only the children that are still working are watched.
	def, _ := wait(coordinator, waitForEventsArgs{}, false)
	require.Equal(t, []uuid.UUID{running.ID, paused.ID}, childIDs(def))
	require.Equal(t, waitForEventsChild{ChatID: running.ID, Title: "running", Status: database.ChatStatusRunning}, def.Children[0])
	rows := wakeups(coordinator)
	require.Len(t, rows, 1)
	row := rows[0]
	require.Equal(t, def.ID, row.ID)
	require.Equal(t, forkdb.ChatWakeupKindEvents, row.Kind)
	require.Equal(t, wakeupSourceEvents, row.Source)
	require.Equal(t, forkdb.ChatWakeupStatusActive, row.Status)
	require.Equal(t, defaultWaitForEventsPrompt, row.Prompt)
	require.Equal(t, coordinator.OwnerID, row.CreatedBy)
	require.JSONEq(t, fmt.Sprintf(`[{"type":"child_done","chat_id":%q},{"type":"child_done","chat_id":%q}]`, running.ID, paused.ID), string(row.Events))
	require.True(t, wakeupTestSlot.Equal(row.NextRunAt.Time), "the first poll is now: %s", row.NextRunAt.Time)
	require.True(t, wakeupTestSlot.Add(24*time.Hour).Equal(row.ExpiresAt.Time), "expires %s", row.ExpiresAt.Time)
	require.True(t, row.ExpiresAt.Time.Equal(def.ExpiresAt))

	// Named children may be done already; duplicates are watched once, and
	// children that def still watches are left out.
	named, _ := wait(coordinator, waitForEventsArgs{
		ChildChatIDs:     []string{waiting.ID.String(), waiting.ID.String(), running.ID.String()},
		Prompt:           "  check the children  ",
		ExpiresInSeconds: new(600),
	}, false)
	require.Equal(t, []uuid.UUID{waiting.ID}, childIDs(named))
	got := f.wakeup(t, named.ID)
	require.Equal(t, "check the children", got.Prompt)
	require.True(t, wakeupTestSlot.Add(10*time.Minute).Equal(got.ExpiresAt.Time), "expires %s", got.ExpiresAt.Time)

	for name, tc := range map[string]struct {
		chat    database.Chat
		args    waitForEventsArgs
		wantErr string
	}{
		"NotAChild":      {coordinator, waitForEventsArgs{ChildChatIDs: []string{stranger.ID.String()}}, "is not a child of this chat"},
		"UnknownChat":    {coordinator, waitForEventsArgs{ChildChatIDs: []string{uuid.NewString()}}, "is not a child of this chat"},
		"NoChildren":     {stranger, waitForEventsArgs{}, "no running child agents to wait for"},
		"ExpiryTooLong":  {fresh, waitForEventsArgs{ExpiresInSeconds: new(31 * 24 * 3600)}, "expires_in_seconds"},
		"ExpiryTooShort": {fresh, waitForEventsArgs{ExpiresInSeconds: new(30)}, "expires_in_seconds"},
		"LoopPrompt":     {fresh, waitForEventsArgs{Prompt: "/loop 5m poll"}, "must not start with /loop"},
		"InvalidChildID": {coordinator, waitForEventsArgs{ChildChatIDs: []string{"not-a-uuid"}}, "invalid child chat ID"},
		"AlreadyWatched": {coordinator, waitForEventsArgs{}, "already watched by wake-up " + def.ID.String()},
	} {
		t.Logf("case %s", name)
		_, content := wait(tc.chat, tc.args, true)
		require.Contains(t, content, tc.wantErr)
	}
	require.Len(t, wakeups(coordinator), 2, "refused calls create nothing")
	require.Empty(t, wakeups(stranger))
	require.Empty(t, wakeups(fresh))

	// A child that its wake-up delivered can be watched again.
	_, err = fdb.InsertChatWakeupRun(ctx, forkdb.InsertChatWakeupRunParams{
		ID:        uuid.New(),
		WakeupID:  def.ID,
		RunKey:    childDoneRunKey(running.ID),
		Status:    forkdb.ChatWakeupRunStatusDelivered,
		CreatedAt: wakeupTestSlot,
		UpdatedAt: wakeupTestSlot,
	})
	require.NoError(t, err)
	again, _ := wait(coordinator, waitForEventsArgs{ChildChatIDs: []string{running.ID.String(), paused.ID.String()}}, false)
	require.Equal(t, []uuid.UUID{running.ID}, childIDs(again))

	// Only children that are done: nothing to wait for.
	forceExecutionState(t, f, running.ID, database.ChatStatusWaiting, false)
	forceExecutionState(t, f, paused.ID, database.ChatStatusError, false)
	_, content := wait(coordinator, waitForEventsArgs{}, true)
	require.Contains(t, content, "no running child agents to wait for")

	// A replayed call returns its wake-up, although its watch set is done now.
	replayed, _ := waitCall(coordinator, def.ID, waitForEventsArgs{}, false)
	require.Equal(t, def.ID, replayed.ID)
	require.Equal(t, []uuid.UUID{running.ID, paused.ID}, childIDs(replayed))
	require.Equal(t, database.ChatStatusWaiting, replayed.Children[0].Status)
	require.Len(t, wakeups(coordinator), 3)

	// A settled child is still watched while it waits for events of its own
	// children or for an answer.
	family := f.createWakeupChat(t)
	nested := f.createChildAgent(t, family, "nested", database.ChatStatusWaiting)
	f.insertWakeup(t, nested, forkdb.InsertChatWakeupParams{
		Kind:   forkdb.ChatWakeupKindEvents,
		Events: childDoneEvents(f.createChildAgent(t, nested, "grandchild", database.ChatStatusRunning)),
	})
	asking := f.createChildAgent(t, family, "asking", database.ChatStatusWaiting)
	_, err = fdb.UpsertChatDescendantQuestion(ctx, forkdb.UpsertChatDescendantQuestionParams{
		ChatID:     asking.ID,
		ToolCallID: "call-1",
		Questions:  json.RawMessage(`[{"question":"Which one?"}]`),
	})
	require.NoError(t, err)
	f.createChildAgent(t, family, "finished", database.ChatStatusWaiting)
	busy, _ := wait(family, waitForEventsArgs{}, false)
	require.Equal(t, []uuid.UUID{nested.ID, asking.ID}, childIDs(busy))

	// A watch set over the limit is refused, not cut short.
	crowded := f.createWakeupChat(t)
	for i := range maxWaitForEventsChildren + 1 {
		f.createChildAgent(t, crowded, fmt.Sprintf("child %d", i), database.ChatStatusRunning)
	}
	_, content = wait(crowded, waitForEventsArgs{}, true)
	require.Contains(t, content, "the maximum is 50")
	require.Empty(t, wakeups(crowded))
}

// The acceptance of PRD 6.4 R4.3: a coordinator that ends its turn after
// spawning children is woken once per child completion.
func TestWakeupEventsDeliversOncePerChild(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	coordinator := f.createWakeupChat(t)
	one := f.createChildAgent(t, coordinator, "one", database.ChatStatusRunning)
	two := f.createChildAgent(t, coordinator, "two", database.ChatStatusRunning)
	three := f.createChildAgent(t, coordinator, "three", database.ChatStatusRunning)
	wakeup := f.insertWakeup(t, coordinator, forkdb.InsertChatWakeupParams{
		Kind:   forkdb.ChatWakeupKindEvents,
		Events: childDoneEvents(one, two, three),
	})
	worker := f.newArchiveWorker(t, nil, nil, nil)
	now := wakeupTestSlot
	tick := func() {
		t.Helper()
		// The worker adds the time it spent since the claim to the next poll,
		// so a tick of exactly the poll interval can come before it is due.
		now = now.Add(wakeupPollInterval + time.Second)
		worker.wakeupOnce(ctx, now)
	}
	doneText := func(child database.Chat) string {
		return fmt.Sprintf("%s\n\nChild agent %q (%s) is done with status waiting.", wakeup.Prompt, child.Title, child.ID)
	}

	tick()
	require.Empty(t, f.wakeMessages(t, coordinator, wakeup.Prompt), "no child is done")
	require.Empty(t, f.wakeupRunKeys(t, wakeup.ID))
	got := f.wakeup(t, wakeup.ID)
	require.Equal(t, forkdb.ChatWakeupStatusActive, got.Status)
	require.WithinDuration(t, now.Add(wakeupPollInterval), got.NextRunAt.Time, time.Second, "polls again")
	require.False(t, got.NextRunAt.Time.Before(now.Add(wakeupPollInterval)))

	forceExecutionState(t, f, one.ID, database.ChatStatusWaiting, false)
	tick()
	require.Equal(t, []string{doneText(one)}, f.wakeMessages(t, coordinator, wakeup.Prompt))
	require.Equal(t, []string{childDoneRunKey(one.ID)}, f.wakeupRunKeys(t, wakeup.ID))
	require.Equal(t, []string{"delivered"}, f.wakeupRunStatuses(t, wakeup.ID))
	got = f.wakeup(t, wakeup.ID)
	require.Equal(t, forkdb.ChatWakeupStatusActive, got.Status)
	require.EqualValues(t, 1, got.RunCount)

	// The coordinator's turn ends; the same child does not wake it again.
	forceExecutionState(t, f, coordinator.ID, database.ChatStatusWaiting, false)
	tick()
	require.Len(t, f.wakeMessages(t, coordinator, wakeup.Prompt), 1)

	// Two children finish at once: one message per tick, never a batch.
	forceExecutionState(t, f, two.ID, database.ChatStatusWaiting, false)
	forceExecutionState(t, f, three.ID, database.ChatStatusWaiting, false)
	tick()
	require.Equal(t, []string{doneText(one), doneText(two)}, f.wakeMessages(t, coordinator, wakeup.Prompt))
	tick()
	require.Len(t, f.wakeMessages(t, coordinator, wakeup.Prompt), 2, "the next child waits for the coordinator's turn")
	require.Equal(t, forkdb.ChatWakeupStatusActive, f.wakeup(t, wakeup.ID).Status)

	forceExecutionState(t, f, coordinator.ID, database.ChatStatusWaiting, false)
	tick()
	require.Equal(t, []string{doneText(one), doneText(two), doneText(three)}, f.wakeMessages(t, coordinator, wakeup.Prompt))
	require.Equal(t, []string{childDoneRunKey(one.ID), childDoneRunKey(two.ID), childDoneRunKey(three.ID)}, f.wakeupRunKeys(t, wakeup.ID))
	got = f.wakeup(t, wakeup.ID)
	require.Equal(t, forkdb.ChatWakeupStatusCompleted, got.Status)
	require.Equal(t, "done", got.StatusReason.String)
	require.EqualValues(t, 3, got.RunCount)
	require.False(t, got.NextRunAt.Valid)
}

func TestWakeupEventsWaitsForBusyChat(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	coordinator := f.createWakeupChat(t)
	child := f.createChildAgent(t, coordinator, "child", database.ChatStatusWaiting)
	forceExecutionState(t, f, coordinator.ID, database.ChatStatusRunning, false)
	wakeup := f.insertWakeup(t, coordinator, forkdb.InsertChatWakeupParams{
		Kind:   forkdb.ChatWakeupKindEvents,
		Events: childDoneEvents(child),
	})
	worker := f.newArchiveWorker(t, nil, nil, nil)
	var prompts atomic.Int32
	useUserPromptHook(t, worker, func(rw http.ResponseWriter) {
		prompts.Add(1)
		_, _ = rw.Write([]byte(`{}`))
	})

	worker.wakeupOnce(ctx, wakeupTestSlot.Add(time.Second))
	require.Empty(t, f.wakeMessages(t, coordinator, wakeup.Prompt))
	require.Zero(t, prompts.Load(), "a busy coordinator gets no send attempt")
	require.Empty(t, f.wakeupRunStatuses(t, wakeup.ID))
	got := f.wakeup(t, wakeup.ID)
	require.Equal(t, forkdb.ChatWakeupStatusActive, got.Status)
	require.True(t, wakeupTestSlot.Equal(got.NextRunAt.Time), "still due: %s", got.NextRunAt.Time)
	require.False(t, got.LeaseExpiresAt.Valid)

	forceExecutionState(t, f, coordinator.ID, database.ChatStatusWaiting, false)
	worker.wakeupOnce(ctx, wakeupTestSlot.Add(20*time.Second))
	require.Len(t, f.wakeMessages(t, coordinator, wakeup.Prompt), 1)
	require.EqualValues(t, 1, prompts.Load())
	require.Equal(t, forkdb.ChatWakeupStatusCompleted, f.wakeup(t, wakeup.ID).Status)
}

// A failed send leaves the child undelivered, so a later poll retries it.
// Repeated failures share one failed run.
func TestWakeupEventsRetriesFailedSend(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	coordinator := f.createWakeupChat(t)
	child := f.createChildAgent(t, coordinator, "child", database.ChatStatusWaiting)
	wakeup := f.insertWakeup(t, coordinator, forkdb.InsertChatWakeupParams{
		Kind:   forkdb.ChatWakeupKindEvents,
		Events: childDoneEvents(child),
	})
	worker := f.newArchiveWorker(t, nil, nil, nil)
	var prompts atomic.Int32
	useUserPromptHook(t, worker, func(rw http.ResponseWriter) {
		if prompts.Add(1) <= 2 {
			rw.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = rw.Write([]byte(`{}`))
	})

	now := wakeupTestSlot.Add(time.Second)
	for range 2 {
		worker.wakeupOnce(ctx, now)
		now = now.Add(wakeupPollInterval + time.Second)
	}
	require.EqualValues(t, 2, prompts.Load())
	require.Empty(t, f.wakeMessages(t, coordinator, wakeup.Prompt))
	require.Equal(t, []string{"failed"}, f.wakeupRunStatuses(t, wakeup.ID))
	require.Equal(t, forkdb.ChatWakeupStatusActive, f.wakeup(t, wakeup.ID).Status)

	worker.wakeupOnce(ctx, now)
	require.Len(t, f.wakeMessages(t, coordinator, wakeup.Prompt), 1)
	require.Contains(t, f.wakeupRunKeys(t, wakeup.ID), childDoneRunKey(child.ID))
	require.Equal(t, forkdb.ChatWakeupStatusCompleted, f.wakeup(t, wakeup.ID).Status)
}

// An archived child counts as done, and a watched child that is gone neither
// sends a message nor keeps the wake-up open.
func TestWakeupEventsArchivedAndMissingChildren(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	coordinator := f.createWakeupChat(t)
	archived := f.createChildAgent(t, coordinator, "archived", database.ChatStatusRunning)
	forceExecutionState(t, f, archived.ID, database.ChatStatusRunning, true)
	missing := database.Chat{ID: uuid.New()}
	wakeup := f.insertWakeup(t, coordinator, forkdb.InsertChatWakeupParams{
		Kind:   forkdb.ChatWakeupKindEvents,
		Events: childDoneEvents(missing, archived),
	})

	f.newArchiveWorker(t, nil, nil, nil).wakeupOnce(ctx, wakeupTestSlot.Add(time.Second))
	require.Equal(t, []string{
		fmt.Sprintf("%s\n\nChild agent \"archived\" (%s) is done with status running.", wakeup.Prompt, archived.ID),
	}, f.wakeMessages(t, coordinator, wakeup.Prompt))
	require.Equal(t, []string{childDoneRunKey(archived.ID)}, f.wakeupRunKeys(t, wakeup.ID))
	got := f.wakeup(t, wakeup.ID)
	require.Equal(t, forkdb.ChatWakeupStatusCompleted, got.Status)
	require.Equal(t, "done", got.StatusReason.String)
}

// A waiting child that waits for events of its own children is not done.
func TestWakeupEventsWaitsForNestedCoordinator(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	coordinator := f.createWakeupChat(t)
	child := f.createChildAgent(t, coordinator, "child", database.ChatStatusWaiting)
	grandchild := f.createChildAgent(t, child, "grandchild", database.ChatStatusRunning)
	nested := f.insertWakeup(t, child, forkdb.InsertChatWakeupParams{
		Kind:   forkdb.ChatWakeupKindEvents,
		Events: childDoneEvents(grandchild),
	})
	wakeup := f.insertWakeup(t, coordinator, forkdb.InsertChatWakeupParams{
		Kind:   forkdb.ChatWakeupKindEvents,
		Events: childDoneEvents(child),
	})
	worker := f.newArchiveWorker(t, nil, nil, nil)

	worker.wakeupOnce(ctx, wakeupTestSlot.Add(time.Second))
	require.Empty(t, f.wakeMessages(t, coordinator, wakeup.Prompt))
	require.Equal(t, forkdb.ChatWakeupStatusActive, f.wakeup(t, wakeup.ID).Status)

	fdb, err := forkdb.From(f.db)
	require.NoError(t, err)
	_, err = fdb.UpdateChatWakeupStatus(ctx, forkdb.UpdateChatWakeupStatusParams{
		ID:           nested.ID,
		Status:       forkdb.ChatWakeupStatusCompleted,
		StatusReason: sql.NullString{String: "done", Valid: true},
		UpdatedAt:    wakeupTestSlot.Add(2 * time.Second),
	})
	require.NoError(t, err)
	worker.wakeupOnce(ctx, wakeupTestSlot.Add(time.Second+wakeupPollInterval+time.Second))
	require.Len(t, f.wakeMessages(t, coordinator, wakeup.Prompt), 1)
	require.Equal(t, forkdb.ChatWakeupStatusCompleted, f.wakeup(t, wakeup.ID).Status)
}

// Events of a type this worker does not know wake nothing and hold nothing
// open, and events that are not a JSON array pause the wake-up.
func TestWakeupEventsUnknownAndInvalidEvents(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	unknownChat := f.createWakeupChat(t)
	child := f.createChildAgent(t, unknownChat, "child", database.ChatStatusWaiting)
	unknown := f.insertWakeup(t, unknownChat, forkdb.InsertChatWakeupParams{
		Kind:   forkdb.ChatWakeupKindEvents,
		Events: json.RawMessage(fmt.Sprintf(`[{"type":"pr_merged","chat_id":%q}]`, child.ID)),
	})
	invalidChat := f.createWakeupChat(t)
	invalid := f.insertWakeup(t, invalidChat, forkdb.InsertChatWakeupParams{
		Kind:   forkdb.ChatWakeupKindEvents,
		Events: json.RawMessage(`{"type":"child_done"}`),
	})

	worker := f.newArchiveWorker(t, nil, nil, nil)
	enqueuer := useFakeEnqueuer(worker)
	worker.wakeupOnce(ctx, wakeupTestSlot.Add(time.Second))

	require.Empty(t, f.wakeMessages(t, unknownChat, unknown.Prompt))
	require.Empty(t, f.wakeupRunKeys(t, unknown.ID))
	got := f.wakeup(t, unknown.ID)
	require.Equal(t, forkdb.ChatWakeupStatusCompleted, got.Status)
	require.Equal(t, "done", got.StatusReason.String)

	require.Empty(t, f.wakeMessages(t, invalidChat, invalid.Prompt))
	got = f.wakeup(t, invalid.ID)
	require.Equal(t, forkdb.ChatWakeupStatusPaused, got.Status)
	require.Equal(t, "invalid_events", got.StatusReason.String)
	require.False(t, got.LeaseExpiresAt.Valid)
	// Only the pause notifies; the completed wake-up does not.
	requireWakeupPausedNotification(t, enqueuer.Sent(), invalidChat, "invalid_events")
}

// An events wake-up checks the caps only when a child is about to be sent.
func TestWakeupEventsCap(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		childStatus database.ChatStatus
		wantStatus  forkdb.ChatWakeupStatus
	}{
		{name: "ChildDone", childStatus: database.ChatStatusWaiting, wantStatus: forkdb.ChatWakeupStatusPaused},
		{name: "NothingPending", childStatus: database.ChatStatusRunning, wantStatus: forkdb.ChatWakeupStatusActive},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newWorkerTestFixture(t)
			ctx := testutil.Context(t, testutil.WaitLong)
			coordinator := f.createWakeupChat(t)
			child := f.createChildAgent(t, coordinator, "child", tc.childStatus)
			wakeup := f.insertWakeup(t, coordinator, forkdb.InsertChatWakeupParams{
				Kind:   forkdb.ChatWakeupKindEvents,
				Events: childDoneEvents(child),
			})
			f.seedDeliveredRuns(t, wakeup, maxChatWakeupRunsPerDay, wakeupTestSlot.Add(-time.Hour))
			worker := f.newArchiveWorker(t, nil, nil, nil)
			enqueuer := useFakeEnqueuer(worker)

			worker.wakeupOnce(ctx, wakeupTestSlot.Add(time.Second))
			require.Empty(t, f.wakeMessages(t, coordinator, wakeup.Prompt))
			require.NotContains(t, f.wakeupRunKeys(t, wakeup.ID), childDoneRunKey(child.ID))
			got := f.wakeup(t, wakeup.ID)
			require.Equal(t, tc.wantStatus, got.Status)
			if tc.wantStatus == forkdb.ChatWakeupStatusPaused {
				require.Equal(t, wakeupReasonDailyRunCap, got.StatusReason.String)
				requireWakeupPausedNotification(t, enqueuer.Sent(), coordinator, wakeupReasonDailyRunCap)
				return
			}
			require.Empty(t, enqueuer.Sent())
		})
	}
}

// createChildAgent creates a direct child of parent with title and status.
func (f *workerTestFixture) createChildAgent(t *testing.T, parent database.Chat, title string, status database.ChatStatus) database.Chat {
	t.Helper()
	child := f.createWakeupChat(t)
	f.linkChild(t, parent.ID, child.ID)
	_, err := f.sqlDB.ExecContext(testutil.Context(t, testutil.WaitShort), "UPDATE chats SET title = $1 WHERE id = $2", title, child.ID)
	require.NoError(t, err)
	return forceExecutionState(t, f, child.ID, status, false)
}

func childDoneEvents(children ...database.Chat) json.RawMessage {
	events := make([]string, 0, len(children))
	for _, child := range children {
		events = append(events, fmt.Sprintf(`{"type":"child_done","chat_id":%q}`, child.ID))
	}
	return json.RawMessage("[" + strings.Join(events, ",") + "]")
}

func (f *workerTestFixture) wakeupRunKeys(t *testing.T, wakeupID uuid.UUID) []string {
	t.Helper()
	fdb, err := forkdb.From(f.db)
	require.NoError(t, err)
	keys, err := fdb.ListChatWakeupRunKeys(testutil.Context(t, testutil.WaitShort), wakeupID)
	require.NoError(t, err)
	return keys
}

// wakeMessages returns, in order, the texts of the user messages from the
// chat owner that start with prompt.
func (f *workerTestFixture) wakeMessages(t *testing.T, chat database.Chat, prompt string) []string {
	t.Helper()
	messages, err := f.db.GetChatMessagesByChatID(testutil.Context(t, testutil.WaitShort), database.GetChatMessagesByChatIDParams{ChatID: chat.ID})
	require.NoError(t, err)
	var texts []string
	for _, msg := range messages {
		if msg.Role != database.ChatMessageRoleUser || msg.CreatedBy.UUID != chat.OwnerID {
			continue
		}
		parts, err := chatprompt.ParseContent(msg)
		require.NoError(t, err)
		var text strings.Builder
		for _, part := range parts {
			text.WriteString(part.Text)
		}
		if strings.HasPrefix(text.String(), prompt) {
			texts = append(texts, text.String())
		}
	}
	return texts
}

// A child that ended its turn with a question for the user is not done until
// the question is answered.
func TestWakeupEventsChildWithPendingQuestion(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	coordinator := f.createWakeupChat(t)
	child := f.createChildAgent(t, coordinator, "child", database.ChatStatusWaiting)
	fdb, err := forkdb.From(f.db)
	require.NoError(t, err)
	_, err = fdb.UpsertChatDescendantQuestion(ctx, forkdb.UpsertChatDescendantQuestionParams{
		ChatID:     child.ID,
		ToolCallID: "call-1",
		Questions:  json.RawMessage(`[{"question":"Which one?"}]`),
	})
	require.NoError(t, err)
	wakeup := f.insertWakeup(t, coordinator, forkdb.InsertChatWakeupParams{
		Kind:   forkdb.ChatWakeupKindEvents,
		Events: childDoneEvents(child),
	})
	worker := f.newArchiveWorker(t, nil, nil, nil)

	worker.wakeupOnce(ctx, wakeupTestSlot.Add(time.Second))
	require.Empty(t, f.wakeMessages(t, coordinator, wakeup.Prompt))
	require.Empty(t, f.wakeupRunKeys(t, wakeup.ID))
	require.Equal(t, forkdb.ChatWakeupStatusActive, f.wakeup(t, wakeup.ID).Status)
}
