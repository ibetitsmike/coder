package chatd

// This file is fork code: it enforces a custom agent's readonly, tools,
// disallowed_tools and mcp fields behind the fork-agent-definitions
// experiment. Spawn stores the policy on the child's chat_subagents row, and
// a root chat created as an agent type on its own row. Upstream's
// buildTurnEnvironment loads it with customAgentToolPolicyOfChat and filters
// with applyCustomAgentToolPolicy. Both change nothing when the experiment is
// off or the chat has no row.

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

	aidmcp "github.com/coder/coder/v2/aibridge/mcp"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatloop"
	"github.com/coder/coder/v2/coderd/x/chatd/chattool"
	"github.com/coder/coder/v2/coderd/x/chatd/mcpclient"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

// customAgentToolPolicy is the tool policy of a custom agent child, stored
// as JSON in chat_subagents.tool_policy.
type customAgentToolPolicy struct {
	// ReadOnly limits the child to the explore allowlist without its shell
	// tools, which can write files.
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
// Grep and Glob become shell searches and Task the subagent tools, as in
// docs/fork/pstack-adapter.md.
var customAgentToolAliases = map[string][]string{
	"Read":      {"read_file"},
	"Write":     {"write_file"},
	"Edit":      {"edit_files"},
	"MultiEdit": {"edit_files"},
	"Bash":      {"execute", "process_output"},
	"Grep":      {"read_file", "execute", "process_output"},
	"Glob":      {"read_file", "execute", "process_output"},
	"WebSearch": {"web_search"},
	"Task": {
		spawnAgentToolName, "wait_agent", "message_agent", "interrupt_agent", "list_agents",
		listSubagentModelsToolName, messageFamilyChatToolName,
	},
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
// <server>__<tool>, sanitized and capped the way chatd exposes MCP tools.
func customAgentToolNames(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, name := range names {
		out[name] = true
		if tool, ok := strings.CutPrefix(name, "mcp__"); ok {
			tool = aidmcp.SanitizeToolName(tool)
			out[tool[:min(len(tool), aidmcp.MaxToolNameLen)]] = true
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
		return server != "" && slices.Contains(policy.MCPServers, server)
	default:
		return true
	}
}

func (policy customAgentToolPolicy) allowsNonMCPTool(name string) bool {
	return !customAgentToolNames(policy.DisallowedTools)[name] &&
		(policy.Tools == nil || customAgentToolNames(policy.Tools)[name])
}

// keepsOrchestrationTools reports whether policy leaves the child the tools
// that the subagent orchestration guidance tells it to call.
func (policy customAgentToolPolicy) keepsOrchestrationTools() bool {
	return !policy.ReadOnly && !slices.ContainsFunc([]string{spawnAgentToolName, "wait_agent", "list_agents"}, func(name string) bool {
		return !policy.allowsNonMCPTool(name)
	})
}

// keepsSkillTools reports whether policy leaves the child read_skill, which
// the skill index tells it to call. readonly keeps it.
func (policy customAgentToolPolicy) keepsSkillTools() bool {
	return policy.allowsNonMCPTool("read_skill")
}

// keepsWorkspaceCreation reports whether policy leaves the tools that the
// detached workspace guidance tells a root chat to call.
func (policy customAgentToolPolicy) keepsWorkspaceCreation() bool {
	return !policy.ReadOnly && policy.allowsNonMCPTool("list_templates") && policy.allowsNonMCPTool("create_workspace")
}

// keepsWorkspaceFileTools reports whether policy leaves the tools that the
// attached workspace guidance names.
func (policy customAgentToolPolicy) keepsWorkspaceFileTools() bool {
	return !policy.ReadOnly && !slices.ContainsFunc([]string{"execute", "read_file", "write_file"}, func(name string) bool {
		return !policy.allowsNonMCPTool(name)
	})
}

// keepsACPTools reports whether tools, as policy filters them, hold every
// ACP tool that the ACP subagent guidance names. A nil policy keeps tools.
// It knows no MCP server slugs, so it only checks non-MCP tools, which the
// system prompt must check before the policy filters the turn's tools.
func (policy *customAgentToolPolicy) keepsACPTools(tools []fantasy.AgentTool) bool {
	if policy != nil {
		tools, _, _ = policy.filter(tools, nil, nil, nil)
	}
	return !slices.ContainsFunc([]string{"acp_spawn_agent", "acp_wait_agent", "acp_message_agent", "acp_interrupt_agent"}, func(name string) bool {
		return !slices.ContainsFunc(tools, func(tool fantasy.AgentTool) bool { return tool.Info().Name == name })
	})
}

// keepDynamicToolNames deletes from names each dynamic tool that policy
// removed from tools, so that a call to it fails as an unknown tool instead
// of waiting for the client to run it. A nil policy keeps names.
func (policy *customAgentToolPolicy) keepDynamicToolNames(names map[string]bool, tools []fantasy.AgentTool) {
	if policy == nil {
		return
	}
	for name := range names {
		if !slices.ContainsFunc(tools, func(tool fantasy.AgentTool) bool { return tool.Info().Name == name }) {
			delete(names, name)
		}
	}
}

// customAgentReadOnlyToolAllowed reports whether a readonly child keeps
// tool: an explore tool other than execute and process_output, because chatd
// cannot limit a shell command to reads.
func customAgentReadOnlyToolAllowed(tool fantasy.AgentTool) bool {
	switch tool.Info().Name {
	case "execute", "process_output":
		return false
	}
	return exploreToolAllowed(tool)
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
	disallowed := customAgentToolNames(policy.DisallowedTools)
	keepNonMCP := policy.allowsNonMCPTool
	keep := func(tool fantasy.AgentTool) bool {
		name := tool.Info().Name
		if disallowed[name] || (policy.ReadOnly && !customAgentReadOnlyToolAllowed(tool)) {
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
		// find_tools is built after this filter, and only when candidates
		// remain, so denying it leaves the MCP tools undeferred instead.
		return disallowed[chattool.FindToolsName] || !keep(candidate.tool)
	})
	providerTools = slices.DeleteFunc(slices.Clone(providerTools), func(tool chatloop.ProviderTool) bool {
		name := tool.Definition.GetName()
		return (policy.ReadOnly && name != "web_search") || !keepNonMCP(name)
	})
	return tools, candidates, providerTools
}

// customAgentToolPolicyOfChat returns the tool policy stored when chat was
// spawned, or when a root chat was created as an agent type, or nil when
// there is none. A policy that cannot be read fails the turn instead of
// running it unrestricted.
func (p *Server) customAgentToolPolicyOfChat(ctx context.Context, chat database.Chat) (*customAgentToolPolicy, error) {
	if !p.agentDefinitionsEnabled() {
		return nil, nil
	}
	fdb, err := forkdb.From(p.db)
	if err != nil {
		return nil, xerrors.Errorf("fork queries: %w", err)
	}
	row, err := fdb.GetChatSubagentByChatID(ctx, chat.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, xerrors.Errorf("get tool policy of chat %s: %w", chat.ID, err)
	}
	if !row.ToolPolicy.Valid {
		return nil, nil
	}
	var policy customAgentToolPolicy
	if err := json.Unmarshal(row.ToolPolicy.RawMessage, &policy); err != nil {
		return nil, xerrors.Errorf("decode tool policy of chat %s: %w", chat.ID, err)
	}
	switch workspacesdk.AgentDefinitionMCPMode(policy.MCPMode) {
	case workspacesdk.AgentDefinitionMCPInherit, workspacesdk.AgentDefinitionMCPNone, workspacesdk.AgentDefinitionMCPNamed:
	default:
		return nil, xerrors.Errorf("tool policy of chat %s has unknown mcp mode %q", chat.ID, policy.MCPMode)
	}
	return &policy, nil
}

// applyCustomAgentToolPolicy filters the turn's tools, deferred MCP
// candidates and provider tools of chat by policy, from
// customAgentToolPolicyOfChat. A nil policy keeps them all.
func (p *Server) applyCustomAgentToolPolicy(
	ctx context.Context,
	chat database.Chat,
	policy *customAgentToolPolicy,
	tools []fantasy.AgentTool,
	candidates []deferredMCPTool,
	providerTools []chatloop.ProviderTool,
) ([]fantasy.AgentTool, []deferredMCPTool, []chatloop.ProviderTool, error) {
	if policy == nil {
		return tools, candidates, providerTools, nil
	}
	var mcpServerSlugs map[uuid.UUID]string
	if workspacesdk.AgentDefinitionMCPMode(policy.MCPMode) == workspacesdk.AgentDefinitionMCPNamed {
		var err error
		mcpServerSlugs, err = p.customAgentMCPServerSlugs(ctx, chat, tools, candidates)
		if err != nil {
			return nil, nil, nil, err
		}
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
