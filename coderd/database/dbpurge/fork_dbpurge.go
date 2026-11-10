package dbpurge

// This file is fork code: it purges the rows of fork tables. purgeTick calls
// purgeForkInTx once, so fork tables never add code to dbpurge.go.

import (
	"context"

	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
)

// chatSecretAnswersBatchSize bounds how many expired answers one tick
// deletes.
const chatSecretAnswersBatchSize = 1000

// purgeForkInTx MUST BE CALLED WITH A TRANSACTION. It returns how many
// chat secret answers it purged.
func (i *instance) purgeForkInTx(ctx context.Context, tx database.Store) (int64, error) {
	ftx, err := forkdb.From(tx)
	if err != nil {
		// Only the mock stores of upstream's purge tests lack fork queries.
		// Reads of secret answers apply the TTL too, so a skipped purge
		// leaves no answer usable past it.
		i.logger.Warn(ctx, "skip fork purge", slog.Error(err))
		return 0, nil
	}
	purged, err := ftx.DeleteOldChatSecretAnswers(ctx, chatSecretAnswersBatchSize)
	if err != nil {
		return 0, xerrors.Errorf("failed to delete old chat secret answers: %w", err)
	}
	return purged, nil
}
