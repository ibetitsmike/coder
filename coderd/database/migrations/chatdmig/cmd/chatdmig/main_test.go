package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	routesHeader = `# Routes of upstream migrations after the split.
#
# <version> <name> <ledger: core, chatd or mixed>
split mcp_server_oauth2_client_registration
`
	earlyQueued = "# <version> <name> <source>\n000640 chat_queued_message_busy_behavior coder/coder#30073\n"
)

// writeMigrations lays out dir/migrations like the real tree and returns its
// path, the dir that route reads.
func writeMigrations(t *testing.T, routes string, upstream map[string]string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "migrations")
	fork := filepath.Join(dir, "chatdmig")
	require.NoError(t, os.MkdirAll(filepath.Join(fork, "nodes"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(fork, "override"), 0o755))

	adopt := "01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema.up.sql"
	body, err := os.ReadFile(filepath.Join("..", "..", "nodes", adopt))
	require.NoError(t, err)
	files := map[string]string{
		filepath.Join(root, "dump.sql"):                                           "CREATE TABLE users (id uuid);\n",
		filepath.Join(dir, "000639_mcp_server_oauth2_client_registration.up.sql"): "SELECT 1;\n",
		filepath.Join(fork, "routes.txt"):                                         routes,
		filepath.Join(fork, "early.txt"):                                          earlyQueued,
		filepath.Join(fork, "nodes", adopt):                                       string(body),
	}
	for name, sql := range upstream {
		files[filepath.Join(dir, name)] = sql
	}
	for path, content := range files {
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}
	return dir
}

func TestRouteEarly(t *testing.T) {
	t.Parallel()

	t.Run("UpstreamBelowEarlyFails", func(t *testing.T) {
		t.Parallel()
		routes := routesHeader + "000640 chat_queued_message_busy_behavior chatd\n"
		dir := writeMigrations(t, routes, map[string]string{
			"000640_other_core.up.sql":                        "ALTER TABLE users ADD COLUMN x int;\n",
			"000641_chat_queued_message_busy_behavior.up.sql": "ALTER TABLE chat_queued_messages ADD COLUMN busy_behavior text;\n",
		})

		err := route(dir)
		require.ErrorContains(t, err, "000640_other_core")
		got, err := os.ReadFile(filepath.Join(dir, "chatdmig", "routes.txt"))
		require.NoError(t, err)
		require.Equal(t, routes, string(got))
	})

	t.Run("RoutedMigrationRenumberedBelowEarlyFails", func(t *testing.T) {
		t.Parallel()
		routes := routesHeader + "000641 after core\n000642 chat_queued_message_busy_behavior chatd\n"
		dir := writeMigrations(t, routes, map[string]string{
			"000640_after.up.sql":                             "ALTER TABLE users ADD COLUMN y int;\n",
			"000642_chat_queued_message_busy_behavior.up.sql": "ALTER TABLE chat_queued_messages ADD COLUMN busy_behavior text;\n",
		})

		require.ErrorContains(t, route(dir), "000640_after")
		got, err := os.ReadFile(filepath.Join(dir, "chatdmig", "routes.txt"))
		require.NoError(t, err)
		require.Equal(t, routes, string(got))
	})

	t.Run("MigrationRoutedBeforeEarlyEntryRoutes", func(t *testing.T) {
		t.Parallel()
		// A hand-edited ledger may leave a version unpadded.
		routes := routesHeader + "640 a core\n000641 b core\n"
		dir := writeMigrations(t, routes, map[string]string{
			"000640_a.up.sql": "ALTER TABLE users ADD COLUMN a int;\n",
			"000641_b.up.sql": "ALTER TABLE users ADD COLUMN b int;\n",
		})
		require.NoError(t, os.WriteFile(filepath.Join(dir, "chatdmig", "early.txt"), []byte("000642 chat_x coder/coder#1\n"), 0o600))

		require.NoError(t, route(dir))
	})

	t.Run("EveryEarlyMigrationRoutes", func(t *testing.T) {
		t.Parallel()
		dir := writeMigrations(t, routesHeader, map[string]string{
			"000640_a_q.up.sql": "ALTER TABLE users ADD COLUMN a int;\n",
			"000641_b_q.up.sql": "ALTER TABLE users ADD COLUMN b int;\n",
		})
		require.NoError(t, os.WriteFile(filepath.Join(dir, "chatdmig", "early.txt"), []byte("000640 a_q coder/coder#1\n000641 b_q coder/coder#1\n"), 0o600))

		require.NoError(t, route(dir))
		got, err := os.ReadFile(filepath.Join(dir, "chatdmig", "routes.txt"))
		require.NoError(t, err)
		require.Equal(t, routesHeader+"000640 a_q core\n000641 b_q core\n", string(got))
	})

	t.Run("EarlyAtOrBelowSplitFails", func(t *testing.T) {
		t.Parallel()
		dir := writeMigrations(t, routesHeader, nil)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "chatdmig", "early.txt"), []byte("000639 chat_x coder/coder#1\n"), 0o600))

		require.ErrorContains(t, route(dir), "000639_chat_x is at or below the split")
		require.ErrorContains(t, check(dir), "000639_chat_x is at or below the split")
	})

	t.Run("EarlyMigrationRoutes", func(t *testing.T) {
		t.Parallel()
		dir := writeMigrations(t, routesHeader, map[string]string{
			"000640_chat_queued_message_busy_behavior.up.sql": "ALTER TABLE chat_queued_messages ADD COLUMN busy_behavior text;\n",
			"000641_after.up.sql":                             "ALTER TABLE users ADD COLUMN y int;\n",
		})

		require.NoError(t, route(dir))
		got, err := os.ReadFile(filepath.Join(dir, "chatdmig", "routes.txt"))
		require.NoError(t, err)
		require.Equal(t, routesHeader+"000640 chat_queued_message_busy_behavior chatd\n000641 after core\n", string(got))
	})

	for _, line := range []string{"640 short", "00064x chat_x coder/coder#1", "640 chat_x coder/coder#1"} {
		t.Run("BadEarlyLineFails/"+line, func(t *testing.T) {
			t.Parallel()
			dir := writeMigrations(t, routesHeader, nil)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "chatdmig", "early.txt"), []byte(line+"\n"), 0o600))

			require.ErrorContains(t, route(dir), fmt.Sprintf("early.txt: bad line %q", line))
			require.ErrorContains(t, check(dir), fmt.Sprintf("early.txt: bad line %q", line))
		})
	}
}
