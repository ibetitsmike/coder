package sandboxtest_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/provisioner/sandbox"
	"github.com/coder/coder/v2/provisioner/sandbox/sandboxtest"
)

// TestFakeRuntime covers the fake-only behavior. The shared lifecycle cases run
// in TestFakeRuntimeContract.
func TestFakeRuntime(t *testing.T) {
	t.Parallel()

	create := func(t *testing.T, rt *sandboxtest.FakeRuntime) sandbox.Container {
		t.Helper()
		c, err := rt.Create(t.Context(), sandbox.CreateRequest{ID: "w1-1", Image: "alpine"})
		require.NoError(t, err)
		return c
	}

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
		require.Empty(t, rt.Containers(), "remove must drop the container")
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
