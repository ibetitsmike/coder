package dockerruntime_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/provisioner/sandbox"
	"github.com/coder/coder/v2/provisioner/sandbox/dockerruntime"
	"github.com/coder/coder/v2/provisioner/sandbox/sandboxtest"
	"github.com/coder/coder/v2/testutil"
)

// TestRuntimeContract runs the runtime contract against a Docker daemon.
// fork-ci sets FORK_SANDBOX_DOCKER_TEST=1, and then a missing daemon fails
// the test instead of skipping it.
func TestRuntimeContract(t *testing.T) {
	t.Parallel()
	if os.Getenv("FORK_SANDBOX_DOCKER_TEST") != "1" {
		t.Skip("set FORK_SANDBOX_DOCKER_TEST=1 to run against a Docker daemon")
	}
	ctx := testutil.Context(t, testutil.WaitLong)
	rt, err := dockerruntime.New(ctx)
	require.NoError(t, err, "FORK_SANDBOX_DOCKER_TEST=1 needs a Docker daemon")
	t.Cleanup(func() { _ = rt.Close() })
	sandboxtest.RuntimeContract(t, "alpine:3.21", func(*testing.T) sandbox.Runtime {
		return rt
	})
}
