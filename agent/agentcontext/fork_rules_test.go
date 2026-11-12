package agentcontext_test

// This file is fork code: it tests discovery of .mdc rule files.

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/agent/agentcontext"
	"github.com/coder/coder/v2/testutil"
)

func instructionResources(resources []agentcontext.Resource) []agentcontext.Resource {
	var out []agentcontext.Resource
	for _, r := range resources {
		if r.Kind == agentcontext.KindInstructionFile {
			out = append(out, r)
		}
	}
	return out
}

func TestResolver_RuleDirectories(t *testing.T) {
	t.Parallel()
	project := t.TempDir()
	coderRules := filepath.Join(t.TempDir(), ".coder", "rules")
	cursorRules := filepath.Join(t.TempDir(), ".cursor", "rules")
	projectRules := filepath.Join(project, ".cursor", "rules")

	always := "---\ndescription: Go style\nalwaysApply: true\n---\nUse gofmt.\n"
	mustWriteFile(t, filepath.Join(projectRules, "go.mdc"), always)
	mustWriteFile(t, filepath.Join(projectRules, "frontend", "components.mdc"), "---\nalwaysApply: true\n---\nNamed exports")
	mustWriteFile(t, filepath.Join(projectRules, "ts.mdc"), "---\nglobs: **/*.ts, *.tsx\nalwaysApply: false\n---\nTS rules")
	mustWriteFile(t, filepath.Join(projectRules, "manual.mdc"), "---\ndescription: Ask for it\nalwaysApply: false\n---\nManual")
	mustWriteFile(t, filepath.Join(projectRules, "plain.mdc"), "No front-matter")
	mustWriteFile(t, filepath.Join(projectRules, "notes.md"), "---\nalwaysApply: true\n---\nNot a rule file")
	mustWriteFile(t, filepath.Join(projectRules, "broken.mdc"), "---\nalwaysApply: true\nno closing delimiter")
	mustWriteFile(t, filepath.Join(project, "rules", "top.mdc"), "---\nalwaysApply: true\n---\nOnly rules scan roots and .cursor/rules count")
	mustWriteFile(t, filepath.Join(coderRules, "coder.mdc"), "---\nalwaysApply: true\n---\n# Coder user rule\nBody")
	mustWriteFile(t, filepath.Join(cursorRules, "cursor.mdc"), "---\nalwaysApply: true\nglobs: \"*.go\"\n---\nCursor user rule")

	r := &agentcontext.Resolver{}
	snap := r.Resolve([]agentcontext.ScanRoot{
		{Path: coderRules},
		{Path: cursorRules, UserSource: cursorRules},
		{Path: project},
	})

	require.Len(t, instructionResources(snap.Resources), 5, "manual, plain, nested and non-.mdc files are not rules")
	got := findResource(t, snap.Resources, agentcontext.KindInstructionFile, filepath.Join(projectRules, "frontend", "components.mdc"))
	require.Equal(t, []byte("Named exports"), got.Payload, "rules in subfolders of a rule directory load")

	got = findResource(t, snap.Resources, agentcontext.KindInstructionFile, filepath.Join(projectRules, "go.mdc"))
	require.Equal(t, agentcontext.StatusOK, got.Status)
	require.Equal(t, []byte("Use gofmt."), got.Payload, "the payload is the body without front-matter")
	require.Equal(t, "Go style", got.Description)
	require.Equal(t, sha256.Sum256([]byte(always)), got.ContentHash, "the hash covers the front-matter too")
	require.Empty(t, got.Globs)

	got = findResource(t, snap.Resources, agentcontext.KindInstructionFile, filepath.Join(projectRules, "broken.mdc"))
	require.Equal(t, agentcontext.StatusInvalid, got.Status)
	require.Empty(t, got.Payload)
	require.Contains(t, got.Error, "missing closing front-matter delimiter")

	got = findResource(t, snap.Resources, agentcontext.KindInstructionFile, filepath.Join(coderRules, "coder.mdc"))
	require.Equal(t, agentcontext.StatusOK, got.Status)
	require.Equal(t, "Coder user rule", got.Description, "description falls back to the first body line")

	got = findResource(t, snap.Resources, agentcontext.KindInstructionFile, filepath.Join(cursorRules, "cursor.mdc"))
	require.Equal(t, agentcontext.StatusOK, got.Status)
	require.Equal(t, cursorRules, got.SourcePath)
	require.Empty(t, got.Globs, "alwaysApply wins over globs")

	require.Len(t, snap.ScopedRules, 1)
	scoped := snap.ScopedRules[0]
	require.Equal(t, filepath.Join(projectRules, "ts.mdc"), scoped.Source)
	require.Equal(t, agentcontext.StatusOK, scoped.Status)
	require.Equal(t, []string{"**/*.ts", "*.tsx"}, scoped.Globs)
	require.Equal(t, []byte("TS rules"), scoped.Payload)
	for _, res := range snap.Resources {
		require.NotEqual(t, scoped.Source, res.Source, "scoped rules are not pushed as instructions")
	}
}

