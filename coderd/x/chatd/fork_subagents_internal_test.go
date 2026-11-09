package chatd

// This file is fork code: it tests nested subagent depths behind the
// fork-nested-subagents experiment.

import (
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
	"github.com/coder/coder/v2/codersdk"
)

type nestedSubagentsFixture struct {
	server *Server
	db     database.Store
	fdb    forkdb.Store
	root   database.Chat
}

func newNestedSubagentsFixture(t *testing.T, experiments codersdk.Experiments, limits Limits) nestedSubagentsFixture {
	t.Helper()

	db, ps := dbtestutil.NewDB(t)
	server := newInternalTestServer(t, db, ps, chatprovider.ProviderAPIKeys{},
		withInternalTestServerExperiments(experiments),
		withInternalTestServerLimits(limits),
	)
	fdb, err := forkdb.From(db)
	require.NoError(t, err)

	ctx := chatdTestContext(t)
	user, org, model := seedInternalChatDeps(t, db)
	workspace, build, agent := seedWorkspaceBinding(t, db, user.ID)
	root, err := server.CreateChat(ctx, CreateOptions{
		OrganizationID:     org.ID,
		OwnerID:            user.ID,
		WorkspaceID:        uuid.NullUUID{UUID: workspace.ID, Valid: true},
		BuildID:            uuid.NullUUID{UUID: build.ID, Valid: true},
		AgentID:            uuid.NullUUID{UUID: agent.ID, Valid: true},
		Title:              "root",
		ModelConfigID:      model.ID,
		InitialUserContent: []codersdk.ChatMessagePart{codersdk.ChatMessageText("hello")},
	})
	require.NoError(t, err)
	root, err = db.GetChatByID(ctx, root.ID)
	require.NoError(t, err)
	return nestedSubagentsFixture{server: server, db: db, fdb: fdb, root: root}
}

func (f nestedSubagentsFixture) spawn(t *testing.T, parent database.Chat, opts childSubagentChatOptions) (database.Chat, error) {
	t.Helper()
	ctx := chatdTestContext(t)
	child, err := f.server.createChildSubagentChatWithOptions(ctx, parent, "do the work", "", opts)
	if err != nil {
		return database.Chat{}, err
	}
	child, err = f.db.GetChatByID(ctx, child.ID)
	require.NoError(t, err)
	return child, nil
}

func (f nestedSubagentsFixture) requireNoChildren(t *testing.T, parent database.Chat) {
	t.Helper()
	children, err := f.db.GetChildChatsByParentIDs(chatdTestContext(t), database.GetChildChatsByParentIDsParams{
		ParentIds: []uuid.UUID{parent.ID},
	})
	require.NoError(t, err)
	require.Empty(t, children)
}

func TestNestedSubagentDepth(t *testing.T) {
	t.Parallel()

	nested := codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}

	t.Run("DefaultLimit", func(t *testing.T) {
		t.Parallel()
		f := newNestedSubagentsFixture(t, nested, Limits{})
		ctx := chatdTestContext(t)

		child, err := f.spawn(t, f.root, childSubagentChatOptions{})
		require.NoError(t, err)
		row, err := f.fdb.GetChatSubagentByChatID(ctx, child.ID)
		require.NoError(t, err)
		require.Equal(t, int32(1), row.Depth)
		require.Equal(t, subagentTypeGeneral, row.AgentType)
		require.Equal(t, "shared", row.Isolation)

		grandchild, err := f.spawn(t, child, childSubagentChatOptions{})
		require.NoError(t, err)
		require.Equal(t, f.root.ID, grandchild.RootChatID.UUID)
		row, err = f.fdb.GetChatSubagentByChatID(ctx, grandchild.ID)
		require.NoError(t, err)
		require.Equal(t, int32(2), row.Depth)

		_, err = f.spawn(t, grandchild, childSubagentChatOptions{})
		require.ErrorContains(t, err, "maximum subagent depth is 2")
		f.requireNoChildren(t, grandchild)
	})

	t.Run("LimitOne", func(t *testing.T) {
		t.Parallel()
		f := newNestedSubagentsFixture(t, nested, Limits{Fork: ForkLimits{MaxSubagentDepth: 1}})
		ctx := chatdTestContext(t)

		child, err := f.spawn(t, f.root, childSubagentChatOptions{})
		require.NoError(t, err)
		_, err = f.spawn(t, child, childSubagentChatOptions{})
		require.ErrorContains(t, err, "maximum subagent depth is 1")
		require.ErrorContains(t, f.server.forkValidateSpawnParent(ctx, child), "maximum subagent depth is 1")
		f.requireNoChildren(t, child)
	})
}

func TestNestedSubagentLeaves(t *testing.T) {
	t.Parallel()

	f := newNestedSubagentsFixture(t, codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}, Limits{})
	ctx := chatdTestContext(t)

	for _, tc := range []struct {
		mode    database.ChatMode
		wantErr string
	}{
		{database.ChatModeExplore, "explore chats cannot create child subagents"},
		{database.ChatModeComputerUse, "computer use chats cannot create child subagents"},
	} {
		child, err := f.spawn(t, f.root, childSubagentChatOptions{
			chatMode: database.NullChatMode{ChatMode: tc.mode, Valid: true},
		})
		require.NoError(t, err)
		row, err := f.fdb.GetChatSubagentByChatID(ctx, child.ID)
		require.NoError(t, err)
		require.Equal(t, subagentTypeFromChat(child), row.AgentType)
		require.ErrorContains(t, f.server.forkValidateSpawnParent(ctx, child), tc.wantErr)
		_, err = f.spawn(t, child, childSubagentChatOptions{})
		require.ErrorContains(t, err, tc.wantErr)
		f.requireNoChildren(t, child)
	}
}

func TestNestedSubagentsExperimentOff(t *testing.T) {
	t.Parallel()

	f := newNestedSubagentsFixture(t, upstreamExperimentsKnown(), Limits{})
	ctx := chatdTestContext(t)

	child, err := f.spawn(t, f.root, childSubagentChatOptions{})
	require.NoError(t, err)
	_, err = f.fdb.GetChatSubagentByChatID(ctx, child.ID)
	require.ErrorIs(t, err, sql.ErrNoRows)

	_, err = f.spawn(t, child, childSubagentChatOptions{})
	require.ErrorContains(t, err, "delegated chats cannot create child subagents")
	require.ErrorContains(t, f.server.forkValidateSpawnParent(ctx, child), "delegated chats cannot create child subagents")
	f.requireNoChildren(t, child)
}
