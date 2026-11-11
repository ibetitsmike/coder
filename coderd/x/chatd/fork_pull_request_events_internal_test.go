package chatd

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
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
	ownerID       uuid.UUID
	url           string
	since         time.Time
	includeChecks bool
	// boundedRead is whether the read's deadline is at most
	// pullRequestReadTimeout away.
	boundedRead bool
}

func (f *fakePullRequestActivity) fetch(ctx context.Context, ownerID uuid.UUID, pullRequestURL string, since time.Time, includeChecks bool) (*gitprovider.PullRequestActivity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	deadline, ok := ctx.Deadline()
	boundedRead := ok && time.Until(deadline) <= pullRequestReadTimeout
	f.calls = append(f.calls, fakePullRequestCall{ownerID: ownerID, url: pullRequestURL, since: since, includeChecks: includeChecks, boundedRead: boundedRead})
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
	// Longer than pullRequestReadTimeout, so the fake can tell that the
	// tool bounds its read.
	ctx := dbauthz.AsChatd(testutil.Context(t, testutil.WaitSuperLong))
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
	require.Equal(t, []fakePullRequestCall{{ownerID: coordinator.OwnerID, url: testPullRequestURL, since: wakeupTestSlot, includeChecks: true, boundedRead: true}}, fake.calls)

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

	// A watch without checks does not read CI, which needs a permission that
	// the other events do not.
	waitCall(uuid.New(), waitForEventsArgs{PullRequestURL: testPullRequestURL, PullRequestEvents: []string{"review"}}, false)
	require.False(t, fake.calls[len(fake.calls)-1].includeChecks)

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
		// local marks an argument error that is found before the pull
		// request read, which costs the owner's quota.
		local bool
	}{
		"Closed":              {activity: closed, args: waitForEventsArgs{PullRequestURL: testPullRequestURL}, wantErr: "pull request " + testPullRequestURL + " is already closed"},
		"Merged":              {activity: merged, args: waitForEventsArgs{PullRequestURL: testPullRequestURL}, wantErr: "is already merged"},
		"FetchError":          {err: xerrors.New("the chat owner has no linked account for github.com"), args: waitForEventsArgs{PullRequestURL: testPullRequestURL}, wantErr: "no linked account for github.com"},
		"NotAvailable":        {noFunc: true, args: waitForEventsArgs{PullRequestURL: testPullRequestURL}, wantErr: "pull request events are not available"},
		"UnknownEvent":        {args: waitForEventsArgs{PullRequestURL: testPullRequestURL, PullRequestEvents: []string{"review", "merged"}}, wantErr: `unknown pull_request_events value "merged", valid values are review, comment, checks, closed`},
		"EventsWithoutURL":    {args: waitForEventsArgs{PullRequestEvents: []string{"review"}}, wantErr: "pull_request_events requires pull_request_url"},
		"UnknownChildWithURL": {activity: openPullRequest(gitprovider.CheckStateNone), args: waitForEventsArgs{PullRequestURL: testPullRequestURL, ChildChatIDs: []string{uuid.NewString()}}, wantErr: "is not a child of this chat", local: true},
		"BadExpiryWithURL":    {activity: openPullRequest(gitprovider.CheckStateNone), args: waitForEventsArgs{PullRequestURL: testPullRequestURL, ExpiresInSeconds: new(30)}, wantErr: "expires_in_seconds", local: true},
	} {
		t.Logf("case %s", name)
		fake.set(tc.activity, tc.err)
		server.forkPullRequestActivity = fake.fetch
		if tc.noFunc {
			server.forkPullRequestActivity = nil
		}
		calls := fake.callCount()
		_, content := waitCall(uuid.New(), tc.args, true)
		require.Contains(t, content, tc.wantErr)
		if tc.local {
			require.Equal(t, calls, fake.callCount(), "the pull request is not read")
		}
	}
	server.forkPullRequestActivity = fake.fetch

	// A pull request that closes during the baseline read is watched, so
	// the first poll reports the closure.
	closedDuringRead := closed
	closedDuringRead.ClosedAt = wakeupTestSlot.Add(time.Second)
	fake.set(closedDuringRead, nil)
	closing, _ := waitCall(uuid.New(), waitForEventsArgs{PullRequestURL: testPullRequestURL}, false)
	require.Equal(t, testPullRequestURL, closing.PullRequest.URL)

	// Reviews and comments of the second of the baseline read are stored as
	// seen. A later one was created during the read and is left to the poll.
	withReview := openPullRequest(gitprovider.CheckStatePending)
	withReview.Reviews = []gitprovider.PullRequestEvent{
		{ID: 1, Author: "alice", State: "approved", URL: testPullRequestURL + "#pullrequestreview-1", CreatedAt: wakeupTestSlot},
		{ID: 2, Author: "alice", State: "approved", URL: testPullRequestURL + "#pullrequestreview-2", CreatedAt: wakeupTestSlot.Add(time.Second)},
	}
	fake.set(withReview, nil)
	seen, _ := waitCall(uuid.New(), waitForEventsArgs{PullRequestURL: testPullRequestURL}, false)
	var seenEvents []wakeupEvent
	require.NoError(t, json.Unmarshal(f.wakeup(t, seen.ID).Events, &seenEvents))
	require.Equal(t, []string{testPullRequestURL + "#pullrequestreview-1"}, seenEvents[0].Seen)

	// CI that settled during the baseline read keeps the start of the read
	// as its baseline time, so the first poll reports it.
	settledDuringRead := openPullRequest(gitprovider.CheckStateSuccess)
	settledDuringRead.Checks.UpdatedAt = wakeupTestSlot.Add(time.Second)
	fake.set(settledDuringRead, nil)
	settled, _ := waitCall(uuid.New(), waitForEventsArgs{PullRequestURL: testPullRequestURL}, false)
	var settledEvents []wakeupEvent
	require.NoError(t, json.Unmarshal(f.wakeup(t, settled.ID).Events, &settledEvents))
	require.Equal(t, wakeupTestSlot, settledEvents[0].ChecksUpdatedAt)

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
	require.Len(t, rows, 6, "refused calls create nothing")
}

