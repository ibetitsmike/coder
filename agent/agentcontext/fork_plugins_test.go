package agentcontext_test

// This file is fork code: it tests plugin pack discovery (PRD 6.7 R7.1,
// R7.4, R7.5). Pack resources are stamped with the pack's manifest path in
// SourcePath so coderd's fork-plugin gate can identify them.

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/agent/agentcontext"
)

func skillName(name string) string {
	return fmt.Sprintf("---\nname: %s\ndescription: A pack skill.\n---\nDo the thing.", name)
}

func packAgent(name string) string {
	return fmt.Sprintf("---\nname: %s\ndescription: A pack agent.\n---\nRun the loop.", name)
}

// writePack lays out a pack with a manifest and optional skill and agent
// containers under root.
func writePack(t *testing.T, root, manifestRel, manifestJSON string, skillsDir, agentsDir string) string {
	t.Helper()
	packDir := filepath.Join(root, "pstack")
	manifestPath := filepath.Join(packDir, filepath.FromSlash(manifestRel))
	mustWriteFile(t, manifestPath, manifestJSON)
	if skillsDir != "" {
		mustWriteFile(t, filepath.Join(packDir, skillsDir, "review-pr", "SKILL.md"), skillName("review-pr"))
	}
	if agentsDir != "" {
		mustWriteFile(t, filepath.Join(packDir, agentsDir, "comment-sicko.md"), packAgent("comment-sicko"))
	}
	return packDir
}

func TestResolver_PluginPacks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	manifest := `{"name": "pstack", "version": "0.15.36", "description": "Deep-first workflows."}`
	writePack(t, root, ".claude-plugin/plugin.json", manifest, "skills", "agents")

	r := &agentcontext.Resolver{}
	snap := r.Resolve([]agentcontext.ScanRoot{{Path: root}})

	skill := findResource(t, snap.Resources, agentcontext.KindSkill, filepath.Join(root, "pstack", "skills", "review-pr"))
	require.Equal(t, agentcontext.StatusOK, skill.Status)
	require.Equal(t, "review-pr", skill.Name)

	agent := findResource(t, snap.Resources, agentcontext.KindSubagent, filepath.Join(root, "pstack", "agents", "comment-sicko.md"))
	require.Equal(t, agentcontext.StatusOK, agent.Status)
	require.Equal(t, "comment-sicko", agent.Name)

	require.Equal(t, filepath.Join(root, "pstack", ".claude-plugin", "plugin.json"), skill.SourcePath)
	require.Equal(t, skill.SourcePath, agent.SourcePath)
}

func TestResolver_PluginPacksRootIsPack(t *testing.T) {
	t.Parallel()

	// A scan root that is itself a pack must stamp everything it emits
	// with the manifest path, so coderd can drop its resources when
	// fork-plugins is off. Ordinary container discovery would claim the
	// same files with the container path as Source and lose the stamp.
	root := t.TempDir()
	manifest := `{"name": "pstack"}`
	mustWriteFile(t, filepath.Join(root, "plugin.json"), manifest)
	mustWriteFile(t, filepath.Join(root, "AGENTS.md"), "Pack instructions.\n")
	mustWriteFile(t, filepath.Join(root, "skills", "review-pr", "SKILL.md"), skillName("review-pr"))
	mustWriteFile(t, filepath.Join(root, "agents", "comment-sicko.md"), packAgent("comment-sicko"))

	r := &agentcontext.Resolver{}
	snap := r.Resolve([]agentcontext.ScanRoot{{Path: root}})

	manifestPath := filepath.Join(root, "plugin.json")
	for _, res := range snap.Resources {
		require.Equal(t, manifestPath, res.SourcePath,
			"every resource of a pack root carries the manifest path, got %v with %q", res.Kind, res.Source)
	}
	findResource(t, snap.Resources, agentcontext.KindSkill, filepath.Join(root, "skills", "review-pr"))
	findResource(t, snap.Resources, agentcontext.KindSubagent, filepath.Join(root, "agents", "comment-sicko.md"))
}

func TestResolver_PluginPacksCursorManifestDeclaredDirs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	manifest := `{"name": "pstack", "skills": "./skills/", "agents": "./agents/"}`
	writePack(t, root, ".cursor-plugin/plugin.json", manifest, "skills", "agents")

	r := &agentcontext.Resolver{}
	snap := r.Resolve([]agentcontext.ScanRoot{{Path: root}})

	skill := findResource(t, snap.Resources, agentcontext.KindSkill, filepath.Join(root, "pstack", "skills", "review-pr"))
	require.Equal(t, agentcontext.StatusOK, skill.Status)
	agent := findResource(t, snap.Resources, agentcontext.KindSubagent, filepath.Join(root, "pstack", "agents", "comment-sicko.md"))
	require.Equal(t, agentcontext.StatusOK, agent.Status)
}

