package sandbox_test

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/coder/coder/v2/codersdk/drpcsdk"
	"github.com/coder/coder/v2/provisioner/sandbox"
	"github.com/coder/coder/v2/provisionersdk"
	"github.com/coder/coder/v2/provisionersdk/proto"
	"github.com/coder/coder/v2/testutil"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m, testutil.GoleakOptions...)
}

// specArchive tars one testdata file as the template upload does: the spec
// at the archive root, named sandbox.yaml regardless of the fixture name.
func specArchive(t *testing.T, name string) []byte {
	t.Helper()

	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	data, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	require.NoError(t, w.WriteHeader(&tar.Header{
		Name: sandbox.SpecFileName,
		Mode: 0o600,
		Size: int64(len(data)),
	}))
	_, err = w.Write(data)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return buf.Bytes()
}

// session runs one protocol session against the engine and the fake
// runtime, mirroring the call sequence of the provisionerd runner.
func session(t *testing.T, rt *sandbox.FakeRuntime, archive []byte) proto.DRPCProvisioner_SessionClient {
	t.Helper()

	client, server := drpcsdk.MemTransportPipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		_ = sandbox.Serve(ctx, rt, &provisionersdk.ServeOptions{
			Listener:      server,
			WorkDirectory: t.TempDir(),
		})
	}()
	api := proto.NewDRPCProvisionerClient(client)
	s, err := api.Session(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	require.NoError(t, s.Send(&proto.Request{Type: &proto.Request_Config{Config: &proto.Config{}}}))

	require.NoError(t, s.Send(&proto.Request{Type: &proto.Request_Init{Init: &proto.InitRequest{
		TemplateSourceArchive: archive,
	}}}))
	_, err = s.Recv()
	require.NoError(t, err)
	return s
}

// call sends one request and returns its response.
func call(t *testing.T, s proto.DRPCProvisioner_SessionClient, req *proto.Request) *proto.Response {
	t.Helper()

	require.NoError(t, s.Send(req))
	resp, err := s.Recv()
	require.NoError(t, err)
	return resp
}

// startSequence runs parse, plan, graph, and a start apply. It returns the
// graph response and the apply state.
func startSequence(t *testing.T, rt *sandbox.FakeRuntime, archive []byte, workspace string) (*proto.GraphComplete, []byte) {
	t.Helper()

	s := session(t, rt, archive)
	resp := call(t, s, &proto.Request{Type: &proto.Request_Parse{Parse: &proto.ParseRequest{}}})
	require.Empty(t, resp.GetParse().GetError())
	resp = call(t, s, &proto.Request{Type: &proto.Request_Plan{Plan: &proto.PlanRequest{}}})
	require.Empty(t, resp.GetPlan().GetError())
	graph := call(t, s, &proto.Request{Type: &proto.Request_Graph{Graph: &proto.GraphRequest{
		Metadata: &proto.Metadata{WorkspaceName: workspace, WorkspaceTransition: proto.WorkspaceTransition_START},
	}}}).GetGraph()
	require.Empty(t, graph.GetError())
	apply := call(t, s, &proto.Request{Type: &proto.Request_Apply{Apply: &proto.ApplyRequest{
		Metadata: &proto.Metadata{
			WorkspaceId:         "11111111-1111-1111-1111-111111111111",
			WorkspaceBuildId:    "22222222-2222-2222-2222-222222222222",
			WorkspaceName:       workspace,
			WorkspaceTransition: proto.WorkspaceTransition_START,
		},
	}}}).GetApply()
	require.Empty(t, apply.GetError())
	return graph, apply.State
}

