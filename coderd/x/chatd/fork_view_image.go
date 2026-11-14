package chatd

// This file is fork code: it holds the view_image tool of the fork-view-image
// experiment and the bound on how many viewed images stay in the prompt.
// appendForkTools adds the tool to a turn and applies the bound.

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"slices"
	"strings"
	"sync/atomic"

	"charm.land/fantasy"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
	"github.com/coder/coder/v2/coderd/x/chatfiles"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

const (
	viewImageToolName = "view_image"
	// viewImageMaxDimension is Anthropic's documented limit on either side of
	// an image. Larger images fail the request, and a stored image would fail
	// every later request too, so the tool rejects them up front.
	viewImageMaxDimension = 8000
	// viewImageMaxPixels bounds the memory of the full decode in
	// imageDecodes. The standard library decoders hold the whole image, up
	// to 8 bytes per pixel for a 16-bit PNG, so one decode takes at most
	// 200 MB, and viewImageDecodeSlot runs one at a time.
	viewImageMaxPixels = 25_000_000
	// maxViewedImagesInPrompt and maxViewedImageBytesInPrompt bound the
	// view_image results a prompt carries. Older images become a text note.
	// Anthropic lowers the per-image limits when a request holds more than
	// maxImagesInPrompt images, tool results included, so viewed images only
	// fill what the prompt's other images leave. Bedrock accepts 20 MB per
	// request and base64 adds a third, so 10 MiB of images leaves room for
	// the rest of the request.
	maxViewedImagesInPrompt     = 8
	maxImagesInPrompt           = 20
	maxViewedImageBytesInPrompt = 10 << 20
)

// viewImageMediaTypes are the image types view_image returns.
var viewImageMediaTypes = []string{"image/png", "image/jpeg", "image/gif", "image/webp"}

// viewImageDecodeSlot lets one view_image decode run at a time on this
// server.
var viewImageDecodeSlot = make(chan struct{}, 1)

type viewImageArgs struct {
	Path string `json:"path" description:"Path of a PNG, JPEG, GIF or WebP file in the workspace. Prefer an absolute path."`
}

// viewImageTool returns the view_image tool, which loads a raster image from
// the workspace into the model's context as an image tool result.
func viewImageTool(model chatprovider.Model, getConn func(context.Context) (workspacesdk.AgentConn, error)) fantasy.AgentTool {
	// buildTurnEnvironment builds the tools for each step, so calls counts
	// the calls of one model response, which chatloop runs concurrently.
	var calls atomic.Int64
	return fantasy.NewAgentTool(
		viewImageToolName,
		"Look at a PNG, JPEG, GIF or WebP image file from the workspace, such as a screenshot. "+
			"The image is added to your context so you can see it. "+
			"Use read_file for text files. Only the newest viewed images stay in context, so call view_image again to see an older one.",
		func(ctx context.Context, args viewImageArgs, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			// read_file passes the path as given, and so does view_image: a
			// file name may start or end with whitespace.
			path := args.Path
			if strings.TrimSpace(path) == "" {
				return fantasy.NewTextErrorResponse("path is required (use an absolute path, e.g. /home/coder/screenshot.png)"), nil
			}
			if calls.Add(1) > maxViewedImagesInPrompt {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("view_image runs at most %d times per response, because only the newest %d viewed images stay in context. View the other images in a later response",
					maxViewedImagesInPrompt, maxViewedImagesInPrompt)), nil
			}
			if err := viewImageModelSupported(model); err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}
			if getConn == nil {
				return fantasy.NewTextErrorResponse("workspace connection resolver is not configured"), nil
			}
			conn, err := getConn(ctx)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}
			resp, err := viewImage(ctx, conn, model, path)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}
			return resp, nil
		},
	)
}

// viewImageModelSupported fails when the turn's model route accepts none of
// the image types view_image returns.
func viewImageModelSupported(model chatprovider.Model) error {
	if slices.ContainsFunc(viewImageMediaTypes, model.AcceptsFilePartMediaType) {
		return nil
	}
	return xerrors.Errorf("model %q on provider %q does not accept image input. Switch to a model with image input to view images",
		model.ModelID(), model.RouteProvider())
}

