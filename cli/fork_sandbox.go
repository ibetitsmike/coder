package cli

// This file is fork code: it wires the built-in sandbox provisioner of the
// fork-sandbox-provisioner experiment. newProvisionerDaemon calls
// forkSandboxProvisioners once, so upstream's daemon wiring stays untouched
// and a server without the experiment behaves exactly like upstream.

import (
	"context"
	"slices"
	"sync"

	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/drpcsdk"
	"github.com/coder/coder/v2/provisioner/sandbox"
	"github.com/coder/coder/v2/provisioner/sandbox/sandboxdb"
	"github.com/coder/coder/v2/provisionerd"
	"github.com/coder/coder/v2/provisionersdk"
	sdkproto "github.com/coder/coder/v2/provisionersdk/proto"
)

// errNoSandboxRuntime reports that this build has no container runtime for
// the sandbox provisioner yet.
var errNoSandboxRuntime = xerrors.New("no sandbox container runtime is available in this build")

// sandboxRuntime returns the container runtime of the sandbox provisioner.
// The test FakeRuntime is never a default, so until a runtime adapter lands
// it returns errNoSandboxRuntime.
func sandboxRuntime() (sandbox.Runtime, error) {
	return nil, errNoSandboxRuntime
}

// forkSandboxProvisioners adds the sandbox provisioner to the built-in
// daemon when the fork-sandbox-provisioner experiment is enabled and a
// container runtime exists. No production runtime exists yet, so with the
// experiment on it logs a warning and returns its inputs unchanged.
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
	runtime, err := sandboxRuntime()
	if err == nil {
		var served []codersdk.ProvisionerType
		served, connector, err = serveSandboxProvisioner(ctx, api, runtime, logger, workDir, types, connector, errCh, wg, cancel)
		if err == nil {
			return served, connector
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
