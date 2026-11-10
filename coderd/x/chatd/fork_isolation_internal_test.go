package chatd

// This file is fork code: it tests spawn_agent's isolation option behind the
// fork-subagent-isolation experiment.

import (
	"context"
	"database/sql"
	"encoding/json"
	"maps"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
	"github.com/coder/coder/v2/coderd/x/chatd/chatstate"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk/agentconnmock"
)

const isolationTestAgentDirectory = "/home/coder/repo"

// fakeWorktreeConn records the worktree calls chatd makes and answers them
// with worktree or createErr, and summary or summaryErr. Any other AgentConn
// call panics.
type fakeWorktreeConn struct {
	workspacesdk.AgentConn
	worktree   workspacesdk.ForkWorktree
	createErr  error
	summary    workspacesdk.ForkWorktreeSummary
	summaryErr error

	mu              sync.Mutex
	creates         []workspacesdk.ForkCreateWorktreeRequest
	deletes         []workspacesdk.ForkDeleteWorktreeRequest
	deleteDeadlines []time.Time
	summaries       []workspacesdk.ForkWorktreeSummaryRequest
}

func (c *fakeWorktreeConn) ForkCreateWorktree(_ context.Context, req workspacesdk.ForkCreateWorktreeRequest) (workspacesdk.ForkWorktree, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.creates = append(c.creates, req)
	if c.createErr != nil {
		return workspacesdk.ForkWorktree{}, c.createErr
	}
	return c.worktree, nil
}

func (c *fakeWorktreeConn) ForkDeleteWorktree(ctx context.Context, req workspacesdk.ForkDeleteWorktreeRequest) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deletes = append(c.deletes, req)
	deadline, _ := ctx.Deadline()
	c.deleteDeadlines = append(c.deleteDeadlines, deadline)
	return nil
}

func (c *fakeWorktreeConn) ForkWorktreeSummary(_ context.Context, req workspacesdk.ForkWorktreeSummaryRequest) (workspacesdk.ForkWorktreeSummary, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.summaries = append(c.summaries, req)
	return c.summary, c.summaryErr
}

func (c *fakeWorktreeConn) calls() ([]workspacesdk.ForkCreateWorktreeRequest, []workspacesdk.ForkDeleteWorktreeRequest) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.creates, c.deletes
}

type isolationFixture struct {
	server *Server
	db     database.Store
	fdb    forkdb.Store
	user   database.User
	org    database.Organization
	model  database.ChatModelConfig
	conns  sync.Map
}

// newIsolationFixture returns a server whose agent connections are the ones
// rootChat registers for each workspace agent.
func newIsolationFixture(t *testing.T, experiments codersdk.Experiments) *isolationFixture {
	t.Helper()
	db, ps := dbtestutil.NewDB(t)
	server := newInternalTestServer(t, db, ps, chatprovider.ProviderAPIKeys{}, withInternalTestServerExperiments(experiments))
	fdb, err := forkdb.From(db)
	require.NoError(t, err)
	user, org, model := seedInternalChatDeps(t, db)
	f := &isolationFixture{server: server, db: db, fdb: fdb, user: user, org: org, model: model}
	server.agentConnFn = func(_ context.Context, agentID uuid.UUID) (workspacesdk.AgentConn, func(), error) {
		conn, ok := f.conns.Load(agentID)
		if !ok {
			return nil, nil, xerrors.Errorf("no connection for agent %s", agentID)
		}
		return conn.(workspacesdk.AgentConn), func() {}, nil
	}
	return f
}

