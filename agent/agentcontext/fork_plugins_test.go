package agentcontext_test

// This file is fork code: it tests plugin pack discovery (PRD 6.7 R7.1,
// R7.4, R7.5). Pack resources are stamped with the pack's manifest path in
// SourcePath so coderd's fork-plugin gate can identify them.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	r.PackRowsAdvertised = func() bool { return true }
	r.PackAgentDefinitionsAdvertised = func() bool { return true }
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
	// fork-plugin-packs is off. Ordinary container discovery would claim the
	// same files with the container path as Source and lose the stamp.
	root := t.TempDir()
	manifest := `{"name": "pstack"}`
	mustWriteFile(t, filepath.Join(root, "plugin.json"), manifest)
	mustWriteFile(t, filepath.Join(root, "AGENTS.md"), "Pack instructions.\n")
	mustWriteFile(t, filepath.Join(root, "skills", "review-pr", "SKILL.md"), skillName("review-pr"))
	mustWriteFile(t, filepath.Join(root, "agents", "comment-sicko.md"), packAgent("comment-sicko"))

	r := &agentcontext.Resolver{}
	r.PackRowsAdvertised = func() bool { return true }
	r.PackAgentDefinitionsAdvertised = func() bool { return true }
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
	r.PackRowsAdvertised = func() bool { return true }
	r.PackAgentDefinitionsAdvertised = func() bool { return true }
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
	r.PackRowsAdvertised = func() bool { return true }
	r.PackAgentDefinitionsAdvertised = func() bool { return true }
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
	r.PackRowsAdvertised = func() bool { return true }
	r.PackAgentDefinitionsAdvertised = func() bool { return true }
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
	r.PackRowsAdvertised = func() bool { return true }
	r.PackAgentDefinitionsAdvertised = func() bool { return true }
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
	r.PackRowsAdvertised = func() bool { return true }
	r.PackAgentDefinitionsAdvertised = func() bool { return true }
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
	r.PackRowsAdvertised = func() bool { return true }
	r.PackAgentDefinitionsAdvertised = func() bool { return true }
	snap := r.Resolve([]agentcontext.ScanRoot{{Path: root}})

	for _, res := range snap.Resources {
		require.NotContains(t, res.Source, filepath.Join("vendor", "deep"),
			"only direct children of a scan root are pack roots")
	}
	skill := findResource(t, snap.Resources, agentcontext.KindSkill, filepath.Join(root, "pstack", "skills", "review-pr"))
	require.Equal(t, agentcontext.StatusOK, skill.Status)
}

func TestResolver_PluginPacksSymlinkEscape(t *testing.T) {
	t.Parallel()

	// A symlinked SKILL.md inside a pack may target only files inside
	// the pack, even when the target stays inside the enclosing scan
	// root: the pack, not the scan root, is the read boundary.
	root := t.TempDir()
	writePack(t, root, "plugin.json", `{"name": "pstack"}`, "skills", "")
	mustWriteFile(t, filepath.Join(root, "outside.md"), skillName("outside"))
	if err := os.Symlink(
		filepath.Join(root, "outside.md"),
		filepath.Join(root, "pstack", "skills", "review-pr", "SKILL.md"),
	); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	r := &agentcontext.Resolver{}
	r.PackRowsAdvertised = func() bool { return true }
	r.PackAgentDefinitionsAdvertised = func() bool { return true }
	snap := r.Resolve([]agentcontext.ScanRoot{{Path: root}})

	for _, res := range snap.Resources {
		if res.Kind == agentcontext.KindSkill && res.Name == "outside" {
			t.Fatalf("a pack skill symlinked outside the pack was discovered: %+v", res)
		}
	}
}

