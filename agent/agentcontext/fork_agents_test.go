package agentcontext_test

// This file is fork code: it tests discovery of custom agent definitions.

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/agent/agentcontext"
	"github.com/coder/coder/v2/testutil"
)

func subagentResources(resources []agentcontext.Resource) []agentcontext.Resource {
	var out []agentcontext.Resource
	for _, r := range resources {
		if r.Kind == agentcontext.KindSubagent {
			out = append(out, r)
		}
	}
	return out
}

func TestResolver_AgentDefinitions(t *testing.T) {
	t.Parallel()
	project := t.TempDir()
	coderHome := filepath.Join(t.TempDir(), ".coder")

	reviewer := "---\nname: reviewer\ndescription: Reviews diffs.\ntools: Read, Grep\n---\nReview the diff."
	mustWriteFile(t, filepath.Join(project, ".claude", "agents", "reviewer.md"), reviewer)
	mustWriteFile(t, filepath.Join(project, ".claude", "agents", "notes.txt"), "---\nname: notes\n---\nnot markdown")
	mustWriteFile(t, filepath.Join(project, ".cursor", "agents", "x.md"), "---\ndescription: Cursor agent.\npermissionMode: plan\n---\nDo x.")
	mustWriteFile(t, filepath.Join(project, ".agents", "agents", "broken.md"), "---\nname: broken\nno closing delimiter")
	poteto := "---\nname: poteto-agent\ndescription: Runs the loop.\nis_background: true\n---\nLoop."
	mustWriteFile(t, filepath.Join(coderHome, "agents", "poteto-agent.md"), poteto)
	mustWriteFile(t, filepath.Join(coderHome, "agents", "README.md"), "# Agents\n\nDrop agent files here.\n")

	r := &agentcontext.Resolver{}
	snap := r.Resolve([]agentcontext.ScanRoot{{Path: project}, {Path: coderHome, UserSource: coderHome}})

	require.Len(t, subagentResources(snap.Resources), 4, "README.md and notes.txt are not agent definitions")

	got := findResource(t, snap.Resources, agentcontext.KindSubagent, filepath.Join(project, ".claude", "agents", "reviewer.md"))
	require.Equal(t, agentcontext.StatusOK, got.Status)
	require.Equal(t, "reviewer", got.Name)
	require.Equal(t, "Reviews diffs.", got.Description)
	require.Equal(t, []byte(reviewer), got.Payload)
	require.Equal(t, sha256.Sum256([]byte(reviewer)), got.ContentHash)
	require.Empty(t, got.Error)

	got = findResource(t, snap.Resources, agentcontext.KindSubagent, filepath.Join(project, ".cursor", "agents", "x.md"))
	require.Equal(t, agentcontext.StatusOK, got.Status)
	require.Equal(t, "x", got.Name, "name falls back to the file name")
	require.Equal(t, `unknown front-matter field "permissionMode" ignored`, got.Error)

	got = findResource(t, snap.Resources, agentcontext.KindSubagent, filepath.Join(project, ".agents", "agents", "broken.md"))
	require.Equal(t, agentcontext.StatusInvalid, got.Status)
	require.Empty(t, got.Payload)
	require.Contains(t, got.Error, "missing closing frontmatter delimiter")

	got = findResource(t, snap.Resources, agentcontext.KindSubagent, filepath.Join(coderHome, "agents", "poteto-agent.md"))
	require.Equal(t, agentcontext.StatusOK, got.Status)
	require.Equal(t, "poteto-agent", got.Name)
	require.Equal(t, coderHome, got.SourcePath)
	require.Empty(t, got.Error)
}

