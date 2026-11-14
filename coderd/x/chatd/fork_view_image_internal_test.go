package chatd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"slices"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/sqlc-dev/pqtype"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
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

// testPNGHeader returns a PNG with a valid IHDR and IEND chunk and no image
// data, so its structure checks out but it does not decode.
func testPNGHeader(width, height uint32) []byte {
	data := []byte("\x89PNG\r\n\x1a\n")
	chunk := func(name string, body []byte) {
		data = binary.BigEndian.AppendUint32(data, uint32(len(body))) //nolint:gosec // Test chunks are small.
		start := len(data)
		data = append(data, name...)
		data = append(data, body...)
		data = binary.BigEndian.AppendUint32(data, crc32.ChecksumIEEE(data[start:]))
	}
	ihdr := binary.BigEndian.AppendUint32(nil, width)
	ihdr = binary.BigEndian.AppendUint32(ihdr, height)
	chunk("IHDR", append(ihdr, 8, 6, 0, 0, 0))
	chunk("IEND", nil)
	return data
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
	// Noise gives the JPEG and GIF scans data to cut.
	noisy := image.NewPaletted(image.Rect(0, 0, 32, 32), color.Palette{color.Black, color.White})
	for i := range noisy.Pix {
		noisy.Pix[i] = uint8(i * 7 % 3 % 2) //nolint:gosec // The value is 0 or 1.
	}
	var noisyJPEG, noisyGIF bytes.Buffer
	require.NoError(t, jpeg.Encode(&noisyJPEG, noisy, nil))
	require.NoError(t, gif.Encode(&noisyGIF, noisy, nil))
	var animatedGIF bytes.Buffer
	require.NoError(t, gif.EncodeAll(&animatedGIF, &gif.GIF{Image: []*image.Paletted{noisy, noisy}, Delay: []int{10, 10}}))
	anthropicLimit := codersdk.AnthropicInlineImageCapBytes / 4 * 3
	// A valid PNG header padded past the limit.
	bigPNG := append(testPNG(t, 2, 2), make([]byte, anthropicLimit)...)
	badCRC := bytes.Clone(pngData)
	badCRC[len(badCRC)-13] ^= 0xff // The last byte of the IDAT chunk's CRC.
	conn := viewImageConn(t, map[string][]byte{
		"/shot.txt":     pngData,
		"/photo.jpg":    jpegBuf.Bytes(),
		"/anim.gif":     gifBuf.Bytes(),
		"/pic.webp":     testWebP(9, 10),
		"/notes.md":     []byte("# notes\nnot an image\n"),
		"/big.png":      bigPNG,
		"/wide.png":     testPNG(t, viewImageMaxDimension+1, 1),
		"/huge.png":     testPNGHeader(6000, 5000),
		"/empty.png":    {},
		"/bad.png":      append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 40)...),
		"/ spaced.png ": pngData, //nolint:gocritic // The test needs a path with outer spaces.
		// Each header decodes, but the rest of the file is missing or damaged.
		"/cut.png": pngData[:len(pngData)-4],
		"/crc.png": badCRC,
		"/cut.jpg": jpegBuf.Bytes()[:jpegBuf.Len()-2],
		"/cut.gif": gifBuf.Bytes()[:gifBuf.Len()-1],
		// The structure checks out, but the image data is missing or cut.
		"/noidat.png": testPNGHeader(4, 3),
		"/scan.jpg":   append(bytes.Clone(noisyJPEG.Bytes()[:noisyJPEG.Len()-40]), 0xff, 0xd9),
		"/frame.gif":  append(bytes.Clone(noisyGIF.Bytes()[:noisyGIF.Len()-20]), 0x3b),
		"/moving.gif": animatedGIF.Bytes(),
		// The RIFF size names 2 bytes past the end of the file.
		"/cut.webp": func() []byte {
			data := testWebP(9, 10)
			binary.LittleEndian.PutUint32(data[4:8], 24)
			return data
		}(),
		// The VP8L chunk claims 2 bytes past the RIFF end.
		"/chunk.webp": func() []byte {
			data := testWebP(9, 10)
			binary.LittleEndian.PutUint32(data[16:20], 12)
			return data
		}(),
		// A key frame whose tag names a 100 byte first partition.
		"/vp8.webp": testRIFF("VP8 ", []byte{100 << 5 & 0xff, 100 >> 3, 0, 0x9d, 0x01, 0x2a, 9, 0, 10, 0}),
		// An extended header with no image chunk after it.
		"/noimage.webp": testRIFF("VP8X", make([]byte, 10)),
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
		resp = runViewImage(t, anthropic, conn, "/huge.png")
		require.True(t, resp.IsError)
		require.Contains(t, resp.Content, "6000x5000 pixels, and view_image accepts at most 25000000 pixels in total")
	})

	t.Run("InvalidImages", func(t *testing.T) {
		t.Parallel()
		require.Contains(t, runViewImage(t, anthropic, conn, "/empty.png").Content, "is empty")
		require.Contains(t, runViewImage(t, anthropic, conn, "/bad.png").Content, "is not a valid image/png image")
		require.Contains(t, runViewImage(t, anthropic, conn, "/missing.png").Content, "file not found")
		require.Contains(t, runViewImage(t, anthropic, conn, " ").Content, "path is required")
		for path, want := range map[string]string{
			"/cut.png":      "truncated",
			"/crc.png":      `bad CRC in chunk "IDAT"`,
			"/cut.jpg":      "truncated before the end of image marker",
			"/cut.gif":      "truncated before the GIF trailer",
			"/cut.webp":     "shorter than its RIFF size",
			"/chunk.webp":   `chunk "VP8L" is longer than the file`,
			"/vp8.webp":     "VP8 chunk is shorter than its first partition",
			"/noimage.webp": "no image data",
			"/noidat.png":   "png: invalid format",
			"/scan.jpg":     "invalid JPEG format",
			"/frame.gif":    "gif: not enough image data",
		} {
			resp := runViewImage(t, openai, conn, path)
			require.True(t, resp.IsError, path)
			require.Contains(t, resp.Content, "is not a valid image/", path)
			require.Contains(t, resp.Content, want, path)
		}
	})

	t.Run("KeepsPathWhitespace", func(t *testing.T) {
		t.Parallel()
		resp := runViewImage(t, anthropic, conn, "/ spaced.png ")
		require.False(t, resp.IsError, resp.Content)
		require.Equal(t, pngData, resp.Data)
	})

	t.Run("AnimatedGIF", func(t *testing.T) {
		t.Parallel()
		resp := runViewImage(t, anthropic, conn, "/moving.gif")
		require.True(t, resp.IsError)
		require.Contains(t, resp.Content, "/moving.gif is an animated GIF with 2 frames")
		frames, err := gifFrames(noisyGIF.Bytes())
		require.NoError(t, err)
		require.Equal(t, 1, frames)
	})

	t.Run("CallsPerResponse", func(t *testing.T) {
		t.Parallel()
		// One tool serves one step, so its calls are one response's calls.
		tool := viewImageTool(openai, func(context.Context) (workspacesdk.AgentConn, error) { return conn, nil })
		for i := range maxViewedImagesInPrompt + 1 {
			resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "call", Name: viewImageToolName, Input: `{"path":"/shot.txt"}`})
			require.NoError(t, err)
			if i < maxViewedImagesInPrompt {
				require.False(t, resp.IsError, resp.Content)
				continue
			}
			require.True(t, resp.IsError)
			require.Contains(t, resp.Content, "view_image runs at most 8 times per response")
		}
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

		out := limitViewedImages(messages, nil, nil)
		require.True(t, isMedia(out[1]), "other tools keep their media")
		for i := range maxViewedImagesInPrompt + 2 {
			result := out[3+2*i]
			if i < 2 {
				require.False(t, isMedia(result), "old image %d is dropped", i)
				text := result.Content[0].(fantasy.ToolResultPart).Output.(fantasy.ToolResultOutputContentText).Text
				require.Contains(t, text, "Call view_image again")
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
		out := limitViewedImages(messages, nil, nil)
		require.False(t, isMedia(out[1]))
		require.False(t, isMedia(out[3]), "two halves exceed the byte bound")
		require.True(t, isMedia(out[5]))

		// The newest image always stays, even over the bound.
		huge := turn("d", viewImageToolName, media(maxViewedImageBytesInPrompt+1, ""))
		require.True(t, isMedia(limitViewedImages(huge, nil, nil)[1]))
	})

	t.Run("NoViewImage", func(t *testing.T) {
		t.Parallel()
		messages := turn("shot", "computer", media(10, ""))
		out := limitViewedImages(messages, nil, nil)
		require.Same(t, &messages[0], &out[0], "the prompt is returned as is")
	})

	t.Run("OtherMediaTakesBudget", func(t *testing.T) {
		t.Parallel()
		other := maxViewedImageBytesInPrompt - 15
		for name, first := range map[string][]fantasy.Message{
			"Screenshot": turn("shot", "computer", media(other, "")),
			"Attachment": {{Role: fantasy.MessageRoleUser, Content: []fantasy.MessagePart{fantasy.FilePart{Data: make([]byte, other), MediaType: "image/png"}}}},
		} {
			messages := slices.Concat(first, turn("a", viewImageToolName, media(10, "")), turn("b", viewImageToolName, media(10, "")))
			out := limitViewedImages(messages, nil, nil)
			n := len(first)
			require.False(t, isMedia(out[n+1]), "%s: the older image does not fit next to the other media", name)
			require.True(t, isMedia(out[n+3]), "%s: the newest image stays", name)
		}
	})

	t.Run("PendingMediaTakesBudget", func(t *testing.T) {
		t.Parallel()
		// The unanswered user tail is appended after the bound, so its
		// attachments count here.
		pending := []fantasy.Message{{Role: fantasy.MessageRoleUser, Content: []fantasy.MessagePart{fantasy.FilePart{Data: make([]byte, maxViewedImageBytesInPrompt-15), MediaType: "image/png"}}}}
		messages := append(turn("a", viewImageToolName, media(10, "")), turn("b", viewImageToolName, media(10, ""))...)
		out := limitViewedImages(messages, pending, nil)
		require.Len(t, out, len(messages))
		require.False(t, isMedia(out[1]), "the older image does not fit next to the pending attachment")
		require.True(t, isMedia(out[3]), "the newest image stays")
		require.True(t, isMedia(limitViewedImages(messages, nil, nil)[1]), "without the tail both images fit")
	})

	t.Run("OtherImagesTakeCount", func(t *testing.T) {
		t.Parallel()
		// Anthropic tightens its limits above 20 images in a request.
		var messages []fantasy.Message
		for range maxImagesInPrompt - 3 {
			messages = append(messages, fantasy.Message{Role: fantasy.MessageRoleUser, Content: []fantasy.MessagePart{fantasy.FilePart{Data: make([]byte, 10), MediaType: "image/png"}}})
		}
		// A PDF is not an image and takes no share of the count.
		messages = append(messages, fantasy.Message{Role: fantasy.MessageRoleUser, Content: []fantasy.MessagePart{fantasy.FilePart{Data: make([]byte, 10), MediaType: "application/pdf"}}})
		n := len(messages)
		for i := range 5 {
			messages = append(messages, turn("view"+string(rune('a'+i)), viewImageToolName, media(10, ""))...)
		}
		out := limitViewedImages(messages, nil, nil)
		for i := range 5 {
			require.Equal(t, i >= 2, isMedia(out[n+2*i+1]), "viewed image %d", i)
		}
	})

	t.Run("ReusedCallID", func(t *testing.T) {
		t.Parallel()
		// A later computer call reuses a view_image call's ID. Its
		// screenshot is not a viewed image and stays.
		messages := append(turn("x", viewImageToolName, media(10, "")), turn("x", "computer", media(10, ""))...)
		for i := range maxViewedImagesInPrompt {
			messages = append(messages, turn("view"+string(rune('a'+i)), viewImageToolName, media(10, ""))...)
		}
		out := limitViewedImages(messages, nil, nil)
		require.False(t, isMedia(out[1]), "the oldest viewed image is dropped")
		require.True(t, isMedia(out[3]), "the computer screenshot stays")
	})

	t.Run("ModelSizeLimit", func(t *testing.T) {
		t.Parallel()
		// Viewed on a model without an inline cap, then the chat switched to
		// Anthropic, whose limit applies to the base64 form.
		limit := codersdk.AnthropicInlineImageCapBytes / 4 * 3
		messages := append(turn("a", viewImageToolName, media(10, "")), turn("b", viewImageToolName, media(limit+1, "too big"))...)
		anthropic := routedModel("anthropic", "anthropic")
		out := limitViewedImages(messages, nil, &anthropic)
		require.True(t, isMedia(out[1]), "the older image fits and stays")
		require.False(t, isMedia(out[3]))
		text := out[3].Content[0].(fantasy.ToolResultPart).Output.(fantasy.ToolResultOutputContentText).Text
		require.Contains(t, text, "larger than the view_image limit of 3932160 bytes for this model")
		require.Contains(t, text, "too big")

		openai := routedModel("openai", "openai")
		out = limitViewedImages(messages, nil, &openai)
		require.True(t, isMedia(out[1]))
		require.True(t, isMedia(out[3]))
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
		planMode    bool
		want        []string
		bounded     bool
	}{
		{name: "Off", want: []string{}, bounded: true},
		{name: "On", experiments: codersdk.Experiments{codersdk.ExperimentForkViewImage}, want: []string{viewImageToolName}, bounded: true},
		// A caller-supplied view_image owns the name, so its results stay.
		{name: "OffWithDynamicTool", dynamic: true, want: []string{}, bounded: false},
		{name: "OnWithDynamicTool", experiments: codersdk.Experiments{codersdk.ExperimentForkViewImage}, dynamic: true, want: []string{viewImageToolName}, bounded: true},
		// Plan mode offers no dynamic tools, so the results are the built-in's.
		{name: "OffWithDynamicToolInPlanMode", dynamic: true, planMode: true, want: []string{}, bounded: true},
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
			if tt.dynamic {
				chat.DynamicTools = pqtype.NullRawMessage{RawMessage: json.RawMessage(`[{"name":"view_image"}]`), Valid: true}
			}
			if tt.planMode {
				chat.PlanMode = database.NullChatPlanMode{ChatPlanMode: database.ChatPlanModePlan, Valid: true}
			}
			original := prompt
			tools, err := server.appendForkTools(ctx, nil, chat, chat.LastModelConfigID, forkToolsWithModel(model, &prompt, nil))
			require.NoError(t, err)
			require.Equal(t, tt.want, toolNames(tools))
			_, isText := prompt[1].Content[0].(fantasy.ToolResultPart).Output.(fantasy.ToolResultOutputContentText)
			require.Equal(t, tt.bounded, isText, "the prompt bound in case %s", tt.name)
			// A compaction override model gets the same bound.
			compaction := server.limitViewedImagesForModel(chat, original, model)
			_, isText = compaction[1].Content[0].(fantasy.ToolResultPart).Output.(fantasy.ToolResultOutputContentText)
			require.Equal(t, tt.bounded, isText, "the compaction bound in case %s", tt.name)
		})
	}
}