// pullRequestEvents returns the events of a wake-up that watches the test
// pull request for on since an hour before wakeupTestSlot, from a baseline of
// head and checks that already listed the events at the seen URLs.
func pullRequestEvents(on []string, head string, checks gitprovider.CheckState, seen ...string) json.RawMessage {
	raw, err := json.Marshal(wakeupEvent{
		Type:        wakeupEventPullRequest,
		URL:         testPullRequestURL,
		On:          on,
		Since:       wakeupTestSlot.Add(-time.Hour),
		HeadSHA:     head,
		ChecksState: string(checks),
		Seen:        seen,
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
		seen         []string
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
			name: "BaselineEventsIgnored", on: all, baselineHead: testHeadSHA, baseline: gitprovider.CheckStatePending,
			seen: []string{review.URL},
			activity: withActivity(openPullRequest(gitprovider.CheckStatePending), func(a *gitprovider.PullRequestActivity) {
				a.Reviews = []gitprovider.PullRequestEvent{review}
			}),
		},
		{
			name: "NewCommentAfterBaselineEvents", on: all, baselineHead: testHeadSHA, baseline: gitprovider.CheckStatePending,
			seen: []string{review.URL},
			activity: withActivity(openPullRequest(gitprovider.CheckStatePending), func(a *gitprovider.PullRequestActivity) {
				a.Reviews = []gitprovider.PullRequestEvent{review}
				a.Comments = []gitprovider.PullRequestEvent{comment}
			}),
			wantLines: []string{
				header,
				"- comment by bob (" + comment.URL + ")",
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
			name: "ChecksRerunSameState", on: []string{"checks"}, baselineHead: testHeadSHA, baseline: gitprovider.CheckStateSuccess,
			activity: withActivity(openPullRequest(gitprovider.CheckStateSuccess), func(a *gitprovider.PullRequestActivity) {
				a.Checks.UpdatedAt = wakeupTestSlot.Add(-time.Minute)
			}),
			wantLines: []string{header, "- checks: success, 0 of 3 failed, on 0123456789ab"},
		},
		{
			name: "NewHeadPending", on: []string{"checks"}, baselineHead: testHeadSHA, baseline: gitprovider.CheckStateSuccess,
			activity: withActivity(openPullRequest(gitprovider.CheckStatePending), func(a *gitprovider.PullRequestActivity) {
				a.HeadSHA = testNewHeadSHA
			}),
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
				Events: pullRequestEvents(tc.on, tc.baselineHead, tc.baseline, tc.seen...),
			})
			worker := f.newArchiveWorker(t, nil, nil, nil)
			fake := &fakePullRequestActivity{activity: tc.activity}
			worker.server.forkPullRequestActivity = fake.fetch
			now := wakeupTestSlot.Add(time.Second)

			worker.wakeupOnce(ctx, now)
			require.Equal(t, []fakePullRequestCall{{
				ownerID:       chat.OwnerID,
				url:           testPullRequestURL,
				since:         wakeupTestSlot.Add(-time.Hour),
				includeChecks: slices.Contains(tc.on, pullRequestEventChecks),
				boundedRead:   true,
			}}, fake.calls)
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
// after the rate limit resets for a rate limit, but no later than
// pullRequestErrorRetry while a watched child is still running.
func TestWakeupPullRequestReadFailures(t *testing.T) {
	t.Parallel()
	now := wakeupTestSlot.Add(time.Second)
	for _, tc := range []struct {
		name      string
		err       error
		withChild bool
		wantNext  time.Time
	}{
		{name: "RateLimit", err: &gitprovider.RateLimitError{RetryAfter: now.Add(10 * time.Minute)}, wantNext: now.Add(10 * time.Minute)},
		{name: "RateLimitWithRunningChild", err: &gitprovider.RateLimitError{RetryAfter: now.Add(10 * time.Minute)}, withChild: true, wantNext: now.Add(pullRequestErrorRetry)},
		{name: "RateLimitResetsSooner", err: &gitprovider.RateLimitError{RetryAfter: now.Add(10 * time.Second)}, wantNext: now.Add(pullRequestPollInterval)},
		{name: "OtherError", err: xerrors.New("github is down"), wantNext: now.Add(pullRequestErrorRetry)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newWorkerTestFixture(t)
			ctx := testutil.Context(t, testutil.WaitLong)
			chat := f.createWakeupChat(t)
			events := pullRequestEvents(pullRequestEventKinds, testHeadSHA, gitprovider.CheckStatePending)
			if tc.withChild {
				child := f.createChildAgent(t, chat, "child", database.ChatStatusRunning)
				events = json.RawMessage(strings.TrimSuffix(string(childDoneEvents(child)), "]") + "," + strings.TrimPrefix(string(events), "["))
			}
			wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
				Kind:   forkdb.ChatWakeupKindEvents,
				Events: events,
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

// A final poll that delivers a done child reads the pull request once more
// after that turn, and reports only what happened until the expiry.
func TestWakeupFinalPollAfterChild(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		closedAt    time.Duration
		wantMessage bool
		wantReason  string
	}{
		{name: "ClosedBeforeExpiry", closedAt: -time.Minute, wantMessage: true, wantReason: "done"},
		{name: "ClosedAfterExpiry", closedAt: time.Second, wantReason: "expired"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newWorkerTestFixture(t)
			ctx := testutil.Context(t, testutil.WaitLong)
			coordinator := f.createWakeupChat(t)
			child := f.createChildAgent(t, coordinator, "child", database.ChatStatusWaiting)
			events := strings.TrimSuffix(string(childDoneEvents(child)), "]") + "," +
				strings.TrimPrefix(string(pullRequestEvents([]string{"closed"}, testHeadSHA, gitprovider.CheckStatePending)), "[")
			// insertWakeup schedules the run at wakeupTestSlot, so this is the
			// final poll.
			wakeup := f.insertWakeup(t, coordinator, forkdb.InsertChatWakeupParams{
				Kind:      forkdb.ChatWakeupKindEvents,
				Events:    json.RawMessage(events),
				ExpiresAt: sql.NullTime{Time: wakeupTestSlot, Valid: true},
			})
			worker := f.newArchiveWorker(t, nil, nil, nil)
			closed := openPullRequest(gitprovider.CheckStatePending)
			closed.State, closed.ClosedAt = gitprovider.PRStateClosed, wakeupTestSlot.Add(tc.closedAt)
			fake := &fakePullRequestActivity{}
			fake.set(closed, nil)
			worker.server.forkPullRequestActivity = fake.fetch

			worker.wakeupOnce(ctx, wakeupTestSlot.Add(time.Second))
			require.Equal(t, []string{childDoneRunKey(child.ID)}, f.wakeupRunKeys(t, wakeup.ID))
			got := f.wakeup(t, wakeup.ID)
			require.Equal(t, forkdb.ChatWakeupStatusActive, got.Status)

			forceExecutionState(t, f, coordinator.ID, database.ChatStatusWaiting, false)
			worker.wakeupOnce(ctx, got.NextRunAt.Time.Add(time.Second))
			require.Equal(t, 1, fake.callCount())
			messages := f.wakeMessages(t, coordinator, wakeup.Prompt)
			if tc.wantMessage {
				require.Len(t, messages, 2)
				require.True(t, strings.HasSuffix(messages[1], "\n- state: closed"), messages[1])
			} else {
				require.Len(t, messages, 1)
			}
			got = f.wakeup(t, wakeup.ID)
			require.Equal(t, forkdb.ChatWakeupStatusCompleted, got.Status)
			require.Equal(t, tc.wantReason, got.StatusReason.String)
		})
	}
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

// The worker bounds each read, so a provider that never answers cannot block
// the worker loop.
func TestWakeupPullRequestReadDeadline(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	// Longer than pullRequestReadTimeout, so only the worker's own bound
	// passes the deadline check.
	ctx := testutil.Context(t, testutil.WaitSuperLong)
	chat := f.createWakeupChat(t)
	f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
		Kind:   forkdb.ChatWakeupKindEvents,
		Events: pullRequestEvents(pullRequestEventKinds, testHeadSHA, gitprovider.CheckStatePending),
	})
	worker := f.newArchiveWorker(t, nil, nil, nil)
	var deadline time.Time
	var hasDeadline bool
	worker.server.forkPullRequestActivity = func(ctx context.Context, _ uuid.UUID, _ string, _ time.Time, _ bool) (*gitprovider.PullRequestActivity, error) {
		deadline, hasDeadline = ctx.Deadline()
		return nil, xerrors.New("github is down")
	}

	start := time.Now()
	worker.wakeupOnce(ctx, wakeupTestSlot.Add(time.Second))
	require.True(t, hasDeadline, "the read has a deadline")
	require.False(t, deadline.After(start.Add(pullRequestReadTimeout).Add(testutil.WaitShort)), "deadline %s", deadline)
	require.Less(t, pullRequestReadTimeout, wakeupLease)
}

// A pull request read that could take the rest of the claim's lease waits
// for the next tick, so stalled reads cannot hold back the rest of a batch.
func TestWakeupPullRequestReadNearLeaseEnd(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	chat := f.createWakeupChat(t)
	wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
		Kind:   forkdb.ChatWakeupKindEvents,
		Events: pullRequestEvents([]string{"closed"}, testHeadSHA, gitprovider.CheckStatePending),
	})
	worker := f.newArchiveWorker(t, nil, nil, nil)
	merged := openPullRequest(gitprovider.CheckStatePending)
	merged.State = gitprovider.PRStateMerged
	fake := &fakePullRequestActivity{}
	fake.set(merged, nil)
	worker.server.forkPullRequestActivity = fake.fetch
	fdb, err := forkdb.From(worker.opts.Store)
	require.NoError(t, err)

	now := wakeupTestSlot.Add(time.Second)
	rows, err := fdb.ClaimDueChatWakeups(dbauthz.AsChatd(ctx), forkdb.ClaimDueChatWakeupsParams{
		Now:            now,
		LeaseExpiresAt: now.Add(wakeupLease),
		LimitCount:     1,
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	late := rows[0].LeaseExpiresAt.Time.Add(-2*pullRequestReadTimeout + time.Second)
	require.NoError(t, worker.server.deliverWakeup(dbauthz.AsChatd(ctx), fdb, rows[0], late))
	require.Zero(t, fake.callCount())
	got := f.wakeup(t, wakeup.ID)
	require.Equal(t, forkdb.ChatWakeupStatusActive, got.Status)
	require.True(t, wakeup.NextRunAt.Time.Equal(got.NextRunAt.Time), "stays due: %s", got.NextRunAt.Time)
	require.False(t, got.LeaseExpiresAt.Valid, "the claim ends")

	worker.wakeupOnce(ctx, now.Add(time.Second))
	require.Equal(t, 1, fake.callCount())
	require.Len(t, f.wakeMessages(t, chat, wakeup.Prompt), 1)
}

// A watch whose next poll would land after its expiry polls once more at the
// expiry, then completes. That poll runs late here, and reports only what
// happened until the expiry.
func TestWakeupPullRequestPollsUntilExpiry(t *testing.T) {
	t.Parallel()
	expiresAt := wakeupTestSlot.Add(30 * time.Second)
	merged := func(at time.Time) func(*gitprovider.PullRequestActivity) {
		return func(a *gitprovider.PullRequestActivity) { a.State, a.ClosedAt = gitprovider.PRStateMerged, at }
	}
	for _, tc := range []struct {
		name        string
		on          []string
		final       func(*gitprovider.PullRequestActivity)
		wantMessage bool
		wantReason  string
	}{
		{name: "ActivityAtLastPoll", on: []string{"closed"}, final: merged(expiresAt.Add(-5 * time.Second)), wantMessage: true, wantReason: "done"},
		{name: "NothingAtLastPoll", on: []string{"closed"}, final: func(*gitprovider.PullRequestActivity) {}, wantReason: "expired"},
		{name: "MergedAfterExpiry", on: []string{"closed"}, final: merged(expiresAt.Add(30 * time.Second)), wantReason: "expired"},
		{name: "ReviewAfterExpiry", on: []string{"review"}, final: func(a *gitprovider.PullRequestActivity) {
			a.Reviews = []gitprovider.PullRequestEvent{{ID: 1, Author: "alice", State: "approved", URL: testPullRequestURL + "#pullrequestreview-1", CreatedAt: expiresAt.Add(30 * time.Second)}}
		}, wantReason: "expired"},
		{name: "ChecksSettleAfterExpiry", on: []string{"checks"}, final: func(a *gitprovider.PullRequestActivity) {
			a.Checks = gitprovider.CheckSummary{State: gitprovider.CheckStateSuccess, Total: 3, UpdatedAt: expiresAt.Add(30 * time.Second)}
		}, wantReason: "expired"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newWorkerTestFixture(t)
			ctx := testutil.Context(t, testutil.WaitLong)
			chat := f.createWakeupChat(t)
			wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
				Kind:      forkdb.ChatWakeupKindEvents,
				Events:    pullRequestEvents(tc.on, testHeadSHA, gitprovider.CheckStatePending),
				ExpiresAt: sql.NullTime{Time: expiresAt, Valid: true},
			})
			worker := f.newArchiveWorker(t, nil, nil, nil)
			fake := &fakePullRequestActivity{activity: openPullRequest(gitprovider.CheckStatePending)}
			worker.server.forkPullRequestActivity = fake.fetch

			worker.wakeupOnce(ctx, wakeupTestSlot.Add(time.Second))
			got := f.wakeup(t, wakeup.ID)
			require.Equal(t, forkdb.ChatWakeupStatusActive, got.Status)
			require.True(t, expiresAt.Equal(got.NextRunAt.Time), "next run %s", got.NextRunAt.Time)

			final := openPullRequest(gitprovider.CheckStatePending)
			tc.final(&final)
			fake.set(final, nil)
			worker.wakeupOnce(ctx, expiresAt.Add(time.Minute))
			require.Equal(t, 2, fake.callCount())
			if tc.wantMessage {
				require.Len(t, f.wakeMessages(t, chat, wakeup.Prompt), 1)
			} else {
				require.Empty(t, f.wakeMessages(t, chat, wakeup.Prompt))
			}
			got = f.wakeup(t, wakeup.ID)
			require.Equal(t, forkdb.ChatWakeupStatusCompleted, got.Status)
			require.Equal(t, tc.wantReason, got.StatusReason.String)
		})
	}
}

