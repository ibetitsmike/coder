package dbcrypt_test

// This file is fork code: it checks that the key commands handle
// chat_secret_answers and plugins, whose key references would otherwise
// block revoking the key.

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtime"
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
		require.NoError(t, fdb.InsertChatSecretAnswer(ctx, forkdb.InsertChatSecretAnswerParams{
			ChatID: chat.ID, ToolCallID: fmt.Sprintf("call_%d", i), Value: value,
		}))
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

// insertPlugins stores n plugins with config through store, so their configs
// are encrypted when store is a dbcrypt store, and returns their
// organization's ID.
func insertPlugins(t *testing.T, rawDB, store database.Store, config string, n int) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	org := dbgen.Organization(t, rawDB, database.Organization{})
	owner := dbgen.User(t, rawDB, database.User{})
	file := dbgen.File(t, rawDB, database.File{CreatedBy: owner.ID})
	fdb, err := forkdb.From(store)
	require.NoError(t, err)
	for i := range n {
		_, err := fdb.InsertPlugin(ctx, forkdb.InsertPluginParams{
			ID:             uuid.New(),
			OrganizationID: org.ID,
			Name:           fmt.Sprintf("plugin-%d", i),
			FileID:         file.ID,
			Manifest:       []byte("{}"),
			Grants:         []byte("{}"),
			Config:         config,
			Enabled:        true,
			CreatedBy:      owner.ID,
			CreatedAt:      dbtime.Now(),
			UpdatedAt:      dbtime.Now(),
		})
		require.NoError(t, err)
	}
	return org.ID
}

func rawPlugins(t *testing.T, rawDB database.Store, orgID uuid.UUID) []forkdb.Plugin {
	t.Helper()
	fdb, err := forkdb.From(rawDB)
	require.NoError(t, err)
	plugins, err := fdb.GetPluginsByOrganizationID(t.Context(), orgID)
	require.NoError(t, err)
	return plugins
}

func TestPluginConfigsKeyCommands(t *testing.T) {
	t.Parallel()
	const config = `{"token":"plugin-secret"}`

	t.Run("Rotate", func(t *testing.T) {
		t.Parallel()
		f := newRotateFixture(t)
		// More plugins than one page of the key commands.
		const count = 101
		orgID := insertPlugins(t, f.rawDB, f.cryptDBA, config, count)
		blankOrgID := insertPlugins(t, f.rawDB, f.cryptDBA, "", 1)
		before := rawPlugins(t, f.rawDB, orgID)

		f.rotate(t)

		plugins := rawPlugins(t, f.rawDB, orgID)
		require.Len(t, plugins, count)
		for i, plugin := range plugins {
			require.Equal(t, f.cipherB.HexDigest(), plugin.ConfigKeyID.String)
			require.Equal(t, config, decryptRawString(t, f.cipherB, plugin.Config))
			// An unchanged config must not restart the plugin.
			require.True(t, before[i].UpdatedAt.Equal(plugin.UpdatedAt))
		}
		blank := rawPlugins(t, f.rawDB, blankOrgID)
		require.Len(t, blank, 1)
		require.False(t, blank[0].ConfigKeyID.Valid)
		require.Empty(t, blank[0].Config)
	})

	t.Run("Decrypt", func(t *testing.T) {
		t.Parallel()
		f := newDecryptFixture(t)
		orgID := insertPlugins(t, f.rawDB, f.cryptDBA, config, 1)

		f.decrypt(t)

		plugins := rawPlugins(t, f.rawDB, orgID)
		require.Len(t, plugins, 1)
		require.False(t, plugins[0].ConfigKeyID.Valid)
		require.Equal(t, config, plugins[0].Config)
	})

	t.Run("Delete", func(t *testing.T) {
		t.Parallel()
		f := newDeleteFixture(t)
		encryptedOrgID := insertPlugins(t, f.rawDB, f.cryptDBA, config, 1)
		plainOrgID := insertPlugins(t, f.rawDB, f.rawDB, config, 1)
		before := rawPlugins(t, f.rawDB, encryptedOrgID)

		f.delete(t)

		requireAllKeysRevoked(f.ctx, t, f.rawDB)
		encrypted := rawPlugins(t, f.rawDB, encryptedOrgID)
		require.Len(t, encrypted, 1)
		require.False(t, encrypted[0].ConfigKeyID.Valid)
		require.Empty(t, encrypted[0].Config)
		// A changed config restarts the plugin.
		require.True(t, encrypted[0].UpdatedAt.After(before[0].UpdatedAt))
		plain := rawPlugins(t, f.rawDB, plainOrgID)
		require.Len(t, plain, 1)
		require.Equal(t, config, plain[0].Config)
	})
}
