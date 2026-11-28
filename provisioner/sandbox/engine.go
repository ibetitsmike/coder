// This file is fork code: it is the engine of the experimental native
// sandbox provisioner. It speaks the standard provisionersdk protocol, so a
// template import runs through the standard provisionerd runner.
package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"

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

// stateBlob is the provisioner state the engine persists between builds.
// ContainerID names the container of the workspace and AgentToken is the
// token the container was created with.
type stateBlob struct {
	ContainerID string `json:"container_id"`
	BuildID     string `json:"build_id"`
	AgentToken  string `json:"agent_token"`
}

// Engine implements provisionersdk.Server.
type Engine struct {
	logger slog.Logger
}

// NewEngine returns a sandbox engine.
func NewEngine(logger slog.Logger) *Engine {
	return &Engine{logger: logger}
}

// Serve serves the sandbox provisioner over the listener in options.
func Serve(ctx context.Context, options *provisionersdk.ServeOptions) error {
	return provisionersdk.Serve(ctx, NewEngine(options.Logger), options)
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

// Apply cannot drive a container yet, so every build fails without a state.
func (*Engine) Apply(_ *provisionersdk.Session, _ *proto.ApplyRequest, _ <-chan struct{}) *proto.ApplyComplete {
	return &proto.ApplyComplete{Error: "the sandbox engine cannot apply builds yet"}
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
