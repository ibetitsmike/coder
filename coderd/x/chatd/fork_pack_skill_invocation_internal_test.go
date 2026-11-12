package chatd

// This file is fork code: it tests that users can invoke plugin pack skills
// hidden from the model, the slash-command entry point of PRD 6.7.

import (
	"database/sql"
	"errors"
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

func TestInvokeHiddenPackSkill(t *testing.T) {
	t.Parallel()

	db, ps := dbtestutil.NewDB(t)
	server := newInternalTestServer(t, db, ps, chatprovider.ProviderAPIKeys{},
		withInternalTestServerExperiments(codersdk.Experiments{codersdk.ExperimentForkPluginPacks}))
	fdb, err := forkdb.From(db)
	require.NoError(t, err)
	ctx := chatdTestContext(t)
	user, org, model := seedInternalChatDeps(t, db)

	now := dbtime.Now()
	pack, err := fdb.InsertPluginPack(ctx, forkdb.InsertPluginPackParams{
		ID: uuid.New(), OrganizationID: org.ID, Name: "pstack", SourceUrl: "https://example.com/plugins.git",
		Ref: "v0.15.36", CommitSha: "0123456789abcdef0123456789abcdef01234567", CreatedBy: user.ID,
		CreatedAt: now, UpdatedAt: now,
	})
	require.NoError(t, err)
	const skillMarkdown = "---\nname: architect\ndescription: Plan a change.\ndisable-model-invocation: true\n---\n\nPlan the change first.\n"
	require.NoError(t, fdb.InsertPluginPackResources(ctx, forkdb.InsertPluginPackResourcesParams{
		PackID:        pack.ID,
		Paths:         []string{".claude-plugin/plugin.json", "skills/architect/SKILL.md"},
		Contents:      [][]byte{[]byte("{}"), []byte(skillMarkdown)},
		ContentHashes: []string{"hash", "hash"},
	}))

	// The agent pushes the materialized pack skill, and each chat on the
	// workspace pins it.
	version := dbgen.TemplateVersion(t, db, database.TemplateVersion{OrganizationID: org.ID, CreatedBy: user.ID})
	tmpl := dbgen.Template(t, db, database.Template{OrganizationID: org.ID, ActiveVersionID: version.ID, CreatedBy: user.ID})
	ws := dbgen.Workspace(t, db, database.WorkspaceTable{OwnerID: user.ID, OrganizationID: org.ID, TemplateID: tmpl.ID})
	job := dbgen.ProvisionerJob(t, db, nil, database.ProvisionerJob{
		OrganizationID: org.ID, CompletedAt: sql.NullTime{Time: now, Valid: true},
	})
	dbgen.WorkspaceBuild(t, db, database.WorkspaceBuild{
		WorkspaceID: ws.ID, TemplateVersionID: version.ID, JobID: job.ID, Transition: database.WorkspaceTransitionStart,
	})
	resource := dbgen.WorkspaceResource(t, db, database.WorkspaceResource{JobID: job.ID, Transition: database.WorkspaceTransitionStart})
	agent := dbgen.WorkspaceAgent(t, db, database.WorkspaceAgent{ResourceID: resource.ID})
	_, err = db.UpsertWorkspaceAgentContextSnapshot(ctx, database.UpsertWorkspaceAgentContextSnapshotParams{
		WorkspaceAgentID: agent.ID, Version: 1, AggregateHash: []byte("hash"), ReceivedAt: now,
	})
	require.NoError(t, err)
	skillDir := "/home/coder/.coder/plugins/pstack/skills/architect"
	_, err = db.UpsertWorkspaceAgentContextResource(ctx, database.UpsertWorkspaceAgentContextResourceParams{
		WorkspaceAgentID: agent.ID, Source: skillDir, SourcePath: "/home/coder/.coder/plugins/pstack/.claude-plugin/plugin.json",
		BodyKind:    database.WorkspaceAgentContextBodyKindSkill,
		Body:        mustMarshalContextBody(t, &agentproto.SkillMetaBody{Meta: []byte(skillMarkdown), Name: "architect"}),
		ContentHash: []byte("hash"), Status: database.WorkspaceAgentContextResourceStatusOk, Now: now,
	})
	require.NoError(t, err)
	pinnedChat := func() database.Chat {
		chat := dbgen.Chat(t, db, database.Chat{
			OrganizationID: org.ID, OwnerID: user.ID, LastModelConfigID: model.ID,
			WorkspaceID: uuid.NullUUID{UUID: ws.ID, Valid: true}, AgentID: uuid.NullUUID{UUID: agent.ID, Valid: true},
		})
		require.NoError(t, db.InsertAgentContextResourcesIntoChat(ctx, database.InsertAgentContextResourcesIntoChatParams{
			ChatID: chat.ID, AgentID: agent.ID,
		}))
		return chat
	}
	enabled, notEnabled := pinnedChat(), pinnedChat()
	_, err = fdb.InsertChatPluginPack(ctx, forkdb.InsertChatPluginPackParams{ChatID: enabled.ID, PackID: pack.ID})
	require.NoError(t, err)

	_, skills, err := server.resolveTurnWorkspaceContext(ctx, enabled, database.WorkspaceAgent{})
	require.NoError(t, err)
	require.Len(t, skills, 1)
	require.Empty(t, workspaceSkillsForResolution(skills), "the skill is hidden from the model's skill index")

	invocation := []codersdk.ChatMessagePart{codersdk.ChatMessageSkillInvocation(codersdk.ChatSkillSourceWorkspace, "architect")}
	resolved, err := server.resolveSkillInvocations(ctx, enabled, invocation)
	require.NoError(t, err)
	require.Equal(t, "Plan the change first.", resolved[0].SkillBody, "a user invocation loads the hidden skill")
	require.Equal(t, skillDir, resolved[0].SkillDir)

	_, err = server.resolveSkillInvocations(ctx, notEnabled, invocation)
	var unavailable *SkillInvocationUnavailableError
	require.True(t, errors.As(err, &unavailable), "a chat without the pack enabled cannot invoke its skill, got %v", err)
}
