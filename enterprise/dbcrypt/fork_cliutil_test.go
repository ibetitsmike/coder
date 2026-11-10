package dbcrypt_test

// This file is fork code: it checks that the key commands handle
// chat_secret_answers, whose key reference would otherwise block revoking
// the key.

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/forkdb"
)

// insertChatSecretAnswer stores value through store, so it is encrypted
// when store is a dbcrypt store, and returns the chat's ID.
func insertChatSecretAnswer(t *testing.T, rawDB, store database.Store, value string) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	org, err := rawDB.GetDefaultOrganization(ctx)
	require.NoError(t, err)
	owner := dbgen.User(t, rawDB, database.User{})
	model := dbgen.ChatModelConfig(t, rawDB, database.ChatModelConfig{OrganizationID: org.ID})
	chat := dbgen.Chat(t, rawDB, database.Chat{OrganizationID: org.ID, OwnerID: owner.ID, LastModelConfigID: model.ID})
	fdb, err := forkdb.From(store)
	require.NoError(t, err)
	require.NoError(t, fdb.InsertChatSecretAnswer(ctx, forkdb.InsertChatSecretAnswerParams{
		ChatID: chat.ID, ToolCallID: "call_1", Value: value,
	}))
	return chat.ID
}

func rawChatSecretAnswers(t *testing.T, rawDB database.Store, chatID uuid.UUID) []forkdb.ChatSecretAnswer {
	t.Helper()
	fdb, err := forkdb.From(rawDB)
	require.NoError(t, err)
	answers, err := fdb.GetChatSecretAnswersByChatID(t.Context(), chatID)
	require.NoError(t, err)
	return answers
}

func TestChatSecretAnswersKeyCommands(t *testing.T) {
	t.Parallel()
	const value = "chat-secret-answer"

	t.Run("Rotate", func(t *testing.T) {
		t.Parallel()
		f := newRotateFixture(t)
		chatID := insertChatSecretAnswer(t, f.rawDB, f.cryptDBA, value)

		f.rotate(t)

		answers := rawChatSecretAnswers(t, f.rawDB, chatID)
		require.Len(t, answers, 1)
		require.Equal(t, f.cipherB.HexDigest(), answers[0].ValueKeyID.String)
		require.Equal(t, value, decryptRawString(t, f.cipherB, answers[0].Value))
	})

	t.Run("Decrypt", func(t *testing.T) {
		t.Parallel()
		f := newDecryptFixture(t)
		chatID := insertChatSecretAnswer(t, f.rawDB, f.cryptDBA, value)

		f.decrypt(t)

		answers := rawChatSecretAnswers(t, f.rawDB, chatID)
		require.Len(t, answers, 1)
		require.False(t, answers[0].ValueKeyID.Valid)
		require.Equal(t, value, answers[0].Value)
	})

	t.Run("Delete", func(t *testing.T) {
		t.Parallel()
		f := newDeleteFixture(t)
		encryptedChatID := insertChatSecretAnswer(t, f.rawDB, f.cryptDBA, value)
		plainChatID := insertChatSecretAnswer(t, f.rawDB, f.rawDB, value)

		f.delete(t)

		require.Empty(t, rawChatSecretAnswers(t, f.rawDB, encryptedChatID))
		require.Len(t, rawChatSecretAnswers(t, f.rawDB, plainChatID), 1)
	})
}
