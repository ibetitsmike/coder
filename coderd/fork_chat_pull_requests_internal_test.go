package coderd

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/singleflight"

	"cdr.dev/slog/v3/sloggers/slogtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/externalauth"
	"github.com/coder/coder/v2/coderd/externalauth/gitprovider"
	"github.com/coder/coder/v2/testutil"
)

// forkChatPullRequestActivity reads the pull request with the token the chat
// owner linked for the provider whose regex matches the URL.
func TestForkChatPullRequestActivity(t *testing.T) {
	t.Parallel()
	db, _ := dbtestutil.NewDB(t)
	// The provider only parses https pull request URLs.
	github := httptest.NewTLSServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer linked-token" {
			rw.WriteHeader(http.StatusUnauthorized)
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		switch path := r.URL.Path; {
		case path == "/api/v3/user":
			_, _ = rw.Write([]byte(`{"login":"owner"}`))
		case path == "/api/v3/repos/org/repo/pulls/7":
			_, _ = rw.Write([]byte(`{"state":"open","merged":false,"head":{"sha":"abc"}}`))
		case path == "/api/v3/repos/org/repo/pulls/7/reviews":
			_, _ = rw.Write([]byte(`[{"id":1,"state":"APPROVED","html_url":"https://example.com/r1","submitted_at":"2030-01-01T00:00:00Z","user":{"login":"alice"}}]`))
		case strings.HasSuffix(path, "/check-runs"), strings.HasSuffix(path, "/status"):
			_, _ = rw.Write([]byte(`{}`))
		default:
			_, _ = rw.Write([]byte(`[]`))
		}
	}))
	t.Cleanup(github.Close)
	host := strings.TrimPrefix(github.URL, "https://")
	api := &API{Options: &Options{
		Database: db,
		Logger:   slogtest.Make(t, nil),
		ExternalAuthConfigs: []*externalauth.Config{{
			ID:           "github",
			Type:         "github",
			Regex:        regexp.MustCompile(regexp.QuoteMeta(host)),
			APIBaseURL:   github.URL + "/api/v3",
			HTTPClient:   github.Client(),
			RefreshGroup: new(singleflight.Group),
		}, {
			ID:           "other",
			Type:         "github",
			Regex:        regexp.MustCompile(`other\.example\.com`),
			RefreshGroup: new(singleflight.Group),
		}},
	}}
	linked := dbgen.User(t, db, database.User{})
	dbgen.ExternalAuthLink(t, db, database.ExternalAuthLink{ProviderID: "github", UserID: linked.ID, OAuthAccessToken: "linked-token"})
	unlinked := dbgen.User(t, db, database.User{})
	// A token for another provider must never reach this provider's host.
	linkedElsewhere := dbgen.User(t, db, database.User{})
	dbgen.ExternalAuthLink(t, db, database.ExternalAuthLink{ProviderID: "other", UserID: linkedElsewhere.ID, OAuthAccessToken: "linked-token"})
	pullRequestURL := github.URL + "/org/repo/pull/7"
	since := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	ctx := testutil.Context(t, testutil.WaitShort)

	activity, err := api.forkChatPullRequestActivity(ctx, linked.ID, pullRequestURL, since, true, "")
	require.NoError(t, err)
	require.Equal(t, gitprovider.PRStateOpen, activity.State)
	require.Equal(t, "abc", activity.HeadSHA)
	require.Len(t, activity.Reviews, 1)
	require.Equal(t, "alice", activity.Reviews[0].Author)

	for _, owner := range []database.User{unlinked, linkedElsewhere} {
		_, err = api.forkChatPullRequestActivity(ctx, owner.ID, pullRequestURL, since, true, "")
		require.ErrorContains(t, err, "the chat owner has no linked account for "+host)
	}
	_, err = api.forkChatPullRequestActivity(ctx, linked.ID, github.URL+"/org/repo/issues/7", since, true, "")
	require.ErrorContains(t, err, "is not a pull request URL")
	other := "https://gitlab.example.com/org/repo/pull/7"
	_, err = api.forkChatPullRequestActivity(ctx, linked.ID, other, since, true, "")
	require.ErrorContains(t, err, "no git provider is configured for "+other)
}

// The viewer login of a read is reused while the link is unchanged, and read
// again after a relink, so a new account is not taken for the old one.
func TestForkChatPullRequestActivityViewer(t *testing.T) {
	t.Parallel()
	db, _ := dbtestutil.NewDB(t)
	logins := map[string]string{"Bearer first-token": "first", "Bearer second-token": "second"}
	var userReads atomic.Int64
	github := httptest.NewTLSServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		login, ok := logins[r.Header.Get("Authorization")]
		if !ok {
			rw.WriteHeader(http.StatusUnauthorized)
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v3/user":
			userReads.Add(1)
			_, _ = fmt.Fprintf(rw, `{"login":%q}`, login)
		case "/api/v3/repos/org/repo/pulls/7":
			_, _ = rw.Write([]byte(`{"state":"open","merged":false,"head":{"sha":"abc"}}`))
		default:
			_, _ = rw.Write([]byte(`[]`))
		}
	}))
	t.Cleanup(github.Close)
	var validations atomic.Int64
	validate := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		validations.Add(1)
		_, _ = rw.Write([]byte(`{}`))
	}))
	t.Cleanup(validate.Close)
	host := strings.TrimPrefix(github.URL, "https://")
	api := &API{Options: &Options{
		Database: db,
		Logger:   slogtest.Make(t, nil),
		ExternalAuthConfigs: []*externalauth.Config{{
			InstrumentedOAuth2Config: &testutil.OAuth2Config{},
			ID:                       "github",
			Type:                     "github",
			Regex:                    regexp.MustCompile(regexp.QuoteMeta(host)),
			APIBaseURL:               github.URL + "/api/v3",
			ValidateURL:              validate.URL,
			HTTPClient:               github.Client(),
			RefreshGroup:             new(singleflight.Group),
		}},
	}}
	owner := dbgen.User(t, db, database.User{})
	link := dbgen.ExternalAuthLink(t, db, database.ExternalAuthLink{ProviderID: "github", UserID: owner.ID, OAuthAccessToken: "first-token"})
	pullRequestURL := github.URL + "/org/repo/pull/7"
	since := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	ctx := testutil.Context(t, testutil.WaitShort)

	activity, err := api.forkChatPullRequestActivity(ctx, owner.ID, pullRequestURL, since, false, "")
	require.NoError(t, err)
	require.EqualValues(t, 1, userReads.Load())
	first := activity.ViewerLogin
	require.True(t, strings.HasSuffix(first, ":first"), first)

	// An unexpired token is neither validated nor asked for its login again.
	activity, err = api.forkChatPullRequestActivity(ctx, owner.ID, pullRequestURL, since, false, first)
	require.NoError(t, err)
	require.EqualValues(t, 1, userReads.Load())
	require.Equal(t, first, activity.ViewerLogin)
	require.Zero(t, validations.Load())

	_, err = db.UpdateExternalAuthLink(ctx, database.UpdateExternalAuthLinkParams{
		ProviderID:       "github",
		UserID:           owner.ID,
		UpdatedAt:        link.UpdatedAt.Add(time.Minute),
		OAuthAccessToken: "second-token",
		OAuthExpiry:      link.OAuthExpiry,
	})
	require.NoError(t, err)
	activity, err = api.forkChatPullRequestActivity(ctx, owner.ID, pullRequestURL, since, false, first)
	require.NoError(t, err)
	require.EqualValues(t, 2, userReads.Load())
	require.True(t, strings.HasSuffix(activity.ViewerLogin, ":second"), activity.ViewerLogin)
}
