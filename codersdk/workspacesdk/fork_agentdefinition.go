package workspacesdk

// This file is fork code: it parses custom agent definition files for the
// fork's custom subagent types, which upstream does not support.

import (
	"fmt"
	"slices"
	"strings"

	"golang.org/x/xerrors"
)

// AgentDefinition is a custom subagent type parsed from a Claude Code or
// Cursor agent markdown file.
type AgentDefinition struct {
	// Name is the kebab-case ID used as the spawn_agent type.
	Name        string
	Description string
	// Prompt is the markdown body after the front-matter, with HTML
	// comments stripped and surrounding whitespace trimmed.
	Prompt string
	// Model is empty when the definition inherits the parent's model.
	Model           string
	ReasoningEffort string
	// Tools is the tool allowlist; nil means every tool of the base type.
	Tools           []string
	DisallowedTools []string
	ReadOnly        bool
	MCPMode         AgentDefinitionMCPMode
	// MCPServers names the allowed MCP servers when MCPMode is
	// AgentDefinitionMCPNamed.
	MCPServers []string
	// BaseType is AgentDefinitionBaseGeneral or AgentDefinitionBaseExplore.
	BaseType string
	// Isolation is the spawn_agent isolation a spawn of this agent uses when
	// the call sets none: "shared", "worktree" or "workspace". Empty means
	// shared.
	Isolation string
	// Warnings lists non-fatal problems, sorted.
	Warnings []string
}

// AgentDefinitionMCPMode selects which MCP servers a custom agent can use.
type AgentDefinitionMCPMode string

const (
	// AgentDefinitionMCPInherit gives the agent the parent's MCP servers.
	AgentDefinitionMCPInherit AgentDefinitionMCPMode = "inherit"
	// AgentDefinitionMCPNone gives the agent no MCP servers.
	AgentDefinitionMCPNone AgentDefinitionMCPMode = "none"
	// AgentDefinitionMCPNamed limits the agent to the servers in MCPServers.
	AgentDefinitionMCPNamed AgentDefinitionMCPMode = "named"
)

const (
	// AgentDefinitionBaseGeneral is the default base subagent type.
	AgentDefinitionBaseGeneral = "general"
	// AgentDefinitionBaseExplore is the read-only exploration base type.
	AgentDefinitionBaseExplore = "explore"
)

// ErrNotAgentDefinition is returned by ParseAgentDefinition for content
// without front-matter, such as a README in an agents directory.
var ErrNotAgentDefinition = xerrors.New("file has no front-matter")

var agentDefinitionKnownKeys = map[string]struct{}{
	"name":             {},
	"description":      {},
	"model":            {},
	"reasoning_effort": {},
	"effort":           {},
	"tools":            {},
	"disallowed_tools": {},
	"disallowedTools":  {},
	"readonly":         {},
	"mcp":              {},
	"mcpServers":       {},
	"base_type":        {},
	"isolation":        {},
}

// agentDefinitionIsolations are the values of the isolation key, matching
// the spawn_agent isolation argument.
var agentDefinitionIsolations = []string{"shared", "worktree", "workspace"}

// agentDefinitionIgnoredKeys are known keys from other tools that have no
// Coder equivalent and are dropped without a warning.
var agentDefinitionIgnoredKeys = map[string]struct{}{
	"is_background": {},
	"color":         {},
}

