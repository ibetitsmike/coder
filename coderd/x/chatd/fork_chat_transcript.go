package chatd

// This file is fork code: it holds the read_chat_transcript chat tool of the
// fork-chat-todos experiment. appendForkTools adds it to a turn.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/db2sdk"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/httpmw"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/coderd/x/chatd/chatloop"
	"github.com/coder/coder/v2/codersdk"
)

const (
	readChatTranscriptToolName = "read_chat_transcript"
	defaultTranscriptMessages  = 20
	maxTranscriptMessages      = 100
	defaultTranscriptBytes     = 32 << 10
	minTranscriptBytes         = 4 << 10
	maxTranscriptBytes         = 64 << 10
	maxTranscriptPartBytes     = 16 << 10
	transcriptScanBatch        = 50
	maxTranscriptScanRows      = 1000
)

type readChatTranscriptArgs struct {
	ChatID    string   `json:"chat_id,omitempty" description:"The ID of the chat to read. Leave it empty to read this chat."`
	AfterID   int64    `json:"after_id,omitempty" description:"Return messages after this message ID. Use 0 for the first page, then next_after_id of the previous page."`
	Roles     []string `json:"roles,omitempty" description:"Return only messages with these roles: user, assistant, tool, system. Empty returns all roles."`
	PartTypes []string `json:"part_types,omitempty" description:"Return only message parts of these types, such as text, reasoning, tool-call or tool-result. Messages without a matching part are skipped. Empty returns all types."`
	Limit     int      `json:"limit,omitempty" description:"The most messages to return, 1 to 100. The default is 20."`
	MaxBytes  int      `json:"max_bytes,omitempty" description:"The most bytes of message JSON to return, 4096 to 65536. The default is 32768."`
}

type chatTranscriptResult struct {
	ChatID      uuid.UUID               `json:"chat_id"`
	Title       string                  `json:"title"`
	Messages    []chatTranscriptMessage `json:"messages"`
	NextAfterID int64                   `json:"next_after_id"`
	HasMore     bool                    `json:"has_more"`
}

type chatTranscriptMessage struct {
	ID        int64                      `json:"id"`
	Role      codersdk.ChatMessageRole   `json:"role"`
	CreatedAt time.Time                  `json:"created_at"`
	Parts     []codersdk.ChatMessagePart `json:"parts"`
}

// chatTranscriptTool returns the read_chat_transcript tool, which reads the
// messages of chat or of another chat its owner can read.
func (p *Server) chatTranscriptTool(chat database.Chat) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		readChatTranscriptToolName,
		"Read the messages of this chat, of a subagent chat, or of another "+
			"chat your user can read, in pages. Messages compacted out of "+
			"your context are still returned verbatim. Long parts are "+
			"truncated. Continue with after_id set to next_after_id while "+
			"has_more is true.",
		func(ctx context.Context, args readChatTranscriptArgs, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			result, err := p.readChatTranscript(ctx, chat, args)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}
			return toolJSONResponse(result), nil
		},
	)
}

// readChatTranscript reads one page of the target chat's messages that
// both its user and its model see. Every read runs as the owner of chat,
// never as chatd, which can read every chat: the owner's own chats, their
// subagent chats and chats shared with them are readable, and other chats
// are not.
func (p *Server) readChatTranscript(ctx context.Context, chat database.Chat, args readChatTranscriptArgs) (chatTranscriptResult, error) {
	limit, maxBytes, err := transcriptPageSize(args)
	if err != nil {
		return chatTranscriptResult{}, err
	}
	roles, partTypes, err := transcriptFilters(args)
	if err != nil {
		return chatTranscriptResult{}, err
	}
	targetID := chat.ID
	if args.ChatID != "" {
		targetID, err = uuid.Parse(args.ChatID)
		if err != nil {
			return chatTranscriptResult{}, xerrors.Errorf("chat_id %q is not a valid ID", args.ChatID)
		}
	}

	owner, _, err := httpmw.UserRBACSubject(ctx, p.db, chat.OwnerID, rbac.ScopeAll)
	if err != nil {
		return chatTranscriptResult{}, xerrors.Errorf("load chat owner authorization: %w", err)
	}
	ownerCtx := dbauthz.As(ctx, owner)
	target, err := p.db.GetChatByID(ownerCtx, targetID)
	if errors.Is(err, sql.ErrNoRows) || dbauthz.IsNotAuthorizedError(err) {
		return chatTranscriptResult{}, xerrors.Errorf("chat %s does not exist or your user cannot read it", targetID)
	}
	if err != nil {
		return chatTranscriptResult{}, xerrors.Errorf("read chat: %w", err)
	}

	result := chatTranscriptResult{
		ChatID:      target.ID,
		Title:       target.Title,
		Messages:    []chatTranscriptMessage{},
		NextAfterID: args.AfterID,
	}
	usedBytes := 0
	for scanned := 0; scanned < maxTranscriptScanRows; {
		rows, err := p.db.GetChatMessagesByChatIDAscPaginated(ownerCtx, database.GetChatMessagesByChatIDAscPaginatedParams{
			ChatID:   target.ID,
			AfterID:  result.NextAfterID,
			LimitVal: transcriptScanBatch,
		})
		if err != nil {
			return chatTranscriptResult{}, xerrors.Errorf("read chat messages: %w", err)
		}
		for i, row := range rows {
			scanned++
			message, ok := transcriptMessage(row, roles, partTypes, min(maxTranscriptPartBytes, maxBytes))
			if ok {
				size, err := jsonSize(message)
				if err != nil {
					return chatTranscriptResult{}, xerrors.Errorf("encode chat message %d: %w", row.ID, err)
				}
				// The first message always fits so that every page moves
				// the cursor forward.
				if len(result.Messages) > 0 && usedBytes+size > maxBytes {
					result.HasMore = true
					return result, nil
				}
				result.Messages = append(result.Messages, message)
				usedBytes += size
			}
			result.NextAfterID = row.ID
			if len(result.Messages) == limit {
				result.HasMore = i < len(rows)-1 || len(rows) == transcriptScanBatch
				return result, nil
			}
		}
		if len(rows) < transcriptScanBatch {
			return result, nil
		}
	}
	result.HasMore = true
	return result, nil
}

