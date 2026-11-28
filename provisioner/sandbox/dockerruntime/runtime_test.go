package dockerruntime_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
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

// TestCreateMapsLoopbackToTheHost checks what the runtime asks the daemon
// for: the agent URL with a loopback host names the Docker host instead, the
// container maps that name to the host gateway, and other variables stay.
func TestCreateMapsLoopbackToTheHost(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var created container.CreateRequest
	docker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("Api-Version", "1.47")
			_, _ = w.Write([]byte("OK"))
		case strings.HasSuffix(r.URL.Path, "/containers/create"):
			created = container.CreateRequest{}
			if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(container.CreateResponse{ID: "id"})
		case strings.HasSuffix(r.URL.Path, "/json"):
			_ = json.NewEncoder(w).Encode(container.InspectResponse{
				Config: created.Config,
				State:  &container.State{Status: container.StateCreated},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(docker.Close)
	ctx := testutil.Context(t, testutil.WaitShort)
	rt, err := dockerruntime.New(ctx, "tcp://"+docker.Listener.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	for _, tc := range []struct{ url, want string }{
		{url: "http://127.0.0.1:3000", want: "http://host.docker.internal:3000"},
		{url: "http://LOCALHOST/", want: "http://host.docker.internal/"},
		{url: "http://[::1]:3000/coder", want: "http://host.docker.internal:3000/coder"},
		{url: "https://coder.example.com:8443", want: "https://coder.example.com:8443"},
	} {
		_, err := rt.Create(ctx, sandbox.CreateRequest{ID: "sbx-" + uuid.NewString(), Image: testImage, Env: map[string]string{
			sandbox.EnvAgentURL: tc.url,
			"APP_URL":           "http://127.0.0.1:8080",
		}})
		require.NoError(t, err, tc.url)
		mu.Lock()
		got := created
		mu.Unlock()
		require.Equal(t, []string{"APP_URL=http://127.0.0.1:8080", sandbox.EnvAgentURL + "=" + tc.want}, got.Env, tc.url)
		require.Equal(t, []string{"host.docker.internal:host-gateway"}, got.HostConfig.ExtraHosts, tc.url)
	}
}
