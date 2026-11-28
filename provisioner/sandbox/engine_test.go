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
	"github.com/coder/coder/v2/provisioner/sandbox/sandboxtest"
	"github.com/coder/coder/v2/provisionersdk"
	"github.com/coder/coder/v2/provisionersdk/proto"
	"github.com/coder/coder/v2/testutil"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m, testutil.GoleakOptions...)
}

// specArchive tars one testdata file as the template upload does: the spec
// at the archive root, named sandbox.yaml whatever the fixture name.
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

// session serves the engine in memory, configures a session and unpacks the
// archive, as the provisionerd runner does at the start of every job.
func session(t *testing.T, archive []byte) proto.DRPCProvisioner_SessionClient {
	t.Helper()
	return serve(t, sandboxtest.NewFakeRuntime(), sandboxtest.NewFakeOperations(), archive)
}

func serve(t *testing.T, rt sandbox.Runtime, ops sandbox.Operations, archive []byte) proto.DRPCProvisioner_SessionClient {
	t.Helper()

	client, server := drpcsdk.MemTransportPipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = sandbox.Serve(ctx, rt, ops, &provisionersdk.ServeOptions{
			Listener:      server,
			WorkDirectory: t.TempDir(),
			Logger:        testutil.Logger(t),
		})
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	s, err := proto.NewDRPCProvisionerClient(client).Session(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	require.NoError(t, s.Send(&proto.Request{Type: &proto.Request_Config{Config: &proto.Config{}}}))
	resp := call(t, s, &proto.Request{Type: &proto.Request_Init{Init: &proto.InitRequest{
		TemplateSourceArchive: archive,
	}}})
	require.Empty(t, resp.GetInit().GetError())
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

func plan(t *testing.T, s proto.DRPCProvisioner_SessionClient, state []byte) *proto.PlanComplete {
	t.Helper()
	return call(t, s, &proto.Request{Type: &proto.Request_Plan{Plan: &proto.PlanRequest{
		Metadata: &proto.Metadata{WorkspaceTransition: proto.WorkspaceTransition_START},
		State:    state,
	}}}).GetPlan()
}

func graph(t *testing.T, s proto.DRPCProvisioner_SessionClient, source proto.GraphSource, transition proto.WorkspaceTransition) *proto.GraphComplete {
	t.Helper()
	return call(t, s, &proto.Request{Type: &proto.Request_Graph{Graph: &proto.GraphRequest{
		Metadata: &proto.Metadata{WorkspaceName: "ws", WorkspaceTransition: transition},
		Source:   source,
	}}}).GetGraph()
}

func TestEngineImport(t *testing.T) {
	t.Parallel()

	t.Run("ParseFailureFailsImport", func(t *testing.T) {
		t.Parallel()

		s := session(t, specArchive(t, "unknown_field.yaml"))
		resp := call(t, s, &proto.Request{Type: &proto.Request_Parse{Parse: &proto.ParseRequest{}}})
		require.Contains(t, resp.GetParse().GetError(), "unknown_field")
		require.NotEmpty(t, plan(t, s, nil).GetError())
	})

	t.Run("ImportGraphsOneAgent", func(t *testing.T) {
		t.Parallel()

		s := session(t, specArchive(t, "sandbox.yaml"))
		resp := call(t, s, &proto.Request{Type: &proto.Request_Parse{Parse: &proto.ParseRequest{}}})
		require.Empty(t, resp.GetParse().GetError())

		// 1000 CPU millis and 2048 MiB cost one unit each, plus the base.
		p := plan(t, s, nil)
		require.Empty(t, p.GetError())
		require.EqualValues(t, 3, p.GetDailyCost())

		g := graph(t, s, proto.GraphSource_SOURCE_PLAN, proto.WorkspaceTransition_START)
		require.Empty(t, g.GetError())
		require.Len(t, g.GetResources(), 1)
		res := g.GetResources()[0]
		require.Equal(t, "ws", res.GetName())
		require.Equal(t, "sandbox_container", res.GetType())
		require.EqualValues(t, 3, res.GetDailyCost())
		require.Len(t, res.GetAgents(), 1)
		agent := res.GetAgents()[0]
		require.Equal(t, "main", agent.GetName())
		require.Equal(t, "linux", agent.GetOperatingSystem())
		require.Equal(t, "amd64", agent.GetArchitecture())
		require.Equal(t, "/home/coder", agent.GetDirectory())
		require.Equal(t, map[string]string{"EDITOR": "vim"}, agent.GetEnv())
		require.NotEmpty(t, agent.GetToken())

		stop := graph(t, s, proto.GraphSource_SOURCE_PLAN, proto.WorkspaceTransition_STOP)
		require.Empty(t, stop.GetError())
		require.Empty(t, stop.GetResources())

		// A template import graphs without a workspace name.
		imported := call(t, s, &proto.Request{Type: &proto.Request_Graph{Graph: &proto.GraphRequest{
			Metadata: &proto.Metadata{WorkspaceTransition: proto.WorkspaceTransition_START},
			Source:   proto.GraphSource_SOURCE_PLAN,
		}}}).GetGraph()
		require.Empty(t, imported.GetError())
		require.Equal(t, "sandbox", imported.GetResources()[0].GetName())
	})

	t.Run("StopAndDestroyCostNothing", func(t *testing.T) {
		t.Parallel()

		s := session(t, specArchive(t, "sandbox.yaml"))
		for _, transition := range []proto.WorkspaceTransition{proto.WorkspaceTransition_STOP, proto.WorkspaceTransition_DESTROY} {
			p := call(t, s, &proto.Request{Type: &proto.Request_Plan{Plan: &proto.PlanRequest{
				Metadata: &proto.Metadata{WorkspaceTransition: transition},
			}}}).GetPlan()
			require.Empty(t, p.GetError(), transition)
			require.Zero(t, p.GetDailyCost(), transition)
		}
	})

	t.Run("GraphFromStateUsesRecordedToken", func(t *testing.T) {
		t.Parallel()

		s := session(t, specArchive(t, "sandbox.yaml"))
		require.Empty(t, plan(t, s, []byte(`{"container_id":"c","agent_token":"recorded"}`)).GetError())
		g := graph(t, s, proto.GraphSource_SOURCE_STATE, proto.WorkspaceTransition_START)
		require.Empty(t, g.GetError())
		require.Len(t, g.GetResources(), 1)
		require.Equal(t, "recorded", g.GetResources()[0].GetAgents()[0].GetToken())
	})

	t.Run("GraphFromBadStateFails", func(t *testing.T) {
		t.Parallel()

		for name, state := range map[string]string{
			"no agent token": `{"container_id":"c"}`,
			"corrupt":        `{not json`,
		} {
			s := session(t, specArchive(t, "sandbox.yaml"))
			require.Empty(t, plan(t, s, []byte(state)).GetError())
			g := graph(t, s, proto.GraphSource_SOURCE_STATE, proto.WorkspaceTransition_START)
			require.Contains(t, g.GetError(), name)
			require.Empty(t, g.GetResources())
		}
	})

	t.Run("ApplyFailsWithoutState", func(t *testing.T) {
		t.Parallel()

		s := session(t, specArchive(t, "sandbox.yaml"))
		require.Empty(t, plan(t, s, nil).GetError())
		apply := call(t, s, &proto.Request{Type: &proto.Request_Apply{Apply: &proto.ApplyRequest{
			Metadata: &proto.Metadata{WorkspaceTransition: proto.WorkspaceTransition_START},
		}}}).GetApply()
		require.Contains(t, apply.GetError(), "invalid workspace id")
		require.Empty(t, apply.GetState())
	})
}
