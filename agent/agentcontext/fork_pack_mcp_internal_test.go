package agentcontext

// This file is fork code: it tests how plugin packs declare MCP servers and
// how their KindMCPServer resources carry the pack marker.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// writePackFiles writes files, keyed by slash path relative to dir.
func writePackFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}
}

func TestPackMCPConfigs(t *testing.T) {
	t.Parallel()

	const (
		claudeManifest = ".claude-plugin/plugin.json"
		cursorManifest = ".cursor-plugin/plugin.json"
		servers        = `{"mcpServers": {"deploy": {"command": "deploy"}}}`
	)
	for _, tc := range []struct {
		name  string
		files map[string]string
		// want lists the declaring files relative to the pack, in load
		// order; "@manifest" stands for an inline declaration.
		want []string
	}{
		{
			name: "ClaudePathAddsToRootFile",
			files: map[string]string{
				claudeManifest:        `{"name": "p", "mcpServers": "./config/servers.json"}`,
				".mcp.json":           servers,
				"config/servers.json": servers,
			},
			want: []string{".mcp.json", "config/servers.json"},
		},
		{
			name: "ClaudeArray",
			files: map[string]string{
				claudeManifest: `{"name": "p", "mcpServers": ["a.json", "missing.json", "b.json"]}`,
				"a.json":       servers,
				"b.json":       servers,
			},
			want: []string{"a.json", "b.json"},
		},
		{
			name: "ClaudeInline",
			files: map[string]string{
				claudeManifest: `{"name": "p", "mcpServers": {"deploy": {"command": "deploy"}}}`,
			},
			want: []string{"@manifest"},
		},
		{
			name: "DefaultRootFile",
			files: map[string]string{
				cursorManifest: `{"name": "p"}`,
				".mcp.json":    servers,
			},
			want: []string{".mcp.json"},
		},
		{
			name: "CursorDeclarationReplacesRootFile",
			files: map[string]string{
				cursorManifest: `{"name": "p", "mcpServers": "servers.json"}`,
				".mcp.json":    servers,
				"servers.json": servers,
			},
			want: []string{"servers.json"},
		},
		{
			name: "CursorMistypedFallsBack",
			files: map[string]string{
				cursorManifest: `{"name": "p", "mcpServers": 42}`,
				".mcp.json":    servers,
			},
			want: []string{".mcp.json"},
		},
		{
			name: "OutsidePathRejected",
			files: map[string]string{
				claudeManifest:    `{"name": "p", "mcpServers": ["../outside.json", "/etc/servers.json"]}`,
				"../outside.json": servers,
			},
		},
		{
			name: "BundlesAndURLsSkipped",
			files: map[string]string{
				claudeManifest: `{"name": "p", "mcpServers": ["tools.mcpb", "https://example.com/servers.json"]}`,
				"tools.mcpb":   servers,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := filepath.Join(t.TempDir(), "pack")
			writePackFiles(t, dir, tc.files)

			pack, ok := readPluginManifest(dir)
			require.True(t, ok)
			var got []string
			for _, cfg := range pack.mcpConfigs() {
				if cfg.Inline != nil {
					require.Equal(t, pack.ManifestPath, cfg.Path)
					require.JSONEq(t, `{"deploy": {"command": "deploy"}}`, string(cfg.Inline))
					got = append(got, "@manifest")
					continue
				}
				rel, err := filepath.Rel(dir, cfg.Path)
				require.NoError(t, err)
				got = append(got, filepath.ToSlash(rel))
			}
			require.Equal(t, tc.want, got)
		})
	}
}

func TestPackMCPConfigsSymlinkEscapeRejected(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "pack")
	writePackFiles(t, root, map[string]string{
		"outside.json":                    `{"mcpServers": {}}`,
		"pack/.claude-plugin/plugin.json": `{"name": "p", "mcpServers": "link.json"}`,
	})
	require.NoError(t, os.Symlink(filepath.Join(root, "outside.json"), filepath.Join(dir, "link.json")))

	pack, ok := readPluginManifest(dir)
	require.True(t, ok)
	require.Empty(t, pack.mcpConfigs())
}

// Claude Code fails a plugin whose mcpServers field has a type it does not
// accept, so the pack must not load.
func TestReadPluginManifestRejectsMistypedClaudeMCPServers(t *testing.T) {
	t.Parallel()
	for _, field := range []string{`42`, `true`, `[42]`} {
		dir := t.TempDir()
		writePackFiles(t, dir, map[string]string{
			".claude-plugin/plugin.json": `{"name": "p", "mcpServers": ` + field + `}`,
		})
		_, ok := readPluginManifest(dir)
		require.False(t, ok, field)
	}
}

