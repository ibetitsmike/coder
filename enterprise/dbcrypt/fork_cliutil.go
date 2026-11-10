package dbcrypt

// This file is fork code: Rotate, Decrypt and Delete call these functions
// for chat_secret_answers and plugins, which reference dbcrypt_keys too, so
// revoking a key never fails on a row that the key still encrypts.

import (
	"context"
	"database/sql"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
)

// chatSecretAnswersPageSize bounds how many answers, each up to 16 KiB, a
// key command holds in memory at once.
const chatSecretAnswersPageSize = 100

// pluginConfigsPageSize bounds how many plugin configs a key command holds
// in memory at once.
const pluginConfigsPageSize = 100

// forkRecryptSecrets decrypts the fork's encrypted columns and encrypts them
// again with the primary cipher of cryptStore, or leaves them decrypted when
// cryptStore has no primary cipher.
func forkRecryptSecrets(ctx context.Context, log slog.Logger, sqlDB *sql.DB, cryptStore database.Store) error {
	cryptDB, ok := cryptStore.(*dbCrypt)
	if !ok {
		return xerrors.Errorf("developer error: dbcrypt.New did not return *dbCrypt")
	}
	if err := forkRecryptChatSecretAnswers(ctx, log, sqlDB, cryptDB); err != nil {
		return err
	}
	return forkRecryptPluginConfigs(ctx, log, sqlDB, cryptDB)
}

func forkRecryptChatSecretAnswers(ctx context.Context, log slog.Logger, sqlDB *sql.DB, cryptDB *dbCrypt) error {
	target := sql.NullString{String: cryptDB.primaryCipherDigest, Valid: cryptDB.primaryCipherDigest != ""}
	log.Info(ctx, "re-encrypting chat secret answers")
	var (
		count int
		after forkdb.ChatSecretAnswer
	)
	// Each page starts after the last answer of the previous one, so no
	// page rescans the answers that are already recrypted.
	for {
		answers, err := chatSecretAnswersNotEncryptedWith(ctx, sqlDB, target, after)
		if err != nil {
			return err
		}
		for _, answer := range answers {
			readValue, readKeyID := answer.Value, answer.ValueKeyID
			if err := cryptDB.decryptField(&answer.Value, answer.ValueKeyID); err != nil {
				return xerrors.Errorf("decrypt chat secret answer chat_id=%s tool_call_id=%s: %w", answer.ChatID, answer.ToolCallID, err)
			}
			keyID := sql.NullString{}
			if err := cryptDB.encryptField(&answer.Value, &keyID); err != nil {
				return xerrors.Errorf("encrypt chat secret answer chat_id=%s tool_call_id=%s: %w", answer.ChatID, answer.ToolCallID, err)
			}
			// An answer that was deleted or answered again meanwhile updates
			// no row, so a retry's new value is never replaced by the old one.
			if _, err := sqlDB.ExecContext(ctx,
				`UPDATE chat_secret_answers SET value = $1, value_key_id = $2
				WHERE chat_id = $3 AND tool_call_id = $4 AND value = $5 AND value_key_id IS NOT DISTINCT FROM $6`,
				answer.Value, keyID, answer.ChatID, answer.ToolCallID, readValue, readKeyID); err != nil {
				return xerrors.Errorf("update chat secret answer chat_id=%s tool_call_id=%s: %w", answer.ChatID, answer.ToolCallID, err)
			}
		}
		count += len(answers)
		if len(answers) < chatSecretAnswersPageSize {
			break
		}
		after = answers[len(answers)-1]
	}
	log.Info(ctx, "re-encrypted chat secret answers", slog.F("answer_count", count))
	return nil
}

// chatSecretAnswersNotEncryptedWith returns up to chatSecretAnswersPageSize
// answers whose key is not keyID, in key order after the answer after. The
// zero answer comes before every answer.
func chatSecretAnswersNotEncryptedWith(ctx context.Context, sqlDB *sql.DB, keyID sql.NullString, after forkdb.ChatSecretAnswer) ([]forkdb.ChatSecretAnswer, error) {
	rows, err := sqlDB.QueryContext(ctx,
		`SELECT chat_id, tool_call_id, value, value_key_id FROM chat_secret_answers
		WHERE value_key_id IS DISTINCT FROM $1 AND (chat_id, tool_call_id) > ($3, $4)
		ORDER BY chat_id, tool_call_id LIMIT $2`,
		keyID, chatSecretAnswersPageSize, after.ChatID, after.ToolCallID)
	if err != nil {
		return nil, xerrors.Errorf("get chat secret answers: %w", err)
	}
	defer rows.Close()
	var answers []forkdb.ChatSecretAnswer
	for rows.Next() {
		var answer forkdb.ChatSecretAnswer
		if err := rows.Scan(&answer.ChatID, &answer.ToolCallID, &answer.Value, &answer.ValueKeyID); err != nil {
			return nil, xerrors.Errorf("scan chat secret answer: %w", err)
		}
		answers = append(answers, answer)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("get chat secret answers: %w", err)
	}
	return answers, nil
}

