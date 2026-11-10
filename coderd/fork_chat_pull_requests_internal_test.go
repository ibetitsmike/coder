package coderd

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
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
		}},
	}}
	linked := dbgen.User(t, db, database.User{})
	dbgen.ExternalAuthLink(t, db, database.ExternalAuthLink{ProviderID: "github", UserID: linked.ID, OAuthAccessToken: "linked-token"})
	unlinked := dbgen.User(t, db, database.User{})
	pullRequestURL := github.URL + "/org/repo/pull/7"
	since := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	ctx := testutil.Context(t, testutil.WaitShort)

	activity, err := api.forkChatPullRequestActivity(ctx, linked.ID, pullRequestURL, since)
	require.NoError(t, err)
	require.Equal(t, gitprovider.PRStateOpen, activity.State)
	require.Equal(t, "abc", activity.HeadSHA)
	require.Len(t, activity.Reviews, 1)
	require.Equal(t, "alice", activity.Reviews[0].Author)

	_, err = api.forkChatPullRequestActivity(ctx, unlinked.ID, pullRequestURL, since)
	require.ErrorContains(t, err, "the chat owner has no linked account for "+host)
	_, err = api.forkChatPullRequestActivity(ctx, linked.ID, github.URL+"/org/repo/issues/7", since)
	require.ErrorContains(t, err, "is not a pull request URL")
	other := "https://gitlab.example.com/org/repo/pull/7"
	_, err = api.forkChatPullRequestActivity(ctx, linked.ID, other, since)
	require.ErrorContains(t, err, "no git provider is configured for "+other)
}
