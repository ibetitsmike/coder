package chatd

// This file is fork code: it tests nested subagents behind the
// fork-nested-subagents experiment.

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprompt"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/serpent"
)

type nestedSubagentsFixture struct {
	server *Server
	db     database.Store
	sqlDB  *sql.DB
	fdb    forkdb.Store
	root   database.Chat
}

func newNestedSubagentsFixture(t *testing.T, experiments codersdk.Experiments, limits Limits, opts ...internalTestServerOpt) nestedSubagentsFixture {
	t.Helper()

	db, ps, sqlDB := dbtestutil.NewDBWithSQLDB(t)
	server := newInternalTestServer(t, db, ps, chatprovider.ProviderAPIKeys{},
		append([]internalTestServerOpt{
			withInternalTestServerExperiments(experiments),
			withInternalTestServerLimits(limits),
		}, opts...)...,
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
	return nestedSubagentsFixture{server: server, db: db, sqlDB: sqlDB, fdb: fdb, root: root}
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

func TestNestedSubagentDepthAtSpawnToolBoundary(t *testing.T) {
	t.Parallel()

	f := newNestedSubagentsFixture(t,
		codersdk.Experiments{codersdk.ExperimentForkNestedSubagents},
		Limits{Fork: ForkLimits{MaxSubagentDepth: 1}})
	ctx := chatdTestContext(t)

	child, err := f.spawn(t, f.root, childSubagentChatOptions{})
	require.NoError(t, err)

	resp := runSpawnAgentTool(ctx, t, f.server, child, spawnAgentArgs{
		Type:   subagentTypeGeneral,
		Prompt: "delegate work",
	})
	require.True(t, resp.IsError, "expected a depth error but got: %s", resp.Content)
	require.Contains(t, resp.Content, "maximum subagent depth is 1")
	f.requireNoChildren(t, child)
}

// A child chat with no chat_subagents row predates depth recording and counts
// as depth 1.
func TestNestedSubagentDepthLegacyChild(t *testing.T) {
	t.Parallel()

	f := newNestedSubagentsFixture(t,
		codersdk.Experiments{codersdk.ExperimentForkNestedSubagents},
		Limits{Fork: ForkLimits{MaxSubagentDepth: 1}})
	ctx := chatdTestContext(t)

	legacy, err := f.server.CreateChat(ctx, CreateOptions{
		OrganizationID:     f.root.OrganizationID,
		OwnerID:            f.root.OwnerID,
		ParentChatID:       uuid.NullUUID{UUID: f.root.ID, Valid: true},
		RootChatID:         uuid.NullUUID{UUID: f.root.ID, Valid: true},
		Title:              "legacy-child",
		ModelConfigID:      f.root.LastModelConfigID,
		InitialUserContent: []codersdk.ChatMessagePart{codersdk.ChatMessageText("do work")},
	})
	require.NoError(t, err)
	legacy, err = f.db.GetChatByID(ctx, legacy.ID)
	require.NoError(t, err)
	_, err = f.fdb.GetChatSubagentByChatID(ctx, legacy.ID)
	require.ErrorIs(t, err, sql.ErrNoRows)

	_, err = f.spawn(t, legacy, childSubagentChatOptions{})
	require.ErrorContains(t, err, "this chat is at subagent depth 1 and the maximum subagent depth is 1")
	f.requireNoChildren(t, legacy)
}

// An organization max_depth lowers the deployment maximum subagent depth but
// cannot raise it.
func TestNestedSubagentOrganizationMaxDepth(t *testing.T) {
	t.Parallel()

	nested := codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}
	setOrgMaxDepth := func(t *testing.T, f nestedSubagentsFixture, maxDepth int32) {
		t.Helper()
		_, err := f.fdb.UpsertChatOrganizationSubagentLimits(chatdTestContext(t), forkdb.UpsertChatOrganizationSubagentLimitsParams{
			OrganizationID: f.root.OrganizationID,
			MaxDepth:       sql.NullInt32{Int32: maxDepth, Valid: true},
		})
		require.NoError(t, err)
	}

	t.Run("Lower", func(t *testing.T) {
		t.Parallel()
		f := newNestedSubagentsFixture(t, nested, Limits{Fork: ForkLimits{MaxSubagentDepth: 2}})
		setOrgMaxDepth(t, f, 1)
		ctx := chatdTestContext(t)

		require.False(t, f.server.childGetsOrchestrationPrompt(ctx, f.root, database.NullChatMode{}, nil))
		tools, err := f.server.subagentTools(ctx, func() database.Chat { return f.root }, f.root.LastModelConfigID)
		require.NoError(t, err)
		spawnTool := findToolByName(tools, spawnAgentToolName)
		require.NotNil(t, spawnTool)
		require.Contains(t, spawnTool.Info().Description, "below the maximum subagent depth of 1;")

		child, err := f.spawn(t, f.root, childSubagentChatOptions{})
		require.NoError(t, err)
		require.Empty(t, f.toolNames(t, child))
		_, err = f.spawn(t, child, childSubagentChatOptions{})
		require.ErrorContains(t, err, "maximum subagent depth is 1")
		require.ErrorContains(t, f.server.forkValidateSpawnParent(ctx, child), "maximum subagent depth is 1")
		f.requireNoChildren(t, child)
	})

	t.Run("Higher", func(t *testing.T) {
		t.Parallel()
		f := newNestedSubagentsFixture(t, nested, Limits{Fork: ForkLimits{MaxSubagentDepth: 2}})
		setOrgMaxDepth(t, f, 3)

		child, err := f.spawn(t, f.root, childSubagentChatOptions{})
		require.NoError(t, err)
		grandchild, err := f.spawn(t, child, childSubagentChatOptions{})
		require.NoError(t, err)
		require.Empty(t, f.toolNames(t, grandchild))
		_, err = f.spawn(t, grandchild, childSubagentChatOptions{})
		require.ErrorContains(t, err, "maximum subagent depth is 2")
		f.requireNoChildren(t, grandchild)
	})
}

func TestNestedSubagentLiveLimit(t *testing.T) {
	t.Parallel()

	f := newNestedSubagentsFixture(t, codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}, Limits{})
	ctx := chatdTestContext(t)
	_, err := f.fdb.UpsertChatOrganizationSubagentLimits(ctx, forkdb.UpsertChatOrganizationSubagentLimitsParams{
		OrganizationID:   f.root.OrganizationID,
		MaxLivePerFamily: sql.NullInt32{Int32: 1, Valid: true},
	})
	require.NoError(t, err)

	child, err := f.spawn(t, f.root, childSubagentChatOptions{})
	require.NoError(t, err)
	require.Equal(t, database.ChatStatusRunning, child.Status)
	_, err = f.spawn(t, f.root, childSubagentChatOptions{})
	require.ErrorContains(t, err, "this chat family has 1 live subagents and the maximum is 1")
	// The limit covers the whole family, not only the root's children.
	_, err = f.spawn(t, child, childSubagentChatOptions{})
	require.ErrorContains(t, err, "the maximum is 1")
	f.requireNoChildren(t, child)
	require.ErrorContains(t, f.server.forkValidateSpawnParent(ctx, f.root), "the maximum is 1")

	f.setStatus(t, child, database.ChatStatusWaiting, uuid.NullUUID{})
	_, err = f.spawn(t, f.root, childSubagentChatOptions{})
	require.NoError(t, err)
	// Resuming the finished child would make two live subagents.
	_, err = f.server.sendSubagentMessage(ctx, f.root.ID, child.ID, "continue")
	require.ErrorContains(t, err, "cannot resume the child agent: this chat family has 1 live subagents and the maximum is 1")

	// A concurrent message_agent resumed the child after this one loaded
	// it, so the send only queues on a live child.
	stale := child
	stale.Status = database.ChatStatusWaiting
	f.setStatus(t, child, database.ChatStatusRunning, uuid.NullUUID{})
	require.NoError(t, f.server.forkValidateSubagentResume(withSubagentResume(ctx), f.db, stale, false))
}

