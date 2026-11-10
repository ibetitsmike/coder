package chatd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/externalauth/gitprovider"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/quartz"
)

const (
	testPullRequestURL = "https://github.com/org/repo/pull/7"
	testHeadSHA        = "0123456789abcdef0123456789abcdef01234567"
	testNewHeadSHA     = "fedcba9876543210fedcba9876543210fedcba98"
)

// fakePullRequestActivity stands in for Server.forkPullRequestActivity: it
// returns a scripted activity or error and records its arguments.
type fakePullRequestActivity struct {
	mu       sync.Mutex
	activity gitprovider.PullRequestActivity
	err      error
	calls    []fakePullRequestCall
}

type fakePullRequestCall struct {
	ownerID uuid.UUID
	url     string
	since   time.Time
}

func (f *fakePullRequestActivity) fetch(_ context.Context, ownerID uuid.UUID, pullRequestURL string, since time.Time) (*gitprovider.PullRequestActivity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakePullRequestCall{ownerID: ownerID, url: pullRequestURL, since: since})
	if f.err != nil {
		return nil, f.err
	}
	activity := f.activity
	return &activity, nil
}

func (f *fakePullRequestActivity) set(activity gitprovider.PullRequestActivity, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.activity, f.err = activity, err
}

func (f *fakePullRequestActivity) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func openPullRequest(checks gitprovider.CheckState) gitprovider.PullRequestActivity {
	return gitprovider.PullRequestActivity{
		State:   gitprovider.PRStateOpen,
		HeadSHA: testHeadSHA,
		Checks:  gitprovider.CheckSummary{State: checks, Total: 3},
	}
}

