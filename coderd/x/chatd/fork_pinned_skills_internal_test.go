package chatd

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprompt"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
	"github.com/coder/coder/v2/codersdk"
)

func TestPinnedSkillsMessage(t *testing.T) {
	t.Parallel()

	db, ps := dbtestutil.NewDB(t)
	server := newInternalTestServer(t, db, ps, chatprovider.ProviderAPIKeys{},
		withInternalTestServerExperiments(codersdk.Experiments{codersdk.ExperimentForkPinnedSkills}))
	user, org, model := seedInternalChatDeps(t, db)
	ctx := chatdTestContext(t)

	const deployBody, reviewBody = "Run make deploy before you push.", "Review every diff twice."
	for name, body := range map[string]string{"deploy": deployBody, "review": reviewBody} {
		_, err := db.InsertUserSkill(ctx, database.InsertUserSkillParams{
			ID:          uuid.New(),
			UserID:      user.ID,
			Name:        name,
			Description: name,
			Content:     "---\nname: " + name + "\ndescription: " + name + "\n---\n\n" + body + "\n",
		})
		require.NoError(t, err)
	}
	chat, err := server.CreateChat(ctx, CreateOptions{
		OrganizationID:     org.ID,
		OwnerID:            user.ID,
		Title:              "pins",
		ModelConfigID:      model.ID,
		InitialUserContent: []codersdk.ChatMessagePart{codersdk.ChatMessageText("hello")},
	})
	require.NoError(t, err)
	fdb, err := forkdb.From(db)
	require.NoError(t, err)

	// pin replaces the pins of the chat with personal skills, the way the
	// pinned skills route does.
	pin := func(t *testing.T, names ...string) {
		t.Helper()
		require.NoError(t, fdb.DeleteChatPinnedSkills(ctx, chat.ID))
		params := forkdb.InsertChatPinnedSkillsParams{ChatID: chat.ID}
		for _, name := range names {
			params.SkillSources = append(params.SkillSources, string(codersdk.ChatSkillSourcePersonal))
			params.SkillNames = append(params.SkillNames, name)
		}
		require.NoError(t, fdb.InsertChatPinnedSkills(ctx, params))
	}
	// insert appends a model-only user row, compressed for the summary that
	// compaction writes, which drops every row before it from the prompt.
	insert := func(t *testing.T, content string, compressed bool) {
		t.Helper()
		_, err := db.InsertChatMessages(ctx, database.InsertChatMessagesParams{
			ChatID:              chat.ID,
			CreatedBy:           []uuid.UUID{user.ID},
			ModelConfigID:       []uuid.UUID{model.ID},
			ReasoningEffort:     []string{""},
			Role:                []database.ChatMessageRole{database.ChatMessageRoleUser},
			Content:             []string{content},
			ContentVersion:      []int16{chatprompt.CurrentContentVersion},
			Visibility:          []database.ChatMessageVisibility{database.ChatMessageVisibilityModel},
			InputTokens:         []int64{0},
			OutputTokens:        []int64{0},
			TotalTokens:         []int64{0},
			ReasoningTokens:     []int64{0},
			CacheCreationTokens: []int64{0},
			CacheReadTokens:     []int64{0},
			ContextLimit:        []int64{0},
			Compressed:          []bool{compressed},
			RuntimeMs:           []int64{0},
		})
		require.NoError(t, err)
	}
	// syncPins returns the text of a due message and stores it, the way
	// syncForkContext commits it before the step runs.
	syncPins := func(t *testing.T) (string, bool) {
		t.Helper()
		message, ok, err := server.pinnedSkillsMessage(ctx, chat)
		require.NoError(t, err)
		if !ok {
			return "", false
		}
		require.Equal(t, database.ChatMessageRoleUser, message.Role)
		require.Equal(t, database.ChatMessageVisibilityModel, message.Visibility)
		insert(t, string(message.Content.RawMessage), false)
		texts := memoryIndexTexts([]database.ChatMessage{{
			Role:           message.Role,
			Visibility:     message.Visibility,
			Content:        message.Content,
			ContentVersion: message.ContentVersion,
		}})
		require.Len(t, texts, 1)
		require.True(t, strings.HasPrefix(texts[0], pinnedSkillsHeader+"\n"))
		return texts[0], true
	}
	summary, err := chatprompt.MarshalParts([]codersdk.ChatMessagePart{codersdk.ChatMessageText("Compacted summary.")})
	require.NoError(t, err)

	// A chat that never pinned a skill gets nothing.
	_, ok := syncPins(t)
	require.False(t, ok)

	// An unknown name is skipped, and the known skill's body is sent once.
	pin(t, "deploy", "missing")
	text, ok := syncPins(t)
	require.True(t, ok)
	require.Contains(t, text, "<pinned-skill name=\"personal/deploy\">\n"+deployBody+"\n</pinned-skill>")
	require.NotContains(t, text, "missing")
	_, ok = syncPins(t)
	require.False(t, ok, "the prompt already shows the pins")

	// Compaction drops the message from the prompt, so the next step sends
	// the body again.
	insert(t, string(summary.RawMessage), true)
	text, ok = syncPins(t)
	require.True(t, ok)
	require.Contains(t, text, deployBody)

	// An unpinned skill leaves the list.
	pin(t, "review")
	text, ok = syncPins(t)
	require.True(t, ok)
	require.Contains(t, text, reviewBody)
	require.NotContains(t, text, deployBody)

	// Unpinning every skill tells the model the earlier list is gone, once.
	pin(t)
	text, ok = syncPins(t)
	require.True(t, ok)
	require.True(t, strings.HasSuffix(text, "\n"+pinnedSkillsNone))
	require.NotContains(t, text, reviewBody)
	_, ok = syncPins(t)
	require.False(t, ok)
}
