package sandbox_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/provisioner/sandbox"
)

func TestFakeRuntime(t *testing.T) {
	t.Parallel()

	t.Run("CreateThenStatus", func(t *testing.T) {
		t.Parallel()
		rt := sandbox.NewFakeRuntime()
		c, err := rt.Create(t.Context(), sandbox.CreateRequest{ID: "w1-1", Image: "alpine"})
		require.NoError(t, err)
		require.Equal(t, sandbox.ContainerStateCreated, c.State)
		got, err := rt.Status(t.Context(), "w1-1")
		require.NoError(t, err)
		require.Equal(t, c, got)
	})

	t.Run("DoubleCreateRejected", func(t *testing.T) {
		t.Parallel()
		rt := sandbox.NewFakeRuntime()
		_, err := rt.Create(t.Context(), sandbox.CreateRequest{ID: "w1-1", Image: "alpine"})
		require.NoError(t, err)
		_, err = rt.Create(t.Context(), sandbox.CreateRequest{ID: "w1-1", Image: "alpine"})
		require.ErrorIs(t, err, sandbox.ErrExists)
	})

	t.Run("StopIdempotent", func(t *testing.T) {
		t.Parallel()
		rt := sandbox.NewFakeRuntime()
		_, err := rt.Create(t.Context(), sandbox.CreateRequest{ID: "w1-1", Image: "alpine"})
		require.NoError(t, err)
		require.NoError(t, rt.Start(t.Context(), "w1-1"))
		require.NoError(t, rt.Stop(t.Context(), "w1-1", time.Second))
		require.NoError(t, rt.Stop(t.Context(), "w1-1", time.Second))
		c, err := rt.Status(t.Context(), "w1-1")
		require.NoError(t, err)
		require.Equal(t, sandbox.ContainerStateStopped, c.State)
	})

	t.Run("StartIdempotent", func(t *testing.T) {
		t.Parallel()
		rt := sandbox.NewFakeRuntime()
		_, err := rt.Create(t.Context(), sandbox.CreateRequest{ID: "w1-1", Image: "alpine"})
		require.NoError(t, err)
		require.NoError(t, rt.Start(t.Context(), "w1-1"))
		require.NoError(t, rt.Start(t.Context(), "w1-1"))
		c, err := rt.Status(t.Context(), "w1-1")
		require.NoError(t, err)
		require.Equal(t, sandbox.ContainerStateRunning, c.State)
	})

	t.Run("RemoveAfterStop", func(t *testing.T) {
		t.Parallel()
		rt := sandbox.NewFakeRuntime()
		_, err := rt.Create(t.Context(), sandbox.CreateRequest{ID: "w1-1", Image: "alpine"})
		require.NoError(t, err)
		require.NoError(t, rt.Stop(t.Context(), "w1-1", time.Second))
		require.NoError(t, rt.Remove(t.Context(), "w1-1"))
		_, err = rt.Status(t.Context(), "w1-1")
		require.ErrorIs(t, err, sandbox.ErrNotFound)
	})

	t.Run("RemoveUnknownRejected", func(t *testing.T) {
		t.Parallel()
		rt := sandbox.NewFakeRuntime()
		err := rt.Remove(t.Context(), "nope")
		require.ErrorIs(t, err, sandbox.ErrNotFound)
	})

	t.Run("InjectedFailure", func(t *testing.T) {
		t.Parallel()
		rt := sandbox.NewFakeRuntime()
		boom := xerrors.New("boom")
		rt.Fail("Create", boom)
		_, err := rt.Create(t.Context(), sandbox.CreateRequest{ID: "w1-1", Image: "alpine"})
		require.ErrorIs(t, err, boom)
		// The injection is one-shot, so the retry succeeds.
		_, err = rt.Create(t.Context(), sandbox.CreateRequest{ID: "w1-1", Image: "alpine"})
		require.NoError(t, err)
	})

	t.Run("CallOrder", func(t *testing.T) {
		t.Parallel()
		rt := sandbox.NewFakeRuntime()
		_, err := rt.Create(t.Context(), sandbox.CreateRequest{ID: "w1-1", Image: "alpine"})
		require.NoError(t, err)
		require.NoError(t, rt.Start(t.Context(), "w1-1"))
		_, err = rt.Exec(t.Context(), sandbox.ExecRequest{ContainerID: "w1-1", Cmd: []string{"echo", "hi"}})
		require.NoError(t, err)
		require.Equal(t, []string{"Create", "Start", "Exec"}, rt.Calls())
	})

	t.Run("ExecNotRunning", func(t *testing.T) {
		t.Parallel()
		rt := sandbox.NewFakeRuntime()
		_, err := rt.Create(t.Context(), sandbox.CreateRequest{ID: "w1-1", Image: "alpine"})
		require.NoError(t, err)
		_, err = rt.Exec(t.Context(), sandbox.ExecRequest{ContainerID: "w1-1", Cmd: []string{"echo"}})
		require.ErrorIs(t, err, sandbox.ErrNotRunning)
	})

	t.Run("RestartKeepsState", func(t *testing.T) {
		t.Parallel()
		rt := sandbox.NewFakeRuntime()
		_, err := rt.Create(t.Context(), sandbox.CreateRequest{ID: "w1-1", Image: "alpine"})
		require.NoError(t, err)
		require.NoError(t, rt.Start(t.Context(), "w1-1"))

		// A crash loses the process, not the containers. A new fake built from
		// the recorded containers models the recovery.
		recovered := sandbox.NewFakeRuntime()
		for id, c := range rt.Containers() {
			_, err := recovered.Create(t.Context(), sandbox.CreateRequest{ID: id, Image: c.Image})
			require.NoError(t, err)
			if c.State == sandbox.ContainerStateRunning {
				require.NoError(t, recovered.Start(t.Context(), id))
			}
		}
		got, err := recovered.Status(t.Context(), "w1-1")
		require.NoError(t, err)
		require.Equal(t, sandbox.ContainerStateRunning, got.State)
	})
}

func BenchmarkFakeRuntimeCreateStart(b *testing.B) {
	rt := sandbox.NewFakeRuntime()
	ctx := b.Context()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := fmt.Sprintf("bench-%d", i)
		_, err := rt.Create(ctx, sandbox.CreateRequest{ID: id, Image: "alpine"})
		if err != nil {
			b.Fatal(err)
		}
		if err := rt.Start(ctx, id); err != nil {
			b.Fatal(err)
		}
		if err := rt.Remove(ctx, id); err != nil {
			b.Fatal(err)
		}
	}
}
