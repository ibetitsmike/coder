package chatd

// This file is fork code: it adds the fork fields to the list_agents output.
// Upstream's subagent.go passes its description through
// forkListAgentsDescription and its result through forkListAgents once, so
// fork features never edit the list_agents tool itself. With
// fork-nested-subagents on, appendForkTools also wraps list_agents with
// forkListAgentsTool, which lists the children or the whole subtree of a
// descendant.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"charm.land/fantasy"
	"charm.land/fantasy/schema"
	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/httpmw"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/codersdk"
)

type forkListAgentsResult struct {
	subagentListResult
	Agents []forkListAgentsEntry `json:"agents"`
}

type forkListAgentsEntry struct {
	subagentListEntry
	ParentChatID       string              `json:"parent_chat_id,omitempty"`
	Todos              []codersdk.ChatTodo `json:"todos,omitempty"`
	WaitingForCapacity bool                `json:"waiting_for_capacity,omitempty"`
}

// maxListedSubtreeLevels bounds the walk of a recursive list_agents, as the
// depth guard of GetChatSubtreeIDs does, in case the hierarchy has a cycle.
const maxListedSubtreeLevels = 64

type forkListAgentsArgs struct {
	ChatID    string `json:"chat_id,omitempty" description:"List the children of this chat instead of this chat's own: this chat or one of its descendants, by a chat_id from an earlier list_agents result."`
	Recursive bool   `json:"recursive,omitempty" description:"List every descendant of the chat instead of only its children."`
}

// forkListAgentsTool is list_agents with the chat_id and recursive options.
// A call without them goes to the upstream tool unchanged.
type forkListAgentsTool struct {
	fantasy.AgentTool
	server *Server
	chat   database.Chat
}

// withForkListDescendants replaces the list_agents tool in tools, if there is
// one, with forkListAgentsTool.
func (p *Server) withForkListDescendants(tools []fantasy.AgentTool, chat database.Chat) []fantasy.AgentTool {
	for i, tool := range tools {
		if tool.Info().Name == "list_agents" {
			tools[i] = forkListAgentsTool{AgentTool: tool, server: p, chat: chat}
		}
	}
	return tools
}

func (t forkListAgentsTool) Info() fantasy.ToolInfo {
	info := t.AgentTool.Info()
	info.Description += " Set chat_id to list the children of one of your descendants, or recursive to list every " +
		"descendant; each agent then also has parent_chat_id. Only this chat's own subtree can be listed."
	if t.server.experiments.Enabled(codersdk.ExperimentForkChatTodos) {
		info.Description += " Pass a listed chat_id to " + readChatTranscriptToolName + " to read that agent's messages."
	}
	info.Parameters = maps.Clone(info.Parameters)
	maps.Copy(info.Parameters, schema.ToParameters(schema.Generate(reflect.TypeFor[forkListAgentsArgs]())))
	return info
}

func (t forkListAgentsTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	var args struct {
		listAgentsArgs
		forkListAgentsArgs
	}
	if err := json.Unmarshal([]byte(call.Input), &args); err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("invalid parameters: %s", err)), nil
	}
	rawChatID := strings.TrimSpace(args.ChatID)
	if rawChatID == "" && !args.Recursive {
		return t.AgentTool.Run(ctx, call)
	}
	targetID := t.chat.ID
	if rawChatID != "" {
		var err error
		if targetID, err = parseSubagentToolChatID(rawChatID); err != nil {
			return fantasy.NewTextErrorResponse(err.Error()), nil
		}
	}
	if targetID != t.chat.ID {
		descendant, err := isSubagentDescendant(ctx, t.server.db, t.chat.ID, targetID)
		if err != nil {
			return fantasy.NewTextErrorResponse(err.Error()), nil
		}
		if !descendant {
			return fantasy.NewTextErrorResponse("chat_id must be this chat or one of its descendants"), nil
		}
	}
	levels := 1
	if args.Recursive {
		levels = maxListedSubtreeLevels
	}
	chats, err := t.server.listDescendantChats(ctx, targetID, levels)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	limit := defaultListAgentsLimit
	if args.Limit != nil {
		limit = min(max(*args.Limit, 1), maxListAgentsLimit)
	}
	offset := 0
	if args.Offset != nil && *args.Offset > 0 {
		offset = *args.Offset
	}
	result := t.server.forkListAgents(ctx, t.chat, chats, listAgentsResult(chats, offset, limit))
	listed, ok := result.(forkListAgentsResult)
	if !ok {
		return toolJSONResponse(result), nil
	}
	parents := make(map[string]string, len(chats))
	for _, chat := range chats {
		parents[chat.ID.String()] = chat.ParentChatID.UUID.String()
	}
	for i := range listed.Agents {
		listed.Agents[i].ParentChatID = parents[listed.Agents[i].ChatID]
	}
	return toolJSONResponse(listed), nil
}

