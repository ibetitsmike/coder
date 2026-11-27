package sandboxtest_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/provisioner/sandbox"
	"github.com/coder/coder/v2/provisioner/sandbox/sandboxtest"
)

func TestFakeRuntime(t *testing.T) {
	t.Parallel()

	create := func(t *testing.T, rt *sandboxtest.FakeRuntime) sandbox.Container {
		t.Helper()
		c, err := rt.Create(t.Context(), sandbox.CreateRequest{ID: "w1-1", Image: "alpine"})
		require.NoError(t, err)
		return c
	}

	t.Run("CreateThenStatus", func(t *testing.T) {
		t.Parallel()
		rt := sandboxtest.NewFakeRuntime()
		c := create(t, rt)
		require.Equal(t, sandbox.ContainerStateCreated, c.State)
		got, err := rt.Status(t.Context(), "w1-1")
		require.NoError(t, err)
		require.Equal(t, c, got)
	})

	t.Run("DoubleCreateRejected", func(t *testing.T) {
		t.Parallel()
		rt := sandboxtest.NewFakeRuntime()
		create(t, rt)
		_, err := rt.Create(t.Context(), sandbox.CreateRequest{ID: "w1-1", Image: "alpine"})
		require.ErrorIs(t, err, sandbox.ErrExists)
	})

	t.Run("StartIdempotent", func(t *testing.T) {
		t.Parallel()
		rt := sandboxtest.NewFakeRuntime()
		create(t, rt)
		require.NoError(t, rt.Start(t.Context(), "w1-1"))
		first, err := rt.Status(t.Context(), "w1-1")
		require.NoError(t, err)
		require.NoError(t, rt.Start(t.Context(), "w1-1"))
		got, err := rt.Status(t.Context(), "w1-1")
		require.NoError(t, err)
		require.Equal(t, sandbox.ContainerStateRunning, got.State)
		require.Equal(t, first.StartedAt, got.StartedAt, "a second start must not restart the container")
	})

	t.Run("StopIdempotent", func(t *testing.T) {
		t.Parallel()
		rt := sandboxtest.NewFakeRuntime()
		create(t, rt)
		require.NoError(t, rt.Start(t.Context(), "w1-1"))
		require.NoError(t, rt.Stop(t.Context(), "w1-1", time.Second))
		require.NoError(t, rt.Stop(t.Context(), "w1-1", time.Second))
		c, err := rt.Status(t.Context(), "w1-1")
		require.NoError(t, err)
		require.Equal(t, sandbox.ContainerStateStopped, c.State)
	})

	t.Run("StopCreatedKeepsState", func(t *testing.T) {
		t.Parallel()
		rt := sandboxtest.NewFakeRuntime()
		create(t, rt)
		require.NoError(t, rt.Stop(t.Context(), "w1-1", time.Second))
		c, err := rt.Status(t.Context(), "w1-1")
		require.NoError(t, err)
		require.Equal(t, sandbox.ContainerStateCreated, c.State)
	})

	t.Run("RemoveAfterStop", func(t *testing.T) {
		t.Parallel()
		rt := sandboxtest.NewFakeRuntime()
		create(t, rt)
		require.NoError(t, rt.Stop(t.Context(), "w1-1", time.Second))
		require.NoError(t, rt.Remove(t.Context(), "w1-1"))
		_, err := rt.Status(t.Context(), "w1-1")
		require.ErrorIs(t, err, sandbox.ErrNotFound)
		require.Empty(t, rt.Containers())
	})

	t.Run("UnknownRejected", func(t *testing.T) {
		t.Parallel()
		rt := sandboxtest.NewFakeRuntime()
		require.ErrorIs(t, rt.Start(t.Context(), "nope"), sandbox.ErrNotFound)
		require.ErrorIs(t, rt.Stop(t.Context(), "nope", time.Second), sandbox.ErrNotFound)
		require.ErrorIs(t, rt.Remove(t.Context(), "nope"), sandbox.ErrNotFound)
	})

	t.Run("InjectedFailureIsOneShot", func(t *testing.T) {
		t.Parallel()
		rt := sandboxtest.NewFakeRuntime()
		boom := xerrors.New("boom")
		rt.Fail("Create", boom)
		_, err := rt.Create(t.Context(), sandbox.CreateRequest{ID: "w1-1", Image: "alpine"})
		require.ErrorIs(t, err, boom)
		require.Empty(t, rt.Containers(), "a failed create must not leave a container")
		create(t, rt)
	})

	t.Run("CallOrder", func(t *testing.T) {
		t.Parallel()
		rt := sandboxtest.NewFakeRuntime()
		create(t, rt)
		require.NoError(t, rt.Start(t.Context(), "w1-1"))
		require.NoError(t, rt.Stop(t.Context(), "w1-1", time.Second))
		require.NoError(t, rt.Remove(t.Context(), "w1-1"))
		require.Equal(t, []string{"Create", "Start", "Stop", "Remove"}, rt.Calls())
	})

	t.Run("ContainersIsACopy", func(t *testing.T) {
		t.Parallel()
		rt := sandboxtest.NewFakeRuntime()
		create(t, rt)
		snapshot := rt.Containers()
		delete(snapshot, "w1-1")
		require.Len(t, rt.Containers(), 1)
	})
}
