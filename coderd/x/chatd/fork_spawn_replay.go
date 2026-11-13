package chatd

// This file is fork code: it makes a replayed spawn_agent call return the
// child it already created, behind the fork-nested-subagents experiment.
// When chatd stops in the middle of a tool batch, the next worker runs the
// whole batch again with the same tool call IDs. Without this wrapper each
// spawn_agent call in that batch would create a second child.

import (
	"context"
	"database/sql"
	"errors"

	"charm.land/fantasy"
	"github.com/google/uuid"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

// forkSpawnChildIDKey carries the child chat ID that a spawn_agent call
// derives from its tool call ID.
type forkSpawnChildIDKey struct{}

// forkReplaySafeSpawnTool wraps spawn_agent so that a call with a tool call
// ID creates its child with an ID derived from that tool call ID, and a
// replay of the call returns that child instead of creating another one.
type forkReplaySafeSpawnTool struct {
	fantasy.AgentTool
	server *Server
	chat   database.Chat
}

// withForkReplaySafeSpawn wraps every spawn_agent tool in tools with
// forkReplaySafeSpawnTool.
func (p *Server) withForkReplaySafeSpawn(tools []fantasy.AgentTool, chat database.Chat) []fantasy.AgentTool {
	if !p.nestedSubagentsEnabled() {
		return tools
	}
	for i, tool := range tools {
		if tool.Info().Name == spawnAgentToolName {
			tools[i] = forkReplaySafeSpawnTool{AgentTool: tool, server: p, chat: chat}
		}
	}
	return tools
}

func (t forkReplaySafeSpawnTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	callID, ok := workspacesdk.ToolCallIDFromContext(ctx)
	if !ok {
		return t.AgentTool.Run(ctx, call)
	}
	childID := uuid.NewSHA1(callID, []byte(spawnAgentToolName))
	child, err := t.server.db.GetChatByID(ctx, childID)
	switch {
	case err == nil && child.ParentChatID.Valid && child.ParentChatID.UUID == t.chat.ID:
		return toolJSONResponse(subagentChatResult(child)), nil
	case err == nil:
		return fantasy.NewTextErrorResponse("spawn_agent: the child chat ID of this call is taken by another chat"), nil
	case !errors.Is(err, sql.ErrNoRows):
		return fantasy.ToolResponse{}, err
	}
	return t.AgentTool.Run(context.WithValue(ctx, forkSpawnChildIDKey{}, childID), call)
}
