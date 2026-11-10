package chatd

// This file is fork code: it tests the fork-plugins gate on pack-contributed
// context resources.

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/codersdk"
)

func packRow(source, sourcePath string) database.ChatContextResource {
	return database.ChatContextResource{
		Source:     source,
		SourcePath: sourcePath,
		Status:     database.WorkspaceAgentContextResourceStatusOk,
	}
}

func TestIsPackContribution(t *testing.T) {
	t.Parallel()
	require.True(t, isPackContribution("/home/u/.coder/plugins/pstack/.claude-plugin/plugin.json"))
	require.True(t, isPackContribution("/home/u/.coder/plugins/pstack/.cursor-plugin/plugin.json"))
	require.True(t, isPackContribution("/home/u/.coder/plugins/pstack/plugin.json"))
	// The source path is stamped by the workspace agent, which may run on
	// Windows while coderd runs on Linux.
	require.True(t, isPackContribution(`C:\Users\u\.coder\plugins\pstack\plugin.json`))
	require.True(t, isPackContribution(`C:\Users\u\.coder\plugins\pstack\.cursor-plugin\plugin.json`))
	require.False(t, isPackContribution(""))
	require.False(t, isPackContribution("/home/u/project"))
	require.False(t, isPackContribution("/home/u/.coder/skills/review-pr"))
}

func TestFilterPackResources(t *testing.T) {
	t.Parallel()

	resources := []database.ChatContextResource{
		packRow("/home/u/.coder/plugins/pstack/skills/review-pr",
			"/home/u/.coder/plugins/pstack/plugin.json"),
		packRow("/home/u/.coder/skills/review-pr", ""),
		packRow("/home/u/.coder/plugins/pstack/agents/comment-sicko.md",
			"/home/u/.coder/plugins/pstack/.cursor-plugin/plugin.json"),
	}

	off := (&Server{experiments: codersdk.Experiments{}}).filterPackResources(append([]database.ChatContextResource{}, resources...))
	require.Len(t, off, 1, "with fork-plugins off only the user skill survives")
	require.Equal(t, "/home/u/.coder/skills/review-pr", off[0].Source)

	on := (&Server{experiments: codersdk.Experiments{codersdk.ExperimentForkPlugins}}).filterPackResources(append([]database.ChatContextResource{}, resources...))
	require.Len(t, on, 3, "with fork-plugins on the pack skills and agents survive")
}
