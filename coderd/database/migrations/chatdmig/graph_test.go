package chatdmig_test

import (
	"os"
	"testing"
	"testing/fstest"

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
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := chatdmig.LoadFS(upstreamFS(), forkFS(tc.routes, tc.nodes))
			require.ErrorContains(t, err, tc.err)
		})
	}
}
