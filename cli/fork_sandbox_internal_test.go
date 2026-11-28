//go:build !slim

package cli

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd"
	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/coderdtest/promhelp"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/provisionerdserver"
	"github.com/coder/coder/v2/coderd/wsbuilder"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/provisioner/sandbox"
	"github.com/coder/coder/v2/provisioner/sandbox/dockerruntime"
	"github.com/coder/coder/v2/provisioner/sandbox/sandboxtest"
	"github.com/coder/coder/v2/provisionerd"
	"github.com/coder/coder/v2/provisionerd/proto"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/serpent"
)

const sandboxSpec = `version: 1
container:
  image: docker.io/library/alpine:3.20
agent:
  name: main
`

func TestForkSandboxProvisioners(t *testing.T) {
	t.Parallel()

	// The test FakeRuntime is never the default.
	_, err := sandboxRuntime(context.Background(), codersdk.ForkSandboxConfig{})
	require.ErrorIs(t, err, errNoSandboxRuntime)
	_, err = sandboxRuntime(context.Background(), codersdk.ForkSandboxConfig{Runtime: "nope"})
	require.ErrorContains(t, err, `unknown sandbox runtime "nope"`)

	experiment := []string{string(codersdk.ExperimentForkSandboxProvisioner)}
	for _, tc := range []struct {
		name        string
		experiments []string
		runtime     string
		registered  bool
	}{
		{name: "ExperimentOff", runtime: codersdk.SandboxRuntimeDocker},
		{name: "NoRuntime", experiments: experiment},
		{name: "ExperimentOnWithRuntime", experiments: experiment, runtime: codersdk.SandboxRuntimeDocker, registered: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// A Docker daemon that only answers pings is enough to register the
			// docker runtime. It counts the runtimes made against it.
			var pings atomic.Int64
			docker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/_ping") {
					http.NotFound(w, r)
					return
				}
				pings.Add(1)
				w.Header().Set("Api-Version", "1.47")
				_, _ = w.Write([]byte("OK"))
			}))
			t.Cleanup(docker.Close)
			dv := coderdtest.DeploymentValues(t)
			dv.Experiments = tc.experiments
			dv.Sandbox.Runtime = serpent.String(tc.runtime)
			dv.Sandbox.DockerHost = serpent.String("tcp://" + docker.Listener.Addr().String())
			_, closer, api := coderdtest.NewWithAPI(t, &coderdtest.Options{DeploymentValues: dv})
			t.Cleanup(func() { _ = closer.Close() })

			// The server calls the hook once for each built-in daemon.
			for range 2 {
				ctx, cancel := context.WithCancel(context.Background())
				var wg sync.WaitGroup
				t.Cleanup(func() {
					cancel()
					wg.Wait()
				})
				echo := []codersdk.ProvisionerType{codersdk.ProvisionerTypeEcho}
				types, connector := forkSandboxProvisioners(ctx, api, testutil.Logger(t), t.TempDir(),
					echo, provisionerd.LocalProvisioners{}, make(chan error, 1), &wg, cancel)
				if !tc.registered {
					require.Equal(t, echo, types)
					require.Empty(t, connector)
					continue
				}
				require.Equal(t, append(echo, codersdk.ProvisionerTypeSandbox), types)
				require.Contains(t, connector, string(codersdk.ProvisionerTypeSandbox))
			}
			if tc.registered {
				// The daemons share one runtime, and so one set of
				// per-container locks.
				require.EqualValues(t, 1, pings.Load())
			}
		})
	}
}