func TestResolver_OversizeRules(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("a", 200)
	tests := []struct {
		name        string
		frontmatter string
		pushed      bool
		scoped      bool
	}{
		{name: "AlwaysApply", frontmatter: "alwaysApply: true", pushed: true},
		{name: "Scoped", frontmatter: "globs: \"*.go\"", scoped: true},
		{name: "Manual", frontmatter: "description: Manual"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := filepath.Join(t.TempDir(), "rules")
			mustWriteFile(t, filepath.Join(dir, "big.mdc"), "---\n"+tt.frontmatter+"\n---\n"+body)

			r := &agentcontext.Resolver{MaxResourceBytes: 100}
			snap := r.Resolve([]agentcontext.ScanRoot{{Path: dir}})

			pushed := instructionResources(snap.Resources)
			switch {
			case tt.pushed:
				require.Len(t, pushed, 1)
				require.Empty(t, snap.ScopedRules)
				require.Equal(t, agentcontext.StatusOversize, pushed[0].Status)
				require.Empty(t, pushed[0].Payload)
			case tt.scoped:
				require.Empty(t, pushed)
				require.Len(t, snap.ScopedRules, 1)
				require.Equal(t, agentcontext.StatusOversize, snap.ScopedRules[0].Status)
				require.Equal(t, []string{"*.go"}, snap.ScopedRules[0].Globs)
				require.Empty(t, snap.ScopedRules[0].Payload)
			default:
				require.Empty(t, pushed, "an oversize rule that applies neither always nor by glob is ignored")
				require.Empty(t, snap.ScopedRules)
			}
		})
	}
}

func TestResolver_AlwaysApplyRuleRespectsAggregateCap(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "rules")
	mustWriteFile(t, filepath.Join(dir, "a.mdc"), "---\nalwaysApply: true\n---\n"+strings.Repeat("a", 30))
	mustWriteFile(t, filepath.Join(dir, "b.mdc"), "---\nalwaysApply: true\n---\n"+strings.Repeat("b", 30))

	r := &agentcontext.Resolver{MaxSnapshotBytes: 40}
	snap := r.Resolve([]agentcontext.ScanRoot{{Path: dir}})

	require.Equal(t, agentcontext.StatusOK, findResource(t, snap.Resources, agentcontext.KindInstructionFile, filepath.Join(dir, "a.mdc")).Status)
	require.Equal(t, agentcontext.StatusExcluded, findResource(t, snap.Resources, agentcontext.KindInstructionFile, filepath.Join(dir, "b.mdc")).Status)
}

func TestResolver_RuleContainerSymlinkOutsideScanRootSkipped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require admin privileges on Windows runners")
	}
	t.Parallel()
	dir := t.TempDir()
	outside := t.TempDir()
	mustWriteFile(t, filepath.Join(outside, "secret.mdc"), "---\nalwaysApply: true\n---\ntoken")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cursor"), 0o755))
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, ".cursor", "rules")))

	r := &agentcontext.Resolver{}
	snap := r.Resolve([]agentcontext.ScanRoot{{Path: dir}})

	require.Empty(t, instructionResources(snap.Resources), "files under an escaping container must not be shipped")
}

func TestDRPCPusher_AlwaysApplyRuleBody(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "rules")
	mustWriteFile(t, filepath.Join(dir, "go.mdc"), "---\nalwaysApply: true\n---\nUse gofmt.")
	snap := (&agentcontext.Resolver{}).Resolve([]agentcontext.ScanRoot{{Path: dir}})

	client := &fakeDRPCClient{}
	_, err := agentcontext.NewDRPCPusher(client).PushContextState(context.Background(), &agentcontext.PushRequest{
		Resources: snap.Resources,
	})
	require.NoError(t, err)

	require.Len(t, client.lastReq.Resources, 1)
	require.Equal(t, filepath.Join(dir, "go.mdc"), client.lastReq.Resources[0].Source)
	require.Equal(t, []byte("Use gofmt."), client.lastReq.Resources[0].GetInstructionFile().GetContent())
}

func TestWatcher_Rules(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		setup  func(t *testing.T, dir string)
		change func(dir string) error
	}{
		{
			name: "RuleEdit",
			setup: func(t *testing.T, dir string) {
				mustWriteFile(t, filepath.Join(dir, ".cursor", "rules", "go.mdc"), "---\nalwaysApply: true\n---\nv1")
			},
			// Rewrite the existing file so only a Write event fires.
			change: func(dir string) error {
				return os.WriteFile(filepath.Join(dir, ".cursor", "rules", "go.mdc"), []byte("---\nalwaysApply: true\n---\nv2"), 0o600)
			},
		},
		{
			name: "NestedRuleEdit",
			setup: func(t *testing.T, dir string) {
				mustWriteFile(t, filepath.Join(dir, ".cursor", "rules", "frontend", "ui.mdc"), "---\nalwaysApply: true\n---\nv1")
			},
			change: func(dir string) error {
				return os.WriteFile(filepath.Join(dir, ".cursor", "rules", "frontend", "ui.mdc"), []byte("---\nalwaysApply: true\n---\nv2"), 0o600)
			},
		},
		{
			name: "RulesDirCreatedUnderExistingCursorDir",
			setup: func(t *testing.T, dir string) {
				require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cursor"), 0o755))
			},
			change: func(dir string) error {
				return os.Mkdir(filepath.Join(dir, ".cursor", "rules"), 0o755)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			tt.setup(t, dir)

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

			require.Eventually(t, func() bool {
				_ = tt.change(dir)
				return fires.Load() >= 1
			}, testutil.WaitShort, testutil.IntervalFast, "expected fire after %s", tt.name)
		})
	}
}
