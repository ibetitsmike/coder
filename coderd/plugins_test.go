package coderd_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/aibridgedtest"
	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/plugins"
	"github.com/coder/coder/v2/coderd/x/plugins/plugintest"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/x/pluginsdk"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/serpent"
)

const bridgePluginPackage = "github.com/coder/coder/v2/coderd/x/plugins/testdata/bridge"

// bridgeRequest and bridgeResponse match the JSON of the bridge test plugin.
type bridgeRequest struct {
	Op         string `json:"op"`
	ExternalID string `json:"external_id,omitempty"`
	Text       string `json:"text,omitempty"`
	Code       string `json:"code,omitempty"`
	NewChat    bool   `json:"new_chat,omitempty"`
	URL        string `json:"url,omitempty"`
	Key        string `json:"key,omitempty"`
}

type bridgeResponse struct {
	Error  string                `json:"error"`
	Value  string                `json:"value"`
	Status int                   `json:"status"`
	Body   string                `json:"body"`
	Events []pluginsdk.ChatEvent `json:"events"`
	Ticks  int                   `json:"ticks"`
}

// postWebhook sends one operation to the bridge plugin. It retries while
// the plugin is not yet running on the replica.
func postWebhook(t *testing.T, client *codersdk.Client, pluginID uuid.UUID, req bridgeRequest) bridgeResponse {
	t.Helper()
	body, err := json.Marshal(req)
	require.NoError(t, err)
	url := client.URL.JoinPath("api", "experimental", "plugins", pluginID.String(), "webhook").String()

	var out bridgeResponse
	ctx := testutil.Context(t, testutil.WaitLong)
	testutil.Eventually(ctx, t, func(ctx context.Context) bool {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		require.NoError(t, err)
		res, err := http.DefaultClient.Do(httpReq)
		require.NoError(t, err)
		defer res.Body.Close()
		if res.StatusCode == http.StatusServiceUnavailable {
			return false
		}
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
		return true
	}, testutil.IntervalFast)
	return out
}

