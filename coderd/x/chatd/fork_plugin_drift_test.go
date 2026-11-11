package chatd

// This file is fork code: it tests the fork-plugin-packs drift hash derivation.

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/agent/agentcontext"
	"github.com/coder/coder/v2/coderd/agentapi"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/codersdk"
)

// The Server must keep satisfying the optional drift-hash capability the
// push handler type-asserts on; a rename here silently reverts chatd to
// pinning chats against the agent's pushed aggregate.
var _ agentapi.ContextDriftHasher = (*Server)(nil)

func driftRow(source, sourcePath string, kind database.WorkspaceAgentContextBodyKind, contentHash []byte) database.UpsertWorkspaceAgentContextResourceParams {
	return database.UpsertWorkspaceAgentContextResourceParams{
		Source:      source,
		SourcePath:  sourcePath,
		BodyKind:    kind,
		ContentHash: contentHash,
		Status:      database.WorkspaceAgentContextResourceStatusOk,
	}
}

func TestDriftHash(t *testing.T) {
	t.Parallel()

	rows := []database.UpsertWorkspaceAgentContextResourceParams{
		driftRow("/home/u/.coder/skills/review-pr", "",
			database.WorkspaceAgentContextBodyKindSkill, []byte{0x01}),
		driftRow("/home/u/.coder/plugins/pstack/skills/review-pr", "/home/u/.coder/plugins/pstack/plugin.json",
			database.WorkspaceAgentContextBodyKindSkill, []byte{0x02}),
		driftRow("/home/u/.coder/plugins/pstack/agents/comment-sicko.md", "/home/u/.coder/plugins/pstack/plugin.json",
			database.WorkspaceAgentContextBodyKindSubagent, []byte{0x03}),
		driftRow("codex", "/home/u/.mcp.json",
			database.WorkspaceAgentContextBodyKindMcpServer, []byte{0x04}),
	}

	t.Run("OffReproducesAgentExclusion", func(t *testing.T) {
		t.Parallel()

		got := (&Server{experiments: codersdk.Experiments{}}).DriftHash(rows, nil)
		want := agentcontext.ComputeAggregateHash([]agentcontext.Resource{
			{
				ID:          "skill:/home/u/.coder/skills/review-pr",
				Kind:        agentcontext.KindSkill,
				Source:      "/home/u/.coder/skills/review-pr",
				ContentHash: [32]byte{0x01},
				Status:      agentcontext.StatusOK,
			},
		})
		require.Equal(t, want[:], got, "with fork-plugin-packs off the derived hash must equal the agent's pushed aggregate over the same set")
	})

	t.Run("OnKeepsPackRowsDriftRelevant", func(t *testing.T) {
		t.Parallel()

		got := (&Server{experiments: codersdk.Experiments{
			codersdk.ExperimentForkPluginPacks, codersdk.ExperimentForkAgentDefinitions,
		}}).DriftHash(rows, nil)
		want := agentcontext.ComputeAggregateHash([]agentcontext.Resource{
			{
				ID:          "skill:/home/u/.coder/skills/review-pr",
				Kind:        agentcontext.KindSkill,
				Source:      "/home/u/.coder/skills/review-pr",
				ContentHash: [32]byte{0x01},
				Status:      agentcontext.StatusOK,
			},
			{
				ID:          "skill:/home/u/.coder/plugins/pstack/skills/review-pr",
				Kind:        agentcontext.KindSkill,
				Source:      "/home/u/.coder/plugins/pstack/skills/review-pr",
				ContentHash: [32]byte{0x02},
				Status:      agentcontext.StatusOK,
			},
			{
				ID:          "subagent:/home/u/.coder/plugins/pstack/agents/comment-sicko.md",
				Kind:        agentcontext.KindSubagent,
				Source:      "/home/u/.coder/plugins/pstack/agents/comment-sicko.md",
				ContentHash: [32]byte{0x03},
				Status:      agentcontext.StatusOK,
			},
		})
		require.Equal(t, want[:], got, "with both fork experiments on pack rows join the drift hash while MCP rows stay live-synced")
	})

	t.Run("OnSkipsPackAgentRowsWhenDefinitionsOff", func(t *testing.T) {
		t.Parallel()

		// fork-plugin-packs alone still surfaces pack skills, but pack
		// agents are invisible (custom agent types are disabled), so they
		// must not contribute to the hash chats pin against.
		got := (&Server{experiments: codersdk.Experiments{codersdk.ExperimentForkPluginPacks}}).DriftHash(rows, nil)
		want := agentcontext.ComputeAggregateHash([]agentcontext.Resource{
			{
				ID:          "skill:/home/u/.coder/skills/review-pr",
				Kind:        agentcontext.KindSkill,
				Source:      "/home/u/.coder/skills/review-pr",
				ContentHash: [32]byte{0x01},
				Status:      agentcontext.StatusOK,
			},
			{
				ID:          "skill:/home/u/.coder/plugins/pstack/skills/review-pr",
				Kind:        agentcontext.KindSkill,
				Source:      "/home/u/.coder/plugins/pstack/skills/review-pr",
				ContentHash: [32]byte{0x02},
				Status:      agentcontext.StatusOK,
			},
		})
		require.Equal(t, want[:], got, "with fork-agent-definitions off a pack agent edit must not drift chats")
	})

	t.Run("PackAgentEditDriftsOnlyWhenDefinitionsOn", func(t *testing.T) {
		t.Parallel()

		both := &Server{experiments: codersdk.Experiments{
			codersdk.ExperimentForkPluginPacks, codersdk.ExperimentForkAgentDefinitions,
		}}
		pluginsOnly := &Server{experiments: codersdk.Experiments{codersdk.ExperimentForkPluginPacks}}

		edited := append([]database.UpsertWorkspaceAgentContextResourceParams{}, rows...)
		edited[2] = driftRow("/home/u/.coder/plugins/pstack/agents/comment-sicko.md", "/home/u/.coder/plugins/pstack/plugin.json",
			database.WorkspaceAgentContextBodyKindSubagent, []byte{0x33})

		require.NotEqual(t, both.DriftHash(rows, nil), both.DriftHash(edited, nil),
			"editing a pack agent must mark chats dirty while fork-agent-definitions is on")
		require.Equal(t, pluginsOnly.DriftHash(rows, nil), pluginsOnly.DriftHash(edited, nil),
			"with fork-agent-definitions off a pack agent edit is invisible to chats and must not drift")
	})

	t.Run("PackEditDriftsOnlyWhenOn", func(t *testing.T) {
		t.Parallel()

		on := &Server{experiments: codersdk.Experiments{codersdk.ExperimentForkPluginPacks}}
		off := &Server{experiments: codersdk.Experiments{}}

		edited := append([]database.UpsertWorkspaceAgentContextResourceParams{}, rows...)
		edited[1] = driftRow("/home/u/.coder/plugins/pstack/skills/review-pr", "/home/u/.coder/plugins/pstack/plugin.json",
			database.WorkspaceAgentContextBodyKindSkill, []byte{0x22})

		require.NotEqual(t, on.DriftHash(rows, nil), on.DriftHash(edited, nil),
			"editing a pack skill must mark chats dirty while fork-plugin-packs is on")
		require.Equal(t, off.DriftHash(rows, nil), off.DriftHash(edited, nil),
			"with fork-plugin-packs off a pack edit is invisible to chats and must not drift")
	})
}
