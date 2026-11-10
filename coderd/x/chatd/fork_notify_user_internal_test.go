package chatd

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/notifications"
	"github.com/coder/coder/v2/coderd/x/chatd/chatstate"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/quartz"
)

func TestNotifyUserTool(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := chatdTestContext(t)
	chat := f.createWakeupChat(t)
	other := f.createWakeupChat(t)

	mClock := quartz.NewMock(t)
	mClock.Set(wakeupTestSlot)
	server := newUnstartedServer(t, f.pubsub, f.db, withInternalTestServerClock(mClock))
	server.experiments = codersdk.Experiments{codersdk.ExperimentForkChatWakeups}
	useStoreEnqueuer(t, f, server, mClock)
	notify := func(chat database.Chat, args notifyUserArgs, wantError bool) string {
		t.Helper()
		tools, err := server.appendForkTools(ctx, nil, chat, chat.LastModelConfigID)
		require.NoError(t, err)
		return runChatTodoTool(ctx, t, tools, notifyUserToolName, args, wantError)
	}

	require.JSONEq(t, `{"sent": true}`, notify(chat, notifyUserArgs{Title: "CI", Message: "The build is green."}, false))
	notify(chat, notifyUserArgs{Message: "Uses the chat title."}, false)
	require.ElementsMatch(t, []inboxNotification{
		{ChatID: chat.ID.String(), Title: "CI", Message: "The build is green."},
		{ChatID: chat.ID.String(), Title: chat.Title, Message: "Uses the chat title."},
	}, f.inboxNotifications(t, chat.OwnerID))

	require.Contains(t, notify(chat, notifyUserArgs{Title: "x", Message: "  "}, true), "message is required")

	for i := range maxChatUserNotificationsPerHour - 2 {
		notify(chat, notifyUserArgs{Message: fmt.Sprintf("update %d", i)}, false)
	}
	require.Equal(t, "notification limit reached: at most 10 per chat per hour",
		notify(chat, notifyUserArgs{Message: "one too many"}, true))
	notify(other, notifyUserArgs{Message: "another chat has its own limit"}, false)

	// The server's background tickers keep the mock clock from jumping an
	// hour, so the notifications move an hour back instead.
	_, err := f.sqlDB.ExecContext(ctx, "UPDATE notification_messages SET created_at = created_at - interval '1 hour 1 second' WHERE user_id = $1", chat.OwnerID)
	require.NoError(t, err)
	notify(chat, notifyUserArgs{Message: "an hour later"}, false)
	require.Len(t, f.inboxNotifications(t, chat.OwnerID), maxChatUserNotificationsPerHour+2)

	// A chat title may be longer than a notification title, so the default
	// is cut to the limit on a rune boundary.
	long := f.createWakeupChat(t)
	long.Title = strings.Repeat("é", 100)
	notify(long, notifyUserArgs{Message: "long title"}, false)
	require.Contains(t, f.inboxNotifications(t, long.OwnerID),
		inboxNotification{ChatID: long.ID.String(), Title: strings.Repeat("é", maxNotificationTitleBytes/2), Message: "long title"})

	// Parallel calls in one step would all count the same notifications and
	// could pass the limit together.
	tools, err := server.appendForkTools(ctx, nil, chat, chat.LastModelConfigID)
	require.NoError(t, err)
	idx := slices.IndexFunc(tools, func(tool fantasy.AgentTool) bool { return tool.Info().Name == notifyUserToolName })
	require.GreaterOrEqual(t, idx, 0)
	serial, ok := tools[idx].(interface{ SerialToolCalls() bool })
	require.True(t, ok && serial.SerialToolCalls(), "notify_user calls must run one at a time")

	system, err := f.db.GetUserByID(ctx, database.PrebuildsSystemUserID)
	require.NoError(t, err)
	require.True(t, system.IsSystem)
	res, err := chatstate.CreateChat(ctx, f.db, f.pubsub, chatstate.CreateChatInput{
		OrganizationID:    f.org.ID,
		OwnerID:           system.ID,
		LastModelConfigID: f.model.ID,
		Title:             "system chat",
		ClientType:        database.ChatClientTypeApi,
		InitialStatus:     database.ChatStatusWaiting,
		InitialMessages: []chatstate.Message{
			userTextMessage(t, "hello", system.ID, f.model.ID),
		},
	})
	require.NoError(t, err)
	require.Contains(t, notify(res.Chat, notifyUserArgs{Message: "hi"}, true), "system user")
	require.Empty(t, f.inboxNotifications(t, system.ID))
}

type inboxNotification struct {
	ChatID  string
	Title   string
	Message string
}

// inboxNotifications returns the custom inbox notifications of userID.
func (f *workerTestFixture) inboxNotifications(t *testing.T, userID uuid.UUID) []inboxNotification {
	t.Helper()
	rows, err := f.sqlDB.QueryContext(testutil.Context(t, testutil.WaitShort), `
		SELECT payload FROM notification_messages
		WHERE user_id = $1 AND method = 'inbox' AND notification_template_id = $2`,
		userID, notifications.TemplateCustomNotification)
	require.NoError(t, err)
	defer rows.Close()
	var got []inboxNotification
	for rows.Next() {
		var raw []byte
		require.NoError(t, rows.Scan(&raw))
		var payload struct {
			Labels map[string]string `json:"labels"`
			Data   map[string]any    `json:"data"`
		}
		require.NoError(t, json.Unmarshal(raw, &payload))
		chatID, _ := payload.Data["chat_id"].(string)
		got = append(got, inboxNotification{ChatID: chatID, Title: payload.Labels["custom_title"], Message: payload.Labels["custom_message"]})
	}
	require.NoError(t, rows.Err())
	return got
}