func TestWaitForEventsPullRequestTool(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := chatdTestContext(t)
	coordinator := f.createWakeupChat(t)
	running := f.createChildAgent(t, coordinator, "running", database.ChatStatusRunning)

	mClock := quartz.NewMock(t)
	mClock.Set(wakeupTestSlot)
	server := newUnstartedServer(t, f.pubsub, f.db, withInternalTestServerClock(mClock))
	server.experiments = codersdk.Experiments{codersdk.ExperimentForkChatWakeups}
	fake := &fakePullRequestActivity{activity: openPullRequest(gitprovider.CheckStatePending)}
	server.forkPullRequestActivity = fake.fetch
	waitCall := func(callID uuid.UUID, args waitForEventsArgs, wantError bool) (waitForEventsResult, string) {
		t.Helper()
		tools, err := server.appendForkTools(ctx, nil, coordinator, coordinator.LastModelConfigID)
		require.NoError(t, err)
		content := runChatTodoTool(workspacesdk.WithToolCallID(ctx, callID), t, tools, waitForEventsToolName, args, wantError)
		var result waitForEventsResult
		if !wantError {
			require.NoError(t, json.Unmarshal([]byte(content), &result), content)
		}
		return result, content
	}
	wantPullRequest := &waitForEventsPullRequest{
		URL:         testPullRequestURL,
		On:          []string{"review", "comment", "checks", "closed"},
		HeadSHA:     testHeadSHA,
		ChecksState: "pending",
	}

	// A pull request alone watches no children, although one is running.
	onlyID := uuid.New()
	only, _ := waitCall(onlyID, waitForEventsArgs{PullRequestURL: "  " + testPullRequestURL + " "}, false)
	require.Empty(t, only.Children)
	require.Equal(t, wantPullRequest, only.PullRequest)
	row := f.wakeup(t, onlyID)
	require.Equal(t, defaultPullRequestWaitPrompt, row.Prompt)
	require.JSONEq(t, fmt.Sprintf(`[{"type":"pull_request","url":%q,"on":["review","comment","checks","closed"],"since":%q,"head_sha":%q,"checks_state":"pending"}]`,
		testPullRequestURL, wakeupTestSlot.Format(time.RFC3339), testHeadSHA), string(row.Events))
	require.Equal(t, []fakePullRequestCall{{ownerID: coordinator.OwnerID, url: testPullRequestURL, since: wakeupTestSlot}}, fake.calls)

	// With children too, both are watched, and the events keep their order.
	both, _ := waitCall(uuid.New(), waitForEventsArgs{
		ChildChatIDs:      []string{running.ID.String()},
		PullRequestURL:    testPullRequestURL,
		PullRequestEvents: []string{"checks", "review"},
	}, false)
	require.Equal(t, []uuid.UUID{running.ID}, []uuid.UUID{both.Children[0].ChatID})
	require.Equal(t, []string{"review", "checks"}, both.PullRequest.On)
	require.JSONEq(t, fmt.Sprintf(`[{"type":"child_done","chat_id":%q},{"type":"pull_request","url":%q,"on":["review","checks"],"since":%q,"head_sha":%q,"checks_state":"pending"}]`,
		running.ID, testPullRequestURL, wakeupTestSlot.Format(time.RFC3339), testHeadSHA), string(f.wakeup(t, both.ID).Events))
	require.Equal(t, defaultPullRequestWaitPrompt, f.wakeup(t, both.ID).Prompt)

	closed := openPullRequest(gitprovider.CheckStateSuccess)
	closed.State = gitprovider.PRStateClosed
	merged := closed
	merged.State = gitprovider.PRStateMerged
	for name, tc := range map[string]struct {
		activity gitprovider.PullRequestActivity
		err      error
		noFunc   bool
		args     waitForEventsArgs
		wantErr  string
	}{
		"Closed":              {activity: closed, args: waitForEventsArgs{PullRequestURL: testPullRequestURL}, wantErr: "pull request " + testPullRequestURL + " is already closed"},
		"Merged":              {activity: merged, args: waitForEventsArgs{PullRequestURL: testPullRequestURL}, wantErr: "is already merged"},
		"FetchError":          {err: xerrors.New("the chat owner has no linked account for github.com"), args: waitForEventsArgs{PullRequestURL: testPullRequestURL}, wantErr: "no linked account for github.com"},
		"NotAvailable":        {noFunc: true, args: waitForEventsArgs{PullRequestURL: testPullRequestURL}, wantErr: "pull request events are not available"},
		"UnknownEvent":        {args: waitForEventsArgs{PullRequestURL: testPullRequestURL, PullRequestEvents: []string{"review", "merged"}}, wantErr: `unknown pull_request_events value "merged", valid values are review, comment, checks, closed`},
		"EventsWithoutURL":    {args: waitForEventsArgs{PullRequestEvents: []string{"review"}}, wantErr: "pull_request_events requires pull_request_url"},
		"UnknownChildWithURL": {activity: openPullRequest(gitprovider.CheckStateNone), args: waitForEventsArgs{PullRequestURL: testPullRequestURL, ChildChatIDs: []string{uuid.NewString()}}, wantErr: "is not a child of this chat"},
	} {
		t.Logf("case %s", name)
		fake.set(tc.activity, tc.err)
		server.forkPullRequestActivity = fake.fetch
		if tc.noFunc {
			server.forkPullRequestActivity = nil
		}
		_, content := waitCall(uuid.New(), tc.args, true)
		require.Contains(t, content, tc.wantErr)
	}
	server.forkPullRequestActivity = fake.fetch

	// A replay returns the stored baseline without reading the pull request.
	fake.set(merged, nil)
	calls := fake.callCount()
	replayed, _ := waitCall(onlyID, waitForEventsArgs{PullRequestURL: testPullRequestURL}, false)
	require.Equal(t, only, replayed)
	require.Equal(t, calls, fake.callCount())

	fdb, err := forkdb.From(f.db)
	require.NoError(t, err)
	rows, err := fdb.ListChatWakeupsByChatID(ctx, coordinator.ID)
	require.NoError(t, err)
	require.Len(t, rows, 2, "refused calls create nothing")
}

// pullRequestEvents returns the events of a wake-up that watches the test
// pull request for on since an hour before wakeupTestSlot, from a baseline of
// head and checks.
func pullRequestEvents(on []string, head string, checks gitprovider.CheckState) json.RawMessage {
	raw, err := json.Marshal(wakeupEvent{
		Type:        wakeupEventPullRequest,
		URL:         testPullRequestURL,
		On:          on,
		Since:       wakeupTestSlot.Add(-time.Hour),
		HeadSHA:     head,
		ChecksState: string(checks),
	})
	if err != nil {
		panic(err)
	}
	return json.RawMessage("[" + string(raw) + "]")
}

