package chatd

// This file is fork code: it tests the fork-plugin-packs gate on pack-contributed
// context resources.

import (
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"

	agentproto "github.com/coder/coder/v2/agent/proto"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
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

func TestManagedPackName(t *testing.T) {
	t.Parallel()
	name, ok := managedPackName("/home/u/.coder/plugins/pstack/.claude-plugin/plugin.json")
	require.True(t, ok)
	require.Equal(t, "pstack", name)
	name, ok = managedPackName(`C:\Users\u\.coder\plugins\pstack\plugin.json`)
	require.True(t, ok)
	require.Equal(t, "pstack", name)
	_, ok = managedPackName("/home/u/.claude/plugins/cache/pstack/plugin.json")
	require.False(t, ok, "a pack outside the managed root is not an installed pack")
	_, ok = managedPackName("/home/u/.coder/plugins/plugin.json")
	require.False(t, ok, "the managed root itself is not a pack directory")
}

func TestFilterPackResources(t *testing.T) {
	t.Parallel()

	managed := func(name string) database.ChatContextResource {
		return packRow("/home/u/.coder/plugins/"+name+"/skills/review-pr",
			"/home/u/.coder/plugins/"+name+"/.claude-plugin/plugin.json")
	}
	resources := []database.ChatContextResource{
		packRow("/home/u/.coder/skills/review-pr", ""),
		packRow("/home/u/.claude/plugins/cache/own/skills/review-pr",
			"/home/u/.claude/plugins/cache/own/plugin.json"),
		managed("project-pack"),
		managed("chat-pack"),
		managed("disabled-pack"),
	}
	sources := func(rows []database.ChatContextResource) []string {
		var out []string
		for _, row := range rows {
			out = append(out, row.Source)
		}
		return out
	}

	t.Run("ExperimentOff", func(t *testing.T) {
		t.Parallel()
		off := (&Server{experiments: codersdk.Experiments{}}).filterPackResources(t.Context(), database.Chat{}, slices.Clone(resources))
		require.Equal(t, []string{"/home/u/.coder/skills/review-pr"}, sources(off),
			"with fork-plugin-packs off only the user skill survives")
	})

	t.Run("Enablement", func(t *testing.T) {
		t.Parallel()
		db, ps := dbtestutil.NewDB(t)
		server := newInternalTestServer(t, db, ps, chatprovider.ProviderAPIKeys{},
			withInternalTestServerExperiments(codersdk.Experiments{codersdk.ExperimentForkPluginPacks}))
		fdb, err := forkdb.From(db)
		require.NoError(t, err)
		ctx := chatdTestContext(t)
		user, org, model := seedInternalChatDeps(t, db)

		packID := func(name string) uuid.UUID {
			now := dbtime.Now()
			pack, err := fdb.InsertPluginPack(ctx, forkdb.InsertPluginPackParams{
				ID: uuid.New(), OrganizationID: org.ID, Name: name, SourceUrl: "https://example.com/packs.git",
				Ref: "main", CommitSha: "0123456789abcdef0123456789abcdef01234567", CreatedBy: user.ID,
				CreatedAt: now, UpdatedAt: now,
			})
			require.NoError(t, err)
			return pack.ID
		}
		projectPack, chatPack := packID("project-pack"), packID("chat-pack")
		_ = packID("disabled-pack")

		project := dbgen.ChatProject(t, db, database.ChatProject{OrganizationID: org.ID, OwnerID: user.ID})
		_, err = fdb.InsertPluginPackProject(ctx, forkdb.InsertPluginPackProjectParams{ProjectID: project.ID, PackID: projectPack})
		require.NoError(t, err)
		root := dbgen.Chat(t, db, database.Chat{
			OrganizationID: org.ID, OwnerID: user.ID, LastModelConfigID: model.ID,
			ProjectID: uuid.NullUUID{UUID: project.ID, Valid: true},
		})
		_, err = fdb.InsertChatPluginPack(ctx, forkdb.InsertChatPluginPackParams{ChatID: root.ID, PackID: chatPack})
		require.NoError(t, err)
		child := dbgen.Chat(t, db, database.Chat{
			OrganizationID: org.ID, OwnerID: user.ID, LastModelConfigID: model.ID,
			ParentChatID: uuid.NullUUID{UUID: root.ID, Valid: true},
			RootChatID:   uuid.NullUUID{UUID: root.ID, Valid: true},
		})
		plain := dbgen.Chat(t, db, database.Chat{OrganizationID: org.ID, OwnerID: user.ID, LastModelConfigID: model.ID})

		unmanaged := []string{"/home/u/.coder/skills/review-pr", "/home/u/.claude/plugins/cache/own/skills/review-pr"}
		enabled := append(slices.Clone(unmanaged),
			"/home/u/.coder/plugins/project-pack/skills/review-pr",
			"/home/u/.coder/plugins/chat-pack/skills/review-pr")
		require.Equal(t, enabled, sources(server.filterPackResources(ctx, root, slices.Clone(resources))),
			"the root chat sees its project's packs and its own")
		require.Equal(t, enabled, sources(server.filterPackResources(ctx, child, slices.Clone(resources))),
			"a subagent sees the packs of its root chat")
		require.Equal(t, unmanaged, sources(server.filterPackResources(ctx, plain, slices.Clone(resources))),
			"a chat with no enabled packs drops every installed pack")
		// The agent type catalog filters for a chat that does not exist yet.
		draft := database.Chat{ID: uuid.New(), OwnerID: user.ID, OrganizationID: org.ID, ProjectID: root.ProjectID}
		require.Equal(t, append(slices.Clone(unmanaged), "/home/u/.coder/plugins/project-pack/skills/review-pr"),
			sources(server.filterPackResources(ctx, draft, slices.Clone(resources))),
			"a new chat sees its project's packs")
	})
}

func TestAgentRowDefinitionsGatesPackAgents(t *testing.T) {
	t.Parallel()

	row := func(name, sourcePath string) database.WorkspaceAgentContextResource {
		body, err := protojson.Marshal(&agentproto.SubagentBody{
			Definition: []byte(agentDefinitionMarkdown("name: "+name+"\ndescription: Test agent.", "Body.")),
			Name:       name,
		})
		require.NoError(t, err)
		return database.WorkspaceAgentContextResource{
			BodyKind:   database.WorkspaceAgentContextBodyKindSubagent,
			Body:       body,
			Status:     database.WorkspaceAgentContextResourceStatusOk,
			SourcePath: sourcePath,
		}
	}
	rows := []database.WorkspaceAgentContextResource{
		row("reviewer", ""),
		row("comment-sicko", "/home/u/.claude/plugins/cache/pstack/plugin.json"),
	}

	names := func(experiments codersdk.Experiments) []string {
		var out []string
		for _, def := range (&Server{experiments: experiments}).agentRowDefinitions(t.Context(), database.Chat{}, rows) {
			out = append(out, def.Name)
		}
		return out
	}
	require.Equal(t, []string{"reviewer"}, names(codersdk.Experiments{}),
		"with fork-plugin-packs off the catalog omits pack agents")
	require.ElementsMatch(t, []string{"reviewer", "comment-sicko"}, names(codersdk.Experiments{codersdk.ExperimentForkPluginPacks}),
		"with fork-plugin-packs on the catalog lists pack agents")
}