// TestForkSandboxAccessHost checks that the access URL redirect lets through
// the host a Docker sandbox container reaches a loopback access URL on.
func TestForkSandboxAccessHost(t *testing.T) {
	t.Parallel()

	served := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	for _, tc := range []struct {
		name       string
		experiment bool
		runtime    string
		accessURL  string
		host       string
		want       int
	}{
		{name: "ContainerHost", experiment: true, runtime: codersdk.SandboxRuntimeDocker, accessURL: "http://127.0.0.1:3000", host: "host.docker.internal:3000", want: http.StatusOK},
		{name: "OtherPort", experiment: true, runtime: codersdk.SandboxRuntimeDocker, accessURL: "http://127.0.0.1:3000", host: "host.docker.internal:4000", want: http.StatusTemporaryRedirect},
		{name: "HTTPSWithoutTLS", experiment: true, runtime: codersdk.SandboxRuntimeDocker, accessURL: "https://localhost:3443", host: "host.docker.internal:3443", want: http.StatusTemporaryRedirect},
		{name: "ExperimentOff", runtime: codersdk.SandboxRuntimeDocker, accessURL: "http://127.0.0.1:3000", host: "host.docker.internal:3000", want: http.StatusTemporaryRedirect},
		{name: "NoDockerRuntime", experiment: true, accessURL: "http://127.0.0.1:3000", host: "host.docker.internal:3000", want: http.StatusTemporaryRedirect},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			accessURL, err := url.Parse(tc.accessURL)
			require.NoError(t, err)
			dv := &codersdk.DeploymentValues{}
			dv.Sandbox.Runtime = serpent.String(tc.runtime)
			api := &coderd.API{Options: &coderd.Options{AccessURL: accessURL, DeploymentValues: dv}}
			if tc.experiment {
				api.Experiments = codersdk.Experiments{codersdk.ExperimentForkSandboxProvisioner}
			}
			handler := forkSandboxAccessHost(api, served, redirectToAccessURL(served, accessURL, false, nil))
			req := httptest.NewRequest(http.MethodGet, "/api/v2/buildinfo", nil)
			req.Host = tc.host
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			require.Equal(t, tc.want, rec.Code)
		})
	}
}

// TestSandboxProvisionerEndToEnd runs a sandbox template and workspace
// through coderd, the provisionerd runner, the engine, the operation log in
// the database and the fake runtime.
func TestSandboxProvisionerEndToEnd(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	jobMetrics := provisionerdserver.NewMetrics(testutil.Logger(t))
	require.NoError(t, jobMetrics.Register(reg))
	buildMetrics, err := wsbuilder.NewMetrics(reg)
	require.NoError(t, err)
	rt := sandboxtest.NewFakeRuntime()
	client, api, user := newSandboxServer(t, rt, &coderdtest.Options{
		ProvisionerdServerMetrics: jobMetrics,
		WorkspaceBuilderMetrics:   buildMetrics,
	})
	template := createSandboxTemplate(t, client, user.OrganizationID, sandboxSpec)

	ws := coderdtest.CreateWorkspace(t, client, template.ID)
	start := coderdtest.AwaitWorkspaceBuildJobCompleted(t, client, ws.LatestBuild.ID)
	require.Equal(t, codersdk.ProvisionerJobSucceeded, start.Job.Status, start.Job.Error)
	require.Len(t, start.Resources, 1)
	require.Equal(t, "sandbox_container", start.Resources[0].Type)
	name := sandbox.ContainerName(ws.ID)
	require.Equal(t, sandbox.ContainerStateRunning, rt.Containers()[name].State)
	creates := rt.Creates()
	require.Len(t, creates, 1)
	require.Equal(t, agentToken(t, api, start.Job.ID).String(), creates[0].Env[sandbox.EnvAgentToken])

	build := func(transition database.WorkspaceTransition) codersdk.WorkspaceBuild {
		b := coderdtest.CreateWorkspaceBuild(t, client, ws, transition)
		b = coderdtest.AwaitWorkspaceBuildJobCompleted(t, client, b.ID)
		require.Equal(t, codersdk.ProvisionerJobSucceeded, b.Job.Status, b.Job.Error)
		return b
	}
	build(database.WorkspaceTransitionStop)
	require.Equal(t, sandbox.ContainerStateStopped, rt.Containers()[name].State)
	// A start replaces the container and keeps the token coderd knows.
	restart := build(database.WorkspaceTransitionStart)
	require.Equal(t, sandbox.ContainerStateRunning, rt.Containers()[name].State)
	require.Len(t, rt.Creates(), 2)
	require.Equal(t, creates[0].Env[sandbox.EnvAgentToken], rt.Creates()[1].Env[sandbox.EnvAgentToken])
	require.Equal(t, creates[0].Env[sandbox.EnvAgentToken], agentToken(t, api, restart.Job.ID).String())
	build(database.WorkspaceTransitionDelete)
	require.Empty(t, rt.Containers())

	// The metric sites count sandbox jobs, which upstream's
	// ProvisionerType.Valid does not know.
	sandboxType := string(codersdk.ProvisionerTypeSandbox)
	require.Equal(t, 2, promhelp.CounterValue(t, reg, "coderd_workspace_builds_enqueued_total", prometheus.Labels{
		"provisioner_type": sandboxType, "build_reason": string(database.BuildReasonInitiator),
		"transition": string(database.WorkspaceTransitionStart), "status": wsbuilder.BuildStatusSuccess,
	}))
	wait := promhelp.HistogramValue(t, reg, "coderd_provisioner_job_queue_wait_seconds", prometheus.Labels{
		"provisioner_type": sandboxType, "job_type": string(database.ProvisionerJobTypeWorkspaceBuild),
		"transition": string(database.WorkspaceTransitionStart), "build_reason": string(database.BuildReasonInitiator),
	})
	require.Equal(t, uint64(2), wait.GetSampleCount())
}

