// This file is fork code: it is the engine of the experimental native
// sandbox provisioner. It speaks the standard provisionersdk protocol and
// drives disposable containers through a Runtime, so import, build, stop,
// and delete keep working through the standard provisionerd runner.
package sandbox

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/afero"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"

	"github.com/coder/coder/v2/provisionersdk"
	"github.com/coder/coder/v2/provisionersdk/proto"
)

// SpecFileName is the archive path the engine reads the template spec from.
const SpecFileName = "sandbox.yaml"

// StateFileName is the session file the engine stores the previous build's
// state blob in. The runner hands the previous state to Plan, not to Apply,
// and plan and apply share one session directory per job.
const StateFileName = "sandbox-state.json"

// stateBlob is the provisioner state the engine persists between builds.
// container_id names the container this deployment created, so a destroy
// removes exactly that container and nothing else. build_id identifies the
// build that produced the state, which is the generation of the operation
// until the acquirer passes a numeric one.
type stateBlob struct {
	ContainerID string `json:"container_id"`
	BuildID     string `json:"build_id"`
}

// Engine implements provisionersdk.Server against a Runtime.
type Engine struct {
	runtime Runtime
	logger  slog.Logger
}

// NewEngine returns an engine that drives containers through runtime.
func NewEngine(runtime Runtime, logger slog.Logger) *Engine {
	return &Engine{runtime: runtime, logger: logger}
}

// Serve serves the sandbox provisioner over the listener in options.
func Serve(ctx context.Context, runtime Runtime, options *provisionersdk.ServeOptions) error {
	return provisionersdk.Serve(ctx, NewEngine(runtime, options.Logger), options)
}

// readSpec extracts and parses the spec of the session's archive. Init has
// unpacked the archive into the work directory by the time any later call
// runs.
func (e *Engine) readSpec(sess *provisionersdk.Session) (*Spec, error) {
	path := filepath.Join(sess.Files.WorkDirectory(), SpecFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, xerrors.Errorf("read %s: %w", SpecFileName, err)
	}
	spec, err := ParseSpec(data)
	if err != nil {
		return nil, xerrors.Errorf("parse %s: %w", SpecFileName, err)
	}
	return spec, nil
}

// specDailyCost derives the template daily cost from the spec's resources.
func specDailyCost(spec *Spec) int32 {
	cpu := spec.Container.CPUMillis / 1024
	if spec.Container.CPUMillis%1024 > 0 {
		cpu++
	}
	mem := spec.Container.MemoryMiB / 2048
	if spec.Container.MemoryMiB%2048 > 0 {
		mem++
	}
	return int32(1 + cpu + mem)
}

func (*Engine) Init(sess *provisionersdk.Session, req *provisionersdk.InitRequest, _ <-chan struct{}) *proto.InitComplete {
	err := sess.Files.ExtractArchive(sess.Context(), sess.Logger, afero.NewOsFs(), req.TemplateSourceArchive, nil)
	if err != nil {
		return provisionersdk.InitErrorf("extract archive: %s", err.Error())
	}
	return &proto.InitComplete{}
}

// Parse validates the spec. A parse failure fails the import job.
func (e *Engine) Parse(sess *provisionersdk.Session, _ *proto.ParseRequest, _ <-chan struct{}) *proto.ParseComplete {
	_, err := e.readSpec(sess)
	if err != nil {
		return &proto.ParseComplete{Error: err.Error()}
	}
	return &proto.ParseComplete{}
}

// Plan reports success and carries the previous build's state into the
// session, where Apply reads it. The daily cost rides the resource in Graph.
func (e *Engine) Plan(sess *provisionersdk.Session, req *proto.PlanRequest, _ <-chan struct{}) *proto.PlanComplete {
	spec, err := e.readSpec(sess)
	if err != nil {
		return &proto.PlanComplete{Error: err.Error()}
	}
	err = os.WriteFile(filepath.Join(sess.Files.WorkDirectory(), StateFileName), req.GetState(), 0o600)
	if err != nil {
		return &proto.PlanComplete{Error: "write session state: " + err.Error()}
	}
	return &proto.PlanComplete{DailyCost: specDailyCost(spec)}
}

// Graph emits one sandbox_container resource with one agent. A stop build
// emits nothing, because the resources are not changing.
func (e *Engine) Graph(sess *provisionersdk.Session, req *proto.GraphRequest, _ <-chan struct{}) *proto.GraphComplete {
	if req.GetMetadata().GetWorkspaceTransition() == proto.WorkspaceTransition_STOP {
		return &proto.GraphComplete{}
	}
	spec, err := e.readSpec(sess)
	if err != nil {
		return &proto.GraphComplete{Error: err.Error()}
	}
	return &proto.GraphComplete{
		Resources: []*proto.Resource{{
			Name:      req.GetMetadata().GetWorkspaceName(),
			Type:      "sandbox_container",
			DailyCost: specDailyCost(spec),
			Agents:    []*proto.Agent{e.specAgent(spec)},
		}},
	}
}

// specAgent builds the resource agent from the spec. Every call mints a
// fresh agent token, so each start build re-authenticates the agent.
func (*Engine) specAgent(spec *Spec) *proto.Agent {
	env := map[string]string{}
	for k, v := range spec.Container.Env {
		env[k] = v
	}
	return &proto.Agent{
		Id:              uuid.NewString(),
		Name:            spec.Agent.Name,
		Env:             env,
		OperatingSystem: spec.Agent.OS,
		Architecture:    spec.Agent.Arch,
		Directory:       spec.Agent.Dir,
		Auth:            &proto.Agent_Token{Token: uuid.NewString()},
	}
}

