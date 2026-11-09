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
	Todos []codersdk.ChatTodo `json:"todos,omitempty"`
}

// forkListAgents returns the list_agents result of parent with the fields of
// the enabled fork features. With fork-chat-todos off it returns result as it
// is, so the output matches upstream. With it on, each listed child carries
// its todo list, read as the owner of parent so the child chats are read with
// the owner's permissions instead of chatd's. W1 (agent type badge) and W2
// (waiting for capacity) extend this function.
func (p *Server) forkListAgents(ctx context.Context, parent database.Chat, result subagentListResult) any {
	if !p.experiments.Enabled(codersdk.ExperimentForkChatTodos) {
		return result
	}
	owner, _, err := httpmw.UserRBACSubject(ctx, p.db, parent.OwnerID, rbac.ScopeAll)
	if err != nil {
		p.logger.Warn(ctx, "load chat owner authorization for list_agents todos",
			slog.F("chat_id", parent.ID), slog.Error(err))
		return result
	}
	ownerCtx := dbauthz.As(ctx, owner)
	agents := make([]forkListAgentsEntry, 0, len(result.Agents))
	for _, entry := range result.Agents {
		forkEntry := forkListAgentsEntry{subagentListEntry: entry}
		childID, err := uuid.Parse(entry.ChatID)
		if err == nil {
			forkEntry.Todos, _, err = p.readChatTodos(ownerCtx, childID)
		}
		if err != nil {
			p.logger.Warn(ctx, "read subagent todo list for list_agents",
				slog.F("chat_id", parent.ID), slog.F("child_chat_id", entry.ChatID), slog.Error(err))
		}
		agents = append(agents, forkEntry)
	}
	return forkListAgentsResult{subagentListResult: result, Agents: agents}
}