// TestSandboxAgentConnects starts a sandbox workspace in a Docker container
// that sets no entrypoint, and waits for the agent that the bootstrap
// downloads from coderd to connect. The access URL is on loopback, so the
// agent only reaches coderd through the host the Docker runtime maps it to.
func TestSandboxAgentConnects(t *testing.T) {
	t.Parallel()
	if os.Getenv("FORK_SANDBOX_DOCKER_TEST") != "1" {
		t.Skip("set FORK_SANDBOX_DOCKER_TEST=1 to run against a Docker daemon")
	}
	arch := runtime.GOARCH
	if arch != "amd64" && arch != "arm64" {
		t.Skipf("no sandbox agent for %s", arch)
	}
	ctx := testutil.Context(t, 5*time.Minute)
	rt, err := dockerruntime.New(ctx, "")
	require.NoError(t, err, "FORK_SANDBOX_DOCKER_TEST=1 needs a Docker daemon")
	t.Cleanup(func() { _ = rt.Close() })

	agentBin := filepath.Join(t.TempDir(), "coder-linux-"+arch)
	build := exec.CommandContext(ctx, "go", "build", "-tags", "slim", "-o", agentBin, "../cmd/coder")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+arch)
	out, err := build.CombinedOutput()
	require.NoError(t, err, string(out))

	// coderdtest listens on 127.0.0.1, which a container cannot reach. A
	// proxy on every interface stands in for the deployment: it serves the
	// agent binary, which coderdtest does not embed, and passes the rest to
	// coderd. The access URL names the proxy on loopback.
	l, err := net.Listen("tcp", ":0") //nolint:gosec // The container reaches the host on the Docker bridge.
	require.NoError(t, err)
	tcpAddr, ok := l.Addr().(*net.TCPAddr)
	require.True(t, ok)
	client, _, user := newSandboxServer(t, rt, &coderdtest.Options{
		AccessURL: &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", tcpAddr.Port)},
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/bin/coder-linux-"+arch, func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, agentBin)
	})
	mux.Handle("/", httputil.NewSingleHostReverseProxy(client.URL))
	proxy := &httptest.Server{Listener: l, Config: &http.Server{Handler: mux, ReadHeaderTimeout: testutil.WaitShort}}
	proxy.Start()
	t.Cleanup(proxy.Close)

	template := createSandboxTemplate(t, client, user.OrganizationID, fmt.Sprintf(`version: 1
container:
  image: docker.io/library/alpine:3.21
agent:
  name: main
  arch: %s
`, arch))
	ws := coderdtest.CreateWorkspace(t, client, template.ID)
	name := sandbox.ContainerName(ws.ID)
	t.Cleanup(func() {
		if t.Failed() {
			logs, _ := exec.Command("docker", "logs", name).CombinedOutput()
			t.Logf("container logs:\n%s", logs)
		}
		_ = rt.Remove(context.Background(), name)
	})
	start := coderdtest.AwaitWorkspaceBuildJobCompleted(t, client, ws.LatestBuild.ID)
	require.Equal(t, codersdk.ProvisionerJobSucceeded, start.Job.Status, start.Job.Error)
	coderdtest.NewWorkspaceAgentWaiter(t, client, ws.ID).WithContext(ctx).Wait()
}