// A failed final poll is retried until pullRequestFinalRetryWindow after the
// expiry, and the retry reports what happened until the expiry.
func TestWakeupPullRequestFinalPollRetry(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		retryAfter time.Duration
		wantRetry  bool
	}{
		{name: "WithinWindow", retryAfter: 10 * time.Minute, wantRetry: true},
		{name: "PastWindow", retryAfter: pullRequestFinalRetryWindow + time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newWorkerTestFixture(t)
			ctx := testutil.Context(t, testutil.WaitLong)
			chat := f.createWakeupChat(t)
			// insertWakeup schedules the run at wakeupTestSlot, so this is the
			// final poll.
			expiresAt := wakeupTestSlot
			wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
				Kind:      forkdb.ChatWakeupKindEvents,
				Events:    pullRequestEvents([]string{"closed"}, testHeadSHA, gitprovider.CheckStatePending),
				ExpiresAt: sql.NullTime{Time: expiresAt, Valid: true},
			})
			worker := f.newArchiveWorker(t, nil, nil, nil)
			retryAt := expiresAt.Add(tc.retryAfter)
			fake := &fakePullRequestActivity{err: &gitprovider.RateLimitError{RetryAfter: retryAt}}
			worker.server.forkPullRequestActivity = fake.fetch

			worker.wakeupOnce(ctx, expiresAt.Add(time.Second))
			got := f.wakeup(t, wakeup.ID)
			if !tc.wantRetry {
				require.Equal(t, forkdb.ChatWakeupStatusCompleted, got.Status)
				require.Equal(t, "expired", got.StatusReason.String)
				return
			}
			require.Equal(t, forkdb.ChatWakeupStatusActive, got.Status)
			require.True(t, retryAt.Equal(got.NextRunAt.Time), "next run %s", got.NextRunAt.Time)

			merged := openPullRequest(gitprovider.CheckStatePending)
			merged.State, merged.ClosedAt = gitprovider.PRStateMerged, expiresAt.Add(-time.Minute)
			fake.set(merged, nil)
			worker.wakeupOnce(ctx, retryAt.Add(time.Second))
			require.Len(t, f.wakeMessages(t, chat, wakeup.Prompt), 1)
			got = f.wakeup(t, wakeup.ID)
			require.Equal(t, forkdb.ChatWakeupStatusCompleted, got.Status)
			require.Equal(t, "done", got.StatusReason.String)
		})
	}
}

