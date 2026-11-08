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

		_, err := fdb.GetChatSubagentByChatID(context.Background(), chat.ID)
		require.ErrorIs(t, err, dbauthz.ErrNoActor, "dbauthz authorizes fork queries")

		// The chat has no subagent row, so the query runs and finds none.
		ctx := dbauthz.AsSystemRestricted(testutil.Context(t, testutil.WaitShort))
		_, err = fdb.GetChatSubagentByChatID(ctx, chat.ID)
		require.ErrorIs(t, err, sql.ErrNoRows)
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
