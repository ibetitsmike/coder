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
	"github.com/coder/coder/v2/coderd/database/dbtime"
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

// TestAPIKeyScopeValues checks that upstream's scopes and the fork's scopes
// together are exactly the values of the api_key_scope enum.
func TestAPIKeyScopeValues(t *testing.T) {
	t.Parallel()

	_, _, sqlDB := dbtestutil.NewDBWithSQLDB(t)
	ctx := testutil.Context(t, testutil.WaitShort)

	rows, err := sqlDB.QueryContext(ctx, "SELECT unnest(enum_range(NULL::public.api_key_scope))::text")
	require.NoError(t, err)
	var got []database.APIKeyScope
	for rows.Next() {
		var s string
		require.NoError(t, rows.Scan(&s))
		got = append(got, database.APIKeyScope(s))
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())

	want := append(database.AllAPIKeyScopeValues(), forkdb.APIKeyScopeValues()...)
	require.ElementsMatch(t, want, got)
	for _, s := range got {
		require.True(t, forkdb.ValidAPIKeyScope(s), "scope %q", s)
	}
	require.False(t, forkdb.ValidAPIKeyScope("plugin:nope"))
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
		orgID := uuid.New()

		_, err := fdb.GetPluginsByOrganizationID(context.Background(), orgID)
		require.ErrorIs(t, err, dbauthz.ErrNoActor, "dbauthz authorizes fork queries")

		ctx := dbauthz.AsSystemRestricted(testutil.Context(t, testutil.WaitShort))
		plugins, err := fdb.GetPluginsByOrganizationID(ctx, orgID)
		require.NoError(t, err)
		require.Empty(t, plugins)
		require.True(t, hasQueryCount(t, reg, "GetPluginsByOrganizationID"), "dbmetrics measures fork queries")
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

// TestInTx checks that the fork queries of forkdb.InTx run in the
// transaction of the upstream queries: they see its uncommitted rows and roll
// back with it.
func TestInTx(t *testing.T) {
	t.Parallel()

	raw, _ := dbtestutil.NewDB(t)
	db := wrap(t, raw)
	fdb := mustFrom(t, db)
	// The system subject may not create plugins, so act as an owner.
	ctx := dbauthz.As(testutil.Context(t, testutil.WaitShort), rbac.Subject{
		ID:     uuid.NewString(),
		Roles:  rbac.RoleIdentifiers{rbac.RoleOwner()},
		Groups: []string{},
		Scope:  rbac.ScopeAll,
	})

	// insert inserts a plugin whose organization, user and file come from
	// the upstream queries of the same transaction, so the foreign keys
	// fail unless both run in it.
	insert := func(tx database.Store, ftx forkdb.Store) (database.Organization, forkdb.Plugin, error) {
		org := dbgen.Organization(t, tx, database.Organization{})
		user := dbgen.User(t, tx, database.User{})
		file := dbgen.File(t, tx, database.File{CreatedBy: user.ID})
		plugin, err := ftx.InsertPlugin(ctx, forkdb.InsertPluginParams{
			ID:             uuid.New(),
			OrganizationID: org.ID,
			Name:           "signal",
			FileID:         file.ID,
			Manifest:       []byte("{}"),
			Grants:         []byte("{}"),
			CreatedBy:      user.ID,
			CreatedAt:      dbtime.Now(),
			UpdatedAt:      dbtime.Now(),
		})
		return org, plugin, err
	}

	t.Run("Commit", func(t *testing.T) {
		t.Parallel()

		var plugin forkdb.Plugin
		err := forkdb.InTx(db, func(tx database.Store, ftx forkdb.Store) error {
			var err error
			_, plugin, err = insert(tx, ftx)
			return err
		}, nil)
		require.NoError(t, err)

		got, err := fdb.GetPluginByID(ctx, plugin.ID)
		require.NoError(t, err)
		require.Equal(t, plugin.ID, got.ID)
	})

	t.Run("Rollback", func(t *testing.T) {
		t.Parallel()

		errRollback := xerrors.New("roll back")
		var org database.Organization
		var plugin forkdb.Plugin
		err := forkdb.InTx(db, func(tx database.Store, ftx forkdb.Store) error {
			var err error
			org, plugin, err = insert(tx, ftx)
			require.NoError(t, err)
			return errRollback
		}, nil)
		require.ErrorIs(t, err, errRollback)

		_, err = db.GetOrganizationByID(ctx, org.ID)
		require.ErrorIs(t, err, sql.ErrNoRows)
		_, err = fdb.GetPluginByID(ctx, plugin.ID)
		require.ErrorIs(t, err, sql.ErrNoRows)
	})
}

// wrap wraps db in dbauthz like coderd does.
func wrap(t *testing.T, db database.Store) database.Store {
	t.Helper()
	authz := rbac.NewAuthorizer(prometheus.NewRegistry())
	return dbauthz.New(db, authz, slogtest.Make(t, nil), coderdtest.AccessControlStorePointer())
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
