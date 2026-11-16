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

// forkPurgeBatchSize bounds how many rows of one fork table one tick
// deletes.
const forkPurgeBatchSize = 1000

// purgeForkInTx MUST BE CALLED WITH A TRANSACTION. It returns how many
// chat secret answers it purged. It also deletes the wake-up run counts older
// than 7 days, expired plugin link codes, and the closed wake-ups and ended
// workflow runs older than 30 days.
func (i *instance) purgeForkInTx(ctx context.Context, tx database.Store) (int64, error) {
	ftx, err := forkdb.From(tx)
	if err != nil {
		// Only the mock stores of upstream's purge tests lack fork queries.
		// Reads of secret answers apply the TTL too, so a skipped purge
		// leaves no answer usable past it.
		i.logger.Warn(ctx, "skip fork purge", slog.Error(err))
		return 0, nil
	}
	purged, err := ftx.DeleteOldChatSecretAnswers(ctx, forkPurgeBatchSize)
	if err != nil {
		return 0, xerrors.Errorf("failed to delete old chat secret answers: %w", err)
	}
	for _, purge := range []struct {
		name string
		run  func(context.Context) (int64, error)
	}{
		{"chat wake-up run counts", ftx.DeleteOldChatWakeupRunCounts},
		{"plugin link codes", func(ctx context.Context) (int64, error) {
			return ftx.DeleteExpiredPluginLinkCodes(ctx, forkPurgeBatchSize)
		}},
		{"chat wake-ups", func(ctx context.Context) (int64, error) {
			return ftx.DeleteOldChatWakeups(ctx, forkPurgeBatchSize)
		}},
		{"workflow runs", func(ctx context.Context) (int64, error) {
			return ftx.DeleteOldWorkflowRuns(ctx, forkPurgeBatchSize)
		}},
	} {
		count, err := purge.run(ctx)
		if err != nil {
			return 0, xerrors.Errorf("failed to delete old %s: %w", purge.name, err)
		}
		if count > 0 {
			i.logger.Debug(ctx, "purged old "+purge.name, slog.F("count", count))
		}
	}
	return purged, nil
}