func transcriptPageSize(args readChatTranscriptArgs) (limit int, maxBytes int, err error) {
	limit, maxBytes = defaultTranscriptMessages, defaultTranscriptBytes
	if args.Limit != 0 {
		if args.Limit < 1 || args.Limit > maxTranscriptMessages {
			return 0, 0, xerrors.Errorf("limit must be between 1 and %d", maxTranscriptMessages)
		}
		limit = args.Limit
	}
	if args.MaxBytes != 0 {
		if args.MaxBytes < minTranscriptBytes || args.MaxBytes > maxTranscriptBytes {
			return 0, 0, xerrors.Errorf("max_bytes must be between %d and %d", minTranscriptBytes, maxTranscriptBytes)
		}
		maxBytes = args.MaxBytes
	}
	if args.AfterID < 0 {
		return 0, 0, xerrors.New("after_id must not be negative")
	}
	return limit, maxBytes, nil
}

func transcriptFilters(args readChatTranscriptArgs) (roles []codersdk.ChatMessageRole, partTypes []codersdk.ChatMessagePartType, err error) {
	knownRoles := []codersdk.ChatMessageRole{
		codersdk.ChatMessageRoleUser,
		codersdk.ChatMessageRoleAssistant,
		codersdk.ChatMessageRoleTool,
		codersdk.ChatMessageRoleSystem,
	}
	for _, role := range args.Roles {
		if !slices.Contains(knownRoles, codersdk.ChatMessageRole(role)) {
			return nil, nil, xerrors.Errorf("unknown role %q", role)
		}
		roles = append(roles, codersdk.ChatMessageRole(role))
	}
	for _, partType := range args.PartTypes {
		if !slices.Contains(codersdk.AllChatMessagePartTypes(), codersdk.ChatMessagePartType(partType)) {
			return nil, nil, xerrors.Errorf("unknown part type %q", partType)
		}
		partTypes = append(partTypes, codersdk.ChatMessagePartType(partType))
	}
	return roles, partTypes, nil
}

// transcriptMessage converts row for a transcript page. db2sdk.ChatMessage
// drops model-only parts and strips provider metadata and credentials with
// StripInternal. It reports false when the filters leave nothing of row.
// User-only rows are skipped: they are projections kept from the model,
// such as a tool result whose user view holds a subagent's private
// reasoning while the model gets a separate model-only row.
func transcriptMessage(row database.ChatMessage, roles []codersdk.ChatMessageRole, partTypes []codersdk.ChatMessagePartType, maxPartBytes int) (chatTranscriptMessage, bool) {
	if row.Visibility != database.ChatMessageVisibilityBoth {
		return chatTranscriptMessage{}, false
	}
	message := db2sdk.ChatMessage(row)
	if len(roles) > 0 && !slices.Contains(roles, message.Role) {
		return chatTranscriptMessage{}, false
	}
	parts := make([]codersdk.ChatMessagePart, 0, len(message.Content))
	for _, part := range message.Content {
		if len(partTypes) > 0 && !slices.Contains(partTypes, part.Type) {
			continue
		}
		parts = append(parts, truncateTranscriptPart(part, maxPartBytes))
	}
	if len(parts) == 0 {
		return chatTranscriptMessage{}, false
	}
	return chatTranscriptMessage{
		ID:        message.ID,
		Role:      message.Role,
		CreatedAt: message.CreatedAt,
		Parts:     parts,
	}, true
}

// truncateTranscriptPart bounds the large fields of part with the tool
// result truncation chatloop applies. Inline file bytes are dropped: the
// media type and name stay, and a model cannot use the base64 data.
func truncateTranscriptPart(part codersdk.ChatMessagePart, maxBytes int) codersdk.ChatMessagePart {
	part.Data = nil
	part.Text, _ = chatloop.TruncateToolResultText(part.Text, maxBytes)
	part.Content, _ = chatloop.TruncateToolResultText(part.Content, maxBytes)
	part.Args = truncateTranscriptJSON(part.Args, maxBytes)
	part.Result = truncateTranscriptJSON(part.Result, maxBytes)
	return part
}

// truncateTranscriptJSON returns raw unchanged when it fits, and otherwise
// the truncated text as a JSON string, since a cut JSON value is not valid.
func truncateTranscriptJSON(raw json.RawMessage, maxBytes int) json.RawMessage {
	text, truncated := chatloop.TruncateToolResultText(string(raw), maxBytes)
	if !truncated {
		return raw
	}
	encoded, err := json.Marshal(text)
	if err != nil {
		return nil
	}
	return encoded
}

func jsonSize(value any) (int, error) {
	data, err := json.Marshal(value)
	return len(data), err
}