func forkRecryptPluginConfigs(ctx context.Context, log slog.Logger, sqlDB *sql.DB, cryptDB *dbCrypt) error {
	target := sql.NullString{String: cryptDB.primaryCipherDigest, Valid: cryptDB.primaryCipherDigest != ""}
	log.Info(ctx, "re-encrypting plugin configs")
	var (
		count int
		after uuid.UUID
	)
	for {
		plugins, err := pluginConfigsNotEncryptedWith(ctx, sqlDB, target, after)
		if err != nil {
			return err
		}
		for _, plugin := range plugins {
			readConfig, readKeyID := plugin.Config, plugin.ConfigKeyID
			if err := cryptDB.decryptField(&plugin.Config, plugin.ConfigKeyID); err != nil {
				return xerrors.Errorf("decrypt plugin config id=%s: %w", plugin.ID, err)
			}
			keyID := sql.NullString{}
			if err := cryptDB.encryptPluginConfig(&plugin.Config, &keyID); err != nil {
				return xerrors.Errorf("encrypt plugin config id=%s: %w", plugin.ID, err)
			}
			// A blank config stays plaintext, so there is nothing to write.
			if plugin.Config == readConfig && keyID == readKeyID {
				continue
			}
			// A config that was updated meanwhile updates no row, so the new
			// config is never replaced by the old one. updated_at stays, so
			// running plugins do not restart for an unchanged config.
			if _, err := sqlDB.ExecContext(ctx,
				`UPDATE plugins SET config = $1, config_key_id = $2
				WHERE id = $3 AND config = $4 AND config_key_id IS NOT DISTINCT FROM $5`,
				plugin.Config, keyID, plugin.ID, readConfig, readKeyID); err != nil {
				return xerrors.Errorf("update plugin config id=%s: %w", plugin.ID, err)
			}
		}
		count += len(plugins)
		if len(plugins) < pluginConfigsPageSize {
			break
		}
		after = plugins[len(plugins)-1].ID
	}
	log.Info(ctx, "re-encrypted plugin configs", slog.F("plugin_count", count))
	return nil
}

// pluginConfigsNotEncryptedWith returns up to pluginConfigsPageSize plugins,
// with only their ID, config and key set, whose key is not keyID, in ID order
// after the plugin with ID after.
func pluginConfigsNotEncryptedWith(ctx context.Context, sqlDB *sql.DB, keyID sql.NullString, after uuid.UUID) ([]forkdb.Plugin, error) {
	rows, err := sqlDB.QueryContext(ctx,
		`SELECT id, config, config_key_id FROM plugins
		WHERE config_key_id IS DISTINCT FROM $1 AND id > $3
		ORDER BY id LIMIT $2`,
		keyID, pluginConfigsPageSize, after)
	if err != nil {
		return nil, xerrors.Errorf("get plugin configs: %w", err)
	}
	defer rows.Close()
	var plugins []forkdb.Plugin
	for rows.Next() {
		var plugin forkdb.Plugin
		if err := rows.Scan(&plugin.ID, &plugin.Config, &plugin.ConfigKeyID); err != nil {
			return nil, xerrors.Errorf("scan plugin config: %w", err)
		}
		plugins = append(plugins, plugin)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("get plugin configs: %w", err)
	}
	return plugins, nil
}

// forkDeleteEncryptedSecrets deletes the encrypted chat secret answers, since
// an answer is only usable for a day and the user answers again. It clears
// encrypted plugin configs instead of deleting the plugins, like upstream
// clears ai_providers.settings, so an admin sets the config again. Bumping
// updated_at restarts running plugins with the cleared config.
func forkDeleteEncryptedSecrets(ctx context.Context, sqlDB *sql.DB) error {
	if _, err := sqlDB.ExecContext(ctx, `DELETE FROM chat_secret_answers WHERE value_key_id IS NOT NULL`); err != nil {
		return xerrors.Errorf("delete encrypted chat secret answers: %w", err)
	}
	if _, err := sqlDB.ExecContext(ctx,
		`UPDATE plugins SET config = '', config_key_id = NULL, updated_at = now() WHERE config_key_id IS NOT NULL`); err != nil {
		return xerrors.Errorf("clear encrypted plugin configs: %w", err)
	}
	return nil
}
