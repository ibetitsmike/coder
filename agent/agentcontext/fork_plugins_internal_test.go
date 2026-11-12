package agentcontext

// This file is fork code: it asserts the built-in scan roots include the
// installed plugin pack root.

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/require"
)

func TestDefaultBuiltinRootsIncludePackRoot(t *testing.T) {
	t.Parallel()
	for _, root := range defaultBuiltinRoots() {
		if root == "~/.coder/plugins" {
			return
		}
	}
	t.Fatal("~/.coder/plugins is missing from defaultBuiltinRoots; installed packs would not be discovered")
}

func TestWatcherEventRelevant(t *testing.T) {
	t.Parallel()
	w := &Watcher{pack: packWatchPaths{
		agentDirs:     map[string]struct{}{"/p/pstack/definitions": {}},
		settingsFiles: map[string]struct{}{"/h/.claude/settings.json": {}},
	}}
	cases := []struct {
		name  string
		event fsnotify.Event
		want  bool
	}{
		{"manifest write re-resolves", fsnotify.Event{Name: "/p/pstack/plugin.json", Op: fsnotify.Write}, true},
		{"manifest create re-resolves", fsnotify.Event{Name: "/p/pstack/plugin.json", Op: fsnotify.Create}, true},
		{"cursor manifest write re-resolves", fsnotify.Event{Name: "/p/pstack/.cursor-plugin/plugin.json", Op: fsnotify.Write}, true},
		{"skill edit re-resolves", fsnotify.Event{Name: "/p/pstack/skills/x/SKILL.md", Op: fsnotify.Write}, true},
		{"install record write re-resolves", fsnotify.Event{Name: "/h/.claude/plugins/installed_plugins.json", Op: fsnotify.Write}, true},
		{"marketplace record write re-resolves", fsnotify.Event{Name: "/h/.claude/plugins/known_marketplaces.json", Op: fsnotify.Write}, true},
		{"marketplace catalog write re-resolves", fsnotify.Event{Name: "/h/.claude/plugins/marketplaces/m/.claude-plugin/marketplace.json", Op: fsnotify.Write}, true},
		{"ordinary write ignored", fsnotify.Event{Name: "/p/pstack/README.md", Op: fsnotify.Write}, false},
		{"declared agent container write re-resolves", fsnotify.Event{Name: "/p/pstack/definitions/reviewer.md", Op: fsnotify.Write}, true},
		{"non-markdown write in agent container ignored", fsnotify.Event{Name: "/p/pstack/definitions/notes.txt", Op: fsnotify.Write}, false},
		{"user settings write re-resolves", fsnotify.Event{Name: "/h/.claude/settings.json", Op: fsnotify.Write}, true},
		{"other write next to user settings ignored", fsnotify.Event{Name: "/h/.claude/history.jsonl", Op: fsnotify.Write}, false},
		{"settings write elsewhere ignored", fsnotify.Event{Name: "/p/.claude/settings.json", Op: fsnotify.Write}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, w.eventRelevant(tc.event))
		})
	}
}

func TestCollectDirsWatchesClaudeCacheLevels(t *testing.T) {
	t.Parallel()
	cache := filepath.Join(t.TempDir(), ".claude", "plugins", "cache")
	plugin := filepath.Join(cache, "market", "pstack")
	live := filepath.Join(plugin, "0.15.36")
	orphaned := filepath.Join(plugin, "0.15.35")
	for _, dir := range []string{filepath.Join(live, ".claude-plugin"), orphaned} {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(orphaned, claudeOrphanedMarker), nil, 0o600))

	dirs, _ := (&Watcher{}).collectDirs([]ScanRoot{{Path: cache}})
	// A new marketplace, plugin, or version, the manifest of a version
	// created before it, and an orphan marker each fire in one of these.
	// The orphaned version stays watched, so removing its marker
	// re-resolves.
	for _, dir := range []string{cache, filepath.Join(cache, "market"), plugin, live, filepath.Join(live, ".claude-plugin"), orphaned} {
		require.Contains(t, dirs, dir)
	}
}

