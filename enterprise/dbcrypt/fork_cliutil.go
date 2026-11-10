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

// forkRecryptChatSecretAnswers decrypts every chat secret answer and
// encrypts it again with the primary cipher of cryptDB, or leaves it
// decrypted when cryptDB has no primary cipher.
func forkRecryptChatSecretAnswers(ctx context.Context, log slog.Logger, sqlDB *sql.DB, cryptStore database.Store) error {
	cryptDB, ok := cryptStore.(*dbCrypt)
	if !ok {
		return xerrors.Errorf("developer error: dbcrypt.New did not return *dbCrypt")
	}
	target := sql.NullString{String: cryptDB.primaryCipherDigest, Valid: cryptDB.primaryCipherDigest != ""}
	rows, err := sqlDB.QueryContext(ctx,
		`SELECT chat_id, tool_call_id, value, value_key_id FROM chat_secret_answers WHERE value_key_id IS DISTINCT FROM $1`,
		target)
	if err != nil {
		return xerrors.Errorf("get chat secret answers: %w", err)
	}
	var answers []forkdb.ChatSecretAnswer
	for rows.Next() {
		var answer forkdb.ChatSecretAnswer
		if err := rows.Scan(&answer.ChatID, &answer.ToolCallID, &answer.Value, &answer.ValueKeyID); err != nil {
			_ = rows.Close()
			return xerrors.Errorf("scan chat secret answer: %w", err)
		}
		answers = append(answers, answer)
	}
	if err := rows.Close(); err != nil {
		return xerrors.Errorf("close chat secret answers: %w", err)
	}
	if err := rows.Err(); err != nil {
		return xerrors.Errorf("get chat secret answers: %w", err)
	}
	log.Info(ctx, "re-encrypting chat secret answers", slog.F("answer_count", len(answers)))
	for _, answer := range answers {
		if err := cryptDB.decryptField(&answer.Value, answer.ValueKeyID); err != nil {
			return xerrors.Errorf("decrypt chat secret answer chat_id=%s tool_call_id=%s: %w", answer.ChatID, answer.ToolCallID, err)
		}
		keyID := sql.NullString{}
		if err := cryptDB.encryptField(&answer.Value, &keyID); err != nil {
			return xerrors.Errorf("encrypt chat secret answer chat_id=%s tool_call_id=%s: %w", answer.ChatID, answer.ToolCallID, err)
		}
		// An answer that was deleted meanwhile updates no row.
		if _, err := sqlDB.ExecContext(ctx,
			`UPDATE chat_secret_answers SET value = $1, value_key_id = $2 WHERE chat_id = $3 AND tool_call_id = $4`,
			answer.Value, keyID, answer.ChatID, answer.ToolCallID); err != nil {
			return xerrors.Errorf("update chat secret answer chat_id=%s tool_call_id=%s: %w", answer.ChatID, answer.ToolCallID, err)
		}
	}
	return nil
}

// forkDeleteEncryptedSecrets deletes the encrypted chat secret answers. An
// answer is only usable for a day, so the user answers again.
func forkDeleteEncryptedSecrets(ctx context.Context, sqlDB *sql.DB) error {
	if _, err := sqlDB.ExecContext(ctx, `DELETE FROM chat_secret_answers WHERE value_key_id IS NOT NULL`); err != nil {
		return xerrors.Errorf("delete encrypted chat secret answers: %w", err)
	}
	return nil
}