// rootChat creates a root chat. With a non-nil conn the chat is bound to a
// new workspace agent in isolationTestAgentDirectory that dials conn.
func (f *isolationFixture) rootChat(t *testing.T, conn workspacesdk.AgentConn) database.Chat {
	t.Helper()
	ctx := chatdTestContext(t)
	opts := CreateOptions{
		OrganizationID:     f.org.ID,
		OwnerID:            f.user.ID,
		Title:              "root",
		ModelConfigID:      f.model.ID,
		InitialUserContent: []codersdk.ChatMessagePart{codersdk.ChatMessageText("hello")},
	}
	if conn != nil {
		workspace, build, agent := seedWorkspaceBinding(t, f.db, f.user.ID)
		require.NoError(t, f.db.UpdateWorkspaceAgentStartupByID(ctx, database.UpdateWorkspaceAgentStartupByIDParams{
			ID:                agent.ID,
			ExpandedDirectory: isolationTestAgentDirectory,
			Subsystems:        []database.WorkspaceAgentSubsystem{},
		}))
		f.conns.Store(agent.ID, conn)
		opts.WorkspaceID = uuid.NullUUID{UUID: workspace.ID, Valid: true}
		opts.BuildID = uuid.NullUUID{UUID: build.ID, Valid: true}
		opts.AgentID = uuid.NullUUID{UUID: agent.ID, Valid: true}
	}
	root, err := f.server.CreateChat(ctx, opts)
	require.NoError(t, err)
	root, err = f.db.GetChatByID(ctx, root.ID)
	require.NoError(t, err)
	return root
}

// spawnTool returns root's spawn_agent tool as a turn of root gets it.
func (f *isolationFixture) spawnTool(t *testing.T, root database.Chat) fantasy.AgentTool {
	t.Helper()
	ctx := chatdTestContext(t)
	tools, err := f.server.subagentTools(ctx, func() database.Chat { return root }, root.LastModelConfigID)
	require.NoError(t, err)
	tools, err = f.server.appendForkTools(ctx, tools, root, root.LastModelConfigID)
	require.NoError(t, err)
	tool := findToolByName(tools, spawnAgentToolName)
	require.NotNil(t, tool)
	return tool
}

func (f *isolationFixture) spawn(t *testing.T, root database.Chat, args map[string]any) fantasy.ToolResponse {
	t.Helper()
	args = maps.Clone(args)
	args["prompt"] = "do the work"
	if _, ok := args["type"]; !ok {
		args["type"] = subagentTypeGeneral
	}
	input, err := json.Marshal(args)
	require.NoError(t, err)
	resp, err := f.spawnTool(t, root).Run(chatdTestContext(t), fantasy.ToolCall{ID: uuid.NewString(), Name: spawnAgentToolName, Input: string(input)})
	require.NoError(t, err)
	return resp
}

func (f *isolationFixture) children(t *testing.T, root database.Chat) []database.GetChildChatsByParentIDsRow {
	t.Helper()
	rows, err := f.db.GetChildChatsByParentIDs(chatdTestContext(t), database.GetChildChatsByParentIDsParams{
		ParentIds: []uuid.UUID{root.ID},
	})
	require.NoError(t, err)
	return rows
}

func TestForkSpawnIsolationWorktree(t *testing.T) {
	t.Parallel()

	f := newIsolationFixture(t, codersdk.Experiments{codersdk.ExperimentForkSubagentIsolation})
	returned := workspacesdk.ForkWorktree{Path: "/home/coder/.coder/worktrees/child", Branch: "agent/returned-branch"}

	for _, tc := range []struct {
		name         string
		args         map[string]any
		wantRepoPath string
		wantBaseRef  string
	}{
		{
			name:         "AgentDirectory",
			args:         map[string]any{"isolation": "worktree", "title": "Fix the Login Bug!"},
			wantRepoPath: isolationTestAgentDirectory,
		},
		{
			name:         "RepoPathAndBaseBranch",
			args:         map[string]any{"isolation": "worktree", "title": "Fix the Login Bug!", "repo_path": "/src/other", "base_branch": "release/1.2"},
			wantRepoPath: "/src/other",
			wantBaseRef:  "release/1.2",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := chatdTestContext(t)
			conn := &fakeWorktreeConn{worktree: returned}
			root := f.rootChat(t, conn)

			resp := f.spawn(t, root, tc.args)
			require.False(t, resp.IsError, resp.Content)
			var result struct {
				ChatID       string `json:"chat_id"`
				Isolation    string `json:"isolation"`
				WorktreePath string `json:"worktree_path"`
				Branch       string `json:"branch"`
			}
			require.NoError(t, json.Unmarshal([]byte(resp.Content), &result))
			childID, err := uuid.Parse(result.ChatID)
			require.NoError(t, err)
			require.Equal(t, "worktree", result.Isolation)
			require.Equal(t, returned.Path, result.WorktreePath)
			require.Equal(t, returned.Branch, result.Branch)

			creates, deletes := conn.calls()
			require.Equal(t, []workspacesdk.ForkCreateWorktreeRequest{{
				RepoPath: tc.wantRepoPath,
				Name:     childID.String(),
				Branch:   "agent/fix-the-login-bug-" + childID.String()[:8],
				BaseRef:  tc.wantBaseRef,
			}}, creates)
			require.Empty(t, deletes)

			row, err := f.fdb.GetChatSubagentByChatID(ctx, childID)
			require.NoError(t, err)
			require.Equal(t, "worktree", row.Isolation)
			require.Equal(t, returned.Path, row.WorktreePath)
			require.Equal(t, returned.Branch, row.Branch)
			require.Equal(t, int32(1), row.Depth)
		})
	}

	t.Run("Schema", func(t *testing.T) {
		t.Parallel()
		info := f.spawnTool(t, f.rootChat(t, nil)).Info()
		isolation, ok := info.Parameters["isolation"].(map[string]any)
		require.True(t, ok, info.Parameters)
		enum, ok := isolation["enum"].([]any)
		require.True(t, ok, isolation)
		require.ElementsMatch(t, []any{"shared", "worktree"}, enum)
		require.Contains(t, info.Parameters, "repo_path")
		require.Contains(t, info.Parameters, "base_branch")
		require.Contains(t, info.Parameters, "prompt")
	})
}

