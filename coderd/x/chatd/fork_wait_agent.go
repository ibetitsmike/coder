package chatd

// This file is fork code: it adds the include_tool_calls option of the
// fork-chat-todos experiment to wait_agent. appendForkTools wraps the
// upstream tool, so fork features never edit the wait_agent tool itself.

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"time"

	"charm.land/fantasy"
	"charm.land/fantasy/schema"
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
	waitAgentToolName          = "wait_agent"
	maxWaitAgentToolCalls      = 20
	maxWaitAgentToolCallBytes  = 2 << 10
	waitAgentToolCallScanBatch = 50
	maxWaitAgentToolCallScan   = 200
)

type forkWaitAgentArgs struct {
	IncludeToolCalls int `json:"include_tool_calls,omitempty" description:"Also return the child's last tool calls, up to this many, 1 to 20, oldest first. The default 0 returns none."`
}

type waitAgentToolCall struct {
	ToolName  string          `json:"tool_name"`
	Args      json.RawMessage `json:"args,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

// forkWaitAgentTool is wait_agent with the include_tool_calls option. It
// passes every call to the upstream tool, which ignores the extra field.
type forkWaitAgentTool struct {
	fantasy.AgentTool
	server *Server
	chat   database.Chat
}

// withForkWaitAgent replaces the wait_agent tool in tools, if there is one,
// with forkWaitAgentTool.
func (p *Server) withForkWaitAgent(tools []fantasy.AgentTool, chat database.Chat) []fantasy.AgentTool {
	for i, tool := range tools {
		if tool.Info().Name == waitAgentToolName {
			tools[i] = forkWaitAgentTool{AgentTool: tool, server: p, chat: chat}
		}
	}
	return tools
}

func (t forkWaitAgentTool) Info() fantasy.ToolInfo {
	info := t.AgentTool.Info()
	info.Description += " Set include_tool_calls to also see the child's last tool calls."
	info.Parameters = maps.Clone(info.Parameters)
	maps.Copy(info.Parameters, schema.ToParameters(schema.Generate(reflect.TypeFor[forkWaitAgentArgs]())))
	return info
}

func (t forkWaitAgentTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	var args struct {
		forkWaitAgentArgs
		ChatID string `json:"chat_id"`
	}
	if err := json.Unmarshal([]byte(call.Input), &args); err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("invalid parameters: %s", err)), nil
	}
	if args.IncludeToolCalls < 0 || args.IncludeToolCalls > maxWaitAgentToolCalls {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("include_tool_calls must be between 0 and %d", maxWaitAgentToolCalls)), nil
	}
	resp, err := t.AgentTool.Run(ctx, call)
	if err != nil || resp.IsError || args.IncludeToolCalls == 0 {
		return resp, err
	}
	// The upstream tool answered without an error, so chat_id is a child
	// this chat may wait on.
	childID, err := uuid.Parse(args.ChatID)
	if err != nil {
		return resp, nil
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(resp.Content), &result); err != nil {
		return resp, nil
	}
	calls, err := t.server.lastToolCalls(ctx, t.chat, childID, args.IncludeToolCalls)
	if err != nil {
		result["tool_calls_error"] = err.Error()
	} else {
		result["tool_calls"] = calls
	}
	content, err := json.Marshal(result)
	if err != nil {
		return resp, nil
	}
	resp.Content = string(content)
	return resp, nil
}

// lastToolCalls returns up to n of the last tool calls of the child chat,
// oldest first, each with its result once there is one. It reads as the
// owner of chat, never as chatd, and only the rows both the user and the
// model see, with provider metadata stripped. Long args and results are
// truncated.
func (p *Server) lastToolCalls(ctx context.Context, chat database.Chat, childID uuid.UUID, n int) ([]waitAgentToolCall, error) {
	owner, _, err := httpmw.UserRBACSubject(ctx, p.db, chat.OwnerID, rbac.ScopeAll)
	if err != nil {
		return nil, xerrors.Errorf("load chat owner authorization: %w", err)
	}
	ownerCtx := dbauthz.As(ctx, owner)

	// Rows come newest first, so a call's result is seen before the call.
	results := map[string]codersdk.ChatMessagePart{}
	calls := []waitAgentToolCall{}
	var beforeID int64
	for scanned := 0; scanned < maxWaitAgentToolCallScan; scanned += waitAgentToolCallScanBatch {
		rows, err := p.db.GetChatMessagesByChatIDDescPaginated(ownerCtx, database.GetChatMessagesByChatIDDescPaginatedParams{
			ChatID:   childID,
			BeforeID: beforeID,
			LimitVal: waitAgentToolCallScanBatch,
		})
		if err != nil {
			return nil, xerrors.Errorf("read child chat messages: %w", err)
		}
		for _, row := range rows {
			beforeID = row.ID
			if row.Visibility != database.ChatMessageVisibilityBoth {
				continue
			}
			message := db2sdk.ChatMessage(row)
			for _, part := range slices.Backward(message.Content) {
				switch part.Type {
				case codersdk.ChatMessagePartTypeToolResult:
					results[part.ToolCallID] = part
				case codersdk.ChatMessagePartTypeToolCall:
					entry := waitAgentToolCall{
						ToolName:  part.ToolName,
						Args:      truncateToolCallJSON(part.Args),
						CreatedAt: message.CreatedAt,
					}
					if result, ok := results[part.ToolCallID]; ok {
						entry.Result = truncateToolCallJSON(result.Result)
						entry.IsError = result.IsError
					}
					calls = append(calls, entry)
					if len(calls) == n {
						slices.Reverse(calls)
						return calls, nil
					}
				}
			}
		}
		if len(rows) < waitAgentToolCallScanBatch {
			break
		}
	}
	slices.Reverse(calls)
	return calls, nil
}

// truncateToolCallJSON returns raw unchanged when it fits, and otherwise
// the truncated text as a JSON string, since a cut JSON value is not valid.
func truncateToolCallJSON(raw json.RawMessage) json.RawMessage {
	text, truncated := chatloop.TruncateToolResultText(string(raw), maxWaitAgentToolCallBytes)
	if !truncated {
		return raw
	}
	encoded, err := json.Marshal(text)
	if err != nil {
		return nil
	}
	return encoded
}
