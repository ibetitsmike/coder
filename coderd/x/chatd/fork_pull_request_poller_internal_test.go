package chatd

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/externalauth/gitprovider"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/quartz"
)

// fakeGitHub is a GitHub API for pull request 7 of org/repo that answers a
// conditional request for an unchanged body with 304 and records every
// request.
type fakeGitHub struct {
	provider gitprovider.Provider
	url      string

	mu       sync.Mutex
	requests []fakeGitHubRequest
	comments string
	// hang, when set, holds every request until it is closed or the
	// request ends. hung gets a value for each held request.
	hang chan struct{}
	hung chan struct{}
}

type fakeGitHubRequest struct {
	path        string
	ifNoneMatch string
	notModified bool
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	g := &fakeGitHub{comments: "[]", hung: make(chan struct{}, 16)}
	srv := httptest.NewTLSServer(http.HandlerFunc(g.serve))
	t.Cleanup(srv.Close)
	gp, err := gitprovider.New("github", srv.URL+"/api/v3", srv.Client())
	require.NoError(t, err)
	g.provider = gp
	g.url = srv.URL + "/org/repo/pull/7"
	return g
}

func (g *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	hang, comments := g.hang, g.comments
	g.mu.Unlock()
	if hang != nil {
		g.hung <- struct{}{}
		select {
		case <-hang:
		case <-r.Context().Done():
			return
		}
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v3")
	var body string
	switch path {
	case "/user":
		body = `{"login":"owner"}`
	case "/repos/org/repo/pulls/7":
		body = fmt.Sprintf(`{"state":"open","merged":false,"head":{"sha":%q}}`, testHeadSHA)
	case "/repos/org/repo/pulls/7/reviews", "/repos/org/repo/pulls/7/comments":
		body = "[]"
	case "/repos/org/repo/issues/7/comments":
		body = comments
	default:
		http.NotFound(w, r)
		return
	}
	sum := sha256.Sum256([]byte(body))
	etag := `"` + hex.EncodeToString(sum[:]) + `"`
	request := fakeGitHubRequest{path: path, ifNoneMatch: r.Header.Get("If-None-Match")}
	request.notModified = request.ifNoneMatch == etag
	g.mu.Lock()
	g.requests = append(g.requests, request)
	g.mu.Unlock()
	w.Header().Set("ETag", etag)
	if request.notModified {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

// fetch is a ForkPullRequestActivityFunc that reads the fake with a token
// of the owner.
func (g *fakeGitHub) fetch(ctx context.Context, ownerID uuid.UUID, pullRequestURL string, since time.Time, includeChecks bool, viewerLogin string) (*gitprovider.PullRequestActivity, error) {
	ref, ok := g.provider.ParsePullRequestURL(pullRequestURL)
	if !ok {
		return nil, xerrors.Errorf("%s is not a pull request URL", pullRequestURL)
	}
	return gitprovider.FetchPullRequestActivity(ctx, g.provider, "token-"+ownerID.String(), ref, since, includeChecks, viewerLogin)
}

// takeRequests returns the requests since the last call.
func (g *fakeGitHub) takeRequests() []fakeGitHubRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	requests := g.requests
	g.requests = nil
	return requests
}

func (g *fakeGitHub) setComments(comments string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.comments = comments
}

// pollerTest is a server of f on a mock clock at wakeupTestSlot whose pull
// request reads go to a fake GitHub.
type pollerTest struct {
	f      *workerTestFixture
	github *fakeGitHub
	server *Server
	clock  *quartz.Mock
	fdb    forkdb.Store
	ctx    context.Context
}

func newPollerTest(t *testing.T) *pollerTest {
	t.Helper()
	f := newWorkerTestFixture(t)
	clock := quartz.NewMock(t)
	clock.Set(wakeupTestSlot)
	server := newUnstartedServer(t, f.pubsub, f.db, withInternalTestServerClock(clock))
	github := newFakeGitHub(t)
	server.forkPullRequestActivity = github.fetch
	fdb, err := forkdb.From(f.db)
	require.NoError(t, err)
	//nolint:gocritic // The poller reads the pull requests of every chat.
	ctx := dbauthz.AsChatd(testutil.Context(t, testutil.WaitLong))
	return &pollerTest{f: f, github: github, server: server, clock: clock, fdb: fdb, ctx: ctx}
}

// watch creates a chat whose wake-up waits for comments on the pull request,
// with the baseline read of the wait_for_events tool at wakeupTestSlot.
func (p *pollerTest) watch(t *testing.T) (database.Chat, forkdb.ChatWakeup) {
	t.Helper()
	chat := p.f.createWakeupChat(t)
	event, err := p.server.pullRequestWaitEvent(p.ctx, chat, p.github.url, []string{pullRequestEventComment})
	require.NoError(t, err)
	wakeup := p.f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
		Kind:      forkdb.ChatWakeupKindEvents,
		ExpiresAt: sql.NullTime{Time: wakeupTestSlot.Add(24 * time.Hour), Valid: true},
	}, event)
	return chat, wakeup
}

