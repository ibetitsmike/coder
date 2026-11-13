package chatd

// This file is fork code: it tests that a replayed message_agent call returns
// without sending its message again (fork_message_agent_replay.go).

import (
	"database/sql"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

// TestForkMessageAgentReplay runs message_agent twice with one tool call ID,
// as chatd does when it replays a tool batch that a restart cut. The child
// must get the message once.
func TestForkMessageAgentReplay(t *testing.T) {
	t.Parallel()

	f := newNestedSubagentsFixture(t, codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}, Limits{})
	ctx := chatdTestContext(t)
	child, err := f.spawn(t, f.root, childSubagentChatOptions{})
	require.NoError(t, err)

	tools, err := f.server.subagentTools(ctx, func() database.Chat { return f.root }, f.root.LastModelConfigID)
	require.NoError(t, err)
	tools, err = f.server.appendForkTools(ctx, tools, f.root, f.root.LastModelConfigID)
	require.NoError(t, err)
	tool := findToolByName(tools, messageAgentToolName)
	require.NotNil(t, tool)

	send := func(callID uuid.UUID, text string) string {
		t.Helper()
		resp, err := tool.Run(workspacesdk.WithToolCallID(ctx, callID), fantasy.ToolCall{
			Input: `{"chat_id":"` + child.ID.String() + `","message":"` + text + `"}`,
		})
		require.NoError(t, err)
		id, ok := requireToolResponseMap(t, resp, false)["chat_id"].(string)
		require.True(t, ok, resp.Content)
		return id
	}
	delivered := func(text string) int {
		t.Helper()
		count := 0
		msgs, err := f.db.GetChatMessagesByChatID(ctx, database.GetChatMessagesByChatIDParams{ChatID: child.ID})
		require.NoError(t, err)
		for _, msg := range msgs {
			if msg.Content.Valid && strings.Contains(string(msg.Content.RawMessage), text) {
				count++
			}
		}
		queued, err := f.db.GetChatQueuedMessages(ctx, child.ID)
		require.NoError(t, err)
		for _, msg := range queued {
			if strings.Contains(string(msg.Content), text) {
				count++
			}
		}
		return count
	}

	callID := uuid.New()
	require.Equal(t, child.ID.String(), send(callID, "NOTE-ONE"))
	require.Equal(t, child.ID.String(), send(callID, "NOTE-ONE"), "a replay returns the recipient")
	require.Equal(t, 1, delivered("NOTE-ONE"), "a replay does not send again")

	require.Equal(t, child.ID.String(), send(uuid.New(), "NOTE-ONE"))
	require.Equal(t, 2, delivered("NOTE-ONE"), "a new call sends again")

	// A send that does not come from the message_agent tool, such as a
	// workflow step, records nothing.
	_, err = f.server.sendSubagentMessage(workspacesdk.WithToolCallID(ctx, uuid.New()), f.root.ID, child.ID, "NOTE-TWO")
	require.NoError(t, err)
	require.Equal(t, 1, delivered("NOTE-TWO"))
	var rows int
	require.NoError(t, f.sqlDB.QueryRowContext(ctx,
		`SELECT count(*) FROM chat_subagent_messages WHERE sender_chat_id = $1`, f.root.ID).Scan(&rows))
	require.Equal(t, 2, rows)

	//nolint:gocritic // The test reads chatd's bookkeeping rows.
	_, err = f.fdb.GetChatSubagentMessageRecipient(dbauthz.AsChatd(ctx), forkdb.GetChatSubagentMessageRecipientParams{
		ID:           callID,
		SenderChatID: child.ID,
	})
	require.ErrorIs(t, err, sql.ErrNoRows, "a call ID is scoped to its sender")
}
