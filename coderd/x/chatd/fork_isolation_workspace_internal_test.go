package chatd

// This file is fork code: it tests spawn_agent's isolation workspace.

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbfake"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprompt"
	"github.com/coder/coder/v2/codersdk"
)

// fakeWorkspaces stands in for coderd's workspace creation and stop. A
// created workspace is bound to the chat; its build fails with buildFails,
// and createErr fails the creation itself.
type fakeWorkspaces struct {
	t          *testing.T
	db         database.Store
	createErr  error
	buildFails bool

	mu      sync.Mutex
	creates []codersdk.CreateWorkspaceRequest
	created []database.WorkspaceTable
	stops   []uuid.UUID
}

func (w *fakeWorkspaces) create(ctx context.Context, ownerID, chatID uuid.UUID, req codersdk.CreateWorkspaceRequest) (codersdk.Workspace, database.Chat, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.creates = append(w.creates, req)
	if w.createErr != nil {
		return codersdk.Workspace{}, database.Chat{}, w.createErr
	}
	template, err := w.db.GetTemplateByID(ctx, req.TemplateID)
	if err != nil {
		return codersdk.Workspace{}, database.Chat{}, err
	}
	builder := dbfake.WorkspaceBuild(w.t, w.db, database.WorkspaceTable{
		OwnerID:        ownerID,
		OrganizationID: template.OrganizationID,
		TemplateID:     template.ID,
		Name:           req.Name,
	}).Seed(database.WorkspaceBuild{TemplateVersionID: template.ActiveVersionID})
	if w.buildFails {
		builder = builder.Failed()
	}
	built := builder.Do()
	w.created = append(w.created, built.Workspace)
	chat, err := w.db.UpdateChatWorkspaceBinding(ctx, database.UpdateChatWorkspaceBindingParams{
		ID:          chatID,
		WorkspaceID: uuid.NullUUID{UUID: built.Workspace.ID, Valid: true},
		BuildID:     uuid.NullUUID{UUID: built.Build.ID, Valid: true},
	})
	if err != nil {
		return codersdk.Workspace{}, database.Chat{}, err
	}
	return codersdk.Workspace{
		ID:          built.Workspace.ID,
		Name:        built.Workspace.Name,
		LatestBuild: codersdk.WorkspaceBuild{ID: built.Build.ID},
	}, chat, nil
}

func (w *fakeWorkspaces) stop(_ context.Context, _ uuid.UUID, workspaceID uuid.UUID, req codersdk.CreateWorkspaceBuildRequest) (codersdk.WorkspaceBuild, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	require.Equal(w.t, codersdk.WorkspaceTransitionStop, req.Transition)
	w.stops = append(w.stops, workspaceID)
	return codersdk.WorkspaceBuild{}, nil
}

// workspaceRoot returns a root chat bound to a workspace of a new template in
// f's organization, built with one parameter, and the template.
func (f *isolationFixture) workspaceRoot(t *testing.T, workspaces *fakeWorkspaces) (database.Chat, database.Template) {
	t.Helper()
	ctx := chatdTestContext(t)
	f.server.createWorkspaceFn = workspaces.create
	f.server.stopWorkspaceFn = workspaces.stop
	version := dbgen.TemplateVersion(t, f.db, database.TemplateVersion{OrganizationID: f.org.ID, CreatedBy: f.user.ID})
	template := dbgen.Template(t, f.db, database.Template{
		OrganizationID:  f.org.ID,
		CreatedBy:       f.user.ID,
		ActiveVersionID: version.ID,
		AgentsAllowed:   true,
	})
	built := dbfake.WorkspaceBuild(t, f.db, database.WorkspaceTable{
		OwnerID:        f.user.ID,
		OrganizationID: f.org.ID,
		TemplateID:     template.ID,
	}).Seed(database.WorkspaceBuild{TemplateVersionID: version.ID}).
		Params(database.WorkspaceBuildParameter{Name: "region", Value: "eu-west"}).
		WithAgent().
		Do()
	root, err := f.server.CreateChat(ctx, CreateOptions{
		OrganizationID:     f.org.ID,
		OwnerID:            f.user.ID,
		Title:              "root",
		ModelConfigID:      f.model.ID,
		InitialUserContent: []codersdk.ChatMessagePart{codersdk.ChatMessageText("hello")},
		WorkspaceID:        uuid.NullUUID{UUID: built.Workspace.ID, Valid: true},
		BuildID:            uuid.NullUUID{UUID: built.Build.ID, Valid: true},
		AgentID:            uuid.NullUUID{UUID: built.Agents[0].ID, Valid: true},
	})
	require.NoError(t, err)
	return root, template
}