// A resume that races a spawn of the same family waits for the spawn to
// commit, so it counts the spawned child against the live limit.
func TestNestedSubagentLiveLimitSerializesResume(t *testing.T) {
	t.Parallel()

	f := newNestedSubagentsFixture(t, codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}, Limits{})
	ctx := chatdTestContext(t)
	_, err := f.fdb.UpsertChatOrganizationSubagentLimits(ctx, forkdb.UpsertChatOrganizationSubagentLimitsParams{
		OrganizationID:   f.root.OrganizationID,
		MaxLivePerFamily: sql.NullInt32{Int32: 1, Valid: true},
	})
	require.NoError(t, err)
	first, err := f.spawn(t, f.root, childSubagentChatOptions{})
	require.NoError(t, err)
	f.setStatus(t, first, database.ChatStatusWaiting, uuid.NullUUID{})
	second, err := f.spawn(t, f.root, childSubagentChatOptions{})
	require.NoError(t, err)
	f.setStatus(t, second, database.ChatStatusWaiting, uuid.NullUUID{})

	// The holder stands in for a spawn that has passed the count and
	// commits a live child.
	lockID := chatFamilyAdmissionLockID(f.root.ID)
	holding := make(chan struct{})
	release := make(chan struct{})
	releaseLock := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseLock)
	holderErr := make(chan error, 1)
	go func() {
		holderErr <- f.db.InTx(func(tx database.Store) error {
			if err := tx.AcquireLock(ctx, lockID); err != nil {
				return err
			}
			close(holding)
			<-release
			_, err := tx.UpdateChatStatus(ctx, database.UpdateChatStatusParams{ID: second.ID, Status: database.ChatStatusRunning})
			return err
		}, nil)
	}()
	testutil.TryReceive(ctx, t, holding)

	resumeErr := make(chan error, 1)
	go func() {
		_, err := f.server.sendSubagentMessage(ctx, f.root.ID, first.ID, "continue")
		resumeErr <- err
	}()
	testutil.Eventually(ctx, t, func(ctx context.Context) bool {
		var waiting int
		err := f.sqlDB.QueryRowContext(ctx, `SELECT count(*) FROM pg_locks
			WHERE locktype = 'advisory' AND NOT granted AND objsubid = 1
			AND classid::bigint = $1 AND objid::bigint = $2`,
			int64(uint32(lockID>>32)), int64(uint32(lockID))).Scan(&waiting)
		return err == nil && waiting == 1
	}, testutil.IntervalFast, "the resume waits for the family admission lock")
	releaseLock()
	require.NoError(t, testutil.TryReceive(ctx, t, holderErr))

	require.ErrorContains(t, testutil.TryReceive(ctx, t, resumeErr), "cannot resume the child agent: this chat family has 1 live subagents and the maximum is 1")
	f.requireStatus(t, database.ChatStatusWaiting, first)
}

