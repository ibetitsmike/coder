package chatd

// This file is fork code: it enforces a custom agent's readonly, tools,
// disallowed_tools and mcp fields behind the fork-agent-definitions
// experiment. Spawn stores the policy on the child's chat_subagents row, and
// upstream's buildTurnEnvironment calls applyCustomAgentToolPolicy, which
// changes nothing when the experiment is off or the chat is a root chat.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/mcpclient"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

// customAgentToolPolicy is the tool policy of a custom agent child, stored
// as JSON in chat_subagents.tool_policy.
type customAgentToolPolicy struct {
	// ReadOnly limits the child to the explore allowlist.
	ReadOnly bool `json:"read_only"`
	// Tools allows only the listed non-MCP tools. Nil means no allowlist;
	// an empty list allows no non-MCP tools.
	Tools           []string `json:"tools"`
	DisallowedTools []string `json:"disallowed_tools"`
	MCPMode         string   `json:"mcp_mode"`
	// MCPServers names the allowed MCP servers when MCPMode is named.
	MCPServers []string `json:"mcp_servers"`
}

// customAgentToolAliases maps Claude Code tool names to chatd tool names.
var customAgentToolAliases = map[string][]string{
	"Read":      {"read_file"},
	"Write":     {"write_file"},
	"Edit":      {"edit_files"},
	"MultiEdit": {"edit_files"},
	"Bash":      {"execute", "process_output"},
}

func customAgentToolPolicyFromDefinition(def workspacesdk.AgentDefinition) customAgentToolPolicy {
	return customAgentToolPolicy{
		ReadOnly:        def.ReadOnly,
		Tools:           def.Tools,
		DisallowedTools: def.DisallowedTools,
		MCPMode:         string(def.MCPMode),
		MCPServers:      def.MCPServers,
	}
}

func customAgentToolNames(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, name := range names {
		out[name] = true
		for _, alias := range customAgentToolAliases[name] {
			out[alias] = true
		}
	}
	return out
}

// customAgentMCPServer reports whether tool is an MCP tool and, if so, the
// name of its server. An MCP tool whose config ID is not in mcpServerSlugs
// has no name.
func customAgentMCPServer(tool fantasy.AgentTool, mcpServerSlugs map[uuid.UUID]string) (string, bool) {
	if identified, ok := tool.(mcpclient.MCPToolIdentifier); ok {
		return mcpServerSlugs[identified.MCPServerConfigID()], true
	}
	if namer, ok := tool.(interface{ ServerName() string }); ok {
		return strings.TrimSpace(namer.ServerName()), true
	}
	return "", false
}

// filter returns the tools and deferred MCP candidates that policy allows,
// in their original order. mcpServerSlugs is only read in named mode.
func (policy customAgentToolPolicy) filter(
	tools []fantasy.AgentTool,
	candidates []deferredMCPTool,
	mcpServerSlugs map[uuid.UUID]string,
) ([]fantasy.AgentTool, []deferredMCPTool) {
	allowed := customAgentToolNames(policy.Tools)
	disallowed := customAgentToolNames(policy.DisallowedTools)
	keep := func(tool fantasy.AgentTool) bool {
		name := tool.Info().Name
		if disallowed[name] || (policy.ReadOnly && !exploreToolAllowed(tool)) {
			return false
		}
		server, isMCP := customAgentMCPServer(tool, mcpServerSlugs)
		if !isMCP {
			return policy.Tools == nil || allowed[name]
		}
		switch workspacesdk.AgentDefinitionMCPMode(policy.MCPMode) {
		case workspacesdk.AgentDefinitionMCPNone:
			return false
		case workspacesdk.AgentDefinitionMCPNamed:
			return server != "" && slices.ContainsFunc(policy.MCPServers, func(name string) bool {
				return strings.EqualFold(strings.TrimSpace(name), server)
			})
		default:
			return true
		}
	}
	tools = slices.DeleteFunc(slices.Clone(tools), func(tool fantasy.AgentTool) bool {
		return !keep(tool)
	})
	candidates = slices.DeleteFunc(slices.Clone(candidates), func(candidate deferredMCPTool) bool {
		return !keep(candidate.tool)
	})
	return tools, candidates
}

// applyCustomAgentToolPolicy filters the turn's tools and deferred MCP
// candidates by the tool policy stored when chat was spawned. A policy that
// cannot be read fails the turn instead of running it unrestricted.
func (p *Server) applyCustomAgentToolPolicy(
	ctx context.Context,
	chat database.Chat,
	tools []fantasy.AgentTool,
	candidates []deferredMCPTool,
) ([]fantasy.AgentTool, []deferredMCPTool, error) {
	if !p.agentDefinitionsEnabled() || !chat.ParentChatID.Valid {
		return tools, candidates, nil
	}
	fdb, err := forkdb.From(p.db)
	if err != nil {
		return nil, nil, xerrors.Errorf("fork queries: %w", err)
	}
	row, err := fdb.GetChatSubagentByChatID(ctx, chat.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return tools, candidates, nil
	}
	if err != nil {
		return nil, nil, xerrors.Errorf("get tool policy of chat %s: %w", chat.ID, err)
	}
	if !row.ToolPolicy.Valid {
		return tools, candidates, nil
	}
	var policy customAgentToolPolicy
	if err := json.Unmarshal(row.ToolPolicy.RawMessage, &policy); err != nil {
		return nil, nil, xerrors.Errorf("decode tool policy of chat %s: %w", chat.ID, err)
	}
	var mcpServerSlugs map[uuid.UUID]string
	switch workspacesdk.AgentDefinitionMCPMode(policy.MCPMode) {
	case workspacesdk.AgentDefinitionMCPInherit, workspacesdk.AgentDefinitionMCPNone:
	case workspacesdk.AgentDefinitionMCPNamed:
		mcpServerSlugs, err = p.customAgentMCPServerSlugs(ctx, chat, tools, candidates)
		if err != nil {
			return nil, nil, err
		}
	default:
		return nil, nil, xerrors.Errorf("tool policy of chat %s has unknown mcp mode %q", chat.ID, policy.MCPMode)
	}
	tools, candidates = policy.filter(tools, candidates, mcpServerSlugs)
	return tools, candidates, nil
}

// customAgentMCPServerSlugs returns the server slug of every MCP server
// config that tools and candidates come from.
func (p *Server) customAgentMCPServerSlugs(
	ctx context.Context,
	chat database.Chat,
	tools []fantasy.AgentTool,
	candidates []deferredMCPTool,
) (map[uuid.UUID]string, error) {
	var ids []uuid.UUID
	addID := func(tool fantasy.AgentTool) {
		if identified, ok := tool.(mcpclient.MCPToolIdentifier); ok && !slices.Contains(ids, identified.MCPServerConfigID()) {
			ids = append(ids, identified.MCPServerConfigID())
		}
	}
	for _, tool := range tools {
		addID(tool)
	}
	for _, candidate := range candidates {
		addID(candidate.tool)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	configs, err := enabledMCPServerConfigsForChatOrg(ctx, p.db, chat.OrganizationID, ids)
	if err != nil {
		return nil, xerrors.Errorf("resolve MCP servers of chat %s: %w", chat.ID, err)
	}
	slugs := make(map[uuid.UUID]string, len(configs))
	for _, config := range configs {
		slugs[config.ID] = config.Slug
	}
	return slugs, nil
}
