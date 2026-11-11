package chatd

// This file is fork code: it tests that same-named pack skills and agents are
// deduplicated after the per-chat pack gate, not by the workspace agent.

import (
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	agentproto "github.com/coder/coder/v2/agent/proto"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
	"github.com/coder/coder/v2/codersdk"
)

const testPackPluginsRoot = "/home/u/.coder/plugins/"

func packSkillRow(t *testing.T, pack, name string) database.ChatContextResource {
	t.Helper()
	row := skillResource(t, testPackPluginsRoot+pack+"/skills/"+name, name, "A pack skill.", database.WorkspaceAgentContextResourceStatusOk)
	row.SourcePath = testPackPluginsRoot + pack + "/.claude-plugin/plugin.json"
	return row
}

func packAgentRow(t *testing.T, pack, file, name string) database.ChatContextResource {
	t.Helper()
	return database.ChatContextResource{
		Source:     testPackPluginsRoot + pack + "/agents/" + file,
		SourcePath: testPackPluginsRoot + pack + "/.claude-plugin/plugin.json",
		BodyKind:   database.WorkspaceAgentContextBodyKindSubagent,
		Body: mustMarshalContextBody(t, &agentproto.SubagentBody{
			Definition: []byte(agentDefinitionMarkdown("name: "+name+"\ndescription: Test agent.", "Body.")),
			Name:       name,
		}),
		Status: database.WorkspaceAgentContextResourceStatusOk,
	}
}

func contextRowSources(rows []database.ChatContextResource) []string {
	var out []string
	for _, row := range rows {
		out = append(out, row.Source)
	}
	return out
}

func TestDedupePackResourceNames(t *testing.T) {
	t.Parallel()

	ordinarySkill := skillResource(t, "/home/u/.coder/skills/review", "review", "Ordinary.", database.WorkspaceAgentContextResourceStatusOk)
	invalidPackSkill := packSkillRow(t, "alpha", "review")
	invalidPackSkill.Status = database.WorkspaceAgentContextResourceStatusInvalid
	unnamedPackSkill := packSkillRow(t, "alpha", "")
	unnamedPackSkill.Source = testPackPluginsRoot + "alpha/skills/unnamed"
	undecodablePackSkill := packSkillRow(t, "beta", "review")
	undecodablePackSkill.Source = testPackPluginsRoot + "beta/skills/broken"
	undecodablePackSkill.Body = []byte(`{"name": 7}`)

	tests := []struct {
		name string
		rows []database.ChatContextResource
		want []string
	}{
		{
			name: "FirstPackWins",
			rows: []database.ChatContextResource{
				packSkillRow(t, "alpha", "review"), packSkillRow(t, "beta", "review"),
				packAgentRow(t, "alpha", "reviewer.md", "reviewer"), packAgentRow(t, "beta", "reviewer.md", "reviewer"),
			},
			want: []string{
				testPackPluginsRoot + "alpha/skills/review",
				testPackPluginsRoot + "alpha/agents/reviewer.md",
			},
		},
		{
			name: "OrdinaryBeatsPack",
			rows: []database.ChatContextResource{
				packSkillRow(t, "alpha", "review"), ordinarySkill,
			},
			want: []string{ordinarySkill.Source},
		},
		{
			name: "SkillAndAgentNamesAreIndependent",
			rows: []database.ChatContextResource{
				packSkillRow(t, "alpha", "review"), packAgentRow(t, "alpha", "review.md", "review"),
			},
			want: []string{testPackPluginsRoot + "alpha/skills/review", testPackPluginsRoot + "alpha/agents/review.md"},
		},
		{
			name: "RowsWithoutAUsableNameAreKept",
			rows: []database.ChatContextResource{
				invalidPackSkill, packSkillRow(t, "beta", "review"), unnamedPackSkill, undecodablePackSkill,
			},
			want: []string{
				invalidPackSkill.Source, testPackPluginsRoot + "beta/skills/review",
				unnamedPackSkill.Source, undecodablePackSkill.Source,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := dedupePackResourceNames(slices.Clone(tt.rows))
			require.ElementsMatch(t, tt.want, contextRowSources(got))
		})
	}
}

