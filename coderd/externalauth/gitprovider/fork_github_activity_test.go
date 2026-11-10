package gitprovider_test

// This file is fork code: it tests FetchPullRequestActivity of
// fork_github_activity.go.

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/externalauth/gitprovider"
	"github.com/coder/coder/v2/testutil"
)

// fakeGitHubActivity serves JSON bodies by request path under /api/v3 and
// records the query of every request.
type fakeGitHubActivity struct {
	mu      sync.Mutex
	queries map[string]string
}

func newFakeGitHubActivity(t *testing.T, routes map[string]http.HandlerFunc) (gitprovider.Provider, *fakeGitHubActivity) {
	t.Helper()
	fake := &fakeGitHubActivity{queries: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v3")
		fake.mu.Lock()
		fake.queries[path] = r.URL.RawQuery
		fake.mu.Unlock()
		handler, ok := routes[path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	gp, err := gitprovider.New("github", srv.URL+"/api/v3", srv.Client())
	require.NoError(t, err)
	return gp, fake
}

func (f *fakeGitHubActivity) query(path string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.queries[path]
}

func jsonBody(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

// activityRoutes returns the routes of an open pull request 7 in org/repo
// with head abc123 and no reviews, comments, check runs or statuses.
func activityRoutes() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"/user":                                     jsonBody(`{"login":"agent"}`),
		"/repos/org/repo/pulls/7":                   jsonBody(`{"state":"open","merged":false,"head":{"sha":"abc123"}}`),
		"/repos/org/repo/pulls/7/reviews":           jsonBody(`[]`),
		"/repos/org/repo/issues/7/comments":         jsonBody(`[]`),
		"/repos/org/repo/pulls/7/comments":          jsonBody(`[]`),
		"/repos/org/repo/commits/abc123/check-runs": jsonBody(`{"total_count":0,"check_runs":[]}`),
		"/repos/org/repo/commits/abc123/status":     jsonBody(`{"state":"pending","total_count":0}`),
	}
}

func TestFetchPullRequestActivity(t *testing.T) {
	t.Parallel()

	ref := gitprovider.PRRef{Owner: "org", Repo: "repo", Number: 7}
	// 06:00 UTC, given in another zone to check the since parameter is
	// sent in UTC.
	since := time.Date(2026, 11, 10, 7, 0, 0, 0, time.FixedZone("CET", 3600))
	at := func(clock string) time.Time {
		parsed, err := time.Parse(time.RFC3339, "2026-11-10T"+clock+"Z")
		require.NoError(t, err)
		return parsed
	}

	t.Run("Activity", func(t *testing.T) {
		t.Parallel()

		routes := activityRoutes()
		// GitHub lists reviews in creation order, so review 4, a draft
		// submitted after review 5, comes first. Review 5 and comment 20
		// are exactly at since.
		routes["/repos/org/repo/pulls/7/reviews"] = jsonBody(`[
			{"id":1,"state":"COMMENTED","html_url":"https://github.com/org/repo/pull/7#r1","submitted_at":"2026-11-10T05:00:00Z","user":{"login":"codex[bot]"}},
			{"id":2,"state":"PENDING","html_url":"https://github.com/org/repo/pull/7#r2","submitted_at":"2026-11-10T06:10:00Z","user":{"login":"alice"}},
			{"id":3,"state":"APPROVED","html_url":"https://github.com/org/repo/pull/7#r3","submitted_at":"2026-11-10T06:20:00Z","user":{"login":"agent"}},
			{"id":4,"state":"APPROVED","html_url":"https://github.com/org/repo/pull/7#r4","submitted_at":"2026-11-10T06:45:00Z","user":{"login":"dave"}},
			{"id":5,"state":"CHANGES_REQUESTED","html_url":"https://github.com/org/repo/pull/7#r5","submitted_at":"2026-11-10T06:00:00Z","user":{"login":"codex[bot]"}}
		]`)
		routes["/repos/org/repo/issues/7/comments"] = jsonBody(`[
			{"id":10,"html_url":"https://github.com/org/repo/pull/7#c10","created_at":"2026-11-10T05:00:00Z","updated_at":"2026-11-10T06:10:00Z","user":{"login":"bob"}},
			{"id":11,"html_url":"https://github.com/org/repo/pull/7#c11","created_at":"2026-11-10T06:20:00Z","updated_at":"2026-11-10T06:20:00Z","user":{"login":"agent"}},
			{"id":12,"html_url":"https://github.com/org/repo/pull/7#c12","created_at":"2026-11-10T06:40:00Z","updated_at":"2026-11-10T06:40:00Z","user":{"login":"bob"}}
		]`)
		routes["/repos/org/repo/pulls/7/comments"] = jsonBody(`[
			{"id":20,"html_url":"https://github.com/org/repo/pull/7#d20","created_at":"2026-11-10T06:00:00Z","updated_at":"2026-11-10T06:00:00Z","user":{"login":"codex[bot]"}}
		]`)
		routes["/repos/org/repo/commits/abc123/check-runs"] = jsonBody(`{"total_count":1,"check_runs":[{"status":"completed","conclusion":"success"}]}`)
		gp, fake := newFakeGitHubActivity(t, routes)

		ctx := testutil.Context(t, testutil.WaitShort)
		activity, err := gitprovider.FetchPullRequestActivity(ctx, gp, "token", ref, since, true)
		require.NoError(t, err)
		assert.Equal(t, &gitprovider.PullRequestActivity{
			State:   gitprovider.PRStateOpen,
			HeadSHA: "abc123",
			Reviews: []gitprovider.PullRequestEvent{{
				ID:        5,
				Author:    "codex[bot]",
				State:     "changes_requested",
				URL:       "https://github.com/org/repo/pull/7#r5",
				CreatedAt: at("06:00:00"),
			}, {
				ID:        4,
				Author:    "dave",
				State:     "approved",
				URL:       "https://github.com/org/repo/pull/7#r4",
				CreatedAt: at("06:45:00"),
			}},
			Comments: []gitprovider.PullRequestEvent{{
				ID:        20,
				Author:    "codex[bot]",
				URL:       "https://github.com/org/repo/pull/7#d20",
				CreatedAt: at("06:00:00"),
			}, {
				ID:        12,
				Author:    "bob",
				URL:       "https://github.com/org/repo/pull/7#c12",
				CreatedAt: at("06:40:00"),
			}},
			Checks: gitprovider.CheckSummary{State: gitprovider.CheckStateSuccess, Total: 1},
		}, activity)
		for _, path := range []string{"/repos/org/repo/issues/7/comments", "/repos/org/repo/pulls/7/comments"} {
			query, err := url.ParseQuery(fake.query(path))
			require.NoError(t, err)
			assert.Equal(t, "2026-11-10T05:59:59Z", query.Get("since"), path)
		}
	})

	t.Run("FractionalSince", func(t *testing.T) {
		t.Parallel()

		routes := activityRoutes()
		routes["/repos/org/repo/pulls/7/reviews"] = jsonBody(`[
			{"id":1,"state":"APPROVED","html_url":"https://github.com/org/repo/pull/7#r1","submitted_at":"2026-11-10T06:00:00Z","user":{"login":"alice"}}
		]`)
		routes["/repos/org/repo/issues/7/comments"] = jsonBody(`[
			{"id":10,"html_url":"https://github.com/org/repo/pull/7#c10","created_at":"2026-11-10T06:00:00Z","user":{"login":"bob"}}
		]`)
		gp, fake := newFakeGitHubActivity(t, routes)

		ctx := testutil.Context(t, testutil.WaitShort)
		activity, err := gitprovider.FetchPullRequestActivity(ctx, gp, "token", ref, at("06:00:00.5"), true)
		require.NoError(t, err)
		require.Len(t, activity.Reviews, 1)
		assert.EqualValues(t, 1, activity.Reviews[0].ID)
		require.Len(t, activity.Comments, 1)
		assert.EqualValues(t, 10, activity.Comments[0].ID)
		query, err := url.ParseQuery(fake.query("/repos/org/repo/issues/7/comments"))
		require.NoError(t, err)
		assert.Equal(t, "2026-11-10T05:59:59Z", query.Get("since"))
	})

	t.Run("Paginated", func(t *testing.T) {
		t.Parallel()

		ownComments := make([]string, 100)
		for i := range ownComments {
			ownComments[i] = fmt.Sprintf(`{"id":%d,"created_at":"2026-11-10T06:10:00Z","user":{"login":"agent"}}`, 100+i)
		}
		routes := activityRoutes()
		routes["/repos/org/repo/issues/7/comments"] = func(w http.ResponseWriter, r *http.Request) {
			body := "[" + strings.Join(ownComments, ",") + "]"
			if r.URL.Query().Get("page") == "2" {
				body = `[{"id":300,"created_at":"2026-11-10T06:20:00Z","user":{"login":"bob"}}]`
			}
			jsonBody(body)(w, r)
		}
		gp, _ := newFakeGitHubActivity(t, routes)

		ctx := testutil.Context(t, testutil.WaitShort)
		activity, err := gitprovider.FetchPullRequestActivity(ctx, gp, "token", ref, since, true)
		require.NoError(t, err)
		require.Len(t, activity.Comments, 1)
		assert.EqualValues(t, 300, activity.Comments[0].ID)
	})

	t.Run("Checks", func(t *testing.T) {
		t.Parallel()

		for _, tc := range []struct {
			name      string
			checkRuns string
			status    string
			want      gitprovider.CheckSummary
		}{{
			name:      "AllSucceeded",
			checkRuns: `[{"id":1,"name":"c1","status":"completed","conclusion":"success"},{"id":2,"name":"c2","status":"completed","conclusion":"success"}]`,
			status:    `{"state":"pending","total_count":0}`,
			want:      gitprovider.CheckSummary{State: gitprovider.CheckStateSuccess, Total: 2},
		}, {
			name:      "OneInProgress",
			checkRuns: `[{"id":3,"name":"c3","status":"completed","conclusion":"success"},{"id":4,"name":"c4","status":"in_progress","conclusion":null}]`,
			status:    `{"state":"pending","total_count":0}`,
			want:      gitprovider.CheckSummary{State: gitprovider.CheckStatePending, Total: 2},
		}, {
			name:      "OneFailed",
			checkRuns: `[{"id":5,"name":"c5","status":"completed","conclusion":"success"},{"id":6,"name":"c6","status":"completed","conclusion":"failure"},{"id":7,"name":"c7","status":"queued","conclusion":null}]`,
			status:    `{"state":"pending","total_count":0}`,
			want:      gitprovider.CheckSummary{State: gitprovider.CheckStateFailure, Total: 3, Failed: 1},
		}, {
			name:      "NoChecks",
			checkRuns: `[]`,
			status:    `{"state":"pending","total_count":0}`,
			want:      gitprovider.CheckSummary{State: gitprovider.CheckStateNone},
		}, {
			name:      "CommitStatusFailed",
			checkRuns: `[]`,
			status:    `{"state":"failure","total_count":3,"statuses":[{"state":"failure","updated_at":"2026-11-10T06:10:00Z"},{"state":"error","updated_at":"2026-11-10T06:30:00Z"},{"state":"success","updated_at":"2026-11-10T06:20:00Z"}]}`,
			want:      gitprovider.CheckSummary{State: gitprovider.CheckStateFailure, Total: 3, Failed: 2, UpdatedAt: at("06:30:00")},
		}, {
			name:      "CommitStatusPending",
			checkRuns: `[{"id":8,"name":"c8","status":"completed","conclusion":"success"}]`,
			status:    `{"state":"pending","total_count":1}`,
			want:      gitprovider.CheckSummary{State: gitprovider.CheckStatePending, Total: 2},
		}, {
			name:      "RerunAfterCancel",
			checkRuns: `[{"id":10,"name":"test","app":{"id":1},"status":"completed","conclusion":"cancelled","completed_at":"2026-11-10T06:30:00Z"},{"id":11,"name":"test","app":{"id":1},"status":"completed","conclusion":"success","completed_at":"2026-11-10T06:20:00Z"},{"id":12,"name":"test","app":{"id":2},"status":"completed","conclusion":"success","completed_at":"2026-11-10T06:10:00Z"}]`,
			status:    `{"state":"pending","total_count":0}`,
			want:      gitprovider.CheckSummary{State: gitprovider.CheckStateSuccess, Total: 2, UpdatedAt: at("06:20:00")},
		}, {
			// Two workflows with a job of the same name are two suites.
			name:      "SameNameInTwoSuites",
			checkRuns: `[{"id":40,"name":"test","app":{"id":1},"check_suite":{"id":7},"status":"in_progress","conclusion":null},{"id":41,"name":"test","app":{"id":1},"check_suite":{"id":8},"status":"completed","conclusion":"success"}]`,
			status:    `{"state":"pending","total_count":0}`,
			want:      gitprovider.CheckSummary{State: gitprovider.CheckStatePending, Total: 2},
		}, {
			name:      "RerunInProgress",
			checkRuns: `[{"id":21,"name":"test","status":"in_progress","conclusion":null},{"id":20,"name":"test","status":"completed","conclusion":"failure"}]`,
			status:    `{"state":"pending","total_count":0}`,
			want:      gitprovider.CheckSummary{State: gitprovider.CheckStatePending, Total: 1},
		}, {
			name:      "Stale",
			checkRuns: `[{"id":30,"name":"c30","status":"completed","conclusion":"stale"}]`,
			status:    `{"state":"pending","total_count":0}`,
			want:      gitprovider.CheckSummary{State: gitprovider.CheckStateFailure, Total: 1, Failed: 1},
		}, {
			name:      "NeutralAndSkipped",
			checkRuns: `[{"id":9,"name":"c9","status":"completed","conclusion":"neutral"},{"id":10,"name":"c10","status":"completed","conclusion":"skipped"}]`,
			status:    `{"state":"pending","total_count":0}`,
			want:      gitprovider.CheckSummary{State: gitprovider.CheckStateSuccess, Total: 2},
		}} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				routes := activityRoutes()
				routes["/repos/org/repo/commits/abc123/check-runs"] = jsonBody(`{"check_runs":` + tc.checkRuns + `}`)
				routes["/repos/org/repo/commits/abc123/status"] = jsonBody(tc.status)
				gp, _ := newFakeGitHubActivity(t, routes)

				ctx := testutil.Context(t, testutil.WaitShort)
				activity, err := gitprovider.FetchPullRequestActivity(ctx, gp, "token", ref, since, true)
				require.NoError(t, err)
				assert.Equal(t, tc.want, activity.Checks)
			})
		}
	})

	t.Run("ChecksPaginated", func(t *testing.T) {
		t.Parallel()

		// successPage returns 100 successful check runs with IDs from first.
		successPage := func(first int) string {
			runs := make([]string, 100)
			for i := range runs {
				runs[i] = fmt.Sprintf(`{"id":%d,"name":"c%d","status":"completed","conclusion":"success"}`, first+i, first+i)
			}
			return strings.Join(runs, ",")
		}
		for _, tc := range []struct {
			name       string
			totalCount int
			page2      string
			want       gitprovider.CheckSummary
		}{{
			// A failed run on the second page counts.
			name:       "FailureOnSecondPage",
			totalCount: 101,
			page2:      `[{"id":1000,"name":"late","status":"completed","conclusion":"failure"}]`,
			want:       gitprovider.CheckSummary{State: gitprovider.CheckStateFailure, Total: 101, Failed: 1},
		}, {
			// Runs past the page cap keep CI pending.
			name:       "PastPageCap",
			totalCount: 1001,
			want:       gitprovider.CheckSummary{State: gitprovider.CheckStatePending, Total: 1000},
		}} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				routes := activityRoutes()
				routes["/repos/org/repo/commits/abc123/check-runs"] = func(w http.ResponseWriter, r *http.Request) {
					page, _ := strconv.Atoi(r.URL.Query().Get("page"))
					runs := "[" + successPage(page*100) + "]"
					if page == 2 && tc.page2 != "" {
						runs = tc.page2
					}
					jsonBody(fmt.Sprintf(`{"total_count":%d,"check_runs":%s}`, tc.totalCount, runs))(w, r)
				}
				gp, _ := newFakeGitHubActivity(t, routes)

				ctx := testutil.Context(t, testutil.WaitShort)
				activity, err := gitprovider.FetchPullRequestActivity(ctx, gp, "token", ref, since, true)
				require.NoError(t, err)
				assert.Equal(t, tc.want, activity.Checks)
			})
		}
	})

	t.Run("Merged", func(t *testing.T) {
		t.Parallel()

		routes := activityRoutes()
		routes["/repos/org/repo/pulls/7"] = jsonBody(`{"state":"closed","merged":true,"closed_at":"2026-11-10T06:50:00Z","head":{"sha":"abc123"}}`)
		gp, _ := newFakeGitHubActivity(t, routes)

		ctx := testutil.Context(t, testutil.WaitShort)
		activity, err := gitprovider.FetchPullRequestActivity(ctx, gp, "token", ref, since, true)
		require.NoError(t, err)
		assert.Equal(t, gitprovider.PRStateMerged, activity.State)
		assert.Equal(t, at("06:50:00"), activity.ClosedAt)
	})

	t.Run("ChecksNotRequested", func(t *testing.T) {
		t.Parallel()

		// A GitHub App without the Checks permission gets 403 for CI.
		forbidden := func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message": "Resource not accessible by integration"}`))
		}
		routes := activityRoutes()
		routes["/repos/org/repo/commits/abc123/check-runs"] = forbidden
		routes["/repos/org/repo/commits/abc123/status"] = forbidden
		gp, _ := newFakeGitHubActivity(t, routes)

		ctx := testutil.Context(t, testutil.WaitShort)
		activity, err := gitprovider.FetchPullRequestActivity(ctx, gp, "token", ref, since, false)
		require.NoError(t, err)
		assert.Equal(t, gitprovider.CheckSummary{State: gitprovider.CheckStateNone}, activity.Checks)
		_, err = gitprovider.FetchPullRequestActivity(ctx, gp, "token", ref, since, true)
		require.Error(t, err)
	})

	t.Run("RateLimited", func(t *testing.T) {
		t.Parallel()

		resetTime := time.Now().Add(60 * time.Second)
		routes := activityRoutes()
		routes["/repos/org/repo/pulls/7/reviews"] = func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-Ratelimit-Remaining", "0")
			w.Header().Set("X-Ratelimit-Reset", fmt.Sprintf("%d", resetTime.Unix()))
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message": "API rate limit exceeded"}`))
		}
		gp, _ := newFakeGitHubActivity(t, routes)

		ctx := testutil.Context(t, testutil.WaitShort)
		_, err := gitprovider.FetchPullRequestActivity(ctx, gp, "token", ref, since, true)
		var rlErr *gitprovider.RateLimitError
		require.True(t, errors.As(err, &rlErr), "error should wrap *RateLimitError, got: %v", err)
		assert.WithinDuration(t, resetTime.Add(gitprovider.RateLimitPadding), rlErr.RetryAfter, 2*time.Second)
	})

	t.Run("Unsupported", func(t *testing.T) {
		t.Parallel()

		gp, err := gitprovider.New("gitlab", "https://gitlab.example.com", nil)
		require.NoError(t, err)

		ctx := testutil.Context(t, testutil.WaitShort)
		_, err = gitprovider.FetchPullRequestActivity(ctx, gp, "token", ref, since, true)
		require.ErrorIs(t, err, gitprovider.ErrPullRequestActivityUnsupported)
	})
}
