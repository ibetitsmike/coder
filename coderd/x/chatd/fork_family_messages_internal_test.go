package chatd

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/codersdk"
)

// familyMessageTest is a root chat with two waiting children on a server with
// fork-family-messages on.
type familyMessageTest struct {
	f      *workerTestFixture
	ctx    context.Context
	server *Server
	root   database.Chat
	a, b   database.Chat
}

func newFamilyMessageTest(t *testing.T) *familyMessageTest {
	t.Helper()
	f := newWorkerTestFixture(t)
	server := newUnstartedServer(t, f.pubsub, f.db)
	server.experiments = codersdk.Experiments{codersdk.ExperimentForkFamilyMessages}
	root := f.createWakeupChat(t)
	return &familyMessageTest{
		f:      f,
		ctx:    chatdTestContext(t),
		server: server,
		root:   root,
		a:      f.createWakeupChild(t, root, database.ChatStatusWaiting),
		b:      f.createWakeupChild(t, root, database.ChatStatusWaiting),
	}
}

func (ft *familyMessageTest) send(t *testing.T, sender database.Chat, target uuid.UUID, message string, wantError bool) string {
	t.Helper()
	tools, err := ft.server.appendForkTools(ft.ctx, nil, sender, sender.LastModelConfigID)
	require.NoError(t, err)
	return runChatTodoTool(ft.ctx, t, tools, messageFamilyChatToolName, messageFamilyChatArgs{ChatID: target.String(), Message: message}, wantError)
}

// familyMessageRows returns the recorded family messages as sender>recipient.
func (ft *familyMessageTest) familyMessageRows(t *testing.T) []string {
	t.Helper()
	rows, err := ft.f.sqlDB.QueryContext(ft.ctx, "SELECT sender_chat_id, recipient_chat_id FROM chat_family_messages")
	require.NoError(t, err)
	defer rows.Close()
	var got []string
	for rows.Next() {
		var sender, recipient uuid.UUID
		require.NoError(t, rows.Scan(&sender, &recipient))
		got = append(got, sender.String()+">"+recipient.String())
	}
	require.NoError(t, rows.Err())
	return got
}

func TestFamilyMessageDelivery(t *testing.T) {
	t.Parallel()
	ft := newFamilyMessageTest(t)

	off := newUnstartedServer(t, ft.f.pubsub, ft.f.db)
	tools, err := off.appendForkTools(ft.ctx, nil, ft.a, ft.a.LastModelConfigID)
	require.NoError(t, err)
	require.NotContains(t, toolNames(tools), messageFamilyChatToolName)

	// The envelope carries the sender title at send time, not at turn start.
	_, err = ft.f.sqlDB.ExecContext(ft.ctx, "UPDATE chats SET title = $1 WHERE id = $2", "researcher \"A\"", ft.a.ID)
	require.NoError(t, err)

	require.JSONEq(t, fmt.Sprintf(`{"chat_id": %q, "status": "running", "title": %q}`, ft.b.ID, ft.b.Title),
		ft.send(t, ft.a, ft.b.ID, "  found the bug in parser.go  ", false))
	require.Equal(t, []string{
		"<agent-message from_chat_id=\"" + ft.a.ID.String() + "\" from_title=\"researcher \\\"A\\\"\">\n" +
			"This message comes from another agent in your chat family, not from the user. " +
			"To reply, call message_family_chat with chat_id \"" + ft.a.ID.String() + "\".\n\n" +
			"found the bug in parser.go\n</agent-message>",
	}, ft.f.wakeMessages(t, ft.b, "<agent-message"))

	// A child reaches its parent the same way.
	ft.send(t, ft.a, ft.root.ID, "done", false)
	require.Equal(t, []string{familyMessageEnvelope(ft.a.ID, "researcher \"A\"", "done")},
		ft.f.wakeMessages(t, ft.root, "<agent-message"))
	root, err := ft.f.db.GetChatByID(ft.ctx, ft.root.ID)
	require.NoError(t, err)
	require.Equal(t, database.ChatStatusRunning, root.Status)

	require.ElementsMatch(t, []string{
		ft.a.ID.String() + ">" + ft.b.ID.String(),
		ft.a.ID.String() + ">" + ft.root.ID.String(),
	}, ft.familyMessageRows(t))
}