// viewImageSizeLimit returns the largest file view_image returns and the
// reason for it. Anthropic's per-image limit applies to the base64 form, so a
// route with an inline image cap gets three quarters of it. Other routes get
// the tool media limit that chatloop enforces on every tool result.
func viewImageSizeLimit(model chatprovider.Model) (int, string) {
	if imageCap, ok := model.InlineImageCapBytes(); ok {
		return imageCap / 4 * 3, fmt.Sprintf("the base64 form must fit the %d byte per-image limit of provider %q", imageCap, model.RouteProvider())
	}
	return codersdk.MaxChatFileSizeBytes, "the chat tool media limit"
}

func viewImage(ctx context.Context, conn workspacesdk.AgentConn, model chatprovider.Model, path string) (fantasy.ToolResponse, error) {
	limit, reason := viewImageSizeLimit(model)
	reader, _, err := conn.ReadFile(ctx, path, 0, int64(limit)+1)
	if err != nil {
		return fantasy.ToolResponse{}, err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil {
		return fantasy.ToolResponse{}, xerrors.Errorf("read %s: %w", path, err)
	}
	if len(data) > limit {
		return fantasy.ToolResponse{}, xerrors.Errorf("%s is larger than %d bytes, the view_image limit: %s. Scale the image down, for example to a smaller screenshot, and try again",
			path, limit, reason)
	}
	if len(data) == 0 {
		return fantasy.ToolResponse{}, xerrors.Errorf("%s is empty", path)
	}
	mediaType := chatfiles.DetectMediaType(data)
	if !slices.Contains(viewImageMediaTypes, mediaType) {
		return fantasy.ToolResponse{}, xerrors.Errorf("%s is %s, not a PNG, JPEG, GIF or WebP image. Use read_file for text files", path, mediaType)
	}
	if !model.AcceptsFilePartMediaType(mediaType) {
		return fantasy.ToolResponse{}, xerrors.Errorf("model %q on provider %q does not accept %s images. Convert the image to PNG or JPEG",
			model.ModelID(), model.RouteProvider(), mediaType)
	}
	width, height, err := imageDimensions(data, mediaType)
	if err == nil {
		err = imageComplete(data, mediaType)
	}
	if err != nil {
		return fantasy.ToolResponse{}, xerrors.Errorf("%s is not a valid %s image: %w", path, mediaType, err)
	}
	if width > viewImageMaxDimension || height > viewImageMaxDimension {
		return fantasy.ToolResponse{}, xerrors.Errorf("%s is %dx%d pixels, and view_image accepts at most %d pixels per side. Scale the image down and try again",
			path, width, height, viewImageMaxDimension)
	}
	if width*height > viewImageMaxPixels {
		return fantasy.ToolResponse{}, xerrors.Errorf("%s is %dx%d pixels, and view_image accepts at most %d pixels in total. Scale the image down and try again",
			path, width, height, viewImageMaxPixels)
	}
	if err := imageDecodes(ctx, data, mediaType); err != nil {
		if ctx.Err() != nil {
			return fantasy.ToolResponse{}, ctx.Err()
		}
		return fantasy.ToolResponse{}, xerrors.Errorf("%s is not a valid %s image: %w", path, mediaType, err)
	}
	if mediaType == "image/gif" {
		// OpenAI rejects animated GIFs, and the stored result would fail
		// every later request.
		frames, err := gifFrames(data)
		if err != nil {
			return fantasy.ToolResponse{}, xerrors.Errorf("%s is not a valid %s image: %w", path, mediaType, err)
		}
		if frames > 1 {
			return fantasy.ToolResponse{}, xerrors.Errorf("%s is an animated GIF with %d frames, and view_image accepts only still images. Save one frame as a PNG and view that",
				path, frames)
		}
	}
	resp := fantasy.NewImageResponse(data, mediaType)
	resp.Content = fmt.Sprintf("Image %s (%s, %d bytes, %dx%d pixels).", path, mediaType, len(data), width, height)
	return resp, nil
}

// imageDimensions reads the width and height from the image header.
func imageDimensions(data []byte, mediaType string) (width, height int, err error) {
	var config image.Config
	switch mediaType {
	case "image/png":
		config, err = png.DecodeConfig(bytes.NewReader(data))
	case "image/jpeg":
		config, err = jpeg.DecodeConfig(bytes.NewReader(data))
	case "image/gif":
		config, err = gif.DecodeConfig(bytes.NewReader(data))
	case "image/webp":
		return webpDimensions(data)
	default:
		return 0, 0, xerrors.Errorf("unsupported image type %s", mediaType)
	}
	if err != nil {
		return 0, 0, err
	}
	return config.Width, config.Height, nil
}

// imageComplete checks the structure of the whole file without decoding the
// pixels, so that a truncated or damaged file, such as a screenshot still
// being written, fails here. A stored image the provider rejects would fail
// every later request, because the newest viewed image always stays in the
// prompt.
func imageComplete(data []byte, mediaType string) error {
	switch mediaType {
	case "image/png":
		// The signature, then chunks of length, type, data and CRC, up to
		// IEND. Bytes after IEND are ignored, as decoders do.
		for offset := 8; ; {
			if len(data)-offset < 12 {
				return xerrors.New("truncated before the IEND chunk")
			}
			length := int64(binary.BigEndian.Uint32(data[offset:]))
			if length > int64(len(data)-offset-12) {
				return xerrors.New("truncated chunk")
			}
			end := offset + 8 + int(length)
			if crc32.ChecksumIEEE(data[offset+4:end]) != binary.BigEndian.Uint32(data[end:]) {
				return xerrors.Errorf("bad CRC in chunk %q", data[offset+4:offset+8])
			}
			if string(data[offset+4:offset+8]) == "IEND" {
				return nil
			}
			offset = end + 4
		}
	case "image/jpeg":
		// Entropy-coded data escapes 0xFF, so a marker pair only appears as a
		// marker. An embedded thumbnail ends before the image's last start of
		// scan, so the end of image must follow that.
		sos := bytes.LastIndex(data, []byte{0xff, 0xda})
		if sos < 0 || bytes.LastIndex(data, []byte{0xff, 0xd9}) < sos {
			return xerrors.New("truncated before the end of image marker")
		}
	case "image/gif":
		if !bytes.HasSuffix(bytes.TrimRight(data, "\x00"), []byte{0x3b}) {
			return xerrors.New("truncated before the GIF trailer")
		}
	case "image/webp":
		return webpComplete(data)
	}
	return nil
}

// imageDecodes decodes the pixels of a PNG, JPEG or GIF, so that damaged
// image data behind an intact structure fails here too. A GIF decodes its
// first frame, as gif.Decode does. WebP has no decoder in the standard
// library, so only imageComplete checks it.
func imageDecodes(ctx context.Context, data []byte, mediaType string) error {
	var decode func(io.Reader) (image.Image, error)
	switch mediaType {
	case "image/png":
		decode = png.Decode
	case "image/jpeg":
		decode = jpeg.Decode
	case "image/gif":
		decode = gif.Decode
	default:
		return nil
	}
	select {
	case viewImageDecodeSlot <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-viewImageDecodeSlot }()
	_, err := decode(bytes.NewReader(data))
	return err
}

// gifFrames walks the blocks of a GIF up to its trailer and returns the
// number of frames, without decoding them.
func gifFrames(data []byte) (int, error) {
	truncated := xerrors.New("truncated GIF block")
	if len(data) < 13 {
		return 0, truncated
	}
	// The header and the logical screen descriptor, then the global color
	// table if its flag is set.
	offset := 13
	if data[10]&0x80 != 0 {
		offset += 3 << (data[10]&7 + 1)
	}
	// skipSubBlocks skips data sub-blocks up to their terminator.
	skipSubBlocks := func() bool {
		for offset < len(data) {
			n := int(data[offset])
			offset += 1 + n
			if n == 0 {
				return true
			}
		}
		return false
	}
	frames := 0
	for offset < len(data) {
		switch data[offset] {
		case 0x3b:
			return frames, nil
		case 0x21:
			// An extension: the introducer, the label, then sub-blocks.
			offset += 2
		case 0x2c:
			// An image descriptor, its local color table if set, and the
			// LZW minimum code size before the sub-blocks.
			if len(data)-offset < 10 {
				return 0, truncated
			}
			flags := data[offset+9]
			offset += 11
			if flags&0x80 != 0 {
				offset += 3 << (flags&7 + 1)
			}
			frames++
		default:
			return 0, xerrors.Errorf("unknown GIF block 0x%02x", data[offset])
		}
		if !skipSubBlocks() {
			return 0, truncated
		}
	}
	return 0, truncated
}

// webpComplete walks the RIFF chunks up to the RIFF size, per the WebP
// container specification. Each chunk must fit in the file, and the file must
// hold image data: a VP8 or VP8L chunk, or animation frames. A VP8 chunk must
// also hold the first partition its frame tag names. Without a WebP decoder,
// the lossless and partition data itself is not checked.
func webpComplete(data []byte) error {
	end := int64(binary.LittleEndian.Uint32(data[4:8])) + 8
	if end > int64(len(data)) {
		return xerrors.New("shorter than its RIFF size")
	}
	hasImage := false
	for offset := int64(12); offset < end; {
		if end-offset < 8 {
			return xerrors.New("truncated chunk header")
		}
		name := data[offset : offset+4]
		size := int64(binary.LittleEndian.Uint32(data[offset+4:]))
		payload := offset + 8
		if size > end-payload {
			return xerrors.Errorf("chunk %q is longer than the file", name)
		}
		switch string(name) {
		case "VP8 ":
			// A 3 byte frame tag with the first partition size in bits 5 to
			// 23, then 7 bytes of start code and sizes.
			if size < 10 {
				return xerrors.New("truncated VP8 chunk")
			}
			tag := int64(data[payload]) | int64(data[payload+1])<<8 | int64(data[payload+2])<<16
			if tag>>5+10 > size {
				return xerrors.New("VP8 chunk is shorter than its first partition")
			}
			hasImage = true
		case "VP8L":
			if size < 5 {
				return xerrors.New("truncated VP8L chunk")
			}
			hasImage = true
		case "ANMF":
			hasImage = true
		}
		// A chunk with an odd size has a pad byte.
		offset = payload + size + size&1
	}
	if !hasImage {
		return xerrors.New("no image data")
	}
	return nil
}

// webpDimensions reads the canvas size of a WebP file from its first chunk,
// per the WebP container specification. The standard library has no WebP
// decoder.
func webpDimensions(data []byte) (width, height int, err error) {
	if len(data) < 30 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return 0, 0, xerrors.New("truncated WebP header")
	}
	chunk := data[20:]
	switch string(data[12:16]) {
	case "VP8 ":
		// Lossy: a 3 byte frame tag, the start code, then 14 bit sizes.
		if chunk[3] != 0x9d || chunk[4] != 0x01 || chunk[5] != 0x2a {
			return 0, 0, xerrors.New("bad VP8 start code")
		}
		return int(binary.LittleEndian.Uint16(chunk[6:8]) & 0x3fff), int(binary.LittleEndian.Uint16(chunk[8:10]) & 0x3fff), nil
	case "VP8L":
		// Lossless: a signature byte, then 14 bit sizes minus one.
		if chunk[0] != 0x2f {
			return 0, 0, xerrors.New("bad VP8L signature")
		}
		bits := binary.LittleEndian.Uint32(chunk[1:5])
		return int(bits&0x3fff) + 1, int((bits>>14)&0x3fff) + 1, nil
	case "VP8X":
		// Extended: flags and reserved bytes, then 24 bit sizes minus one.
		w := int(chunk[4]) | int(chunk[5])<<8 | int(chunk[6])<<16
		h := int(chunk[7]) | int(chunk[8])<<8 | int(chunk[9])<<16
		return w + 1, h + 1, nil
	default:
		return 0, 0, xerrors.Errorf("unknown WebP chunk %q", data[12:16])
	}
}

