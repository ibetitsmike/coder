package chatd

// This file is fork code: it holds the chat_history chat tool of the
// fork-token-budget experiment. appendForkTools adds it to a turn.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/fantasy"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatloop"
	"github.com/coder/coder/v2/codersdk"
)

const (
	chatHistoryToolName = "chat_history"
	// maxChatHistoryBytes bounds the JSON of one chat_history result.
	maxChatHistoryBytes  = 16 << 10
	defaultHistoryItems  = 20
	maxHistoryItems      = 50
	maxHistoryMatches    = 20
	minHistoryQueryBytes = 2
	maxHistoryQueryBytes = 200
	historyPreviewBytes  = 240
	historySnippetBytes  = 320
	historyEchoBytes     = 64
	// historyChunkBytes is the most text of one part that read_item returns
	// at a time. JSON escaping can grow it, so the result is checked again.
	historyChunkBytes = 12 << 10
	// historyMessageBytes / 2 is the first cut of each part of the message
	// read_item returns without a part. historyMessageResult then fits the
	// whole result in maxChatHistoryBytes.
	historyMessageBytes = maxChatHistoryBytes - 1024
)

const chatHistoryToolDescription = "Find and read messages of this chat that are no longer in your " +
	"context. The history is a list of context windows: each compaction or new context window " +
	"starts the next one, and the last window is the current one. It never reaches past a " +
	"clear of the chat. Actions: list_windows; list_items (window, after_id, limit) lists the " +
	"messages of a window with a short preview; search (query, window, before_id) finds the " +
	"newest messages whose text, tool calls or tool results contain the query; read_item (id) " +
	"reads one message, with long parts cut, and read_item (id, part, offset) reads one part " +
	"in full, a slice at a time. Each result holds 16 KiB at most."

type chatHistoryArgs struct {
	Action   string `json:"action" enum:"list_windows,list_items,search,read_item"`
	Window   string `json:"window,omitempty" description:"A window ID from list_windows, such as w:2. list_items needs it. search takes it to look in that window only."`
	Query    string `json:"query,omitempty" description:"For search, the text to find, 2 to 200 bytes. Case does not matter."`
	ID       int64  `json:"id,omitempty" description:"For read_item, the message ID from list_items or search."`
	Part     int    `json:"part,omitempty" description:"For read_item, the number of one part to read in full, starting at 1. Leave it out to read the whole message with long parts cut."`
	Offset   int    `json:"offset,omitempty" description:"For read_item with part, the byte offset to read from. Use next_offset of the previous call."`
	AfterID  int64  `json:"after_id,omitempty" description:"For list_items, return messages after this ID. Use next_after_id of the previous page."`
	BeforeID int64  `json:"before_id,omitempty" description:"For search, return matches before this message ID. Use next_before_id of the previous page."`
	Limit    int    `json:"limit,omitempty" description:"For list_items, the most messages to return, 1 to 50. The default is 20."`
}

// chatHistoryWindow is one context window of a chat. after and before bound
// its message IDs, both exclusive, and before is 0 for the current window.
type chatHistoryWindow struct {
	ID        string    `json:"id"`
	StartedBy string    `json:"started_by"`
	StartedAt time.Time `json:"started_at"`
	Messages  int       `json:"messages"`
	FirstID   int64     `json:"first_message_id,omitempty"`
	LastID    int64     `json:"last_message_id,omitempty"`
	Current   bool      `json:"current,omitempty"`
	after     int64
	before    int64
}

type chatHistoryWindowsResult struct {
	Windows        []chatHistoryWindow `json:"windows"`
	OmittedWindows int                 `json:"omitted_windows,omitempty"`
}

type chatHistoryItem struct {
	ID        int64                    `json:"id"`
	Role      codersdk.ChatMessageRole `json:"role"`
	CreatedAt time.Time                `json:"created_at"`
	Preview   string                   `json:"preview"`
	Bytes     int                      `json:"bytes"`
}

