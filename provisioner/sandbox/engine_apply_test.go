package sandbox_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/provisioner/sandbox"
	"github.com/coder/coder/v2/provisioner/sandbox/sandboxtest"
	"github.com/coder/coder/v2/provisionersdk/proto"
	"github.com/coder/coder/v2/testutil"
)

const coderURL = "https://coder.example.com"

// harness is one workspace with a fake runtime and a fake operation log.
type harness struct {
	ws      uuid.UUID
	rt      *sandboxtest.FakeRuntime
	runtime sandbox.Runtime
	ops     *sandboxtest.FakeOperations
	archive []byte
}

func newHarness(t *testing.T) *harness {
	rt := sandboxtest.NewFakeRuntime()
	return &harness{ws: uuid.New(), rt: rt, runtime: rt, ops: sandboxtest.NewFakeOperations(), archive: specArchive(t, "sandbox.yaml")}
}

// build runs plan and apply of one transition for build, as the runner does,
// and a state graph after a successful start.
func (h *harness) build(t *testing.T, build uuid.UUID, transition proto.WorkspaceTransition, state []byte) (*proto.ApplyComplete, *proto.GraphComplete) {
	t.Helper()

	s := serve(t, h.runtime, h.ops, h.archive)
	require.Empty(t, plan(t, s, state).GetError())
	apply := call(t, s, h.apply(build, transition)).GetApply()
	if apply.GetError() != "" || transition != proto.WorkspaceTransition_START {
		return apply, nil
	}
	return apply, graph(t, s, proto.GraphSource_SOURCE_STATE, transition)
}

func (h *harness) apply(build uuid.UUID, transition proto.WorkspaceTransition) *proto.Request {
	return &proto.Request{Type: &proto.Request_Apply{Apply: &proto.ApplyRequest{
		Metadata: &proto.Metadata{
			CoderUrl:            coderURL,
			WorkspaceId:         h.ws.String(),
			WorkspaceBuildId:    build.String(),
			WorkspaceTransition: transition,
		},
	}}}
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

// blockedStart blocks every Start until its context ends.
type blockedStart struct{ *sandboxtest.FakeRuntime }

func (blockedStart) Start(ctx context.Context, _ string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(testutil.WaitShort):
		return xerrors.New("the start was not canceled")
	}
}

