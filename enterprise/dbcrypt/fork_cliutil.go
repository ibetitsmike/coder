package dbcrypt

// This file is fork code: Rotate, Decrypt and Delete call these functions
// for chat_secret_answers, which references dbcrypt_keys too, so revoking a
// key never fails on an answer that the key still encrypts.

import (
	"context"
	"database/sql"

	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
)

// chatSecretAnswersPageSize bounds how many answers, each up to 16 KiB, a
// key command holds in memory at once.
const chatSecretAnswersPageSize = 100

// forkRecryptChatSecretAnswers decrypts every chat secret answer and
// encrypts it again with the primary cipher of cryptDB, or leaves it
// decrypted when cryptDB has no primary cipher.
func forkRecryptChatSecretAnswers(ctx context.Context, log slog.Logger, sqlDB *sql.DB, cryptStore database.Store) error {
	cryptDB, ok := cryptStore.(*dbCrypt)
	if !ok {
		return xerrors.Errorf("developer error: dbcrypt.New did not return *dbCrypt")
	}
	target := sql.NullString{String: cryptDB.primaryCipherDigest, Valid: cryptDB.primaryCipherDigest != ""}
	log.Info(ctx, "re-encrypting chat secret answers")
	var count int
	// A recrypted answer leaves the filter, so each page starts at the
	// first answer that is left.
	for {
		answers, err := chatSecretAnswersNotEncryptedWith(ctx, sqlDB, target)
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
	}
	log.Info(ctx, "re-encrypted chat secret answers", slog.F("answer_count", count))
	return nil
}

// chatSecretAnswersNotEncryptedWith returns up to chatSecretAnswersPageSize
// answers whose key is not keyID.
func chatSecretAnswersNotEncryptedWith(ctx context.Context, sqlDB *sql.DB, keyID sql.NullString) ([]forkdb.ChatSecretAnswer, error) {
	rows, err := sqlDB.QueryContext(ctx,
		`SELECT chat_id, tool_call_id, value, value_key_id FROM chat_secret_answers WHERE value_key_id IS DISTINCT FROM $1 ORDER BY chat_id, tool_call_id LIMIT $2`,
		keyID, chatSecretAnswersPageSize)
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

// forkDeleteEncryptedSecrets deletes the encrypted chat secret answers. An
// answer is only usable for a day, so the user answers again.
func forkDeleteEncryptedSecrets(ctx context.Context, sqlDB *sql.DB) error {
	if _, err := sqlDB.ExecContext(ctx, `DELETE FROM chat_secret_answers WHERE value_key_id IS NOT NULL`); err != nil {
		return xerrors.Errorf("delete encrypted chat secret answers: %w", err)
	}
	return nil
}