// listDescendantChats returns the unarchived descendants of chatID up to
// levels below it, reached through unarchived chats, most recently active
// first, as listSubagents orders them.
func (p *Server) listDescendantChats(ctx context.Context, chatID uuid.UUID, levels int) ([]database.Chat, error) {
	var chats []database.Chat
	parents := []uuid.UUID{chatID}
	for level := 0; len(parents) > 0 && level < levels; level++ {
		rows, err := p.db.GetChildChatsByParentIDs(ctx, database.GetChildChatsByParentIDsParams{
			ParentIds: parents,
			Archived:  sql.NullBool{Bool: false, Valid: true},
		})
		if err != nil {
			return nil, xerrors.Errorf("list child agents: %w", err)
		}
		parents = make([]uuid.UUID, 0, len(rows))
		for _, row := range rows {
			chats = append(chats, row.Chat)
			parents = append(parents, row.Chat.ID)
		}
	}
	slices.SortStableFunc(chats, func(a, b database.Chat) int {
		if c := b.UpdatedAt.Compare(a.UpdatedAt); c != 0 {
			return c
		}
		return strings.Compare(b.ID.String(), a.ID.String())
	})
	return chats, nil
}

// forkListAgentsDescription documents the waiting_for_capacity field in
// upstream's list_agents description when nested subagents are enabled.
func (p *Server) forkListAgentsDescription(description string) string {
	if !p.nestedSubagentsEnabled() {
		return description
	}
	return description + " A running agent that waits for a free subagent concurrency slot " +
		"also has waiting_for_capacity: true; the field is absent otherwise."
}

// forkListAgents returns the list_agents result of parent with the fields of
// the enabled fork features. With fork-chat-todos and fork-nested-subagents
// off it returns result as it is, so the output matches upstream. With
// fork-chat-todos on, each listed child carries its todo list, read as the
// owner of parent so the child chats are read with the owner's permissions
// instead of chatd's. With fork-nested-subagents on, each listed child
// reports whether it waits for a fork concurrency slot.
func (p *Server) forkListAgents(ctx context.Context, parent database.Chat, children []database.Chat, result subagentListResult) any {
	todosEnabled := p.experiments.Enabled(codersdk.ExperimentForkChatTodos)
	if !todosEnabled && !p.nestedSubagentsEnabled() {
		return result
	}
	var ownerCtx context.Context
	if todosEnabled {
		owner, _, err := httpmw.UserRBACSubject(ctx, p.db, parent.OwnerID, rbac.ScopeAll)
		if err != nil {
			p.logger.Warn(ctx, "load chat owner authorization for list_agents todos",
				slog.F("chat_id", parent.ID), slog.Error(err))
		} else {
			ownerCtx = dbauthz.As(ctx, owner)
		}
	}
	childrenByID := make(map[string]database.Chat, len(children))
	for _, child := range children {
		childrenByID[child.ID.String()] = child
	}
	agents := make([]forkListAgentsEntry, 0, len(result.Agents))
	for _, entry := range result.Agents {
		forkEntry := forkListAgentsEntry{subagentListEntry: entry}
		if ownerCtx != nil {
			childID, err := uuid.Parse(entry.ChatID)
			if err == nil {
				forkEntry.Todos, _, err = p.readChatTodos(ownerCtx, childID)
			}
			if err != nil {
				p.logger.Warn(ctx, "read subagent todo list for list_agents",
					slog.F("chat_id", parent.ID), slog.F("child_chat_id", entry.ChatID), slog.Error(err))
			}
		}
		if child, ok := childrenByID[entry.ChatID]; ok {
			waiting, _, err := p.forkChatQueuedForCapacity(ctx, child)
			if err != nil {
				p.logger.Warn(ctx, "check subagent capacity wait for list_agents",
					slog.F("chat_id", parent.ID), slog.F("child_chat_id", entry.ChatID), slog.Error(err))
			}
			forkEntry.WaitingForCapacity = waiting
		}
		agents = append(agents, forkEntry)
	}
	return forkListAgentsResult{subagentListResult: result, Agents: agents}
}