func TestResolver_PluginPacksRootManifestDefaults(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	manifest := `{"$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json", "name": "pstack", "version": "0.15.36"}`
	writePack(t, root, "plugin.json", manifest, "skills", "agents")

	r := &agentcontext.Resolver{}
	snap := r.Resolve([]agentcontext.ScanRoot{{Path: root}})

	skill := findResource(t, snap.Resources, agentcontext.KindSkill, filepath.Join(root, "pstack", "skills", "review-pr"))
	require.Equal(t, agentcontext.StatusOK, skill.Status)
	agent := findResource(t, snap.Resources, agentcontext.KindSubagent, filepath.Join(root, "pstack", "agents", "comment-sicko.md"))
	require.Equal(t, agentcontext.StatusOK, agent.Status)
}

func TestResolver_PluginPacksRejectsEscapingPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "outside", "esc", "SKILL.md"), skillName("esc"))
	mustWriteFile(t, filepath.Join(root, "outside", "runner.md"), packAgent("runner"))
	manifest := `{"name": "pstack", "skills": "../outside", "agents": "/etc"}`
	writePack(t, root, "plugin.json", manifest, "", "")

	r := &agentcontext.Resolver{}
	snap := r.Resolve([]agentcontext.ScanRoot{{Path: root}})

	for _, res := range snap.Resources {
		require.NotEqual(t, "esc", res.Name, "a pack must not discover containers outside the pack root")
		require.NotEqual(t, "runner", res.Name, "a pack must not discover agents outside the pack root")
	}
}

func TestResolver_PluginPacksInvalidManifest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writePack(t, root, "plugin.json", `{"name": broken`, "skills", "agents")

	r := &agentcontext.Resolver{}
	snap := r.Resolve([]agentcontext.ScanRoot{{Path: root}})

	require.Empty(t, snap.Resources, "a pack whose manifest does not parse contributes nothing")
}

func TestResolver_PluginPacksYieldToUserSkills(t *testing.T) {
	t.Parallel()

	userRoot := t.TempDir()
	packRoot := t.TempDir()
	mustWriteFile(t, filepath.Join(userRoot, "skills", "review-pr", "SKILL.md"), skillName("review-pr"))
	manifest := `{"name": "pstack"}`
	writePack(t, packRoot, "plugin.json", manifest, "skills", "")

	// The built-in roots order user config before the plugin cache, so the
	// user's skill must win the name collision.
	r := &agentcontext.Resolver{}
	snap := r.Resolve([]agentcontext.ScanRoot{
		{Path: userRoot},
		{Path: packRoot},
	})

	var skills []agentcontext.Resource
	for _, res := range snap.Resources {
		if res.Kind == agentcontext.KindSkill && res.Status == agentcontext.StatusOK && res.Name == "review-pr" {
			skills = append(skills, res)
		}
	}
	require.Len(t, skills, 1, "first valid skill with a name wins")
	require.Equal(t, filepath.Join(userRoot, "skills", "review-pr"), skills[0].Source)
	require.Equal(t, "", skills[0].SourcePath, "the winner is the user skill, not the pack skill")
}

func TestResolver_PluginPacksDirectChildrenOnly(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	manifest := `{"name": "nested"}`
	mustWriteFile(t, filepath.Join(root, "vendor", "deep", "plugin.json"), manifest)
	mustWriteFile(t, filepath.Join(root, "vendor", "deep", "skills", "review-pr", "SKILL.md"), skillName("review-pr"))
	writePack(t, root, "plugin.json", `{"name": "pstack"}`, "skills", "")

	r := &agentcontext.Resolver{}
	snap := r.Resolve([]agentcontext.ScanRoot{{Path: root}})

	for _, res := range snap.Resources {
		require.NotContains(t, res.Source, filepath.Join("vendor", "deep"),
			"only direct children of a scan root are pack roots")
	}
	skill := findResource(t, snap.Resources, agentcontext.KindSkill, filepath.Join(root, "pstack", "skills", "review-pr"))
	require.Equal(t, agentcontext.StatusOK, skill.Status)
}