type chatHistoryItemsResult struct {
	Window      string            `json:"window"`
	Items       []chatHistoryItem `json:"items"`
	NextAfterID int64             `json:"next_after_id"`
	HasMore     bool              `json:"has_more"`
}

type chatHistoryMatch struct {
	ID        int64                    `json:"id"`
	Window    string                   `json:"window"`
	Role      codersdk.ChatMessageRole `json:"role"`
	CreatedAt time.Time                `json:"created_at"`
	Part      int                      `json:"part"`
	Snippet   string                   `json:"snippet"`
}

type chatHistorySearchResult struct {
	Matches      []chatHistoryMatch `json:"matches"`
	NextBeforeID int64              `json:"next_before_id,omitempty"`
	HasMore      bool               `json:"has_more"`
}

type chatHistoryMessageResult struct {
	ID           int64                      `json:"id"`
	Window       string                     `json:"window"`
	Role         codersdk.ChatMessageRole   `json:"role"`
	CreatedAt    time.Time                  `json:"created_at"`
	Parts        []codersdk.ChatMessagePart `json:"parts"`
	PartBytes    []int                      `json:"part_bytes"`
	OmittedParts int                        `json:"omitted_parts,omitempty"`
}

type chatHistoryPartResult struct {
	ID         int64                        `json:"id"`
	Part       int                          `json:"part"`
	Type       codersdk.ChatMessagePartType `json:"type"`
	ToolName   string                       `json:"tool_name,omitempty"`
	Offset     int                          `json:"offset"`
	NextOffset int                          `json:"next_offset"`
	TotalBytes int                          `json:"total_bytes"`
	HasMore    bool                         `json:"has_more"`
	Text       string                       `json:"text"`
}

// chatHistoryTool returns the chat_history tool, which finds and reads the
// messages of chat across its context windows.
func (p *Server) chatHistoryTool(chat database.Chat) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		chatHistoryToolName,
		chatHistoryToolDescription,
		func(ctx context.Context, args chatHistoryArgs, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			result, err := p.runChatHistory(ctx, chat, args)
			if err != nil {
				text, _ := chatloop.TruncateToolResultText(err.Error(), maxChatHistoryBytes)
				return fantasy.NewTextErrorResponse(text), nil
			}
			return toolJSONResponse(result), nil
		},
	)
}

// runChatHistory runs one chat_history action on chat. The returned error is
// the text the model sees.
func (p *Server) runChatHistory(ctx context.Context, chat database.Chat, args chatHistoryArgs) (any, error) {
	fdb, err := forkdb.From(p.db)
	if err != nil {
		return nil, xerrors.Errorf("fork queries: %w", err)
	}
	switch args.Action {
	case "list_windows", "list_items", "search", "read_item":
	default:
		return nil, xerrors.Errorf("unknown action %q: use list_windows, list_items, search or read_item", historyEcho(args.Action))
	}
	rows, err := fdb.ListChatHistoryRows(ctx, chat.ID)
	if err != nil {
		return nil, xerrors.Errorf("read chat history: %w", err)
	}
	windows := chatHistoryWindows(chat, rows)
	switch args.Action {
	case "list_windows":
		return fitHistoryWindows(windows)
	case "list_items":
		return p.listHistoryItems(ctx, chat, windows, args)
	case "search":
		return searchChatHistory(ctx, fdb, chat, windows, args)
	default:
		return p.readHistoryItem(ctx, chat, windows, args)
	}
}

