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

// customAgentToolNames expands names with their aliases. A Claude Code MCP
// tool ID, mcp__<server>__<tool>, also matches the chatd tool name
// <server>__<tool>.
func customAgentToolNames(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, name := range names {
		out[name] = true
		if tool, ok := strings.CutPrefix(name, "mcp__"); ok {
			out[tool] = true
		}
		for _, alias := range customAgentToolAliases[name] {
			out[alias] = true
		}
	}
	return out
}

// customAgentMCPServer reports whether tool is an MCP tool and, if so, the
// name of its server. An MCP tool whose server ID is not in mcpServerSlugs
// has no name. Workspace server names stay untrimmed, because workspace
// config can hold both "x" and " x " as distinct servers.
func customAgentMCPServer(tool fantasy.AgentTool, mcpServerSlugs map[uuid.UUID]string) (string, bool) {
	if namer, ok := tool.(interface{ ServerName() string }); ok {
		return namer.ServerName(), true
	}
	if identified, ok := tool.(mcpclient.MCPToolIdentifier); ok {
		return mcpServerSlugs[identified.MCPServerConfigID()], true
	}
	return "", false
}

func (policy customAgentToolPolicy) allowsMCPServer(server string) bool {
	switch workspacesdk.AgentDefinitionMCPMode(policy.MCPMode) {
	case workspacesdk.AgentDefinitionMCPNone:
		return false
	case workspacesdk.AgentDefinitionMCPNamed:
		return server != "" && slices.ContainsFunc(policy.MCPServers, func(name string) bool {
			return strings.EqualFold(name, server)
		})
	default:
		return true
	}
}

// filter returns the tools, deferred MCP candidates and provider tools that
// policy allows, in their original order. readonly keeps web_search, the
// provider tool that Explore children keep. mcpServerSlugs is only read in
// named mode.
func (policy customAgentToolPolicy) filter(
	tools []fantasy.AgentTool,
	candidates []deferredMCPTool,
	providerTools []chatloop.ProviderTool,
	mcpServerSlugs map[uuid.UUID]string,
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
		if server, isMCP := customAgentMCPServer(tool, mcpServerSlugs); isMCP {
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
	var mcpServerSlugs map[uuid.UUID]string
	switch workspacesdk.AgentDefinitionMCPMode(policy.MCPMode) {
	case workspacesdk.AgentDefinitionMCPInherit, workspacesdk.AgentDefinitionMCPNone:
	case workspacesdk.AgentDefinitionMCPNamed:
		mcpServerSlugs, err = p.customAgentMCPServerSlugs(ctx, chat, tools, candidates)
		if err != nil {
			return nil, nil, nil, err
		}
	default:
		return nil, nil, nil, xerrors.Errorf("tool policy of chat %s has unknown mcp mode %q", chat.ID, policy.MCPMode)
	}
	tools, candidates, providerTools = policy.filter(tools, candidates, providerTools, mcpServerSlugs)
	return tools, candidates, providerTools, nil
}

// customAgentMCPServerSlugs maps the server ID of every MCP tool in tools and
// candidates to its exact slug: org server configs, the root chat's inline
// servers, and internal servers. Tool names are no substitute, because
// mcpclient sanitizes and truncates them.
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
	slugs := make(map[uuid.UUID]string, len(ids))
	for _, internal := range p.internalMCPServers {
		slugs[internal.id()] = internal.Slug
	}
	configs, err := enabledMCPServerConfigsForChatOrg(ctx, p.db, chat.OrganizationID, ids)
	if err != nil {
		return nil, xerrors.Errorf("resolve MCP servers of chat %s: %w", chat.ID, err)
	}
	for _, config := range configs {
		slugs[config.ID] = config.Slug
	}
	rootChatID := chat.ID
	if chat.RootChatID.Valid {
		rootChatID = chat.RootChatID.UUID
	}
	inline, err := p.db.GetChatMCPServersByChatID(ctx, rootChatID)
	if err != nil {
		return nil, xerrors.Errorf("get inline MCP servers of chat %s: %w", chat.ID, err)
	}
	for _, server := range inline {
		slugs[server.ID] = server.Slug
	}
	return slugs, nil
}
