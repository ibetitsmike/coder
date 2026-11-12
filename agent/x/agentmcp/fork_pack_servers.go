package agentmcp

// This file is fork code: it starts the MCP servers that plugin packs
// declare (agent/agentcontext/fork_pack_mcp.go finds them) next to the
// workspace .mcp.json servers, in the same Manager, so each server runs
// once for discovery and tool calls alike.

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"

	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/agent/agentchat"
)

// PackSource is the MCP server declarations of one plugin pack, in load
// order.
type PackSource struct {
	// ManifestPath is the pack's manifest, which coderd's plugin gate
	// matches pack rows on.
	ManifestPath string
	// Dir is the pack root.
	Dir string
	// ExpandPluginRoot expands ${CLAUDE_PLUGIN_ROOT} to Dir, as Claude
	// Code does for its plugins.
	ExpandPluginRoot bool
	// Revision identifies the pack's installed files. A change restarts
	// the pack's servers even when their declarations are unchanged,
	// because an update can replace the program a server runs.
	Revision string
	Configs  []PackConfig
}

// PackConfig is one mcpServers declaration of a pack.
type PackConfig struct {
	// Path is the declaring file: a JSON file holding the servers, or the
	// manifest for Inline servers.
	Path string
	// Inline is the mcpServers map declared in the manifest itself; nil
	// means the servers are read from Path.
	Inline json.RawMessage
}

// PackOrigin records which pack declared a server. It is zero for a
// workspace .mcp.json server.
type PackOrigin struct {
	Manifest string
	File     string
	Revision string
}

// claudePluginRootVar and claudePluginRootEnv follow Claude Code: the
// placeholder resolves in the server config and the variable is exported
// to stdio servers.
const (
	claudePluginRootVar = "${CLAUDE_PLUGIN_ROOT}"
	claudePluginRootEnv = "CLAUDE_PLUGIN_ROOT"
)

// SetPackSources replaces the plugin pack declarations and reloads when they
// changed. It does not block: the agentcontext manager calls it from its
// resolve loop, and a reload connects servers. Before the first Reload it
// only records the sources, which that Reload then includes.
func (m *Manager) SetPackSources(sources []PackSource) {
	m.mu.Lock()
	if m.closed || reflect.DeepEqual(m.packSources, sources) {
		m.mu.Unlock()
		return
	}
	m.packSources = slices.Clone(sources)
	// The declarations can change while every file stat stays the same
	// (an inline map or a revision), so Reload skips only once it has
	// loaded this generation.
	m.packGen++
	started := m.reloadRequested
	m.mu.Unlock()
	if !started {
		return
	}
	go func() {
		// A Reload that joins a reload already in flight returns once
		// that reload installs the previous sources. The second call
		// then sees the newer pack generation and reloads again;
		// otherwise it is a no-op.
		for range 2 {
			m.mu.RLock()
			paths := slices.Clone(m.lastPaths)
			m.mu.RUnlock()
			if err := m.Reload(m.ctx, paths); err != nil {
				m.logger.Debug(m.ctx, "plugin pack MCP reload stopped", slog.Error(err))
				return
			}
		}
	}()
}

// currentPackSources returns the current pack declarations.
func (m *Manager) currentPackSources() []PackSource {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.packSources
}

// packSourcesForReload returns the pack declarations for one reload and
// records their generation, which installServers then marks as loaded.
// Reload bodies never overlap (singleflight), so one field is enough.
func (m *Manager) packSourcesForReload() []PackSource {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.parsedPackGen = m.packGen
	return m.packSources
}

// packSourcesLoaded reports whether the installed servers come from the
// latest SetPackSources call.
func (m *Manager) packSourcesLoaded() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.loadedPackGen == m.packGen
}

// withPackFiles adds the files the current pack declarations read to the
// workspace config paths, so the watcher and the change snapshot cover them.
func (m *Manager) withPackFiles(paths []string) []string {
	return withPackFiles(paths, m.currentPackSources())
}

