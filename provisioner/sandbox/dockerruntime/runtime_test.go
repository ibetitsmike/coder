package dockerruntime_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/provisioner/sandbox"
	"github.com/coder/coder/v2/provisioner/sandbox/dockerruntime"
	"github.com/coder/coder/v2/provisioner/sandbox/sandboxtest"
	"github.com/coder/coder/v2/testutil"
)

const testImage = "alpine:3.21"

// newRuntime connects to the Docker daemon. fork-ci sets
// FORK_SANDBOX_DOCKER_TEST=1, and then a missing daemon fails the test
// instead of skipping it.
func newRuntime(t *testing.T) *dockerruntime.Runtime {
	t.Helper()
	if os.Getenv("FORK_SANDBOX_DOCKER_TEST") != "1" {
		t.Skip("set FORK_SANDBOX_DOCKER_TEST=1 to run against a Docker daemon")
	}
	rt, err := dockerruntime.New(testutil.Context(t, testutil.WaitLong), "")
	require.NoError(t, err, "FORK_SANDBOX_DOCKER_TEST=1 needs a Docker daemon")
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

// TestRuntimeContract runs the runtime contract against a Docker daemon.
func TestRuntimeContract(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t)
	sandboxtest.RuntimeContract(t, testImage, func(*testing.T) sandbox.Runtime {
		return rt
	})
}

// TestForeignContainer checks that the runtime leaves alone a container of
// the same name that it did not create.
func TestForeignContainer(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t)
	ctx := testutil.Context(t, testutil.WaitSuperLong)
	c, err := client.New(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })

	pull, err := c.ImagePull(ctx, testImage, client.ImagePullOptions{})
	require.NoError(t, err)
	require.NoError(t, pull.Wait(ctx))
	_ = pull.Close()
	id := "sbx-foreign-" + uuid.NewString()
	_, err = c.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name:   id,
		Config: &container.Config{Image: testImage, Entrypoint: []string{"sleep", "300"}},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = c.ContainerRemove(context.Background(), id, client.ContainerRemoveOptions{Force: true})
	})

	_, err = rt.Create(ctx, sandbox.CreateRequest{ID: id, Image: testImage})
	require.ErrorIs(t, err, dockerruntime.ErrNotOwned)
	require.NotErrorIs(t, err, sandbox.ErrExists)
	_, err = rt.Status(ctx, id)
	require.ErrorIs(t, err, dockerruntime.ErrNotOwned)
	require.ErrorIs(t, rt.Start(ctx, id), dockerruntime.ErrNotOwned)
	require.ErrorIs(t, rt.Stop(ctx, id, time.Second), dockerruntime.ErrNotOwned)
	require.ErrorIs(t, rt.Remove(ctx, id), dockerruntime.ErrNotOwned)

	in, err := c.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	require.NoError(t, err)
	require.Equal(t, container.StateCreated, in.Container.State.Status)
}
