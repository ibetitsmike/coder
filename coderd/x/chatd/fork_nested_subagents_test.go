package chatd_test

// This file is fork code: it tests nested subagents behind the
// fork-nested-subagents experiment end to end.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd"
	"github.com/coder/coder/v2/coderd/x/chatd/chattest"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
)

// TestNestedSubagentFamilyCompletes runs a root, a coordinator child and a
// worker grandchild against a fake model: the root spawns the coordinator,
// the coordinator spawns the worker, and the worker's answer must reach the
// root through the coordinator's report.
func TestNestedSubagentFamilyCompletes(t *testing.T) {
	t.Parallel()

	const (
		coordinatorPrompt = "COORDINATE-FAMILY"
		workerPrompt      = "DO-WORKER-TASK"
		workerResult      = "WORKER-RESULT-42"
	)
	db, ps := dbtestutil.NewDB(t)
	ctx := testutil.Context(t, testutil.WaitLong)

	// Each chat's turn is decided from its own request: its first user
	// message names the chat, and its tool results so far give the step.
	openAIURL := chattest.NewOpenAI(t, func(req *chattest.OpenAIRequest) chattest.OpenAIResponse {
		if !req.Stream {
			return chattest.OpenAINonStreamingResponse("title")
		}
		var userText string
		var toolResults []string
		for _, message := range req.Messages {
			switch message.Role {
			case "user":
				if userText == "" {
					userText = message.Content
				}
			case "tool":
				toolResults = append(toolResults, message.Content)
			}
		}
		spawnThenWait := func(childPrompt, title string) chattest.OpenAIResponse {
			if len(toolResults) == 0 {
				return chattest.OpenAIStreamingResponse(chattest.OpenAIToolCallChunk("spawn_agent",
					fmt.Sprintf(`{"type":"general","prompt":%q,"title":%q}`, childPrompt, title)))
			}
			var spawned struct {
				ChatID string `json:"chat_id"`
			}
			if err := json.Unmarshal([]byte(toolResults[0]), &spawned); err != nil || spawned.ChatID == "" {
				return chattest.OpenAIStreamingResponse(chattest.OpenAITextChunks("spawn failed: " + toolResults[0])...)
			}
			return chattest.OpenAIStreamingResponse(chattest.OpenAIToolCallChunk("wait_agent",
				fmt.Sprintf(`{"chat_id":%q}`, spawned.ChatID)))
		}
		switch {
		case strings.Contains(userText, workerPrompt):
			return chattest.OpenAIStreamingResponse(chattest.OpenAITextChunks(workerResult)...)
		case strings.Contains(userText, coordinatorPrompt):
			if len(toolResults) < 2 {
				return spawnThenWait(workerPrompt, "worker")
			}
			if strings.Contains(toolResults[1], workerResult) {
				return chattest.OpenAIStreamingResponse(chattest.OpenAITextChunks("coordinator saw " + workerResult)...)
			}
			return chattest.OpenAIStreamingResponse(chattest.OpenAITextChunks("coordinator saw nothing")...)
		default:
			if len(toolResults) < 2 {
				return spawnThenWait(coordinatorPrompt, "coordinator")
			}
			return chattest.OpenAIStreamingResponse(chattest.OpenAITextChunks("root done")...)
		}
	})
	user, org, model := seedChatDependenciesWithProvider(t, db, "openai-compat", openAIURL)
	server := newActiveTestServer(t, db, ps, func(cfg *chatd.Config) {
		cfg.AIBridgeTransportFactory = chatAIGatewayTransportFactoryPointer(chattest.NewMockAIBridgeTransport(t, openAIURL))
		cfg.Experiments = codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}
	})

	root, err := server.CreateChat(ctx, chatd.CreateOptions{
		OrganizationID:     org.ID,
		OwnerID:            user.ID,
		Title:              "nested-family",
		ModelConfigID:      model.ID,
		InitialUserContent: []codersdk.ChatMessagePart{codersdk.ChatMessageText("run the family")},
	})
	require.NoError(t, err)
	waitForChatStatus(ctx, t, db, root.ID, database.ChatStatusWaiting)

	fdb, err := forkdb.From(db)
	require.NoError(t, err)
	childOf := func(parentID uuid.UUID, wantDepth int32) database.Chat {
		t.Helper()
		rows, err := db.GetChildChatsByParentIDs(ctx, database.GetChildChatsByParentIDsParams{
			ParentIds: []uuid.UUID{parentID},
		})
		require.NoError(t, err)
		require.Len(t, rows, 1)
		row, err := fdb.GetChatSubagentByChatID(ctx, rows[0].Chat.ID)
		require.NoError(t, err)
		require.Equal(t, wantDepth, row.Depth)
		return rows[0].Chat
	}
	coordinator := childOf(root.ID, 1)
	worker := childOf(coordinator.ID, 2)
	require.Equal(t, root.ID, worker.RootChatID.UUID)
	rows, err := db.GetChildChatsByParentIDs(ctx, database.GetChildChatsByParentIDsParams{
		ParentIds: []uuid.UUID{worker.ID},
	})
	require.NoError(t, err)
	require.Empty(t, rows)

	var waitResults []string
	for _, message := range chatMessages(ctx, t, db, root.ID) {
		if message.Role == database.ChatMessageRoleTool && strings.Contains(string(message.Content.RawMessage), "wait_agent") {
			waitResults = append(waitResults, string(message.Content.RawMessage))
		}
	}
	require.Len(t, waitResults, 1)
	require.Contains(t, waitResults[0], "coordinator saw "+workerResult)
}