func TestResolver_DuplicateAgentNamesFirstRootWins(t *testing.T) {
	t.Parallel()
	first := t.TempDir()
	second := t.TempDir()
	mustWriteFile(t, filepath.Join(first, ".claude", "agents", "reviewer.md"), "---\ndescription: first\n---\nFirst.")
	mustWriteFile(t, filepath.Join(second, ".cursor", "agents", "other.md"), "---\nname: reviewer\ndescription: second\n---\nSecond.")

	r := &agentcontext.Resolver{}
	snap := r.Resolve([]agentcontext.ScanRoot{{Path: first}, {Path: second}})

	agents := subagentResources(snap.Resources)
	require.Len(t, agents, 1)
	require.Equal(t, "first", agents[0].Description)
	require.Equal(t, filepath.Join(first, ".claude", "agents", "reviewer.md"), agents[0].Source)
}

func TestResolver_AgentDefinitionsInAgentsScanRoot(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "agents")
	mustWriteFile(t, filepath.Join(root, "reviewer.md"), "---\ndescription: d\n---\nReview.")

	r := &agentcontext.Resolver{}
	snap := r.Resolve([]agentcontext.ScanRoot{{Path: root}})

	got := findResource(t, snap.Resources, agentcontext.KindSubagent, filepath.Join(root, "reviewer.md"))
	require.Equal(t, agentcontext.StatusOK, got.Status)
	require.Equal(t, "reviewer", got.Name)
}

func TestResolver_AgentDefinitionSymlinkOutsideScanRootRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require admin privileges on Windows runners")
	}
	t.Parallel()
	dir := t.TempDir()
	secret := filepath.Join(t.TempDir(), "secret.md")
	mustWriteFile(t, secret, "---\nname: secret\ndescription: d\n---\ntoken")
	link := filepath.Join(dir, ".claude", "agents", "secret.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(link), 0o755))
	require.NoError(t, os.Symlink(secret, link))

	r := &agentcontext.Resolver{}
	snap := r.Resolve([]agentcontext.ScanRoot{{Path: dir}})

	got := findResource(t, snap.Resources, agentcontext.KindSubagent, link)
	require.Equal(t, agentcontext.StatusInvalid, got.Status)
	require.Empty(t, got.Payload, "escaping symlink target must not be shipped")
	require.Contains(t, got.Error, "escapes scan root")
}

func TestDRPCPusher_SubagentBody(t *testing.T) {
	t.Parallel()
	client := &fakeDRPCClient{}
	pusher := agentcontext.NewDRPCPusher(client)

	definition := []byte("---\nname: reviewer\ndescription: Reviews diffs.\nfoo: bar\n---\nReview.")
	_, err := pusher.PushContextState(context.Background(), &agentcontext.PushRequest{
		Resources: []agentcontext.Resource{{
			ID:          "subagent:/tmp/.claude/agents/reviewer.md",
			Kind:        agentcontext.KindSubagent,
			Source:      "/tmp/.claude/agents/reviewer.md",
			Payload:     definition,
			Status:      agentcontext.StatusOK,
			Error:       `unknown front-matter field "foo" ignored`,
			Name:        "reviewer",
			Description: "Reviews diffs.",
		}},
	})
	require.NoError(t, err)

	require.Len(t, client.lastReq.Resources, 1)
	entry := client.lastReq.Resources[0]
	require.Equal(t, `unknown front-matter field "foo" ignored`, entry.Error)
	body := entry.GetSubagent()
	require.NotNil(t, body, "subagent body must be set")
	require.Equal(t, definition, body.GetDefinition())
	require.Equal(t, "reviewer", body.GetName())
	require.Equal(t, "Reviews diffs.", body.GetDescription())
}

func TestWatcher_FiresOnAgentDefinitionEdit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude", "agents", "reviewer.md")
	mustWriteFile(t, path, "---\ndescription: v1\n---\nv1")

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

	// Rewrite the existing file so only a Write event fires: the
	// container must be watched and the path must count as relevant.
	require.Eventually(t, func() bool {
		_ = os.WriteFile(path, []byte("---\ndescription: v2\n---\nv2"), 0o600)
		return fires.Load() >= 1
	}, testutil.WaitShort, testutil.IntervalFast, "expected fire after agent definition edit")
}