func TestWakeupPullRequestEvents(t *testing.T) {
	t.Parallel()
	all := pullRequestEventKinds
	review := gitprovider.PullRequestEvent{ID: 1, Author: "alice", State: "approved", URL: testPullRequestURL + "#pullrequestreview-1", CreatedAt: wakeupTestSlot.Add(-time.Minute)}
	comment := gitprovider.PullRequestEvent{ID: 2, Author: "bob", URL: testPullRequestURL + "#issuecomment-2", CreatedAt: wakeupTestSlot.Add(-2 * time.Minute)}
	withActivity := func(activity gitprovider.PullRequestActivity, edit func(*gitprovider.PullRequestActivity)) gitprovider.PullRequestActivity {
		edit(&activity)
		return activity
	}
	manyComments := make([]gitprovider.PullRequestEvent, 0, maxPullRequestSummaryItems+2)
	for i := range maxPullRequestSummaryItems + 2 {
		manyComments = append(manyComments, gitprovider.PullRequestEvent{ID: int64(i), Author: "bob", URL: fmt.Sprintf("%s#issuecomment-%d", testPullRequestURL, i), CreatedAt: wakeupTestSlot.Add(time.Duration(i-100) * time.Second)})
	}
	header := "Pull request " + testPullRequestURL + " has new activity:"

	for _, tc := range []struct {
		name         string
		on           []string
		baselineHead string
		baseline     gitprovider.CheckState
		activity     gitprovider.PullRequestActivity
		// wantLines is the summary, or nil when no message is due.
		wantLines []string
	}{
		{
			name: "NoActivity", on: all, baselineHead: testHeadSHA, baseline: gitprovider.CheckStatePending,
			activity: openPullRequest(gitprovider.CheckStatePending),
		},
		{
			name: "ReviewAndComment", on: all, baselineHead: testHeadSHA, baseline: gitprovider.CheckStatePending,
			activity: withActivity(openPullRequest(gitprovider.CheckStatePending), func(a *gitprovider.PullRequestActivity) {
				a.Reviews = []gitprovider.PullRequestEvent{review}
				a.Comments = []gitprovider.PullRequestEvent{comment}
			}),
			wantLines: []string{
				header,
				"- comment by bob (" + comment.URL + ")",
				"- review by alice: approved (" + review.URL + ")",
				"- checks: pending, 0 of 3 failed, on 0123456789ab",
			},
		},
		{
			name: "ChecksOnlyIgnoresComment", on: []string{"checks"}, baselineHead: testHeadSHA, baseline: gitprovider.CheckStatePending,
			activity: withActivity(openPullRequest(gitprovider.CheckStatePending), func(a *gitprovider.PullRequestActivity) {
				a.Comments = []gitprovider.PullRequestEvent{comment}
			}),
		},
		{
			name: "ChecksSettle", on: []string{"checks"}, baselineHead: testHeadSHA, baseline: gitprovider.CheckStatePending,
			activity:  openPullRequest(gitprovider.CheckStateSuccess),
			wantLines: []string{header, "- checks: success, 0 of 3 failed, on 0123456789ab"},
		},
		{
			name: "ChecksUnchanged", on: all, baselineHead: testHeadSHA, baseline: gitprovider.CheckStateSuccess,
			activity: openPullRequest(gitprovider.CheckStateSuccess),
		},
		{
			name: "NewHeadFailure", on: []string{"checks"}, baselineHead: testHeadSHA, baseline: gitprovider.CheckStateFailure,
			activity: withActivity(openPullRequest(gitprovider.CheckStateFailure), func(a *gitprovider.PullRequestActivity) {
				a.HeadSHA = testNewHeadSHA
				a.Checks.Failed = 1
			}),
			wantLines: []string{header, "- checks: failure, 1 of 3 failed, on fedcba987654"},
		},
		{
			name: "Merged", on: []string{"closed"}, baselineHead: testHeadSHA, baseline: gitprovider.CheckStateSuccess,
			activity: withActivity(openPullRequest(gitprovider.CheckStateSuccess), func(a *gitprovider.PullRequestActivity) {
				a.State = gitprovider.PRStateMerged
			}),
			wantLines: []string{header, "- state: merged"},
		},
		{
			name: "ManyComments", on: []string{"comment"}, baselineHead: testHeadSHA, baseline: gitprovider.CheckStateNone,
			activity: withActivity(openPullRequest(gitprovider.CheckStateNone), func(a *gitprovider.PullRequestActivity) {
				a.Comments = manyComments
			}),
			wantLines: func() []string {
				lines := []string{header}
				for _, c := range manyComments[:maxPullRequestSummaryItems] {
					lines = append(lines, "- comment by bob ("+c.URL+")")
				}
				return append(lines, "- and 2 more")
			}(),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newWorkerTestFixture(t)
			ctx := testutil.Context(t, testutil.WaitLong)
			chat := f.createWakeupChat(t)
			wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
				Kind:   forkdb.ChatWakeupKindEvents,
				Events: pullRequestEvents(tc.on, tc.baselineHead, tc.baseline),
			})
			worker := f.newArchiveWorker(t, nil, nil, nil)
			fake := &fakePullRequestActivity{activity: tc.activity}
			worker.server.forkPullRequestActivity = fake.fetch
			now := wakeupTestSlot.Add(time.Second)

			worker.wakeupOnce(ctx, now)
			require.Equal(t, []fakePullRequestCall{{ownerID: chat.OwnerID, url: testPullRequestURL, since: wakeupTestSlot.Add(-time.Hour)}}, fake.calls)
			got := f.wakeup(t, wakeup.ID)
			if tc.wantLines == nil {
				require.Empty(t, f.wakeMessages(t, chat, wakeup.Prompt))
				require.Empty(t, f.wakeupRunKeys(t, wakeup.ID))
				require.Equal(t, forkdb.ChatWakeupStatusActive, got.Status)
				require.WithinDuration(t, now.Add(pullRequestPollInterval), got.NextRunAt.Time, time.Second)
				require.False(t, got.NextRunAt.Time.Before(now.Add(pullRequestPollInterval)))
				return
			}
			require.Equal(t, []string{wakeup.Prompt + "\n\n" + strings.Join(tc.wantLines, "\n")}, f.wakeMessages(t, chat, wakeup.Prompt))
			require.Equal(t, []string{pullRequestRunKey(testPullRequestURL)}, f.wakeupRunKeys(t, wakeup.ID))
			require.Equal(t, forkdb.ChatWakeupStatusCompleted, got.Status)
			require.Equal(t, "done", got.StatusReason.String)
		})
	}
}

