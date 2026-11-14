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

func TestRetireChatDescendantQuestion(t *testing.T) {
	t.Parallel()

	const (
		plain  = `[{"header": "DB"}]`
		hidden = `[{"header": "Token", "secret": true}]`
	)
	for _, tc := range []struct {
		name       string
		questions  string
		arg        forkdb.RetireChatDescendantQuestionParams
		answeredBy string // empty: the question stays pending
	}{
		{name: "VisibleMessage", questions: plain, arg: forkdb.RetireChatDescendantQuestionParams{Visible: true}, answeredBy: "message"},
		{name: "PrefixedAnswer", questions: plain, arg: forkdb.RetireChatDescendantQuestionParams{Visible: true, Answers: []string{"call-1:\nPostgres"}}, answeredBy: "message"},
		{name: "ModelOnlyMessage", questions: plain},
		{name: "ContextBoundary", questions: plain, arg: forkdb.RetireChatDescendantQuestionParams{Compressed: true}, answeredBy: "context_boundary"},
		{name: "SecretVisibleMessage", questions: hidden, arg: forkdb.RetireChatDescendantQuestionParams{Visible: true}},
		{name: "SecretOtherCallAnswer", questions: hidden, arg: forkdb.RetireChatDescendantQuestionParams{Visible: true, Answers: []string{"call-other:\nThe user gave a secret answer."}}},
		{name: "SecretAnswer", questions: hidden, arg: forkdb.RetireChatDescendantQuestionParams{Visible: true, Answers: []string{"call-1:\nThe user gave a secret answer."}}, answeredBy: "secret_answer"},
		{name: "SecretContextBoundary", questions: hidden, arg: forkdb.RetireChatDescendantQuestionParams{Compressed: true}, answeredBy: "context_boundary"},
		// A question that is not an array fails closed like Secret.
		{name: "MalformedVisibleMessage", questions: `{"header": "DB"}`, arg: forkdb.RetireChatDescendantQuestionParams{Visible: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
			_, err := fdb.UpsertChatDescendantQuestion(ctx, forkdb.UpsertChatDescendantQuestionParams{
				ChatID:     chat.ID,
				ToolCallID: "call-1",
				Questions:  json.RawMessage(tc.questions),
			})
			require.NoError(t, err)

			arg := tc.arg
			arg.ChatID = chat.ID
			n, err := fdb.RetireChatDescendantQuestion(ctx, arg)
			require.NoError(t, err)
			q, err := fdb.GetPendingChatDescendantQuestion(ctx, chat.ID)
			if tc.answeredBy == "" {
				require.Zero(t, n)
				require.NoError(t, err, "the question stays pending")
				require.False(t, q.AnsweredAt.Valid)
				return
			}
			require.EqualValues(t, 1, n)
			require.ErrorIs(t, err, sql.ErrNoRows, "the question is answered")
			var (
				answeredAt sql.NullTime
				answeredBy sql.NullString
			)
			err = sqlDB.QueryRowContext(ctx, "SELECT answered_at, answered_by FROM chatd.chat_descendant_questions WHERE chat_id = $1", chat.ID).
				Scan(&answeredAt, &answeredBy)
			require.NoError(t, err)
			require.True(t, answeredAt.Valid)
			require.Equal(t, tc.answeredBy, answeredBy.String)

			// A later event leaves the answer as it is.
			n, err = fdb.RetireChatDescendantQuestion(ctx, forkdb.RetireChatDescendantQuestionParams{ChatID: chat.ID, Compressed: true})
			require.NoError(t, err)
			require.Zero(t, n)

			// A new question of the chat is pending again.
			_, err = fdb.UpsertChatDescendantQuestion(ctx, forkdb.UpsertChatDescendantQuestionParams{
				ChatID:     chat.ID,
				ToolCallID: "call-2",
				Questions:  json.RawMessage(plain),
			})
			require.NoError(t, err)
			q, err = fdb.GetPendingChatDescendantQuestion(ctx, chat.ID)
			require.NoError(t, err)
			require.Equal(t, "call-2", q.ToolCallID)
			require.False(t, q.AnsweredAt.Valid)
			require.False(t, q.AnsweredBy.Valid)
		})
	}
}

func TestChatDescendantQuestionDeletedWithChat(t *testing.T) {
	t.Parallel()

	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitShort)

	orgID := dbgen.Organization(t, db, database.Organization{}).ID
	ownerID := dbgen.User(t, db, database.User{}).ID
	model := dbgen.ChatModelConfig(t, db, database.ChatModelConfig{})
	root := dbgen.Chat(t, db, database.Chat{OrganizationID: orgID, OwnerID: ownerID, LastModelConfigID: model.ID})
	child := dbgen.Chat(t, db, database.Chat{
		OrganizationID:    orgID,
		OwnerID:           ownerID,
		LastModelConfigID: model.ID,
		ParentChatID:      uuid.NullUUID{UUID: root.ID, Valid: true},
		RootChatID:        uuid.NullUUID{UUID: root.ID, Valid: true},
	})
	_, err := fdb.UpsertChatDescendantQuestion(ctx, forkdb.UpsertChatDescendantQuestionParams{
		ChatID:     child.ID,
		ToolCallID: "call-1",
		Questions:  json.RawMessage(`[{"header": "DB"}]`),
	})
	require.NoError(t, err)

	// Deleting the root, as dbpurge can before a descendant, keeps the
	// descendant's pending question.
	_, err = sqlDB.ExecContext(ctx, "DELETE FROM chats WHERE id = $1", root.ID)
	require.NoError(t, err)
	q, err := fdb.GetPendingChatDescendantQuestion(ctx, child.ID)
	require.NoError(t, err)
	require.Equal(t, "call-1", q.ToolCallID)

	// Deleting the chat deletes its question.
	_, err = sqlDB.ExecContext(ctx, "DELETE FROM chats WHERE id = $1", child.ID)
	require.NoError(t, err)
	var count int
	err = sqlDB.QueryRowContext(ctx, "SELECT count(*) FROM chatd.chat_descendant_questions WHERE chat_id = $1", child.ID).Scan(&count)
	require.NoError(t, err)
	require.Zero(t, count)
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
	otherRoot := chat(nil)
	otherChild := chat(&otherRoot)
	emptyRoot := chat(nil)
	_ = chat(&emptyRoot)

	ask(root, "root")
	ask(grandchild, "grandchild")
	ask(child, "child")
	ask(answered, "answered")
	_, err := fdb.RetireChatDescendantQuestion(ctx, forkdb.RetireChatDescendantQuestionParams{ChatID: answered.ID, Visible: true})
	require.NoError(t, err)
	ask(archived, "archived")
	_, err = sqlDB.ExecContext(ctx, "UPDATE chats SET archived = true WHERE id = $1", archived.ID)
	require.NoError(t, err)
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
