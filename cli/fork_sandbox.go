//go:build !slim

package cli

// This file is fork code: it wires the built-in sandbox provisioner of the
// fork-sandbox-provisioner experiment. newProvisionerDaemon calls
// forkSandboxProvisioners once, so upstream's daemon wiring stays untouched
// and a server without the experiment behaves exactly like upstream.

import (
	"cmp"
	"context"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/drpcsdk"
	"github.com/coder/coder/v2/provisioner/sandbox"
	"github.com/coder/coder/v2/provisioner/sandbox/dockerruntime"
	"github.com/coder/coder/v2/provisioner/sandbox/sandboxdb"
	"github.com/coder/coder/v2/provisionerd"
	"github.com/coder/coder/v2/provisionersdk"
	sdkproto "github.com/coder/coder/v2/provisionersdk/proto"
)

// errNoSandboxRuntime reports that no deployment option selects a container
// runtime for the sandbox provisioner.
var errNoSandboxRuntime = xerrors.New("no sandbox container runtime is selected, set --sandbox-runtime")

// sandboxRuntime returns the container runtime that the deployment selects.
// The test FakeRuntime is never a default: without --sandbox-runtime it
// returns errNoSandboxRuntime.
func sandboxRuntime(ctx context.Context, cfg codersdk.ForkSandboxConfig) (sandbox.Runtime, error) {
	switch cfg.Runtime.Value() {
	case "":
		return nil, errNoSandboxRuntime
	case codersdk.SandboxRuntimeDocker:
		// A daemon that does not answer must not hold up the server start.
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return dockerruntime.New(ctx, cfg.DockerHost.Value())
	default:
		return nil, xerrors.Errorf("unknown sandbox runtime %q", cfg.Runtime.Value())
	}
}

// forkSandboxProvisioners adds the sandbox provisioner to the built-in
// daemon when the fork-sandbox-provisioner experiment is enabled and a
// container runtime is selected. Otherwise it logs why and returns its
// inputs unchanged.
func forkSandboxProvisioners(
	ctx context.Context,
	api *coderd.API,
	logger slog.Logger,
	workDir string,
	types []codersdk.ProvisionerType,
	connector provisionerd.LocalProvisioners,
	errCh chan error,
	wg *sync.WaitGroup,
	cancel context.CancelFunc,
) ([]codersdk.ProvisionerType, provisionerd.LocalProvisioners) {
	if !api.Experiments.Enabled(codersdk.ExperimentForkSandboxProvisioner) {
		return types, connector
	}
	err := sandboxAccessURLError(api.AccessURL, api.DeploymentValues)
	if err != nil {
		logger.Warn(ctx, "the sandbox provisioner is not registered", slog.Error(err))
		return types, connector
	}
	runtime, release, err := acquireSandboxRuntime(ctx, api)
	if err == nil {
		var served []codersdk.ProvisionerType
		served, connector, err = serveSandboxProvisioner(ctx, api, runtime, logger, workDir, types, connector, errCh, wg, cancel)
		if err == nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-ctx.Done()
				release()
			}()
			return served, connector
		}
		release()
	}
	logger.Warn(ctx, "the sandbox provisioner is not registered", slog.Error(err))
	return types, connector
}

// sandboxAccessURLError reports why the agents in docker sandbox containers
// could not reach coderd. The runtime points the agent of a loopback access
// URL at the host gateway, which reaches coderd only when coderd serves plain
// HTTP on every interface at the access URL port.
func sandboxAccessURLError(accessURL *url.URL, dv *codersdk.DeploymentValues) error {
	if dv.Sandbox.Runtime.Value() != codersdk.SandboxRuntimeDocker ||
		dockerruntime.ContainerURL(accessURL.String()) == accessURL.String() {
		return nil
	}
	if accessURL.Scheme != "http" {
		return xerrors.Errorf("sandbox containers reach the loopback access URL %s on another host name, which its TLS certificate does not cover: set an access URL the containers can reach", accessURL)
	}
	port := cmp.Or(accessURL.Port(), "80")
	host, listenPort, err := net.SplitHostPort(dv.HTTPAddress.Value())
	ip, ipErr := netip.ParseAddr(host)
	if err != nil || listenPort != port || (host != "" && (ipErr != nil || !ip.IsUnspecified())) {
		return xerrors.Errorf("sandbox containers reach the loopback access URL %s through the host gateway, but coderd listens on %q: set --http-address 0.0.0.0:%s or an access URL the containers can reach", accessURL, dv.HTTPAddress.Value(), port)
	}
	return nil
}

