package agentcontext_test

// This file is fork code: it tests plugin pack discovery (PRD 6.7 R7.1,
// R7.4, R7.5). Pack resources are stamped with the pack's manifest path in
// SourcePath so coderd's fork-plugin gate can identify them.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestResolver_PluginPacksClaudeCacheLayout(t *testing.T) {
	t.Parallel()

	// Claude Code caches each plugin version three levels down and keeps
	// orphaned versions for a while: only the newest live version counts.
	cache := filepath.Join(t.TempDir(), ".claude", "plugins", "cache")
	plugin := filepath.Join(cache, "pstack-marketplace", "pstack")
	writeVersion := func(version string, modTime time.Time) string {
		dir := filepath.Join(plugin, version)
		mustWriteFile(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), `{"name": "pstack"}`)
		mustWriteFile(t, filepath.Join(dir, "skills", "review-pr", "SKILL.md"), skillName("review-pr"))
		require.NoError(t, os.Chtimes(dir, modTime, modTime))
		return dir
	}
	now := time.Now()
	writeVersion("0.15.34", now.Add(-2*time.Hour))
	current := writeVersion("0.15.36", now.Add(-time.Hour))
	orphaned := writeVersion("0.15.37", now)
	mustWriteFile(t, filepath.Join(orphaned, ".orphaned_at"), "1794430000000")
	require.NoError(t, os.Chtimes(orphaned, now, now))

	r := &agentcontext.Resolver{}
	r.PackRowsAdvertised = func() bool { return true }
	snap := r.Resolve([]agentcontext.ScanRoot{{Path: cache}})

	var sources []string
	for _, res := range snap.Resources {
		if res.Kind == agentcontext.KindSkill {
			sources = append(sources, res.Source)
		}
	}
	require.Equal(t, []string{filepath.Join(current, "skills", "review-pr")}, sources)
}