func TestResolver_PluginPacksManifestTooLarge(t *testing.T) {
	t.Parallel()

	// A manifest larger than the per-resource payload cap is not a pack;
	// probing it must not read an unbounded file.
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "pstack", "plugin.json"),
		`{"name": "pstack", "pad": "`+strings.Repeat("x", 70*1024)+`"}`)
	mustWriteFile(t, filepath.Join(root, "pstack", "skills", "review-pr", "SKILL.md"), skillName("review-pr"))

	r := &agentcontext.Resolver{}
	r.PackRowsAdvertised = func() bool { return true }
	r.PackAgentDefinitionsAdvertised = func() bool { return true }
	snap := r.Resolve([]agentcontext.ScanRoot{{Path: root}})

	for _, res := range snap.Resources {
		require.NotEqual(t, agentcontext.StatusOK, res.Status,
			"an oversized manifest must not contribute resources")
	}
}

func TestResolver_PluginPacksSortedLastAtCaps(t *testing.T) {
	t.Parallel()

	// Pack contributions lose cap competitions to ordinary resources, so
	// the chatd fork-plugin filter can never leave an ordinary resource
	// excluded on behalf of a hidden pack.
	root := t.TempDir()
	manifest := `{"name": "pstack"}`
	writePack(t, root, "plugin.json", manifest, "skills", "agents")
	var names []string
	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("user-skill-%02d", i)
		names = append(names, name)
		mustWriteFile(t, filepath.Join(root, "user", "skills", name, "SKILL.md"), skillName(name))
	}

	r := &agentcontext.Resolver{MaxResources: len(names) + 1}
	r.PackRowsAdvertised = func() bool { return true }
	r.PackAgentDefinitionsAdvertised = func() bool { return true }
	snap := r.Resolve([]agentcontext.ScanRoot{
		{Path: filepath.Join(root, "user")},
		{Path: filepath.Join(root, "pstack")},
	})

	byName := map[string]agentcontext.Resource{}
	for _, res := range snap.Resources {
		byName[res.Name] = res
	}
	for _, name := range names {
		require.Equal(t, agentcontext.StatusOK, byName[name].Status,
			"an ordinary user skill must keep its cap slot")
	}
	cappedPack := 0
	for _, res := range snap.Resources {
		// The manifest base name in SourcePath is what marks a pack
		// contribution (the chatd gate matches on it too), and the cut
		// must land on one of those, never on an ordinary skill.
		if res.SourcePath != "" && filepath.Base(res.SourcePath) == "plugin.json" && res.Status != agentcontext.StatusOK {
			cappedPack++
		}
	}
	if cappedPack < 1 {
		for _, res := range snap.Resources {
			t.Logf("snapshot row kind=%v name=%q status=%v source=%q sourcePath=%q",
				res.Kind, res.Name, res.Status, res.Source, res.SourcePath)
		}
	}
	require.GreaterOrEqual(t, cappedPack, 1, "a pack contribution must be the capped resource")
}

func TestResolver_PluginPacksManifestSymlinkRejected(t *testing.T) {
	t.Parallel()

	// A manifest that is itself a symlink is not a pack: Lstat must
	// reject it before any read, however small the target reports
	// itself. The pack-style skills container must stay undiscovered:
	// if the symlinked manifest were accepted, the directory would
	// classify as a pack and its skill would come back stamped.
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "real.json"), `{"name": "pstack"}`)
	mustWriteFile(t, filepath.Join(root, "pstack", "skills", "review-pr", "SKILL.md"), skillName("review-pr"))
	if err := os.Symlink(
		filepath.Join(root, "real.json"),
		filepath.Join(root, "pstack", "plugin.json"),
	); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	r := &agentcontext.Resolver{}
	r.PackRowsAdvertised = func() bool { return true }
	r.PackAgentDefinitionsAdvertised = func() bool { return true }
	snap := r.Resolve([]agentcontext.ScanRoot{{Path: root}})

	for _, res := range snap.Resources {
		require.NotEqual(t, filepath.Join(root, "pstack", "plugin.json"), res.SourcePath,
			"a symlinked manifest must not classify the directory as a pack")
		require.NotContains(t, res.Source, filepath.Join(root, "pstack"),
			"nothing from a pack with a symlinked manifest may be discovered")
	}
}

