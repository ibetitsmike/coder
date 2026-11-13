package chatd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
	"github.com/coder/coder/v2/coderd/x/chatd/chattest"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk/agentconnmock"
)

func testPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

// testWebP returns a lossless WebP header for a width x height canvas. The
// tool reads only the header.
func testWebP(width, height uint32) []byte {
	chunk := make([]byte, 5)
	chunk[0] = 0x2f
	binary.LittleEndian.PutUint32(chunk[1:], (width-1)|(height-1)<<14)
	data := []byte("RIFF\x00\x00\x00\x00WEBPVP8L")
	data = binary.LittleEndian.AppendUint32(data, 5)
	data = append(data, chunk...)
	data = append(data, make([]byte, 16)...)
	// RIFF size: 4 (WEBP) + 8 (chunk header) + 5 (chunk) + 16 (padding).
	binary.LittleEndian.PutUint32(data[4:8], 33)
	return data
}

func viewImageConn(t *testing.T, files map[string][]byte) workspacesdk.AgentConn {
	t.Helper()
	conn := agentconnmock.NewMockAgentConn(gomock.NewController(t))
	conn.EXPECT().ReadFile(gomock.Any(), gomock.Any(), int64(0), gomock.Any()).
		DoAndReturn(func(_ context.Context, path string, _, limit int64) (io.ReadCloser, string, error) {
			data, ok := files[path]
			if !ok {
				return nil, "", xerrors.New("file not found")
			}
			if int64(len(data)) > limit {
				data = data[:limit]
			}
			return io.NopCloser(bytes.NewReader(data)), "", nil
		}).AnyTimes()
	return conn
}

func runViewImage(t *testing.T, model chatprovider.Model, conn workspacesdk.AgentConn, path string) fantasy.ToolResponse {
	t.Helper()
	tool := viewImageTool(model, func(context.Context) (workspacesdk.AgentConn, error) { return conn, nil })
	input, err := json.Marshal(viewImageArgs{Path: path})
	require.NoError(t, err)
	resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "call_1", Name: viewImageToolName, Input: string(input)})
	require.NoError(t, err)
	return resp
}