// dynamicToolOwnsViewImage reports whether this turn offers a caller-supplied
// view_image, which it does while fork-view-image is off and appendDynamicTools
// adds the chat's dynamic tools.
func (p *Server) dynamicToolOwnsViewImage(chat database.Chat) bool {
	if p.experiments.Enabled(codersdk.ExperimentForkViewImage) || p.disableCallerSuppliedTools ||
		isExploreSubagentMode(chat.Mode) || (chat.PlanMode.Valid && chat.PlanMode.ChatPlanMode == database.ChatPlanModePlan) {
		return false
	}
	names, err := parseDynamicToolNames(chat.DynamicTools)
	return err == nil && names[viewImageToolName]
}

// limitViewedImagesForModel applies limitViewedImages with another model than
// the turn's, such as a compaction override. The override can have a lower
// view_image size limit than the chat model.
func (p *Server) limitViewedImagesForModel(chat database.Chat, messages []fantasy.Message, model chatprovider.Model) []fantasy.Message {
	if p.dynamicToolOwnsViewImage(chat) {
		return messages
	}
	return limitViewedImages(messages, nil, &model)
}

// limitViewedImages keeps the newest view_image results of messages as images,
// at most maxViewedImagesInPrompt of them, maxImagesInPrompt images and
// maxViewedImageBytesInPrompt decoded bytes together with the other media of
// the prompt, and always the newest one. It replaces older ones with a text
// note. The stored conversation keeps every image and only the prompt drops
// them, so each turn sends a bounded amount of image data. With a model, it also
// replaces an image over view_image's size limit for that model, which a chat
// can hold after it switched from a model with a higher limit. It copies the
// messages it changes and leaves messages itself unchanged. pending is the
// tail the turn appends after messages. Its media counts toward the byte
// budget, and limitViewedImages does not change it.
func limitViewedImages(messages, pending []fantasy.Message, model *chatprovider.Model) []fantasy.Message {
	sizeLimit := -1
	if model != nil {
		sizeLimit, _ = viewImageSizeLimit(*model)
	}
	// A result belongs to the newest earlier call with its ID, so a reused
	// ID does not make another tool's result count as a viewed image.
	// Other media, such as computer screenshots and attachments, stays in
	// the prompt, so it takes its share of the byte budget first, and so does
	// the media of the pending tail.
	type position struct{ i, j int }
	var results []position
	otherBytes, otherImages := 0, 0
	callTools := map[string]string{}
	n := len(messages)
	for i, msg := range append(messages[:n:n], pending...) {
		for j, part := range msg.Content {
			if call, ok := toolCallPartOf(part); ok {
				callTools[call.ToolCallID] = call.ToolName
				continue
			}
			if file, ok := part.(fantasy.FilePart); ok {
				otherBytes += len(file.Data)
				if strings.HasPrefix(file.MediaType, "image/") {
					otherImages++
				}
				continue
			}
			result, ok := toolResultPartOf(part)
			if !ok {
				continue
			}
			if callTools[result.ToolCallID] == viewImageToolName && i < n {
				results = append(results, position{i, j})
			} else if media, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentMedia](result.Output); ok {
				otherBytes += base64DecodedLen(media.Data)
				if strings.HasPrefix(media.MediaType, "image/") {
					otherImages++
				}
			}
		}
	}

	var out []fantasy.Message
	cloned := map[int]bool{}
	kept, keptBytes := 0, otherBytes
	for k := len(results) - 1; k >= 0; k-- {
		i, j := results[k].i, results[k].j
		result, _ := toolResultPartOf(messages[i].Content[j])
		media, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentMedia](result.Output)
		if !ok {
			continue
		}
		size := base64DecodedLen(media.Data)
		var note string
		switch {
		case sizeLimit >= 0 && size > sizeLimit:
			note = fmt.Sprintf("[image omitted: %d bytes is larger than the view_image limit of %d bytes for this model. Scale the image down and call view_image again.]", size, sizeLimit)
		case kept == 0 || (kept < maxViewedImagesInPrompt && otherImages+kept < maxImagesInPrompt && keptBytes+size <= maxViewedImageBytesInPrompt):
			kept++
			keptBytes += size
			continue
		default:
			note = "[image no longer in context: only the newest viewed images are kept. Call view_image again to see it.]"
		}
		if media.Text != "" {
			note += "\n\n" + media.Text
		}
		result.Output = fantasy.ToolResultOutputContentText{Text: note}
		if out == nil {
			out = slices.Clone(messages)
		}
		if !cloned[i] {
			out[i].Content = slices.Clone(messages[i].Content)
			cloned[i] = true
		}
		out[i].Content[j] = result
	}
	if out == nil {
		return messages
	}
	return out
}

func toolCallPartOf(part fantasy.MessagePart) (fantasy.ToolCallPart, bool) {
	switch call := part.(type) {
	case fantasy.ToolCallPart:
		return call, true
	case *fantasy.ToolCallPart:
		if call != nil {
			return *call, true
		}
	}
	return fantasy.ToolCallPart{}, false
}

func toolResultPartOf(part fantasy.MessagePart) (fantasy.ToolResultPart, bool) {
	switch result := part.(type) {
	case fantasy.ToolResultPart:
		return result, true
	case *fantasy.ToolResultPart:
		if result != nil {
			return *result, true
		}
	}
	return fantasy.ToolResultPart{}, false
}