func TestResolver_PluginPacksNamelessManifest(t *testing.T) {
	t.Parallel()

	// A plugin.json that parses but declares no name is not a pack:
	// the directory keeps ordinary discovery and nothing is stamped
	// with the pack marker. Ordinary discovery never reaches a child
	// directory's files, so the pack-style skill under pstack must
	// stay undiscovered too; if a nameless manifest were accepted,
	// that skill would come back stamped.
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "AGENTS.md"), "Root instructions.\n")
	mustWriteFile(t, filepath.Join(root, "pstack", "plugin.json"), `{}`)
	mustWriteFile(t, filepath.Join(root, "pstack", "skills", "review-pr", "SKILL.md"), skillName("review-pr"))

	r := &agentcontext.Resolver{}
	r.PackRowsAdvertised = func() bool { return true }
	r.PackAgentDefinitionsAdvertised = func() bool { return true }
	snap := r.Resolve([]agentcontext.ScanRoot{{Path: root}})

	for _, res := range snap.Resources {
		require.Empty(t, res.SourcePath,
			"a nameless manifest must not classify the directory as a pack")
		require.NotContains(t, res.Source, filepath.Join(root, "pstack"),
			"nothing from a pack with a nameless manifest may be discovered")
	}
	findResource(t, snap.Resources, agentcontext.KindInstructionFile, filepath.Join(root, "AGENTS.md"))
}

func TestResolver_PluginPacksExcludedFromDrift(t *testing.T) {
	t.Parallel()

	// Installing or editing a pack must not change the drift hash
	// while fork-plugin-packs is off: pack rows are filtered from the
	// prompt by chatd, so they cannot read as prompt drift. Resolve
	// the same directory before and after the pack appears: the
	// hash covers absolute sources, so only a same-root comparison
	// can be equal.
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "skills", "review-pr", "SKILL.md"), skillName("review-pr"))

	r := &agentcontext.Resolver{}
	r.PackRowsAdvertised = func() bool { return true }
	r.PackAgentDefinitionsAdvertised = func() bool { return true }
	h1 := r.Resolve([]agentcontext.ScanRoot{{Path: root}}).AggregateHash

	writePack(t, root, "plugin.json", `{"name": "pstack"}`, "skills", "agents")

	h2 := r.Resolve([]agentcontext.ScanRoot{{Path: root}}).AggregateHash
	require.Equal(t, h1, h2, "a pack must not change the drift hash")
}

func TestResolver_PluginPacksRequireAdvertisedCapability(t *testing.T) {
	t.Parallel()

	// Pack rows reach shared tables only after the server advertised the
	// push-response capability. A resolver without the advertisement (an
	// older coderd replica, or the experiment off) must discover nothing
	// from packs, even with one fully laid out on disk.
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "AGENTS.md"), "Root instructions.\n")
	writePack(t, root, "plugin.json", `{"name": "pstack"}`, "skills", "agents")

	for name, advertised := range map[string]func() bool{
		"nil callback":   nil,
		"not advertised": func() bool { return false },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := &agentcontext.Resolver{}
			r.PackRowsAdvertised = advertised
			snap := r.Resolve([]agentcontext.ScanRoot{{Path: root}})

			require.NotEmpty(t, snap.Resources, "ordinary discovery must still run")
			for _, res := range snap.Resources {
				require.Empty(t, res.SourcePath,
					"no resource may carry the pack marker without the server capability")
				require.NotContains(t, res.Source, filepath.Join(root, "pstack"),
					"nothing from a pack may be discovered without the server capability")
			}
			findResource(t, snap.Resources, agentcontext.KindInstructionFile, filepath.Join(root, "AGENTS.md"))
		})
	}
}

