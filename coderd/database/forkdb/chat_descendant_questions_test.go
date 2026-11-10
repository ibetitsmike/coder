package forkdb_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/sqlc-dev/pqtype"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/testutil"
)

func TestGetPendingChatDescendantQuestion(t *testing.T) {
	t.Parallel()

	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitShort)

	model := dbgen.ChatModelConfig(t, db, database.ChatModelConfig{})
	chat := dbgen.Chat(t, db, database.Chat{
		OrganizationID:    dbgen.Organization(t, db, database.Organization{}).ID,
		OwnerID:           dbgen.User(t, db, database.User{}).ID,
		LastModelConfigID: model.ID,
	})
	message := func(role database.ChatMessageRole, visibility database.ChatMessageVisibility) {
		dbgen.ChatMessage(t, db, database.ChatMessage{
			ChatID:        chat.ID,
			ModelConfigID: uuid.NullUUID{UUID: model.ID, Valid: true},
			Role:          role,
			Visibility:    visibility,
		})
	}
	ask := func(callID, questions string) {
		_, err := fdb.UpsertChatDescendantQuestion(ctx, forkdb.UpsertChatDescendantQuestionParams{
			ChatID:     chat.ID,
			ToolCallID: callID,
			Questions:  json.RawMessage(questions),
		})
		require.NoError(t, err)
	}
	pending := func() (forkdb.ChatDescendantQuestion, bool) {
		q, err := fdb.GetPendingChatDescendantQuestion(ctx, chat.ID)
		if err == nil {
			return q, true
		}
		require.ErrorIs(t, err, sql.ErrNoRows)
		return q, false
	}

	message(database.ChatMessageRoleUser, database.ChatMessageVisibilityBoth)
	_, ok := pending()
	require.False(t, ok, "a chat starts without a question")

	ask("call-1", `[{"header": "DB"}]`)
	message(database.ChatMessageRoleAssistant, database.ChatMessageVisibilityBoth)
	// Lifecycle hooks add model-only user rows, which the user never wrote.
	message(database.ChatMessageRoleUser, database.ChatMessageVisibilityModel)
	got, ok := pending()
	require.True(t, ok, "only a user message the user sees answers the question")
	require.Equal(t, "call-1", got.ToolCallID)

	message(database.ChatMessageRoleUser, database.ChatMessageVisibilityBoth)
	_, ok = pending()
	require.False(t, ok, "a later user message answers the question")

	ask("call-2", `[{"header": "API"}]`)
	got, ok = pending()
	require.True(t, ok, "asking again after the answer makes a new question pending")
	require.Equal(t, "call-2", got.ToolCallID)
	require.JSONEq(t, `[{"header": "API"}]`, string(got.Questions))

	dbgen.ChatMessage(t, db, database.ChatMessage{
		ChatID:        chat.ID,
		ModelConfigID: uuid.NullUUID{UUID: model.ID, Valid: true},
		Role:          database.ChatMessageRoleUser,
		Visibility:    database.ChatMessageVisibilityModel,
		Compressed:    true,
	})
	_, ok = pending()
	require.False(t, ok, "a context boundary, such as a clear, retires the question")

	text := func(text string) {
		dbgen.ChatMessage(t, db, database.ChatMessage{
			ChatID:        chat.ID,
			ModelConfigID: uuid.NullUUID{UUID: model.ID, Valid: true},
			Role:          database.ChatMessageRoleUser,
			Visibility:    database.ChatMessageVisibilityBoth,
			Content: pqtype.NullRawMessage{
				RawMessage: json.RawMessage(fmt.Sprintf(`[{"type": "text", "text": %q}]`, text)),
				Valid:      true,
			},
		})
	}
	ask("call-secret", `[{"header": "DB"}, {"header": "Token", "secret": true}]`)
	// The turn's end promotes a message the user queued before the question.
	text("Also check the staging database.")
	text("Answer to ask_user_question call call-other:\nThe user gave a secret answer.")
	got, ok = pending()
	require.True(t, ok, "only the secret answer's message answers a secret question")
	require.Equal(t, "call-secret", got.ToolCallID)
	text("Answer to ask_user_question call call-secret:\nThe user gave a secret answer.")
	_, ok = pending()
	require.False(t, ok, "the secret answer's message answers the secret question")

	_, err := sqlDB.ExecContext(ctx, "DELETE FROM chats WHERE id = $1", chat.ID)
	require.NoError(t, err)
	var count int
	err = sqlDB.QueryRowContext(ctx, "SELECT count(*) FROM chatd.chat_descendant_questions WHERE chat_id = $1", chat.ID).Scan(&count)
	require.NoError(t, err)
	require.Zero(t, count, "deleting the chat deletes its question")
}