// chatHistoryWindows splits rows into context windows. A compressed
// model-only user row is the anchor of a boundary, the row the prompt
// query starts from, and opens the next window. The other compressed rows
// of the boundary name it with their tool. A chat_cleared boundary drops the
// windows before it, so the history never reaches past a clear.
func chatHistoryWindows(chat database.Chat, rows []forkdb.ListChatHistoryRowsRow) []chatHistoryWindow {
	windows := []chatHistoryWindow{{StartedBy: "chat_start", StartedAt: chat.CreatedAt}}
	for _, row := range rows {
		current := &windows[len(windows)-1]
		if !row.Compressed {
			if current.FirstID == 0 {
				current.FirstID = row.ID
			}
			current.LastID = row.ID
			current.Messages++
			continue
		}
		if row.Role == string(database.ChatMessageRoleUser) && row.Visibility == string(database.ChatMessageVisibilityModel) {
			current.before = row.ID
			windows = append(windows, chatHistoryWindow{StartedAt: row.CreatedAt, after: row.ID})
			continue
		}
		name := boundaryToolName(row.BoundaryContent)
		if current.StartedBy == "" {
			current.StartedBy = name
		}
		if name == "chat_cleared" && len(windows) > 1 {
			windows = []chatHistoryWindow{*current}
		}
	}
	for i := range windows {
		windows[i].ID = "w:" + strconv.Itoa(i+1)
		if windows[i].StartedBy == "" {
			windows[i].StartedBy = "context_boundary"
		}
	}
	windows[len(windows)-1].Current = true
	return windows
}

// boundaryToolName returns the tool name of the first tool part of a
// boundary row's content.
func boundaryToolName(content json.RawMessage) string {
	var parts []struct {
		ToolName string `json:"tool_name"`
	}
	if err := json.Unmarshal(content, &parts); err != nil {
		return ""
	}
	for _, part := range parts {
		if part.ToolName != "" {
			return part.ToolName
		}
	}
	return ""
}

func fitHistoryWindows(windows []chatHistoryWindow) (chatHistoryWindowsResult, error) {
	result := chatHistoryWindowsResult{Windows: windows}
	for {
		size, err := jsonSize(result)
		if err != nil {
			return chatHistoryWindowsResult{}, xerrors.Errorf("encode windows: %w", err)
		}
		if size <= maxChatHistoryBytes || len(result.Windows) == 1 {
			return result, nil
		}
		result.Windows = result.Windows[1:]
		result.OmittedWindows++
	}
}

func findHistoryWindow(windows []chatHistoryWindow, id string) (chatHistoryWindow, error) {
	for _, window := range windows {
		if window.ID == id {
			return window, nil
		}
	}
	return chatHistoryWindow{}, xerrors.Errorf("unknown window %q: list_windows lists w:1 to w:%d", historyEcho(id), len(windows))
}

// historyEcho shortens an argument of the model that an error repeats.
func historyEcho(value string) string {
	if len(value) <= historyEchoBytes {
		return value
	}
	return strings.ToValidUTF8(value[:historyEchoBytes], "") + "..."
}

// historyWindowOf returns the ID of the window that holds message id, or ""
// when the message is before the history starts.
func historyWindowOf(windows []chatHistoryWindow, id int64) string {
	for _, window := range windows {
		if id > window.after && (window.before == 0 || id < window.before) {
			return window.ID
		}
	}
	return ""
}