func TestResolver_PluginPacksFollowCapabilityFlips(t *testing.T) {
	t.Parallel()

	// The advertised value is re-read on every resolve: losing the
	// capability (a deployment restart with the experiment off) stops
	// pack emission without an agent restart.
	root := t.TempDir()
	writePack(t, root, "plugin.json", `{"name": "pstack"}`, "skills", "agents")

	advertised := false
	r := &agentcontext.Resolver{}
	r.PackRowsAdvertised = func() bool { return advertised }

	packRows := func() int {
		snap := r.Resolve([]agentcontext.ScanRoot{{Path: root}})
		count := 0
		for _, res := range snap.Resources {
			if res.SourcePath != "" {
				count++
			}
		}
		return count
	}

	require.Zero(t, packRows(), "a pack must not be discovered before the capability")
	advertised = true
	require.Positive(t, packRows(), "a pack must be discovered once the capability is advertised")
	advertised = false
	require.Zero(t, packRows(), "losing the capability must stop pack discovery")
}

func TestResolver_PluginPacksAgentsNeedDefinitionsCapability(t *testing.T) {
	t.Parallel()

	// Pack agent rows are omitted entirely while the fork-agent-definitions
	// capability is unadvertised: chatd drops every subagent row then, so
	// the rows would be invisible inventory that still occupies the
	// index-based count cap ahead of live MCP and ACP resources (a
	// 500-agent pack fills DefaultMaxResources on its own). Omission
	// mirrors how pack skills stay omitted while the pack capability is
	// unadvertised; the pack's skills keep flowing in both cases here.
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "AGENTS.md"), "Root instructions.\n")
	writePack(t, root, "plugin.json", `{"name": "pstack"}`, "skills", "agents")

	for name, advertised := range map[string]func() bool{
		"nil callback":   nil,
		"not advertised": func() bool { return false },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := &agentcontext.Resolver{}
			r.PackRowsAdvertised = func() bool { return true }
			r.PackAgentDefinitionsAdvertised = advertised
			snap := r.Resolve([]agentcontext.ScanRoot{{Path: root}})

			for _, res := range snap.Resources {
				require.NotContains(t, res.Source, filepath.Join(root, "pstack", "agents"),
					"no pack agent row may be emitted while agent definitions are unadvertised")
			}
			findResource(t, snap.Resources, agentcontext.KindSkill,
				filepath.Join(root, "pstack", "skills", "review-pr"))
		})
	}
}

func TestResolver_PluginPacksOverTransportLimitOmitted(t *testing.T) {
	t.Parallel()

	// A pack whose manifest path exceeds coderd's source_path transport
	// limit must contribute nothing: the manifest path is the marker every
	// pack row carries, and coderd rejects a whole snapshot whose
	// source_path is over the limit, leaving pushWithRetry to retry that
	// rejection forever. The fixture needs a path longer than 1024 bytes,
	// which Linux filesystems allow and macOS does not, so hosts that
	// cannot create it skip.
	root := t.TempDir()
	parent := filepath.Join(root,
		strings.Repeat("d", 200),
		strings.Repeat("d", 200),
		strings.Repeat("d", 200),
		strings.Repeat("d", 200),
	)
	packDir := filepath.Join(parent, strings.Repeat("p", 200))
	if err := os.MkdirAll(packDir, 0o700); err != nil {
		t.Skipf("filesystem cannot create a manifest path over %d bytes: %v", 1024, err) // 1024 = maxSourcePathBytes
	}
	// Laid out directly (not via writePack, which nests a pstack level) so
	// the pack directory itself is the direct child of the scan root and
	// its manifest path exceeds the transport limit.
	mustWriteFile(t, filepath.Join(packDir, "plugin.json"), `{"name": "pstack"}`)
	mustWriteFile(t, filepath.Join(packDir, "skills", "review-pr", "SKILL.md"), skillName("review-pr"))
	mustWriteFile(t, filepath.Join(packDir, "agents", "comment-sicko.md"), packAgent("comment-sicko"))
	// 1024 = maxSourcePathBytes (unexported): a limit change must
	// resize this fixture deliberately.
	require.Greater(t, len(filepath.Join(packDir, "plugin.json")), 1024,
		"fixture must exceed the transport limit to exercise the guard")
	mustWriteFile(t, filepath.Join(parent, "AGENTS.md"), "Root instructions.\n")

	for name, scanRoot := range map[string]agentcontext.ScanRoot{
		"child pack": {Path: parent},
		"pack root":  {Path: packDir},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := &agentcontext.Resolver{}
			r.PackRowsAdvertised = func() bool { return true }
			r.PackAgentDefinitionsAdvertised = func() bool { return true }
			snap := r.Resolve([]agentcontext.ScanRoot{scanRoot})

			for _, res := range snap.Resources {
				require.Empty(t, res.SourcePath,
					"no row may carry a pack marker when the manifest path is over the transport limit")
				require.NotContains(t, res.Source, packDir,
					"nothing from an over-limit pack may be discovered")
			}
			if name == "child pack" {
				// Positive control: ordinary discovery still runs next
				// to the skipped pack.
				findResource(t, snap.Resources, agentcontext.KindInstructionFile,
					filepath.Join(parent, "AGENTS.md"))
			} else {
				require.Empty(t, snap.Resources,
					"a pack root over the transport limit is wholly pack content and must be skipped")
			}
		})
	}
}

