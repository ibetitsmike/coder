package dbcrypt

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/database/forkdb"
)

// TestForkQueries checks that fork queries run through dbCrypt, which wraps
// the store in deployments with encryption keys.
func TestForkQueries(t *testing.T) {
	t.Parallel()

	_, crypt, _ := setup(t)
	fdb, err := forkdb.From(crypt)
	require.NoError(t, err)
	_, err = fdb.GetChatSubagentByChatID(context.Background(), uuid.New())
	require.ErrorIs(t, err, sql.ErrNoRows)
}

func TestPlugins(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const config = `{"token":"plugin-secret"}`

	// setupFork returns the fork queries of the raw store and of the
	// encrypting store.
	setupFork := func(t *testing.T) (database.Store, forkdb.Store, *dbCrypt, forkdb.Store, []Cipher) {
		t.Helper()
		db, crypt, ciphers := setup(t)
		raw, err := forkdb.From(db)
		require.NoError(t, err)
		fcrypt, err := forkdb.From(crypt)
		require.NoError(t, err)
		return db, raw, crypt, fcrypt, ciphers
	}
	insertPlugin := func(t *testing.T, db database.Store, crypt *dbCrypt, ciphers []Cipher) forkdb.Plugin {
		t.Helper()
		org := dbgen.Organization(t, db, database.Organization{})
		user := dbgen.User(t, db, database.User{})
		file := dbgen.File(t, db, database.File{CreatedBy: user.ID})
		plugin := dbgen.Plugin(t, crypt, forkdb.Plugin{
			OrganizationID: org.ID,
			FileID:         file.ID,
			CreatedBy:      user.ID,
			Config:         config,
			Enabled:        true,
		})
		requirePluginDecrypted(t, plugin, ciphers, config)
		return plugin
	}
	requireRawEncrypted := func(t *testing.T, raw forkdb.Store, id uuid.UUID, ciphers []Cipher, want string) {
		t.Helper()
		got, err := raw.GetPluginByID(ctx, id)
		require.NoError(t, err)
		require.Equal(t, ciphers[0].HexDigest(), got.ConfigKeyID.String)
		requireEncryptedEquals(t, ciphers[0], got.Config, want)
	}

	t.Run("InsertPlugin", func(t *testing.T) {
		t.Parallel()
		db, raw, crypt, _, ciphers := setupFork(t)
		plugin := insertPlugin(t, db, crypt, ciphers)
		requireRawEncrypted(t, raw, plugin.ID, ciphers, config)
	})

	t.Run("InsertPluginEmptyConfig", func(t *testing.T) {
		t.Parallel()
		db, raw, crypt, _, _ := setupFork(t)
		org := dbgen.Organization(t, db, database.Organization{})
		user := dbgen.User(t, db, database.User{})
		file := dbgen.File(t, db, database.File{CreatedBy: user.ID})
		plugin := dbgen.Plugin(t, crypt, forkdb.Plugin{OrganizationID: org.ID, FileID: file.ID, CreatedBy: user.ID})
		got, err := raw.GetPluginByID(ctx, plugin.ID)
		require.NoError(t, err)
		require.Empty(t, got.Config)
		require.False(t, got.ConfigKeyID.Valid)
	})

	t.Run("GetPluginByID", func(t *testing.T) {
		t.Parallel()
		db, _, crypt, fcrypt, ciphers := setupFork(t)
		plugin := insertPlugin(t, db, crypt, ciphers)
		got, err := fcrypt.GetPluginByID(ctx, plugin.ID)
		require.NoError(t, err)
		requirePluginDecrypted(t, got, ciphers, config)
	})

	t.Run("GetPluginsByOrganizationID", func(t *testing.T) {
		t.Parallel()
		db, _, crypt, fcrypt, ciphers := setupFork(t)
		plugin := insertPlugin(t, db, crypt, ciphers)
		got, err := fcrypt.GetPluginsByOrganizationID(ctx, plugin.OrganizationID)
		require.NoError(t, err)
		require.Len(t, got, 1)
		requirePluginDecrypted(t, got[0], ciphers, config)
	})

	t.Run("AcquirePluginLeases", func(t *testing.T) {
		t.Parallel()
		db, _, crypt, fcrypt, ciphers := setupFork(t)
		plugin := insertPlugin(t, db, crypt, ciphers)
		got, err := fcrypt.AcquirePluginLeases(ctx, forkdb.AcquirePluginLeasesParams{
			RunnerID:     uuid.New(),
			LeaseSeconds: 30,
			MaxPlugins:   10,
		})
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.Equal(t, plugin.ID, got[0].ID)
		requirePluginDecrypted(t, got[0], ciphers, config)
	})

	t.Run("UpdatePluginConfig", func(t *testing.T) {
		t.Parallel()
		db, raw, crypt, fcrypt, ciphers := setupFork(t)
		plugin := insertPlugin(t, db, crypt, ciphers)
		const updated = `{"token":"rotated-secret"}`
		got, err := fcrypt.UpdatePluginConfig(ctx, forkdb.UpdatePluginConfigParams{
			ID:        plugin.ID,
			Config:    updated,
			UpdatedAt: dbtime.Now(),
		})
		require.NoError(t, err)
		requirePluginDecrypted(t, got, ciphers, updated)
		requireRawEncrypted(t, raw, plugin.ID, ciphers, updated)
	})

	t.Run("UpdatePluginEnabled", func(t *testing.T) {
		t.Parallel()
		db, _, crypt, fcrypt, ciphers := setupFork(t)
		plugin := insertPlugin(t, db, crypt, ciphers)
		got, err := fcrypt.UpdatePluginEnabled(ctx, forkdb.UpdatePluginEnabledParams{
			ID:        plugin.ID,
			Enabled:   false,
			UpdatedAt: dbtime.Now(),
		})
		require.NoError(t, err)
		require.False(t, got.Enabled)
		requirePluginDecrypted(t, got, ciphers, config)
	})

	t.Run("InTx", func(t *testing.T) {
		t.Parallel()
		db, raw, crypt, _, ciphers := setupFork(t)
		plugin := insertPlugin(t, db, crypt, ciphers)
		const updated = `{"token":"tx-secret"}`
		err := forkdb.InTx(crypt, func(_ database.Store, ftx forkdb.Store) error {
			got, err := ftx.UpdatePluginConfig(ctx, forkdb.UpdatePluginConfigParams{
				ID:        plugin.ID,
				Config:    updated,
				UpdatedAt: dbtime.Now(),
			})
			if err != nil {
				return err
			}
			requirePluginDecrypted(t, got, ciphers, updated)
			return nil
		}, nil)
		require.NoError(t, err)
		requireRawEncrypted(t, raw, plugin.ID, ciphers, updated)
	})
}