// A done child counts at a final poll that runs on time, but not at one that
// runs well after the expiry, when the child may have finished after it.
func TestWakeupFinalPollChildren(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name             string
		late             time.Duration
		doneBeforeExpiry bool
		wantMessage      bool
		wantReason       string
	}{
		{name: "OnTime", late: time.Second, wantMessage: true, wantReason: "done"},
		{name: "Late", late: time.Minute, wantReason: "expired"},
		// The turn of another child can keep the chat busy past the cutoff.
		{name: "LateDoneBeforeExpiry", late: time.Minute, doneBeforeExpiry: true, wantMessage: true, wantReason: "done"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newWorkerTestFixture(t)
			ctx := testutil.Context(t, testutil.WaitLong)
			chat := f.createWakeupChat(t)
			child := f.createChildAgent(t, chat, "child", database.ChatStatusWaiting)
			if tc.doneBeforeExpiry {
				_, err := f.sqlDB.ExecContext(ctx, "UPDATE chats SET updated_at = $1 WHERE id = $2", wakeupTestSlot.Add(-time.Minute), child.ID)
				require.NoError(t, err)
			}
			// insertWakeup schedules the run at wakeupTestSlot, so this is the
			// final poll.
			wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
				Kind:      forkdb.ChatWakeupKindEvents,
				Events:    childDoneEvents(child),
				ExpiresAt: sql.NullTime{Time: wakeupTestSlot, Valid: true},
			})
			worker := f.newArchiveWorker(t, nil, nil, nil)

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

