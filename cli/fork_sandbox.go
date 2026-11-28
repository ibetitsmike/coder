//go:build !slim

package cli

// This file is fork code: it wires the built-in sandbox provisioner of the
// fork-sandbox-provisioner experiment. newProvisionerDaemon calls
// forkSandboxProvisioners once, so upstream's daemon wiring stays untouched
// and a server without the experiment behaves exactly like upstream.

import (
	"context"
	"io"
	"slices"
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
	runtime, err := sandboxRuntime(ctx, api.DeploymentValues.Sandbox)
	if err == nil {
		var served []codersdk.ProvisionerType
		served, connector, err = serveSandboxProvisioner(ctx, api, runtime, logger, workDir, types, connector, errCh, wg, cancel)
		if err == nil {
			return served, connector
		}
		if c, ok := runtime.(io.Closer); ok {
			_ = c.Close()
		}
	}
	logger.Warn(ctx, "the sandbox provisioner is not registered", slog.Error(err))
	return types, connector
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
		if c, ok := runtime.(io.Closer); ok {
			_ = c.Close()
		}
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
