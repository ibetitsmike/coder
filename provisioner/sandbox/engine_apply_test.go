package sandbox_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/provisioner/sandbox"
	"github.com/coder/coder/v2/provisioner/sandbox/sandboxtest"
	"github.com/coder/coder/v2/provisionersdk/proto"
)

const coderURL = "https://coder.example.com"

// harness is one workspace with a fake runtime and a fake operation log.
type harness struct {
	ws      uuid.UUID
	rt      *sandboxtest.FakeRuntime
	runtime sandbox.Runtime
	ops     *sandboxtest.FakeOperations
}

func newHarness() *harness {
	rt := sandboxtest.NewFakeRuntime()
	return &harness{ws: uuid.New(), rt: rt, runtime: rt, ops: sandboxtest.NewFakeOperations()}
}

// build runs plan and apply of one transition for build, as the runner does,
// and a state graph after a successful start.
func (h *harness) build(t *testing.T, build uuid.UUID, transition proto.WorkspaceTransition, state []byte) (*proto.ApplyComplete, *proto.GraphComplete) {
	t.Helper()

	s := serve(t, h.runtime, h.ops, specArchive(t, "sandbox.yaml"))
	require.Empty(t, plan(t, s, state).GetError())
	apply := call(t, s, &proto.Request{Type: &proto.Request_Apply{Apply: &proto.ApplyRequest{
		Metadata: &proto.Metadata{
			CoderUrl:            coderURL,
			WorkspaceId:         h.ws.String(),
			WorkspaceBuildId:    build.String(),
			WorkspaceTransition: transition,
		},
	}}}).GetApply()
	if apply.GetError() != "" || transition != proto.WorkspaceTransition_START {
		return apply, nil
	}
	return apply, graph(t, s, proto.GraphSource_SOURCE_STATE, transition)
}

func token(t *testing.T, g *proto.GraphComplete) string {
	t.Helper()
	require.Empty(t, g.GetError())
	require.Len(t, g.GetResources(), 1)
	return g.GetResources()[0].GetAgents()[0].GetToken()
}

// startHook runs fn before every Start, so a test can act while a build runs.
type startHook struct {
	*sandboxtest.FakeRuntime
	fn func()
}

func (r startHook) Start(ctx context.Context, id string) error {
	r.fn()
	return r.FakeRuntime.Start(ctx, id)
}

