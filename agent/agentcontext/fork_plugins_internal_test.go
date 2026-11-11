package agentcontext

// This file is fork code: it asserts the built-in scan roots include the
// installed plugin pack root.

import (
	"os"
	"path/filepath"
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
	w := &Watcher{agentDirs: map[string]struct{}{"/p/pstack/definitions": {}}}
	cases := []struct {
		name  string
		event fsnotify.Event
		want  bool
	}{
		{"manifest write re-resolves", fsnotify.Event{Name: "/p/pstack/plugin.json", Op: fsnotify.Write}, true},
		{"manifest create re-resolves", fsnotify.Event{Name: "/p/pstack/plugin.json", Op: fsnotify.Create}, true},
		{"cursor manifest write re-resolves", fsnotify.Event{Name: "/p/pstack/.cursor-plugin/plugin.json", Op: fsnotify.Write}, true},
		{"skill edit re-resolves", fsnotify.Event{Name: "/p/pstack/skills/x/SKILL.md", Op: fsnotify.Write}, true},
		{"ordinary write ignored", fsnotify.Event{Name: "/p/pstack/README.md", Op: fsnotify.Write}, false},
		{"declared agent container write re-resolves", fsnotify.Event{Name: "/p/pstack/definitions/reviewer.md", Op: fsnotify.Write}, true},
		{"non-markdown write in agent container ignored", fsnotify.Event{Name: "/p/pstack/definitions/notes.txt", Op: fsnotify.Write}, false},
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
}