func TestCollectDirsWatchesClaudeMarketplaceCatalog(t *testing.T) {
	t.Parallel()
	plugins := filepath.Join(t.TempDir(), ".claude", "plugins")
	cache := filepath.Join(plugins, "cache")
	catalogDir := filepath.Join(plugins, "marketplaces", "market", ".claude-plugin")
	for _, dir := range []string{filepath.Join(cache, "market", "pstack", "1.0.0"), catalogDir} {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}

	dirs, _ := (&Watcher{}).collectDirs([]ScanRoot{{Path: cache}})
	// An updated catalog can change the entry a cached plugin loads with.
	require.Contains(t, dirs, catalogDir)
}

func TestCollectDirsWatchesNestedClaudeAgentDirs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, manifestDir := range []string{".claude-plugin", ".cursor-plugin"} {
		pack := filepath.Join(root, manifestDir[1:])
		require.NoError(t, os.MkdirAll(filepath.Join(pack, manifestDir), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(pack, manifestDir, packManifestBaseName), []byte(`{"name": "pstack"}`), 0o600))
		require.NoError(t, os.MkdirAll(filepath.Join(pack, "agents", "team", "deep"), 0o755))
	}

	dirs, pack := (&Watcher{}).collectDirs([]ScanRoot{{Path: root}})
	// A definition created in a nested folder of a Claude pack's default
	// agents directory re-resolves; Cursor packs keep a flat directory.
	claudeAgents := filepath.Join(root, "claude-plugin", "agents")
	for _, dir := range []string{claudeAgents, filepath.Join(claudeAgents, "team"), filepath.Join(claudeAgents, "team", "deep")} {
		require.Contains(t, dirs, dir)
		require.Contains(t, pack.agentDirs, dir)
	}
	cursorAgents := filepath.Join(root, "cursor-plugin", "agents")
	require.Contains(t, pack.agentDirs, cursorAgents)
	require.NotContains(t, pack.agentDirs, filepath.Join(cursorAgents, "team"))
}

func TestCollectDirsWatchesClaudeUserSettings(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cache := filepath.Join(home, ".claude", "plugins", "cache")
	require.NoError(t, os.MkdirAll(filepath.Join(cache, "market", "pstack", "1.0.0"), 0o755))
	settings := filepath.Join(home, ".claude", "settings.json")

	w := &Watcher{}
	dirs, pack := w.collectDirs([]ScanRoot{{Path: cache}})
	// Toggling a plugin in enabledPlugins changes which cached plugins
	// load, so the settings directory is watched and a write to the
	// settings file, and only that file, re-resolves.
	require.Contains(t, dirs, filepath.Dir(settings))
	w.pack = pack
	require.True(t, w.eventRelevant(fsnotify.Event{Name: settings, Op: fsnotify.Write}))
	require.False(t, w.eventRelevant(fsnotify.Event{Name: filepath.Join(home, ".claude", "history.jsonl"), Op: fsnotify.Write}))

	// A scan root that is not a Claude plugin cache watches no settings.
	_, other := w.collectDirs([]ScanRoot{{Path: t.TempDir()}})
	require.Empty(t, other.settingsFiles)
}

