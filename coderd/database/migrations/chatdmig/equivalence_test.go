package chatdmig_test

import (
	"database/sql"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/migrations"
	"github.com/coder/coder/v2/coderd/database/migrations/chatdmig"
	"github.com/coder/coder/v2/cryptorand"
)

// deployedVersion is the core version that the fork's first deployment ran
// before it switched to the fork (Coder v2.39.0).
const deployedVersion = 634

// TestRoutedSchemaMatchesUpstream migrates one database with upstream's
// numbered migrations and one with the routed graph, without fork nodes.
// The schemas must be the same, except for the chatd schema name.
func TestRoutedSchemaMatchesUpstream(t *testing.T) {
	t.Parallel()
	admin := adminURL(t)
	g, err := migrations.LoadGraph()
	require.NoError(t, err)

	upstreamURL, upstreamDB := createDB(t, admin)
	require.NoError(t, migrations.Up(upstreamDB))
	require.ErrorContains(t, migrations.EnsureCleanAll(upstreamDB), "needs chatd migration")

	routedURL, routedDB := createDB(t, admin)
	require.NoError(t, migrations.UpGraph(routedDB, g.WithoutForkNodes()))

	requireSameBlocks(t, dumpBlocks(t, upstreamURL, true), dumpBlocks(t, routedURL, true))

	var left []string
	rows, err := routedDB.Query(`
		SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relname ~ 'chat'
		UNION ALL
		SELECT t.typname FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
		WHERE n.nspname = 'public' AND t.typname ~ 'chat'
		UNION ALL
		SELECT p.proname FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname = 'public' AND p.proname ~ '(chat|skill|mcp_server)'`)
	require.NoError(t, err)
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		left = append(left, name)
	}
	require.NoError(t, rows.Err())
	require.Empty(t, left, "chat objects left in public")

	// A core trigger calls a moved function whose body names chat tables.
	_, err = routedDB.Exec(`DELETE FROM organization_members`)
	require.NoError(t, err)
}

// TestUpgradeMatchesFresh upgrades databases from older core versions and
// compares them with a fresh database.
func TestUpgradeMatchesFresh(t *testing.T) {
	t.Parallel()
	admin := adminURL(t)
	g, err := migrations.LoadGraph()
	require.NoError(t, err)

	freshURL, freshDB := createDB(t, admin)
	require.NoError(t, migrations.UpAll(freshDB))
	require.NoError(t, migrations.EnsureCleanAll(freshDB))
	fresh := dumpBlocks(t, freshURL, false)

	for _, from := range []uint{deployedVersion, g.SplitVersion} {
		t.Run(fmt.Sprint(from), func(t *testing.T) {
			t.Parallel()
			oldURL, oldDB := createDB(t, admin)
			require.NoError(t, migrations.UpWithFS(oldDB, upstreamUpTo(t, from)))
			require.NoError(t, migrations.UpAll(oldDB))
			require.NoError(t, migrations.EnsureCleanAll(oldDB))
			requireSameBlocks(t, fresh, dumpBlocks(t, oldURL, false))
		})
	}

	// A database that ran every node but one, as when the node lands on
	// main, gets the same schema once the node runs.
	for _, n := range g.Nodes {
		if n.Source != chatdmig.SourceFork || n.ID == chatdmig.AdoptNodeID {
			continue
		}
		t.Run("without "+n.Name, func(t *testing.T) {
			t.Parallel()
			oldURL, oldDB := createDB(t, admin)
			require.NoError(t, migrations.UpGraph(oldDB, withoutNode(g, n)))
			require.NoError(t, migrations.UpAll(oldDB))
			require.NoError(t, migrations.EnsureCleanAll(oldDB))
			requireSameBlocks(t, fresh, dumpBlocks(t, oldURL, false))
		})
	}
}

// withoutNode returns g without n and the nodes that need n.
func withoutNode(g *chatdmig.Graph, n *chatdmig.Node) *chatdmig.Graph {
	out := *g
	out.Nodes = nil
	dropped := map[uuid.UUID]bool{n.ID: true}
	// g.Nodes is in topological order, so parents come first.
	for _, m := range g.Nodes {
		for _, r := range m.Requires {
			if dropped[r.Node] {
				dropped[m.ID] = true
			}
		}
		if !dropped[m.ID] {
			out.Nodes = append(out.Nodes, m)
		}
	}
	return &out
}