func TestPlugins(t *testing.T) {
	t.Parallel()
	wasm := plugintest.BuildWasm(t, bridgePluginPackage)

	values := coderdtest.DeploymentValues(t)
	values.Experiments = serpent.StringArray{string(codersdk.ExperimentPlugins)}
	ownerClient, _, api := coderdtest.NewWithAPI(t, &coderdtest.Options{DeploymentValues: values})
	aibridgedtest.StartTestAIBridgeDaemon(t.Context(), t, api, nil)
	owner := coderdtest.CreateFirstUser(t, ownerClient)
	client := codersdk.NewExperimentalClient(ownerClient)
	memberRaw, member := coderdtest.CreateAnotherUser(t, ownerClient, owner.OrganizationID)
	memberClient := codersdk.NewExperimentalClient(memberRaw)

	ctx := testutil.Context(t, testutil.WaitSuperLong)
	aiProvider := createAIProviderForTest(t, client, "openai-compat", "test-api-key")
	contextLimit := int64(4096)
	_, err := client.CreateChatModel(ctx, owner.OrganizationID, codersdk.CreateChatModelRequest{
		AIProviderID: &aiProvider.ID,
		Model:        "gpt-4o-mini",
		ContextLimit: &contextLimit,
	})
	require.NoError(t, err)

	inspected, err := client.InspectPlugin(ctx, owner.OrganizationID, wasm)
	require.NoError(t, err)
	require.Equal(t, "bridge", inspected.Manifest.Name)
	require.Equal(t, len(wasm), inspected.SizeBytes)
	grants := codersdk.GrantsForManifest(inspected.Manifest, nil)

	t.Run("GrantsMustMatchManifest", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitLong)
		wrong := grants
		wrong.Chats = []string{codersdk.PluginChatRead}
		_, err := client.CreatePlugin(ctx, owner.OrganizationID, codersdk.CreatePluginRequest{Wasm: wasm, Grants: wrong})
		var sdkErr *codersdk.Error
		require.ErrorAs(t, err, &sdkErr)
		require.Equal(t, http.StatusBadRequest, sdkErr.StatusCode())
	})

	t.Run("MemberCannotInstall", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitLong)
		_, err := memberClient.CreatePlugin(ctx, owner.OrganizationID, codersdk.CreatePluginRequest{Wasm: wasm, Grants: grants})
		var sdkErr *codersdk.Error
		require.ErrorAs(t, err, &sdkErr)
		require.Equal(t, http.StatusForbidden, sdkErr.StatusCode())
		_, err = memberClient.Plugins(ctx, owner.OrganizationID)
		require.ErrorAs(t, err, &sdkErr)
		require.Equal(t, http.StatusForbidden, sdkErr.StatusCode())
	})

	plugin, err := client.CreatePlugin(ctx, owner.OrganizationID, codersdk.CreatePluginRequest{
		Wasm:   wasm,
		Grants: grants,
		Config: map[string]string{"token": "secret-value"},
	})
	require.NoError(t, err)
	require.True(t, plugin.Enabled)
	require.Equal(t, []string{"token"}, plugin.ConfigKeysSet)

	t.Run("Config", func(t *testing.T) {
		t.Parallel()
		res := postWebhook(t, ownerClient, plugin.ID, bridgeRequest{Op: "config", Key: "token"})
		require.Equal(t, "secret-value", res.Value)
	})

	t.Run("Tick", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitLong)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return postWebhook(t, ownerClient, plugin.ID, bridgeRequest{Op: "ticks"}).Ticks > 0
		}, testutil.IntervalMedium)
	})

	t.Run("FetchNeedsGrantedOrigin", func(t *testing.T) {
		t.Parallel()
		res := postWebhook(t, ownerClient, plugin.ID, bridgeRequest{Op: "fetch", URL: ownerClient.URL.String() + "/api/v2/buildinfo"})
		require.Contains(t, res.Error, "is not granted")
		// The origin is granted, but loopback is private and not in the
		// private CIDR grant.
		res = postWebhook(t, ownerClient, plugin.ID, bridgeRequest{Op: "fetch", URL: "http://127.0.0.1:9/"})
		require.NotEmpty(t, res.Error)
		require.NotContains(t, res.Error, "is not granted")
	})

	t.Run("UnlinkedAccountCannotSend", func(t *testing.T) {
		t.Parallel()
		res := postWebhook(t, ownerClient, plugin.ID, bridgeRequest{Op: "send", ExternalID: "+15550000000", Text: "hello"})
		require.Contains(t, res.Error, "not linked")
	})

	t.Run("ConverseScopeOnlyReachesChats", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitLong)
		_, token := dbgen.APIKey(t, api.Database, database.APIKey{
			UserID: member.ID,
			Scopes: database.APIKeyScopes{forkdb.APIKeyScopeCoderChatsConverse},
		})
		scoped := codersdk.NewExperimentalClient(codersdk.New(ownerClient.URL, codersdk.WithSessionToken(token)))
		_, err := scoped.CreateChat(ctx, codersdk.CreateChatRequest{
			OrganizationID: owner.OrganizationID,
			Content:        []codersdk.ChatInputPart{{Type: codersdk.ChatInputPartTypeText, Text: "hello"}},
		})
		require.NoError(t, err)
		var sdkErr *codersdk.Error
		_, err = scoped.CreateToken(ctx, codersdk.Me, codersdk.CreateTokenRequest{})
		require.ErrorAs(t, err, &sdkErr)
		require.Contains(t, []int{http.StatusForbidden, http.StatusNotFound}, sdkErr.StatusCode())
	})

	t.Run("LinkSendAndDeliver", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitSuperLong)
		externalID := "+15551234567"

		res := postWebhook(t, ownerClient, plugin.ID, bridgeRequest{Op: "link", ExternalID: externalID, Code: "NOT-A-CODE"})
		require.Contains(t, res.Error, "not valid")

		code, err := memberClient.CreatePluginLinkCode(ctx, plugin.ID)
		require.NoError(t, err)
		// Users type codes by hand, so case and separators do not matter.
		res = postWebhook(t, ownerClient, plugin.ID, bridgeRequest{Op: "link", ExternalID: externalID, Code: " " + code.Code + " "})
		require.Empty(t, res.Error)
		require.Equal(t, member.Username, res.Value)

		res = postWebhook(t, ownerClient, plugin.ID, bridgeRequest{Op: "link", ExternalID: "+15559999999", Code: code.Code})
		require.Contains(t, res.Error, "not valid", "a code links one account")

		linkable, err := memberClient.LinkablePlugins(ctx)
		require.NoError(t, err)
		require.Len(t, linkable, 1)
		require.Equal(t, plugin.ID, linkable[0].ID)
		require.Len(t, linkable[0].Links, 1)
		require.Equal(t, externalID, linkable[0].Links[0].ExternalID)

		res = postWebhook(t, ownerClient, plugin.ID, bridgeRequest{Op: "send", ExternalID: externalID, Text: "hello from signal"})
		require.Empty(t, res.Error)
		chatID, err := uuid.Parse(res.Value)
		require.NoError(t, err)

		//nolint:gocritic // The test reads the chat row to check its owner.
		chat, err := api.Database.GetChatByID(dbauthz.AsSystemRestricted(ctx), chatID)
		require.NoError(t, err)
		require.Equal(t, member.ID, chat.OwnerID)
		require.Equal(t, plugin.Name, chat.Labels[plugins.ChatLabelPlugin])

		var delivered pluginsdk.ChatEvent
		testutil.Eventually(ctx, t, func(context.Context) bool {
			for _, ev := range postWebhook(t, ownerClient, plugin.ID, bridgeRequest{Op: "events"}).Events {
				if ev.ChatID == chatID.String() && len(ev.Messages) > 0 {
					delivered = ev
					return true
				}
			}
			return false
		}, testutil.IntervalMedium)
		assert.Equal(t, externalID, delivered.ExternalID)
		assert.Equal(t, "Hello from test server.", delivered.Messages[0].Text)

		// The chat that replied last gets the next message.
		res = postWebhook(t, ownerClient, plugin.ID, bridgeRequest{Op: "send", ExternalID: externalID, Text: "and again"})
		require.Empty(t, res.Error)
		require.Equal(t, chatID.String(), res.Value)

		res = postWebhook(t, ownerClient, plugin.ID, bridgeRequest{Op: "send", ExternalID: externalID, Text: "new topic", NewChat: true})
		require.Empty(t, res.Error)
		require.NotEqual(t, chatID.String(), res.Value)

		require.NoError(t, memberClient.DeletePluginLink(ctx, plugin.ID, externalID))
		res = postWebhook(t, ownerClient, plugin.ID, bridgeRequest{Op: "send", ExternalID: externalID, Text: "after unlink"})
		require.Contains(t, res.Error, "not linked")
	})
}

func TestPluginsDisabled(t *testing.T) {
	t.Parallel()
	ownerClient := coderdtest.New(t, nil)
	owner := coderdtest.CreateFirstUser(t, ownerClient)
	client := codersdk.NewExperimentalClient(ownerClient)
	ctx := testutil.Context(t, testutil.WaitShort)
	_, err := client.Plugins(ctx, owner.OrganizationID)
	var sdkErr *codersdk.Error
	require.ErrorAs(t, err, &sdkErr)
	require.Equal(t, http.StatusForbidden, sdkErr.StatusCode(), fmt.Sprintf("%+v", sdkErr))
}
