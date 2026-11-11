package sandbox_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd"
	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/drpcsdk"
	"github.com/coder/coder/v2/provisioner/sandbox"
	"github.com/coder/coder/v2/provisionerd"
	"github.com/coder/coder/v2/provisionerd/proto"
	"github.com/coder/coder/v2/provisionersdk"
	sdkproto "github.com/coder/coder/v2/provisionersdk/proto"
	"github.com/coder/coder/v2/testutil"
)

// TestSandboxEndToEnd drives the sandbox provisioner through coderd and the
// provisionerd runner, with the engine serving the jobs and the fake runtime
// holding the containers. Every subtest runs its workspace through the full
// API surface: import, start, stop, and destroy.
func TestSandboxEndToEnd(t *testing.T) {
	t.Parallel()

	t.Run("Lifecycle", func(t *testing.T) {
		t.Parallel()

		srv := startSandboxServer(t, nil)
		srv.importTemplate(t, "sandbox.yaml")

		srv.createWorkspace(t)
		build := srv.latestBuild(t, "1")
		require.Equal(t, "sandbox_container", build.Resources[0].Type)
		require.Len(t, build.Resources[0].Agents, 1)
		require.Equal(t, []string{"Create", "Start"}, srv.rt.Calls())
		require.NotEqual(t, uuid.Nil, srv.agentToken(t, "1"))

		srv.buildTransition(t, database.WorkspaceTransitionStop)
		require.Equal(t, []string{"Create", "Start", "Stop"}, srv.rt.Calls())

		srv.buildTransition(t, database.WorkspaceTransitionDelete)
		require.Empty(t, srv.rt.Containers())
	})

	t.Run("FreshTokenPerStart", func(t *testing.T) {
		t.Parallel()

		srv := startSandboxServer(t, nil)
		srv.importTemplate(t, "sandbox.yaml")
		srv.createWorkspace(t)
		token1 := srv.agentToken(t, "1")

		srv.buildTransition(t, database.WorkspaceTransitionStop)
		srv.buildTransition(t, database.WorkspaceTransitionStart)
		token2 := srv.agentToken(t, "3")

		require.NotEqual(t, token1, token2)
	})

	t.Run("DestroyWithCorruptStateFailsClosed", func(t *testing.T) {
		t.Parallel()

		srv := startSandboxServer(t, nil)
		srv.importTemplate(t, "sandbox.yaml")
		srv.createWorkspace(t)
		srv.buildTransition(t, database.WorkspaceTransitionStop)

		// Corrupt the persisted state so destroy cannot name the container.
		build := srv.latestBuild(t, "2")
		err := srv.client.UpdateWorkspaceBuildState(context.Background(), build.ID, []byte("{not json"))
		require.NoError(t, err)

		destroy := srv.buildTransition(t, database.WorkspaceTransitionDelete)
		require.Equal(t, codersdk.ProvisionerJobFailed, destroy.Job.Status)
		require.Contains(t, destroy.Job.Error, "corrupt")
		require.Equal(t, []string{"Create", "Start", "Stop"}, srv.rt.Calls())
	})

	t.Run("ParseFailureFailsImport", func(t *testing.T) {
		t.Parallel()

		srv := startSandboxServer(t, nil)
		version := srv.importVersion(t, "unknown_field.yaml")
		require.Equal(t, codersdk.ProvisionerJobFailed, version.Job.Status)
		require.Contains(t, version.Job.Error, "unknown_field")
	})

	t.Run("QuotaCommit", func(t *testing.T) {
		t.Parallel()

		srv := startSandboxServer(t, nil)
		srv.importTemplate(t, "sandbox.yaml")
		srv.createWorkspace(t)

		logs := srv.buildLogs(t, "1")
		require.Contains(t, logs, "Build cost")
	})
}

// sandboxServer is a coderd test server with the sandbox engine behind a
// provisionerd and the fake runtime holding the containers.
type sandboxServer struct {
	t       *testing.T
	client  *codersdk.Client
	db      database.Store
	orgID   uuid.UUID
	rt      *sandbox.FakeRuntime
	version codersdk.TemplateVersion
	tmpl    codersdk.Template
	ws      codersdk.Workspace
}

// startSandboxServer boots coderd, the sandbox daemon, and uploads the spec
// the tests import. experiments enables fork experiments on the deployment.
func startSandboxServer(t *testing.T, experiments []string) *sandboxServer {
	t.Helper()

	vals := coderdtest.DeploymentValues(t)
	if experiments != nil {
		vals.Experiments = append(vals.Experiments, experiments...)
	}
	client, closer, api := coderdtest.NewWithAPI(t, &coderdtest.Options{
		IncludeProvisionerDaemon: false,
		DeploymentValues:         vals,
	})
	t.Cleanup(func() { _ = closer.Close() })
	user := coderdtest.CreateFirstUser(t, client)

	rt := sandbox.NewFakeRuntime()
	serveSandboxDaemon(t, api, rt)

	return &sandboxServer{
		t:      t,
		client: client,
		db:     api.Database,
		orgID:  user.OrganizationID,
		rt:     rt,
	}
}