// TestForkSpawnIsolationStoredAgentType checks that, with agent definitions
// on, the isolation wrapper composes with the computer use overrides and
// stored agent type wrappers: the schema keeps both rewrites, and a custom
// type spawned into a worktree gets its definition, its worktree, and a
// result that reports both.
func TestForkSpawnIsolationStoredAgentType(t *testing.T) {
	t.Parallel()

	f := newIsolationFixture(t, codersdk.Experiments{codersdk.ExperimentForkSubagentIsolation, codersdk.ExperimentForkAgentDefinitions})
	ctx := chatdTestContext(t)
	returned := workspacesdk.ForkWorktree{Path: "/home/coder/.coder/worktrees/child", Branch: "agent/returned-branch"}
	conn := &fakeWorktreeConn{worktree: returned}
	root := f.rootChat(t, conn)
	_, err := f.fdb.InsertUserAgentDefinition(ctx, forkdb.InsertUserAgentDefinitionParams{
		OrganizationID: root.OrganizationID,
		UserID:         root.OwnerID,
		Name:           "planner",
		Description:    "Plans the work.",
		Content:        agentDefinitionMarkdown("name: planner", "PLANNER-MARKER"),
	})
	require.NoError(t, err)

	info := f.spawnTool(t, root).Info()
	require.Contains(t, info.Parameters, "isolation")
	require.Contains(t, info.Description, "both apply to every type")

	resp := f.spawn(t, root, map[string]any{"type": "personal/planner", "isolation": "worktree", "title": "Plan"})
	require.False(t, resp.IsError, resp.Content)
	var result struct {
		ChatID       string `json:"chat_id"`
		Type         string `json:"type"`
		Isolation    string `json:"isolation"`
		WorktreePath string `json:"worktree_path"`
		Branch       string `json:"branch"`
	}
	require.NoError(t, json.Unmarshal([]byte(resp.Content), &result))
	childID, err := uuid.Parse(result.ChatID)
	require.NoError(t, err)
	require.Equal(t, "planner", result.Type)
	require.Equal(t, "worktree", result.Isolation)
	require.Equal(t, returned.Path, result.WorktreePath)
	require.Equal(t, returned.Branch, result.Branch)

	creates, deletes := conn.calls()
	require.Len(t, creates, 1)
	require.Equal(t, childID.String(), creates[0].Name)
	require.Empty(t, deletes)

	row, err := f.fdb.GetChatSubagentByChatID(ctx, childID)
	require.NoError(t, err)
	require.Equal(t, "planner", row.AgentType)
	require.Equal(t, "worktree", row.Isolation)
	require.Equal(t, returned.Path, row.WorktreePath)
	require.Contains(t, agentDefinitionsFixture{db: f.db}.systemPrompt(t, childID), "PLANNER-MARKER")
}