func requestPaths(requests []fakeGitHubRequest) []string {
	paths := make([]string, 0, len(requests))
	for _, request := range requests {
		paths = append(paths, request.path)
	}
	return paths
}

// pollerEndpoints are the endpoints that a poll of comments reads.
var pollerEndpoints = []string{
	"/repos/org/repo/pulls/7",
	"/repos/org/repo/pulls/7/reviews",
	"/repos/org/repo/issues/7/comments",
	"/repos/org/repo/pulls/7/comments",
}

// A poll of a pull request that did not change sends a conditional request
// to every endpoint and gets 304 for each, and the login of the token is
// read once per watch, by the baseline read.
func TestPullRequestPollerConditionalRequests(t *testing.T) {
	t.Parallel()
	p := newPollerTest(t)
	chat, wakeup := p.watch(t)
	require.ElementsMatch(t, append([]string{"/user"}, pollerEndpoints...), requestPaths(p.github.takeRequests()))

	for i := 1; i <= 3; i++ {
		now := wakeupTestSlot.Add(time.Duration(i) * pullRequestPollInterval)
		p.server.pollPullRequests(p.ctx, p.fdb, now)
		requests := p.github.takeRequests()
		require.ElementsMatch(t, pollerEndpoints, requestPaths(requests), "poll %d", i)
		for _, request := range requests {
			require.NotEmpty(t, request.ifNoneMatch, "poll %d of %s is not conditional", i, request.path)
			require.True(t, request.notModified, "poll %d of %s changed", i, request.path)
		}
		watch := p.f.pullRequestWatch(t, wakeup.ID)
		require.True(t, now.Equal(watch.PolledAt), "polled at %s", watch.PolledAt)
		require.True(t, now.Add(pullRequestPollInterval).Equal(watch.NextPollAt), "next poll %s", watch.NextPollAt)
	}
	require.Empty(t, p.f.wakeMessages(t, chat, wakeup.Prompt))
}

// Two chats of one owner that watch one pull request share one read per
// poll.
func TestPullRequestPollerSharesReads(t *testing.T) {
	t.Parallel()
	p := newPollerTest(t)
	_, first := p.watch(t)
	_, second := p.watch(t)
	_ = p.github.takeRequests()

	now := wakeupTestSlot.Add(pullRequestPollInterval)
	p.server.pollPullRequests(p.ctx, p.fdb, now)
	require.ElementsMatch(t, pollerEndpoints, requestPaths(p.github.takeRequests()))
	for _, wakeup := range []forkdb.ChatWakeup{first, second} {
		watch := p.f.pullRequestWatch(t, wakeup.ID)
		require.True(t, now.Equal(watch.PolledAt), "polled at %s", watch.PolledAt)
	}
}

// More watches of one pull request and owner than one claim takes still
// share one read.
func TestPullRequestPollerSharesReadsAcrossClaims(t *testing.T) {
	t.Parallel()
	p := newPollerTest(t)
	var wakeups []forkdb.ChatWakeup
	for range pullRequestPollerBatch + 2 {
		wakeups = append(wakeups, p.f.insertWakeup(t, p.f.createWakeupChat(t), forkdb.InsertChatWakeupParams{
			Kind:      forkdb.ChatWakeupKindEvents,
			ExpiresAt: sql.NullTime{Time: wakeupTestSlot.Add(24 * time.Hour), Valid: true},
		}, wakeupEvent{
			Type:        wakeupEventPullRequest,
			URL:         p.github.url,
			On:          []string{pullRequestEventComment},
			Since:       wakeupTestSlot,
			HeadSHA:     testHeadSHA,
			ViewerLogin: "owner",
		}))
	}

	now := wakeupTestSlot.Add(pullRequestPollInterval)
	p.server.pollPullRequests(p.ctx, p.fdb, now)
	require.ElementsMatch(t, pollerEndpoints, requestPaths(p.github.takeRequests()))
	for _, wakeup := range wakeups {
		watch := p.f.pullRequestWatch(t, wakeup.ID)
		require.True(t, now.Equal(watch.PolledAt), "polled at %s", watch.PolledAt)
	}
}

