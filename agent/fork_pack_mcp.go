package agent

// This file is fork code: it hands the MCP servers plugin packs declare from
// the context manager, which re-resolves packs as they are installed and
// removed, to the shared MCP engine that starts them.

import (
	"github.com/coder/coder/v2/agent/agentcontext"
	"github.com/coder/coder/v2/agent/x/agentmcp"
)

// setPackMCPSources replaces the pack servers of the MCP engine. The engine
// reloads only when the declarations changed, and never blocks the resolve
// loop that calls this.
func (a *agent) setPackMCPSources(sources []agentcontext.PackMCPSource) {
	a.mcpManager.SetPackSources(packMCPSourcesToAgentMCP(sources))
}

// packMCPSourcesToAgentMCP adapts the context manager's pack declarations to
// the MCP engine's. The types stay separate so agentcontext does not import
// agent/x/agentmcp.
func packMCPSourcesToAgentMCP(sources []agentcontext.PackMCPSource) []agentmcp.PackSource {
	if len(sources) == 0 {
		return nil
	}
	out := make([]agentmcp.PackSource, 0, len(sources))
	for _, source := range sources {
		pack := agentmcp.PackSource{
			ManifestPath:     source.ManifestPath,
			Dir:              source.Dir,
			ExpandPluginRoot: source.Claude,
			Revision:         source.Revision,
			Configs:          make([]agentmcp.PackConfig, 0, len(source.Configs)),
		}
		for _, cfg := range source.Configs {
			pack.Configs = append(pack.Configs, agentmcp.PackConfig{Path: cfg.Path, Inline: cfg.Inline})
		}
		out = append(out, pack)
	}
	return out
}
