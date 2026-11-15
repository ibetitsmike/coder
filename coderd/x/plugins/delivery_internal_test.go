package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/sqlc-dev/pqtype"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"golang.org/x/time/rate"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"cdr.dev/slog/v3/sloggers/sloghuman"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbmock"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/database/forkdb/forkdbmock"
	"github.com/coder/coder/v2/coderd/database/pubsub"
	coderdpubsub "github.com/coder/coder/v2/coderd/pubsub"
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
// its first chat. It goes to nobody when the pick would be a guess, or when
// the chat was made for or already reached a link that has moved on.
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
		// delivered marks a chat whose events already reached a link.
		delivered bool
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
			name: "DeliveredChatNoLinkFollowsGetsNothing",
			links: []forkdb.PluginUserLink{
				{ExternalID: "idle-link"},
				{ExternalID: "other-chat-link", ActiveChatID: other},
			},
			delivered: true,
			wantNone:  true,
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
			fdb.EXPECT().UpdatePluginChatBindingDelivered(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, arg forkdb.UpdatePluginChatBindingDeliveredParams) error {
					require.Equal(t, !tc.wantNone, arg.Delivered, "binding must record whether a link got the event")
					return nil
				})

			r.deliverChat(ctx, forkdb.GetPluginChatBindingsDueRow{
				PluginID:    r.plugin.ID,
				ChatID:      chatID,
				ChatStatus:  database.ChatStatusWaiting,
				ChatTitle:   "delivery routing",
				ChatOwnerID: owner,
				Delivered:   tc.delivered,
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

// TestRunnerBindsOnlyChangedChats verifies the scan binds no chat: the
// runner binds every chat of a user once when it starts to follow the user,
// and after that one chat for each created event. Before, every scan and
// every chat event ran the full rebind: 40 in 10 minutes of scans.
func TestRunnerBindsOnlyChangedChats(t *testing.T) {
	t.Parallel()
	ctx := testutil.Context(t, testutil.WaitLong)
	ctrl := gomock.NewController(t)
	fdb := forkdbmock.NewMockStore(ctrl)
	ps := pubsub.NewInMemory()
	orgID := uuid.New()
	r := &runner{
		m:      &Manager{opts: Options{Pubsub: ps}, fdb: fdb},
		plugin: forkdb.Plugin{ID: uuid.New(), OrganizationID: orgID},
		grants: codersdk.PluginGrants{
			Chats:  []string{codersdk.PluginChatRead},
			Events: []string{codersdk.PluginEventChatMessagesAdded},
		},
		logger:      testutil.Logger(t),
		deliverWake: make(chan struct{}, 1),
		subs:        map[uuid.UUID]func(){},
		newChats:    map[uuid.UUID]struct{}{},
	}
	t.Cleanup(r.unsubscribeAll)

	var (
		mu     sync.Mutex
		links  = []forkdb.PluginUserLink{{UserID: uuid.New()}}
		full   int
		one    []uuid.UUID
		failed = map[uuid.UUID]bool{}
	)
	fdb.EXPECT().GetPluginUserLinksByPluginID(gomock.Any(), r.plugin.ID).DoAndReturn(
		func(context.Context, uuid.UUID) ([]forkdb.PluginUserLink, error) {
			mu.Lock()
			defer mu.Unlock()
			return slices.Clone(links), nil
		}).AnyTimes()
	fdb.EXPECT().EnsurePluginChatBindings(gomock.Any(), r.plugin.ID).DoAndReturn(
		func(context.Context, uuid.UUID) error {
			mu.Lock()
			defer mu.Unlock()
			full++
			return nil
		}).AnyTimes()
	fdb.EXPECT().EnsurePluginChatBinding(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg forkdb.EnsurePluginChatBindingParams) error {
			mu.Lock()
			defer mu.Unlock()
			require.Equal(t, r.plugin.ID, arg.PluginID)
			if failed[arg.ChatID] {
				delete(failed, arg.ChatID)
				return xerrors.New("bind failed")
			}
			one = append(one, arg.ChatID)
			return nil
		}).AnyTimes()
	fdb.EXPECT().GetPluginChatBindingsDue(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	binds := func() (int, []uuid.UUID) {
		mu.Lock()
		defer mu.Unlock()
		return full, slices.Clone(one)
	}

	// The first scan follows the linked user and binds its chats.
	r.scan(ctx)
	fullBinds, _ := binds()
	require.Equal(t, 1, fullBinds)

	// 10 minutes of scans with no change bind nothing.
	for range 40 {
		r.scan(ctx)
	}
	fullBinds, oneBinds := binds()
	require.Equal(t, 1, fullBinds)
	require.Empty(t, oneBinds)

	userID := links[0].UserID
	publish := func(kind codersdk.ChatWatchEventKind, chat codersdk.Chat) {
		t.Helper()
		msg, err := json.Marshal(codersdk.ChatWatchEvent{Kind: kind, Chat: chat})
		require.NoError(t, err)
		require.NoError(t, ps.Publish(coderdpubsub.ChatWatchEventChannel(userID), msg))
	}
	parentID := uuid.New()
	created := codersdk.Chat{ID: uuid.New(), OrganizationID: orgID}
	publish(codersdk.ChatWatchEventKindCreated, created)
	// A child chat, a chat of another organization and other kinds of
	// event bind nothing.
	publish(codersdk.ChatWatchEventKindCreated, codersdk.Chat{ID: uuid.New(), OrganizationID: orgID, ParentChatID: &parentID})
	publish(codersdk.ChatWatchEventKindCreated, codersdk.Chat{ID: uuid.New(), OrganizationID: uuid.New()})
	publish(codersdk.ChatWatchEventKindStatusChange, codersdk.Chat{ID: uuid.New(), OrganizationID: orgID})
	testutil.TryReceive(ctx, t, r.deliverWake)
	r.deliver(ctx)
	r.deliver(ctx)
	fullBinds, oneBinds = binds()
	require.Equal(t, 1, fullBinds)
	require.Equal(t, []uuid.UUID{created.ID}, oneBinds)

	// A chat whose binding failed is bound by the next delivery.
	retried := codersdk.Chat{ID: uuid.New(), OrganizationID: orgID}
	mu.Lock()
	failed[retried.ID] = true
	mu.Unlock()
	publish(codersdk.ChatWatchEventKindCreated, retried)
	r.deliver(ctx)
	_, oneBinds = binds()
	require.Equal(t, []uuid.UUID{created.ID}, oneBinds)
	r.deliver(ctx)
	_, oneBinds = binds()
	require.Equal(t, []uuid.UUID{created.ID, retried.ID}, oneBinds)

	// A newly linked user has its chats bound once.
	mu.Lock()
	links = append(links, forkdb.PluginUserLink{UserID: uuid.New()})
	mu.Unlock()
	r.scan(ctx)
	r.scan(ctx)
	fullBinds, _ = binds()
	require.Equal(t, 2, fullBinds)
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