// A pass whose reads take longer than the poll interval does not claim the
// watches it read again, so it reaches the end of the backlog.
func TestPullRequestPollerClaimsOncePerPass(t *testing.T) {
	t.Parallel()
	p := newPollerTest(t)
	const watches = pullRequestPollerBatch + 1
	for i := range watches {
		p.f.insertWakeup(t, p.f.createWakeupChat(t), forkdb.InsertChatWakeupParams{
			Kind:      forkdb.ChatWakeupKindEvents,
			ExpiresAt: sql.NullTime{Time: wakeupTestSlot.Add(24 * time.Hour), Valid: true},
		}, wakeupEvent{
			Type:        wakeupEventPullRequest,
			URL:         fmt.Sprintf("https://github.example.com/org/repo/pull/%d", i+1),
			On:          []string{pullRequestEventComment},
			Since:       wakeupTestSlot,
			HeadSHA:     testHeadSHA,
			ViewerLogin: "owner",
		})
	}
	ctx, cancel := context.WithCancel(p.ctx)
	defer cancel()
	var (
		mu    sync.Mutex
		reads = map[string]int{}
		total int
	)
	p.server.forkPullRequestActivity = func(_ context.Context, _ uuid.UUID, pullRequestURL string, _ time.Time, _ bool, _ string) (*gitprovider.PullRequestActivity, error) {
		mu.Lock()
		defer mu.Unlock()
		reads[pullRequestURL]++
		total++
		if total > 2*watches {
			cancel()
		}
		// Each read takes longer than the poll interval. The mock clock
		// cannot jump over the timers of the server, so it fires them.
		target := p.clock.Now().Add(2 * pullRequestPollInterval)
		for {
			d, ok := p.clock.Peek()
			if !ok || p.clock.Now().Add(d).After(target) {
				break
			}
			_, w := p.clock.AdvanceNext()
			w.MustWait(p.ctx)
		}
		p.clock.Set(target).MustWait(p.ctx)
		return &gitprovider.PullRequestActivity{State: gitprovider.PRStateOpen, HeadSHA: testHeadSHA}, nil
	}

	p.server.pollPullRequests(ctx, p.fdb, wakeupTestSlot.Add(pullRequestPollInterval))
	require.NoError(t, ctx.Err(), "the pass must end")
	require.Len(t, reads, watches)
	for url, n := range reads {
		require.Equal(t, 1, n, "reads of %s", url)
	}
}

// A failed read of the checks does not hold back the watches of the same
// pull request that do not watch checks.
func TestPullRequestPollerChecksFailureIsolated(t *testing.T) {
	t.Parallel()
	p := newPollerTest(t)
	insert := func(on string) forkdb.ChatWakeup {
		return p.f.insertWakeup(t, p.f.createWakeupChat(t), forkdb.InsertChatWakeupParams{
			Kind:      forkdb.ChatWakeupKindEvents,
			ExpiresAt: sql.NullTime{Time: wakeupTestSlot.Add(24 * time.Hour), Valid: true},
		}, wakeupEvent{
			Type:        wakeupEventPullRequest,
			URL:         p.github.url,
			On:          []string{on},
			Since:       wakeupTestSlot,
			HeadSHA:     testHeadSHA,
			ViewerLogin: "owner",
		})
	}
	comments := insert(pullRequestEventComment)
	checks := insert(pullRequestEventChecks)

	// The fake has no checks endpoints, so the read with checks fails.
	now := wakeupTestSlot.Add(pullRequestPollInterval)
	p.server.pollPullRequests(p.ctx, p.fdb, now)
	watch := p.f.pullRequestWatch(t, comments.ID)
	require.True(t, now.Equal(watch.PolledAt), "polled at %s", watch.PolledAt)
	require.True(t, now.Add(pullRequestPollInterval).Equal(watch.NextPollAt), "next poll at %s", watch.NextPollAt)
	watch = p.f.pullRequestWatch(t, checks.ID)
	require.True(t, watch.PolledAt.IsZero(), "polled at %s", watch.PolledAt)
	require.True(t, now.Add(pullRequestErrorRetry).Equal(watch.NextPollAt), "next poll at %s", watch.NextPollAt)
}