// importVersion creates a template version from the named testdata spec and
// waits for its import job.
func (s *sandboxServer) importVersion(t *testing.T, spec string) codersdk.TemplateVersion {
	t.Helper()

	data := specArchive(t, spec)
	file, err := s.client.Upload(context.Background(), codersdk.ContentTypeTar, bytes.NewReader(data))
	require.NoError(t, err)
	version, err := s.client.CreateTemplateVersion(context.Background(), s.orgID, codersdk.CreateTemplateVersionRequest{
		FileID:        file.ID,
		StorageMethod: codersdk.ProvisionerStorageMethodFile,
		Provisioner:   codersdk.ProvisionerType("sandbox"),
	})
	require.NoError(t, err)
	completed := coderdtest.AwaitTemplateVersionJobCompleted(t, s.client, version.ID)
	version.Job = completed.Job
	return version
}

// importTemplate imports the named spec and creates a template from it.
func (s *sandboxServer) importTemplate(t *testing.T, spec string) {
	t.Helper()

	version := s.importVersion(t, spec)
	require.Empty(t, version.Job.Error)
	s.version = version

	template, err := s.client.CreateTemplate(context.Background(), s.orgID, codersdk.CreateTemplateRequest{
		Name:      coderdtest.RandomUsername(t),
		VersionID: version.ID,
	})
	require.NoError(t, err)
	require.Equal(t, codersdk.ProvisionerType("sandbox"), template.Provisioner)
	s.tmpl = template
}

// createWorkspace creates a workspace from the template and waits for the
// start build.
func (s *sandboxServer) createWorkspace(t *testing.T) {
	t.Helper()

	ws := coderdtest.CreateWorkspace(t, s.client, s.tmpl.ID)
	coderdtest.AwaitWorkspaceBuildJobCompleted(t, s.client, ws.LatestBuild.ID)
	s.ws = ws
}

// buildTransition triggers a build of the current transition and waits for
// the job.
func (s *sandboxServer) buildTransition(t *testing.T, transition database.WorkspaceTransition) codersdk.WorkspaceBuild {
	t.Helper()

	build := coderdtest.CreateWorkspaceBuild(t, s.client, s.ws, transition)
	return coderdtest.AwaitWorkspaceBuildJobCompleted(t, s.client, build.ID)
}

// latestBuild returns the build at the given number.
func (s *sandboxServer) latestBuild(t *testing.T, number string) codersdk.WorkspaceBuild {
	t.Helper()

	user, err := s.client.User(context.Background(), codersdk.Me)
	require.NoError(t, err)
	build, err := s.client.WorkspaceBuildByUsernameAndWorkspaceNameAndBuildNumber(
		context.Background(), user.Username, s.ws.Name, number)
	require.NoError(t, err)
	return build
}

// buildLogs returns every provisioner log line of the build at the number.
func (s *sandboxServer) buildLogs(t *testing.T, number string) string {
	t.Helper()

	build := s.latestBuild(t, number)
	logs, closer, err := s.client.WorkspaceBuildLogsAfter(context.Background(), build.ID, 0)
	require.NoError(t, err)
	defer closer.Close()

	var out []string
	for log := range logs {
		out = append(out, log.Output)
	}
	return strings.Join(out, "\n")
}

// agentToken returns the persisted auth token of the agent in the build at
// the number. The API does not expose agent tokens, so read the row as the
// provisionerd subject, which owns ResourceSystem.
func (s *sandboxServer) agentToken(t *testing.T, number string) uuid.UUID {
	t.Helper()

	build := s.latestBuild(t, number)
	resources, err := s.db.GetWorkspaceResourcesByJobID(dbauthz.AsProvisionerd(context.Background()), build.Job.ID)
	require.NoError(t, err)
	require.Len(t, resources, 1)
	agents, err := s.db.GetWorkspaceAgentsByResourceIDs(dbauthz.AsProvisionerd(context.Background()), []uuid.UUID{resources[0].ID})
	require.NoError(t, err)
	require.Len(t, agents, 1)
	return agents[0].AuthToken
}

// serveSandboxDaemon serves the sandbox engine behind a provisionerd that
// registers with coderd under the sandbox type.
func serveSandboxDaemon(t *testing.T, api *coderd.API, rt *sandbox.FakeRuntime) {
	t.Helper()

	ctx, cancelFunc := context.WithCancel(context.Background())
	sandboxClient, sandboxServer := drpcsdk.MemTransportPipe()
	workDir := t.TempDir()
	connectedCh := make(chan struct{})
	daemon := provisionerd.New(func(dialCtx context.Context) (proto.DRPCProvisionerDaemonClient, error) {
		return api.CreateInMemoryTaggedProvisionerDaemon(dialCtx, "sandbox-e2e", []codersdk.ProvisionerType{codersdk.ProvisionerType("sandbox")}, nil)
	}, &provisionerd.Options{
		Logger:              testutil.Logger(t),
		UpdateInterval:      250 * time.Millisecond,
		ForceCancelInterval: 5 * time.Second,
		Connector: provisionerd.LocalProvisioners{
			"sandbox": sdkproto.NewDRPCProvisionerClient(sandboxClient),
		},
		InitConnectionCh: connectedCh,
	})
	closer := coderdtest.NewProvisionerDaemonCloser(daemon)
	t.Cleanup(func() { _ = closer.Close() })
	t.Cleanup(cancelFunc)
	go func() {
		_ = sandbox.Serve(ctx, rt, &provisionersdk.ServeOptions{
			Listener:      sandboxServer,
			WorkDirectory: workDir,
		})
	}()
	select {
	case <-connectedCh:
	case <-time.After(10 * time.Second):
		t.Fatal("sandbox daemon did not connect within 10s")
	}
}
