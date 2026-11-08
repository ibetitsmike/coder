package coderd_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/x/plugins"
	"github.com/coder/coder/v2/coderd/x/plugins/plugintest"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/enterprise/coderd/coderdenttest"
	"github.com/coder/coder/v2/enterprise/coderd/license"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/serpent"
)

// TestPluginWebhookRelay checks that a replica that does not run a plugin
// forwards the plugin's webhooks and connect requests to the replica that
// does.
func TestPluginWebhookRelay(t *testing.T) {
	t.Parallel()
	wasm := plugintest.BuildWasm(t, "github.com/coder/coder/v2/coderd/x/plugins/testdata/bridge")

	db, ps := dbtestutil.NewDB(t)
	replicaOptions := func() *coderdtest.Options {
		values := coderdtest.DeploymentValues(t)
		values.Experiments = serpent.StringArray{string(codersdk.ExperimentPlugins)}
		return &coderdtest.Options{Database: db, Pubsub: ps, DeploymentValues: values}
	}
	firstClient, _, firstAPI, owner := coderdenttest.NewWithAPI(t, &coderdenttest.Options{
		Options: replicaOptions(),
		LicenseOptions: &coderdenttest.LicenseOptions{
			Features: license.Features{codersdk.FeatureHighAvailability: 1},
		},
	})
	secondClient, _, secondAPI, _ := coderdenttest.NewWithAPI(t, &coderdenttest.Options{
		Options:          replicaOptions(),
		DontAddLicense:   true,
		DontAddFirstUser: true,
	})

	ctx := testutil.Context(t, testutil.WaitSuperLong)
	client := codersdk.NewExperimentalClient(firstClient)
	inspected, err := client.InspectPlugin(ctx, owner.OrganizationID, wasm)
	require.NoError(t, err)
	plugin, err := client.CreatePlugin(ctx, owner.OrganizationID, codersdk.CreatePluginRequest{
		Wasm:   wasm,
		Grants: codersdk.GrantsForManifest(inspected.Manifest, nil),
	})
	require.NoError(t, err)

	var holder uuid.UUID
	testutil.Eventually(ctx, t, func(ctx context.Context) bool {
		p, err := client.Plugin(ctx, plugin.ID)
		require.NoError(t, err)
		if p.RunnerID == nil {
			return false
		}
		holder = *p.RunnerID
		return true
	}, testutil.IntervalFast)
	other := firstClient.URL
	if holder == firstAPI.AGPL.ID {
		other = secondClient.URL
	} else {
		require.Equal(t, secondAPI.AGPL.ID, holder)
	}
	webhookURL := other.JoinPath("api", "experimental", "plugins", plugin.ID.String(), "webhook")
	webhookURL.RawQuery = "update=1"

	// post sends one bridge operation to the replica that does not run the
	// plugin. It retries while the plugin is starting.
	post := func(t *testing.T, key string, header http.Header) (int, http.Header, bridgeResponse) {
		t.Helper()
		body, err := json.Marshal(map[string]string{"op": "request", "key": key})
		require.NoError(t, err)
		var (
			status int
			resH   http.Header
			out    bridgeResponse
		)
		testutil.Eventually(ctx, t, func(ctx context.Context) bool {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL.String(), bytes.NewReader(body))
			require.NoError(t, err)
			for name, values := range header {
				req.Header[name] = values
			}
			res, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer res.Body.Close()
			status, resH = res.StatusCode, res.Header
			if status != http.StatusOK {
				// Only a relayed request may give up with 503.
				return status == http.StatusServiceUnavailable && header.Get(plugins.RelaySourceHeader) != ""
			}
			require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
			return true
		}, testutil.IntervalFast)
		return status, resH, out
	}

	t.Run("Forwarded", func(t *testing.T) {
		t.Parallel()
		status, _, res := post(t, "X-Telegram-Bot-Api-Secret-Token", http.Header{
			"X-Telegram-Bot-Api-Secret-Token": {"s3cret"},
		})
		require.Equal(t, http.StatusOK, status)
		require.Empty(t, res.Error)
		require.Equal(t, "/api/experimental/plugins/"+plugin.ID.String()+"/webhook?update=1", res.Value)
		require.Equal(t, "s3cret", res.Body)
	})

	t.Run("PluginDoesNotSeeRelayHeader", func(t *testing.T) {
		t.Parallel()
		status, _, res := post(t, plugins.RelaySourceHeader, nil)
		require.Equal(t, http.StatusOK, status)
		require.Empty(t, res.Body)
	})

	t.Run("ConnectForwarded", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitLong)
		otherClient := codersdk.NewExperimentalClient(codersdk.New(other, codersdk.WithSessionToken(firstClient.SessionToken())))
		var prompt codersdk.PluginConnectPrompt
		testutil.Eventually(ctx, t, func(ctx context.Context) bool {
			var err error
			prompt, err = otherClient.ConnectPlugin(ctx, plugin.ID)
			var sdkErr *codersdk.Error
			if errors.As(err, &sdkErr) && sdkErr.StatusCode() == http.StatusServiceUnavailable {
				return false
			}
			require.NoError(t, err)
			return true
		}, testutil.IntervalFast)
		require.Equal(t, "\x89PNG\r\n\x1a\nbridge", string(prompt.QRCodePNG))
		require.NotEmpty(t, prompt.Instructions)
	})

	t.Run("RelayedRequestIsNotForwardedAgain", func(t *testing.T) {
		t.Parallel()
		status, header, _ := post(t, "", http.Header{plugins.RelaySourceHeader: {uuid.NewString()}})
		require.Equal(t, http.StatusServiceUnavailable, status)
		require.Equal(t, "5", header.Get("Retry-After"))
	})
}

// bridgeResponse matches the JSON of the bridge test plugin.
type bridgeResponse struct {
	Error string `json:"error"`
	Value string `json:"value"`
	Body  string `json:"body"`
}