func (p *Server) listHistoryItems(ctx context.Context, chat database.Chat, windows []chatHistoryWindow, args chatHistoryArgs) (chatHistoryItemsResult, error) {
	if args.Window == "" {
		return chatHistoryItemsResult{}, xerrors.New("list_items needs a window, such as w:1")
	}
	window, err := findHistoryWindow(windows, args.Window)
	if err != nil {
		return chatHistoryItemsResult{}, err
	}
	limit := defaultHistoryItems
	if args.Limit != 0 {
		if args.Limit < 1 || args.Limit > maxHistoryItems {
			return chatHistoryItemsResult{}, xerrors.Errorf("limit must be between 1 and %d", maxHistoryItems)
		}
		limit = args.Limit
	}
	result := chatHistoryItemsResult{
		Window:      window.ID,
		Items:       []chatHistoryItem{},
		NextAfterID: max(args.AfterID, window.after),
	}
	usedBytes := 0
	for scanned := 0; scanned < maxTranscriptScanRows; {
		rows, err := p.db.GetChatMessagesByChatIDAscPaginated(ctx, database.GetChatMessagesByChatIDAscPaginatedParams{
			ChatID:   chat.ID,
			AfterID:  result.NextAfterID,
			LimitVal: transcriptScanBatch,
		})
		if err != nil {
			return chatHistoryItemsResult{}, xerrors.Errorf("read chat messages: %w", err)
		}
		for _, row := range rows {
			if window.before != 0 && row.ID >= window.before {
				return result, nil
			}
			scanned++
			if message, ok := historyMessage(row); ok {
				item := chatHistoryItem{
					ID:        message.ID,
					Role:      message.Role,
					CreatedAt: message.CreatedAt,
					Preview:   historyPreview(message.Parts),
				}
				item.Bytes, err = jsonSize(message.Parts)
				if err != nil {
					return chatHistoryItemsResult{}, xerrors.Errorf("encode chat message %d: %w", row.ID, err)
				}
				size, err := jsonSize(item)
				if err != nil {
					return chatHistoryItemsResult{}, xerrors.Errorf("encode chat message %d: %w", row.ID, err)
				}
				// The JSON around the items takes the rest of the budget.
				if usedBytes+size > maxChatHistoryBytes-256 || len(result.Items) == limit {
					result.HasMore = true
					return result, nil
				}
				result.Items = append(result.Items, item)
				usedBytes += size
			}
			result.NextAfterID = row.ID
		}
		if len(rows) < transcriptScanBatch {
			return result, nil
		}
	}
	result.HasMore = true
	return result, nil
}

// historyMessage converts row with the transcript rules, and skips the
// compressed rows of a boundary.
func historyMessage(row database.ChatMessage) (chatTranscriptMessage, bool) {
	if row.Compressed {
		return chatTranscriptMessage{}, false
	}
	return transcriptMessage(row, nil, nil, 0, true)
}

// historyPreview describes parts in one line of at most
// historyPreviewBytes.
func historyPreview(parts []codersdk.ChatMessagePart) string {
	pieces := make([]string, 0, len(parts))
	for _, part := range parts {
		var piece string
		switch part.Type {
		case codersdk.ChatMessagePartTypeToolCall:
			piece = "call " + part.ToolName + " " + string(part.Args)
		case codersdk.ChatMessagePartTypeToolResult:
			piece = "result " + part.ToolName + " " + string(part.Result)
			if part.IsError {
				piece = "error " + part.ToolName + " " + string(part.Result)
			}
		case codersdk.ChatMessagePartTypeText:
			piece = part.Text
		default:
			piece = string(part.Type) + " " + strings.TrimSpace(part.Text+" "+part.Name+" "+part.FileName)
		}
		pieces = append(pieces, strings.Join(strings.Fields(piece), " "))
	}
	preview := strings.Join(pieces, " | ")
	if len(preview) <= historyPreviewBytes {
		return preview
	}
	return strings.ToValidUTF8(preview[:historyPreviewBytes-3], "") + "..."
}

