package chatd

// This file is fork code: it tests the archive_agent tool of the
// fork-nested-subagents experiment and the cleanup it runs behind
// fork-subagent-isolation.

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/codersdk"
)

// archiveAgentToolOf returns chat's archive_agent tool as a turn of chat
// gets it, or nil when the turn has none.
func archiveAgentToolOf(t *testing.T, server *Server, chat database.Chat) fantasy.AgentTool {
	t.Helper()
	ctx := chatdTestContext(t)
	tools, err := server.subagentTools(ctx, func() database.Chat { return chat }, chat.LastModelConfigID)
	require.NoError(t, err)
	tools, err = server.appendForkTools(ctx, tools, chat, chat.LastModelConfigID)
	require.NoError(t, err)
	return findToolByName(tools, archiveAgentToolName)
}

// runArchiveAgent calls chat's archive_agent tool for target.
func runArchiveAgent(t *testing.T, server *Server, chat database.Chat, target uuid.UUID) fantasy.ToolResponse {
	t.Helper()
	tool := archiveAgentToolOf(t, server, chat)
	require.NotNil(t, tool)
	input, err := json.Marshal(archiveAgentArgs{ChatID: target.String()})
	require.NoError(t, err)
	resp, err := tool.Run(chatdTestContext(t), fantasy.ToolCall{ID: uuid.NewString(), Name: archiveAgentToolName, Input: string(input)})
	require.NoError(t, err)
	return resp
}

func requireArchiveAgentResult(t *testing.T, resp fantasy.ToolResponse) archiveAgentResult {
	t.Helper()
	require.False(t, resp.IsError, resp.Content)
	var result archiveAgentResult
	require.NoError(t, json.Unmarshal([]byte(resp.Content), &result))
	return result
}

func requireChatsArchived(t *testing.T, db database.Store, want bool, chats ...database.Chat) {
	t.Helper()
	for _, chat := range chats {
		got, err := db.GetChatByID(chatdTestContext(t), chat.ID)
		require.NoError(t, err)
		require.Equal(t, want, got.Archived, "chat %s", chat.Title)
	}
}

