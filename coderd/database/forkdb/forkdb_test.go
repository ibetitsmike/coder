package forkdb_test

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"golang.org/x/xerrors"
	"gopkg.in/yaml.v3"

	"cdr.dev/slog/v3/sloggers/slogtest"
	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbmetrics"
	"github.com/coder/coder/v2/coderd/database/dbmock"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/database/migrations"
	"github.com/coder/coder/v2/coderd/database/migrations/chatdmig"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/testutil"
)

// TestSQLCSchemaOrder checks that sqlc reads upstream's dump.sql and then
// every fork node in graph order, so the generated code matches the schema
// that the migrations build.
func TestSQLCSchemaOrder(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("sqlc.yaml")
	require.NoError(t, err)
	var cfg struct {
		SQL []struct {
			Schema []string `yaml:"schema"`
		} `yaml:"sql"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &cfg))
	require.Len(t, cfg.SQL, 1)

	g, err := migrations.LoadGraph()
	require.NoError(t, err)
	want := []string{"../dump.sql"}
	for _, n := range g.Nodes {
		if n.Source == chatdmig.SourceFork {
			want = append(want, "../migrations/chatdmig/nodes/"+n.Name+".up.sql")
		}
	}
	require.Equal(t, want, cfg.SQL[0].Schema)
}

func TestFrom(t *testing.T) {
	t.Parallel()

	t.Run("UnknownStore", func(t *testing.T) {
		t.Parallel()

		_, err := forkdb.From(dbmock.NewMockStore(gomock.NewController(t)))
		require.ErrorContains(t, err, "does not support fork queries")
	})

	t.Run("Wrappers", func(t *testing.T) {
		t.Parallel()

		raw, _ := dbtestutil.NewDB(t)
		reg := prometheus.NewRegistry()
		db := wrap(t, dbmetrics.NewQueryMetrics(raw, slogtest.Make(t, nil), reg))
		fdb := mustFrom(t, db)
		chat := dbgen.Chat(t, raw, database.Chat{
			OrganizationID:    dbgen.Organization(t, raw, database.Organization{}).ID,
			OwnerID:           dbgen.User(t, raw, database.User{}).ID,
			LastModelConfigID: dbgen.ChatModelConfig(t, raw, database.ChatModelConfig{}).ID,
		})

		sub := dbgen.ChatSubagent(t, raw, forkdb.ChatSubagent{ChatID: chat.ID})

		_, err := fdb.GetChatSubagentByChatID(context.Background(), chat.ID)
		require.ErrorIs(t, err, dbauthz.ErrNoActor, "dbauthz authorizes fork queries")

		ctx := dbauthz.AsSystemRestricted(testutil.Context(t, testutil.WaitShort))
		got, err := fdb.GetChatSubagentByChatID(ctx, chat.ID)
		require.NoError(t, err)
		require.Equal(t, sub, got)
		require.True(t, hasQueryCount(t, reg, "GetChatSubagentByChatID"), "dbmetrics measures fork queries")
	})

	t.Run("DBMetrics", func(t *testing.T) {
		t.Parallel()

		// Deployments without query metrics wrap the store in NewDBMetrics.
		raw, _ := dbtestutil.NewDB(t)
		fdb := mustFrom(t, dbmetrics.NewDBMetrics(raw, slogtest.Make(t, nil), prometheus.NewRegistry()))

		_, err := fdb.GetChatSubagentByChatID(testutil.Context(t, testutil.WaitShort), uuid.New())
		require.ErrorIs(t, err, sql.ErrNoRows)
	})
}

// TestInTx checks that forkdb.InTx runs fork and upstream queries in one
// transaction: a chat and its subagent commit together, and a rollback leaves
// neither.
func TestInTx(t *testing.T) {
	t.Parallel()

	// createChatWithSubagent inserts a chat through tx and its subagent
	// through ftx. The subagent insert passes its foreign key only if ftx
	// sees the uncommitted chat.
	createChatWithSubagent := func(t *testing.T, tx database.Store, ftx forkdb.Store) (database.Chat, forkdb.ChatSubagent) {
		t.Helper()
		chat := dbgen.Chat(t, tx, database.Chat{
			OrganizationID:    dbgen.Organization(t, tx, database.Organization{}).ID,
			OwnerID:           dbgen.User(t, tx, database.User{}).ID,
			LastModelConfigID: dbgen.ChatModelConfig(t, tx, database.ChatModelConfig{}).ID,
		})
		sub, err := ftx.InsertChatSubagent(testutil.Context(t, testutil.WaitShort), forkdb.InsertChatSubagentParams{
			ChatID:    chat.ID,
			AgentType: "explore",
			Depth:     1,
			Isolation: "shared",
		})
		require.NoError(t, err, "fork query runs in the transaction")
		return chat, sub
	}

	t.Run("Commit", func(t *testing.T) {
		t.Parallel()
		db, _ := dbtestutil.NewDB(t)
		ctx := testutil.Context(t, testutil.WaitShort)

		var chat database.Chat
		var sub forkdb.ChatSubagent
		err := forkdb.InTx(db, func(tx database.Store, ftx forkdb.Store) error {
			chat, sub = createChatWithSubagent(t, tx, ftx)
			return nil
		}, nil)
		require.NoError(t, err)

		_, err = db.GetChatByID(ctx, chat.ID)
		require.NoError(t, err, "the chat commits")
		got, err := mustFrom(t, db).GetChatSubagentByChatID(ctx, chat.ID)
		require.NoError(t, err, "the subagent commits with the chat")
		require.Equal(t, sub, got)
	})

	t.Run("Rollback", func(t *testing.T) {
		t.Parallel()
		db, _ := dbtestutil.NewDB(t)
		ctx := testutil.Context(t, testutil.WaitShort)
		errRollback := xerrors.New("roll back")

		var chat database.Chat
		err := forkdb.InTx(db, func(tx database.Store, ftx forkdb.Store) error {
			chat, _ = createChatWithSubagent(t, tx, ftx)
			return errRollback
		}, nil)
		require.ErrorIs(t, err, errRollback)

		_, err = db.GetChatByID(ctx, chat.ID)
		require.ErrorIs(t, err, sql.ErrNoRows, "the chat rolls back")
		_, err = mustFrom(t, db).GetChatSubagentByChatID(ctx, chat.ID)
		require.ErrorIs(t, err, sql.ErrNoRows, "the subagent rolls back with the chat")
	})
}

// wrap wraps db in dbauthz like coderd does.
func wrap(t *testing.T, db database.Store) database.Store {
	t.Helper()
	authz := rbac.NewAuthorizer(prometheus.NewRegistry())
	return dbauthz.New(db, authz, slogtest.Make(t, nil), coderdtest.AccessControlStorePointer())
}

func mustFrom(t *testing.T, db database.Store) forkdb.Store {
	t.Helper()
	fdb, err := forkdb.From(db)
	require.NoError(t, err)
	return fdb
}

func hasQueryCount(t *testing.T, reg *prometheus.Registry, query string) bool {
	t.Helper()
	families, err := reg.Gather()
	require.NoError(t, err)
	for _, f := range families {
		if f.GetName() != "coderd_db_query_counts_total" {
			continue
		}
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "query" && l.GetValue() == query {
					return true
				}
			}
		}
	}
	return false
}
