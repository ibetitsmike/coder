package chatdmig_test

import (
	"context"
	"database/sql"
	"io/fs"
	"maps"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/require"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database/migrations"
	"github.com/coder/coder/v2/coderd/database/migrations/chatdmig"
	"github.com/coder/coder/v2/testutil"
)

// TestUpGraphLockTimeout holds a lock that the next migration needs, as a
// long transaction of an old server would.
func TestUpGraphLockTimeout(t *testing.T) {
	t.Parallel()
	admin := adminURL(t)
	// From 634, 635 runs in core and 636 runs as a routed node.
	g := earlySplitGraph(t, deployedVersion)
	_, db := createDB(t, admin)
	require.NoError(t, migrations.UpWithFS(db, upstreamUpTo(t, deployedVersion)))
	giveUp := migrations.WithLockPolicy(chatdmig.LockPolicy{
		Timeout:  100 * time.Millisecond,
		Attempts: 2,
		Backoff:  func(int) time.Duration { return 10 * time.Millisecond },
	})

	// 635 deletes from site_configs.
	holder := holdLock(t, db, "LOCK TABLE site_configs IN SHARE MODE")
	err := migrations.UpGraph(db, g, giveUp)
	require.ErrorContains(t, err, "core step after 634")
	require.True(t, chatdmig.IsLockConflict(err), err)
	requireCoreVersion(t, db, 634)
	require.NoError(t, holder.Rollback())

	// 636 alters organizations. 635 and the core stub of 636 commit before
	// the node gives up.
	holder = holdLock(t, db, "SELECT 1 FROM organizations LIMIT 1")
	err = migrations.UpGraph(db, g, giveUp)
	require.ErrorContains(t, err, "node 000636_organization_skill_member_preferences")
	require.True(t, chatdmig.IsLockConflict(err), err)
	requireCoreVersion(t, db, 636)

	// While the migration retries, queries on organizations still run.
	retrying := make(chan struct{}, 1)
	logger := slog.Make(sinkFunc(func(e slog.SinkEntry) {
		if strings.Contains(e.Message, "waiting for a lock") {
			select {
			case retrying <- struct{}{}:
			default:
			}
		}
	}))
	done := make(chan error, 1)
	go func() {
		done <- migrations.UpGraph(db, g, migrations.WithLogger(logger), migrations.WithLockPolicy(chatdmig.LockPolicy{
			Timeout:  200 * time.Millisecond,
			Attempts: 200,
			Backoff:  func(int) time.Duration { return 50 * time.Millisecond },
		}))
	}()
	<-retrying
	for range 5 {
		ctx, cancel := context.WithTimeout(context.Background(), testutil.WaitShort)
		start := time.Now()
		_, err := db.ExecContext(ctx, "SELECT count(*) FROM organizations")
		cancel()
		require.NoError(t, err)
		require.Less(t, time.Since(start), 2*time.Second)
	}
	require.NoError(t, holder.Rollback())
	require.NoError(t, <-done)
	pending, err := g.Pending(context.Background(), db)
	require.NoError(t, err)
	require.Empty(t, pending)
}

