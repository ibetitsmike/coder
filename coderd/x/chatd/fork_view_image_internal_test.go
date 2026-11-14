package chatd

import (
	"bytes"
	"context"
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
	"github.com/sqlc-dev/pqtype"
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

// testWebP returns a lossless WebP file with a width x height canvas. The tool
// checks the header and the chunks, not the lossless bitstream.
func testWebP(width, height uint32) []byte {
	chunk := make([]byte, 10)
	chunk[0] = 0x2f
	binary.LittleEndian.PutUint32(chunk[1:], (width-1)|(height-1)<<14)
	return testRIFF("VP8L", chunk)
}

// testRIFF returns a WebP file with one chunk.
func testRIFF(name string, chunk []byte) []byte {
	data := []byte("RIFF\x00\x00\x00\x00WEBP" + name)
	data = binary.LittleEndian.AppendUint32(data, uint32(len(chunk))) //nolint:gosec // Test chunks are small.
	data = append(data, chunk...)
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8)) //nolint:gosec // Test files are small.
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
		"/shot.txt":     pngData,
		"/photo.jpg":    jpegBuf.Bytes(),
		"/anim.gif":     gifBuf.Bytes(),
		"/pic.webp":     testWebP(9, 10),
		"/notes.md":     []byte("# notes\nnot an image\n"),
		"/big.png":      bigPNG,
		"/wide.png":     testPNG(t, viewImageMaxDimension+1, 1),
		"/empty.png":    {},
		"/bad.png":      append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 40)...),
		"/ spaced.png ": pngData, //nolint:gocritic // The test needs a path with outer spaces.
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
		require.Contains(t, resp.Content, "8001x1 pixels, and view_image accepts at most 8000 pixels per side")
	})

	t.Run("InvalidImages", func(t *testing.T) {
		t.Parallel()
		require.Contains(t, runViewImage(t, anthropic, conn, "/empty.png").Content, "is empty")
		require.Contains(t, runViewImage(t, anthropic, conn, "/bad.png").Content, "is not a valid image/png image")
		require.Contains(t, runViewImage(t, anthropic, conn, "/missing.png").Content, "file not found")
		require.Contains(t, runViewImage(t, anthropic, conn, " ").Content, "path is required")
	})

	t.Run("KeepsPathWhitespace", func(t *testing.T) {
		t.Parallel()
		resp := runViewImage(t, anthropic, conn, "/ spaced.png ")
		require.False(t, resp.IsError, resp.Content)
		require.Equal(t, pngData, resp.Data)
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

func TestViewImageCustomAgentPolicy(t *testing.T) {
	t.Parallel()

	builtin := newTestAgentTool(viewImageToolName)
	// A caller-supplied tool that takes the name while fork-view-image is off.
	dynamic := &dynamicTool{name: viewImageToolName}
	for _, tt := range []struct {
		name   string
		policy customAgentToolPolicy
		want   []string
	}{
		{name: "ReadOnly", policy: customAgentToolPolicy{ReadOnly: true}, want: []string{"builtin"}},
		{name: "ReadAlias", policy: customAgentToolPolicy{Tools: []string{"Read"}}, want: []string{"builtin"}},
		{name: "Named", policy: customAgentToolPolicy{Tools: []string{viewImageToolName}}, want: []string{"builtin", "dynamic"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got []string
			for kind, tool := range map[string]fantasy.AgentTool{"builtin": builtin, "dynamic": dynamic} {
				if kept, _, _ := tt.policy.filter([]fantasy.AgentTool{tool}, nil, nil, nil); len(kept) == 1 {
					got = append(got, kind)
				}
			}
			require.ElementsMatch(t, tt.want, got)
		})
	}
}

func TestViewImageToolGate(t *testing.T) {
	t.Parallel()

	model := routedModel("anthropic", "anthropic")
	for _, tt := range []struct {
		name        string
		experiments codersdk.Experiments
		dynamic     bool
		want        []string
	}{
		{name: "Off", want: []string{}},
		{name: "On", experiments: codersdk.Experiments{codersdk.ExperimentForkViewImage}, want: []string{viewImageToolName}},
		// appendDynamicTools adds a caller-supplied view_image, not appendForkTools.
		{name: "OffWithDynamicTool", dynamic: true, want: []string{}},
		{name: "OnWithDynamicTool", experiments: codersdk.Experiments{codersdk.ExperimentForkViewImage}, dynamic: true, want: []string{viewImageToolName}},
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
			if tt.dynamic {
				chat.DynamicTools = pqtype.NullRawMessage{RawMessage: json.RawMessage(`[{"name":"view_image"}]`), Valid: true}
			}
			tools, err := server.appendForkTools(ctx, nil, chat, chat.LastModelConfigID, forkToolsWithModel(model))
			require.NoError(t, err)
			require.Equal(t, tt.want, toolNames(tools))
		})
	}
}
