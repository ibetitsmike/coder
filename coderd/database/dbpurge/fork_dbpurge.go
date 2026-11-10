package dbpurge

// This file is fork code: it purges the rows of fork tables. purgeTick calls
// purgeForkInTx once, so fork tables never add code to dbpurge.go.

import (
	"context"
	"time"

	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
)

// chatSecretAnswerTTL matches the TTL that GetChatSecretAnswersByChatID
// applies when it reads the answers.
const chatSecretAnswerTTL = 24 * time.Hour

// purgeForkInTx MUST BE CALLED WITH A TRANSACTION.
func (i *instance) purgeForkInTx(ctx context.Context, tx database.Store, start time.Time) error {
	ftx, err := forkdb.From(tx)
	if err != nil {
		// Only the mock stores of upstream's purge tests lack fork queries.
		// Reads of secret answers apply the TTL too, so a skipped purge
		// leaves no answer usable past it.
		i.logger.Warn(ctx, "skip fork purge", slog.Error(err))
		return nil
	}
	purged, err := ftx.DeleteOldChatSecretAnswers(ctx, start.Add(-chatSecretAnswerTTL))
	if err != nil {
		return xerrors.Errorf("failed to delete old chat secret answers: %w", err)
	}
	i.logger.Debug(ctx, "purged old fork database entries", slog.F("chat_secret_answers", purged))
	return nil
}
