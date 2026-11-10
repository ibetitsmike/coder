package forkdb_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/testutil"
)

func TestDeleteChatSecretAnswer(t *testing.T) {
	t.Parallel()

	db, _ := dbtestutil.NewDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitShort)

	model := dbgen.ChatModelConfig(t, db, database.ChatModelConfig{})
	chat := dbgen.Chat(t, db, database.Chat{
		OrganizationID:    dbgen.Organization(t, db, database.Organization{}).ID,
		OwnerID:           dbgen.User(t, db, database.User{}).ID,
		LastModelConfigID: model.ID,
	})
	insert := func(value string) time.Time {
		createdAt, err := fdb.InsertChatSecretAnswer(ctx, forkdb.InsertChatSecretAnswerParams{
			ChatID: chat.ID, ToolCallID: "call-1", Value: value,
		})
		require.NoError(t, err)
		return createdAt
	}
	remove := func(createdAt sql.NullTime) {
		require.NoError(t, fdb.DeleteChatSecretAnswer(ctx, forkdb.DeleteChatSecretAnswerParams{
			ChatID: chat.ID, ToolCallID: "call-1", CreatedAt: createdAt,
		}))
	}
	values := func() []string {
		answers, err := fdb.GetChatSecretAnswersByChatID(ctx, chat.ID)
		require.NoError(t, err)
		var got []string
		for _, answer := range answers {
			got = append(got, answer.Value)
		}
		return got
	}

	first := insert("first secret")
	remove(sql.NullTime{})
	require.Empty(t, values(), "a delete without created_at deletes any answer")

	second := insert("second secret")
	require.True(t, second.After(first))
	remove(sql.NullTime{Time: first, Valid: true})
	require.Equal(t, []string{"second secret"}, values(), "a replaced answer's delete keeps its replacement")

	remove(sql.NullTime{Time: second, Valid: true})
	require.Empty(t, values())
}
