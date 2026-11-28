// This file is fork code: it is the engine of the experimental native
// sandbox provisioner. It speaks the standard provisionersdk protocol, so a
// template import runs through the standard provisionerd runner.
package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
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

// StateFileName is the session file that carries the state between calls.
// The runner hands the previous build's state to Plan and runs Graph after
// Apply, and all calls of one job share the session directory.
const StateFileName = "sandbox-state.json"

// The environment variables the engine sets so the agent in the container
// can reach coderd and authenticate.
const (
	EnvAgentToken = ReservedEnvPrefix + "TOKEN"
	EnvAgentURL   = ReservedEnvPrefix + "URL"
)

const stopTimeout = 30 * time.Second

// stateBlob is the provisioner state the engine persists between builds.
// ContainerID names the container of the workspace and AgentToken is the
// token the container was created with.
type stateBlob struct {
	ContainerID string `json:"container_id"`
	BuildID     string `json:"build_id"`
	AgentToken  string `json:"agent_token"`
	// Config is a hash of the container spec and the agent URL, so a start
	// with a changed configuration replaces the container.
	Config string `json:"config"`
}

// ContainerName is the container ID of a workspace. It is deterministic so
// a build can find a container that a crashed worker created but did not
// record.
func ContainerName(workspaceID uuid.UUID) string {
	return "sbx-" + workspaceID.String()
}

// Engine implements provisionersdk.Server against a Runtime.
type Engine struct {
	runtime Runtime
	ops     Operations
	logger  slog.Logger
}

// NewEngine returns an engine that drives containers through runtime and
// records every build in ops before it touches the runtime.
func NewEngine(runtime Runtime, ops Operations, logger slog.Logger) *Engine {
	return &Engine{runtime: runtime, ops: ops, logger: logger}
}

// Serve serves the sandbox provisioner over the listener in options.
func Serve(ctx context.Context, runtime Runtime, ops Operations, options *provisionersdk.ServeOptions) error {
	return provisionersdk.Serve(ctx, NewEngine(runtime, ops, options.Logger), options)
}

// readSpec parses the spec that Init unpacked into the work directory.
func readSpec(sess *provisionersdk.Session) (*Spec, error) {
	data, err := os.ReadFile(filepath.Join(sess.Files.WorkDirectory(), SpecFileName))
	if err != nil {
		return nil, xerrors.Errorf("read %s: %w", SpecFileName, err)
	}
	spec, err := ParseSpec(data)
	if err != nil {
		return nil, xerrors.Errorf("parse %s: %w", SpecFileName, err)
	}
	return spec, nil
}

// readState reads the state of the session. A missing or empty file is the
// state of a workspace without a container.
func readState(sess *provisionersdk.Session) (stateBlob, error) {
	var state stateBlob
	data, err := os.ReadFile(filepath.Join(sess.Files.WorkDirectory(), StateFileName))
	if errors.Is(err, os.ErrNotExist) || (err == nil && len(data) == 0) {
		return state, nil
	}
	if err != nil {
		return state, xerrors.Errorf("read session state: %w", err)
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, xerrors.Errorf("state is corrupt: %w", err)
	}
	return state, nil
}

func writeState(sess *provisionersdk.Session, data []byte) error {
	return os.WriteFile(filepath.Join(sess.Files.WorkDirectory(), StateFileName), data, 0o600)
}

// specDailyCost derives the daily cost from the spec's resources: one unit,
// plus one per started 1024 CPU millis and per started 2048 MiB.
func specDailyCost(spec *Spec) int32 {
	cpu := (spec.Container.CPUMillis + 1023) / 1024
	mem := (spec.Container.MemoryMiB + 2047) / 2048
	return int32(1 + cpu + mem) //nolint:gosec // The spec bounds both values.
}

func (*Engine) Init(sess *provisionersdk.Session, req *provisionersdk.InitRequest, _ <-chan struct{}) *proto.InitComplete {
	err := sess.Files.ExtractArchive(sess.Context(), sess.Logger, afero.NewOsFs(), req.TemplateSourceArchive, nil)
	if err != nil {
		return provisionersdk.InitErrorf("extract archive: %s", err.Error())
	}
	return &proto.InitComplete{}
}

// Parse validates the spec. A parse failure fails the import job.
func (*Engine) Parse(sess *provisionersdk.Session, _ *proto.ParseRequest, _ <-chan struct{}) *proto.ParseComplete {
	if _, err := readSpec(sess); err != nil {
		return &proto.ParseComplete{Error: err.Error()}
	}
	return &proto.ParseComplete{}
}