func TestEngineApply(t *testing.T) {
	t.Parallel()

	t.Run("StartCreatesThenStartsWithAgentEnv", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
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

	t.Run("EveryStartReplacesTheContainer", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		name := sandbox.ContainerName(h.ws)
		x, g := h.build(t, h.ops.NewBuild(h.ws), proto.WorkspaceTransition_START, nil)
		first := token(t, g)
		stop, _ := h.build(t, h.ops.NewBuild(h.ws), proto.WorkspaceTransition_STOP, x.GetState())
		require.Empty(t, stop.GetError())
		require.Equal(t, x.GetState(), stop.GetState())
		require.Equal(t, sandbox.ContainerStateStopped, h.rt.Containers()[name].State)

		// A start after a stop, and a start of a running container, each
		// replace the container and keep the token.
		again, g := h.build(t, h.ops.NewBuild(h.ws), proto.WorkspaceTransition_START, stop.GetState())
		require.Empty(t, again.GetError())
		require.Equal(t, first, token(t, g))
		_, g = h.build(t, h.ops.NewBuild(h.ws), proto.WorkspaceTransition_START, again.GetState())
		require.Equal(t, first, token(t, g))
		require.Equal(t, []string{"Create", "Start", "Stop", "Create", "Stop", "Remove", "Create", "Start", "Create", "Stop", "Remove", "Create", "Start"}, h.rt.Calls())

		// A start with spec Y loses its state, and a rollback to spec X
		// arrives with the older state of X. It must not keep the Y container.
		h.archive = specArchive(t, "sandbox.yaml", "alpine:3.20", "alpine:3.21")
		y, _ := h.build(t, h.ops.NewBuild(h.ws), proto.WorkspaceTransition_START, x.GetState())
		require.Empty(t, y.GetError())
		h.archive = specArchive(t, "sandbox.yaml")
		rollback, g := h.build(t, h.ops.NewBuild(h.ws), proto.WorkspaceTransition_START, x.GetState())
		require.Empty(t, rollback.GetError())
		require.Equal(t, first, token(t, g))
		creates := h.rt.Creates()
		require.Len(t, creates, 5)
		require.Equal(t, "docker.io/library/alpine:3.21", creates[3].Image)
		require.Equal(t, "docker.io/library/alpine:3.20", creates[4].Image)
		require.Equal(t, sandbox.ContainerStateRunning, h.rt.Containers()[name].State)
	})

	t.Run("RecreateKeepsTheRecordedToken", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		start, g := h.build(t, h.ops.NewBuild(h.ws), proto.WorkspaceTransition_START, nil)
		first := token(t, g)
		require.NoError(t, h.rt.Remove(context.Background(), sandbox.ContainerName(h.ws)))

		again, g := h.build(t, h.ops.NewBuild(h.ws), proto.WorkspaceTransition_START, start.GetState())
		require.Empty(t, again.GetError())
		require.Equal(t, first, token(t, g))
		creates := h.rt.Creates()
		require.Len(t, creates, 2)
		require.Equal(t, first, creates[1].Env[sandbox.EnvAgentToken])

		// A replay from the older state, as after a lost job state, replaces
		// the container again with the same token.
		replay, g := h.build(t, h.ops.NewBuild(h.ws), proto.WorkspaceTransition_START, start.GetState())
		require.Empty(t, replay.GetError())
		require.Equal(t, first, token(t, g))
		creates = h.rt.Creates()
		require.Len(t, creates, 3)
		require.Equal(t, first, creates[2].Env[sandbox.EnvAgentToken])
	})

	t.Run("CancelEndsABlockedRuntimeCall", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		b := h.ops.NewBuild(h.ws)
		s := serve(t, blockedStart{h.rt}, h.ops, h.archive)
		require.Empty(t, plan(t, s, nil).GetError())
		require.NoError(t, s.Send(h.apply(b, proto.WorkspaceTransition_START)))
		apply := call(t, s, &proto.Request{Type: &proto.Request_Cancel{Cancel: &proto.CancelRequest{}}}).GetApply()
		require.Contains(t, apply.GetError(), "context canceled")
		require.Equal(t, sandboxtest.OperationFailed, h.ops.Status(b))
	})

	t.Run("LostJobStateReplaysTheStart", func(t *testing.T) {
		t.Parallel()

		// The start completed in the log, but its job state never reached
		// coderd, so the next build has no state and no token.
		h := newHarness(t)
		first := h.ops.NewBuild(h.ws)
		start, _ := h.build(t, first, proto.WorkspaceTransition_START, nil)
		require.Empty(t, start.GetError())
		require.Equal(t, sandboxtest.OperationDone, h.ops.Status(first))

		next := h.ops.NewBuild(h.ws)
		replay, g := h.build(t, next, proto.WorkspaceTransition_START, nil)
		require.Empty(t, replay.GetError())
		creates := h.rt.Creates()
		require.Len(t, creates, 2)
		require.Equal(t, creates[1].Env[sandbox.EnvAgentToken], token(t, g))
		require.Equal(t, sandbox.ContainerStateRunning, h.rt.Containers()[sandbox.ContainerName(h.ws)].State)
		require.Equal(t, sandboxtest.OperationDone, h.ops.Status(next))
	})

	t.Run("CrashedBuildOrphanIsFoundByName", func(t *testing.T) {
		t.Parallel()

		for _, tr := range []proto.WorkspaceTransition{proto.WorkspaceTransition_START, proto.WorkspaceTransition_STOP, proto.WorkspaceTransition_DESTROY} {
			// A crashed build began, created and started its container and
			// never completed or returned a state.
			h := newHarness(t)
			ctx := context.Background()
			crashed := h.ops.NewBuild(h.ws)
			_, err := h.ops.Begin(ctx, h.ws, crashed)
			require.NoError(t, err)
			name := sandbox.ContainerName(h.ws)
			_, err = h.rt.Create(ctx, sandbox.CreateRequest{ID: name, Env: map[string]string{sandbox.EnvAgentToken: "orphan"}})
			require.NoError(t, err)
			require.NoError(t, h.rt.Start(ctx, name))

			apply, g := h.build(t, h.ops.NewBuild(h.ws), tr, nil)
			require.Empty(t, apply.GetError(), tr.String())
			require.Equal(t, sandboxtest.OperationReconciled, h.ops.Status(crashed), tr.String())
			switch tr {
			case proto.WorkspaceTransition_START:
				creates := h.rt.Creates()
				require.Len(t, creates, 2)
				require.Equal(t, creates[1].Env[sandbox.EnvAgentToken], token(t, g))
				require.Equal(t, sandbox.ContainerStateRunning, h.rt.Containers()[name].State)
			case proto.WorkspaceTransition_STOP:
				require.Equal(t, sandbox.ContainerStateStopped, h.rt.Containers()[name].State)
			default:
				require.Empty(t, apply.GetState())
				require.Empty(t, h.rt.Containers())
			}
			if tr != proto.WorkspaceTransition_START {
				// Without a container, the transition succeeds again.
				_ = h.rt.Remove(ctx, name)
				apply, _ = h.build(t, h.ops.NewBuild(h.ws), tr, nil)
				require.Empty(t, apply.GetError(), tr.String())
			}
		}
	})

	t.Run("StateNamingAnotherContainerFails", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		other := sandbox.ContainerName(uuid.New())
		_, err := h.rt.Create(context.Background(), sandbox.CreateRequest{ID: other})
		require.NoError(t, err)
		b := h.ops.NewBuild(h.ws)
		apply, _ := h.build(t, b, proto.WorkspaceTransition_DESTROY, []byte(`{"container_id":"`+other+`"}`))
		require.Contains(t, apply.GetError(), "another workspace")
		require.Contains(t, h.rt.Containers(), other)
		require.Empty(t, h.ops.Status(b))
	})

	t.Run("SupersededBuildDoesNotTouchTheRuntime", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		old := h.ops.NewBuild(h.ws)
		h.ops.NewBuild(h.ws)
		apply, _ := h.build(t, old, proto.WorkspaceTransition_START, nil)
		require.Contains(t, apply.GetError(), "superseded")
		require.Empty(t, apply.GetState())
		require.Empty(t, h.rt.Calls())
	})

	t.Run("SupersededMidApplyNewerBuildReplacesTheContainer", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
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

	t.Run("CorruptStateFailsBeforeTheOperationLog", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		b := h.ops.NewBuild(h.ws)
		apply, _ := h.build(t, b, proto.WorkspaceTransition_DESTROY, []byte("{not json"))
		require.Contains(t, apply.GetError(), "corrupt")
		require.Empty(t, h.rt.Calls())
		require.Empty(t, h.ops.Status(b))
	})

	t.Run("FailedStartKeepsTheStateAndRecordsFailed", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		b := h.ops.NewBuild(h.ws)
		h.rt.Fail("Start", xerrors.New("boom"))
		apply, _ := h.build(t, b, proto.WorkspaceTransition_START, nil)
		require.Contains(t, apply.GetError(), "boom")
		var state map[string]string
		require.NoError(t, json.Unmarshal(apply.GetState(), &state))
		require.Equal(t, sandbox.ContainerName(h.ws), state["container_id"])
		require.Equal(t, sandboxtest.OperationFailed, h.ops.Status(b))

		// A start that cannot replace the container keeps the recorded token.
		h.rt.Fail("Remove", xerrors.New("stuck"))
		again, _ := h.build(t, h.ops.NewBuild(h.ws), proto.WorkspaceTransition_START, apply.GetState())
		require.Contains(t, again.GetError(), "stuck")
		require.Equal(t, apply.GetState(), again.GetState())
	})
}