func TestCollectDirsWatchesSymlinkedClaudeUserSettings(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cache := filepath.Join(home, ".claude", "plugins", "cache")
	require.NoError(t, os.MkdirAll(filepath.Join(cache, "market", "pstack", "1.0.0"), 0o755))
	dotfiles := filepath.Join(t.TempDir(), "dotfiles")
	require.NoError(t, os.MkdirAll(dotfiles, 0o755))
	target := filepath.Join(dotfiles, "settings.json")
	require.NoError(t, os.WriteFile(target, []byte(`{}`), 0o600))
	if err := os.Symlink(target, filepath.Join(home, ".claude", "settings.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(target)
	require.NoError(t, err)

	w := &Watcher{}
	dirs, pack := w.collectDirs([]ScanRoot{{Path: cache}})
	// An edit through the link fires only in the target's directory.
	require.Contains(t, dirs, filepath.Dir(resolved))
	w.pack = pack
	require.True(t, w.eventRelevant(fsnotify.Event{Name: resolved, Op: fsnotify.Write}))
	require.False(t, w.eventRelevant(fsnotify.Event{Name: filepath.Join(filepath.Dir(resolved), "notes.txt"), Op: fsnotify.Write}))
}

func TestCollectDirsWatchesClaudeLocalMarketplace(t *testing.T) {
	t.Parallel()
	plugins := filepath.Join(t.TempDir(), ".claude", "plugins")
	cache := filepath.Join(plugins, "cache")
	require.NoError(t, os.MkdirAll(cache, 0o755))
	root := filepath.Join(t.TempDir(), "local-market")
	catalogDir := filepath.Join(root, ".claude-plugin")
	pluginDir := filepath.Join(root, "plugins", "inplace")
	require.NoError(t, os.MkdirAll(catalogDir, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(pluginDir, "skills"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(catalogDir, claudeMarketplaceFileName),
		[]byte(`{"name": "local", "plugins": [{"name": "inplace", "source": "./plugins/inplace"}]}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(plugins, claudeKnownMarketplacesFile),
		[]byte(fmt.Sprintf(`{"local": {"source": {"source": "directory", "path": %q}, "installLocation": %q}}`, root, root)), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(plugins, claudeInstalledPluginsFile),
		[]byte(`{"version": 2, "plugins": {"inplace@local": [{"scope": "user"}]}}`), 0o600))

	dirs, _ := (&Watcher{}).collectDirs([]ScanRoot{{Path: cache}})
	// The catalog lists the in-place plugins, and each one is a pack
	// watched where it lives.
	for _, dir := range []string{catalogDir, pluginDir, filepath.Join(pluginDir, "skills")} {
		require.Contains(t, dirs, dir)
	}
}

func TestNestedAgentDirsBounds(t *testing.T) {
	t.Parallel()
	container := t.TempDir()
	deep := container
	for i := range maxPackAgentDepth + 2 {
		deep = filepath.Join(deep, fmt.Sprintf("level%d", i))
	}
	require.NoError(t, os.MkdirAll(deep, 0o755))
	require.Len(t, nestedAgentDirs(container), maxPackAgentDepth)

	wide := t.TempDir()
	for i := range maxPackAgentDirs + 5 {
		require.NoError(t, os.MkdirAll(filepath.Join(wide, fmt.Sprintf("dir%d", i)), 0o755))
	}
	require.Len(t, nestedAgentDirs(wide), maxPackAgentDirs)
}

func TestSubstituteMarkdownBody(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		in      string
		want    string
		changed bool
	}{
		{"body only", "---\nname: x ${V}\n---\nuse ${V}/a and ${V}", "---\nname: x ${V}\n---\nuse /r/a and /r", true},
		{"byte order mark and CRLF", "\xef\xbb\xbf---\r\nname: x\r\n---\r\n${V}\r\n", "\xef\xbb\xbf---\r\nname: x\r\n---\r\n/r\r\n", true},
		{"no reference", "---\nname: x\n---\nbody", "---\nname: x\n---\nbody", false},
		{"reference only in front-matter", "---\nname: ${V}\n---\nbody", "---\nname: ${V}\n---\nbody", false},
		{"no front-matter", "${V}", "${V}", false},
		{"unclosed front-matter", "---\nname: x\n${V}", "---\nname: x\n${V}", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, changed := substituteMarkdownBody([]byte(tc.in), "${V}", "/r")
			require.Equal(t, tc.want, string(got))
			require.Equal(t, tc.changed, changed)
		})
	}
}

func TestCollectDirsWatchesDefaultAndSingleSkillFolders(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	pack := filepath.Join(root, "pstack")
	manifest := filepath.Join(pack, ".claude-plugin", "plugin.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(manifest), 0o755))
	require.NoError(t, os.WriteFile(manifest, []byte(`{"name": "pstack", "skills": "./review-pr"}`), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(pack, "skills", "default-skill"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(pack, "review-pr"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pack, "review-pr", skillMetaFileName), []byte("x"), 0o600))

	dirs, _ := (&Watcher{}).collectDirs([]ScanRoot{{Path: root}})
	// The default container and its skill folders stay watched next to the
	// declared single skill folder.
	for _, dir := range []string{filepath.Join(pack, "skills"), filepath.Join(pack, "skills", "default-skill"), filepath.Join(pack, "review-pr")} {
		require.Contains(t, dirs, dir)
	}
}

//nolint:paralleltest,tparallel // Uses t.Setenv.
func TestClaudePluginCacheDirOverride(t *testing.T) {
	t.Setenv(claudePluginCacheDirEnv, "")
	t.Setenv(claudeConfigDirEnv, "")
	require.Equal(t, "~/.claude/plugins/cache", claudePluginCacheRoot())
	require.Contains(t, defaultBuiltinRoots(), "~/.claude/plugins/cache")
	require.False(t, isClaudePluginCache(filepath.Join(t.TempDir(), "custom", "cache")))

	root := t.TempDir()
	t.Setenv(claudePluginCacheDirEnv, root)
	cache := filepath.Join(root, "cache")
	require.Equal(t, cache, claudePluginCacheRoot())
	require.Contains(t, defaultBuiltinRoots(), cache)
	require.NotContains(t, defaultBuiltinRoots(), "~/.claude/plugins/cache")
	require.True(t, isClaudePluginCache(cache))
	require.True(t, isClaudePluginCache(filepath.Join(t.TempDir(), ".claude", "plugins", "cache")))
	require.False(t, isClaudePluginCache(filepath.Join(root, "other")))

	// CLAUDE_CONFIG_DIR moves the cache when the cache variable is unset,
	// and the cache variable still wins when both are set.
	configDir := t.TempDir()
	configCache := filepath.Join(configDir, "plugins", "cache")
	t.Setenv(claudePluginCacheDirEnv, "")
	t.Setenv(claudeConfigDirEnv, configDir)
	require.Equal(t, configCache, claudePluginCacheRoot())
	require.Contains(t, defaultBuiltinRoots(), configCache)
	require.NotContains(t, defaultBuiltinRoots(), "~/.claude/plugins/cache")
	require.True(t, isClaudePluginCache(configCache))
	t.Setenv(claudePluginCacheDirEnv, root)
	require.Equal(t, cache, claudePluginCacheRoot())
}

//nolint:paralleltest,tparallel // Uses t.Setenv.
func TestClaudeUserSettingsPath(t *testing.T) {
	t.Setenv(claudePluginCacheDirEnv, "")
	t.Setenv(claudeConfigDirEnv, "")
	cache := filepath.Join(t.TempDir(), ".claude", "plugins", "cache")
	got, ok := claudeUserSettingsPath(cache)
	require.True(t, ok)
	require.Equal(t, filepath.Join(filepath.Dir(filepath.Dir(cache)), "settings.json"), got)

	// A cache moved by the environment variable keeps its settings in the
	// home directory.
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	got, ok = claudeUserSettingsPath(filepath.Join(t.TempDir(), "cache"))
	require.True(t, ok)
	require.Equal(t, filepath.Join(userHome, ".claude", "settings.json"), got)
}

//nolint:paralleltest,tparallel // Uses t.Setenv.
func TestClaudeUserSettingsPathConfigDir(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv(claudePluginCacheDirEnv, "")
	t.Setenv(claudeConfigDirEnv, configDir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	got, ok := claudeUserSettingsPath(filepath.Join(configDir, "plugins", "cache"))
	require.True(t, ok)
	require.Equal(t, filepath.Join(configDir, "settings.json"), got)

	// A cache moved by CLAUDE_CODE_PLUGIN_CACHE_DIR still reads the settings
	// of the configuration directory, not the home directory.
	got, ok = claudeUserSettingsPath(filepath.Join(t.TempDir(), "cache"))
	require.True(t, ok)
	require.Equal(t, filepath.Join(configDir, "settings.json"), got)
	// The cache variable moves only the plugins, even to a path that ends
	// like a default cache, so the settings stay in the configuration
	// directory.
	sharedPlugins := filepath.Join(t.TempDir(), ".claude", "plugins")
	t.Setenv(claudePluginCacheDirEnv, sharedPlugins)
	got, ok = claudeUserSettingsPath(filepath.Join(sharedPlugins, "cache"))
	require.True(t, ok)
	require.Equal(t, filepath.Join(configDir, "settings.json"), got)
}

//nolint:paralleltest,tparallel // Uses t.Setenv.
func TestIsClaudePluginCacheThroughSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need elevated privileges on Windows")
	}
	cases := []struct {
		name string
		link func(t *testing.T, home string) string
		env  func(t *testing.T, home string)
	}{
		{
			name: "symlinked .claude",
			link: func(t *testing.T, home string) string {
				actual := filepath.Join(t.TempDir(), "dotfiles-claude")
				require.NoError(t, os.MkdirAll(filepath.Join(actual, "plugins", "cache"), 0o755))
				require.NoError(t, os.Symlink(actual, filepath.Join(home, ".claude")))
				return filepath.Join(actual, "plugins", "cache")
			},
		},
		{
			name: "symlinked plugins directory",
			link: func(t *testing.T, home string) string {
				actual := filepath.Join(t.TempDir(), "plugins-data")
				require.NoError(t, os.MkdirAll(filepath.Join(actual, "cache"), 0o755))
				require.NoError(t, os.MkdirAll(filepath.Join(home, ".claude"), 0o755))
				require.NoError(t, os.Symlink(actual, filepath.Join(home, ".claude", "plugins")))
				return filepath.Join(actual, "cache")
			},
		},
		{
			name: "symlinked cache directory",
			link: func(t *testing.T, home string) string {
				actual := filepath.Join(t.TempDir(), "cache-data")
				require.NoError(t, os.MkdirAll(actual, 0o755))
				require.NoError(t, os.MkdirAll(filepath.Join(home, ".claude", "plugins"), 0o755))
				require.NoError(t, os.Symlink(actual, filepath.Join(home, ".claude", "plugins", "cache")))
				return actual
			},
		},
		{
			name: "symlinked CLAUDE_CONFIG_DIR",
			link: func(t *testing.T, home string) string {
				actual := filepath.Join(t.TempDir(), "config-data")
				require.NoError(t, os.MkdirAll(filepath.Join(actual, "plugins", "cache"), 0o755))
				require.NoError(t, os.Symlink(actual, filepath.Join(home, "config")))
				return filepath.Join(actual, "plugins", "cache")
			},
			env: func(t *testing.T, home string) {
				t.Setenv(claudeConfigDirEnv, filepath.Join(home, "config"))
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv(claudePluginCacheDirEnv, "")
			t.Setenv(claudeConfigDirEnv, "")
			target := tc.link(t, home)
			if tc.env != nil {
				tc.env(t, home)
			}
			resolved, err := filepath.EvalSymlinks(target)
			require.NoError(t, err)
			require.True(t, isClaudePluginCache(resolved))
			require.False(t, isClaudePluginCache(filepath.Join(filepath.Dir(resolved), "other")))
		})
	}
}

func TestCollectDirsWatchesClaudeInstallRecordDir(t *testing.T) {
	t.Parallel()
	plugins := filepath.Join(t.TempDir(), ".claude", "plugins")
	cache := filepath.Join(plugins, "cache")
	require.NoError(t, os.MkdirAll(cache, 0o755))

	dirs, _ := (&Watcher{}).collectDirs([]ScanRoot{{Path: cache}})
	require.Contains(t, dirs, plugins)
}
