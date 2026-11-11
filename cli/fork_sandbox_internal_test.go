package cli

import (
	"context"
	"sync"
	"testing"

	"github.com/coder/serpent"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/provisionerd"
	"github.com/coder/coder/v2/testutil"
)

// TestForkSandboxProvisioners checks the daemon wiring: the sandbox type and
// connector entry appear only when the fork-sandbox-provisioner experiment is
// enabled.
func TestForkSandboxProvisioners(t *testing.T) {
	t.Parallel()

	t.Run("Off", func(t *testing.T) {
		t.Parallel()

		types, connector := forkSandboxProvisionersForTest(t, nil)
		require.NotContains(t, types, codersdk.ProvisionerType("sandbox"))
		require.NotContains(t, connector, "sandbox")
	})

	t.Run("On", func(t *testing.T) {
		t.Parallel()

		types, connector := forkSandboxProvisionersForTest(t, []string{string(codersdk.ExperimentForkSandboxProvisioner)})
		require.Contains(t, types, codersdk.ProvisionerType("sandbox"))
		require.Contains(t, connector, "sandbox")
	})
}

// forkSandboxProvisionersForTest builds the wiring the server builds, against
// a test API with the given experiments.
func forkSandboxProvisionersForTest(t *testing.T, experiments []string) ([]codersdk.ProvisionerType, provisionerd.LocalProvisioners) {
	t.Helper()

	_, closer, api := coderdtest.NewWithAPI(t, &coderdtest.Options{
		IncludeProvisionerDaemon: false,
		DeploymentValues:         deploymentValuesWithExperiments(t, experiments),
	})
	t.Cleanup(func() { _ = closer.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	// wg.Wait must run before cancel: cleanups run last added, first called,
	// and the wiring's pipes close only after the context is done.
	t.Cleanup(wg.Wait)
	t.Cleanup(cancel)
	return forkSandboxProvisioners(ctx, api, testutil.Logger(t), t.TempDir(),
		[]codersdk.ProvisionerType{codersdk.ProvisionerTypeTerraform}, provisionerd.LocalProvisioners{}, errCh, &wg, cancel)
}

func deploymentValuesWithExperiments(t *testing.T, experiments []string) *codersdk.DeploymentValues {
	t.Helper()

	vals := coderdtest.DeploymentValues(t)
	vals.Experiments = serpent.StringArray(experiments)
	return vals
}
