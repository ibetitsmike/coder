package agentcontext_test

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/agent/agentcontext"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/quartz"
)

func TestWatcher_FiresOnAgentsMdEdit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("v1"), 0o600))

	var fires atomic.Int32
	w, err := agentcontext.NewWatcher(agentcontext.WatcherOptions{
		Logger:   testutil.Logger(t).Named("watcher"),
		Clock:    quartz.NewReal(),
		Debounce: 10 * time.Millisecond,
		OnChange: func() { fires.Add(1) },
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })

	ctx := testutil.Context(t, testutil.WaitShort)
	w.Sync(ctx, []agentcontext.ScanRoot{{Path: dir}})

	// Rewrite the file inside Eventually so the test does not race
	// fsnotify's watch-setup window. As soon as the watch is live,
	// the next write fires the debounce timer.
	require.Eventually(t, func() bool {
		_ = os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("v2"), 0o600)
		return fires.Load() >= 1
	}, testutil.WaitShort, testutil.IntervalFast, "expected at least one fire after AGENTS.md edit")
}

func TestWatcher_FiresOnNewSkillFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	skillsRoot := filepath.Join(dir, ".agents", "skills")
	require.NoError(t, os.MkdirAll(skillsRoot, 0o755))

	var fires atomic.Int32
	w, err := agentcontext.NewWatcher(agentcontext.WatcherOptions{
		Logger:   testutil.Logger(t).Named("watcher"),
		Debounce: 10 * time.Millisecond,
		OnChange: func() { fires.Add(1) },
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })

	ctx := testutil.Context(t, testutil.WaitShort)
	w.Sync(ctx, []agentcontext.ScanRoot{{Path: dir}})

	// Create SKILL.md inside Eventually so the test does not race
	// fsnotify's watch-setup window. The Manager pre-creates the
	// skill dir, then rewrites SKILL.md each tick until the watcher
	// fires at least once.
	skillDir := filepath.Join(skillsRoot, "foo")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	require.Eventually(t, func() bool {
		_ = os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: foo\ndescription: bar\n---\nbody"), 0o600)
		return fires.Load() >= 1
	}, testutil.WaitShort, testutil.IntervalFast, "expected fire after SKILL.md create")
}

func TestWatcher_CloseIsIdempotent(t *testing.T) {
	t.Parallel()
	w, err := agentcontext.NewWatcher(agentcontext.WatcherOptions{
		Logger:   testutil.Logger(t).Named("watcher"),
		OnChange: func() {},
	})
	require.NoError(t, err)
	require.NoError(t, w.Close())
	require.NoError(t, w.Close())
}

func TestWatcher_SyncAfterCloseNoop(t *testing.T) {
	t.Parallel()
	w, err := agentcontext.NewWatcher(agentcontext.WatcherOptions{
		Logger:   testutil.Logger(t).Named("watcher"),
		OnChange: func() {},
	})
	require.NoError(t, err)
	require.NoError(t, w.Close())

	// Must not panic.
	w.Sync(context.Background(), []agentcontext.ScanRoot{{Path: t.TempDir()}})
}

