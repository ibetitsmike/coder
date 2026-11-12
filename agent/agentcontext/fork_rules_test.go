package agentcontext_test

// This file is fork code: it tests discovery of .mdc rule files.

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
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

func TestResolver_RuleContainerSymlinkInsideScanRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require admin privileges on Windows runners")
	}
	t.Parallel()
	dir := t.TempDir()
	shared := filepath.Join(dir, "shared-rules")
	mustWriteFile(t, filepath.Join(shared, "go.mdc"), "---\nalwaysApply: true\n---\nTop")
	mustWriteFile(t, filepath.Join(shared, "frontend", "ui.mdc"), "---\nalwaysApply: true\n---\nNested")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cursor"), 0o755))
	require.NoError(t, os.Symlink(shared, filepath.Join(dir, ".cursor", "rules")))

	r := &agentcontext.Resolver{}
	snap := r.Resolve([]agentcontext.ScanRoot{{Path: dir}})

	got := findResource(t, snap.Resources, agentcontext.KindInstructionFile, filepath.Join(dir, ".cursor", "rules", "frontend", "ui.mdc"))
	require.Equal(t, []byte("Nested"), got.Payload, "a symlinked container inside the scan root is walked")
	got = findResource(t, snap.Resources, agentcontext.KindInstructionFile, filepath.Join(dir, ".cursor", "rules", "go.mdc"))
	require.Equal(t, []byte("Top"), got.Payload)
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

func globRulesOn() bool { return true }

func globRule(resources []agentcontext.Resource) *agentcontext.Resource {
	for i := range resources {
		if strings.HasSuffix(resources[i].Source, filepath.Join(".cursor", "rules", "ts.mdc")) {
			return &resources[i]
		}
	}
	return nil
}

func TestResolver_GlobRulesAdvertisedArePushed(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "rules")
	mustWriteFile(t, filepath.Join(dir, "ts.mdc"), "---\nglobs: \"**/*.ts\"\n---\nTS rules")

	snap := (&agentcontext.Resolver{GlobRulesAdvertised: globRulesOn}).Resolve([]agentcontext.ScanRoot{{Path: dir}})
	require.Empty(t, snap.ScopedRules)
	got := findResource(t, snap.Resources, agentcontext.KindInstructionFile, filepath.Join(dir, "ts.mdc"))
	require.Equal(t, []string{"**/*.ts"}, got.Globs)

	client := &fakeDRPCClient{}
	_, err := agentcontext.NewDRPCPusher(client).PushContextState(context.Background(), &agentcontext.PushRequest{
		Resources: snap.Resources,
	})
	require.NoError(t, err)
	require.Len(t, client.lastReq.Resources, 1)
	body := client.lastReq.Resources[0].GetInstructionFile()
	require.Equal(t, []byte("TS rules"), body.GetContent())
	require.Equal(t, []string{"**/*.ts"}, body.GetGlobs())
}

func TestResolver_GlobRulesShareCaps(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T) (string, string) {
		project := t.TempDir()
		rules := filepath.Join(project, ".cursor", "rules")
		mustWriteFile(t, filepath.Join(project, "AGENTS.md"), strings.Repeat("a", 30))
		mustWriteFile(t, filepath.Join(rules, "ts.mdc"), "---\nglobs: \"*.ts\"\n---\n"+strings.Repeat("b", 30))
		return project, filepath.Join(rules, "ts.mdc")
	}

	t.Run("Count", func(t *testing.T) {
		t.Parallel()
		project, rule := setup(t)
		r := &agentcontext.Resolver{MaxResources: 1, GlobRulesAdvertised: globRulesOn}
		snap := r.Resolve([]agentcontext.ScanRoot{{Path: project}})

		require.Equal(t, agentcontext.StatusOK, findResource(t, snap.Resources, agentcontext.KindInstructionFile, filepath.Join(project, "AGENTS.md")).Status,
			"always-on instruction files are kept before glob rules")
		got := findResource(t, snap.Resources, agentcontext.KindInstructionFile, rule)
		require.Equal(t, agentcontext.StatusExcluded, got.Status)
		require.Contains(t, got.Error, "count cap")
	})

	t.Run("Bytes", func(t *testing.T) {
		t.Parallel()
		project, rule := setup(t)
		r := &agentcontext.Resolver{MaxSnapshotBytes: 40, GlobRulesAdvertised: globRulesOn}
		snap := r.Resolve([]agentcontext.ScanRoot{{Path: project}})

		got := findResource(t, snap.Resources, agentcontext.KindInstructionFile, rule)
		require.Equal(t, agentcontext.StatusExcluded, got.Status)
		require.Contains(t, got.Error, "aggregate cap")
		require.Equal(t, []string{"*.ts"}, got.Globs, "an excluded rule keeps its globs")
	})
}

func TestRunPush_GlobRulesFollowCapability(t *testing.T) {
	t.Parallel()
	for _, advertised := range []bool{true, false} {
		t.Run(strconv.FormatBool(advertised), func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			mustWriteFile(t, filepath.Join(dir, ".cursor", "rules", "ts.mdc"), "---\nglobs: \"*.ts\"\n---\nTS rules")
			m := newTestManager(t, agentcontext.ManagerOptions{
				WorkingDir: func() string { return dir },
			})

			p := newFakePusher()
			p.mu.Lock()
			p.resp = &agentcontext.PushResponse{Accepted: true, ForkGlobRules: advertised}
			p.mu.Unlock()
			ctx, cancel := context.WithCancel(testutil.Context(t, testutil.WaitShort))
			defer cancel()
			pushDone := make(chan error, 1)
			go func() {
				pushDone <- m.RunPush(ctx, p, agentcontext.PushOptions{
					Logger: testutil.Logger(t).Named("push"),
				})
			}()

			settled := waitSettled(t, p, 1)
			for _, req := range p.snapshot() {
				if req.CapabilityGeneration != 0 {
					require.Nil(t, globRule(req.Resources), "a probe never carries glob rules")
				}
			}
			got := globRule(settled[0].Resources)
			if advertised {
				require.NotNil(t, got, "an advertising server receives the glob rule")
				require.Equal(t, []string{"*.ts"}, got.Globs)
			} else {
				require.Nil(t, got, "a server without the capability never receives the glob rule")
			}

			cancel()
			require.ErrorIs(t, <-pushDone, context.Canceled)
		})
	}
}