func TestResolver_PluginPacksRootRequiresAdvertisedCapability(t *testing.T) {
	t.Parallel()

	// A scan root that is itself a pack is wholly pack content. Before
	// the server advertises the capability nothing may be discovered
	// from it, top-level files included: an older replica pins unmarked
	// rows as ordinary context, so the marker alone cannot protect them.
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "AGENTS.md"), "Pack instructions.\n")
	mustWriteFile(t, filepath.Join(root, "plugin.json"), `{"name": "pstack"}`)
	mustWriteFile(t, filepath.Join(root, "skills", "review-pr", "SKILL.md"), skillName("review-pr"))
	mustWriteFile(t, filepath.Join(root, "agents", "comment-sicko.md"), packAgent("comment-sicko"))

	for name, advertised := range map[string]func() bool{
		"nil callback":   nil,
		"not advertised": func() bool { return false },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := &agentcontext.Resolver{}
			r.PackRowsAdvertised = advertised
			snap := r.Resolve([]agentcontext.ScanRoot{{Path: root}})
			require.Empty(t, snap.Resources, "a pack root must yield nothing before the capability")
		})
	}

	// Once the capability is advertised the root-pack flow runs: the
	// top-level files are stamped with the manifest path and the pack
	// containers are discovered.
	r := &agentcontext.Resolver{}
	r.PackRowsAdvertised = func() bool { return true }
	r.PackAgentDefinitionsAdvertised = func() bool { return true }
	snap := r.Resolve([]agentcontext.ScanRoot{{Path: root}})
	instruction := findResource(t, snap.Resources, agentcontext.KindInstructionFile, filepath.Join(root, "AGENTS.md"))
	require.Equal(t, filepath.Join(root, "plugin.json"), instruction.SourcePath)
	findResource(t, snap.Resources, agentcontext.KindSkill, filepath.Join(root, "skills", "review-pr"))
}

