package coderd

// This file is fork code: it covers the secret answer replacement of the
// fork-nested-subagents experiment.

import (
	"encoding/json"
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

func TestReplaceUnreportedSecretAnswer(t *testing.T) {
	t.Parallel()

	db, _ := dbtestutil.NewDB(t)
	ctx := testutil.Context(t, testutil.WaitShort)
	fdb, err := forkdb.From(db)
	require.NoError(t, err)
	model := dbgen.ChatModelConfig(t, db, database.ChatModelConfig{})
	chat := dbgen.Chat(t, db, database.Chat{
		OrganizationID:    dbgen.Organization(t, db, database.Organization{}).ID,
		OwnerID:           dbgen.User(t, db, database.User{}).ID,
		LastModelConfigID: model.ID,
	})
	_, err = fdb.UpsertChatDescendantQuestion(ctx, forkdb.UpsertChatDescendantQuestionParams{
		ChatID:     chat.ID,
		ToolCallID: "call-secret",
		Questions:  json.RawMessage(`[{"header": "Token", "secret": true}]`),
	})
	require.NoError(t, err)
	answer := func(value string) forkdb.InsertChatSecretAnswerParams {
		return forkdb.InsertChatSecretAnswerParams{ChatID: chat.ID, ToolCallID: "call-secret", Value: value}
	}
	require.NoError(t, fdb.InsertChatSecretAnswer(ctx, answer("first-value")))

	reportQueued, err := replaceUnreportedSecretAnswer(ctx, db, answer("second-value"))
	require.NoError(t, err, "no message reports the answer, so a new answer replaces it")
	require.False(t, reportQueued)
	dbgen.ChatMessage(t, db, database.ChatMessage{
		ChatID:        chat.ID,
		ModelConfigID: uuid.NullUUID{UUID: model.ID, Valid: true},
		Role:          database.ChatMessageRoleUser,
		Visibility:    database.ChatMessageVisibilityBoth,
		Content: pqtype.NullRawMessage{
			RawMessage: json.RawMessage(`[{"type": "text", "text": "Answer to ask_user_question call call-secret:\nThe user gave a secret answer."}]`),
			Valid:      true,
		},
	})
	_, err = replaceUnreportedSecretAnswer(ctx, db, answer("third-value"))
	require.ErrorIs(t, err, errSecretAnswerReported, "a delivered message reports the answer")
	answers, err := fdb.GetChatSecretAnswersByChatID(ctx, chat.ID)
	require.NoError(t, err)
	require.Len(t, answers, 1)
	require.Equal(t, "second-value", answers[0].Value)
}
