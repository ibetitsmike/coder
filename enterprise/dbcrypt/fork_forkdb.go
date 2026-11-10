package dbcrypt

// This file is fork code: it encrypts the secrets of fork tables, queried
// through package forkdb, like dbCrypt encrypts upstream's. See
// coderd/database/migrations/chatdmig/README.md#fork-queries.

import (
	"context"
	"database/sql"
	"strings"

	"github.com/google/uuid"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
)

var _ forkdb.Wrapper = (*dbCrypt)(nil)

func (db *dbCrypt) ForkUnwrap() database.Store {
	return db.Store
}

func (db *dbCrypt) ForkWrap(s forkdb.Store) forkdb.Store {
	return &forkCrypt{crypt: db, Store: s}
}

// forkCrypt encrypts plugins.config and chat_secret_answers.value. Queries
// that return no secret pass through the embedded store.
type forkCrypt struct {
	crypt *dbCrypt
	forkdb.Store
}

func (db *forkCrypt) decryptPlugins(plugins []forkdb.Plugin) error {
	for i := range plugins {
		if err := db.crypt.decryptField(&plugins[i].Config, plugins[i].ConfigKeyID); err != nil {
			return err
		}
	}
	return nil
}

func (db *forkCrypt) encryptPluginConfig(config *string, keyID *sql.NullString) error {
	if strings.TrimSpace(*config) == "" {
		*keyID = sql.NullString{}
		return nil
	}
	return db.crypt.encryptField(config, keyID)
}

func (db *forkCrypt) InsertPlugin(ctx context.Context, params forkdb.InsertPluginParams) (forkdb.Plugin, error) {
	if err := db.encryptPluginConfig(&params.Config, &params.ConfigKeyID); err != nil {
		return forkdb.Plugin{}, err
	}
	plugin, err := db.Store.InsertPlugin(ctx, params)
	if err != nil {
		return forkdb.Plugin{}, err
	}
	if err := db.crypt.decryptField(&plugin.Config, plugin.ConfigKeyID); err != nil {
		return forkdb.Plugin{}, err
	}
	return plugin, nil
}

func (db *forkCrypt) GetPluginByID(ctx context.Context, id uuid.UUID) (forkdb.Plugin, error) {
	plugin, err := db.Store.GetPluginByID(ctx, id)
	if err != nil {
		return forkdb.Plugin{}, err
	}
	if err := db.crypt.decryptField(&plugin.Config, plugin.ConfigKeyID); err != nil {
		return forkdb.Plugin{}, err
	}
	return plugin, nil
}

func (db *forkCrypt) GetPluginsByOrganizationID(ctx context.Context, organizationID uuid.UUID) ([]forkdb.Plugin, error) {
	plugins, err := db.Store.GetPluginsByOrganizationID(ctx, organizationID)
	if err != nil {
		return nil, err
	}
	if err := db.decryptPlugins(plugins); err != nil {
		return nil, err
	}
	return plugins, nil
}

func (db *forkCrypt) AcquirePluginLeases(ctx context.Context, params forkdb.AcquirePluginLeasesParams) ([]forkdb.Plugin, error) {
	plugins, err := db.Store.AcquirePluginLeases(ctx, params)
	if err != nil {
		return nil, err
	}
	if err := db.decryptPlugins(plugins); err != nil {
		return nil, err
	}
	return plugins, nil
}

func (db *forkCrypt) UpdatePluginConfig(ctx context.Context, params forkdb.UpdatePluginConfigParams) (forkdb.Plugin, error) {
	if err := db.encryptPluginConfig(&params.Config, &params.ConfigKeyID); err != nil {
		return forkdb.Plugin{}, err
	}
	plugin, err := db.Store.UpdatePluginConfig(ctx, params)
	if err != nil {
		return forkdb.Plugin{}, err
	}
	if err := db.crypt.decryptField(&plugin.Config, plugin.ConfigKeyID); err != nil {
		return forkdb.Plugin{}, err
	}
	return plugin, nil
}

func (db *forkCrypt) UpdatePluginEnabled(ctx context.Context, params forkdb.UpdatePluginEnabledParams) (forkdb.Plugin, error) {
	plugin, err := db.Store.UpdatePluginEnabled(ctx, params)
	if err != nil {
		return forkdb.Plugin{}, err
	}
	if err := db.crypt.decryptField(&plugin.Config, plugin.ConfigKeyID); err != nil {
		return forkdb.Plugin{}, err
	}
	return plugin, nil
}

func (db *forkCrypt) InsertChatSecretAnswer(ctx context.Context, params forkdb.InsertChatSecretAnswerParams) error {
	if err := db.crypt.encryptField(&params.Value, &params.ValueKeyID); err != nil {
		return err
	}
	return db.Store.InsertChatSecretAnswer(ctx, params)
}

func (db *forkCrypt) GetChatSecretAnswersByChatID(ctx context.Context, chatID uuid.UUID) ([]forkdb.ChatSecretAnswer, error) {
	answers, err := db.Store.GetChatSecretAnswersByChatID(ctx, chatID)
	if err != nil {
		return nil, err
	}
	for i := range answers {
		if err := db.crypt.decryptField(&answers[i].Value, answers[i].ValueKeyID); err != nil {
			return nil, err
		}
	}
	return answers, nil
}