func TestResolver_PluginPacksManifestPathShapes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		manifest   string
		skillDirs  []string
		agentFiles []string
		wantSkills []string
		wantAgents []string
	}{
		{
			name:       "string declarations",
			manifest:   `{"name": "pstack", "skills": "./extra-skills", "agents": "./extra-agents"}`,
			skillDirs:  []string{"skills/default-skill", "extra-skills/extra-skill"},
			agentFiles: []string{"agents/default-agent.md", "extra-agents/extra-agent.md"},
			wantSkills: []string{"default-skill", "extra-skill"},
			wantAgents: []string{"extra-agent"},
		},
		{
			name:       "declared skill folder holding SKILL.md is one skill",
			manifest:   `{"name": "pstack", "skills": "./review-pr"}`,
			skillDirs:  []string{"skills/default-skill", "review-pr", "review-pr/nested-skill"},
			wantSkills: []string{"default-skill", "review-pr"},
		},
		{
			name:       "declared container and skill folder mix",
			manifest:   `{"name": "pstack", "skills": ["./extra-skills", "./solo-skill"]}`,
			skillDirs:  []string{"extra-skills/extra-skill", "solo-skill"},
			wantSkills: []string{"extra-skill", "solo-skill"},
		},
		{
			name:       "declaring the default container does not duplicate it",
			manifest:   `{"name": "pstack", "skills": ["./skills/", "./skills/default-skill"]}`,
			skillDirs:  []string{"skills/default-skill", "skills/other-skill"},
			wantSkills: []string{"default-skill", "other-skill"},
		},
		{
			name:       "array declarations",
			manifest:   `{"name": "pstack", "skills": ["./skills/", "./extra-skills"], "agents": ["./agents/", "./extra-agents"]}`,
			skillDirs:  []string{"skills/default-skill", "extra-skills/extra-skill"},
			agentFiles: []string{"agents/default-agent.md", "extra-agents/extra-agent.md"},
			wantSkills: []string{"default-skill", "extra-skill"},
			wantAgents: []string{"default-agent", "extra-agent"},
		},
		{
			name:       "agent file entries serve only the named files",
			manifest:   `{"name": "pstack", "agents": ["./agents/one.md", "./agents/two.md"]}`,
			agentFiles: []string{"agents/one.md", "agents/two.md", "agents/three.md"},
			wantAgents: []string{"one", "two"},
		},
		{
			name:       "single agent file string",
			manifest:   `{"name": "pstack", "agents": "./agents/one.md"}`,
			agentFiles: []string{"agents/one.md", "agents/three.md"},
			wantAgents: []string{"one"},
		},
		{
			name:       "agent file entry mixed with a directory",
			manifest:   `{"name": "pstack", "agents": ["./agents/", "./single/solo.md"]}`,
			agentFiles: []string{"agents/one.md", "single/solo.md", "single/other.md"},
			wantAgents: []string{"one", "solo"},
		},
		{
			name:       "agent file entry escaping the pack is ignored",
			manifest:   `{"name": "pstack", "agents": ["../outside.md", "./agents/one.md"]}`,
			agentFiles: []string{"agents/one.md"},
			wantAgents: []string{"one"},
		},
		{
			name:       "unsupported shapes fall back to defaults",
			manifest:   `{"name": "pstack", "skills": 5, "agents": {"path": "./x"}}`,
			skillDirs:  []string{"skills/default-skill"},
			agentFiles: []string{"agents/default-agent.md"},
			wantSkills: []string{"default-skill"},
			wantAgents: []string{"default-agent"},
		},
		{
			name:       "array with a non-string element falls back to defaults",
			manifest:   `{"name": "pstack", "skills": ["./extra-skills", 3], "agents": [null]}`,
			skillDirs:  []string{"skills/default-skill", "extra-skills/extra-skill"},
			agentFiles: []string{"agents/default-agent.md"},
			wantSkills: []string{"default-skill"},
			wantAgents: []string{"default-agent"},
		},
		{
			name:       "null and empty array keep defaults",
			manifest:   `{"name": "pstack", "skills": null, "agents": []}`,
			skillDirs:  []string{"skills/default-skill"},
			agentFiles: []string{"agents/default-agent.md"},
			wantSkills: []string{"default-skill"},
			wantAgents: []string{"default-agent"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			packDir := filepath.Join(root, "pstack")
			mustWriteFile(t, filepath.Join(packDir, ".claude-plugin", "plugin.json"), tc.manifest)
			for _, dir := range tc.skillDirs {
				mustWriteFile(t, filepath.Join(packDir, filepath.FromSlash(dir), "SKILL.md"), skillName(filepath.Base(dir)))
			}
			for _, file := range tc.agentFiles {
				mustWriteFile(t, filepath.Join(packDir, filepath.FromSlash(file)), packAgent(strings.TrimSuffix(filepath.Base(file), ".md")))
			}
			mustWriteFile(t, filepath.Join(root, "outside.md"), packAgent("outside"))

			r := &agentcontext.Resolver{}
			r.PackRowsAdvertised = func() bool { return true }
			r.PackAgentDefinitionsAdvertised = func() bool { return true }
			snap := r.Resolve([]agentcontext.ScanRoot{{Path: root}})

			var skills, agents []string
			for _, res := range snap.Resources {
				switch res.Kind {
				case agentcontext.KindSkill:
					skills = append(skills, res.Name)
				case agentcontext.KindSubagent:
					agents = append(agents, res.Name)
				}
			}
			require.ElementsMatch(t, tc.wantSkills, skills)
			require.ElementsMatch(t, tc.wantAgents, agents)
		})
	}
}

