package chatd

// This file is fork code: it tests the lock order of a family archive behind
// the fork-nested-subagents experiment.

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
)

// holdTransition starts a transaction that updates chatID once, like the
// snapshot bump of a chatstate transition, and keeps it open. finish makes
// the second update of the transition, which rechecks the foreign keys of
// the row, and commits.
func holdTransition(ctx context.Context, t *testing.T, sqlDB *sql.DB, chatID uuid.UUID) (pid int, finish func() error) {
	t.Helper()
	conn, err := sqlDB.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	tx, err := conn.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid))
	_, err = tx.ExecContext(ctx, "UPDATE chats SET snapshot_version = snapshot_version + 1 WHERE id = $1", chatID)
	require.NoError(t, err)
	return pid, func() error {
		if _, err := tx.ExecContext(ctx, "UPDATE chats SET status = status WHERE id = $1", chatID); err != nil {
			return err
		}
		return tx.Commit()
	}
}

// requireBlockedBy waits until another backend waits for a lock that pid
// holds.
func requireBlockedBy(ctx context.Context, t *testing.T, sqlDB *sql.DB, pid int) {
	t.Helper()
	require.True(t, testutil.Eventually(ctx, t, func(ctx context.Context) bool {
		var blocked bool
		err := sqlDB.QueryRowContext(ctx,
			"SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1::int = ANY(pg_blocking_pids(pid)))",
			pid,
		).Scan(&blocked)
		return err == nil && blocked
	}, testutil.IntervalFast), "the archive never waited for the held transition")
}

// The held transition is on the grandchild, so its second update takes FOR
// KEY SHARE on the child and the root while the archive waits for it.
// Neither may be locked by the archive in a mode that blocks that.
func TestForkSetFamilyArchivedConcurrentTransition(t *testing.T) {
	t.Parallel()

	t.Run("ArchiveAndUnarchive", func(t *testing.T) {
		t.Parallel()
		f := newNestedSubagentsFixture(t, codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}, Limits{})
		ctx := chatdTestContext(t)
		child, err := f.spawn(t, f.root, childSubagentChatOptions{})
		require.NoError(t, err)
		grandchild, err := f.spawn(t, child, childSubagentChatOptions{})
		require.NoError(t, err)
		for _, chat := range []database.Chat{f.root, child, grandchild} {
			f.setStatus(t, chat, database.ChatStatusWaiting, uuid.NullUUID{})
		}

		for _, archived := range []bool{true, false} {
			pid, finish := holdTransition(ctx, t, f.sqlDB, grandchild.ID)
			archiveErr := make(chan error, 1)
			go func() {
				if archived {
					archiveErr <- f.server.ArchiveChat(ctx, f.root)
				} else {
					archiveErr <- f.server.UnarchiveChat(ctx, f.root)
				}
			}()
			requireBlockedBy(ctx, t, f.sqlDB, pid)
			require.NoError(t, finish())
			require.NoError(t, testutil.RequireReceive(ctx, t, archiveErr))
			f.requireArchived(t, archived, f.root, child, grandchild)
		}
	})

	t.Run("AutoArchive", func(t *testing.T) {
		t.Parallel()
		f := newWorkerTestFixture(t)
		ctx := testutil.Context(t, testutil.WaitLong)
		now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
		root := f.createArchiveCandidate(t, now.Add(-120*24*time.Hour))
		child := f.createArchiveCandidate(t, now.Add(-120*24*time.Hour))
		grandchild := f.createArchiveCandidate(t, now.Add(-120*24*time.Hour))
		f.linkChild(t, root.ID, child.ID)
		_, err := f.sqlDB.ExecContext(ctx, "UPDATE chats SET parent_chat_id = $1, root_chat_id = $2 WHERE id = $3", child.ID, root.ID, grandchild.ID)
		require.NoError(t, err)
		require.NoError(t, f.db.UpsertChatAutoArchiveDays(ctx, 90))
		server, err := New(f.pubsub, Config{
			Logger:      testutil.Logger(t),
			Database:    f.db,
			ReplicaID:   uuid.New(),
			Experiments: codersdk.Experiments{codersdk.ExperimentForkNestedSubagents},
		})
		require.NoError(t, err)
		t.Cleanup(func() { _ = server.Close() })
		opts := f.archiveWorkerOptions()
		opts.Pubsub = f.pubsub
		worker, err := newChatWorker(server, opts)
		require.NoError(t, err)
		row, cutoff := staleAutoArchiveCandidate(t, f, now)

		pid, finish := holdTransition(ctx, t, f.sqlDB, grandchild.ID)
		type result struct {
			family []autoArchivedChat
			err    error
		}
		done := make(chan result, 1)
		go func() {
			family, err := worker.archiveCandidate(ctx, row, cutoff)
			done <- result{family: family, err: err}
		}()
		requireBlockedBy(ctx, t, f.sqlDB, pid)
		require.NoError(t, finish())
		res := testutil.RequireReceive(ctx, t, done)
		require.NoError(t, res.err)
		require.Len(t, res.family, 3)
		for _, id := range []uuid.UUID{root.ID, child.ID, grandchild.ID} {
			require.True(t, f.archived(t, id))
		}
	})
}