// A failed read never pauses or completes the wake-up: it polls again later,
// after the rate limit resets for a rate limit.
func TestWakeupPullRequestReadFailures(t *testing.T) {
	t.Parallel()
	now := wakeupTestSlot.Add(time.Second)
	for _, tc := range []struct {
		name     string
		err      error
		wantNext time.Time
	}{
		{name: "RateLimit", err: &gitprovider.RateLimitError{RetryAfter: now.Add(10 * time.Minute)}, wantNext: now.Add(10 * time.Minute)},
		{name: "OtherError", err: xerrors.New("github is down"), wantNext: now.Add(pullRequestErrorRetry)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newWorkerTestFixture(t)
			ctx := testutil.Context(t, testutil.WaitLong)
			chat := f.createWakeupChat(t)
			wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
				Kind:   forkdb.ChatWakeupKindEvents,
				Events: pullRequestEvents(pullRequestEventKinds, testHeadSHA, gitprovider.CheckStatePending),
			})
			worker := f.newArchiveWorker(t, nil, nil, nil)
			fake := &fakePullRequestActivity{err: tc.err}
			worker.server.forkPullRequestActivity = fake.fetch

			worker.wakeupOnce(ctx, now)
			require.Empty(t, f.wakeMessages(t, chat, wakeup.Prompt))
			require.Empty(t, f.wakeupRunStatuses(t, wakeup.ID))
			got := f.wakeup(t, wakeup.ID)
			require.Equal(t, forkdb.ChatWakeupStatusActive, got.Status)
			require.WithinDuration(t, tc.wantNext, got.NextRunAt.Time, time.Second)
		})
	}
}