// Plan carries the previous build's state into the session, where Apply
// and Graph read it. A stop or destroy leaves no container, so it costs
// nothing.
func (*Engine) Plan(sess *provisionersdk.Session, req *proto.PlanRequest, _ <-chan struct{}) *proto.PlanComplete {
	spec, err := readSpec(sess)
	if err != nil {
		return &proto.PlanComplete{Error: err.Error()}
	}
	if err := writeState(sess, req.GetState()); err != nil {
		return &proto.PlanComplete{Error: "write session state: " + err.Error()}
	}
	switch req.GetMetadata().GetWorkspaceTransition() {
	case proto.WorkspaceTransition_STOP, proto.WorkspaceTransition_DESTROY:
		return &proto.PlanComplete{}
	}
	return &proto.PlanComplete{DailyCost: specDailyCost(spec)}
}

// Apply records the build in the operation log, drives the runtime through
// the transition and records the result. A build that a newer build
// superseded fails before it touches the runtime, or fails without a state
// when the newer build arrived while it ran.
func (e *Engine) Apply(sess *provisionersdk.Session, req *proto.ApplyRequest, canceledOrComplete <-chan struct{}) *proto.ApplyComplete {
	// A cancel of the job stops a blocked runtime call. The result is still
	// recorded under the session context.
	ctx, cancel := context.WithCancel(sess.Context())
	defer cancel()
	go func() {
		select {
		case <-canceledOrComplete:
		case <-ctx.Done():
		}
		cancel()
	}()
	metadata := req.GetMetadata()
	spec, err := readSpec(sess)
	if err != nil {
		return &proto.ApplyComplete{Error: err.Error()}
	}
	workspaceID, err := uuid.Parse(metadata.GetWorkspaceId())
	if err != nil {
		return &proto.ApplyComplete{Error: "invalid workspace id: " + err.Error()}
	}
	buildID, err := uuid.Parse(metadata.GetWorkspaceBuildId())
	if err != nil {
		return &proto.ApplyComplete{Error: "invalid workspace build id: " + err.Error()}
	}
	state, err := readState(sess)
	if err != nil {
		return &proto.ApplyComplete{Error: err.Error()}
	}

	op, err := e.ops.Begin(ctx, workspaceID, buildID)
	if err != nil {
		return &proto.ApplyComplete{Error: "begin sandbox operation: " + err.Error()}
	}
	if op.Abandoned {
		e.logger.Warn(ctx, "older builds left sandbox operations unfinished",
			slog.F("workspace_id", workspaceID), slog.F("build_id", buildID))
	}

	var next stateBlob
	var applyErr error
	switch metadata.GetWorkspaceTransition() {
	case proto.WorkspaceTransition_START:
		next, applyErr = e.applyStart(ctx, spec, metadata, workspaceID, state)
	case proto.WorkspaceTransition_STOP:
		next, applyErr = e.applyStop(ctx, workspaceID, state)
	case proto.WorkspaceTransition_DESTROY:
		applyErr = e.applyDestroy(ctx, workspaceID, state)
	default:
		applyErr = xerrors.New("unknown transition")
	}

	if err := e.ops.Complete(sess.Context(), op, applyErr); err != nil {
		if errors.Is(err, ErrSuperseded) {
			return &proto.ApplyComplete{Error: "complete sandbox operation: " + err.Error()}
		}
		if applyErr == nil {
			applyErr = xerrors.Errorf("complete sandbox operation: %w", err)
		}
	}

	var data []byte
	if next.ContainerID != "" {
		data, err = json.Marshal(next)
		if err != nil {
			return &proto.ApplyComplete{Error: "marshal state: " + err.Error()}
		}
	}
	if err := writeState(sess, data); err != nil {
		return &proto.ApplyComplete{Error: "write session state: " + err.Error()}
	}
	if applyErr != nil {
		// The state still names a container the build created, so a later
		// build can resume or remove it.
		return &proto.ApplyComplete{State: data, Error: applyErr.Error()}
	}
	return &proto.ApplyComplete{State: data}
}

// Graph emits one sandbox_container resource with one agent. After an apply
// the agent carries the token the container was created with. A template
// import has no container, so it gets a placeholder token. A stop or destroy
// emits nothing.
func (*Engine) Graph(sess *provisionersdk.Session, req *proto.GraphRequest, _ <-chan struct{}) *proto.GraphComplete {
	switch req.GetMetadata().GetWorkspaceTransition() {
	case proto.WorkspaceTransition_STOP, proto.WorkspaceTransition_DESTROY:
		return &proto.GraphComplete{}
	}
	spec, err := readSpec(sess)
	if err != nil {
		return &proto.GraphComplete{Error: err.Error()}
	}
	token := uuid.NewString()
	if req.GetSource() == proto.GraphSource_SOURCE_STATE {
		state, err := readState(sess)
		if err != nil {
			return &proto.GraphComplete{Error: err.Error()}
		}
		if state.AgentToken == "" {
			return &proto.GraphComplete{Error: "the state records no agent token"}
		}
		token = state.AgentToken
	}
	// A template import has no workspace name.
	name := req.GetMetadata().GetWorkspaceName()
	if name == "" {
		name = "sandbox"
	}
	return &proto.GraphComplete{
		Resources: []*proto.Resource{{
			Name:      name,
			Type:      "sandbox_container",
			DailyCost: specDailyCost(spec),
			Agents: []*proto.Agent{{
				Id:              uuid.NewString(),
				Name:            spec.Agent.Name,
				Env:             maps.Clone(spec.Container.Env),
				OperatingSystem: spec.Agent.OS,
				Architecture:    spec.Agent.Arch,
				Directory:       spec.Agent.Dir,
				Auth:            &proto.Agent_Token{Token: token},
			}},
		}},
	}
}

