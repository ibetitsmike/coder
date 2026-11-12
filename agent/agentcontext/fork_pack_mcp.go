package agentcontext

// This file is fork code: it finds the MCP servers plugin packs declare, so
// the agent's MCP manager starts them next to the workspace .mcp.json
// servers, and stamps their KindMCPServer resources with the pack marker
// the coderd fork-plugin gate filters on.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// manifestMCPServers is a manifest mcpServers declaration: a config file
// path, an inline map of servers keyed by name, or an array of either. Any
// other shape decodes to no declaration, so a mistyped field of a Cursor or
// root manifest falls back to the default .mcp.json. readPluginManifest
// rejects a Claude manifest whose field is mistyped (claudePathFieldsTyped)
// first.
type manifestMCPServers []manifestMCPDecl

// manifestMCPDecl is one mcpServers entry: a path, or an inline server map.
type manifestMCPDecl struct {
	path   string
	inline json.RawMessage
}

func (d *manifestMCPServers) UnmarshalJSON(data []byte) error {
	*d = nil
	var items []json.RawMessage
	if err := json.Unmarshal(data, &items); err != nil {
		items = []json.RawMessage{data}
	}
	for _, item := range items {
		var path string
		if err := json.Unmarshal(item, &path); err == nil {
			*d = append(*d, manifestMCPDecl{path: path})
			continue
		}
		var servers map[string]json.RawMessage
		if err := json.Unmarshal(item, &servers); err == nil && servers != nil {
			// The decoder's buffer is not ours to keep.
			*d = append(*d, manifestMCPDecl{inline: bytes.Clone(item)})
		}
	}
	return nil
}

// isMCPServersDeclaration reports whether a Claude mcpServers field is
// absent, null, a string, an object, or an array of strings and objects.
func isMCPServersDeclaration(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	items, ok := value.([]any)
	if !ok {
		return value == nil || isMCPServersItem(value)
	}
	for _, item := range items {
		if !isMCPServersItem(item) {
			return false
		}
	}
	return true
}

func isMCPServersItem(value any) bool {
	switch value.(type) {
	case string, map[string]any:
		return true
	default:
		return false
	}
}

// PackMCPSource is the MCP server declarations of one plugin pack, in load
// order. A server name declared later in the pack replaces an earlier one.
type PackMCPSource struct {
	// ManifestPath is the pack marker every pack row carries.
	ManifestPath string
	// Dir is the pack root, which ${CLAUDE_PLUGIN_ROOT} names.
	Dir string
	// Claude marks a Claude Code pack, whose configs expand
	// ${CLAUDE_PLUGIN_ROOT}.
	Claude bool
	// Revision is the pack's commit marker, empty for a pack coderd did
	// not write. It changes when an update replaces the pack's files.
	Revision string
	Configs  []PackMCPConfig
}

// packCommitMarkerName is the file coderd writes into a pack directory
// after all of its files (coderd/x/chatd/fork_pack_materializer.go).
const packCommitMarkerName = ".coder-pack-commit"

// packRevision reads the commit marker of the pack in dir.
func packRevision(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, packCommitMarkerName))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// PackMCPConfig is one mcpServers declaration of a pack.
type PackMCPConfig struct {
	// Path is the declaring file: a config file inside the pack, or the
	// manifest for Inline servers.
	Path string
	// Inline is an inline server map from the manifest; nil means the
	// servers are read from Path.
	Inline json.RawMessage
}

// mcpConfigs returns the pack's MCP declarations in load order. A Claude
// pack loads .mcp.json at its root and then each declared entry, the way
// Claude Code merges them
// (https://code.claude.com/docs/en/plugins/manifest-reference#mcpservers);
// any other manifest's declaration replaces the default file, as its skills
// declaration does. Like a skill or agent path, a declared path must name a
// file inside the pack, or that entry loads nothing. MCP bundles and bundle
// URLs are not supported, so their entries load nothing either.
func (p packRoot) mcpConfigs() []PackMCPConfig {
	var out []PackMCPConfig
	if p.Claude || len(p.Manifest.MCPServers) == 0 {
		out = p.appendMCPFile(out, mcpConfigFileName)
	}
	for _, decl := range p.Manifest.MCPServers {
		if decl.inline != nil {
			out = append(out, PackMCPConfig{Path: p.ManifestPath, Inline: decl.inline})
			continue
		}
		out = p.appendMCPFile(out, decl.path)
	}
	return out
}

