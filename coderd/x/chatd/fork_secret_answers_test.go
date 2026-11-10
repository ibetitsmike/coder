package chatd_test

// This file is fork code: it checks end to end that a secret answer of the
// fork-nested-subagents experiment reaches a workspace tool but never the
// model or the chat's messages.

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd"
	"github.com/coder/coder/v2/coderd/x/chatd/chattest"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk/agentconnmock"
	"github.com/coder/coder/v2/testutil"
)

func TestSecretAnswerReachesToolsNotModel(t *testing.T) {
	t.Parallel()

	// The quote and the angle brackets differ in every JSON escaping the
	// scrub has to match.
	const (
		secret      = `tok-"q"-<s3cret>&`
		callID      = "call_secret"
		placeholder = "{{secret:" + callID + "}}"
	)
	escaped, err := json.Marshal(secret)
	require.NoError(t, err)
	leaks := func(text string) bool {
		return strings.Contains(text, secret) || strings.Contains(text, string(escaped[1:len(escaped)-1])) ||
			strings.Contains(text, `tok-\"q\"-<s3cret>&`)
	}

	db, ps := dbtestutil.NewDB(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	fdb, err := forkdb.From(db)
	require.NoError(t, err)

	var (
		mu         sync.Mutex
		modelSaw   []string
		toolResult string
		written    string
	)
	openAIURL := chattest.NewOpenAI(t, func(req *chattest.OpenAIRequest) chattest.OpenAIResponse {
		if !req.Stream {
			return chattest.OpenAINonStreamingResponse("title")
		}
		raw, err := json.Marshal(req.Messages)
		if err != nil {
			return chattest.OpenAIStreamingResponse(chattest.OpenAITextChunks("encode failed")...)
		}
		var tools []string
		for _, message := range req.Messages {
			if message.Role == "tool" {
				tools = append(tools, message.Content)
			}
		}
		mu.Lock()
		modelSaw = append(modelSaw, string(raw))
		if len(tools) == 2 {
			toolResult = tools[1]
		}
		mu.Unlock()
		switch len(tools) {
		case 0:
			return chattest.OpenAIStreamingResponse(chattest.OpenAIToolCallChunk("write_file",
				`{"path":"/tmp/token","content":"`+placeholder+`"}`))
		case 1:
			return chattest.OpenAIStreamingResponse(chattest.OpenAIToolCallChunk("read_file", `{"path":"/tmp/token"}`))
		default:
			return chattest.OpenAIStreamingResponse(chattest.OpenAITextChunks("done")...)
		}
	})
	user, org, model := seedChatDependenciesWithProvider(t, db, "openai-compat", openAIURL)
	ws, dbAgent := seedWorkspaceWithAgent(t, db, user.ID)

	ctrl := gomock.NewController(t)
	mockConn := agentconnmock.NewMockAgentConn(ctrl)
	setupToolExecutionAgentConn(t, mockConn)
	mockConn.EXPECT().WriteFile(gomock.Any(), "/tmp/token", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, reader io.Reader) error {
			data, err := io.ReadAll(reader)
			mu.Lock()
			written = string(data)
			mu.Unlock()
			return err
		})
	mockConn.EXPECT().ReadFileLines(gomock.Any(), "/tmp/token", int64(1), int64(0), gomock.Any()).
		Return(workspacesdk.ReadFileLinesResponse{
			Success: true, FileSize: int64(len(secret)), TotalLines: 1, LinesRead: 1, Content: secret,
		}, nil)

	server := newActiveTestServer(t, db, ps, func(cfg *chatd.Config) {
		cfg.AIBridgeTransportFactory = chatAIGatewayTransportFactoryPointer(chattest.NewMockAIBridgeTransport(t, openAIURL))
		cfg.Experiments = codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}
		cfg.AgentConn = func(context.Context, uuid.UUID) (workspacesdk.AgentConn, func(), error) {
			return mockConn, func() {}, nil
		}
	})

	seed := database.Chat{
		OrganizationID:    org.ID,
		OwnerID:           user.ID,
		LastModelConfigID: model.ID,
		WorkspaceID:       uuid.NullUUID{UUID: ws.ID, Valid: true},
		AgentID:           uuid.NullUUID{UUID: dbAgent.ID, Valid: true},
	}
	root := dbgen.Chat(t, db, seed)
	seed.ParentChatID = uuid.NullUUID{UUID: root.ID, Valid: true}
	seed.RootChatID = seed.ParentChatID
	child := dbgen.Chat(t, db, seed)
	_, err = fdb.UpsertChatDescendantQuestion(ctx, forkdb.UpsertChatDescendantQuestionParams{
		ChatID: child.ID, ToolCallID: callID,
		Questions: json.RawMessage(`[{"header":"Token","question":"Paste the token.","secret":true}]`),
	})
	require.NoError(t, err)
	require.NoError(t, fdb.InsertChatSecretAnswer(ctx, forkdb.InsertChatSecretAnswerParams{
		ChatID: child.ID, ToolCallID: callID, Value: secret,
	}))

	_, err = server.SendMessage(ctx, chatd.SendMessageOptions{
		ChatID:    child.ID,
		CreatedBy: user.ID,
		Content: []codersdk.ChatMessagePart{codersdk.ChatMessageText(
			"Answer to ask_user_question call " + callID + ":\nThe user gave a secret answer.")},
		ModelConfigID: model.ID,
	})
	require.NoError(t, err)
	waitForChatStatus(ctx, t, db, child.ID, database.ChatStatusWaiting)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, secret, written, "write_file gets the value in place of the placeholder")
	require.Len(t, modelSaw, 3)
	for _, request := range modelSaw {
		require.False(t, leaks(request), "a model request holds the value: %s", request)
	}
	require.Contains(t, toolResult, placeholder, "read_file output shows the placeholder")
	for _, message := range chatMessages(ctx, t, db, child.ID) {
		require.False(t, leaks(string(message.Content.RawMessage)), "a chat message holds the value: %s", message.Content.RawMessage)
	}
}