func TestViewImageTool(t *testing.T) {
	t.Parallel()

	anthropic := routedModel("anthropic", "anthropic")
	openai := routedModel("openai", "openai")
	google := routedModel("openai-compat", "google")
	pngData := testPNG(t, 4, 3)
	var jpegBuf, gifBuf bytes.Buffer
	require.NoError(t, jpeg.Encode(&jpegBuf, image.NewRGBA(image.Rect(0, 0, 5, 6)), nil))
	require.NoError(t, gif.Encode(&gifBuf, image.NewPaletted(image.Rect(0, 0, 7, 8), color.Palette{color.Black}), nil))
	anthropicLimit := codersdk.AnthropicInlineImageCapBytes / 4 * 3
	// A valid PNG header padded past the limit.
	bigPNG := append(testPNG(t, 2, 2), make([]byte, anthropicLimit)...)
	conn := viewImageConn(t, map[string][]byte{
		"/shot.txt":  pngData,
		"/photo.jpg": jpegBuf.Bytes(),
		"/anim.gif":  gifBuf.Bytes(),
		"/pic.webp":  testWebP(9, 10),
		"/notes.md":  []byte("# notes\nnot an image\n"),
		"/big.png":   bigPNG,
		"/wide.png":  testPNG(t, viewImageMaxDimension+1, 1),
		"/empty.png": {},
		"/bad.png":   append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 40)...),
	})

	t.Run("DetectsTypeByContent", func(t *testing.T) {
		t.Parallel()
		resp := runViewImage(t, anthropic, conn, "/shot.txt")
		require.False(t, resp.IsError, resp.Content)
		require.Equal(t, "image", resp.Type)
		require.Equal(t, "image/png", resp.MediaType)
		require.Equal(t, pngData, resp.Data)
		require.Contains(t, resp.Content, "4x3 pixels")
	})

	t.Run("Formats", func(t *testing.T) {
		t.Parallel()
		for path, want := range map[string]string{
			"/photo.jpg": "image/jpeg 5x6",
			"/anim.gif":  "image/gif 7x8",
			"/pic.webp":  "image/webp 9x10",
		} {
			resp := runViewImage(t, openai, conn, path)
			require.False(t, resp.IsError, resp.Content)
			mediaType, size, _ := strings.Cut(want, " ")
			require.Equal(t, mediaType, resp.MediaType, path)
			require.Contains(t, resp.Content, size+" pixels", path)
		}
	})

	t.Run("NonImageFile", func(t *testing.T) {
		t.Parallel()
		resp := runViewImage(t, anthropic, conn, "/notes.md")
		require.True(t, resp.IsError)
		require.Contains(t, resp.Content, "is text/plain, not a PNG, JPEG, GIF or WebP image")
	})

	t.Run("SizeLimit", func(t *testing.T) {
		t.Parallel()
		resp := runViewImage(t, anthropic, conn, "/big.png")
		require.True(t, resp.IsError)
		require.Contains(t, resp.Content, "larger than 3932160 bytes")
		require.Contains(t, resp.Content, "5242880 byte per-image limit")

		// Without an inline cap the tool media limit applies.
		resp = runViewImage(t, openai, conn, "/big.png")
		require.False(t, resp.IsError, resp.Content)
		limit, reason := viewImageSizeLimit(openai)
		require.Equal(t, codersdk.MaxChatFileSizeBytes, limit)
		require.Equal(t, "the chat tool media limit", reason)
	})

	t.Run("DimensionLimit", func(t *testing.T) {
		t.Parallel()
		resp := runViewImage(t, anthropic, conn, "/wide.png")
		require.True(t, resp.IsError)
		require.Contains(t, resp.Content, "8001x1 pixels; view_image accepts at most 8000 pixels per side")
	})

	t.Run("InvalidImages", func(t *testing.T) {
		t.Parallel()
		require.Contains(t, runViewImage(t, anthropic, conn, "/empty.png").Content, "is empty")
		require.Contains(t, runViewImage(t, anthropic, conn, "/bad.png").Content, "is not a valid image/png image")
		require.Contains(t, runViewImage(t, anthropic, conn, "/missing.png").Content, "file not found")
		require.Contains(t, runViewImage(t, anthropic, conn, " ").Content, "path is required")
	})

	t.Run("ModelWithoutImageInput", func(t *testing.T) {
		t.Parallel()
		for _, model := range []chatprovider.Model{
			routedModel("openai", "copilot"),
			chatprovider.NewModel(&chattest.FakeModel{ProviderName: "fake", ModelName: "text-only"}, nil),
		} {
			resp := runViewImage(t, model, conn, "/shot.txt")
			require.True(t, resp.IsError)
			require.Contains(t, resp.Content, "does not accept image input")
			require.Contains(t, resp.Content, `model "`+model.ModelID()+`" on provider "`+model.RouteProvider()+`"`)
		}
	})

	t.Run("RouteRejectsType", func(t *testing.T) {
		t.Parallel()
		resp := runViewImage(t, google, conn, "/anim.gif")
		require.True(t, resp.IsError)
		require.Contains(t, resp.Content, `on provider "google" does not accept image/gif images`)
		require.False(t, runViewImage(t, google, conn, "/shot.txt").IsError)
	})
}

