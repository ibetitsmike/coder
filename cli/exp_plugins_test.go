package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/cli/clitest"
	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/x/plugins/plugintest"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/serpent"
)

func TestExpPlugins(t *testing.T) {
	t.Parallel()
	wasmPath := filepath.Join(t.TempDir(), "bridge.wasm")
	require.NoError(t, os.WriteFile(wasmPath, plugintest.BuildWasm(t, "github.com/coder/coder/v2/coderd/x/plugins/testdata/bridge"), 0o600))

	values := coderdtest.DeploymentValues(t)
	values.Experiments = serpent.StringArray{string(codersdk.ExperimentPlugins)}
	client := coderdtest.New(t, &coderdtest.Options{DeploymentValues: values})
	_ = coderdtest.CreateFirstUser(t, client)

	run := func(t *testing.T, args ...string) string {
		t.Helper()
		inv, root := clitest.New(t, append([]string{"exp", "plugins"}, args...)...)
		clitest.SetupConfig(t, client, root)
		out := new(bytes.Buffer)
		inv.Stdout = out
		require.NoError(t, inv.WithContext(testutil.Context(t, testutil.WaitLong)).Run())
		return out.String()
	}
	list := func(t *testing.T) []codersdk.Plugin {
		t.Helper()
		var plugins []codersdk.Plugin
		require.NoError(t, json.Unmarshal([]byte(run(t, "list", "-o", "json")), &plugins))
		return plugins
	}

	out := run(t, "install", wasmPath, "--yes", "--config", "token=secret", "--allow-private-cidr", "10.0.0.0/8")
	require.Contains(t, out, "(as linked users)")
	require.Contains(t, out, "Installed bridge")

	plugins := list(t)
	require.Len(t, plugins, 1)
	require.Equal(t, []string{"token"}, plugins[0].ConfigKeysSet)
	require.Equal(t, []string{"10.0.0.0/8"}, plugins[0].Grants.PrivateCIDRs)

	run(t, "disable", "bridge")
	require.False(t, list(t)[0].Enabled)

	run(t, "config", "bridge")
	require.Empty(t, list(t)[0].ConfigKeysSet)

	run(t, "delete", "bridge", "--yes")
	require.Empty(t, list(t))
}