func TestForkSpawnIsolationErrors(t *testing.T) {
	t.Parallel()

	f := newIsolationFixture(t, codersdk.Experiments{codersdk.ExperimentForkSubagentIsolation})
	worktree := map[string]any{"isolation": "worktree"}

	for _, tc := range []struct {
		name string
		// conn nil means a root chat without a workspace.
		conn    func(t *testing.T) workspacesdk.AgentConn
		args    map[string]any
		wantErr string
	}{
		{
			name: "OlderAgent",
			conn: func(*testing.T) workspacesdk.AgentConn {
				return &fakeWorktreeConn{createErr: codersdk.NewError(http.StatusNotFound, codersdk.Response{Message: "Route not found."})}
			},
			args:    worktree,
			wantErr: "this workspace's agent does not support worktree isolation",
		},
		{
			name: "AgentRejects",
			conn: func(*testing.T) workspacesdk.AgentConn {
				return &fakeWorktreeConn{createErr: codersdk.NewError(http.StatusBadRequest, codersdk.Response{
					Message: "Base ref does not exist.",
					Detail:  "fatal: invalid reference: nope",
				})}
			},
			args:    worktree,
			wantErr: "create worktree: Base ref does not exist: fatal: invalid reference: nope",
		},
		{
			name: "AgentUnreachable",
			conn: func(*testing.T) workspacesdk.AgentConn {
				return &fakeWorktreeConn{createErr: xerrors.New("connection reset by peer")}
			},
			args:    worktree,
			wantErr: "create worktree: connection reset by peer",
		},
		{
			name: "ConnWithoutWorktrees",
			conn: func(t *testing.T) workspacesdk.AgentConn {
				return agentconnmock.NewMockAgentConn(gomock.NewController(t))
			},
			args:    worktree,
			wantErr: "agent connection does not support worktree isolation",
		},
		{
			name:    "NoWorkspace",
			args:    worktree,
			wantErr: "isolation worktree needs a workspace",
		},
		{
			name:    "UnknownIsolation",
			conn:    func(*testing.T) workspacesdk.AgentConn { return &fakeWorktreeConn{} },
			args:    map[string]any{"isolation": "workspace"},
			wantErr: "isolation must be one of: shared, worktree",
		},
		{
			name:    "RepoPathWithoutWorktree",
			conn:    func(*testing.T) workspacesdk.AgentConn { return &fakeWorktreeConn{} },
			args:    map[string]any{"repo_path": "/src/other"},
			wantErr: "repo_path and base_branch need isolation worktree",
		},
		{
			name:    "BaseBranchWithShared",
			conn:    func(*testing.T) workspacesdk.AgentConn { return &fakeWorktreeConn{} },
			args:    map[string]any{"isolation": "shared", "base_branch": "main"},
			wantErr: "repo_path and base_branch need isolation worktree",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var conn workspacesdk.AgentConn
			if tc.conn != nil {
				conn = tc.conn(t)
			}
			root := f.rootChat(t, conn)

			resp := f.spawn(t, root, tc.args)
			require.True(t, resp.IsError, resp.Content)
			require.Contains(t, resp.Content, tc.wantErr)
			require.Empty(t, f.children(t, root))
			if fake, ok := conn.(*fakeWorktreeConn); ok {
				_, deletes := fake.calls()
				require.Empty(t, deletes)
			}
		})
	}
}

func TestForkSpawnIsolationExperimentOff(t *testing.T) {
	t.Parallel()

	f := newIsolationFixture(t, upstreamExperimentsKnown())
	ctx := chatdTestContext(t)
	conn := &fakeWorktreeConn{worktree: workspacesdk.ForkWorktree{Path: "/wt", Branch: "agent/wt"}}
	root := f.rootChat(t, conn)

	info := f.spawnTool(t, root).Info()
	require.NotContains(t, info.Parameters, "isolation")
	require.NotContains(t, info.Parameters, "repo_path")
	require.NotContains(t, info.Parameters, "base_branch")

	resp := f.spawn(t, root, map[string]any{"isolation": "worktree", "repo_path": "/src/other"})
	require.False(t, resp.IsError, resp.Content)
	require.NotContains(t, resp.Content, "isolation")
	var result struct {
		ChatID string `json:"chat_id"`
	}
	require.NoError(t, json.Unmarshal([]byte(resp.Content), &result))
	childID, err := uuid.Parse(result.ChatID)
	require.NoError(t, err)
	// Upstream records no chat_subagents row.
	_, err = f.fdb.GetChatSubagentByChatID(ctx, childID)
	require.ErrorIs(t, err, sql.ErrNoRows)
	creates, _ := conn.calls()
	require.Empty(t, creates)
}