func TestNodeRunModes(t *testing.T) {
	t.Parallel()
	admin := adminURL(t)
	const (
		table    = "0193f0aa-0000-7000-8000-0000000000a1_backfill_table.up.sql"
		index    = "0193f0aa-0000-7000-8000-0000000000a2_backfill_index.up.sql"
		backfill = "0193f0aa-0000-7000-8000-0000000000a3_backfill.up.sql"
		unique   = "0193f0aa-0000-7000-8000-0000000000a4_backfill_unique.up.sql"
		adopt    = "-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8\n"
		after    = "-- requires: 0193f0aa-0000-7000-8000-0000000000a1\n"
	)
	nodes := map[string]string{
		table: adopt + `CREATE TABLE backfill (id int PRIMARY KEY, v int);
INSERT INTO backfill SELECT i, NULL FROM generate_series(1, 2500) i;
`,
		index: after + "-- transaction: none\nCREATE INDEX CONCURRENTLY IF NOT EXISTS backfill_v ON backfill (v);\n",
		backfill: after + "-- phase: background\n-- repeat: until-no-rows\n" +
			"UPDATE backfill SET v = id WHERE id IN (SELECT id FROM backfill WHERE v IS NULL LIMIT 1000);\n",
	}
	g := graphWith(t, nodes)

	t.Run("Server", func(t *testing.T) {
		t.Parallel()
		_, db := createDB(t, admin)
		require.NoError(t, migrations.UpGraph(db, g, migrations.DeferBackground()))
		pending, err := g.Pending(context.Background(), db)
		require.NoError(t, err)
		require.Equal(t, []string{strings.TrimSuffix(backfill, ".up.sql")}, pending)
		var valid bool
		require.NoError(t, db.QueryRow(`SELECT indisvalid FROM pg_index WHERE indexrelid = 'chatd.backfill_v'::regclass`).Scan(&valid))
		require.True(t, valid)

		require.NoError(t, migrations.RunBackground(context.Background(), db, g))
		pending, err = g.Pending(context.Background(), db)
		require.NoError(t, err)
		require.Empty(t, pending)
		// Three batches of at most 1000 rows, each in its own transaction.
		var nulls, txns int
		require.NoError(t, db.QueryRow(`SELECT count(*) FILTER (WHERE v IS NULL), count(DISTINCT xmin::text) FROM chatd.backfill`).Scan(&nulls, &txns))
		require.Equal(t, 0, nulls)
		require.Equal(t, 3, txns)

		// A failed CREATE INDEX CONCURRENTLY leaves an invalid index, which
		// IF NOT EXISTS skips on the next start.
		withUnique := maps.Clone(nodes)
		withUnique[unique] = after + "-- transaction: none\nCREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS backfill_parity ON backfill ((v % 2));\n"
		gUnique := graphWith(t, withUnique)
		require.ErrorContains(t, migrations.UpGraph(db, gUnique), "could not create unique index")
		require.ErrorContains(t, migrations.UpGraph(db, gUnique), "invalid index chatd.backfill_parity")
	})

	// Tests and dbtestutil templates run every node in UpAll.
	t.Run("Inline", func(t *testing.T) {
		t.Parallel()
		_, db := createDB(t, admin)
		require.NoError(t, migrations.UpGraph(db, g))
		pending, err := g.Pending(context.Background(), db)
		require.NoError(t, err)
		require.Empty(t, pending)
	})
}

// graphWith is the embedded graph plus extra fork nodes.
func graphWith(t *testing.T, nodes map[string]string) *chatdmig.Graph {
	t.Helper()
	fork := fstest.MapFS{}
	for _, pattern := range []string{"routes.txt", "nodes/*.up.sql", "override/*.up.sql"} {
		files, err := fs.Glob(os.DirFS("."), pattern)
		require.NoError(t, err)
		for _, f := range files {
			b, err := os.ReadFile(f)
			require.NoError(t, err)
			fork[f] = &fstest.MapFile{Data: b}
		}
	}
	for name, body := range nodes {
		fork["nodes/"+name] = &fstest.MapFile{Data: []byte(body)}
	}
	g, err := chatdmig.LoadFS(os.DirFS(".."), fork)
	require.NoError(t, err)
	return g
}

func holdLock(t *testing.T, db *sql.DB, query string) *sql.Tx {
	t.Helper()
	tx, err := db.Begin()
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	_, err = tx.Exec(query)
	require.NoError(t, err)
	return tx
}

func requireCoreVersion(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var version int
	var dirty bool
	require.NoError(t, db.QueryRow("SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty))
	require.Equal(t, want, version)
	require.False(t, dirty)
}

type sinkFunc func(slog.SinkEntry)

func (f sinkFunc) LogEntry(_ context.Context, e slog.SinkEntry) { f(e) }
func (sinkFunc) Sync()                                          {}