// sandboxRuntimes holds the runtime that the built-in daemons of one API
// share. The server starts several daemons and any of them can acquire a
// build of a workspace, so they need one runtime with one set of
// per-container locks.
var sandboxRuntimes = struct {
	sync.Mutex
	byAPI map[*coderd.API]*sharedRuntime
}{byAPI: map[*coderd.API]*sharedRuntime{}}

type sharedRuntime struct {
	runtime sandbox.Runtime
	users   int
}

// acquireSandboxRuntime returns the runtime of api and creates it for the
// first daemon. release closes it after the last daemon released it.
func acquireSandboxRuntime(ctx context.Context, api *coderd.API) (sandbox.Runtime, func(), error) {
	sandboxRuntimes.Lock()
	defer sandboxRuntimes.Unlock()
	s, ok := sandboxRuntimes.byAPI[api]
	if !ok {
		runtime, err := sandboxRuntime(ctx, api.DeploymentValues.Sandbox)
		if err != nil {
			return nil, nil, err
		}
		s = &sharedRuntime{runtime: runtime}
		sandboxRuntimes.byAPI[api] = s
	}
	s.users++
	return s.runtime, sync.OnceFunc(func() {
		sandboxRuntimes.Lock()
		defer sandboxRuntimes.Unlock()
		s.users--
		if s.users > 0 {
			return
		}
		delete(sandboxRuntimes.byAPI, api)
		if c, ok := s.runtime.(io.Closer); ok {
			_ = c.Close()
		}
	}), nil
}

// serveSandboxProvisioner serves the sandbox engine over runtime in memory
// and adds it to the daemon's types and connector. The operation log lives
// in the API's database.
func serveSandboxProvisioner(
	ctx context.Context,
	api *coderd.API,
	runtime sandbox.Runtime,
	logger slog.Logger,
	workDir string,
	types []codersdk.ProvisionerType,
	connector provisionerd.LocalProvisioners,
	errCh chan error,
	wg *sync.WaitGroup,
	cancel context.CancelFunc,
) ([]codersdk.ProvisionerType, provisionerd.LocalProvisioners, error) {
	if slices.Contains(types, codersdk.ProvisionerTypeSandbox) {
		return types, connector, nil
	}
	ops, err := sandboxdb.New(api.Database)
	if err != nil {
		return types, connector, xerrors.Errorf("sandbox operation log: %w", err)
	}
	client, server := drpcsdk.MemTransportPipe()
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		_ = client.Close()
		_ = server.Close()
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()
		err := sandbox.Serve(ctx, runtime, ops, &provisionersdk.ServeOptions{
			Listener:      server,
			WorkDirectory: workDir,
			Logger:        logger.Named("sandbox"),
		})
		if err != nil && !xerrors.Is(err, context.Canceled) {
			select {
			case errCh <- err:
			default:
			}
		}
	}()
	connector[string(codersdk.ProvisionerTypeSandbox)] = sdkproto.NewDRPCProvisionerClient(client)
	return append(slices.Clone(types), codersdk.ProvisionerTypeSandbox), connector, nil
}

// forkSandboxAccessHost lets requests from Docker sandbox containers past the
// access URL redirect. For a loopback access URL the Docker runtime points
// the agent at the host gateway, and a redirect back to loopback would send
// the agent to the container itself. Other requests go to redirecting.
func forkSandboxAccessHost(api *coderd.API, direct, redirecting http.Handler) http.Handler {
	if !api.Experiments.Enabled(codersdk.ExperimentForkSandboxProvisioner) ||
		api.DeploymentValues.Sandbox.Runtime.Value() != codersdk.SandboxRuntimeDocker {
		return redirecting
	}
	accessURL := api.AccessURL
	u, err := url.Parse(dockerruntime.ContainerURL(accessURL.String()))
	if err != nil || u.Host == accessURL.Host || accessURL.Scheme != "http" {
		return redirecting
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.EqualFold(strings.TrimSuffix(r.Host, ":80"), strings.TrimSuffix(u.Host, ":80")) {
			direct.ServeHTTP(w, r)
			return
		}
		redirecting.ServeHTTP(w, r)
	})
}