func TestResolver_PluginPacksSameNameAcrossPacks(t *testing.T) {
	t.Parallel()

	// Packs on disk include ones disabled for a chat or removed from a
	// commit, so the agent must not pick a winner between packs: coderd
	// dedupes after its per-chat filtering. Ordinary resources still win.
	tests := []struct {
		name         string
		ordinary     string
		wantPacks    bool
		wantOrdinary bool
	}{
		{name: "PacksOnly", ordinary: "", wantPacks: true},
		{name: "OrdinaryRootBeforePacks", ordinary: "before", wantOrdinary: true},
		{name: "OrdinaryRootAfterPacks", ordinary: "after", wantOrdinary: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			packsRoot := t.TempDir()
			var manifests []string
			for _, dir := range []string{"alpha", "beta"} {
				packDir := filepath.Join(packsRoot, dir)
				manifest := filepath.Join(packDir, "plugin.json")
				mustWriteFile(t, manifest, fmt.Sprintf(`{"name": %q}`, dir))
				mustWriteFile(t, filepath.Join(packDir, "skills", "review", "SKILL.md"), skillName("review"))
				mustWriteFile(t, filepath.Join(packDir, "agents", "reviewer.md"), packAgent("reviewer"))
				manifests = append(manifests, manifest)
			}

			ordinaryRoot := t.TempDir()
			mustWriteSkill(t, filepath.Join(ordinaryRoot, "skills"), "review", "ordinary skill")
			mustWriteFile(t, filepath.Join(ordinaryRoot, "agents", "reviewer.md"),
				"---\nname: reviewer\ndescription: ordinary agent\n---\nOrdinary.")

			roots := []agentcontext.ScanRoot{{Path: packsRoot}}
			switch tt.ordinary {
			case "before":
				roots = []agentcontext.ScanRoot{{Path: ordinaryRoot}, {Path: packsRoot}}
			case "after":
				roots = []agentcontext.ScanRoot{{Path: packsRoot}, {Path: ordinaryRoot}}
			}

			r := &agentcontext.Resolver{}
			r.PackRowsAdvertised = func() bool { return true }
			r.PackAgentDefinitionsAdvertised = func() bool { return true }
			snap := r.Resolve(roots)

			var packSkills, packAgents, ordinarySkills, ordinaryAgents []string
			for _, res := range snap.Resources {
				if res.Status != agentcontext.StatusOK {
					continue
				}
				switch {
				case res.Kind == agentcontext.KindSkill && res.Name == "review" && res.SourcePath != "":
					packSkills = append(packSkills, res.SourcePath)
				case res.Kind == agentcontext.KindSubagent && res.Name == "reviewer" && res.SourcePath != "":
					packAgents = append(packAgents, res.SourcePath)
				case res.Kind == agentcontext.KindSkill && res.Name == "review":
					ordinarySkills = append(ordinarySkills, res.Source)
				case res.Kind == agentcontext.KindSubagent && res.Name == "reviewer":
					ordinaryAgents = append(ordinaryAgents, res.Source)
				}
			}

			if tt.wantPacks {
				require.ElementsMatch(t, manifests, packSkills, "every pack keeps its same-named skill")
				require.ElementsMatch(t, manifests, packAgents, "every pack keeps its same-named agent")
			} else {
				require.Empty(t, packSkills, "an ordinary skill shadows pack skills")
				require.Empty(t, packAgents, "an ordinary agent shadows pack agents")
			}
			if tt.wantOrdinary {
				require.Equal(t, []string{filepath.Join(ordinaryRoot, "skills", "review")}, ordinarySkills)
				require.Equal(t, []string{filepath.Join(ordinaryRoot, "agents", "reviewer.md")}, ordinaryAgents)
			} else {
				require.Empty(t, ordinarySkills)
				require.Empty(t, ordinaryAgents)
			}
		})
	}
}

func TestResolver_PluginPacksStaleAgentFileKept(t *testing.T) {
	t.Parallel()

	// An update that renames agents/a.md to agents/z.md leaves a.md on
	// disk with the same frontmatter name. Both rows must reach coderd,
	// which drops the one the commit no longer defines.
	root := t.TempDir()
	packDir := filepath.Join(root, "pstack")
	mustWriteFile(t, filepath.Join(packDir, "plugin.json"), `{"name": "pstack"}`)
	mustWriteFile(t, filepath.Join(packDir, "agents", "a.md"), packAgent("bar"))
	mustWriteFile(t, filepath.Join(packDir, "agents", "z.md"), packAgent("bar"))

	r := &agentcontext.Resolver{}
	r.PackRowsAdvertised = func() bool { return true }
	r.PackAgentDefinitionsAdvertised = func() bool { return true }
	snap := r.Resolve([]agentcontext.ScanRoot{{Path: root}})

	var sources []string
	for _, res := range snap.Resources {
		if res.Kind == agentcontext.KindSubagent && res.Status == agentcontext.StatusOK && res.Name == "bar" {
			sources = append(sources, res.Source)
		}
	}
	require.ElementsMatch(t, []string{
		filepath.Join(packDir, "agents", "a.md"),
		filepath.Join(packDir, "agents", "z.md"),
	}, sources)
}