// New activity sends exactly one message to each chat that watches the pull
// request, through the waker the poller pokes, with no clock advance.
func TestPullRequestPollerDeliversOncePerChat(t *testing.T) {
	t.Parallel()
	p := newPollerTest(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	firstChat, first := p.watch(t)
	secondChat, second := p.watch(t)
	expiresAt := first.ExpiresAt.Time
	h := p.f.startWaker(t, func(h *wakerHarness) {
		h.server.forkPullRequestActivity = p.github.fetch
	})
	start := h.clock.Now()
	for _, wakeup := range []forkdb.ChatWakeup{first, second} {
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return expiresAt.Equal(p.f.wakeup(t, wakeup.ID).NextRunAt.Time)
		}, testutil.IntervalFast, "the first pass must move the run time to the expiry")
	}

	p.github.setComments(fmt.Sprintf(`[{"id":1,"html_url":%q,"created_at":%q,"user":{"login":"alice"}}]`,
		p.github.url+"#issuecomment-1", wakeupTestSlot.Add(90*time.Second).Format(time.RFC3339)))
	h.server.pollPullRequests(p.ctx, p.fdb, wakeupTestSlot.Add(2*pullRequestPollInterval))
	for _, chat := range []database.Chat{firstChat, secondChat} {
		wakeup := first
		if chat.ID == secondChat.ID {
			wakeup = second
		}
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return len(p.f.wakeMessages(t, chat, wakeup.Prompt)) == 1
		}, testutil.IntervalFast, "the poll must deliver the comment")
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return p.f.wakeup(t, wakeup.ID).Status == forkdb.ChatWakeupStatusCompleted
		}, testutil.IntervalFast, "the delivery must complete the wake-up")
		messages := p.f.wakeMessages(t, chat, wakeup.Prompt)
		require.Len(t, messages, 1)
		require.Contains(t, messages[0], "alice")
	}
	require.True(t, start.Equal(h.clock.Now()), "no clock advance")

	_ = p.github.takeRequests()
	h.server.pollPullRequests(p.ctx, p.fdb, wakeupTestSlot.Add(3*pullRequestPollInterval))
	require.Empty(t, p.github.takeRequests(), "a fired watch is not polled again")
	require.Len(t, p.f.wakeMessages(t, firstChat, first.Prompt), 1)
	require.Len(t, p.f.wakeMessages(t, secondChat, second.Prompt), 1)
}

// A read that hangs does not hold up the delivery of another chat's
// wake-up, even with one delivery slot.
func TestPullRequestPollerHangDoesNotDelayDelivery(t *testing.T) {
	t.Parallel()
	p := newPollerTest(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	watching := p.f.createWakeupChat(t)
	p.f.insertWakeup(t, watching, forkdb.InsertChatWakeupParams{
		Kind:      forkdb.ChatWakeupKindEvents,
		ExpiresAt: sql.NullTime{Time: wakeupTestSlot.Add(24 * time.Hour), Valid: true},
	}, wakeupEvent{
		Type:        wakeupEventPullRequest,
		URL:         p.github.url,
		On:          []string{pullRequestEventComment},
		Since:       wakeupTestSlot.Add(-time.Hour),
		HeadSHA:     testHeadSHA,
		ViewerLogin: "owner",
	})
	other := p.f.createWakeupChat(t)
	interval := p.f.insertWakeup(t, other, forkdb.InsertChatWakeupParams{
		Kind:            forkdb.ChatWakeupKindInterval,
		IntervalSeconds: sql.NullInt32{Int32: 60, Valid: true},
	})

	release := make(chan struct{})
	p.github.mu.Lock()
	p.github.hang = release
	p.github.mu.Unlock()
	polled := make(chan struct{})
	p.f.startWaker(t, func(h *wakerHarness) {
		h.waker.slots = make(chan struct{}, 1)
		h.server.forkPullRequestActivity = p.github.fetch
		go func() {
			defer close(polled)
			h.server.pollPullRequests(p.ctx, p.fdb, wakeupTestSlot.Add(time.Second))
		}()
		testutil.RequireReceive(ctx, t, p.github.hung)
	})
	testutil.Eventually(ctx, t, func(context.Context) bool {
		return p.f.countPromptMessages(t, other, interval.Prompt) == 1
	}, testutil.IntervalFast, "the interval wake-up must be delivered while the read hangs")
	select {
	case <-polled:
		t.Fatal("the poll must still hang")
	default:
	}
	close(release)
	testutil.TryReceive(ctx, t, polled)
}
