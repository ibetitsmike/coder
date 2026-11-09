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
	"golang.org/x/xerrors"

	aidmcp "github.com/coder/coder/v2/aibridge/mcp"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatloop"
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
// Grep and Glob become shell searches, as in docs/fork/pstack-adapter.md.
var customAgentToolAliases = map[string][]string{
	"Read":      {"read_file"},
	"Write":     {"write_file"},
	"Edit":      {"edit_files"},
	"MultiEdit": {"edit_files"},
	"Bash":      {"execute", "process_output"},
	"Grep":      {"read_file", "execute", "process_output"},
	"Glob":      {"read_file", "execute", "process_output"},
	"WebSearch": {"web_search"},
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
// name of its server.
func customAgentMCPServer(tool fantasy.AgentTool) (string, bool) {
	if namer, ok := tool.(interface{ ServerName() string }); ok {
		return strings.TrimSpace(namer.ServerName()), true
	}
	if _, ok := tool.(mcpclient.MCPToolIdentifier); ok {
		// mcpclient names the tools of org, inline and internal servers
		// alike <sanitized server slug>__<tool>.
		server, _, found := strings.Cut(tool.Info().Name, "__")
		if !found {
			return "", true
		}
		return server, true
	}
	return "", false
}

func (policy customAgentToolPolicy) allowsMCPServer(server string) bool {
	switch workspacesdk.AgentDefinitionMCPMode(policy.MCPMode) {
	case workspacesdk.AgentDefinitionMCPNone:
		return false
	case workspacesdk.AgentDefinitionMCPNamed:
		return server != "" && slices.ContainsFunc(policy.MCPServers, func(name string) bool {
			name = strings.TrimSpace(name)
			return strings.EqualFold(name, server) || strings.EqualFold(aidmcp.SanitizeToolName(name), server)
		})
	default:
		return true
	}
}

// filter returns the tools, deferred MCP candidates and provider tools that
// policy allows, in their original order. readonly keeps web_search, the
// provider tool that Explore children keep.
func (policy customAgentToolPolicy) filter(
	tools []fantasy.AgentTool,
	candidates []deferredMCPTool,
	providerTools []chatloop.ProviderTool,
) ([]fantasy.AgentTool, []deferredMCPTool, []chatloop.ProviderTool) {
	allowed := customAgentToolNames(policy.Tools)
	disallowed := customAgentToolNames(policy.DisallowedTools)
	keepNonMCP := func(name string) bool {
		return !disallowed[name] && (policy.Tools == nil || allowed[name])
	}
	keep := func(tool fantasy.AgentTool) bool {
		name := tool.Info().Name
		if disallowed[name] || (policy.ReadOnly && !exploreToolAllowed(tool)) {
			return false
		}
		if server, isMCP := customAgentMCPServer(tool); isMCP {
			return policy.allowsMCPServer(server)
		}
		return keepNonMCP(name)
	}
	tools = slices.DeleteFunc(slices.Clone(tools), func(tool fantasy.AgentTool) bool {
		return !keep(tool)
	})
	candidates = slices.DeleteFunc(slices.Clone(candidates), func(candidate deferredMCPTool) bool {
		return !keep(candidate.tool)
	})
	providerTools = slices.DeleteFunc(slices.Clone(providerTools), func(tool chatloop.ProviderTool) bool {
		name := tool.Definition.GetName()
		return (policy.ReadOnly && name != "web_search") || !keepNonMCP(name)
	})
	return tools, candidates, providerTools
}

// applyCustomAgentToolPolicy filters the turn's tools, deferred MCP
// candidates and provider tools by the tool policy stored when chat was
// spawned. A policy that cannot be read fails the turn instead of running it
// unrestricted.
func (p *Server) applyCustomAgentToolPolicy(
	ctx context.Context,
	chat database.Chat,
	tools []fantasy.AgentTool,
	candidates []deferredMCPTool,
	providerTools []chatloop.ProviderTool,
) ([]fantasy.AgentTool, []deferredMCPTool, []chatloop.ProviderTool, error) {
	if !p.agentDefinitionsEnabled() || !chat.ParentChatID.Valid {
		return tools, candidates, providerTools, nil
	}
	fdb, err := forkdb.From(p.db)
	if err != nil {
		return nil, nil, nil, xerrors.Errorf("fork queries: %w", err)
	}
	row, err := fdb.GetChatSubagentByChatID(ctx, chat.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return tools, candidates, providerTools, nil
	}
	if err != nil {
		return nil, nil, nil, xerrors.Errorf("get tool policy of chat %s: %w", chat.ID, err)
	}
	if !row.ToolPolicy.Valid {
		return tools, candidates, providerTools, nil
	}
	var policy customAgentToolPolicy
	if err := json.Unmarshal(row.ToolPolicy.RawMessage, &policy); err != nil {
		return nil, nil, nil, xerrors.Errorf("decode tool policy of chat %s: %w", chat.ID, err)
	}
	switch workspacesdk.AgentDefinitionMCPMode(policy.MCPMode) {
	case workspacesdk.AgentDefinitionMCPInherit, workspacesdk.AgentDefinitionMCPNone, workspacesdk.AgentDefinitionMCPNamed:
	default:
		return nil, nil, nil, xerrors.Errorf("tool policy of chat %s has unknown mcp mode %q", chat.ID, policy.MCPMode)
	}
	tools, candidates, providerTools = policy.filter(tools, candidates, providerTools)
	return tools, candidates, providerTools, nil
}
