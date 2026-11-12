package agentmcp

// This file is fork code: it tests the plugin pack MCP servers the Manager
// starts next to the workspace .mcp.json servers.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"cdr.dev/slog/v3"
	"cdr.dev/slog/v3/sloggers/slogtest"
	"github.com/coder/coder/v2/agent/agentexec"
	"github.com/coder/coder/v2/testutil"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func TestParsePackSource(t *testing.T) {
	t.Parallel()

	t.Run("ExpandsPluginRoot", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		manifest := filepath.Join(dir, "plugin.json")
		wrapped := filepath.Join(dir, ".mcp.json")
		bare := filepath.Join(dir, "servers.json")
		writeTestFile(t, wrapped, `{"mcpServers": {"deploy": {"command": "old"}}}`)
		writeTestFile(t, bare, `{
			"deploy": {
				"command": "${CLAUDE_PLUGIN_ROOT}/bin/deploy",
				"args": ["--root", "${CLAUDE_PLUGIN_ROOT}"],
				"env": {"DATA": "${CLAUDE_PLUGIN_ROOT}/data"}
			},
			"docs": {
				"url": "http://127.0.0.1:1/${CLAUDE_PLUGIN_ROOT}",
				"headers": {"X-Root": "${CLAUDE_PLUGIN_ROOT}"}
			}
		}`)

		servers, errs := parsePackSource(PackSource{
			ManifestPath:     manifest,
			Dir:              dir,
			ExpandPluginRoot: true,
			Configs: []PackConfig{
				{Path: wrapped},
				// A later declaration of a name replaces the earlier one.
				{Path: bare},
				{Path: manifest, Inline: json.RawMessage(`{"lint": {"command": "lint"}}`)},
				{Path: filepath.Join(dir, "missing.json")},
			},
		})
		require.Len(t, errs, 1, "a missing file is reported and skipped")
		require.Len(t, servers, 3)

		deploy, docs, lint := servers[0], servers[1], servers[2]
		require.Equal(t, "deploy", deploy.Name)
		require.Equal(t, dir+"/bin/deploy", deploy.Command)
		require.Equal(t, []string{"--root", dir}, deploy.Args)
		require.Equal(t, map[string]string{"DATA": dir + "/data", "CLAUDE_PLUGIN_ROOT": dir}, deploy.Env)
		require.Equal(t, PackOrigin{Manifest: manifest, File: bare}, deploy.Pack)

		require.Equal(t, "docs", docs.Name)
		require.Equal(t, "http", docs.Transport)
		require.Equal(t, "http://127.0.0.1:1/"+dir, docs.URL)
		require.Equal(t, map[string]string{"X-Root": dir}, docs.Headers)
		require.Empty(t, docs.Env, "only stdio servers get the variable")

		require.Equal(t, "lint", lint.Name)
		require.Equal(t, PackOrigin{Manifest: manifest, File: manifest}, lint.Pack)
		require.Equal(t, map[string]string{"CLAUDE_PLUGIN_ROOT": dir}, lint.Env)
	})

	t.Run("KeepsPlaceholderOutsideClaudePacks", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		manifest := filepath.Join(dir, "plugin.json")
		servers, errs := parsePackSource(PackSource{
			ManifestPath: manifest,
			Dir:          dir,
			Configs: []PackConfig{{
				Path:   manifest,
				Inline: json.RawMessage(`{"deploy": {"command": "${CLAUDE_PLUGIN_ROOT}/bin/deploy"}}`),
			}},
		})
		require.Empty(t, errs)
		require.Len(t, servers, 1)
		require.Equal(t, "${CLAUDE_PLUGIN_ROOT}/bin/deploy", servers[0].Command)
		require.NotContains(t, servers[0].Env, claudePluginRootEnv)
	})
}