func TestWatcher_FiresOnNestedPackManifestEdit(t *testing.T) {
	t.Parallel()

	// Nested manifests (.claude-plugin/plugin.json) live in their own
	// directory: fsnotify reports edits there, so that directory must
	// be watched for manifest edits to re-resolve the pack at all.
	dir := t.TempDir()
	packDir := filepath.Join(dir, "pstack")
	manifestDir := filepath.Join(packDir, ".claude-plugin")
	require.NoError(t, os.MkdirAll(manifestDir, 0o755))
	manifestPath := filepath.Join(manifestDir, "plugin.json")
	require.NoError(t, os.WriteFile(manifestPath,
		[]byte(`{"name": "pstack", "skills": "skills"}`), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(packDir, "skills", "review-pr"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(packDir, "skills", "review-pr", "SKILL.md"),
		[]byte("---\nname: review-pr\n---\nbody\n"), 0o600))

	var fires atomic.Int32
	w, err := agentcontext.NewWatcher(agentcontext.WatcherOptions{
		Logger:   testutil.Logger(t).Named("watcher"),
		Debounce: 10 * time.Millisecond,
		OnChange: func() { fires.Add(1) },
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })

	ctx := testutil.Context(t, testutil.WaitShort)
	w.Sync(ctx, []agentcontext.ScanRoot{{Path: dir}})

	// Rewrite the nested manifest inside Eventually so the test does
	// not race fsnotify's watch-setup window: as soon as the manifest
	// parent's watch is live, the next write fires the debounce timer.
	require.Eventually(t, func() bool {
		_ = os.WriteFile(manifestPath,
			[]byte(`{"name": "pstack", "skills": "skills", "version": "0.0.2"}`), 0o600)
		return fires.Load() >= 1
	}, testutil.WaitShort, testutil.IntervalFast,
		"expected a fire after editing the nested pack manifest")
}

func TestWatcher_FiresWhenNestedPackManifestAppears(t *testing.T) {
	t.Parallel()

	// A nested candidate directory (.claude-plugin/) can exist before
	// its plugin.json does, or after the manifest was deleted. No pack
	// parses there, so the directory must be watched on its own:
	// otherwise creating the manifest fires an event only in a
	// directory fsnotify does not observe.
	dir := t.TempDir()
	packDir := filepath.Join(dir, "pstack")
	manifestDir := filepath.Join(packDir, ".claude-plugin")
	require.NoError(t, os.MkdirAll(manifestDir, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(packDir, "skills", "review-pr"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(packDir, "skills", "review-pr", "SKILL.md"),
		[]byte("---\nname: review-pr\n---\nbody\n"), 0o600))

	var fires atomic.Int32
	w, err := agentcontext.NewWatcher(agentcontext.WatcherOptions{
		Logger:   testutil.Logger(t).Named("watcher"),
		Debounce: 10 * time.Millisecond,
		OnChange: func() { fires.Add(1) },
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })

	ctx := testutil.Context(t, testutil.WaitShort)
	w.Sync(ctx, []agentcontext.ScanRoot{{Path: dir}})

	// Write the manifest inside Eventually so the test does not race
	// fsnotify's watch-setup window: once the candidate directory's
	// watch is live, the next write fires the debounce timer.
	manifestPath := filepath.Join(manifestDir, "plugin.json")
	require.Eventually(t, func() bool {
		_ = os.WriteFile(manifestPath, []byte(`{"name": "pstack"}`), 0o600)
		return fires.Load() >= 1
	}, testutil.WaitShort, testutil.IntervalFast,
		"expected a fire after creating the nested pack manifest")
}

// TestWatcher_FiresWhenDeclaredPackContainerAppears covers the
// missing-declared-container case: a manifest may declare a nested
// container that does not exist yet ("skills": "components/skills" while
// only components/ is present). fsnotify is non-recursive, so with only
// the pack root watched, creating the container later fires no event and
// the pack stays undiscovered until an unrelated resync. The deepest
// existing ancestor of the declared path must be watched.
func TestWatcher_FiresWhenDeclaredPackContainerAppears(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	packDir := filepath.Join(dir, "pstack")
	manifestDir := filepath.Join(packDir, ".claude-plugin")
	require.NoError(t, os.MkdirAll(manifestDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(manifestDir, "plugin.json"),
		[]byte(`{"name": "pstack", "skills": "components/skills"}`), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(packDir, "components"), 0o755))

	var fires atomic.Int32
	w, err := agentcontext.NewWatcher(agentcontext.WatcherOptions{
		Logger:   testutil.Logger(t).Named("watcher"),
		Debounce: 10 * time.Millisecond,
		OnChange: func() { fires.Add(1) },
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })

	ctx := testutil.Context(t, testutil.WaitShort)
	w.Sync(ctx, []agentcontext.ScanRoot{{Path: dir}})

	// A directory is created once, so recreate it inside Eventually the
	// way the edit test rewrites its file: every pass fires a fresh
	// create event as soon as the watch-setup window has closed, without
	// racing an initial create that could land before the watch is live.
	require.Eventually(t, func() bool {
		skillsDir := filepath.Join(packDir, "components", "skills")
		_ = os.RemoveAll(skillsDir)
		if err := os.MkdirAll(skillsDir, 0o755); err != nil {
			return false
		}
		return fires.Load() >= 1
	}, testutil.WaitShort, testutil.IntervalFast, "expected a fire after creating the declared skills container")
}