// A done child and a pull request with activity are delivered one per tick,
// in the order of the events, and the pull request is not read in the tick
// that delivers the child.
func TestWakeupPullRequestAfterChild(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	coordinator := f.createWakeupChat(t)
	child := f.createChildAgent(t, coordinator, "child", database.ChatStatusWaiting)
	events := strings.TrimSuffix(string(childDoneEvents(child)), "]") + "," +
		strings.TrimPrefix(string(pullRequestEvents(pullRequestEventKinds, testHeadSHA, gitprovider.CheckStatePending)), "[")
	wakeup := f.insertWakeup(t, coordinator, forkdb.InsertChatWakeupParams{
		Kind:   forkdb.ChatWakeupKindEvents,
		Events: json.RawMessage(events),
	})
	worker := f.newArchiveWorker(t, nil, nil, nil)
	fake := &fakePullRequestActivity{activity: openPullRequest(gitprovider.CheckStatePending)}
	fake.activity.State = gitprovider.PRStateClosed
	worker.server.forkPullRequestActivity = fake.fetch

	now := wakeupTestSlot.Add(time.Second)
	worker.wakeupOnce(ctx, now)
	require.Len(t, f.wakeMessages(t, coordinator, wakeup.Prompt), 1)
	require.Equal(t, []string{childDoneRunKey(child.ID)}, f.wakeupRunKeys(t, wakeup.ID))
	require.Zero(t, fake.callCount())
	got := f.wakeup(t, wakeup.ID)
	require.Equal(t, forkdb.ChatWakeupStatusActive, got.Status)
	require.WithinDuration(t, now.Add(pullRequestPollInterval), got.NextRunAt.Time, time.Second)

	forceExecutionState(t, f, coordinator.ID, database.ChatStatusWaiting, false)
	worker.wakeupOnce(ctx, now.Add(pullRequestPollInterval+time.Second))
	messages := f.wakeMessages(t, coordinator, wakeup.Prompt)
	require.Len(t, messages, 2)
	require.True(t, strings.HasSuffix(messages[1], "\n- state: closed"), messages[1])
	require.Equal(t, []string{childDoneRunKey(child.ID), pullRequestRunKey(testPullRequestURL)}, f.wakeupRunKeys(t, wakeup.ID))
	require.Equal(t, forkdb.ChatWakeupStatusCompleted, f.wakeup(t, wakeup.ID).Status)
}

// A pull request with activity pauses a wake-up at the daily run cap instead
// of sending.
func TestWakeupPullRequestCap(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	chat := f.createWakeupChat(t)
	wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
		Kind:   forkdb.ChatWakeupKindEvents,
		Events: pullRequestEvents([]string{"closed"}, testHeadSHA, gitprovider.CheckStatePending),
	})
	f.seedDeliveredRuns(t, wakeup, maxChatWakeupRunsPerDay, wakeupTestSlot.Add(-time.Hour))
	worker := f.newArchiveWorker(t, nil, nil, nil)
	enqueuer := useFakeEnqueuer(worker)
	fake := &fakePullRequestActivity{activity: openPullRequest(gitprovider.CheckStatePending)}
	fake.activity.State = gitprovider.PRStateMerged
	worker.server.forkPullRequestActivity = fake.fetch

	worker.wakeupOnce(ctx, wakeupTestSlot.Add(time.Second))
	require.Empty(t, f.wakeMessages(t, chat, wakeup.Prompt))
	require.NotContains(t, f.wakeupRunKeys(t, wakeup.ID), pullRequestRunKey(testPullRequestURL))
	got := f.wakeup(t, wakeup.ID)
	require.Equal(t, forkdb.ChatWakeupStatusPaused, got.Status)
	require.Equal(t, wakeupReasonDailyRunCap, got.StatusReason.String)
	requireWakeupPausedNotification(t, enqueuer.Sent(), chat, wakeupReasonDailyRunCap)
}