func TestListPendingChatDescendantQuestionsByRoot(t *testing.T) {
	t.Parallel()

	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitShort)

	orgID := dbgen.Organization(t, db, database.Organization{}).ID
	ownerID := dbgen.User(t, db, database.User{}).ID
	model := dbgen.ChatModelConfig(t, db, database.ChatModelConfig{})
	chat := func(parent *database.Chat) database.Chat {
		seed := database.Chat{OrganizationID: orgID, OwnerID: ownerID, LastModelConfigID: model.ID}
		if parent != nil {
			seed.ParentChatID = uuid.NullUUID{UUID: parent.ID, Valid: true}
			seed.RootChatID = parent.RootChatID
			if !parent.RootChatID.Valid {
				seed.RootChatID = seed.ParentChatID
			}
		}
		return dbgen.Chat(t, db, seed)
	}
	ask := func(c database.Chat, callID string) {
		_, err := fdb.UpsertChatDescendantQuestion(ctx, forkdb.UpsertChatDescendantQuestionParams{
			ChatID:     c.ID,
			ToolCallID: callID,
			Questions:  json.RawMessage(`[{"header": "DB"}]`),
		})
		require.NoError(t, err)
	}

	root := chat(nil)
	child := chat(&root)
	grandchild := chat(&child)
	answered := chat(&root)
	archived := chat(&root)
	cleared := chat(&root)
	otherRoot := chat(nil)
	otherChild := chat(&otherRoot)
	emptyRoot := chat(nil)
	_ = chat(&emptyRoot)

	ask(root, "root")
	ask(grandchild, "grandchild")
	ask(child, "child")
	// A model-only user row is not the user's answer.
	dbgen.ChatMessage(t, db, database.ChatMessage{
		ChatID:        child.ID,
		ModelConfigID: uuid.NullUUID{UUID: model.ID, Valid: true},
		Role:          database.ChatMessageRoleUser,
		Visibility:    database.ChatMessageVisibilityModel,
	})
	ask(answered, "answered")
	dbgen.ChatMessage(t, db, database.ChatMessage{
		ChatID:        answered.ID,
		ModelConfigID: uuid.NullUUID{UUID: model.ID, Valid: true},
		Role:          database.ChatMessageRoleUser,
	})
	ask(archived, "archived")
	_, err := sqlDB.ExecContext(ctx, "UPDATE chats SET archived = true WHERE id = $1", archived.ID)
	require.NoError(t, err)
	ask(cleared, "cleared")
	dbgen.ChatMessage(t, db, database.ChatMessage{
		ChatID:        cleared.ID,
		ModelConfigID: uuid.NullUUID{UUID: model.ID, Valid: true},
		Role:          database.ChatMessageRoleUser,
		Visibility:    database.ChatMessageVisibilityModel,
		Compressed:    true,
	})
	ask(otherChild, "other")

	rows, err := fdb.ListPendingChatDescendantQuestionsByRoot(ctx, root.ID)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "grandchild", rows[0].ToolCallID, "oldest question first")
	require.Equal(t, grandchild.Title, rows[0].ChatTitle)
	require.Equal(t, child.ID, rows[0].ParentChatID.UUID)
	require.Equal(t, "child", rows[1].ToolCallID)
	require.Equal(t, root.ID, rows[1].ParentChatID.UUID)
	require.JSONEq(t, `[{"header": "DB"}]`, string(rows[1].Questions))

	rows, err = fdb.ListPendingChatDescendantQuestionsByRoot(ctx, emptyRoot.ID)
	require.NoError(t, err)
	require.Empty(t, rows)
}