// chatdObject matches a dump block that creates or changes an object in
// chatd. pg_dump names public objects without a schema.
var chatdObject = regexp.MustCompile(`^(CREATE (TABLE|TYPE|FUNCTION|SEQUENCE|VIEW|(UNIQUE )?INDEX \S+ ON|TRIGGER .+\bON)|ALTER (TABLE( ONLY)?|SEQUENCE)|COMMENT ON \w+) chatd\.`)

// enumBlock matches a dump block that creates an enum type.
var enumBlock = regexp.MustCompile(`^CREATE TYPE (\S+) AS ENUM \(`)

// TestForkNodesOnlyAddToChatd migrates one database with the routed graph
// and one with every node. Fork nodes may create objects in chatd and add
// values to enums, and nothing else. So upstream's dump.sql, and the queries
// that sqlc generates from it, still describe every core object.
func TestForkNodesOnlyAddToChatd(t *testing.T) {
	t.Parallel()
	admin := adminURL(t)
	g, err := migrations.LoadGraph()
	require.NoError(t, err)

	routedURL, routedDB := createDB(t, admin)
	require.NoError(t, migrations.UpGraph(routedDB, g.WithoutForkNodes()))
	fullURL, fullDB := createDB(t, admin)
	require.NoError(t, migrations.UpAll(fullDB))
	routed := dumpBlocks(t, routedURL, false)
	full := dumpBlocks(t, fullURL, false)

	enumValues := regexp.MustCompile(`'([^']*)'`)
	fullEnums := map[string][]string{}
	for b := range full {
		if m := enumBlock.FindStringSubmatch(b); m != nil {
			for _, v := range enumValues.FindAllStringSubmatch(b, -1) {
				fullEnums[m[1]] = append(fullEnums[m[1]], v[1])
			}
		}
	}
	for b := range routed {
		if full[b] > 0 {
			continue
		}
		m := enumBlock.FindStringSubmatch(b)
		require.NotNil(t, m, "a fork node changed or dropped an object:\n%s", b)
		var values []string
		for _, v := range enumValues.FindAllStringSubmatch(b, -1) {
			values = append(values, v[1])
		}
		require.Subset(t, fullEnums[m[1]], values, "a fork node dropped a value of enum %s", m[1])
	}
	for b := range full {
		if routed[b] > 0 || enumBlock.MatchString(b) {
			continue
		}
		require.Regexp(t, chatdObject, b, "a fork node created an object outside chatd")
	}
}

func adminURL(t *testing.T) string {
	t.Helper()
	u := os.Getenv("CHATDMIG_TEST_DATABASE_URL")
	if u == "" {
		t.Skip("set CHATDMIG_TEST_DATABASE_URL to a Postgres URL that can create databases")
	}
	return u
}

func createDB(t *testing.T, admin string) (string, *sql.DB) {
	t.Helper()
	suffix, err := cryptorand.StringCharset(cryptorand.Lower, 10)
	require.NoError(t, err)
	name := "chatdmig_test_" + suffix

	adminDB, err := sql.Open("postgres", admin)
	require.NoError(t, err)
	t.Cleanup(func() { _ = adminDB.Close() })
	_, err = adminDB.Exec("CREATE DATABASE " + name)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = adminDB.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)") })

	u, err := url.Parse(admin)
	require.NoError(t, err)
	u.Path = "/" + name
	dsn := migrations.WithChatdSearchPath(u.String())
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return dsn, db
}

// upstreamUpTo returns upstream's migrations up to version v.
func upstreamUpTo(t *testing.T, v uint) fs.FS {
	t.Helper()
	files, err := fs.Glob(os.DirFS(".."), "*.sql")
	require.NoError(t, err)
	out := fstest.MapFS{}
	for _, f := range files {
		var version uint
		if _, err := fmt.Sscanf(f, "%06d_", &version); err != nil || version > v {
			continue
		}
		b, err := os.ReadFile("../" + f)
		require.NoError(t, err)
		out[f] = &fstest.MapFile{Data: b}
	}
	return out
}

var chatdQualifier = regexp.MustCompile(`(\W)chatd\.([a-z_"])`)

// dumpBlocks splits a schema dump into blocks. With stripChatd, it removes
// the chatd schema name, the schema and the ledger.
func dumpBlocks(t *testing.T, dsn string, stripChatd bool) map[string]int {
	t.Helper()
	dump, err := dbtestutil.PGDumpSchemaOnly(dsn)
	require.NoError(t, err)
	s := string(dump)
	if stripChatd {
		s = chatdQualifier.ReplaceAllString(s, "$1$2")
	}
	blocks := map[string]int{}
	for _, b := range strings.Split(s, "\n\n") {
		b = strings.TrimSpace(b)
		if b == "" || (stripChatd && (b == "CREATE SCHEMA chatd;" || strings.Contains(b, "migration_nodes"))) {
			continue
		}
		blocks[b]++
	}
	return blocks
}