// A pull request wake-up that is claimed after its expiry because it missed
// the poll before still reads what happened until the expiry.
func TestWakeupPullRequestMissedTickBeforeExpiry(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	chat := f.createWakeupChat(t)
	// insertWakeup schedules the run at wakeupTestSlot, before the expiry.
	expiresAt := wakeupTestSlot.Add(time.Minute)
	wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
		Kind:      forkdb.ChatWakeupKindEvents,
		Events:    pullRequestEvents([]string{"closed"}, testHeadSHA, gitprovider.CheckStatePending),
		ExpiresAt: sql.NullTime{Time: expiresAt, Valid: true},
	})
	worker := f.newArchiveWorker(t, nil, nil, nil)
	merged := openPullRequest(gitprovider.CheckStatePending)
	merged.State, merged.ClosedAt = gitprovider.PRStateMerged, expiresAt.Add(-time.Second)
	fake := &fakePullRequestActivity{}
	fake.set(merged, nil)
	worker.server.forkPullRequestActivity = fake.fetch

	worker.wakeupOnce(ctx, expiresAt.Add(time.Second))
	require.Len(t, f.wakeMessages(t, chat, wakeup.Prompt), 1)
	got := f.wakeup(t, wakeup.ID)
	require.Equal(t, forkdb.ChatWakeupStatusCompleted, got.Status)
	require.Equal(t, "done", got.StatusReason.String)
}

