package chatd

// This file is fork code: it tests the chat_id and recursive options that
// fork-nested-subagents adds to list_agents.

import (
	"context"
	"encoding/json"
	"testing"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/codersdk"
)

func TestForkListAgentsDescendants(t *testing.T) {
	t.Parallel()

	f := newNestedSubagentsFixture(t, codersdk.Experiments{
		codersdk.ExperimentForkNestedSubagents, codersdk.ExperimentForkChatTodos,
	}, Limits{})
	child, err := f.spawn(t, f.root, childSubagentChatOptions{})
	require.NoError(t, err)
	sibling, err := f.spawn(t, f.root, childSubagentChatOptions{})
	require.NoError(t, err)
	grandchild, err := f.spawn(t, child, childSubagentChatOptions{})
	require.NoError(t, err)

	// tools builds a turn's tool list the way turn_environment.go does: a
	// root gets the subagent tools from appendRootChatTools, a child from
	// appendForkTools.
	// Each subtest makes its own context: parallel subtests can wait for a
	// slot long after the parent returns.
	tools := func(ctx context.Context, t *testing.T, chat database.Chat) []fantasy.AgentTool {
		t.Helper()
		var base []fantasy.AgentTool
		if !chat.ParentChatID.Valid {
			var err error
			base, err = f.server.subagentTools(ctx, func() database.Chat { return chat }, chat.LastModelConfigID)
			require.NoError(t, err)
		}
		all, err := f.server.appendForkTools(ctx, base, chat, chat.LastModelConfigID)
		require.NoError(t, err)
		return all
	}
	list := func(ctx context.Context, t *testing.T, chat database.Chat, args map[string]any, wantError bool) (forkListAgentsResult, string) {
		t.Helper()
		content := runChatTodoTool(ctx, t, tools(ctx, t, chat), "list_agents", args, wantError)
		var result forkListAgentsResult
		if !wantError {
			require.NoError(t, json.Unmarshal([]byte(content), &result))
		}
		return result, content
	}
	parentsByID := func(result forkListAgentsResult) map[string]string {
		parents := map[string]string{}
		for _, agent := range result.Agents {
			parents[agent.ChatID] = agent.ParentChatID
		}
		return parents
	}

	t.Run("RootListsSubtreeAndReadsGrandchild", func(t *testing.T) {
		t.Parallel()
		ctx := chatdTestContext(t)
		result, _ := list(ctx, t, f.root, map[string]any{"recursive": true}, false)
		require.Equal(t, map[string]string{
			child.ID.String():      f.root.ID.String(),
			sibling.ID.String():    f.root.ID.String(),
			grandchild.ID.String(): child.ID.String(),
		}, parentsByID(result))
		require.Equal(t, 3, result.Total)

		result, _ = list(ctx, t, f.root, map[string]any{"chat_id": child.ID.String()}, false)
		require.Equal(t, map[string]string{grandchild.ID.String(): child.ID.String()}, parentsByID(result))

		content := runChatTodoTool(ctx, t, tools(ctx, t, f.root), readChatTranscriptToolName,
			readChatTranscriptArgs{ChatID: grandchild.ID.String()}, false)
		var page chatTranscriptResult
		require.NoError(t, json.Unmarshal([]byte(content), &page))
		require.Equal(t, grandchild.ID, page.ChatID)
		require.NotEmpty(t, page.Messages)
	})

	t.Run("PagesTheSubtree", func(t *testing.T) {
		t.Parallel()
		ctx := chatdTestContext(t)
		result, _ := list(ctx, t, f.root, map[string]any{"recursive": true, "limit": 2, "offset": 1}, false)
		require.Len(t, result.Agents, 2)
		require.Equal(t, 3, result.Total)
		require.Equal(t, 1, result.Offset)
		require.False(t, result.HasMore)
	})

	t.Run("WithoutOptionsListsChildrenAsUpstream", func(t *testing.T) {
		t.Parallel()
		ctx := chatdTestContext(t)
		_, content := list(ctx, t, f.root, map[string]any{}, false)
		require.NotContains(t, content, "parent_chat_id")
		require.NotContains(t, content, grandchild.ID.String())
	})

	t.Run("ChildListsOnlyItsSubtree", func(t *testing.T) {
		t.Parallel()
		ctx := chatdTestContext(t)
		result, _ := list(ctx, t, child, map[string]any{"recursive": true}, false)
		require.Equal(t, map[string]string{grandchild.ID.String(): child.ID.String()}, parentsByID(result))
		for _, outside := range []uuid.UUID{f.root.ID, sibling.ID, uuid.New()} {
			_, content := list(ctx, t, child, map[string]any{"chat_id": outside.String()}, true)
			require.Contains(t, content, "chat_id must be this chat or one of its descendants")
		}
	})

	t.Run("ExperimentOffKeepsUpstreamSchema", func(t *testing.T) {
		t.Parallel()
		ctx := chatdTestContext(t)
		off := newNestedSubagentsFixture(t, codersdk.Experiments{codersdk.ExperimentForkChatTodos}, Limits{})
		base, err := off.server.subagentTools(ctx, func() database.Chat { return off.root }, off.root.LastModelConfigID)
		require.NoError(t, err)
		all, err := off.server.appendForkTools(ctx, base, off.root, off.root.LastModelConfigID)
		require.NoError(t, err)
		tool := findToolByName(all, "list_agents")
		require.NotNil(t, tool)
		require.NotContains(t, tool.Info().Parameters, "chat_id")
		require.NotContains(t, tool.Info().Parameters, "recursive")
	})
}

// Deleting a family root promotes its child to a root and clears the
// root_chat_id of every descendant, and a recursive list from the promoted
// root still reaches the deepest chat.
func TestForkListAgentsAfterRootDeleted(t *testing.T) {
	t.Parallel()

	f := newNestedSubagentsFixture(t, codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}, Limits{})
	ctx := chatdTestContext(t)
	descendant := func(parent database.Chat) database.Chat {
		return dbgen.Chat(t, f.db, database.Chat{
			OrganizationID:    f.root.OrganizationID,
			OwnerID:           f.root.OwnerID,
			LastModelConfigID: f.root.LastModelConfigID,
			ParentChatID:      uuid.NullUUID{UUID: parent.ID, Valid: true},
			RootChatID:        uuid.NullUUID{UUID: f.root.ID, Valid: true},
			Status:            database.ChatStatusWaiting,
		})
	}
	promoted := descendant(f.root)
	child := descendant(promoted)
	grandchild := descendant(child)
	_, err := f.sqlDB.ExecContext(ctx, "DELETE FROM chats WHERE id = $1", f.root.ID)
	require.NoError(t, err)
	promoted, err = f.db.GetChatByID(ctx, promoted.ID)
	require.NoError(t, err)
	require.False(t, promoted.ParentChatID.Valid)
	require.False(t, promoted.RootChatID.Valid)

	base, err := f.server.subagentTools(ctx, func() database.Chat { return promoted }, promoted.LastModelConfigID)
	require.NoError(t, err)
	tools, err := f.server.appendForkTools(ctx, base, promoted, promoted.LastModelConfigID)
	require.NoError(t, err)
	content := runChatTodoTool(ctx, t, tools, "list_agents", map[string]any{"recursive": true}, false)
	var result forkListAgentsResult
	require.NoError(t, json.Unmarshal([]byte(content), &result))
	parents := map[string]string{}
	for _, agent := range result.Agents {
		parents[agent.ChatID] = agent.ParentChatID
	}
	require.Equal(t, map[string]string{
		child.ID.String():      promoted.ID.String(),
		grandchild.ID.String(): child.ID.String(),
	}, parents)
}