func requireSameBlocks(t *testing.T, want, got map[string]int) {
	t.Helper()
	var missing, extra []string
	for b, n := range want {
		if got[b] < n {
			missing = append(missing, b)
		}
	}
	for b, n := range got {
		if want[b] < n {
			extra = append(extra, b)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	require.Empty(t, missing, "blocks only in the expected schema")
	require.Empty(t, extra, "blocks only in the migrated schema")
}

// TestRoutingMatchesUpstream moves the split back to the deployed version,
// so the newer upstream migrations go through the router as a sync would
// route them. Fresh and upgraded databases must match upstream's schema.
func TestRoutingMatchesUpstream(t *testing.T) {
	t.Parallel()
	admin := adminURL(t)
	g := earlySplitGraph(t, deployedVersion)
	var routed []uint
	for _, r := range g.Routes {
		if r.Ledger != chatdmig.LedgerCore {
			routed = append(routed, r.Version)
		}
	}
	require.NotEmpty(t, routed, "the test needs at least one routed migration")

	upstreamURL, upstreamDB := createDB(t, admin)
	require.NoError(t, migrations.Up(upstreamDB))
	upstream := dumpBlocks(t, upstreamURL, true)

	freshURL, freshDB := createDB(t, admin)
	require.NoError(t, migrations.UpGraph(freshDB, g))
	requireSameBlocks(t, upstream, dumpBlocks(t, freshURL, true))

	var versions []uint
	rows, err := freshDB.Query(`SELECT upstream_version FROM chatd.migration_nodes WHERE source = 'upstream' ORDER BY applied_at, upstream_version`)
	require.NoError(t, err)
	for rows.Next() {
		var v uint
		require.NoError(t, rows.Scan(&v))
		versions = append(versions, v)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, routed, versions)

	oldURL, oldDB := createDB(t, admin)
	require.NoError(t, migrations.UpWithFS(oldDB, upstreamUpTo(t, deployedVersion-10)))
	require.NoError(t, migrations.UpGraph(oldDB, g))
	requireSameBlocks(t, dumpBlocks(t, freshURL, false), dumpBlocks(t, oldURL, false))
}

// earlySplitGraph builds a graph whose split is at version split, with the
// later upstream migrations routed by the classifier.
func earlySplitGraph(t *testing.T, split uint) *chatdmig.Graph {
	t.Helper()
	dump, err := os.ReadFile("../../dump.sql")
	require.NoError(t, err)
	classifier := chatdmig.NewClassifier(string(dump))
	files, err := fs.Glob(os.DirFS(".."), "*.up.sql")
	require.NoError(t, err)
	sort.Strings(files)

	var splitName string
	var routes strings.Builder
	for _, f := range files {
		var v uint
		if _, err := fmt.Sscanf(f, "%06d_", &v); err != nil {
			continue
		}
		name := strings.TrimSuffix(f[7:], ".up.sql")
		switch {
		case v == split:
			splitName = name
		case v > split:
			sql, err := os.ReadFile("../" + f)
			require.NoError(t, err)
			_, _ = fmt.Fprintf(&routes, "%06d %s %s\n", v, name, classifier.Classify(string(sql)))
		}
	}
	require.NotEmpty(t, splitName)

	adopt, err := os.ReadFile(adoptFile)
	require.NoError(t, err)
	adopt = regexp.MustCompile(`(?m)^-- requires: upstream:\w+$`).ReplaceAll(adopt, []byte("-- requires: upstream:"+splitName))
	fork := fstest.MapFS{
		"routes.txt": {Data: []byte("split " + splitName + "\n" + routes.String())},
		adoptFile:    {Data: adopt},
	}
	// A mixed migration runs its override.
	overrides, err := fs.Glob(os.DirFS("."), "override/*.up.sql")
	require.NoError(t, err)
	for _, f := range overrides {
		b, err := os.ReadFile(f)
		require.NoError(t, err)
		fork[f] = &fstest.MapFile{Data: b}
	}
	g, err := chatdmig.LoadFS(os.DirFS(".."), fork)
	require.NoError(t, err)
	return g
}