func requirePluginDecrypted(t *testing.T, plugin forkdb.Plugin, ciphers []Cipher, want string) {
	t.Helper()
	require.Equal(t, want, plugin.Config)
	require.Equal(t, ciphers[0].HexDigest(), plugin.ConfigKeyID.String)
}

func TestChatSecretAnswers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const value = "chat-secret-answer"
	db, crypt, ciphers := setup(t)
	raw, err := forkdb.From(db)
	require.NoError(t, err)
	fcrypt, err := forkdb.From(crypt)
	require.NoError(t, err)

	org, err := db.GetDefaultOrganization(ctx)
	require.NoError(t, err)
	owner := dbgen.User(t, db, database.User{})
	model := dbgen.ChatModelConfig(t, db, database.ChatModelConfig{OrganizationID: org.ID})
	chat := dbgen.Chat(t, db, database.Chat{OrganizationID: org.ID, OwnerID: owner.ID, LastModelConfigID: model.ID})

	err = fcrypt.InsertChatSecretAnswer(ctx, forkdb.InsertChatSecretAnswerParams{
		ChatID:     chat.ID,
		ToolCallID: "call_1",
		Value:      value,
	})
	require.NoError(t, err)

	stored, err := raw.GetChatSecretAnswersByChatID(ctx, chat.ID)
	require.NoError(t, err)
	require.Len(t, stored, 1)
	require.Equal(t, ciphers[0].HexDigest(), stored[0].ValueKeyID.String)
	requireEncryptedEquals(t, ciphers[0], stored[0].Value, value)

	answers, err := fcrypt.GetChatSecretAnswersByChatID(ctx, chat.ID)
	require.NoError(t, err)
	require.Len(t, answers, 1)
	require.Equal(t, value, answers[0].Value)
	require.Equal(t, "call_1", answers[0].ToolCallID)
}
