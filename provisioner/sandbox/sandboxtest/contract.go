package sandboxtest

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/provisioner/sandbox"
	"github.com/coder/coder/v2/testutil"
)

// RuntimeContract checks that a sandbox.Runtime behaves as the engine
// expects. Each subtest makes one container from image with a long sleep as
// its entrypoint, under an ID of its own, and removes it when it ends, so
// the runtime may be shared and run the subtests in parallel.
func RuntimeContract(t *testing.T, image string, newRuntime func(t *testing.T) sandbox.Runtime) {
	t.Helper()

	setup := func(t *testing.T) (sandbox.Runtime, sandbox.CreateRequest) {
		t.Helper()
		rt := newRuntime(t)
		req := sandbox.CreateRequest{
			ID:         "sbx-contract-" + uuid.NewString(),
			Image:      image,
			Entrypoint: []string{"sleep", "300"},
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), testutil.WaitSuperLong)
			defer cancel()
			_ = rt.Remove(ctx, req.ID)
		})
		return rt, req
	}
	create := func(t *testing.T, rt sandbox.Runtime, req sandbox.CreateRequest) sandbox.Container {
		t.Helper()
		c, err := rt.Create(t.Context(), req)
		require.NoError(t, err)
		return c
	}
	state := func(t *testing.T, rt sandbox.Runtime, id string) sandbox.Container {
		t.Helper()
		c, err := rt.Status(t.Context(), id)
		require.NoError(t, err)
		return c
	}

	t.Run("CreateThenStatus", func(t *testing.T) {
		t.Parallel()
		rt, req := setup(t)
		c := create(t, rt, req)
		require.Equal(t, req.ID, c.ID)
		require.Equal(t, image, c.Image)
		require.Equal(t, sandbox.ContainerStateCreated, c.State)
		require.Equal(t, c, state(t, rt, req.ID))
	})

	t.Run("DoubleCreateRejected", func(t *testing.T) {
		t.Parallel()
		rt, req := setup(t)
		create(t, rt, req)
		_, err := rt.Create(t.Context(), req)
		require.ErrorIs(t, err, sandbox.ErrExists)
	})

	t.Run("StartIdempotent", func(t *testing.T) {
		t.Parallel()
		rt, req := setup(t)
		create(t, rt, req)
		require.NoError(t, rt.Start(t.Context(), req.ID))
		first := state(t, rt, req.ID)
		require.Equal(t, sandbox.ContainerStateRunning, first.State)
		require.NoError(t, rt.Start(t.Context(), req.ID))
		got := state(t, rt, req.ID)
		require.Equal(t, sandbox.ContainerStateRunning, got.State)
		require.Equal(t, first.StartedAt, got.StartedAt, "a second start must not restart the container")
	})

	t.Run("StopIdempotent", func(t *testing.T) {
		t.Parallel()
		rt, req := setup(t)
		create(t, rt, req)
		require.NoError(t, rt.Start(t.Context(), req.ID))
		require.NoError(t, rt.Stop(t.Context(), req.ID, time.Second))
		require.NoError(t, rt.Stop(t.Context(), req.ID, time.Second))
		require.Equal(t, sandbox.ContainerStateStopped, state(t, rt, req.ID).State)
	})

	t.Run("StopCreatedKeepsState", func(t *testing.T) {
		t.Parallel()
		rt, req := setup(t)
		create(t, rt, req)
		require.NoError(t, rt.Stop(t.Context(), req.ID, time.Second))
		require.Equal(t, sandbox.ContainerStateCreated, state(t, rt, req.ID).State)
	})

	t.Run("RemoveThenRecreate", func(t *testing.T) {
		t.Parallel()
		rt, req := setup(t)
		create(t, rt, req)
		require.NoError(t, rt.Start(t.Context(), req.ID))
		require.NoError(t, rt.Stop(t.Context(), req.ID, time.Second))
		require.NoError(t, rt.Remove(t.Context(), req.ID))
		_, err := rt.Status(t.Context(), req.ID)
		require.ErrorIs(t, err, sandbox.ErrNotFound)
		require.ErrorIs(t, rt.Remove(t.Context(), req.ID), sandbox.ErrNotFound)
		require.Equal(t, sandbox.ContainerStateCreated, create(t, rt, req).State)
	})

	t.Run("VolumeOutlivesTheContainer", func(t *testing.T) {
		t.Parallel()
		rt, req := setup(t)
		vol := req.ID + "-data"
		req.Volumes = []sandbox.VolumeMount{{Name: vol, Path: "/data"}}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), testutil.WaitSuperLong)
			defer cancel()
			_ = rt.Remove(ctx, req.ID)
			_ = rt.RemoveVolume(ctx, vol)
		})
		volumes := func() []string {
			t.Helper()
			names, err := rt.Volumes(t.Context(), req.ID+"-")
			require.NoError(t, err)
			return names
		}
		create(t, rt, req)
		require.Equal(t, []string{vol}, volumes())
		err := rt.RemoveVolume(t.Context(), vol)
		require.Error(t, err, "a mounted volume must not be removed")
		require.NotErrorIs(t, err, sandbox.ErrNotFound)
		require.NoError(t, rt.Remove(t.Context(), req.ID))
		require.Equal(t, []string{vol}, volumes())
		create(t, rt, req)
		require.NoError(t, rt.Remove(t.Context(), req.ID))
		require.NoError(t, rt.RemoveVolume(t.Context(), vol))
		require.Empty(t, volumes())
		require.ErrorIs(t, rt.RemoveVolume(t.Context(), vol), sandbox.ErrNotFound)
	})

	t.Run("UnknownRejected", func(t *testing.T) {
		t.Parallel()
		rt, req := setup(t)
		require.ErrorIs(t, rt.Start(t.Context(), req.ID), sandbox.ErrNotFound)
		require.ErrorIs(t, rt.Stop(t.Context(), req.ID, time.Second), sandbox.ErrNotFound)
		require.ErrorIs(t, rt.Remove(t.Context(), req.ID), sandbox.ErrNotFound)
		_, err := rt.Status(t.Context(), req.ID)
		require.ErrorIs(t, err, sandbox.ErrNotFound)
	})
}