func TestParseAndDedupPackServers(t *testing.T) {
	t.Parallel()
	ctx := testutil.Context(t, testutil.WaitShort)
	logger := slogtest.Make(t, nil).Leveled(slog.LevelDebug)
	dir := t.TempDir()

	workspace := writeMCPConfig(t, dir, map[string]mcpServerEntry{"shared": {Command: "workspace"}})
	first := filepath.Join(dir, "first.json")
	writeTestFile(t, first, `{"mcpServers": {"shared": {"command": "pack"}, "extra": {"command": "first"}}}`)
	secondManifest := filepath.Join(dir, "second", "plugin.json")

	m := NewManager(ctx, logger, agentexec.DefaultExecer, nil, nil, nil, nil)
	t.Cleanup(func() { _ = m.Close() })
	m.SetPackSources([]PackSource{
		{ManifestPath: filepath.Join(dir, "plugin.json"), Dir: dir, Configs: []PackConfig{{Path: first}}},
		{ManifestPath: secondManifest, Configs: []PackConfig{{
			Path:   secondManifest,
			Inline: json.RawMessage(`{"extra": {"command": "second"}}`),
		}}},
	})

	configs, snap := m.parseAndDedup(ctx, []string{workspace})
	byName := map[string]ServerConfig{}
	for _, cfg := range configs {
		byName[cfg.Name] = cfg
	}
	require.Len(t, byName, 2)
	require.Equal(t, "workspace", byName["shared"].Command, "the workspace .mcp.json wins a name collision")
	require.Zero(t, byName["shared"].Pack)
	require.Equal(t, "first", byName["extra"].Command, "an earlier pack beats a later one")
	require.Equal(t, first, byName["extra"].Pack.File)
	require.Contains(t, snap, first, "pack config files are part of the change snapshot")
}

func TestSetPackSourcesReloads(t *testing.T) {
	t.Parallel()
	ctx := testutil.Context(t, testutil.WaitLong)
	logger := slogtest.Make(t, nil).Leveled(slog.LevelDebug)
	dir := t.TempDir()

	workspace := writeMCPConfig(t, dir, map[string]mcpServerEntry{})
	m := NewManager(ctx, logger, agentexec.DefaultExecer, nil, nil, nil, nil)
	t.Cleanup(func() { _ = m.Close() })
	require.NoError(t, m.Reload(ctx, []string{workspace}))
	require.Empty(t, m.connectedTools())

	_, entry := fakeMCPServerConfig(t, "pack-srv")
	inline, err := json.Marshal(map[string]mcpServerEntry{"pack-srv": entry})
	require.NoError(t, err)
	manifest := filepath.Join(dir, "plugin.json")
	writeTestFile(t, manifest, `{"name": "p"}`)
	m.SetPackSources([]PackSource{{ManifestPath: manifest, Dir: dir, Configs: []PackConfig{{Path: manifest, Inline: inline}}}})

	awaitTools(ctx, t, m, func(tools []catalogTool) bool {
		return len(tools) == 1 && tools[0].server == "pack-srv"
	})
	catalog := m.Catalog()
	require.Len(t, catalog, 1)
	require.Equal(t, PackOrigin{Manifest: manifest, File: manifest}, catalog[0].Pack)

	m.mu.RLock()
	before := m.servers["pack-srv"].client
	m.mu.RUnlock()
	m.SetPackSources([]PackSource{{ManifestPath: manifest, Dir: dir, Revision: "r2", Configs: []PackConfig{{Path: manifest, Inline: inline}}}})
	require.Eventually(t, func() bool {
		m.mu.RLock()
		defer m.mu.RUnlock()
		entry, ok := m.servers["pack-srv"]
		return ok && entry.client != before && entry.config.Pack.Revision == "r2"
	}, testutil.WaitLong, testutil.IntervalFast, "a new pack revision restarts the pack's servers")

	renamed, err := json.Marshal(map[string]mcpServerEntry{"renamed-srv": entry})
	require.NoError(t, err)
	m.SetPackSources([]PackSource{{ManifestPath: manifest, Dir: dir, Revision: "r2", Configs: []PackConfig{{Path: manifest, Inline: renamed}}}})
	awaitTools(ctx, t, m, func(tools []catalogTool) bool {
		return len(tools) == 1 && tools[0].server == "renamed-srv"
	})

	m.SetPackSources(nil)
	awaitTools(ctx, t, m, func(tools []catalogTool) bool { return len(tools) == 0 })
}