// applyStart resumes the recorded container when its configuration is
// current, or creates and starts a new one with the recorded agent token.
func (e *Engine) applyStart(ctx context.Context, spec *Spec, metadata *proto.Metadata, workspaceID uuid.UUID, state stateBlob) (stateBlob, error) {
	config, err := json.Marshal([]any{spec.Container, metadata.GetCoderUrl()})
	if err != nil {
		return stateBlob{}, xerrors.Errorf("marshal container config: %w", err)
	}
	sum := sha256.Sum256(config)
	if state.ContainerID != "" && state.AgentToken != "" && state.Config == hex.EncodeToString(sum[:]) {
		c, err := e.runtime.Status(ctx, state.ContainerID)
		switch {
		case err == nil && c.State == ContainerStateRunning:
			return state, nil
		case err == nil && (c.State == ContainerStateStopped || c.State == ContainerStateCreated):
			if err := e.runtime.Start(ctx, state.ContainerID); err != nil {
				return state, xerrors.Errorf("start container: %w", err)
			}
			return state, nil
		case err == nil:
			return state, xerrors.Errorf("container is in an unknown state: %s", c.State)
		case !errors.Is(err, ErrNotFound):
			return state, xerrors.Errorf("read container: %w", err)
		}
		// The container is gone. Create a fresh one with the recorded token, so
		// a replay of this build from the same state resumes a container whose
		// token matches the state.
	}

	next := stateBlob{
		ContainerID: ContainerName(workspaceID),
		BuildID:     metadata.GetWorkspaceBuildId(),
		AgentToken:  state.AgentToken,
		Config:      hex.EncodeToString(sum[:]),
	}
	if next.AgentToken == "" {
		next.AgentToken = uuid.NewString()
	}
	env := maps.Clone(spec.Container.Env)
	if env == nil {
		env = map[string]string{}
	}
	env[EnvAgentToken] = next.AgentToken
	env[EnvAgentURL] = metadata.GetCoderUrl()
	create := CreateRequest{
		ID:         next.ContainerID,
		Image:      spec.Container.Image,
		Entrypoint: spec.Container.Entrypoint,
		Env:        env,
		CPUMillis:  spec.Container.CPUMillis,
		MemoryMiB:  spec.Container.MemoryMiB,
	}
	_, err = e.runtime.Create(ctx, create)
	if errors.Is(err, ErrExists) {
		// The container has an older configuration, or a crashed build created
		// it and never recorded its agent token. Replace it.
		if err := e.removeContainer(ctx, next.ContainerID); err != nil {
			return stateBlob{}, err
		}
		_, err = e.runtime.Create(ctx, create)
	}
	if err != nil {
		return stateBlob{}, xerrors.Errorf("create container: %w", err)
	}
	if err := e.runtime.Start(ctx, next.ContainerID); err != nil {
		return next, xerrors.Errorf("start container: %w", err)
	}
	return next, nil
}

// applyStop stops the container and keeps the state, so a later start can
// resume it. Without a recorded container it stops the deterministic name,
// which a crashed start may have left running.
func (e *Engine) applyStop(ctx context.Context, workspaceID uuid.UUID, state stateBlob) (stateBlob, error) {
	id := state.ContainerID
	if id == "" {
		id = ContainerName(workspaceID)
	}
	err := e.runtime.Stop(ctx, id, stopTimeout)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return state, xerrors.Errorf("stop container: %w", err)
	}
	return state, nil
}

// applyDestroy removes the container of the workspace. Without a recorded
// container it removes the deterministic name, which only this workspace
// uses, so a crashed first build leaves nothing behind. A corrupt state has
// already failed the apply.
func (e *Engine) applyDestroy(ctx context.Context, workspaceID uuid.UUID, state stateBlob) error {
	id := state.ContainerID
	if id == "" {
		id = ContainerName(workspaceID)
	}
	return e.removeContainer(ctx, id)
}

// removeContainer stops and removes a container. A missing container is
// already removed.
func (e *Engine) removeContainer(ctx context.Context, id string) error {
	if err := e.runtime.Stop(ctx, id, stopTimeout); err != nil && !errors.Is(err, ErrNotFound) {
		return xerrors.Errorf("stop container: %w", err)
	}
	if err := e.runtime.Remove(ctx, id); err != nil && !errors.Is(err, ErrNotFound) {
		return xerrors.Errorf("remove container: %w", err)
	}
	return nil
}