func searchChatHistory(ctx context.Context, fdb forkdb.Store, chat database.Chat, windows []chatHistoryWindow, args chatHistoryArgs) (chatHistorySearchResult, error) {
	if len(args.Query) < minHistoryQueryBytes || len(args.Query) > maxHistoryQueryBytes {
		return chatHistorySearchResult{}, xerrors.Errorf("query must be %d to %d bytes", minHistoryQueryBytes, maxHistoryQueryBytes)
	}
	if args.BeforeID < 0 {
		return chatHistorySearchResult{}, xerrors.New("before_id must not be negative")
	}
	after, before := windows[0].after, int64(0)
	if args.Window != "" {
		window, err := findHistoryWindow(windows, args.Window)
		if err != nil {
			return chatHistorySearchResult{}, err
		}
		after, before = window.after, window.before
	}
	if args.BeforeID != 0 && (before == 0 || args.BeforeID < before) {
		before = args.BeforeID
	}
	query := strings.ToLower(args.Query)
	jsonQuery, err := jsonStringText(query)
	if err != nil {
		return chatHistorySearchResult{}, xerrors.Errorf("encode query: %w", err)
	}
	rows, err := fdb.SearchChatHistory(ctx, forkdb.SearchChatHistoryParams{
		ChatID:     chat.ID,
		AfterID:    after,
		BeforeID:   before,
		Needle:     jsonQuery,
		LimitCount: maxHistoryMatches,
	})
	if err != nil {
		return chatHistorySearchResult{}, xerrors.Errorf("search chat history: %w", err)
	}
	result := chatHistorySearchResult{Matches: []chatHistoryMatch{}}
	usedBytes := 0
	for _, row := range rows {
		message, ok := transcriptMessage(database.ChatMessage{
			ID:             row.ID,
			ChatID:         chat.ID,
			CreatedAt:      row.CreatedAt,
			Role:           database.ChatMessageRole(row.Role),
			Content:        row.Content,
			ContentVersion: row.ContentVersion,
			Visibility:     database.ChatMessageVisibilityBoth,
		}, nil, nil, 0, true)
		// The SQL match also hits JSON keys and parts the transcript
		// drops, so a row counts only when a part it shows matches.
		if ok {
			if part, snippet, found := historyMatch(message.Parts, query, jsonQuery); found {
				match := chatHistoryMatch{
					ID:        message.ID,
					Window:    historyWindowOf(windows, message.ID),
					Role:      message.Role,
					CreatedAt: message.CreatedAt,
					Part:      part,
					Snippet:   snippet,
				}
				size, err := jsonSize(match)
				if err != nil {
					return chatHistorySearchResult{}, xerrors.Errorf("encode match: %w", err)
				}
				if usedBytes+size > maxChatHistoryBytes-256 {
					result.HasMore = true
					return result, nil
				}
				result.Matches = append(result.Matches, match)
				usedBytes += size
			}
		}
		result.NextBeforeID = row.ID
	}
	result.HasMore = len(rows) == maxHistoryMatches
	if !result.HasMore {
		result.NextBeforeID = 0
	}
	return result, nil
}

// jsonStringText returns s as it appears inside a JSON string, without the
// quotes, the way Postgres prints jsonb.
func jsonStringText(s string) (string, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(s); err != nil {
		return "", err
	}
	encoded := strings.TrimSuffix(buf.String(), "\n")
	return encoded[1 : len(encoded)-1], nil
}

// historyMatch finds the first part that contains query, lowercased, and
// returns its number, starting at 1, and a snippet around the match. Text
// fields are matched against query, and JSON fields against jsonQuery.
func historyMatch(parts []codersdk.ChatMessagePart, query, jsonQuery string) (int, string, bool) {
	for i, part := range parts {
		fields := []struct {
			value  string
			needle string
		}{
			{part.Text, query},
			{part.Content, query},
			{part.ToolName, query},
			{string(part.Args), jsonQuery},
			{string(part.Result), jsonQuery},
		}
		for _, field := range fields {
			lower := strings.ToLower(field.value)
			index := strings.Index(lower, field.needle)
			if index < 0 {
				continue
			}
			// Lowercasing can change byte lengths. Cut the original
			// only when it did not.
			text := field.value
			if len(lower) != len(text) {
				text = lower
			}
			return i + 1, historySnippet(text, index, len(field.needle)), true
		}
	}
	return 0, "", false
}

func historySnippet(text string, index, length int) string {
	start := max(0, index-(historySnippetBytes-length)/2)
	end := min(len(text), start+historySnippetBytes)
	start = max(0, end-historySnippetBytes)
	snippet := strings.ToValidUTF8(text[start:end], "")
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(text) {
		snippet += "..."
	}
	return snippet
}