func TestForkLimitsFromConfig(t *testing.T) {
	t.Parallel()

	cfg := codersdk.ChatConfig{Fork: codersdk.ForkChatConfig{
		MaxSubagentDepth:                serpent.Int64(4),
		MaxConcurrentSubagents:          serpent.Int64(5),
		MaxConcurrentSubagentsPerFamily: serpent.Int64(6),
		MaxLiveSubagentsPerFamily:       serpent.Int64(7),
	}}
	require.Equal(t, ForkLimits{
		MaxSubagentDepth:                4,
		MaxConcurrentSubagents:          5,
		MaxConcurrentSubagentsPerFamily: 6,
		MaxLiveSubagentsPerFamily:       7,
	}, LimitsFromConfig(cfg).Fork)
}

func TestNestedSubagentLeaves(t *testing.T) {
	t.Parallel()

	f := newNestedSubagentsFixture(t, codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}, Limits{})
	ctx := chatdTestContext(t)

	for _, tc := range []struct {
		mode     database.ChatMode
		wantType string
		wantErr  string
	}{
		{database.ChatModeExplore, subagentTypeExplore, "explore chats cannot create child subagents"},
		{database.ChatModeComputerUse, subagentTypeComputerUse, "computer use chats cannot create child subagents"},
	} {
		child, err := f.spawn(t, f.root, childSubagentChatOptions{
			chatMode: database.NullChatMode{ChatMode: tc.mode, Valid: true},
		})
		require.NoError(t, err)
		row, err := f.fdb.GetChatSubagentByChatID(ctx, child.ID)
		require.NoError(t, err)
		require.Equal(t, tc.wantType, row.AgentType)
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

func (f nestedSubagentsFixture) toolNames(t *testing.T, chat database.Chat) []string {
	t.Helper()
	tools, err := f.server.nestedSubagentTools(chatdTestContext(t), chat, chat.LastModelConfigID)
	require.NoError(t, err)
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Info().Name)
	}
	return names
}

func TestNestedSubagentTools(t *testing.T) {
	t.Parallel()

	f := newNestedSubagentsFixture(t, codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}, Limits{})
	child, err := f.spawn(t, f.root, childSubagentChatOptions{})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{
		spawnAgentToolName, listSubagentModelsToolName,
		"list_agents", "wait_agent", "message_agent", "interrupt_agent",
	}, f.toolNames(t, child))

	grandchild, err := f.spawn(t, child, childSubagentChatOptions{})
	require.NoError(t, err)
	require.Empty(t, f.toolNames(t, grandchild), "a chat at the maximum depth cannot spawn")
	explore, err := f.spawn(t, f.root, childSubagentChatOptions{
		chatMode: database.NullChatMode{ChatMode: database.ChatModeExplore, Valid: true},
	})
	require.NoError(t, err)
	require.Empty(t, f.toolNames(t, explore))
	require.Empty(t, f.toolNames(t, f.root), "roots get the subagent tools from appendRootChatTools")

	off := newNestedSubagentsFixture(t, upstreamExperimentsKnown(), Limits{})
	offChild, err := off.spawn(t, off.root, childSubagentChatOptions{})
	require.NoError(t, err)
	require.Empty(t, off.toolNames(t, offChild))
}

