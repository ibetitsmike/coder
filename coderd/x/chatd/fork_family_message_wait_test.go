package chatd_test

// This file is fork code: it tests that a family message ends a wait_agent
// call of the fork-family-messages experiment.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/x/chatd"
	"github.com/coder/coder/v2/coderd/x/chatd/chattest"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
)

// TestWaitAgentEndsForFamilyMessage has a parent wait for a child that never
// finishes, with a wait timeout of an hour. The child sends the parent a
// family message while the parent waits. The wait must end early, and the
// parent's next model call must see the message once.
func TestWaitAgentEndsForFamilyMessage(t *testing.T) {
	t.Parallel()

	db, ps := dbtestutil.NewDB(t)
	ctx := testutil.Context(t, testutil.WaitLong)

	const (
		childPrompt = "CHILD-ASKS-PARENT"
		question    = "NEED-INPUT-BEFORE-I-FINISH"
		parentSaw   = "PARENT-SAW-THE-MESSAGE"
	)
	// parentID is set before waitCommitted closes, and only the child reads
	// it after that.
	var parentID uuid.UUID
	// waitCommitted holds the child's message until the parent committed its
	// wait_agent call, so the message arrives while the parent waits.
	waitCommitted := make(chan struct{})
	openAIURL := chattest.NewOpenAI(t, func(req *chattest.OpenAIRequest) chattest.OpenAIResponse {
		if !req.Stream {
			return chattest.OpenAINonStreamingResponse("title")
		}
		var users, toolResults []string
		for _, message := range req.Messages {
			switch message.Role {
			case "user":
				users = append(users, message.Content)
			case "tool":
				toolResults = append(toolResults, message.Content)
			}
		}
		text := func(s string) chattest.OpenAIResponse {
			return chattest.OpenAIStreamingResponse(chattest.OpenAITextChunks(s)...)
		}
		if len(users) > 0 && strings.Contains(users[0], childPrompt) {
			if len(toolResults) == 0 {
				select {
				case <-waitCommitted:
				case <-req.Context().Done():
					return chattest.OpenAIStreamingResponse()
				}
				return chattest.OpenAIStreamingResponse(familyRestartToolCalls(
					[2]string{"message_family_chat", fmt.Sprintf(`{"chat_id":%q,"message":%q}`, parentID, question)},
				))
			}
			// The child never finishes, so only the family message can end
			// the parent's wait.
			<-req.Context().Done()
			return chattest.OpenAIStreamingResponse()
		}
		switch len(toolResults) {
		case 0:
			return chattest.OpenAIStreamingResponse(familyRestartToolCalls(
				[2]string{"spawn_agent", fmt.Sprintf(`{"type":"general","prompt":%q,"title":"child"}`, childPrompt)},
			))
		case 1:
			var spawned struct {
				ChatID string `json:"chat_id"`
			}
			if err := json.Unmarshal([]byte(toolResults[0]), &spawned); err != nil || spawned.ChatID == "" {
				return text("spawn failed: " + toolResults[0])
			}
			return chattest.OpenAIStreamingResponse(familyRestartToolCalls(
				[2]string{"wait_agent", fmt.Sprintf(`{"chat_id":%q,"timeout_seconds":3600}`, spawned.ChatID)},
			))
		default:
			if strings.Contains(strings.Join(users, "\n"), question) {
				return text(parentSaw)
			}
			return text("parent did not see the message")
		}
	})
	user, org, model := seedChatDependenciesWithProvider(t, db, "openai-compat", openAIURL)
	server := newActiveTestServer(t, db, ps, func(cfg *chatd.Config) {
		cfg.AIBridgeTransportFactory = chatAIGatewayTransportFactoryPointer(chattest.NewMockAIBridgeTransport(t, openAIURL))
		cfg.Experiments = codersdk.Experiments{
			codersdk.ExperimentForkNestedSubagents,
			codersdk.ExperimentForkFamilyMessages,
		}
	})
	parent, err := server.CreateChat(ctx, chatd.CreateOptions{
		OrganizationID:     org.ID,
		OwnerID:            user.ID,
		Title:              "waiting-parent",
		ModelConfigID:      model.ID,
		InitialUserContent: []codersdk.ChatMessagePart{codersdk.ChatMessageText("wait for your child")},
	})
	require.NoError(t, err)
	parentID = parent.ID

	testutil.Eventually(ctx, t, func(ctx context.Context) bool {
		return len(familyRestartParts(t, chatMessages(ctx, t, db, parent.ID), codersdk.ChatMessagePartTypeToolCall, "wait_agent")) == 1
	}, testutil.IntervalFast)
	close(waitCommitted)

	testutil.Eventually(ctx, t, func(ctx context.Context) bool {
		return familyRestartCount(t, chatMessages(ctx, t, db, parent.ID), parentSaw) == 1
	}, testutil.IntervalFast)
	parent = waitForChatStatus(ctx, t, db, parent.ID, database.ChatStatusWaiting)
	require.False(t, parent.LastError.Valid, "parent failed: %s", parent.LastError.RawMessage)

	messages := chatMessages(ctx, t, db, parent.ID)
	waits := familyRestartParts(t, messages, codersdk.ChatMessagePartTypeToolResult, "wait_agent")
	require.Len(t, waits, 1)
	var result struct {
		FamilyMessagePending bool   `json:"family_message_pending"`
		Status               string `json:"status"`
	}
	require.NoError(t, json.Unmarshal([]byte(waits[0]), &result), waits[0])
	require.True(t, result.FamilyMessagePending, waits[0])
	require.Equal(t, string(database.ChatStatusRunning), result.Status)
	require.Equal(t, 1, familyRestartCount(t, messages, question), "the family message is delivered once")
	queued, err := db.GetChatQueuedMessages(ctx, parent.ID)
	require.NoError(t, err)
	require.Empty(t, queued)

	children := familyRestartChild(ctx, t, db, parent.ID, 1)
	require.Equal(t, database.ChatStatusRunning, children[0].Status, "the child keeps running")
}