// TestForkSpawnIsolationCreateChat covers createSubagentChatWithID failures
// the spawn_agent tool cannot reach: its own checks stop them earlier.
func TestForkSpawnIsolationCreateChat(t *testing.T) {
	t.Parallel()

	f := newIsolationFixture(t, codersdk.Experiments{codersdk.ExperimentForkSubagentIsolation})
	isolatedContext := func(t *testing.T) context.Context {
		return context.WithValue(chatdTestContext(t), subagentIsolationKey{}, subagentIsolationRequest{isolation: subagentIsolationWorktree})
	}

	t.Run("RemovesWorktreeWhenTransactionFails", func(t *testing.T) {
		t.Parallel()
		conn := &fakeWorktreeConn{worktree: workspacesdk.ForkWorktree{Path: "/wt/child", Branch: "agent/child"}}
		root := f.rootChat(t, conn)
		// The missing client type makes the chat insert fail inside the
		// transaction, after the worktree exists.
		_, err := f.server.createSubagentChatWithID(isolatedContext(t), f.server.pubsub, uuid.New(), root, nil, chatstate.CreateChatInput{
			OrganizationID:    root.OrganizationID,
			OwnerID:           root.OwnerID,
			WorkspaceID:       root.WorkspaceID,
			AgentID:           root.AgentID,
			ParentChatID:      uuid.NullUUID{UUID: root.ID, Valid: true},
			RootChatID:        uuid.NullUUID{UUID: root.ID, Valid: true},
			LastModelConfigID: root.LastModelConfigID,
			Title:             "child",
		})
		require.ErrorContains(t, err, "chat_client_type")
		creates, deletes := conn.calls()
		require.Len(t, creates, 1)
		require.Equal(t, []workspacesdk.ForkDeleteWorktreeRequest{{Path: "/wt/child", Branch: "agent/child"}}, deletes)
		conn.mu.Lock()
		require.Greater(t, time.Until(conn.deleteDeadlines[0]), 5*time.Minute)
		conn.mu.Unlock()
		require.Empty(t, f.children(t, root))
	})

	t.Run("ComputerUse", func(t *testing.T) {
		t.Parallel()
		conn := &fakeWorktreeConn{}
		root := f.rootChat(t, conn)
		_, err := f.server.createSubagentChatWithID(isolatedContext(t), f.server.pubsub, uuid.New(), root, nil, chatstate.CreateChatInput{
			OrganizationID: root.OrganizationID,
			OwnerID:        root.OwnerID,
			AgentID:        root.AgentID,
			Title:          "desktop",
			Mode:           database.NullChatMode{ChatMode: database.ChatModeComputerUse, Valid: true},
		})
		require.ErrorContains(t, err, "not supported for computer use agents")
		creates, _ := conn.calls()
		require.Empty(t, creates)
	})
}

func TestForkWorktreeBranch(t *testing.T) {
	t.Parallel()

	chatID := uuid.MustParse("0123abcd-4567-89ab-cdef-0123456789ab")
	for _, tc := range []struct {
		title string
		want  string
	}{
		{title: "Fix the Login Bug!", want: "agent/fix-the-login-bug-0123abcd"},
		{title: "  --Refactor   chatd/ISOLATION__v2-- ", want: "agent/refactor-chatd-isolation-v2-0123abcd"},
		{title: "Überprüfe café", want: "agent/berpr-fe-caf-0123abcd"},
		{title: "", want: "agent/0123abcd"},
		{title: "!!! ???", want: "agent/0123abcd"},
		// 39 letters then a separator: the cut at 40 bytes ends in a dash,
		// which is trimmed.
		{title: strings.Repeat("a", 39) + " bbbb", want: "agent/" + strings.Repeat("a", 39) + "-0123abcd"},
		{title: strings.Repeat("x", 60), want: "agent/" + strings.Repeat("x", 40) + "-0123abcd"},
	} {
		require.Equal(t, tc.want, forkWorktreeBranch(tc.title, chatID), "title %q", tc.title)
	}
}
