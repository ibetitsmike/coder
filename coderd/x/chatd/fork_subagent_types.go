package chatd

// This file is fork code: with the fork-agent-definitions experiment on, the
// subagent tools report each child's stored chat_subagents agent type, so a
// child spawned from a custom agent type reads as that type instead of the
// built-in type its chat mode implies. appendForkTools wraps the upstream
// tools, so with the experiment off their results are unchanged.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	"charm.land/fantasy"
	"github.com/google/uuid"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
)

var storedSubagentTypeToolNames = []string{
	spawnAgentToolName, waitAgentToolName, "message_agent", "interrupt_agent", "list_agents",
}

// storedSubagentTypeTool is a subagent tool of chat whose results report the
// stored agent type of each child they name.
type storedSubagentTypeTool struct {
	fantasy.AgentTool
	server *Server
	chatID uuid.UUID
}

// withStoredSubagentTypes wraps the subagent tools of chat in tools with
// storedSubagentTypeTool.
func (p *Server) withStoredSubagentTypes(tools []fantasy.AgentTool, chat database.Chat) []fantasy.AgentTool {
	if !p.agentDefinitionsEnabled() {
		return tools
	}
	for i, tool := range tools {
		if slices.Contains(storedSubagentTypeToolNames, tool.Info().Name) {
			tools[i] = storedSubagentTypeTool{AgentTool: tool, server: p, chatID: chat.ID}
		}
	}
	return tools
}

func (t storedSubagentTypeTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	resp, err := t.AgentTool.Run(ctx, call)
	if err != nil {
		return resp, err
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal([]byte(resp.Content), &result); err != nil {
		return resp, nil
	}
	fdb, err := forkdb.From(t.server.db)
	if err != nil {
		return resp, nil
	}
	var changed bool
	if _, ok := result["agents"]; ok {
		if changed, err = t.setListedAgentTypes(ctx, fdb, result); err != nil {
			return resp, nil
		}
	} else {
		changed = t.server.setStoredSubagentType(ctx, fdb, result)
	}
	if !changed {
		return resp, nil
	}
	content, err := json.Marshal(result)
	if err != nil {
		return resp, nil
	}
	resp.Content = string(content)
	return resp, nil
}

// setStoredSubagentType sets the type of entry to the stored agent type of
// the chat its chat_id names, and reports whether the type changed. Entries
// without a type, and chats without a stored agent type, are left as they are.
func (p *Server) setStoredSubagentType(ctx context.Context, fdb forkdb.Store, entry map[string]json.RawMessage) bool {
	if _, ok := entry["type"]; !ok {
		return false
	}
	var rawChatID string
	if err := json.Unmarshal(entry["chat_id"], &rawChatID); err != nil {
		return false
	}
	chatID, err := parseSubagentToolChatID(rawChatID)
	if err != nil {
		return false
	}
	row, err := fdb.GetChatSubagentByChatID(ctx, chatID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			p.logger.Warn(ctx, "read stored subagent type", slog.F("chat_id", chatID), slog.Error(err))
		}
		return false
	}
	agentType, err := json.Marshal(row.AgentType)
	if err != nil || bytes.Equal(agentType, entry["type"]) {
		return false
	}
	entry["type"] = agentType
	return true
}

// setListedAgentTypes sets the type of each entry of a list_agents agents
// list to its stored agent type, read for all children of the chat in one
// query, and reports whether a type changed. An entry that is not a child,
// which a list of a descendant's children or of the subtree has, is read on
// its own.
func (t storedSubagentTypeTool) setListedAgentTypes(ctx context.Context, fdb forkdb.Store, result map[string]json.RawMessage) (bool, error) {
	var agents []map[string]json.RawMessage
	if err := json.Unmarshal(result["agents"], &agents); err != nil {
		return false, err
	}
	rows, err := fdb.GetChatSubagentsByParentChatID(ctx, t.chatID)
	if err != nil {
		t.server.logger.Warn(ctx, "read stored subagent types", slog.F("chat_id", t.chatID), slog.Error(err))
		return false, err
	}
	stored := make(map[string]string, len(rows))
	for _, row := range rows {
		stored[row.ChatID.String()] = row.AgentType
	}
	changed := false
	for _, agent := range agents {
		var chatID string
		if _, ok := agent["type"]; !ok || json.Unmarshal(agent["chat_id"], &chatID) != nil {
			continue
		}
		agentType, ok := stored[chatID]
		if !ok {
			changed = t.server.setStoredSubagentType(ctx, fdb, agent) || changed
			continue
		}
		encoded, err := json.Marshal(agentType)
		if err != nil || bytes.Equal(encoded, agent["type"]) {
			continue
		}
		agent["type"] = encoded
		changed = true
	}
	if !changed {
		return false, nil
	}
	if result["agents"], err = json.Marshal(agents); err != nil {
		return false, err
	}
	return true, nil
}