func TestLimitViewedImages(t *testing.T) {
	t.Parallel()

	media := func(size int, text string) fantasy.ToolResultOutputContent {
		return fantasy.ToolResultOutputContentMedia{
			Data:      base64.StdEncoding.EncodeToString(make([]byte, size)),
			MediaType: "image/png",
			Text:      text,
		}
	}
	turn := func(id, tool string, output fantasy.ToolResultOutputContent) []fantasy.Message {
		return []fantasy.Message{
			{Role: fantasy.MessageRoleAssistant, Content: []fantasy.MessagePart{fantasy.ToolCallPart{ToolCallID: id, ToolName: tool}}},
			{Role: fantasy.MessageRoleTool, Content: []fantasy.MessagePart{fantasy.ToolResultPart{ToolCallID: id, Output: output}}},
		}
	}
	isMedia := func(msg fantasy.Message) bool {
		_, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentMedia](msg.Content[0].(fantasy.ToolResultPart).Output)
		return ok
	}

	t.Run("KeepsNewestByCount", func(t *testing.T) {
		t.Parallel()
		var messages []fantasy.Message
		// A computer-use screenshot is not a view_image result and stays.
		messages = append(messages, turn("shot", "computer", media(10, "screenshot"))...)
		for i := range maxViewedImagesInPrompt + 2 {
			messages = append(messages, turn("view"+string(rune('a'+i)), viewImageToolName, media(10, "image "+string(rune('a'+i))))...)
		}
		before, err := json.Marshal(messages)
		require.NoError(t, err)

		out := limitViewedImages(messages)
		require.True(t, isMedia(out[1]), "other tools keep their media")
		for i := range maxViewedImagesInPrompt + 2 {
			result := out[3+2*i]
			if i < 2 {
				require.False(t, isMedia(result), "old image %d is dropped", i)
				text := result.Content[0].(fantasy.ToolResultPart).Output.(fantasy.ToolResultOutputContentText).Text
				require.Contains(t, text, "call view_image again")
				require.Contains(t, text, "image "+string(rune('a'+i)))
				continue
			}
			require.True(t, isMedia(result), "new image %d is kept", i)
		}
		after, err := json.Marshal(messages)
		require.NoError(t, err)
		require.JSONEq(t, string(before), string(after), "the input is unchanged")
	})

	t.Run("KeepsNewestByBytes", func(t *testing.T) {
		t.Parallel()
		big := maxViewedImageBytesInPrompt/2 + 1
		messages := append(turn("a", viewImageToolName, media(big, "")), turn("b", viewImageToolName, media(big, ""))...)
		messages = append(messages, turn("c", viewImageToolName, media(big, ""))...)
		out := limitViewedImages(messages)
		require.False(t, isMedia(out[1]))
		require.False(t, isMedia(out[3]), "two halves exceed the byte bound")
		require.True(t, isMedia(out[5]))

		// The newest image always stays, even over the bound.
		huge := turn("d", viewImageToolName, media(maxViewedImageBytesInPrompt+1, ""))
		require.True(t, isMedia(limitViewedImages(huge)[1]))
	})

	t.Run("NoViewImage", func(t *testing.T) {
		t.Parallel()
		messages := turn("shot", "computer", media(10, ""))
		out := limitViewedImages(messages)
		require.Same(t, &messages[0], &out[0], "the prompt is returned as is")
	})
}

func TestViewImageToolGate(t *testing.T) {
	t.Parallel()

	model := routedModel("anthropic", "anthropic")
	for _, tt := range []struct {
		name        string
		experiments codersdk.Experiments
		want        []string
	}{
		{name: "Off", want: []string{}},
		{name: "On", experiments: codersdk.Experiments{codersdk.ExperimentForkViewImage}, want: []string{viewImageToolName}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db, ps := dbtestutil.NewDB(t)
			server := newInternalTestServer(t, db, ps, chatprovider.ProviderAPIKeys{}, withInternalTestServerExperiments(tt.experiments))
			ctx := chatdTestContext(t)
			user, org, modelConfig := seedInternalChatDeps(t, db)
			chat, err := server.CreateChat(ctx, CreateOptions{OrganizationID: org.ID, OwnerID: user.ID, Title: "view", ModelConfigID: modelConfig.ID, InitialUserContent: []codersdk.ChatMessagePart{codersdk.ChatMessageText("hello")}})
			require.NoError(t, err)
			require.NoError(t, WaitUntilIdleForTest(ctx, server))

			var prompt []fantasy.Message
			for i := range maxViewedImagesInPrompt + 1 {
				id := string(rune('a' + i))
				prompt = append(prompt,
					fantasy.Message{Role: fantasy.MessageRoleAssistant, Content: []fantasy.MessagePart{fantasy.ToolCallPart{ToolCallID: id, ToolName: viewImageToolName}}},
					fantasy.Message{Role: fantasy.MessageRoleTool, Content: []fantasy.MessagePart{fantasy.ToolResultPart{ToolCallID: id, Output: fantasy.ToolResultOutputContentMedia{Data: "AAAA", MediaType: "image/png"}}}},
				)
			}
			tools, err := server.appendForkTools(ctx, nil, chat, chat.LastModelConfigID, forkToolsWithModel(model, &prompt))
			require.NoError(t, err)
			require.Equal(t, tt.want, toolNames(tools))
			_, isText := prompt[1].Content[0].(fantasy.ToolResultPart).Output.(fantasy.ToolResultOutputContentText)
			require.True(t, isText, "the prompt is bounded with the experiment %s", strings.ToLower(tt.name))
		})
	}
}