// newSandboxServer starts coderd with the sandbox experiment and a
// provisioner daemon that runs sandbox jobs on rt.
func newSandboxServer(t *testing.T, rt sandbox.Runtime, opts *coderdtest.Options) (*codersdk.Client, *coderd.API, codersdk.CreateFirstUserResponse) {
	t.Helper()

	if opts.DeploymentValues == nil {
		opts.DeploymentValues = coderdtest.DeploymentValues(t)
	}
	opts.DeploymentValues.Experiments = []string{string(codersdk.ExperimentForkSandboxProvisioner)}
	client, closer, api := coderdtest.NewWithAPI(t, opts)
	t.Cleanup(func() { _ = closer.Close() })
	user := coderdtest.CreateFirstUser(t, client)

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})
	types, connector, err := serveSandboxProvisioner(ctx, api, rt, testutil.Logger(t), t.TempDir(),
		nil, provisionerd.LocalProvisioners{}, make(chan error, 1), &wg, cancel)
	require.NoError(t, err)
	require.Equal(t, []codersdk.ProvisionerType{codersdk.ProvisionerTypeSandbox}, types)
	connected := make(chan struct{})
	daemon := provisionerd.New(func(dialCtx context.Context) (proto.DRPCProvisionerDaemonClient, error) {
		return api.CreateInMemoryTaggedProvisionerDaemon(dialCtx, "sandbox", types, nil)
	}, &provisionerd.Options{
		Logger:              testutil.Logger(t),
		UpdateInterval:      250 * time.Millisecond,
		ForceCancelInterval: 5 * time.Second,
		Connector:           connector,
		InitConnectionCh:    connected,
	})
	t.Cleanup(func() { _ = coderdtest.NewProvisionerDaemonCloser(daemon).Close() })
	testutil.TryReceive(testutil.Context(t, testutil.WaitShort), t, connected)
	return client, api, user
}

// createSandboxTemplate makes a template from a sandbox spec.
func createSandboxTemplate(t *testing.T, client *codersdk.Client, orgID uuid.UUID, spec string) codersdk.Template {
	t.Helper()

	ctx := testutil.Context(t, testutil.WaitLong)
	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	require.NoError(t, w.WriteHeader(&tar.Header{Name: sandbox.SpecFileName, Mode: 0o600, Size: int64(len(spec))}))
	_, err := w.Write([]byte(spec))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	file, err := client.Upload(ctx, codersdk.ContentTypeTar, &buf)
	require.NoError(t, err)
	version, err := client.CreateTemplateVersion(ctx, orgID, codersdk.CreateTemplateVersionRequest{
		FileID:        file.ID,
		StorageMethod: codersdk.ProvisionerStorageMethodFile,
		Provisioner:   codersdk.ProvisionerTypeSandbox,
	})
	require.NoError(t, err)
	version = coderdtest.AwaitTemplateVersionJobCompleted(t, client, version.ID)
	require.Equal(t, codersdk.ProvisionerJobSucceeded, version.Job.Status, version.Job.Error)
	return coderdtest.CreateTemplate(t, client, orgID, version.ID)
}

// agentToken reads the auth token coderd stored for the one agent of a
// build job. The API does not expose agent tokens.
func agentToken(t *testing.T, api *coderd.API, jobID uuid.UUID) uuid.UUID {
	t.Helper()

	ctx := dbauthz.AsSystemRestricted(context.Background())
	resources, err := api.Database.GetWorkspaceResourcesByJobID(ctx, jobID)
	require.NoError(t, err)
	require.Len(t, resources, 1)
	agents, err := api.Database.GetWorkspaceAgentsByResourceIDs(ctx, []uuid.UUID{resources[0].ID})
	require.NoError(t, err)
	require.Len(t, agents, 1)
	return agents[0].AuthToken
}