func (p *Server) readHistoryItem(ctx context.Context, chat database.Chat, windows []chatHistoryWindow, args chatHistoryArgs) (any, error) {
	if args.ID <= 0 {
		return nil, xerrors.New("read_item needs the id of a message")
	}
	if args.Part < 0 || args.Offset < 0 {
		return nil, xerrors.New("part and offset must not be negative")
	}
	row, err := p.db.GetChatMessageByID(ctx, args.ID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && row.ChatID != chat.ID) {
		return nil, xerrors.Errorf("message %d does not exist in this chat", args.ID)
	}
	if err != nil {
		return nil, xerrors.Errorf("read chat message: %w", err)
	}
	message, ok := historyMessage(row)
	if !ok {
		return nil, xerrors.Errorf("message %d has no readable content", args.ID)
	}
	window := historyWindowOf(windows, row.ID)
	if window == "" {
		return nil, xerrors.Errorf("message %d is from before the chat was cleared", args.ID)
	}
	if args.Part == 0 {
		return historyMessageResult(message, window)
	}
	if args.Part > len(message.Parts) {
		return nil, xerrors.Errorf("message %d has %d parts", args.ID, len(message.Parts))
	}
	part := message.Parts[args.Part-1]
	text := historyPartText(part)
	if args.Offset > len(text) {
		return nil, xerrors.Errorf("offset %d is past the end of part %d, which has %d bytes", args.Offset, args.Part, len(text))
	}
	result := chatHistoryPartResult{
		ID:         message.ID,
		Part:       args.Part,
		Type:       part.Type,
		ToolName:   part.ToolName,
		Offset:     args.Offset,
		TotalBytes: len(text),
	}
	for chunk := historyChunkBytes; ; chunk /= 2 {
		end := min(len(text), args.Offset+chunk)
		for end < len(text) && end > args.Offset && !utf8.RuneStart(text[end]) {
			end--
		}
		result.Text = strings.ToValidUTF8(text[args.Offset:end], "")
		result.NextOffset = end
		result.HasMore = end < len(text)
		size, err := jsonSize(result)
		if err != nil {
			return nil, xerrors.Errorf("encode part: %w", err)
		}
		if size <= maxChatHistoryBytes || chunk <= utf8.UTFMax {
			return result, nil
		}
	}
}

// historyMessageResult fits the whole result in maxChatHistoryBytes: it
// drops trailing parts with their sizes, and then cuts a last part that is
// still too large to a smaller size.
func historyMessageResult(message chatTranscriptMessage, window string) (chatHistoryMessageResult, error) {
	result := chatHistoryMessageResult{
		ID:           message.ID,
		Window:       window,
		Role:         message.Role,
		CreatedAt:    message.CreatedAt,
		Parts:        make([]codersdk.ChatMessagePart, 0, len(message.Parts)),
		PartBytes:    make([]int, 0, len(message.Parts)),
		OmittedParts: message.OmittedParts,
	}
	partBytes := historyMessageBytes / 2
	for _, part := range message.Parts {
		result.PartBytes = append(result.PartBytes, len(historyPartText(part)))
		result.Parts = append(result.Parts, truncateTranscriptPart(part, partBytes))
	}
	for {
		size, err := jsonSize(result)
		if err != nil {
			return chatHistoryMessageResult{}, xerrors.Errorf("encode chat message %d: %w", message.ID, err)
		}
		switch {
		case size <= maxChatHistoryBytes:
			return result, nil
		case len(result.Parts) > 1:
			result.Parts = result.Parts[:len(result.Parts)-1]
			result.PartBytes = result.PartBytes[:len(result.PartBytes)-1]
			result.OmittedParts++
		case len(result.Parts) == 1 && partBytes > utf8.UTFMax:
			partBytes /= 2
			result.Parts[0] = truncateTranscriptPart(message.Parts[0], partBytes)
		default:
			return result, nil
		}
	}
}

// historyPartText returns the main text of part: its text, its file
// content, its tool result or its tool call arguments.
func historyPartText(part codersdk.ChatMessagePart) string {
	switch {
	case part.Text != "":
		return part.Text
	case part.Content != "":
		return part.Content
	case len(part.Result) > 0:
		return string(part.Result)
	case len(part.Args) > 0:
		return string(part.Args)
	default:
		return ""
	}
}
