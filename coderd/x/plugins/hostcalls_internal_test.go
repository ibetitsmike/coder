package plugins

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/x/pluginsdk/pluginproto"
	"github.com/coder/coder/v2/testutil"
)

func TestValidateGrants(t *testing.T) {
	t.Parallel()
	manifest := codersdk.PluginManifest{
		Name:        "signal",
		Events:      []string{codersdk.PluginEventChatMessagesAdded},
		Chats:       []string{codersdk.PluginChatSend, codersdk.PluginChatRead},
		HTTPOrigins: []string{"https://signal.example.com"},
		TickSeconds: 5,
		ConfigKeys:  []string{"account"},
	}
	exact := codersdk.GrantsForManifest(manifest, []string{"10.0.0.0/8"})
	require.NoError(t, ValidateGrants(manifest, exact))

	reordered := exact
	reordered.Chats = []string{codersdk.PluginChatRead, codersdk.PluginChatSend}
	require.NoError(t, ValidateGrants(manifest, reordered))

	for name, mutate := range map[string]func(*codersdk.PluginGrants){
		"MissingChat": func(g *codersdk.PluginGrants) { g.Chats = []string{codersdk.PluginChatRead} },
		"ExtraChat":   func(g *codersdk.PluginGrants) { g.Chats = append(g.Chats, codersdk.PluginChatCreate) },
		"ExtraOrigin": func(g *codersdk.PluginGrants) { g.HTTPOrigins = append(g.HTTPOrigins, "https://other.example.com") },
		"ExtraEvent":  func(g *codersdk.PluginGrants) { g.Events = append(g.Events, codersdk.PluginEventChatStatusChanged) },
		"ExtraKey":    func(g *codersdk.PluginGrants) { g.ConfigKeys = append(g.ConfigKeys, "other") },
		"Tick":        func(g *codersdk.PluginGrants) { g.TickSeconds = 2 },
		"Webhook":     func(g *codersdk.PluginGrants) { g.Webhook = true },
		"InvalidCIDR": func(g *codersdk.PluginGrants) { g.PrivateCIDRs = []string{"not-a-cidr"} },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			g := exact
			g.Chats = append([]string(nil), exact.Chats...)
			g.HTTPOrigins = append([]string(nil), exact.HTTPOrigins...)
			g.Events = append([]string(nil), exact.Events...)
			g.ConfigKeys = append([]string(nil), exact.ConfigKeys...)
			mutate(&g)
			require.Error(t, ValidateGrants(manifest, g))
		})
	}
}

// newFetchRunner returns a runner that can only make http_fetch calls.
func newFetchRunner(t *testing.T, origins, privateCIDRs []string) *runner {
	t.Helper()
	grants, err := json.Marshal(codersdk.PluginGrants{HTTPOrigins: origins, PrivateCIDRs: privateCIDRs})
	require.NoError(t, err)
	r := &runner{
		m:      &Manager{opts: Options{HTTPClient: &http.Client{}}},
		plugin: database.Plugin{Manifest: []byte(`{}`), Grants: grants},
	}
	require.NoError(t, r.decode())
	return r
}

func TestHTTPFetch(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/redirect":
			http.Redirect(rw, req, "http://localhost:1/elsewhere", http.StatusFound)
		default:
			require.Empty(t, req.Header.Get("Proxy-Authorization"), "blocked headers must not reach the server")
			_, _ = rw.Write([]byte("hello " + req.Header.Get("X-Test")))
		}
	}))
	t.Cleanup(srv.Close)

	t.Run("GrantedOriginAndPrivateRange", func(t *testing.T) {
		t.Parallel()
		r := newFetchRunner(t, []string{srv.URL}, []string{"127.0.0.1/32"})
		res, err := r.httpFetch(testutil.Context(t, testutil.WaitShort), &pluginproto.HTTPRequest{
			Url: srv.URL + "/ok",
			Headers: []*pluginproto.Header{
				{Name: "X-Test", Value: "plugin"},
				{Name: "Proxy-Authorization", Value: "secret"},
			},
		})
		require.NoError(t, err)
		require.EqualValues(t, http.StatusOK, res.GetStatus())
		require.Equal(t, "hello plugin", string(res.GetBody()))
	})

	t.Run("OriginNotGranted", func(t *testing.T) {
		t.Parallel()
		r := newFetchRunner(t, []string{"https://example.com"}, []string{"127.0.0.1/32"})
		_, err := r.httpFetch(testutil.Context(t, testutil.WaitShort), &pluginproto.HTTPRequest{Url: srv.URL})
		require.ErrorContains(t, err, "is not granted")
	})

	t.Run("PrivateAddressNotGranted", func(t *testing.T) {
		t.Parallel()
		r := newFetchRunner(t, []string{srv.URL}, nil)
		_, err := r.httpFetch(testutil.Context(t, testutil.WaitShort), &pluginproto.HTTPRequest{Url: srv.URL})
		require.Error(t, err)
		require.NotContains(t, err.Error(), "is not granted")
	})

	t.Run("RedirectToOtherOrigin", func(t *testing.T) {
		t.Parallel()
		r := newFetchRunner(t, []string{srv.URL}, []string{"127.0.0.0/8"})
		_, err := r.httpFetch(testutil.Context(t, testutil.WaitShort), &pluginproto.HTTPRequest{Url: srv.URL + "/redirect"})
		require.Error(t, err)
	})

	t.Run("MethodNotAllowed", func(t *testing.T) {
		t.Parallel()
		r := newFetchRunner(t, []string{srv.URL}, []string{"127.0.0.1/32"})
		_, err := r.httpFetch(testutil.Context(t, testutil.WaitShort), &pluginproto.HTTPRequest{Method: "CONNECT", Url: srv.URL})
		require.ErrorContains(t, err, "is not allowed")
	})
}
