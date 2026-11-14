package chatd_test

// This file is fork code: it checks end to end that a view_image result of
// the fork-view-image experiment reaches the model as an image, on the turn
// that viewed it and on the next turn.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"slices"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/x/chatd"
	"github.com/coder/coder/v2/coderd/x/chatd/chattest"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk/agentconnmock"
	"github.com/coder/coder/v2/testutil"
)

// anthropicToolResultImages returns the base64 data of every image block
// inside a tool_result block of req.
func anthropicToolResultImages(t *testing.T, req *chattest.AnthropicRequest) []string {
	t.Helper()
	type block struct {
		Type    string          `json:"type"`
		Content json.RawMessage `json:"content"`
		Source  struct {
			MediaType string `json:"media_type"`
			Data      string `json:"data"`
		} `json:"source"`
	}
	var images []string
	for _, message := range req.Messages {
		var blocks []block
		if json.Unmarshal(message.Content, &blocks) != nil {
			continue
		}
		for _, result := range blocks {
			var inner []block
			if result.Type != "tool_result" || json.Unmarshal(result.Content, &inner) != nil {
				continue
			}
			for _, part := range inner {
				if part.Type == "image" {
					require.Equal(t, "image/png", part.Source.MediaType)
					images = append(images, part.Source.Data)
				}
			}
		}
	}
	return images
}

func TestViewImageReachesModel(t *testing.T) {
	t.Parallel()

	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	img.Set(1, 1, color.RGBA{G: 200, A: 255})
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	pngData := buf.Bytes()
	encoded := base64.StdEncoding.EncodeToString(pngData)

	db, ps := dbtestutil.NewDB(t)
	ctx := testutil.Context(t, testutil.WaitLong)

	var (
		mu       sync.Mutex
		requests []*chattest.AnthropicRequest
	)
	anthropicURL := chattest.NewAnthropic(t, func(req *chattest.AnthropicRequest) chattest.AnthropicResponse {
		if !req.Stream {
			return chattest.AnthropicNonStreamingResponse("title")
		}
		mu.Lock()
		requests = append(requests, req)
		n := len(requests)
		mu.Unlock()
		if n == 1 {
			return chattest.AnthropicStreamingResponse(chattest.AnthropicToolCallChunks("view_image", `{"path":"/home/coder/shot.png"}`)...)
		}
		return chattest.AnthropicStreamingResponse(chattest.AnthropicTextChunks("a green pixel")...)
	})
	user, org, model := seedAnthropicChatDependencies(t, db, anthropicURL)
	ws, dbAgent := seedWorkspaceWithAgent(t, db, user.ID)

	ctrl := gomock.NewController(t)
	mockConn := agentconnmock.NewMockAgentConn(ctrl)
	// Declared before the generic expectations so it matches first.
	mockConn.EXPECT().ReadFile(gomock.Any(), "/home/coder/shot.png", int64(0), gomock.Any()).
		DoAndReturn(func(context.Context, string, int64, int64) (io.ReadCloser, string, error) {
			return io.NopCloser(bytes.NewReader(pngData)), "", nil
		}).Times(1)
	setupToolExecutionAgentConn(t, mockConn)

	server := newActiveTestServer(t, db, ps, func(cfg *chatd.Config) {
		cfg.AIBridgeTransportFactory = chatAIGatewayTransportFactoryPointer(chattest.NewMockAIBridgeTransport(t, anthropicURL, chattest.WithPreservePath()))
		cfg.Experiments = codersdk.Experiments{codersdk.ExperimentForkViewImage}
		cfg.AgentConn = func(context.Context, uuid.UUID) (workspacesdk.AgentConn, func(), error) {
			return mockConn, func() {}, nil
		}
	})
	chat, err := server.CreateChat(ctx, chatd.CreateOptions{
		OrganizationID:     org.ID,
		OwnerID:            user.ID,
		WorkspaceID:        uuid.NullUUID{UUID: ws.ID, Valid: true},
		AgentID:            uuid.NullUUID{UUID: dbAgent.ID, Valid: true},
		Title:              "view-image",
		ModelConfigID:      model.ID,
		InitialUserContent: []codersdk.ChatMessagePart{codersdk.ChatMessageText("look at the screenshot")},
	})
	require.NoError(t, err)
	waitForChatStatus(ctx, t, db, chat.ID, database.ChatStatusWaiting)

	_, err = server.SendMessage(ctx, chatd.SendMessageOptions{
		ChatID:        chat.ID,
		CreatedBy:     user.ID,
		Content:       []codersdk.ChatMessagePart{codersdk.ChatMessageText("what color was it?")},
		ModelConfigID: model.ID,
	})
	require.NoError(t, err)
	waitForChatStatus(ctx, t, db, chat.ID, database.ChatStatusWaiting)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, requests, 3)
	require.True(t, slices.ContainsFunc(requests[0].Tools, func(tool chattest.AnthropicRequestTool) bool {
		return tool.Name == "view_image"
	}), "the model gets the view_image tool")
	require.Empty(t, anthropicToolResultImages(t, requests[0]))
	require.Equal(t, []string{encoded}, anthropicToolResultImages(t, requests[1]), "the step after the call sees the image")
	require.Equal(t, []string{encoded}, anthropicToolResultImages(t, requests[2]), "the next turn still sees the image")

	var stored bool
	for _, message := range chatMessages(ctx, t, db, chat.ID) {
		if message.Role != database.ChatMessageRoleTool {
			continue
		}
		var parts []struct {
			ToolName string `json:"tool_name"`
			IsMedia  bool   `json:"is_media"`
			Result   struct {
				Data     string `json:"data"`
				MimeType string `json:"mime_type"`
			} `json:"result"`
		}
		require.NoError(t, json.Unmarshal(message.Content.RawMessage, &parts))
		for _, part := range parts {
			if part.ToolName == "view_image" {
				stored = part.IsMedia && part.Result.Data == encoded && part.Result.MimeType == "image/png"
			}
		}
	}
	require.True(t, stored, "the image is stored as a media tool result")
}
