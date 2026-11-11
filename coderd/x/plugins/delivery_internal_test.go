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
// plugin link whose active chat is that chat. One Coder user may link several
// external identities to a plugin, and each identity follows its own active
// chat, so broadcasting to every link of the owner would push one identity's
// conversation to all of them.
func TestDeliverChatRoutesToActiveLink(t *testing.T) {
	t.Parallel()

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
	ctx := testutil.Context(t, testutil.WaitSuperLong)
	client, err := pluginhost.StartInProcess(ctx, testutil.Logger(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	_, err = client.Load(ctx, plugintest.BuildWasm(t, bridgePluginPackage), sandbox.Limits{CallTimeout: testutil.WaitShort})
	require.NoError(t, err)
	r.client = client
	r.ready = make(chan struct{})
	close(r.ready)

	chatID := uuid.New()
	owner := uuid.New()
	msg := database.ChatMessage{
		ID:             7,
		Role:           database.ChatMessageRoleAssistant,
		Content:        nullRawMessage(`[{"type":"text","text":"hello active link"}]`),
		ContentVersion: chatprompt.ContentVersionV1,
	}
	db.EXPECT().GetChatMessagesByChatIDAscPaginated(gomock.Any(), database.GetChatMessagesByChatIDAscPaginatedParams{
		ChatID:   chatID,
		AfterID:  0,
		LimitVal: messagesPerEvent,
	}).Return([]database.ChatMessage{msg}, nil)
	fdb.EXPECT().GetPluginUserLinksByPluginAndUser(gomock.Any(), forkdb.GetPluginUserLinksByPluginAndUserParams{
		PluginID: r.plugin.ID,
		UserID:   owner,
	}).Return([]forkdb.PluginUserLink{
		{ExternalID: "active-link", ActiveChatID: uuid.NullUUID{UUID: chatID, Valid: true}},
		{ExternalID: "other-chat-link", ActiveChatID: uuid.NullUUID{UUID: uuid.New(), Valid: true}},
		{ExternalID: "no-active-chat-link"},
	}, nil)
	fdb.EXPECT().UpdatePluginChatBindingDelivered(gomock.Any(), gomock.Any()).Return(nil)

	r.deliverChat(ctx, forkdb.GetPluginChatBindingsDueRow{
		PluginID:    r.plugin.ID,
		ChatID:      chatID,
		ChatStatus:  database.ChatStatusWaiting,
		ChatTitle:   "delivery routing",
		ChatOwnerID: owner,
	})

	delivered := logs.String()
	require.Contains(t, delivered, "bridge-event external_id=active-link chat_id="+chatID.String())
	require.NotContains(t, delivered, "other-chat-link", "a link bound to another chat must not receive this chat's events")
	require.NotContains(t, delivered, "no-active-chat-link", "a link with no active chat must not receive events")
}

func nullRawMessage(s string) pqtype.NullRawMessage {
	return pqtype.NullRawMessage{
		RawMessage: json.RawMessage(s),
		Valid:      s != "",
	}
}
