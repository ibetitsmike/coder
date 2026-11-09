package forkdb_test

import (
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
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
	message := func(role database.ChatMessageRole) {
		dbgen.ChatMessage(t, db, database.ChatMessage{
			ChatID:        chat.ID,
			ModelConfigID: uuid.NullUUID{UUID: model.ID, Valid: true},
			Role:          role,
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

	message(database.ChatMessageRoleUser)
	_, ok := pending()
	require.False(t, ok, "a chat starts without a question")

	ask("call-1", `[{"header": "DB"}]`)
	message(database.ChatMessageRoleAssistant)
	got, ok := pending()
	require.True(t, ok, "only a user message answers the question")
	require.Equal(t, "call-1", got.ToolCallID)

	message(database.ChatMessageRoleUser)
	_, ok = pending()
	require.False(t, ok, "a later user message answers the question")

	ask("call-2", `[{"header": "API"}]`)
	got, ok = pending()
	require.True(t, ok, "asking again after the answer makes a new question pending")
	require.Equal(t, "call-2", got.ToolCallID)
	require.JSONEq(t, `[{"header": "API"}]`, string(got.Questions))

	_, err := sqlDB.ExecContext(ctx, "DELETE FROM chats WHERE id = $1", chat.ID)
	require.NoError(t, err)
	var count int
	err = sqlDB.QueryRowContext(ctx, "SELECT count(*) FROM chatd.chat_descendant_questions WHERE chat_id = $1", chat.ID).Scan(&count)
	require.NoError(t, err)
	require.Zero(t, count, "deleting the chat deletes its question")
}
