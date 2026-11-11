package cli

// This file is fork code: it wires the built-in sandbox provisioner daemon of
// the fork-sandbox-provisioner experiment. cli/server.go calls
// forkSandboxProvisioners once, so upstream's daemon wiring stays untouched
// and a server without the experiment behaves exactly like upstream.

import (
	"context"
	"slices"
	"sync"

	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"

	"github.com/coder/coder/v2/coderd"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/drpcsdk"
	"github.com/coder/coder/v2/provisioner/sandbox"
	"github.com/coder/coder/v2/provisionerd"
	"github.com/coder/coder/v2/provisionersdk"
	sdkproto "github.com/coder/coder/v2/provisionersdk/proto"
)

// sandboxProvisionerType is the provisioner type of the built-in sandbox
// daemon, from the value the fork's chatdmig node adds to the enum.
var sandboxProvisionerType = codersdk.ProvisionerType(forkdb.ProvisionerTypeSandbox)

// forkSandboxProvisioners adds the sandbox provisioner to the built-in
// daemon's types and connector when the fork-sandbox-provisioner experiment
// is enabled. It returns its inputs unchanged otherwise, so a server without
// the experiment behaves exactly like upstream.
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
	if slices.Contains(types, sandboxProvisionerType) {
		return types, connector
	}

	// The engine drives the fake runtime until the real containerd adapter
	// lands. The experiment gates the daemon, so this serves nothing outside
	// a deployment that opted in.
	runtime := sandbox.NewFakeRuntime()
	sandboxClient, sandboxServer := drpcsdk.MemTransportPipe()
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		_ = sandboxClient.Close()
		_ = sandboxServer.Close()
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()
		err := sandbox.Serve(ctx, runtime, &provisionersdk.ServeOptions{
			Listener:      sandboxServer,
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

	types = append(types, sandboxProvisionerType)
	connector[string(forkdb.ProvisionerTypeSandbox)] = sdkproto.NewDRPCProvisionerClient(sandboxClient)
	return types, connector
}