func (p packRoot) appendMCPFile(out []PackMCPConfig, declared string) []PackMCPConfig {
	rel := mcpFileRel(declared)
	if rel == "" {
		return out
	}
	for _, path := range inPackFiles(p, rel) {
		out = append(out, PackMCPConfig{Path: path})
	}
	return out
}

// mcpFileRels returns the validated paths of the declared MCP config files,
// existing or not, so the watcher sees one created later.
func (p packRoot) mcpFileRels() []string {
	var out []string
	for _, decl := range p.Manifest.MCPServers {
		if rel := mcpFileRel(decl.path); decl.inline == nil && rel != "" {
			out = append(out, rel)
		}
	}
	return out
}

// mcpFileRel validates a declared MCP config path, or returns "" for an
// escaping path, a bundle, or a URL.
func mcpFileRel(declared string) string {
	declared = strings.TrimSpace(declared)
	ext := strings.ToLower(filepath.Ext(declared))
	if strings.Contains(declared, "://") || ext == ".mcpb" || ext == ".dxt" {
		return ""
	}
	return containerRelPath(declared, "")
}

// packMCPSources returns the MCP declarations of the packs under roots in
// scan-root order, which is the order a server name collision is settled in.
// Like emitPacks it finds nothing until the server advertises pack rows, so a
// deployment without the feature never starts pack servers, and it skips a
// pack whose manifest path coderd cannot transport. It also skips a managed
// pack without a commit marker: coderd writes the marker last and empties it
// once no chat on the workspace enables the pack, so such a pack is
// incomplete or disabled.
func (r *Resolver) packMCPSources(roots []ScanRoot) []PackMCPSource {
	if !r.packRowsAdvertised() {
		return nil
	}
	seen := make(map[string]struct{})
	var out []PackMCPSource
	for _, root := range roots {
		if root.Path == "" {
			continue
		}
		for _, pack := range packRootsFor(root.Path) {
			if _, ok := seen[pack.ManifestPath]; ok || len(pack.ManifestPath) > maxSourcePathBytes {
				continue
			}
			seen[pack.ManifestPath] = struct{}{}
			configs := pack.mcpConfigs()
			if len(configs) == 0 {
				continue
			}
			revision := packRevision(pack.Dir)
			if revision == "" && isManagedPackManifest(pack.ManifestPath) {
				continue
			}
			out = append(out, PackMCPSource{ManifestPath: pack.ManifestPath, Dir: pack.Dir, Claude: pack.Claude, Revision: revision, Configs: configs})
		}
	}
	return out
}

// packMCPPublisher hands the pack MCP declarations of each published resolve
// to the agent's MCP manager.
type packMCPPublisher struct {
	deliver func([]PackMCPSource)
	mu      sync.Mutex
	epoch   uint64
}

// publish delivers the declarations under roots after the resolve pass with
// the given epoch published, unless a newer pass already delivered its own,
// so passes that finish out of order cannot restore a stale pack list. A
// pass inside a capability negotiation delivers nothing: every connection
// starts one with pack rows unadvertised, and delivering its empty list
// would restart every pack server on each reconnect.
func (p *packMCPPublisher) publish(r *Resolver, roots []ScanRoot, epoch uint64, negotiating bool) {
	if p.deliver == nil || negotiating {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if epoch < p.epoch {
		return
	}
	p.epoch = epoch
	p.deliver(r.packMCPSources(roots))
}

// gatePackMCPResources drops the resources of pack servers while pack rows
// are unadvertised. Pack servers keep running through a capability
// negotiation, and their rows must not reach a replica that never asked for
// pack rows, which would serve their tools to every chat.
func (m *Manager) gatePackMCPResources(resources []Resource) []Resource {
	if m.packRowsAdvertised.Load() {
		return resources
	}
	return slices.DeleteFunc(resources, isPackContribution)
}

// packMCPServerSep joins a pack server's declaring file and name in its
// resource source. Keep in sync with coderd/x/chatd/fork_plugin_gate.go.
const packMCPServerSep = "#"

// resourceSource returns the source of the server's KindMCPServer resource.
// A pack server's source names its declaring file and the server, so coderd's
// plugin gate can check the file against the pack's commit: one file
// declares several servers and a source must be unique per agent. A source
// over the transport limit falls back to the name, which the gate drops for
// an installed pack.
func (s MCPServerStatus) resourceSource() string {
	if s.PackManifest == "" || s.PackSource == "" {
		return s.Name
	}
	source := s.PackSource + packMCPServerSep + s.Name
	if len(source) > maxSourcePathBytes {
		return s.Name
	}
	return source
}
