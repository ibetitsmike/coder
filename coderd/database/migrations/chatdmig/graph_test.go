package chatdmig_test

import (
	"os"
	"testing"
	"testing/fstest"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database/migrations/chatdmig"
)

const adoptFile = "nodes/01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema.up.sql"

func upstreamFS() fstest.MapFS {
	return fstest.MapFS{
		"000001_users.up.sql":  {Data: []byte("CREATE TABLE users ();")},
		"000002_split.up.sql":  {Data: []byte("CREATE TABLE workspaces ();")},
		"000003_chat_x.up.sql": {Data: []byte("CREATE TABLE chat_x ();")},
	}
}

func forkFS(routes string, nodes map[string]string) fstest.MapFS {
	fs := fstest.MapFS{
		"routes.txt": {Data: []byte(routes)},
		adoptFile:    {Data: []byte("-- requires: upstream:split\nSELECT 1;\n")},
	}
	for name, body := range nodes {
		fs[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return fs
}

const validRoutes = "split split\n000003 chat_x chatd\n"

func TestEmbeddedGraph(t *testing.T) {
	t.Parallel()
	g, err := chatdmig.Load(os.DirFS(".."))
	require.NoError(t, err)
	require.NotNil(t, g.Node(chatdmig.AdoptNodeID))
}

func TestLoadOrdersRoutedNodesAfterAdopt(t *testing.T) {
	t.Parallel()
	g, err := chatdmig.LoadFS(upstreamFS(), forkFS(validRoutes, nil))
	require.NoError(t, err)
	require.Len(t, g.Nodes, 2)
	require.Equal(t, chatdmig.AdoptNodeID, g.Nodes[0].ID)
	require.Equal(t, chatdmig.UpstreamNodeID("chat_x"), g.Nodes[1].ID)
	require.Equal(t, uint(2), g.Nodes[0].Requires[0].CoreVersion)
}

func TestLoadRejectsBadGraphs(t *testing.T) {
	t.Parallel()
	const a = "nodes/0193f0aa-0000-7000-8000-00000000000a_a.up.sql"
	const b = "nodes/0193f0aa-0000-7000-8000-00000000000b_b.up.sql"
	const requireAdopt = "-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8\n"
	for _, tc := range []struct {
		name   string
		routes string
		nodes  map[string]string
		err    string
	}{
		{"MissingRoute", "split split\n", nil, "000003_chat_x.up.sql has no route"},
		{"RouteBeforeSplit", validRoutes + "000002 split core\n", nil, "versions up to the split"},
		{"MixedWithoutOverride", "split split\n000003 chat_x mixed\n", nil, "write override/chat_x.up.sql"},
		{"UnknownParent", validRoutes, map[string]string{
			a: requireAdopt + "-- requires: 0193f0aa-0000-7000-8000-0000000000ff\n",
		}, "requires unknown"},
		{"DuplicateID", validRoutes, map[string]string{
			a: requireAdopt,
			"nodes/0193f0aa-0000-7000-8000-00000000000a_other.up.sql": requireAdopt,
		}, "share id"},
		{"Cycle", validRoutes, map[string]string{
			a: requireAdopt + "-- requires: 0193f0aa-0000-7000-8000-00000000000b\n",
			b: requireAdopt + "-- requires: 0193f0aa-0000-7000-8000-00000000000a\n",
		}, "cycle"},
		{"NoPathToAdopt", validRoutes, map[string]string{
			a: "-- requires: upstream:users\n",
		}, "must require the adopt node"},
		{"StartupRequiresBackground", validRoutes, map[string]string{
			a: requireAdopt + "-- phase: background\nSELECT 1;\n",
			b: requireAdopt + "-- requires: 0193f0aa-0000-7000-8000-00000000000a\nSELECT 1;\n",
		}, "cannot require background node"},
		{"UnknownPhase", validRoutes, map[string]string{
			a: requireAdopt + "-- phase: later\nSELECT 1;\n",
		}, "want startup or background"},
		{"NoTransactionTwoStatements", validRoutes, map[string]string{
			a: requireAdopt + "-- transaction: none\nCREATE INDEX CONCURRENTLY a ON chats (id);\nCREATE INDEX CONCURRENTLY b ON chats (id);\n",
		}, "exactly one statement"},
		{"RepeatTwoStatements", validRoutes, map[string]string{
			a: requireAdopt + "-- repeat: until-no-rows\nUPDATE chats SET x = 1;\nUPDATE chats SET y = 1;\n",
		}, "exactly one statement"},
		{"RepeatWithoutTransaction", validRoutes, map[string]string{
			a: requireAdopt + "-- transaction: none\n-- repeat: until-no-rows\nUPDATE chats SET x = 1;\n",
		}, "cannot use transaction: none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := chatdmig.LoadFS(upstreamFS(), forkFS(tc.routes, tc.nodes))
			require.ErrorContains(t, err, tc.err)
		})
	}
}

func TestLoadReadsRunDirectives(t *testing.T) {
	t.Parallel()
	const index = "nodes/0193f0aa-0000-7000-8000-00000000000a_index.up.sql"
	const backfill = "nodes/0193f0aa-0000-7000-8000-00000000000b_backfill.up.sql"
	g, err := chatdmig.LoadFS(upstreamFS(), forkFS(validRoutes, map[string]string{
		index: "-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8\n-- transaction: none\n" +
			"-- A semicolon in a comment; or 'a string;' is not a statement.\n" +
			"CREATE INDEX CONCURRENTLY IF NOT EXISTS chats_x ON chats ((title || ';'));\n",
		backfill: "-- requires: 0193f0aa-0000-7000-8000-00000000000a_index\n-- phase: background\n-- repeat: until-no-rows\n" +
			"UPDATE chats SET x = 1 WHERE id IN (SELECT id FROM chats WHERE x IS NULL LIMIT 1000)\n",
	}))
	require.NoError(t, err)
	idx := g.Node(uuid.MustParse("0193f0aa-0000-7000-8000-00000000000a"))
	require.True(t, idx.NoTransaction)
	require.False(t, idx.Repeat)
	require.Equal(t, chatdmig.PhaseStartup, idx.Phase)
	bf := g.Node(uuid.MustParse("0193f0aa-0000-7000-8000-00000000000b"))
	require.True(t, bf.Repeat)
	require.False(t, bf.NoTransaction)
	require.Equal(t, chatdmig.PhaseBackground, bf.Phase)
}