// Lowering the maximum depth below an existing family leaves the parent of
// the deeper children with the tools to manage them, but not to spawn more.
func TestNestedSubagentToolsAfterDepthLowered(t *testing.T) {
	t.Parallel()

	f := newNestedSubagentsFixture(t, codersdk.Experiments{codersdk.ExperimentForkNestedSubagents},
		Limits{Fork: ForkLimits{MaxSubagentDepth: 1}})
	ctx := chatdTestContext(t)

	child, err := f.spawn(t, f.root, childSubagentChatOptions{})
	require.NoError(t, err)
	require.Empty(t, f.toolNames(t, child), "a chat at the maximum depth with no children gets no subagent tools")

	// CreateChat skips the depth check, standing in for a grandchild
	// spawned while the maximum depth was higher.
	_, err = f.server.CreateChat(ctx, CreateOptions{
		OrganizationID:     child.OrganizationID,
		OwnerID:            child.OwnerID,
		ParentChatID:       uuid.NullUUID{UUID: child.ID, Valid: true},
		RootChatID:         uuid.NullUUID{UUID: f.root.ID, Valid: true},
		Title:              "grandchild",
		ModelConfigID:      child.LastModelConfigID,
		InitialUserContent: []codersdk.ChatMessagePart{codersdk.ChatMessageText("do work")},
	})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"list_agents", "wait_agent", "message_agent", "interrupt_agent"}, f.toolNames(t, child))
}

func TestNestedSubagentSpawnDescription(t *testing.T) {
	t.Parallel()

	spawnDescription := func(t *testing.T, f nestedSubagentsFixture, chat database.Chat) string {
		t.Helper()
		tools, err := f.server.subagentTools(chatdTestContext(t), func() database.Chat { return chat }, chat.LastModelConfigID)
		require.NoError(t, err)
		tool := findToolByName(tools, spawnAgentToolName)
		require.NotNil(t, tool)
		return tool.Info().Description
	}

	// The fork rewrites this upstream sentence, so it must stay in
	// upstream's description for the rewrite to apply.
	off := newNestedSubagentsFixture(t, upstreamExperimentsKnown(), Limits{})
	require.Contains(t, spawnDescription(t, off, off.root), upstreamSpawnAgentChildToolsSentence)

	f := newNestedSubagentsFixture(t, codersdk.Experiments{codersdk.ExperimentForkNestedSubagents},
		Limits{Fork: ForkLimits{MaxSubagentDepth: 3}})
	description := spawnDescription(t, f, f.root)
	require.NotContains(t, description, upstreamSpawnAgentChildToolsSentence)
	require.Contains(t, description, "A general child can spawn its own subagents while it is below the maximum subagent depth of 3; "+
		"explore and computer use children cannot. Its tools depend on its type and mode. ")
}