func TestResolver_PluginPacksClaudeCacheEnabledPlugins(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		settings string
		want     []string
	}{
		{
			name:     "explicitly disabled plugin is skipped",
			settings: `{"enabledPlugins": {"alpha@market": false, "beta@market": true}}`,
			want:     []string{"beta", "gamma"},
		},
		{
			name:     "disabled id must match plugin and marketplace",
			settings: `{"enabledPlugins": {"alpha@elsewhere": false, "beta": false}}`,
			want:     []string{"alpha", "beta", "gamma"},
		},
		{
			name:     "non-boolean value keeps the plugin",
			settings: `{"enabledPlugins": {"alpha@market": ["^1.0"]}}`,
			want:     []string{"alpha", "beta", "gamma"},
		},
		{
			name:     "settings past the manifest cap still apply",
			settings: `{"padding": "` + strings.Repeat("x", 128*1024) + `", "enabledPlugins": {"alpha@market": false}}`,
			want:     []string{"beta", "gamma"},
		},
		{
			name:     "settings past the settings cap keep every plugin",
			settings: `{"padding": "` + strings.Repeat("x", 2*1024*1024) + `", "enabledPlugins": {"alpha@market": false}}`,
			want:     []string{"alpha", "beta", "gamma"},
		},
		{
			name:     "missing settings keeps every plugin",
			settings: "",
			want:     []string{"alpha", "beta", "gamma"},
		},
		{
			name:     "invalid settings keeps every plugin",
			settings: `{"enabledPlugins": {"alpha@market": false`,
			want:     []string{"alpha", "beta", "gamma"},
		},
		{
			name:     "malformed enabledPlugins keeps every plugin",
			settings: `{"enabledPlugins": ["alpha@market"]}`,
			want:     []string{"alpha", "beta", "gamma"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			home := t.TempDir()
			cache := filepath.Join(home, ".claude", "plugins", "cache")
			for _, plugin := range []string{"alpha", "beta", "gamma"} {
				version := filepath.Join(cache, "market", plugin, "1.0.0")
				mustWriteFile(t, filepath.Join(version, ".claude-plugin", "plugin.json"), `{"name": "`+plugin+`"}`)
				mustWriteFile(t, filepath.Join(version, "skills", plugin, "SKILL.md"), skillName(plugin))
			}
			if tc.settings != "" {
				mustWriteFile(t, filepath.Join(home, ".claude", "settings.json"), tc.settings)
			}

			r := &agentcontext.Resolver{}
			r.PackRowsAdvertised = func() bool { return true }
			snap := r.Resolve([]agentcontext.ScanRoot{{Path: cache}})

			var skills []string
			for _, res := range snap.Resources {
				if res.Kind == agentcontext.KindSkill {
					skills = append(skills, res.Name)
				}
			}
			require.ElementsMatch(t, tc.want, skills)
		})
	}
}

// resolveSkillSources resolves a single scan root with pack rows advertised
// and returns the source of every skill it emitted.
func resolveSkillSources(root string) []string {
	r := &agentcontext.Resolver{}
	r.PackRowsAdvertised = func() bool { return true }
	r.PackAgentDefinitionsAdvertised = func() bool { return true }
	var sources []string
	for _, res := range r.Resolve([]agentcontext.ScanRoot{{Path: root}}).Resources {
		if res.Kind == agentcontext.KindSkill {
			sources = append(sources, res.Source)
		}
	}
	return sources
}

func TestResolver_PluginPacksClaudeSettingsSymlink(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	cache := filepath.Join(home, ".claude", "plugins", "cache")
	for _, plugin := range []string{"alpha", "beta"} {
		version := filepath.Join(cache, "market", plugin, "1.0.0")
		mustWriteFile(t, filepath.Join(version, ".claude-plugin", "plugin.json"), `{"name": "`+plugin+`"}`)
		mustWriteFile(t, filepath.Join(version, "skills", plugin, "SKILL.md"), skillName(plugin))
	}
	// Dotfile managers link settings.json to a file elsewhere.
	target := filepath.Join(home, "dotfiles", "settings.json")
	mustWriteFile(t, target, `{"enabledPlugins": {"alpha@market": false}}`)
	if err := os.Symlink(target, filepath.Join(home, ".claude", "settings.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	require.Equal(t, []string{filepath.Join(cache, "market", "beta", "1.0.0", "skills", "beta")}, resolveSkillSources(cache))
}

func TestResolver_PluginPacksClaudeCacheWithoutManifest(t *testing.T) {
	t.Parallel()

	// Claude Code loads a cached plugin without a manifest from the
	// standard layout, naming it after its directory.
	cache := filepath.Join(t.TempDir(), ".claude", "plugins", "cache")
	version := filepath.Join(cache, "market", "bare", "1.0.0")
	mustWriteFile(t, filepath.Join(version, "skills", "review-pr", "SKILL.md"), skillName("review-pr"))
	mustWriteFile(t, filepath.Join(version, "agents", "reviewer.md"), packAgent("reviewer"))

	r := &agentcontext.Resolver{}
	r.PackRowsAdvertised = func() bool { return true }
	r.PackAgentDefinitionsAdvertised = func() bool { return true }
	snap := r.Resolve([]agentcontext.ScanRoot{{Path: cache}})

	marker := filepath.Join(version, ".claude-plugin", "plugin.json")
	skill := findResource(t, snap.Resources, agentcontext.KindSkill, filepath.Join(version, "skills", "review-pr"))
	require.Equal(t, marker, skill.SourcePath)
	agent := findResource(t, snap.Resources, agentcontext.KindSubagent, filepath.Join(version, "agents", "reviewer.md"))
	require.Equal(t, marker, agent.SourcePath)

	// Outside the Claude cache a directory without a manifest is no pack.
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "bare", "skills", "review-pr", "SKILL.md"), skillName("review-pr"))
	require.Empty(t, resolveSkillSources(root))
}