func TestForkArchiveAgent(t *testing.T) {
	t.Parallel()
	both := codersdk.Experiments{codersdk.ExperimentForkSubagentIsolation, codersdk.ExperimentForkNestedSubagents}
	worktree := map[string]any{"isolation": "worktree"}

	// atRest waits for the server's turns and sets chats waiting.
	atRest := func(t *testing.T, f *isolationFixture, chats ...database.Chat) {
		t.Helper()
		ctx := chatdTestContext(t)
		require.NoError(t, WaitUntilIdleForTest(ctx, f.server))
		for _, chat := range chats {
			_, err := f.db.UpdateChatStatus(ctx, database.UpdateChatStatusParams{ID: chat.ID, Status: database.ChatStatusWaiting})
			require.NoError(t, err)
		}
	}

	t.Run("ArchivesOneChildSubtree", func(t *testing.T) {
		t.Parallel()
		f := newIsolationFixture(t, both)
		conn := &cleanupWorktreeConn{}
		root := f.rootChat(t, conn)
		retired := f.spawnChild(t, root, worktree)
		grandchild := f.spawnChild(t, retired, worktree)
		sibling := f.spawnChild(t, root, worktree)
		atRest(t, f, root, retired, grandchild, sibling)

		result := requireArchiveAgentResult(t, runArchiveAgent(t, f.server, root, retired.ID))
		require.Equal(t, retired.ID.String(), result.ChatID)
		require.Equal(t, []string{grandchild.ID.String(), retired.ID.String()}, result.ArchivedChatIDs)
		require.Empty(t, result.KeptWorktrees)

		requireChatsArchived(t, f.db, true, retired, grandchild)
		requireChatsArchived(t, f.db, false, root, sibling)
		deleted := map[string]string{}
		for _, req := range conn.deleted() {
			deleted[req.Path] = req.Branch
		}
		require.Len(t, deleted, 2)
		for _, chat := range []database.Chat{retired, grandchild} {
			row, err := f.fdb.GetChatSubagentByChatID(chatdTestContext(t), chat.ID)
			require.NoError(t, err)
			require.Equal(t, row.Branch, deleted[row.WorktreePath], "worktree of %s", chat.ID)
		}
		siblingRow, err := f.fdb.GetChatSubagentByChatID(chatdTestContext(t), sibling.ID)
		require.NoError(t, err)
		require.NotContains(t, deleted, siblingRow.WorktreePath)

		// The archived child leaves list_agents, and the root keeps working.
		listed, err := f.server.listSubagents(chatdTestContext(t), root.ID)
		require.NoError(t, err)
		require.Len(t, listed, 1)
		require.Equal(t, sibling.ID, listed[0].ID)
		require.Empty(t, f.systemNotices(t, root))
	})

	t.Run("ReportsKeptWorktree", func(t *testing.T) {
		t.Parallel()
		f := newIsolationFixture(t, both)
		conn := &cleanupWorktreeConn{}
		root := f.rootChat(t, conn)
		dirty := f.spawnChild(t, root, worktree)
		atRest(t, f, root, dirty)
		row, err := f.fdb.GetChatSubagentByChatID(chatdTestContext(t), dirty.ID)
		require.NoError(t, err)
		conn.failDelete(row.WorktreePath, codersdk.NewError(http.StatusConflict, codersdk.Response{
			Message: "worktree " + row.WorktreePath + " has 2 uncommitted or untracked changes; commit or discard them first",
		}))

		result := requireArchiveAgentResult(t, runArchiveAgent(t, f.server, root, dirty.ID))
		require.Equal(t, []archiveAgentKeptWorktree{{
			Branch: row.Branch,
			Path:   row.WorktreePath,
			Reason: "worktree " + row.WorktreePath + " has 2 uncommitted or untracked changes; commit or discard them first",
		}}, result.KeptWorktrees)
		requireChatsArchived(t, f.db, true, dirty)
		requireChatsArchived(t, f.db, false, root)
		require.Empty(t, f.systemNotices(t, root))
	})

	t.Run("StopsChildWorkspace", func(t *testing.T) {
		t.Parallel()
		f := newIsolationFixture(t, both)
		workspaces := &fakeWorkspaces{t: t, db: f.db}
		root, _ := f.workspaceRoot(t, workspaces)
		retired := f.spawnChild(t, root, map[string]any{"isolation": "workspace"})
		sibling := f.spawnChild(t, root, map[string]any{"isolation": "workspace"})
		atRest(t, f, root, retired, sibling)

		requireArchiveAgentResult(t, runArchiveAgent(t, f.server, root, retired.ID))
		// The stop runs in the background after the archive commits.
		f.server.drainInflight()
		requireChatsArchived(t, f.db, true, retired)
		requireChatsArchived(t, f.db, false, root, sibling)
		workspaces.mu.Lock()
		defer workspaces.mu.Unlock()
		require.Equal(t, []uuid.UUID{retired.WorkspaceID.UUID}, workspaces.stops)
	})

	t.Run("OnlyDirectChildren", func(t *testing.T) {
		t.Parallel()
		f := newIsolationFixture(t, both)
		root := f.rootChat(t, nil)
		child := f.spawnChild(t, root, map[string]any{})
		grandchild := f.spawnChild(t, child, map[string]any{})
		sibling := f.spawnChild(t, root, map[string]any{})
		atRest(t, f, root, child, grandchild, sibling)

		for _, tc := range []struct {
			name   string
			caller database.Chat
			target uuid.UUID
		}{
			{"Grandchild", root, grandchild.ID},
			{"Sibling", child, sibling.ID},
			{"Parent", child, root.ID},
			{"Self", child, child.ID},
			{"Unknown", root, uuid.New()},
		} {
			resp := runArchiveAgent(t, f.server, tc.caller, tc.target)
			require.True(t, resp.IsError, tc.name)
			require.Contains(t, resp.Content, "chat_id must be a direct child of this chat", tc.name)
		}
		requireChatsArchived(t, f.db, false, root, child, grandchild, sibling)

		// A grandchild is archived by its own parent.
		result := requireArchiveAgentResult(t, runArchiveAgent(t, f.server, child, grandchild.ID))
		require.Equal(t, []string{grandchild.ID.String()}, result.ArchivedChatIDs)
		requireChatsArchived(t, f.db, true, grandchild)
		requireChatsArchived(t, f.db, false, root, child, sibling)

		// Archiving the child again later skips the archived grandchild.
		result = requireArchiveAgentResult(t, runArchiveAgent(t, f.server, root, child.ID))
		require.Equal(t, []string{grandchild.ID.String(), child.ID.String()}, result.ArchivedChatIDs)
		requireChatsArchived(t, f.db, true, child, grandchild)
		requireChatsArchived(t, f.db, false, root, sibling)
	})

	t.Run("ExperimentOff", func(t *testing.T) {
		t.Parallel()
		f := newIsolationFixture(t, codersdk.Experiments{codersdk.ExperimentForkSubagentIsolation})
		root := f.rootChat(t, nil)
		require.Nil(t, archiveAgentToolOf(t, f.server, root))
	})
}

// systemNotices returns the user-visible system messages of chat.
func (f *isolationFixture) systemNotices(t *testing.T, chat database.Chat) []string {
	t.Helper()
	messages, err := f.db.GetChatMessagesByChatID(chatdTestContext(t), database.GetChatMessagesByChatIDParams{ChatID: chat.ID})
	require.NoError(t, err)
	var notices []string
	for _, msg := range messages {
		if msg.Role == database.ChatMessageRoleSystem && msg.Visibility == database.ChatMessageVisibilityUser {
			notices = append(notices, string(msg.Content.RawMessage))
		}
	}
	return notices
}

// An archived child no longer counts toward the live subagent limit of its
// family, and it cannot be resumed past that limit.
func TestForkArchiveAgentFreesLiveLimit(t *testing.T) {
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
	require.Equal(t, database.ChatStatusRunning, first.Status)
	_, err = f.spawn(t, f.root, childSubagentChatOptions{})
	require.ErrorContains(t, err, "the maximum is 1")

	// The running child is interrupted and archived.
	result := requireArchiveAgentResult(t, runArchiveAgent(t, f.server, f.root, first.ID))
	require.Equal(t, []string{first.ID.String()}, result.ArchivedChatIDs)
	f.requireArchived(t, true, first)
	f.requireArchived(t, false, f.root)

	second, err := f.spawn(t, f.root, childSubagentChatOptions{})
	require.NoError(t, err)
	require.Equal(t, database.ChatStatusRunning, second.Status)
	_, err = f.server.sendSubagentMessage(ctx, f.root.ID, first.ID, "continue")
	require.Error(t, err)
	f.requireArchived(t, true, first)
}