func TestNestedSubagentTargets(t *testing.T) {
	t.Parallel()

	f := newNestedSubagentsFixture(t, codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}, Limits{})
	ctx := chatdTestContext(t)
	child, err := f.spawn(t, f.root, childSubagentChatOptions{})
	require.NoError(t, err)
	grandchild, err := f.spawn(t, child, childSubagentChatOptions{})
	require.NoError(t, err)

	for _, tc := range []struct {
		parent, target uuid.UUID
		want           bool
	}{
		{f.root.ID, child.ID, true},
		{f.root.ID, grandchild.ID, false},
		{child.ID, grandchild.ID, true},
		{grandchild.ID, child.ID, false},
		{f.root.ID, uuid.New(), false},
	} {
		got, err := f.server.isSubagentTarget(ctx, tc.parent, tc.target)
		require.NoError(t, err)
		require.Equal(t, tc.want, got, "parent %s target %s", tc.parent, tc.target)
	}
	_, _, err = f.server.awaitSubagentCompletion(ctx, f.root.ID, grandchild.ID, 0)
	require.ErrorIs(t, err, ErrSubagentNotDescendant)
	_, err = f.server.awaitSubagent(ctx, f.root, grandchild.ID, 0)
	require.ErrorIs(t, err, ErrSubagentNotDescendant)
	_, err = f.server.sendSubagentMessage(ctx, f.root.ID, grandchild.ID, "hi")
	require.ErrorIs(t, err, ErrSubagentNotDescendant)
	_, _, err = f.server.interruptSubagent(ctx, f.root.ID, grandchild.ID)
	require.ErrorIs(t, err, ErrSubagentNotDescendant)

	listed := func(chat database.Chat) []string {
		t.Helper()
		resp := runSubagentTool(ctx, t, f.server, chat, chat.LastModelConfigID, "list_agents", listAgentsArgs{})
		require.False(t, resp.IsError, resp.Content)
		var result subagentListResult
		require.NoError(t, json.Unmarshal([]byte(resp.Content), &result))
		ids := make([]string, 0, len(result.Agents))
		for _, agent := range result.Agents {
			ids = append(ids, agent.ChatID)
		}
		return ids
	}
	require.Equal(t, []string{child.ID.String()}, listed(f.root))
	require.Equal(t, []string{grandchild.ID.String()}, listed(child))

	// Upstream lets a chat target any descendant. Only nested subagents can
	// build a grandchild, so check upstream's rule on the same family.
	off := &Server{db: f.db, experiments: upstreamExperimentsKnown()}
	got, err := off.isSubagentTarget(ctx, f.root.ID, grandchild.ID)
	require.NoError(t, err)
	require.True(t, got)
}

func TestNestedSubagentOrchestrationPrompt(t *testing.T) {
	t.Parallel()

	deploymentPrompt := func(t *testing.T, f nestedSubagentsFixture, chat database.Chat) string {
		t.Helper()
		messages, err := f.db.GetChatMessagesForPromptByChatID(chatdTestContext(t), chat.ID)
		require.NoError(t, err)
		require.NotEmpty(t, messages)
		require.Equal(t, database.ChatMessageRoleSystem, messages[0].Role)
		parts, err := chatprompt.ParseContent(messages[0])
		require.NoError(t, err)
		require.Len(t, parts, 1)
		return parts[0].Text
	}
	require.Contains(t, DefaultSystemPrompt, subagentOrchestrationPromptBlock)
	stripped := strings.Replace(DefaultSystemPrompt, subagentOrchestrationPromptBlock, "", 1)

	f := newNestedSubagentsFixture(t, codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}, Limits{})
	child, err := f.spawn(t, f.root, childSubagentChatOptions{})
	require.NoError(t, err)
	require.Equal(t, DefaultSystemPrompt, deploymentPrompt(t, f, child))
	grandchild, err := f.spawn(t, child, childSubagentChatOptions{})
	require.NoError(t, err)
	require.Equal(t, stripped, deploymentPrompt(t, f, grandchild), "a child at the maximum depth cannot spawn")
	explore, err := f.spawn(t, f.root, childSubagentChatOptions{
		chatMode: database.NullChatMode{ChatMode: database.ChatModeExplore, Valid: true},
	})
	require.NoError(t, err)
	require.Equal(t, stripped, deploymentPrompt(t, f, explore))

	off := newNestedSubagentsFixture(t, upstreamExperimentsKnown(), Limits{})
	offChild, err := off.spawn(t, off.root, childSubagentChatOptions{})
	require.NoError(t, err)
	require.Equal(t, stripped, deploymentPrompt(t, off, offChild))
}

func TestAncestorChatIDs(t *testing.T) {
	t.Parallel()

	f := newNestedSubagentsFixture(t, codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}, Limits{})
	ctx := chatdTestContext(t)
	child, err := f.spawn(t, f.root, childSubagentChatOptions{})
	require.NoError(t, err)
	grandchild, err := f.spawn(t, child, childSubagentChatOptions{})
	require.NoError(t, err)

	ids := func(nested bool, chat database.Chat) []string {
		t.Helper()
		return AncestorChatIDs(ctx, f.db, f.server.logger, nested, chat)
	}

	require.Nil(t, ids(true, f.root))
	require.Equal(t, []string{f.root.ID.String()}, ids(true, child))
	require.Equal(t, []string{child.ID.String(), f.root.ID.String()}, ids(true, grandchild))

	require.Nil(t, ids(false, f.root))
	require.Equal(t, []string{f.root.ID.String()}, ids(false, child))
	require.Equal(t, []string{child.ID.String()}, ids(false, grandchild))
}
