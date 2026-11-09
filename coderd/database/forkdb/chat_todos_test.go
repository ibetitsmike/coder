package forkdb_test

import (
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/testutil"
)

func TestUpsertChatTodos(t *testing.T) {
	t.Parallel()

	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitShort)

	chat := dbgen.Chat(t, db, database.Chat{
		OrganizationID:    dbgen.Organization(t, db, database.Organization{}).ID,
		OwnerID:           dbgen.User(t, db, database.User{}).ID,
		LastModelConfigID: dbgen.ChatModelConfig(t, db, database.ChatModelConfig{}).ID,
	})

	_, err := fdb.GetChatTodosByChatID(ctx, chat.ID)
	require.ErrorIs(t, err, sql.ErrNoRows, "a chat starts without a todo list")

	first, err := fdb.UpsertChatTodos(ctx, forkdb.UpsertChatTodosParams{
		ChatID:    chat.ID,
		Items:     json.RawMessage(`[{"id": "a", "content": "plan", "status": "in_progress"}]`),
		UpdatedAt: dbtime.Now(),
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, first.Version)

	second, err := fdb.UpsertChatTodos(ctx, forkdb.UpsertChatTodosParams{
		ChatID:    chat.ID,
		Items:     json.RawMessage(`[{"id": "b", "content": "ship", "status": "pending"}]`),
		UpdatedAt: dbtime.Now(),
	})
	require.NoError(t, err)
	require.EqualValues(t, 2, second.Version)

	got, err := fdb.GetChatTodosByChatID(ctx, chat.ID)
	require.NoError(t, err)
	require.EqualValues(t, 2, got.Version)
	require.JSONEq(t, `[{"id": "b", "content": "ship", "status": "pending"}]`, string(got.Items), "a write replaces the whole list")

	_, err = sqlDB.ExecContext(ctx, "DELETE FROM chats WHERE id = $1", chat.ID)
	require.NoError(t, err)
	var count int
	err = sqlDB.QueryRowContext(ctx, "SELECT count(*) FROM chatd.chat_todos WHERE chat_id = $1", chat.ID).Scan(&count)
	require.NoError(t, err)
	require.Zero(t, count, "deleting the chat deletes its todo list")
}