// ParseAgentDefinition parses a Claude Code or Cursor agent markdown file.
// fallbackName is used when the front-matter has no name, and is normally
// the file name without its .md extension. Unknown front-matter keys become
// warnings; known keys with the wrong type are errors.
func ParseAgentDefinition(content, fallbackName string) (AgentDefinition, error) {
	firstLine, _, _ := strings.Cut(strings.TrimPrefix(content, "\xef\xbb\xbf"), "\n")
	if strings.TrimSpace(firstLine) != "---" {
		return AgentDefinition{}, ErrNotAgentDefinition
	}
	fm, body, err := splitSkillFrontmatter(content)
	if err != nil {
		return AgentDefinition{}, err
	}

	def := AgentDefinition{
		Prompt:   body,
		MCPMode:  AgentDefinitionMCPInherit,
		BaseType: AgentDefinitionBaseGeneral,
	}
	var warnings []string
	for key := range fm {
		if _, ok := agentDefinitionKnownKeys[key]; ok {
			continue
		}
		if _, ok := agentDefinitionIgnoredKeys[key]; ok {
			continue
		}
		warnings = append(warnings, fmt.Sprintf("unknown front-matter field %q ignored", key))
	}

	if def.Name, err = agentStringField(fm, "name"); err != nil {
		return AgentDefinition{}, err
	}
	if def.Name == "" {
		def.Name = fallbackName
	}
	if !SkillNamePattern.MatchString(def.Name) {
		return AgentDefinition{}, xerrors.Errorf("agent name %q is not kebab-case", def.Name)
	}
	if def.Description, err = agentStringField(fm, "description"); err != nil {
		return AgentDefinition{}, err
	}
	if def.Description == "" {
		warnings = append(warnings, "missing description")
	}
	if def.Prompt == "" {
		return AgentDefinition{}, xerrors.New("agent definition has no prompt body")
	}
	if def.Model, err = agentStringField(fm, "model"); err != nil {
		return AgentDefinition{}, err
	}
	if def.Model == "inherit" {
		def.Model = ""
	}
	// reasoning_effort wins over Claude Code's effort when both are set.
	for _, key := range []string{"reasoning_effort", "effort"} {
		effort, err := agentStringField(fm, key)
		if err != nil {
			return AgentDefinition{}, err
		}
		if def.ReasoningEffort == "" {
			def.ReasoningEffort = effort
		}
	}
	if def.Tools, err = agentToolListField(fm, "tools"); err != nil {
		return AgentDefinition{}, err
	}
	for _, key := range []string{"disallowed_tools", "disallowedTools"} {
		tools, err := agentToolListField(fm, key)
		if err != nil {
			return AgentDefinition{}, err
		}
		def.DisallowedTools = append(def.DisallowedTools, tools...)
	}
	if v, ok := fm["readonly"]; ok {
		readOnly, isBool := v.(bool)
		if !isBool {
			return AgentDefinition{}, xerrors.New(`front-matter field "readonly" must be a boolean`)
		}
		def.ReadOnly = readOnly
	}
	mcpWarnings, err := parseAgentMCP(fm, &def)
	if err != nil {
		return AgentDefinition{}, err
	}
	warnings = append(warnings, mcpWarnings...)
	baseType, err := agentStringField(fm, "base_type")
	if err != nil {
		return AgentDefinition{}, err
	}
	switch baseType {
	case "":
	case AgentDefinitionBaseGeneral, AgentDefinitionBaseExplore:
		def.BaseType = baseType
	default:
		return AgentDefinition{}, xerrors.Errorf("front-matter field \"base_type\" must be %q or %q, got %q", AgentDefinitionBaseGeneral, AgentDefinitionBaseExplore, baseType)
	}
	if def.Isolation, err = agentStringField(fm, "isolation"); err != nil {
		return AgentDefinition{}, err
	}
	if def.Isolation != "" && !slices.Contains(agentDefinitionIsolations, def.Isolation) {
		return AgentDefinition{}, xerrors.Errorf("front-matter field \"isolation\" must be one of %s, got %q", strings.Join(agentDefinitionIsolations, ", "), def.Isolation)
	}

	slices.Sort(warnings)
	def.Warnings = warnings
	return def, nil
}

// parseAgentMCP sets def.MCPMode and def.MCPServers from the mcp key. The
// Claude Code mcpServers key only adds servers to the inherited set, so named
// references keep the inherit mode. Inline server definitions are rejected:
// they often hold credentials in env or headers, and the workspace agent ships
// the whole definition file to coderd.
func parseAgentMCP(fm map[string]any, def *AgentDefinition) (warnings []string, err error) {
	mcp, hasMCP := fm["mcp"]
	servers, hasServers := fm["mcpServers"]
	if hasServers && servers != nil {
		switch v := servers.(type) {
		case []any:
			for _, entry := range v {
				if _, ok := entry.(string); !ok {
					return nil, errInlineMCPServers
				}
			}
		case map[string]any:
			return nil, errInlineMCPServers
		default:
			return nil, xerrors.New(`front-matter field "mcpServers" must be a list of server names`)
		}
	}
	if hasMCP && mcp != nil {
		if hasServers {
			warnings = append(warnings, "mcpServers ignored because mcp is set")
		}
		switch v := mcp.(type) {
		case string:
			switch AgentDefinitionMCPMode(strings.TrimSpace(v)) {
			case AgentDefinitionMCPInherit:
				def.MCPMode = AgentDefinitionMCPInherit
			case AgentDefinitionMCPNone:
				def.MCPMode = AgentDefinitionMCPNone
			default:
				return nil, xerrors.Errorf("front-matter field \"mcp\" must be %q, %q, or a list of server names, got %q", AgentDefinitionMCPInherit, AgentDefinitionMCPNone, v)
			}
		case []any:
			names, err := agentStringList(v, "mcp")
			if err != nil {
				return nil, err
			}
			def.MCPMode = AgentDefinitionMCPNamed
			def.MCPServers = names
		default:
			return nil, xerrors.New(`front-matter field "mcp" must be a string or a list of server names`)
		}
		return warnings, nil
	}
	return warnings, nil
}

var errInlineMCPServers = xerrors.New(`front-matter field "mcpServers" has inline server definitions, which are not supported; list server names instead`)

func agentStringField(fm map[string]any, key string) (string, error) {
	switch v := fm[key].(type) {
	case nil:
		return "", nil
	case string:
		return strings.TrimSpace(v), nil
	default:
		return "", xerrors.Errorf("front-matter field %q must be a string", key)
	}
}

// agentToolListField accepts a YAML list of strings or a single
// comma-separated string such as "Read, Grep, Glob". An absent key
// returns nil.
func agentToolListField(fm map[string]any, key string) ([]string, error) {
	switch v := fm[key].(type) {
	case nil:
		return nil, nil
	case string:
		out := []string{}
		for _, tool := range strings.Split(v, ",") {
			if tool = strings.TrimSpace(tool); tool != "" {
				out = append(out, tool)
			}
		}
		return out, nil
	case []any:
		return agentStringList(v, key)
	default:
		return nil, xerrors.Errorf("front-matter field %q must be a list of strings or a comma-separated string", key)
	}
}

func agentStringList(values []any, key string) ([]string, error) {
	out := make([]string, 0, len(values))
	for _, value := range values {
		s, ok := value.(string)
		if !ok {
			return nil, xerrors.Errorf("front-matter field %q must contain only strings", key)
		}
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}
