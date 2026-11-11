package plugins

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/sqlc-dev/pqtype"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"golang.org/x/time/rate"

	"cdr.dev/slog/v3"
	"cdr.dev/slog/v3/sloggers/sloghuman"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbmock"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/database/forkdb/forkdbmock"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprompt"
	"github.com/coder/coder/v2/coderd/x/plugins/pluginhost"
	"github.com/coder/coder/v2/coderd/x/plugins/plugintest"
	"github.com/coder/coder/v2/coderd/x/plugins/sandbox"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
)

const bridgePluginPackage = "github.com/coder/coder/v2/coderd/x/plugins/testdata/bridge"

// TestDeliverChatRoutesToActiveLink verifies a chat's events reach only the
// plugin link that follows that chat. One Coder user may link several
// external identities to a plugin, and each identity follows its own active
// chat, so broadcasting to every link of the owner would push one identity's
// conversation to all of them. A chat no link follows yet goes to the
// owner's one idle link, so a plugin that cannot create chats still reaches
// its first chat. It goes to nobody when the pick would be a guess or when a
// plugin started the chat for a link that has moved on.
func TestDeliverChatRoutesToActiveLink(t *testing.T) {
	t.Parallel()

	chatID := uuid.New()
	active := uuid.NullUUID{UUID: chatID, Valid: true}
	other := uuid.NullUUID{UUID: uuid.New(), Valid: true}
	for _, tc := range []struct {
		name string
		// links are the owner's links; the first one is the expected
		// recipient unless wantNone is set.
		links []forkdb.PluginUserLink
		// chat is what the chat lookup returns, when delivery needs it.
		chat *database.Chat
		// bootstrap expects the recipient to be given the chat first.
		bootstrap bool
		wantNone  bool
	}{
		{
			name: "ActiveLinkOnly",
			links: []forkdb.PluginUserLink{
				{ExternalID: "active-link", ActiveChatID: active},
				{ExternalID: "other-chat-link", ActiveChatID: other},
				{ExternalID: "idle-link"},
			},
		},
		{
			name: "SingleIdleLinkBootstraps",
			links: []forkdb.PluginUserLink{
				{ExternalID: "idle-link"},
				{ExternalID: "other-chat-link", ActiveChatID: other},
			},
			chat:      &database.Chat{ID: chatID},
			bootstrap: true,
		},
		{
			name: "PluginChatNoLinkFollowsGetsNothing",
			links: []forkdb.PluginUserLink{
				{ExternalID: "idle-link"},
				{ExternalID: "other-chat-link", ActiveChatID: other},
			},
			chat:     &database.Chat{ID: chatID, Labels: database.StringMap{ChatLabelPlugin: "bridge"}},
			wantNone: true,
		},
		{
			name: "SeveralIdleLinksGetNothing",
			links: []forkdb.PluginUserLink{
				{ExternalID: "idle-link-1"},
				{ExternalID: "idle-link-2"},
				{ExternalID: "other-chat-link", ActiveChatID: other},
			},
			wantNone: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, db, fdb, logs := newDeliveryRunner(t)
			ctx := testutil.Context(t, testutil.WaitLong)
			owner := uuid.New()

			db.EXPECT().GetChatMessagesByChatIDAscPaginated(gomock.Any(), database.GetChatMessagesByChatIDAscPaginatedParams{
				ChatID:   chatID,
				AfterID:  0,
				LimitVal: messagesPerEvent,
			}).Return([]database.ChatMessage{{
				ID:             7,
				Role:           database.ChatMessageRoleAssistant,
				Content:        nullRawMessage(`[{"type":"text","text":"hello"}]`),
				ContentVersion: chatprompt.ContentVersionV1,
			}}, nil)
			fdb.EXPECT().GetPluginUserLinksByPluginAndUser(gomock.Any(), forkdb.GetPluginUserLinksByPluginAndUserParams{
				PluginID: r.plugin.ID,
				UserID:   owner,
			}).Return(tc.links, nil)
			if tc.chat != nil {
				db.EXPECT().GetChatByID(gomock.Any(), chatID).Return(*tc.chat, nil)
			}
			if tc.bootstrap {
				fdb.EXPECT().SetPluginUserLinkActiveChat(gomock.Any(), forkdb.SetPluginUserLinkActiveChatParams{
					PluginID:     r.plugin.ID,
					ExternalID:   tc.links[0].ExternalID,
					ActiveChatID: active,
				}).Return(nil)
			}
			fdb.EXPECT().UpdatePluginChatBindingDelivered(gomock.Any(), gomock.Any()).Return(nil)

			r.deliverChat(ctx, forkdb.GetPluginChatBindingsDueRow{
				PluginID:    r.plugin.ID,
				ChatID:      chatID,
				ChatStatus:  database.ChatStatusWaiting,
				ChatTitle:   "delivery routing",
				ChatOwnerID: owner,
			})

			delivered := logs.String()
			for i, l := range tc.links {
				event := "bridge-event external_id=" + l.ExternalID + " "
				if i == 0 && !tc.wantNone {
					require.Contains(t, delivered, event+"chat_id="+chatID.String())
					continue
				}
				require.NotContains(t, delivered, event, "link %s must not receive this chat's events", l.ExternalID)
			}
		})
	}
}

// newDeliveryRunner returns a runner with the bridge plugin loaded
// in-process. The bridge plugin logs each event it receives, so the returned
// buffer shows which links got an event.
func newDeliveryRunner(t *testing.T) (*runner, *dbmock.MockStore, *forkdbmock.MockStore, *bytes.Buffer) {
	t.Helper()
	ctrl := gomock.NewController(t)
	db := dbmock.NewMockStore(ctrl)
	fdb := forkdbmock.NewMockStore(ctrl)

	grants, err := json.Marshal(codersdk.PluginGrants{
		Events: []string{codersdk.PluginEventChatMessagesAdded},
	})
	require.NoError(t, err)

	var logs bytes.Buffer
	r := &runner{
		m:      &Manager{opts: Options{HTTPClient: &http.Client{}}, db: db, fdb: fdb},
		plugin: forkdb.Plugin{Manifest: []byte(`{}`), Grants: grants},
		logger: slog.Make(sloghuman.Sink(&logs)),

		logLimiter: rate.NewLimiter(20, 100),
	}
	require.NoError(t, r.decode())
	// Build before the deadline starts: a cold wasm build can exceed
	// WaitSuperLong and does not observe the context.
	wasm := plugintest.BuildWasm(t, bridgePluginPackage)
	ctx := testutil.Context(t, testutil.WaitSuperLong)
	client, err := pluginhost.StartInProcess(ctx, testutil.Logger(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	_, err = client.Load(ctx, wasm, sandbox.Limits{CallTimeout: testutil.WaitShort})
	require.NoError(t, err)
	r.client = client
	r.ready = make(chan struct{})
	close(r.ready)
	return r, db, fdb, &logs
}

func nullRawMessage(s string) pqtype.NullRawMessage {
	return pqtype.NullRawMessage{
		RawMessage: json.RawMessage(s),
		Valid:      s != "",
	}
}