func TestResolver_PluginPacksCursorSkillsReplaceDefault(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	packDir := filepath.Join(root, "pstack")
	mustWriteFile(t, filepath.Join(packDir, ".cursor-plugin", "plugin.json"), `{"name": "pstack", "skills": "./extra-skills"}`)
	mustWriteFile(t, filepath.Join(packDir, "skills", "default-skill", "SKILL.md"), skillName("default-skill"))
	mustWriteFile(t, filepath.Join(packDir, "extra-skills", "extra-skill", "SKILL.md"), skillName("extra-skill"))

	require.Equal(t, []string{filepath.Join(packDir, "extra-skills", "extra-skill")}, resolveSkillSources(root))
}

func TestResolver_PluginPacksClaudeInstallRecord(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		registry func(older, newer, outside string) string
		wantOld  bool
		wantNone bool
	}{
		{
			name: "user-scope record in an array",
			registry: func(older, _, _ string) string {
				return installRecords(map[string][]map[string]string{"pstack@market": {{"scope": "user", "installPath": older}}})
			},
			wantOld: true,
		},
		{
			name: "record without a scope",
			registry: func(older, _, _ string) string {
				return installRecords(map[string][]map[string]string{"pstack@market": {{"installPath": older}}})
			},
			wantOld: true,
		},
		{
			name: "single record object",
			registry: func(older, _, _ string) string {
				return installRecords(map[string]map[string]string{"pstack@market": {"scope": "user", "installPath": older}})
			},
			wantOld: true,
		},
		{
			name: "user record after a project record",
			registry: func(older, newer, _ string) string {
				return installRecords(map[string][]map[string]string{"pstack@market": {
					{"scope": "project", "installPath": newer},
					{"scope": "user", "installPath": older},
				}})
			},
			wantOld: true,
		},
		{
			name: "user record wins over a newer project record",
			registry: func(older, newer, _ string) string {
				return installRecords(map[string][]map[string]string{"pstack@market": {
					{"scope": "user", "installPath": older},
					{"scope": "project", "installPath": newer},
				}})
			},
			wantOld: true,
		},
		{
			name: "managed record applies everywhere",
			registry: func(older, _, _ string) string {
				return installRecords(map[string][]map[string]string{"pstack@market": {{"scope": "managed", "installPath": older}}})
			},
			wantOld: true,
		},
		{
			name: "project-only record skips the plugin",
			registry: func(older, _, _ string) string {
				return installRecords(map[string][]map[string]string{"pstack@market": {{"scope": "project", "installPath": older}}})
			},
			wantNone: true,
		},
		{
			name: "local-only record skips the plugin",
			registry: func(older, _, _ string) string {
				return installRecords(map[string][]map[string]string{"pstack@market": {{"scope": "local", "installPath": older}}})
			},
			wantNone: true,
		},
		{
			name: "user record with an invalid path falls back despite a project record",
			registry: func(older, _, outside string) string {
				return installRecords(map[string][]map[string]string{"pstack@market": {
					{"scope": "project", "installPath": older},
					{"scope": "user", "installPath": outside},
				}})
			},
		},
		{
			name: "record outside the cache falls back",
			registry: func(_, _, outside string) string {
				return installRecords(map[string][]map[string]string{"pstack@market": {{"scope": "user", "installPath": outside}}})
			},
		},
		{
			name: "record for another plugin falls back",
			registry: func(older, _, _ string) string {
				return installRecords(map[string][]map[string]string{"other@market": {{"scope": "user", "installPath": older}}})
			},
		},
		{
			name: "relative record falls back",
			registry: func(_, _, _ string) string {
				return installRecords(map[string][]map[string]string{"pstack@market": {{"scope": "user", "installPath": "market/pstack/1.0.0"}}})
			},
		},
		{
			name:     "malformed registry falls back",
			registry: func(_, _, _ string) string { return `{"plugins": ` },
		},
		{
			name:     "no registry falls back",
			registry: func(_, _, _ string) string { return "" },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			home := t.TempDir()
			cache := filepath.Join(home, ".claude", "plugins", "cache")
			plugin := filepath.Join(cache, "market", "pstack")
			now := time.Now()
			versions := map[string]time.Time{"1.0.0": now.Add(-time.Hour), "2.0.0": now}
			for version, modTime := range versions {
				dir := filepath.Join(plugin, version)
				mustWriteFile(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), `{"name": "pstack"}`)
				mustWriteFile(t, filepath.Join(dir, "skills", "review-pr", "SKILL.md"), skillName("review-pr"))
				require.NoError(t, os.Chtimes(dir, modTime, modTime))
			}
			older, newer := filepath.Join(plugin, "1.0.0"), filepath.Join(plugin, "2.0.0")
			if registry := tc.registry(older, newer, t.TempDir()); registry != "" {
				mustWriteFile(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), registry)
			}

			if tc.wantNone {
				require.Empty(t, resolveSkillSources(cache))
				return
			}
			want := newer
			if tc.wantOld {
				want = older
			}
			require.Equal(t, []string{filepath.Join(want, "skills", "review-pr")}, resolveSkillSources(cache))
		})
	}
}