// A final poll whose send fails transiently is retried, and the retry
// delivers what the final poll found, although it runs well after the expiry.
func TestWakeupFinalSendRetry(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"PullRequest", "Child"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newWorkerTestFixture(t)
			ctx := testutil.Context(t, testutil.WaitLong)
			chat := f.createWakeupChat(t)
			events := pullRequestEvents([]string{"closed"}, testHeadSHA, gitprovider.CheckStatePending)
			if name == "Child" {
				events = childDoneEvents(f.createChildAgent(t, chat, "child", database.ChatStatusWaiting))
			}
			// insertWakeup schedules the run at wakeupTestSlot, so this is
			// the final poll.
			wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
				Kind:      forkdb.ChatWakeupKindEvents,
				Events:    events,
				ExpiresAt: sql.NullTime{Time: wakeupTestSlot, Valid: true},
			})
			worker := f.newArchiveWorker(t, nil, nil, nil)
			merged := openPullRequest(gitprovider.CheckStatePending)
			merged.State, merged.ClosedAt = gitprovider.PRStateMerged, wakeupTestSlot.Add(-time.Minute)
			fake := &fakePullRequestActivity{}
			fake.set(merged, nil)
			worker.server.forkPullRequestActivity = fake.fetch
			var hookDown atomic.Bool
			hookDown.Store(true)
			useUserPromptHook(t, worker, func(rw http.ResponseWriter) {
				if hookDown.Load() {
					rw.WriteHeader(http.StatusInternalServerError)
					return
				}
				_, _ = rw.Write([]byte(`{}`))
			})

			failedAt := wakeupTestSlot.Add(time.Second)
			worker.wakeupOnce(ctx, failedAt)
			require.Empty(t, f.wakeMessages(t, chat, wakeup.Prompt))
			got := f.wakeup(t, wakeup.ID)
			require.Equal(t, forkdb.ChatWakeupStatusActive, got.Status)
			// The worker adds the time it has taken since the claim.
			require.WithinDuration(t, failedAt.Add(pullRequestErrorRetry), got.NextRunAt.Time, time.Minute)

			hookDown.Store(false)
			worker.wakeupOnce(ctx, got.NextRunAt.Time.Add(time.Second))
			require.Len(t, f.wakeMessages(t, chat, wakeup.Prompt), 1)
			got = f.wakeup(t, wakeup.ID)
			require.Equal(t, forkdb.ChatWakeupStatusCompleted, got.Status)
			require.Equal(t, "done", got.StatusReason.String)
		})
	}
}