func withPackFiles(paths []string, packs []PackSource) []string {
	if len(packs) == 0 {
		return paths
	}
	out := slices.Clone(paths)
	for _, pack := range packs {
		for _, cfg := range pack.Configs {
			out = append(out, cfg.Path)
		}
	}
	return out
}

// parsePackServers returns the servers of every pack, after the workspace
// configs in the reload's first-wins order, so a workspace .mcp.json server
// wins a name collision and an earlier pack beats a later one. A
// declaration that fails to parse is logged and skipped, like a broken
// workspace .mcp.json.
func (m *Manager) parsePackServers(ctx context.Context, packs []PackSource) []ServerConfig {
	logger := m.logger.With(agentchat.Fields(ctx)...)
	var out []ServerConfig
	for _, pack := range packs {
		servers, errs := parsePackSource(pack)
		for _, err := range errs {
			logger.Warn(ctx, "failed to parse plugin pack MCP config",
				slog.F("manifest", pack.ManifestPath), slog.Error(err))
		}
		out = append(out, servers...)
	}
	return out
}

// parsePackSource returns the servers one pack declares, sorted by name. A
// server name declared later replaces an earlier one, as in Claude Code.
func parsePackSource(pack PackSource) ([]ServerConfig, []error) {
	var prepare func(*mcpServerEntry)
	if pack.ExpandPluginRoot {
		prepare = func(e *mcpServerEntry) { expandPluginRoot(e, pack.Dir) }
	}
	var errs []error
	byName := map[string]ServerConfig{}
	for _, cfg := range pack.Configs {
		servers, err := parsePackConfig(cfg, prepare)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, server := range servers {
			server.Pack = PackOrigin{Manifest: pack.ManifestPath, File: cfg.Path, Revision: pack.Revision}
			byName[server.Name] = server
		}
	}
	out := make([]ServerConfig, 0, len(byName))
	for _, server := range byName {
		out = append(out, server)
	}
	slices.SortFunc(out, func(a, b ServerConfig) int { return strings.Compare(a.Name, b.Name) })
	return out, errs
}

// parsePackConfig parses one declaration. A file may wrap its servers in an
// mcpServers key, like a workspace .mcp.json, or hold the map directly.
func parsePackConfig(cfg PackConfig, prepare func(*mcpServerEntry)) ([]ServerConfig, error) {
	raw := cfg.Inline
	if raw == nil {
		data, err := os.ReadFile(cfg.Path)
		if err != nil {
			return nil, xerrors.Errorf("read mcp config %q: %w", cfg.Path, err)
		}
		var wrapped struct {
			MCPServers json.RawMessage `json:"mcpServers"`
		}
		if err := json.Unmarshal(data, &wrapped); err != nil {
			return nil, xerrors.Errorf("parse mcp config %q: %w", cfg.Path, err)
		}
		raw = data
		if wrapped.MCPServers != nil {
			raw = wrapped.MCPServers
		}
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, xerrors.Errorf("parse mcp servers in %q: %w", cfg.Path, err)
	}
	return parseServerEntries(entries, cfg.Path, prepare)
}

// expandPluginRoot resolves ${CLAUDE_PLUGIN_ROOT} in the fields Claude Code
// substitutes and exports the variable to a stdio server. It runs before env
// expansion, which would otherwise replace the unset variable with "".
func expandPluginRoot(e *mcpServerEntry, dir string) {
	expand := func(s string) string { return strings.ReplaceAll(s, claudePluginRootVar, dir) }
	e.Command = expand(e.Command)
	for i, arg := range e.Args {
		e.Args[i] = expand(arg)
	}
	for k, v := range e.Env {
		e.Env[k] = expand(v)
	}
	e.URL = expand(e.URL)
	for k, v := range e.Headers {
		e.Headers[k] = expand(v)
	}
	if e.Command == "" {
		return
	}
	if e.Env == nil {
		e.Env = map[string]string{}
	}
	if _, ok := e.Env[claudePluginRootEnv]; !ok {
		e.Env[claudePluginRootEnv] = dir
	}
}