func TestForkSpawnIsolationWorkspace(t *testing.T) {
	t.Parallel()

	// Each case sets the fixture's workspace functions, so cases get their
	// own fixture.
	t.Run("CreatesWorkspaceThenSendsPrompt", func(t *testing.T) {
		t.Parallel()
		f := newIsolationFixture(t, codersdk.Experiments{codersdk.ExperimentForkSubagentIsolation})
		ctx := chatdTestContext(t)
		workspaces := &fakeWorkspaces{t: t, db: f.db}
		root, template := f.workspaceRoot(t, workspaces)

		resp := f.spawn(t, root, map[string]any{"isolation": "workspace", "title": "child"})
		require.False(t, resp.IsError, resp.Content)
		var result struct {
			ChatID        string `json:"chat_id"`
			Isolation     string `json:"isolation"`
			WorkspaceName string `json:"workspace_name"`
		}
		require.NoError(t, json.Unmarshal([]byte(resp.Content), &result))
		childID, err := uuid.Parse(result.ChatID)
		require.NoError(t, err)

		workspaces.mu.Lock()
		creates, created := workspaces.creates, workspaces.created
		workspaces.mu.Unlock()
		require.Len(t, creates, 1)
		require.Equal(t, template.ID, creates[0].TemplateID)
		require.Equal(t, []codersdk.WorkspaceBuildParameter{{Name: "region", Value: "eu-west"}}, creates[0].RichParameterValues)
		require.Len(t, created, 1)
		require.Equal(t, "workspace", result.Isolation)
		require.Equal(t, created[0].Name, result.WorkspaceName)

		child, err := f.db.GetChatByID(ctx, childID)
		require.NoError(t, err)
		require.Equal(t, uuid.NullUUID{UUID: created[0].ID, Valid: true}, child.WorkspaceID)
		require.False(t, child.Archived)
		row, err := f.fdb.GetChatSubagentByChatID(ctx, childID)
		require.NoError(t, err)
		require.Equal(t, "workspace", row.Isolation)
		require.Equal(t, child.WorkspaceID, row.WorkspaceID)

		messages, err := f.db.GetChatMessagesByChatID(ctx, database.GetChatMessagesByChatIDParams{ChatID: childID})
		require.NoError(t, err)
		var prompts []string
		for _, message := range messages {
			if message.Role != database.ChatMessageRoleUser {
				continue
			}
			parts, err := chatprompt.ParseContent(message)
			require.NoError(t, err)
			prompts = append(prompts, parts[0].Text)
		}
		require.Equal(t, []string{"do the work"}, prompts)

		require.NoError(t, WaitUntilIdleForTest(ctx, f.server))
		setChatStatus(ctx, t, f.db, childID, database.ChatStatusWaiting, "")
		insertAssistantMessage(t, f.db, childID, f.model.ID, "task complete")
		tools, err := f.server.subagentTools(ctx, func() database.Chat { return root }, root.LastModelConfigID)
		require.NoError(t, err)
		tools, err = f.server.appendForkTools(ctx, tools, root, root.LastModelConfigID)
		require.NoError(t, err)
		wait := findToolByName(tools, waitAgentToolName)
		require.NotNil(t, wait)
		resp, err = wait.Run(ctx, fantasy.ToolCall{ID: uuid.NewString(), Name: waitAgentToolName, Input: `{"chat_id":"` + childID.String() + `"}`})
		require.NoError(t, err)
		require.False(t, resp.IsError, resp.Content)
		var waited map[string]any
		require.NoError(t, json.Unmarshal([]byte(resp.Content), &waited))
		require.Equal(t, "workspace", waited["isolation"])
		require.Equal(t, created[0].Name, waited["workspace_name"])
	})

	for _, tc := range []struct {
		name       string
		workspaces func(t *testing.T, db database.Store) *fakeWorkspaces
		wantErr    string
		wantStop   bool
	}{
		{
			name: "CreateFails",
			workspaces: func(t *testing.T, db database.Store) *fakeWorkspaces {
				return &fakeWorkspaces{t: t, db: db, createErr: xerrors.New("workspace quota exceeded")}
			},
			wantErr: "create the child's workspace: workspace quota exceeded",
		},
		{
			name: "BuildFails",
			workspaces: func(t *testing.T, db database.Store) *fakeWorkspaces {
				return &fakeWorkspaces{t: t, db: db, buildFails: true}
			},
			wantErr:  "workspace build failed",
			wantStop: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newIsolationFixture(t, codersdk.Experiments{codersdk.ExperimentForkSubagentIsolation})
			ctx := chatdTestContext(t)
			workspaces := tc.workspaces(t, f.db)
			root, _ := f.workspaceRoot(t, workspaces)

			resp := f.spawn(t, root, map[string]any{"isolation": "workspace"})
			require.True(t, resp.IsError, resp.Content)
			require.Contains(t, resp.Content, tc.wantErr)

			children := f.children(t, root)
			require.Len(t, children, 1)
			child, err := f.db.GetChatByID(ctx, children[0].Chat.ID)
			require.NoError(t, err)
			require.True(t, child.Archived)
			require.Equal(t, database.ChatStatusWaiting, child.Status)

			workspaces.mu.Lock()
			defer workspaces.mu.Unlock()
			if tc.wantStop {
				require.Len(t, workspaces.created, 1)
				require.Equal(t, []uuid.UUID{workspaces.created[0].ID}, workspaces.stops)
			} else {
				require.Empty(t, workspaces.stops)
			}
		})
	}

	t.Run("NoWorkspace", func(t *testing.T) {
		t.Parallel()
		f := newIsolationFixture(t, codersdk.Experiments{codersdk.ExperimentForkSubagentIsolation})
		workspaces := &fakeWorkspaces{t: t, db: f.db}
		f.server.createWorkspaceFn = workspaces.create
		root := f.rootChat(t, nil)

		resp := f.spawn(t, root, map[string]any{"isolation": "workspace"})
		require.True(t, resp.IsError, resp.Content)
		require.Contains(t, resp.Content, "this chat has none")
		require.Empty(t, f.children(t, root))
		require.Empty(t, workspaces.creates)
	})
}