func TestResolver_PluginPacksClaudeInstallRecordIgnoresOrphaned(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	cache := filepath.Join(home, ".claude", "plugins", "cache")
	plugin := filepath.Join(cache, "market", "pstack")
	now := time.Now()
	for version, modTime := range map[string]time.Time{"1.0.0": now, "2.0.0": now.Add(-time.Hour)} {
		dir := filepath.Join(plugin, version)
		mustWriteFile(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), `{"name": "pstack"}`)
		mustWriteFile(t, filepath.Join(dir, "skills", "review-pr", "SKILL.md"), skillName("review-pr"))
		require.NoError(t, os.Chtimes(dir, modTime, modTime))
	}
	// The record names a version Claude Code has since orphaned.
	mustWriteFile(t, filepath.Join(plugin, "1.0.0", ".orphaned_at"), "1794430000000")
	mustWriteFile(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"),
		installRecords(map[string][]map[string]string{"pstack@market": {{"scope": "user", "installPath": filepath.Join(plugin, "1.0.0")}}}))

	require.Equal(t, []string{filepath.Join(plugin, "2.0.0", "skills", "review-pr")}, resolveSkillSources(cache))
}

// installRecords renders an installed_plugins.json holding plugins.
func installRecords(plugins any) string {
	data, _ := json.Marshal(map[string]any{"version": 2, "plugins": plugins})
	return string(data)
}

// writeNamedPack lays out a pack directory dir/name that defines the review
// skill and the reviewer agent, and returns its manifest path.
func writeNamedPack(t *testing.T, dir, name string) string {
	t.Helper()
	packDir := filepath.Join(dir, name)
	manifest := filepath.Join(packDir, "plugin.json")
	mustWriteFile(t, manifest, fmt.Sprintf(`{"name": %q}`, name))
	mustWriteFile(t, filepath.Join(packDir, "skills", "review", "SKILL.md"), skillName("review"))
	mustWriteFile(t, filepath.Join(packDir, "agents", "reviewer.md"), packAgent("reviewer"))
	return manifest
}