// Apply drives the runtime through the transition and returns the state
// blob. Every step converges when retried, so a crashed worker can replay
// its apply.
func (e *Engine) Apply(sess *provisionersdk.Session, req *proto.ApplyRequest, _ <-chan struct{}) *proto.ApplyComplete {
	spec, err := e.readSpec(sess)
	if err != nil {
		return &proto.ApplyComplete{Error: err.Error()}
	}
	metadata := req.GetMetadata()
	var state stateBlob
	stateData, err := os.ReadFile(filepath.Join(sess.Files.WorkDirectory(), StateFileName))
	if err == nil && len(stateData) > 0 {
		if err := json.Unmarshal(stateData, &state); err != nil {
			return &proto.ApplyComplete{Error: "state is corrupt: " + err.Error()}
		}
	} else if err != nil && !xerrors.Is(err, os.ErrNotExist) {
		return &proto.ApplyComplete{Error: "read session state: " + err.Error()}
	}

	switch metadata.GetWorkspaceTransition() {
	case proto.WorkspaceTransition_START:
		return e.applyStart(sess, spec, metadata, state)
	case proto.WorkspaceTransition_STOP:
		return e.applyStop(sess, state)
	case proto.WorkspaceTransition_DESTROY:
		return e.applyDestroy(sess, state)
	default:
		return &proto.ApplyComplete{Error: "unknown transition"}
	}
}

// applyStart creates and starts the container, or converges on the one a
// previous run left behind.
func (e *Engine) applyStart(sess *provisionersdk.Session, spec *Spec, metadata *proto.Metadata, state stateBlob) *proto.ApplyComplete {
	ctx := sess.Context()
	if state.ContainerID != "" {
		c, err := e.runtime.Status(ctx, state.ContainerID)
		switch {
		case err == nil && c.State == ContainerStateRunning:
			// A replay of the build that created the container. Nothing to do.
			return &proto.ApplyComplete{State: e.stateBytes(sess, state)}
		case err == nil && c.State == ContainerStateStopped:
			if err := e.runtime.Start(ctx, state.ContainerID); err != nil {
				return &proto.ApplyComplete{Error: "start container: " + err.Error()}
			}
			return &proto.ApplyComplete{State: e.stateBytes(sess, state)}
		case err == nil:
			return &proto.ApplyComplete{Error: "container is in an unknown state: " + string(c.State)}
		}
		if !xerrors.Is(err, ErrNotFound) {
			return &proto.ApplyComplete{Error: "read container: " + err.Error()}
		}
		// The container is gone. Fall through and create a fresh one.
	}

	id := "sbx-" + metadata.GetWorkspaceId()
	_, err := e.runtime.Create(ctx, CreateRequest{
		ID:         id,
		Image:      spec.Container.Image,
		Entrypoint: spec.Container.Entrypoint,
		Env:        spec.Container.Env,
		CPUMillis:  spec.Container.CPUMillis,
		MemoryMiB:  spec.Container.MemoryMiB,
	})
	if xerrors.Is(err, ErrExists) {
		// A crashed run created the container but died before it could
		// persist the state. Adopt it instead of failing the build.
	} else if err != nil {
		return &proto.ApplyComplete{Error: "create container: " + err.Error()}
	}
	if err := e.runtime.Start(ctx, id); err != nil {
		return &proto.ApplyComplete{Error: "start container: " + err.Error()}
	}
	return &proto.ApplyComplete{State: e.stateBytes(sess, stateBlob{
		ContainerID: id,
		BuildID:     metadata.GetWorkspaceBuildId(),
	})}
}

// applyStop stops the container and keeps the state, so a later start can
// resume it.
func (e *Engine) applyStop(sess *provisionersdk.Session, state stateBlob) *proto.ApplyComplete {
	if state.ContainerID == "" {
		return &proto.ApplyComplete{Error: "no container to stop"}
	}
	if err := e.runtime.Stop(sess.Context(), state.ContainerID, 30*time.Second); err != nil {
		if xerrors.Is(err, ErrNotFound) {
			// Nothing to stop. The state stays for a later start or destroy.
			return &proto.ApplyComplete{State: e.stateBytes(sess, state)}
		}
		return &proto.ApplyComplete{Error: "stop container: " + err.Error()}
	}
	return &proto.ApplyComplete{State: e.stateBytes(sess, state)}
}

// applyDestroy removes the container the state names. A missing or corrupt
// state fails closed, because the engine removes only containers this
// deployment created.
func (e *Engine) applyDestroy(sess *provisionersdk.Session, state stateBlob) *proto.ApplyComplete {
	if state.ContainerID == "" {
		return &proto.ApplyComplete{Error: "no container recorded in the state; refusing to remove an orphan"}
	}
	ctx := sess.Context()
	// A container that already vanished is already destroyed.
	_ = e.runtime.Stop(ctx, state.ContainerID, 30*time.Second)
	if err := e.runtime.Remove(ctx, state.ContainerID); err != nil && !xerrors.Is(err, ErrNotFound) {
		return &proto.ApplyComplete{Error: "remove container: " + err.Error()}
	}
	return &proto.ApplyComplete{}
}

func (e *Engine) stateBytes(sess *provisionersdk.Session, state stateBlob) []byte {
	data, err := json.Marshal(state)
	if err != nil {
		// A marshal of two strings cannot fail.
		e.logger.Error(sess.Context(), "marshal sandbox state", slog.Error(err))
		return nil
	}
	return data
}

func (*Engine) Shutdown(_ context.Context, _ *proto.Empty) (*proto.Empty, error) {
	return &proto.Empty{}, nil
}