func TestFilterPackResourcesDedupesEnabledPacks(t *testing.T) {
	t.Parallel()

	db, ps := dbtestutil.NewDB(t)
	server := newInternalTestServer(t, db, ps, chatprovider.ProviderAPIKeys{},
		withInternalTestServerExperiments(codersdk.Experiments{codersdk.ExperimentForkPluginPacks}))
	fdb, err := forkdb.From(db)
	require.NoError(t, err)
	ctx := chatdTestContext(t)
	user, org, model := seedInternalChatDeps(t, db)

	// Packs alpha and beta both define a review skill and a reviewer agent.
	// Pack pstack was updated so that agents/a.md became agents/z.md under the
	// same agent name; only z.md is in its current commit.
	insertPack := func(name string, paths ...string) uuid.UUID {
		now := dbtime.Now()
		pack, err := fdb.InsertPluginPack(ctx, forkdb.InsertPluginPackParams{
			ID: uuid.New(), OrganizationID: org.ID, Name: name, SourceUrl: "https://example.com/packs.git",
			Ref: "main", CommitSha: "0123456789abcdef0123456789abcdef01234567", CreatedBy: user.ID,
			CreatedAt: now, UpdatedAt: now,
		})
		require.NoError(t, err)
		contents := make([][]byte, len(paths))
		hashes := make([]string, len(paths))
		for i := range paths {
			contents[i] = []byte("content")
			hashes[i] = "hash"
		}
		require.NoError(t, fdb.InsertPluginPackResources(ctx, forkdb.InsertPluginPackResourcesParams{
			PackID: pack.ID, Paths: paths, Contents: contents, ContentHashes: hashes,
		}))
		return pack.ID
	}
	shared := []string{".claude-plugin/plugin.json", "skills/review/SKILL.md", "agents/reviewer.md"}
	alpha := insertPack("alpha", shared...)
	beta := insertPack("beta", shared...)
	pstack := insertPack("pstack", ".claude-plugin/plugin.json", "agents/z.md")

	newChat := func(packIDs ...uuid.UUID) database.Chat {
		chat := dbgen.Chat(t, db, database.Chat{OrganizationID: org.ID, OwnerID: user.ID, LastModelConfigID: model.ID})
		for _, packID := range packIDs {
			_, err := fdb.InsertChatPluginPack(ctx, forkdb.InsertChatPluginPackParams{ChatID: chat.ID, PackID: packID})
			require.NoError(t, err)
		}
		return chat
	}

	// The agent pushes the rows of every pack on disk, in source order.
	rows := []database.ChatContextResource{
		packSkillRow(t, "alpha", "review"),
		packAgentRow(t, "alpha", "reviewer.md", "reviewer"),
		packSkillRow(t, "beta", "review"),
		packAgentRow(t, "beta", "reviewer.md", "reviewer"),
		packAgentRow(t, "pstack", "a.md", "bar"),
		packAgentRow(t, "pstack", "z.md", "bar"),
	}

	tests := []struct {
		name string
		chat database.Chat
		want []string
	}{
		{
			name: "AlphaEnabledOnly",
			chat: newChat(alpha),
			want: []string{testPackPluginsRoot + "alpha/skills/review", testPackPluginsRoot + "alpha/agents/reviewer.md"},
		},
		{
			name: "DisabledPackDoesNotShadowEnabledPack",
			chat: newChat(beta),
			want: []string{testPackPluginsRoot + "beta/skills/review", testPackPluginsRoot + "beta/agents/reviewer.md"},
		},
		{
			name: "BothEnabledFirstPackWins",
			chat: newChat(alpha, beta),
			want: []string{testPackPluginsRoot + "alpha/skills/review", testPackPluginsRoot + "alpha/agents/reviewer.md"},
		},
		{
			name: "RenamedAgentFileStaysAvailable",
			chat: newChat(pstack),
			want: []string{testPackPluginsRoot + "pstack/agents/z.md"},
		},
		{
			name: "NoPacksEnabled",
			chat: newChat(),
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := server.filterPackResources(ctx, tt.chat, slices.Clone(rows))
			require.ElementsMatch(t, tt.want, contextRowSources(got))

			// Every consumer reads the filtered rows: the prompt's skill
			// list, and the agent definitions behind spawn_agent.
			_, skills, malformed := contextResourcesToPrompt(got, "linux", "/home/u")
			require.Zero(t, malformed)
			names := map[string]int{}
			for _, skill := range skills {
				names[skill.Name]++
			}
			for _, def := range parseAgentDefinitionResources(got) {
				names[def.Name]++
			}
			for name, count := range names {
				require.Equal(t, 1, count, "%s must be listed once", name)
			}
		})
	}
}