func TestResolver_PluginPacksSameNameAcrossPacks(t *testing.T) {
	t.Parallel()

	// Packs that coderd installs under ~/.coder/plugins include ones disabled
	// for a chat or removed from a commit, so the agent keeps same-named
	// resources of different managed packs and coderd picks the live one after
	// its per-chat filtering. Any other source is always live, so it shadows
	// managed packs whatever the scan-root order.
	tests := []struct {
		name  string
		roots []string
		want  []string
	}{
		{name: "ManagedPacksOnly", roots: []string{"managed"}, want: []string{"managed:alpha", "managed:beta"}},
		{name: "OrdinaryRootBeforeManaged", roots: []string{"ordinary", "managed"}, want: []string{"ordinary"}},
		{name: "OrdinaryRootAfterManaged", roots: []string{"managed", "ordinary"}, want: []string{"ordinary"}},
		{name: "UnmanagedPackBeforeManaged", roots: []string{"unmanaged", "managed"}, want: []string{"unmanaged"}},
		{name: "UnmanagedPackAfterManaged", roots: []string{"managed", "unmanaged"}, want: []string{"unmanaged"}},
		{name: "UnmanagedPacksFirstWins", roots: []string{"unmanaged", "unmanaged2"}, want: []string{"unmanaged"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			managedRoot := filepath.Join(t.TempDir(), ".coder", "plugins")
			writeNamedPack(t, managedRoot, "alpha")
			writeNamedPack(t, managedRoot, "beta")
			unmanagedRoot := t.TempDir()
			writeNamedPack(t, unmanagedRoot, "gamma")
			unmanagedRoot2 := t.TempDir()
			writeNamedPack(t, unmanagedRoot2, "delta")
			ordinaryRoot := t.TempDir()
			mustWriteSkill(t, filepath.Join(ordinaryRoot, "skills"), "review", "ordinary skill")
			mustWriteFile(t, filepath.Join(ordinaryRoot, "agents", "reviewer.md"),
				"---\nname: reviewer\ndescription: ordinary agent\n---\nOrdinary.")

			byName := map[string]string{
				"managed": managedRoot, "unmanaged": unmanagedRoot, "unmanaged2": unmanagedRoot2, "ordinary": ordinaryRoot,
			}
			var roots []agentcontext.ScanRoot
			for _, name := range tt.roots {
				roots = append(roots, agentcontext.ScanRoot{Path: byName[name]})
			}

			r := &agentcontext.Resolver{}
			r.PackRowsAdvertised = func() bool { return true }
			r.PackAgentDefinitionsAdvertised = func() bool { return true }
			snap := r.Resolve(roots)

			origin := func(res agentcontext.Resource) string {
				switch {
				case res.SourcePath == "":
					return "ordinary"
				case strings.HasPrefix(res.SourcePath, managedRoot):
					return "managed:" + filepath.Base(filepath.Dir(res.SourcePath))
				case strings.HasPrefix(res.SourcePath, unmanagedRoot):
					return "unmanaged"
				default:
					return "unmanaged2"
				}
			}
			var skills, agents []string
			for _, res := range snap.Resources {
				switch {
				case res.Status != agentcontext.StatusOK:
				case res.Kind == agentcontext.KindSkill && res.Name == "review":
					skills = append(skills, origin(res))
				case res.Kind == agentcontext.KindSubagent && res.Name == "reviewer":
					agents = append(agents, origin(res))
				}
			}
			require.ElementsMatch(t, tt.want, skills)
			require.ElementsMatch(t, tt.want, agents)
		})
	}
}

func TestResolver_PluginPacksStaleAgentFileKept(t *testing.T) {
	t.Parallel()

	// An update that renames agents/a.md to agents/z.md leaves a.md on
	// disk with the same frontmatter name. Both rows must reach coderd,
	// which drops the one the commit no longer defines.
	root := filepath.Join(t.TempDir(), ".coder", "plugins")
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

func TestResolver_PluginPackDuplicatesUseSpareCapacity(t *testing.T) {
	t.Parallel()

	// Two managed packs define the same three skills. The first pack's rows
	// are the ones that count against the resource cap; the second pack's are
	// copies for a chat that enables only it, so they never displace
	// anything and a push stays within twice the cap.
	tests := []struct {
		name         string
		maxResources int
		wantTotal    int
		wantOK       int
	}{
		{name: "RoomForCopies", maxResources: 4, wantTotal: 6, wantOK: 6},
		{name: "SomeCopiesOmitted", maxResources: 2, wantTotal: 4, wantOK: 3},
		{name: "NoCopiesWhenPrimariesExceedTwiceTheCap", maxResources: 1, wantTotal: 3, wantOK: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := filepath.Join(t.TempDir(), ".coder", "plugins")
			for _, pack := range []string{"alpha", "beta"} {
				mustWriteFile(t, filepath.Join(root, pack, "plugin.json"), fmt.Sprintf(`{"name": %q}`, pack))
				for _, skill := range []string{"one", "two", "three"} {
					mustWriteFile(t, filepath.Join(root, pack, "skills", skill, "SKILL.md"), skillName(skill))
				}
			}

			r := &agentcontext.Resolver{MaxResources: tt.maxResources}
			r.PackRowsAdvertised = func() bool { return true }
			snap := r.Resolve([]agentcontext.ScanRoot{{Path: root}})

			require.Len(t, snap.Resources, tt.wantTotal)
			var ok int
			for _, res := range snap.Resources {
				if res.Status == agentcontext.StatusOK {
					ok++
				}
			}
			require.Equal(t, tt.wantOK, ok)
		})
	}
}