func TestResolverPackMCPSources(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "pack")
	writePackFiles(t, dir, map[string]string{
		".claude-plugin/plugin.json": `{"name": "p"}`,
		".mcp.json":                  `{"mcpServers": {"deploy": {"command": "deploy"}}}`,
		packCommitMarkerName:         "pack-id abc123\n",
	})
	// A pack without MCP servers contributes no source.
	writePackFiles(t, filepath.Join(root, "plain"), map[string]string{
		".claude-plugin/plugin.json": `{"name": "plain"}`,
	})
	roots := []ScanRoot{{Path: root}}

	r := &Resolver{}
	require.Empty(t, r.packMCPSources(roots), "unadvertised pack rows start no pack servers")

	r.PackRowsAdvertised = func() bool { return true }
	require.Equal(t, []PackMCPSource{{
		ManifestPath: filepath.Join(dir, ".claude-plugin", "plugin.json"),
		Dir:          dir,
		Claude:       true,
		Revision:     "pack-id abc123",
		Configs:      []PackMCPConfig{{Path: filepath.Join(dir, ".mcp.json")}},
	}}, r.packMCPSources(roots))
	require.True(t, packWatchPaths{}.relevant(filepath.Join(dir, packCommitMarkerName)), "rewriting the commit marker re-resolves")
}

func TestPackMCPPublisher(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writePackFiles(t, filepath.Join(root, "pack"), map[string]string{
		".claude-plugin/plugin.json": `{"name": "p"}`,
		".mcp.json":                  `{"mcpServers": {"deploy": {"command": "deploy"}}}`,
	})
	roots := []ScanRoot{{Path: root}}
	r := &Resolver{PackRowsAdvertised: func() bool { return true }}

	var delivered [][]PackMCPSource
	p := packMCPPublisher{deliver: func(s []PackMCPSource) { delivered = append(delivered, s) }}
	p.publish(r, roots, 2, false)
	require.Len(t, delivered, 1)
	require.Len(t, delivered[0], 1)

	p.publish(r, roots, 1, false)
	require.Len(t, delivered, 1, "a stale pass must not restore an older pack list")
	p.publish(r, roots, 3, true)
	require.Len(t, delivered, 1, "a negotiating pass must not restart pack servers")
	p.publish(r, roots, 3, false)
	require.Len(t, delivered, 2)

	(&packMCPPublisher{}).publish(r, roots, 1, false)
}

func TestBuildMCPServerResourcesStampsPackServers(t *testing.T) {
	t.Parallel()
	const (
		manifest = "/home/u/.coder/plugins/p/.claude-plugin/plugin.json"
		file     = "/home/u/.coder/plugins/p/.mcp.json"
	)
	got := buildMCPServerResources([]MCPServerStatus{
		{Name: "deploy", Connected: true, Tools: []MCPTool{{Name: "run"}}, PackManifest: manifest, PackSource: file},
		{Name: "down", Err: "boom", PackManifest: manifest, PackSource: manifest},
		{Name: "github", Connected: true, Tools: []MCPTool{{Name: "search"}}},
		{Name: "long", Connected: true, Tools: []MCPTool{{Name: "run"}}, PackManifest: manifest, PackSource: "/" + strings.Repeat("a", maxSourcePathBytes)},
	})
	require.Len(t, got, 4)

	require.Equal(t, file+"#deploy", got[0].Source)
	require.Equal(t, manifest, got[0].SourcePath)
	require.Equal(t, "deploy", got[0].Name)
	require.True(t, isPackContribution(got[0]))

	require.Equal(t, StatusUnreadable, got[1].Status)
	require.Equal(t, manifest+"#down", got[1].Source)
	require.Equal(t, manifest, got[1].SourcePath)

	require.Equal(t, "github", got[2].Source, "a workspace server keeps its name as source")
	require.Empty(t, got[2].SourcePath)
	require.False(t, isPackContribution(got[2]))

	require.Equal(t, "long", got[3].Source, "an untransportable source falls back to the name")
}

func TestGatePackMCPResources(t *testing.T) {
	t.Parallel()
	resources := func() []Resource {
		return buildMCPServerResources([]MCPServerStatus{
			{Name: "deploy", Connected: true, Tools: []MCPTool{{Name: "run"}}, PackManifest: "/p/plugin.json", PackSource: "/p/.mcp.json"},
			{Name: "github", Connected: true, Tools: []MCPTool{{Name: "search"}}},
		})
	}
	m := &Manager{}
	got := m.gatePackMCPResources(resources())
	require.Len(t, got, 1)
	require.Equal(t, "github", got[0].Name)

	m.packRowsAdvertised.Store(true)
	require.Len(t, m.gatePackMCPResources(resources()), 2)
}
