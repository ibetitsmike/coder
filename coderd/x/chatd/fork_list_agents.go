package chatd

// This file is fork code: it adds the fork fields to the list_agents output.
// Upstream's subagent.go passes its result through forkListAgents once, so
// fork features never edit the list_agents tool itself.

import (
	"context"

	"github.com/google/uuid"

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
	Todos              []codersdk.ChatTodo `json:"todos,omitempty"`
	WaitingForCapacity bool                `json:"waiting_for_capacity,omitempty"`
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
			waiting, err := p.forkChatQueuedForCapacity(ctx, child)
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
