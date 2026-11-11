package plugins

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/plugins/pluginhost"
	"github.com/coder/coder/v2/coderd/x/plugins/plugintest"
	"github.com/coder/coder/v2/coderd/x/plugins/sandbox"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
)

const echoPluginPackage = "github.com/coder/coder/v2/coderd/x/plugins/testdata/echo"

// newWebhookRunner returns a runner that serves webhook calls with the echo
// plugin loaded in-process. The echo plugin reflects each request back, so
// tests can assert exactly what the plugin received.
func newWebhookRunner(t *testing.T) *runner {
	t.Helper()
	grants, err := json.Marshal(codersdk.PluginGrants{Webhook: true})
	require.NoError(t, err)
	r := &runner{
		m:      &Manager{opts: Options{HTTPClient: &http.Client{}}},
		plugin: forkdb.Plugin{Manifest: []byte(`{}`), Grants: grants},
		logger: testutil.Logger(t),
	}
	require.NoError(t, r.decode())
	ctx := testutil.Context(t, testutil.WaitSuperLong)
	client, err := pluginhost.StartInProcess(ctx, testutil.Logger(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	_, err = client.Load(ctx, plugintest.BuildWasm(t, echoPluginPackage), sandbox.Limits{CallTimeout: testutil.WaitShort})
	require.NoError(t, err)
	r.client = client
	r.ready = make(chan struct{})
	close(r.ready)
	return r
}

// TestWebhookStripsCoderCredentials verifies the unauthenticated webhook
// route forwards a signed-in caller's request to the plugin without its
// Coder session credentials, while everything else arrives intact.
func TestWebhookStripsCoderCredentials(t *testing.T) {
	t.Parallel()
	r := newWebhookRunner(t)

	req := httptest.NewRequest(http.MethodPost,
		"/api/experimental/plugins/"+uuid.NewString()+"/webhook?"+codersdk.SessionTokenCookie+"=leak-me&keep=1",
		strings.NewReader("webhook body"))
	req.Header.Set("Cookie", codersdk.SessionTokenCookie+"=leak-me; other=stay")
	req.Header.Add("Cookie", "another=also-stay")
	req.Header.Set(codersdk.SessionTokenHeader, "leak-me")
	req.Header.Set("Authorization", "Bearer leak-me")
	req.Header.Set("X-Unrelated", "stay")

	resp, err := r.webhook(testutil.Context(t, testutil.WaitLong), req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got struct {
		Method  string      `json:"method"`
		URL     string      `json:"url"`
		Body    string      `json:"body"`
		Headers http.Header `json:"headers"`
	}
	require.NoError(t, json.Unmarshal(resp.Body, &got))

	// None of the four credential vectors reaches the plugin.
	require.NotContains(t, got.URL, codersdk.SessionTokenCookie+"=leak-me")
	require.NotContains(t, got.URL, "leak-me")
	require.Empty(t, got.Headers.Values(codersdk.SessionTokenHeader))
	require.Empty(t, got.Headers.Values("Authorization"))
	cookies := strings.Join(got.Headers.Values("Cookie"), "; ")
	require.NotContains(t, cookies, codersdk.SessionTokenCookie)
	require.NotContains(t, cookies, "leak-me")

	// Everything else passes through unchanged.
	require.Equal(t, http.MethodPost, got.Method)
	require.Equal(t, "webhook body", got.Body)
	require.Equal(t, "stay", got.Headers.Get("X-Unrelated"))
	require.Contains(t, got.URL, "keep=1")
	require.Contains(t, cookies, "other=stay")
	require.Contains(t, cookies, "another=also-stay")

	// The incoming request is not mutated: the route handler and the
	// replica forwarding still read it.
	require.Equal(t, "leak-me", req.Header.Get(codersdk.SessionTokenHeader))
	require.Equal(t, "Bearer leak-me", req.Header.Get("Authorization"))
	require.Contains(t, req.Header.Get("Cookie"), codersdk.SessionTokenCookie+"=leak-me")
	require.Contains(t, req.URL.RawQuery, codersdk.SessionTokenCookie+"=leak-me")
}