func TestEngineApply(t *testing.T) {
	t.Parallel()

	t.Run("StartCreatesThenStartsWithAgentEnv", func(t *testing.T) {
		t.Parallel()

		h := newHarness()
		b := h.ops.NewBuild(h.ws)
		apply, g := h.build(t, b, proto.WorkspaceTransition_START, nil)
		require.Empty(t, apply.GetError())
		require.Equal(t, []string{"Create", "Start"}, h.rt.Calls())
		creates := h.rt.Creates()
		require.Len(t, creates, 1)
		require.Equal(t, sandbox.ContainerName(h.ws), creates[0].ID)
		require.Equal(t, "docker.io/library/alpine:3.20", creates[0].Image)
		require.Equal(t, "vim", creates[0].Env["EDITOR"])
		require.Equal(t, coderURL, creates[0].Env[sandbox.EnvAgentURL])
		require.NotEmpty(t, creates[0].Env[sandbox.EnvAgentToken])
		require.Equal(t, creates[0].Env[sandbox.EnvAgentToken], token(t, g))
		require.Contains(t, string(apply.GetState()), sandbox.ContainerName(h.ws))
		require.Equal(t, sandboxtest.OperationDone, h.ops.Status(b))
	})

	t.Run("StopThenStartKeepsTheContainerAndToken", func(t *testing.T) {
		t.Parallel()

		h := newHarness()
		start, g := h.build(t, h.ops.NewBuild(h.ws), proto.WorkspaceTransition_START, nil)
		first := token(t, g)
		stop, _ := h.build(t, h.ops.NewBuild(h.ws), proto.WorkspaceTransition_STOP, start.GetState())
		require.Empty(t, stop.GetError())
		require.Equal(t, start.GetState(), stop.GetState())
		require.Equal(t, sandbox.ContainerStateStopped, h.rt.Containers()[sandbox.ContainerName(h.ws)].State)

		again, g := h.build(t, h.ops.NewBuild(h.ws), proto.WorkspaceTransition_START, stop.GetState())
		require.Empty(t, again.GetError())
		require.Equal(t, first, token(t, g))
		require.Len(t, h.rt.Creates(), 1)
		require.Equal(t, sandbox.ContainerStateRunning, h.rt.Containers()[sandbox.ContainerName(h.ws)].State)

		// Replaying the start of a running container changes nothing.
		replay, _ := h.build(t, h.ops.NewBuild(h.ws), proto.WorkspaceTransition_START, again.GetState())
		require.Empty(t, replay.GetError())
		require.Equal(t, []string{"Create", "Start", "Stop", "Status", "Start", "Status"}, h.rt.Calls())
	})

	t.Run("RecreateKeepsTheRecordedToken", func(t *testing.T) {
		t.Parallel()

		h := newHarness()
		start, g := h.build(t, h.ops.NewBuild(h.ws), proto.WorkspaceTransition_START, nil)
		first := token(t, g)
		require.NoError(t, h.rt.Remove(context.Background(), sandbox.ContainerName(h.ws)))

		again, g := h.build(t, h.ops.NewBuild(h.ws), proto.WorkspaceTransition_START, start.GetState())
		require.Empty(t, again.GetError())
		require.Equal(t, first, token(t, g))
		creates := h.rt.Creates()
		require.Len(t, creates, 2)
		require.Equal(t, first, creates[1].Env[sandbox.EnvAgentToken])

		// A replay from the older state, as after a lost job state, resumes
		// the recreated container with the token it runs with.
		replay, g := h.build(t, h.ops.NewBuild(h.ws), proto.WorkspaceTransition_START, start.GetState())
		require.Empty(t, replay.GetError())
		require.Equal(t, first, token(t, g))
		require.Len(t, h.rt.Creates(), 2)
	})

	t.Run("SupersededBuildDoesNotTouchTheRuntime", func(t *testing.T) {
		t.Parallel()

		h := newHarness()
		old := h.ops.NewBuild(h.ws)
		h.ops.NewBuild(h.ws)
		apply, _ := h.build(t, old, proto.WorkspaceTransition_START, nil)
		require.Contains(t, apply.GetError(), "superseded")
		require.Empty(t, apply.GetState())
		require.Empty(t, h.rt.Calls())
	})

	t.Run("SupersededMidApplyNewerBuildReplacesTheContainer", func(t *testing.T) {
		t.Parallel()

		h := newHarness()
		old := h.ops.NewBuild(h.ws)
		var newer uuid.UUID
		h.runtime = startHook{FakeRuntime: h.rt, fn: func() {
			if newer == uuid.Nil {
				newer = h.ops.NewBuild(h.ws)
			}
		}}
		apply, _ := h.build(t, old, proto.WorkspaceTransition_START, nil)
		require.Contains(t, apply.GetError(), "superseded")
		require.Empty(t, apply.GetState())
		require.Contains(t, h.rt.Containers(), sandbox.ContainerName(h.ws))

		// The newer build cannot know the token of the unrecorded container,
		// so it replaces it and reconciles the abandoned operation.
		apply, g := h.build(t, newer, proto.WorkspaceTransition_START, nil)
		require.Empty(t, apply.GetError())
		require.Equal(t, []string{"Create", "Start", "Create", "Stop", "Remove", "Create", "Start"}, h.rt.Calls())
		creates := h.rt.Creates()
		require.Len(t, creates, 2)
		require.Equal(t, creates[1].Env[sandbox.EnvAgentToken], token(t, g))
		require.Equal(t, sandboxtest.OperationReconciled, h.ops.Status(old))
		require.Equal(t, sandboxtest.OperationDone, h.ops.Status(newer))
	})

	t.Run("StopAndDestroyWithoutStateUseTheWorkspaceName", func(t *testing.T) {
		t.Parallel()

		h := newHarness()
		start, _ := h.build(t, h.ops.NewBuild(h.ws), proto.WorkspaceTransition_START, nil)
		require.Empty(t, start.GetError())
		stop, _ := h.build(t, h.ops.NewBuild(h.ws), proto.WorkspaceTransition_STOP, nil)
		require.Empty(t, stop.GetError())
		require.Equal(t, sandbox.ContainerStateStopped, h.rt.Containers()[sandbox.ContainerName(h.ws)].State)
		destroy, _ := h.build(t, h.ops.NewBuild(h.ws), proto.WorkspaceTransition_DESTROY, nil)
		require.Empty(t, destroy.GetError())
		require.Empty(t, destroy.GetState())
		require.Empty(t, h.rt.Containers())

		// A workspace without a container stops and deletes cleanly.
		for _, tr := range []proto.WorkspaceTransition{proto.WorkspaceTransition_STOP, proto.WorkspaceTransition_DESTROY} {
			apply, _ := h.build(t, h.ops.NewBuild(h.ws), tr, nil)
			require.Empty(t, apply.GetError())
		}
	})

	t.Run("CorruptStateFailsBeforeTheOperationLog", func(t *testing.T) {
		t.Parallel()

		h := newHarness()
		b := h.ops.NewBuild(h.ws)
		apply, _ := h.build(t, b, proto.WorkspaceTransition_DESTROY, []byte("{not json"))
		require.Contains(t, apply.GetError(), "corrupt")
		require.Empty(t, h.rt.Calls())
		require.Empty(t, h.ops.Status(b))
	})

	t.Run("FailedStartKeepsTheStateAndRecordsFailed", func(t *testing.T) {
		t.Parallel()

		h := newHarness()
		b := h.ops.NewBuild(h.ws)
		h.rt.Fail("Start", xerrors.New("boom"))
		apply, _ := h.build(t, b, proto.WorkspaceTransition_START, nil)
		require.Contains(t, apply.GetError(), "boom")
		var state map[string]string
		require.NoError(t, json.Unmarshal(apply.GetState(), &state))
		require.Equal(t, sandbox.ContainerName(h.ws), state["container_id"])
		require.Equal(t, sandboxtest.OperationFailed, h.ops.Status(b))
	})
}
