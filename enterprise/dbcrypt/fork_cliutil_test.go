package dbcrypt_test

// This file is fork code: it checks that the key commands handle
// chat_secret_answers, whose key reference would otherwise block revoking
// the key.

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/forkdb"
)

// insertChatSecretAnswers stores n answers with value through store, so they
// are encrypted when store is a dbcrypt store, and returns the chat's ID.
func insertChatSecretAnswers(t *testing.T, rawDB, store database.Store, value string, n int) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	org, err := rawDB.GetDefaultOrganization(ctx)
	require.NoError(t, err)
	owner := dbgen.User(t, rawDB, database.User{})
	model := dbgen.ChatModelConfig(t, rawDB, database.ChatModelConfig{OrganizationID: org.ID})
	chat := dbgen.Chat(t, rawDB, database.Chat{OrganizationID: org.ID, OwnerID: owner.ID, LastModelConfigID: model.ID})
	fdb, err := forkdb.From(store)
	require.NoError(t, err)
	for i := range n {
		_, err := fdb.InsertChatSecretAnswer(ctx, forkdb.InsertChatSecretAnswerParams{
			ChatID: chat.ID, ToolCallID: fmt.Sprintf("call_%d", i), Value: value,
		})
		require.NoError(t, err)
	}
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
		// More answers than one page of the key commands.
		const count = 101
		chatID := insertChatSecretAnswers(t, f.rawDB, f.cryptDBA, value, count)

		f.rotate(t)

		answers := rawChatSecretAnswers(t, f.rawDB, chatID)
		require.Len(t, answers, count)
		for _, answer := range answers {
			require.Equal(t, f.cipherB.HexDigest(), answer.ValueKeyID.String)
			require.Equal(t, value, decryptRawString(t, f.cipherB, answer.Value))
		}
	})

	t.Run("Decrypt", func(t *testing.T) {
		t.Parallel()
		f := newDecryptFixture(t)
		chatID := insertChatSecretAnswers(t, f.rawDB, f.cryptDBA, value, 1)

		f.decrypt(t)

		answers := rawChatSecretAnswers(t, f.rawDB, chatID)
		require.Len(t, answers, 1)
		require.False(t, answers[0].ValueKeyID.Valid)
		require.Equal(t, value, answers[0].Value)
	})

	t.Run("Delete", func(t *testing.T) {
		t.Parallel()
		f := newDeleteFixture(t)
		encryptedChatID := insertChatSecretAnswers(t, f.rawDB, f.cryptDBA, value, 1)
		plainChatID := insertChatSecretAnswers(t, f.rawDB, f.rawDB, value, 1)

		f.delete(t)

		require.Empty(t, rawChatSecretAnswers(t, f.rawDB, encryptedChatID))
		require.Len(t, rawChatSecretAnswers(t, f.rawDB, plainChatID), 1)
	})
}