func TestFamilyMessageSteersBusyRecipient(t *testing.T) {
	t.Parallel()
	ft := newFamilyMessageTest(t)
	setChatStatus(ft.ctx, t, ft.f.db, ft.b.ID, database.ChatStatusRunning, "")

	require.JSONEq(t, fmt.Sprintf(`{"chat_id": %q, "queued": true, "status": "running", "title": %q}`, ft.b.ID, ft.b.Title),
		ft.send(t, ft.a, ft.b.ID, "change of plan", false))

	queued, err := ft.f.db.GetChatQueuedMessages(ft.ctx, ft.b.ID)
	require.NoError(t, err)
	require.Len(t, queued, 1)
	require.Equal(t, database.ChatBusyBehaviorSteer, queued[0].BusyBehavior)
	require.Contains(t, string(queued[0].Content), "change of plan")
	b, err := ft.f.db.GetChatByID(ft.ctx, ft.b.ID)
	require.NoError(t, err)
	require.Equal(t, database.ChatStatusRunning, b.Status)
	require.Len(t, ft.familyMessageRows(t), 1)
}

func TestFamilyMessageRefusals(t *testing.T) {
	t.Parallel()
	ft := newFamilyMessageTest(t)
	otherRoot := ft.f.createWakeupChat(t)
	otherChild := ft.f.createWakeupChild(t, otherRoot, database.ChatStatusWaiting)
	archived := ft.f.createWakeupChild(t, ft.root, database.ChatStatusWaiting)
	_, err := ft.f.db.ArchiveChatByID(ft.ctx, archived.ID)
	require.NoError(t, err)
	unknown := uuid.New()

	for _, tc := range []struct {
		name    string
		sender  database.Chat
		target  string
		message string
		want    string
	}{
		{"OtherFamily", ft.a, otherChild.ID.String(), "hi", fmt.Sprintf("chat %s is not in this chat's family", otherChild.ID)},
		{"OtherRoot", ft.root, otherRoot.ID.String(), "hi", fmt.Sprintf("chat %s is not in this chat's family", otherRoot.ID)},
		{"Unknown", ft.a, unknown.String(), "hi", fmt.Sprintf("chat %s is not in this chat's family", unknown)},
		{"Self", ft.a, ft.a.ID.String(), "hi", "a chat cannot message itself"},
		{"Archived", ft.a, archived.ID.String(), "hi", fmt.Sprintf("chat %s is archived", archived.ID)},
		{"Empty", ft.a, ft.b.ID.String(), " \n ", "message is required"},
		{"TooLong", ft.a, ft.b.ID.String(), strings.Repeat("x", maxFamilyMessageBytes+1), "message is too long: at most 16384 bytes"},
		{"BadID", ft.a, "not-a-uuid", "hi", "chat_id must be a valid UUID"},
	} {
		tools, err := ft.server.appendForkTools(ft.ctx, nil, tc.sender, tc.sender.LastModelConfigID)
		require.NoError(t, err)
		require.Equal(t, tc.want, runChatTodoTool(ft.ctx, t, tools, messageFamilyChatToolName,
			messageFamilyChatArgs{ChatID: tc.target, Message: tc.message}, true), tc.name)
	}
	require.Empty(t, ft.familyMessageRows(t))
	require.Empty(t, ft.f.wakeMessages(t, ft.b, "<agent-message"))
}

func TestFamilyMessageRateLimit(t *testing.T) {
	t.Parallel()
	ft := newFamilyMessageTest(t)

	// The recipient goes back to waiting after each send, so the messages do
	// not fill its queue.
	for i := range maxFamilyMessagesPerHour {
		ft.send(t, ft.a, ft.b.ID, fmt.Sprintf("update %d", i), false)
		setChatStatus(ft.ctx, t, ft.f.db, ft.b.ID, database.ChatStatusWaiting, "")
	}
	require.Equal(t, "message limit reached: at most 60 messages per chat per hour",
		ft.send(t, ft.a, ft.b.ID, "one too many", true))
	ft.send(t, ft.b, ft.a.ID, "another sender has its own limit", false)

	_, err := ft.f.sqlDB.ExecContext(ft.ctx, "UPDATE chat_family_messages SET created_at = created_at - interval '1 hour 1 second' WHERE sender_chat_id = $1", ft.a.ID)
	require.NoError(t, err)
	ft.send(t, ft.a, ft.b.ID, "an hour later", false)
	require.Len(t, ft.f.wakeMessages(t, ft.b, "<agent-message"), maxFamilyMessagesPerHour+1)

	// Parallel calls in one step would all count the same messages and could
	// pass the limit together.
	tools, err := ft.server.appendForkTools(ft.ctx, nil, ft.a, ft.a.LastModelConfigID)
	require.NoError(t, err)
	idx := slices.IndexFunc(tools, func(tool fantasy.AgentTool) bool { return tool.Info().Name == messageFamilyChatToolName })
	require.GreaterOrEqual(t, idx, 0)
	serial, ok := tools[idx].(interface{ SerialToolCalls() bool })
	require.True(t, ok && serial.SerialToolCalls(), "message_family_chat calls must run one at a time")
}