func TestEngine(t *testing.T) {
	t.Parallel()

	t.Run("ParseFailureFailsImport", func(t *testing.T) {
		t.Parallel()

		rt := sandbox.NewFakeRuntime()
		archive := specArchive(t, "unknown_field.yaml")
		s := session(t, rt, archive)
		resp := call(t, s, &proto.Request{Type: &proto.Request_Parse{Parse: &proto.ParseRequest{}}})
		require.NotEmpty(t, resp.GetParse().GetError())
		require.Contains(t, resp.GetParse().GetError(), "unknown_field")
	})

	t.Run("StartCreateStartInOrderAndState", func(t *testing.T) {
		t.Parallel()

		rt := sandbox.NewFakeRuntime()
		graph, state := startSequence(t, rt, specArchive(t, "sandbox.yaml"), "ws-start")
		require.Len(t, graph.Resources, 1)
		require.Equal(t, "sandbox_container", graph.Resources[0].Type)
		require.Len(t, graph.Resources[0].Agents, 1)
		require.Equal(t, "ws-start", graph.Resources[0].Name)

		require.Equal(t, []string{"Create", "Start"}, rt.Calls())
		containers := rt.Containers()
		require.Len(t, containers, 1)
		require.Contains(t, string(state), "sbx-11111111-1111-1111-1111-111111111111")
	})

	t.Run("StopThenDestroyRemovesExactlyTheContainer", func(t *testing.T) {
		t.Parallel()

		rt := sandbox.NewFakeRuntime()
		_, startState := startSequence(t, rt, specArchive(t, "sandbox.yaml"), "ws-stop")

		// A stop build replays the state through plan.
		s := session(t, rt, specArchive(t, "sandbox.yaml"))
		_ = call(t, s, &proto.Request{Type: &proto.Request_Plan{Plan: &proto.PlanRequest{State: startState}}})
		graph := call(t, s, &proto.Request{Type: &proto.Request_Graph{Graph: &proto.GraphRequest{
			Metadata: &proto.Metadata{WorkspaceTransition: proto.WorkspaceTransition_STOP},
		}}}).GetGraph()
		require.Empty(t, graph.GetError())
		require.Empty(t, graph.Resources)
		apply := call(t, s, &proto.Request{Type: &proto.Request_Apply{Apply: &proto.ApplyRequest{
			Metadata: &proto.Metadata{WorkspaceTransition: proto.WorkspaceTransition_STOP},
		}}}).GetApply()
		require.Empty(t, apply.GetError())
		require.Equal(t, []string{"Create", "Start", "Stop"}, rt.Calls())

		// A destroy of the stopped container removes it and nothing else.
		s = session(t, rt, specArchive(t, "sandbox.yaml"))
		_ = call(t, s, &proto.Request{Type: &proto.Request_Plan{Plan: &proto.PlanRequest{State: startState}}})
		apply = call(t, s, &proto.Request{Type: &proto.Request_Apply{Apply: &proto.ApplyRequest{
			Metadata: &proto.Metadata{WorkspaceTransition: proto.WorkspaceTransition_DESTROY},
		}}}).GetApply()
		require.Empty(t, apply.GetError())
		require.Equal(t, []string{"Create", "Start", "Stop", "Stop", "Remove"}, rt.Calls())
		require.Empty(t, rt.Containers())
	})

	t.Run("DestroyWithoutStateFailsClosed", func(t *testing.T) {
		t.Parallel()

		rt := sandbox.NewFakeRuntime()
		s := session(t, rt, specArchive(t, "sandbox.yaml"))
		_ = call(t, s, &proto.Request{Type: &proto.Request_Plan{Plan: &proto.PlanRequest{}}})
		apply := call(t, s, &proto.Request{Type: &proto.Request_Apply{Apply: &proto.ApplyRequest{
			Metadata: &proto.Metadata{WorkspaceTransition: proto.WorkspaceTransition_DESTROY},
		}}}).GetApply()
		require.NotEmpty(t, apply.GetError())
		require.Contains(t, apply.GetError(), "refusing")
		require.NotContains(t, rt.Calls(), "Remove")
	})

	t.Run("DestroyWithCorruptStateFailsClosed", func(t *testing.T) {
		t.Parallel()

		rt := sandbox.NewFakeRuntime()
		s := session(t, rt, specArchive(t, "sandbox.yaml"))
		_ = call(t, s, &proto.Request{Type: &proto.Request_Plan{Plan: &proto.PlanRequest{State: []byte("{not json")}}})
		apply := call(t, s, &proto.Request{Type: &proto.Request_Apply{Apply: &proto.ApplyRequest{
			Metadata: &proto.Metadata{WorkspaceTransition: proto.WorkspaceTransition_DESTROY},
		}}}).GetApply()
		require.NotEmpty(t, apply.GetError())
		require.Contains(t, apply.GetError(), "corrupt")
		require.NotContains(t, rt.Calls(), "Remove")
	})

	t.Run("FreshAgentTokenPerStart", func(t *testing.T) {
		t.Parallel()

		rt := sandbox.NewFakeRuntime()
		archive := specArchive(t, "sandbox.yaml")
		graph1, _ := startSequence(t, rt, archive, "ws-token")
		graph2, _ := startSequence(t, rt, archive, "ws-token")
		token1 := graph1.Resources[0].Agents[0].GetToken()
		token2 := graph2.Resources[0].Agents[0].GetToken()
		require.NotEqual(t, token1, token2)
	})

	t.Run("StartReplayAdoptsRunningContainer", func(t *testing.T) {
		t.Parallel()

		rt := sandbox.NewFakeRuntime()
		_, state := startSequence(t, rt, specArchive(t, "sandbox.yaml"), "ws-replay")
		calls := len(rt.Calls())

		// The runner replays the build with the persisted state after a
		// crash. The engine must not create a second container.
		s := session(t, rt, specArchive(t, "sandbox.yaml"))
		_ = call(t, s, &proto.Request{Type: &proto.Request_Plan{Plan: &proto.PlanRequest{State: state}}})
		apply := call(t, s, &proto.Request{Type: &proto.Request_Apply{Apply: &proto.ApplyRequest{
			Metadata: &proto.Metadata{
				WorkspaceId:         "11111111-1111-1111-1111-111111111111",
				WorkspaceTransition: proto.WorkspaceTransition_START,
			},
		}}}).GetApply()
		require.Empty(t, apply.GetError())
		require.Len(t, rt.Containers(), 1)
		require.Len(t, rt.Calls(), calls+1, "only the status call of the replay")
		require.Equal(t, "Status", rt.Calls()[calls])
	})
}

func TestSpecDailyCost(t *testing.T) {
	t.Parallel()

	// The daily cost is deterministic in the spec's resources.
	graph, _ := startSequence(t, sandbox.NewFakeRuntime(), specArchive(t, "sandbox.yaml"), "ws-cost")
	require.Positive(t, graph.Resources[0].DailyCost)
}
