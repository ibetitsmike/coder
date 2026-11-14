package chatd

// This file is fork code: it tests saved workflows of the fork-workflows
// experiment from the agent side: run_workflow by name, list_workflows, the
// tiers and their access rules, and that no agent tool writes them.

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sqlc-dev/pqtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"cdr.dev/slog/v3/sloggers/slogtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/coderd/rbac/policy"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/testutil"
)

const greetWorkflowScript = `def main(args):
    return agent("Say %s." % args["topic"], title = args["topic"])
`

const greetArgsSchema = `{"type":"object","required":["topic"],"properties":{"topic":{"type":"string"}}}`

func titleWorkflowScript(title string) string {
	return fmt.Sprintf("def main(args):\n    return agent(\"Work.\", title = %q)\n", title)
}

func (f *forkWorkflowFixture) saveUserWorkflow(t *testing.T, name, script, schema string) forkdb.SavedWorkflow {
	t.Helper()
	params := forkdb.InsertUserSavedWorkflowParams{
		OrganizationID: f.org.ID,
		UserID:         f.user.ID,
		Name:           name,
		Description:    "Personal " + name + ".",
		Script:         script,
		Phases:         []string{"plan", "work"},
	}
	if schema != "" {
		params.ArgsSchema = pqtype.NullRawMessage{RawMessage: json.RawMessage(schema), Valid: true}
	}
	row, err := forkWorkflowDB(t, f.db).InsertUserSavedWorkflow(context.Background(), params)
	require.NoError(t, err)
	return row
}

func (f *forkWorkflowFixture) saveOrgWorkflow(t *testing.T, name, script string) forkdb.SavedWorkflow {
	t.Helper()
	row, err := forkWorkflowDB(t, f.db).InsertOrganizationSavedWorkflow(context.Background(), forkdb.InsertOrganizationSavedWorkflowParams{
		OrganizationID: f.org.ID,
		CreatedBy:      f.user.ID,
		Name:           name,
		Description:    "Team " + name + ".",
		Script:         script,
		GroupAcl:       database.ChatACL{f.org.ID.String(): {Permissions: []policy.Action{policy.ActionRead}}},
		UserAcl:        database.ChatACL{},
	})
	require.NoError(t, err)
	return row
}

// runTool calls one of the workflow tools with a fresh tool call ID.
func runTool(ctx context.Context, t *testing.T, tool fantasy.AgentTool, input string) fantasy.ToolResponse {
	t.Helper()
	resp, err := tool.Run(workspacesdk.WithToolCallID(ctx, uuid.New()), fantasy.ToolCall{Input: input})
	require.NoError(t, err)
	return resp
}

// startByName runs a saved workflow by name and returns its run.
func (f *forkWorkflowFixture) startByName(ctx context.Context, t *testing.T, server *Server, parent database.Chat, input string) *workflowRun {
	t.Helper()
	tools := server.forkWorkflowTools(ctx, parent, f.model.ID, nil)
	resp := runTool(ctx, t, tools[0], input)
	require.False(t, resp.IsError, resp.Content)
	var started runWorkflowResult
	require.NoError(t, json.Unmarshal([]byte(resp.Content), &started))
	run := server.workflows.get(uuid.MustParse(started.RunID))
	require.NotNil(t, run)
	return run
}

func listWorkflows(ctx context.Context, t *testing.T, tools []fantasy.AgentTool, input string) []listedWorkflow {
	t.Helper()
	resp := runTool(ctx, t, tools[2], input)
	require.False(t, resp.IsError, resp.Content)
	var out struct {
		Workflows []listedWorkflow `json:"workflows"`
	}
	require.NoError(t, json.Unmarshal([]byte(resp.Content), &out))
	return out.Workflows
}

func TestForkSavedWorkflowTools(t *testing.T) {
	t.Parallel()

	t.Run("RunByName", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		saved := f.saveUserWorkflow(t, "greet", greetWorkflowScript, greetArgsSchema)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)

		run := f.startByName(ctx, t, f.server, parent, `{"name":"greet","args":{"topic":"hello"}}`)
		step := f.steps(ctx, t, run, 1)[0]
		assert.Equal(t, "hello", step.Title)
		row, err := forkWorkflowDB(t, f.db).GetWorkflowRun(ctx, run.id)
		require.NoError(t, err)
		assert.Equal(t, saved.Script, row.Script, "the journal holds the saved script")
		assert.JSONEq(t, `{"topic":"hello"}`, string(row.Args))

		f.report(ctx, t, step.ChatID, `{"text":"hello"}`)
		tools := f.server.forkWorkflowTools(ctx, parent, f.model.ID, nil)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			resp := runTool(ctx, t, tools[1], fmt.Sprintf(`{"run_id":%q,"wait_seconds":0}`, run.id))
			var res runWorkflowResult
			return !resp.IsError && json.Unmarshal([]byte(resp.Content), &res) == nil &&
				res.Status == workflowStatusCompleted && string(res.Result) == `{"text":"hello"}`
		}, testutil.IntervalFast)
	})

	t.Run("PersonalHidesOrganization", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		f.saveUserWorkflow(t, "dup", titleWorkflowScript("personal dup"), "")
		f.saveOrgWorkflow(t, "dup", titleWorkflowScript("team dup"))
		f.saveOrgWorkflow(t, "team", titleWorkflowScript("team"))
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)

		tools := f.server.forkWorkflowTools(ctx, parent, f.model.ID, nil)
		listed := listWorkflows(ctx, t, tools, `{}`)
		require.Len(t, listed, 2)
		assert.Equal(t, listedWorkflow{Name: "dup", Tier: "personal", Description: "Personal dup.", Phases: []string{"plan", "work"}}, listed[0])
		assert.Equal(t, listedWorkflow{Name: "team", Tier: "organization", Description: "Team team."}, listed[1])

		run := f.startByName(ctx, t, f.server, parent, `{"name":"dup"}`)
		assert.Equal(t, "personal dup", f.steps(ctx, t, run, 1)[0].Title)
		run = f.startByName(ctx, t, f.server, parent, `{"name":"team"}`)
		assert.Equal(t, "team", f.steps(ctx, t, run, 1)[0].Title)
	})

	t.Run("ListDetails", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		long := strings.Repeat("é", maxListedWorkflowDescription)
		_, err := forkWorkflowDB(t, f.db).InsertUserSavedWorkflow(ctx, forkdb.InsertUserSavedWorkflowParams{
			OrganizationID: f.org.ID,
			UserID:         f.user.ID,
			Name:           "long",
			Description:    long,
			Script:         greetWorkflowScript,
			ArgsSchema:     pqtype.NullRawMessage{RawMessage: json.RawMessage(greetArgsSchema), Valid: true},
		})
		require.NoError(t, err)
		tools := f.server.forkWorkflowTools(ctx, f.parent(ctx, t), f.model.ID, nil)

		listed := listWorkflows(ctx, t, tools, `{}`)
		require.Len(t, listed, 1)
		assert.True(t, listed[0].HasArgsSchema)
		assert.Empty(t, listed[0].ArgsSchema, "the list leaves the schema out")
		assert.LessOrEqual(t, len(listed[0].Description), maxListedWorkflowDescription+3)
		assert.True(t, strings.HasSuffix(listed[0].Description, "..."))
		assert.True(t, strings.HasPrefix(long, strings.TrimSuffix(listed[0].Description, "...")), "the cut keeps whole runes")

		resp := runTool(ctx, t, tools[2], `{"name":"long"}`)
		require.False(t, resp.IsError, resp.Content)
		var detail listedWorkflow
		require.NoError(t, json.Unmarshal([]byte(resp.Content), &detail))
		assert.Equal(t, long, detail.Description)
		assert.JSONEq(t, greetArgsSchema, string(detail.ArgsSchema))

		resp = runTool(ctx, t, tools[2], `{"name":"missing"}`)
		assert.True(t, resp.IsError)
		assert.Contains(t, resp.Content, `no saved workflow is named "missing"`)
	})

	t.Run("RejectsBadInput", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		f.saveUserWorkflow(t, "greet", greetWorkflowScript, greetArgsSchema)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		tools := f.server.forkWorkflowTools(ctx, parent, f.model.ID, nil)

		for input, want := range map[string]string{
			`{"name":"greet"}`:                                    `args do not match the args schema of workflow "greet"`,
			`{"name":"greet","args":{"topic":7}}`:                 `args do not match the args schema of workflow "greet"`,
			`{"name":"nope","args":{"topic":"x"}}`:                `no saved workflow is named "nope"`,
			`{"name":"greet","script":"def main(args): pass"}`:    "not more than one",
			`{"name":"greet","script_path":"/home/coder/w.star"}`: "not more than one",
		} {
			resp := runTool(ctx, t, tools[0], input)
			assert.True(t, resp.IsError, "%s: %s", input, resp.Content)
			assert.Contains(t, resp.Content, want, input)
		}
		runs, err := forkWorkflowDB(t, f.db).GetWorkflowRunsByChat(ctx, forkdb.GetWorkflowRunsByChatParams{ChatID: parent.ID, RowLimit: 10})
		require.NoError(t, err)
		assert.Empty(t, runs, "a rejected call starts no run")
	})

	t.Run("RestartReadsSavedWorkflow", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		f.saveUserWorkflow(t, "greet", greetWorkflowScript, greetArgsSchema)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)

		restarted := f.restartWorkflowServer(t)
		tools := restarted.server.forkWorkflowTools(ctx, parent, f.model.ID, nil)
		listed := listWorkflows(ctx, t, tools, `{}`)
		require.Len(t, listed, 1)
		assert.Equal(t, "greet", listed[0].Name)
		run := f.startByName(ctx, t, restarted.server, parent, `{"name":"greet","args":{"topic":"again"}}`)
		assert.Equal(t, "again", f.steps(ctx, t, run, 1)[0].Title)
	})

	t.Run("ToolsOnlyRead", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		before := f.saveUserWorkflow(t, "greet", greetWorkflowScript, greetArgsSchema)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		tools := f.server.forkWorkflowTools(ctx, parent, f.model.ID, nil)

		names := make([]string, 0, len(tools))
		for _, tool := range tools {
			names = append(names, tool.Info().Name)
		}
		assert.Equal(t, []string{runWorkflowToolName, awaitWorkflowToolName, listWorkflowsToolName}, names)
		assert.Contains(t, tools[2].Info().Parameters, "name")
		assert.NotContains(t, tools[2].Info().Parameters, "script", "list_workflows takes no script")

		listWorkflows(ctx, t, tools, `{}`)
		runTool(ctx, t, tools[2], `{"name":"greet"}`)
		f.steps(ctx, t, f.startByName(ctx, t, f.server, parent, `{"name":"greet","args":{"topic":"x"}}`), 1)
		after, err := forkWorkflowDB(t, f.db).GetUserSavedWorkflowByName(ctx, forkdb.GetUserSavedWorkflowByNameParams{
			OrganizationID: f.org.ID, UserID: f.user.ID, Name: "greet",
		})
		require.NoError(t, err)
		assert.Equal(t, before, after, "running and listing leave the saved workflow as it was")
	})
}

// TestForkSavedWorkflowsNoWriter fails when chatd code calls a query that
// creates, edits or deletes a saved workflow. Only the coderd routes write
// them, so no agent tool can.
func TestForkSavedWorkflowsNoWriter(t *testing.T) {
	t.Parallel()

	writer := regexp.MustCompile(`^(Insert|Update|Upsert|Delete)\w*SavedWorkflow`)
	fset := token.NewFileSet()
	var scanned int
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		scanned++
		ast.Inspect(file, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && writer.MatchString(sel.Sel.Name) {
				t.Errorf("%s calls %s", fset.Position(sel.Pos()), sel.Sel.Name)
			}
			return true
		})
		return nil
	})
	require.NoError(t, err)
	require.Greater(t, scanned, 10, "the scan found the chatd sources")
}

func TestForkSavedWorkflowsAuthz(t *testing.T) {
	t.Parallel()

	ctx := chatdTestContext(t)
	db, _ := dbtestutil.NewDB(t)
	authzDB := dbauthz.New(db, rbac.NewStrictCachingAuthorizer(prometheus.NewRegistry()), slogtest.Make(t, nil), nil)
	server := &Server{
		db:          authzDB,
		logger:      slogtest.Make(t, &slogtest.Options{IgnoreErrors: true}),
		experiments: codersdk.Experiments{codersdk.ExperimentForkWorkflows},
		modelConfigContext: func(ctx context.Context, ownerID uuid.UUID) (context.Context, error) {
			return callerModelConfigContext(ctx, authzDB, ownerID)
		},
	}
	fdb := forkWorkflowDB(t, db)
	org := dbgen.Organization(t, db, database.Organization{})
	member := dbgen.User(t, db, database.User{})
	other := dbgen.User(t, db, database.User{})
	outsider := dbgen.User(t, db, database.User{})
	for _, user := range []database.User{member, other} {
		dbgen.OrganizationMember(t, db, database.OrganizationMember{UserID: user.ID, OrganizationID: org.ID})
	}
	for name, user := range map[string]database.User{"member-wf": member, "other-wf": other, "outsider-wf": outsider} {
		_, err := fdb.InsertUserSavedWorkflow(ctx, forkdb.InsertUserSavedWorkflowParams{
			OrganizationID: org.ID, UserID: user.ID, Name: name, Script: titleWorkflowScript(name),
		})
		require.NoError(t, err)
	}
	for name, groupACL := range map[string]database.ChatACL{
		"shared":  {org.ID.String(): {Permissions: []policy.Action{policy.ActionRead}}},
		"private": {},
	} {
		_, err := fdb.InsertOrganizationSavedWorkflow(ctx, forkdb.InsertOrganizationSavedWorkflowParams{
			OrganizationID: org.ID, CreatedBy: member.ID, Name: name, Script: titleWorkflowScript(name),
			GroupAcl: groupACL, UserAcl: database.ChatACL{},
		})
		require.NoError(t, err)
	}

	chatOf := func(user database.User) database.Chat {
		return database.Chat{ID: uuid.New(), OwnerID: user.ID, OrganizationID: org.ID}
	}
	names := func(user database.User) []string {
		listed, err := server.listSavedWorkflows(ctx, chatOf(user))
		require.NoError(t, err)
		out := []string{}
		for _, w := range listed {
			out = append(out, w.Tier+"/"+w.Name)
		}
		return out
	}
	assert.Equal(t, []string{"personal/member-wf", "organization/shared"}, names(member))
	assert.Equal(t, []string{"personal/other-wf", "organization/shared"}, names(other))
	assert.Empty(t, names(outsider), "a user outside the organization sees nothing")

	_, script, err := server.loadSavedWorkflow(ctx, chatOf(member), "shared")
	require.NoError(t, err)
	assert.Equal(t, titleWorkflowScript("shared"), script)
	for _, name := range []string{"other-wf", "private", "outsider-wf"} {
		_, _, err := server.loadSavedWorkflow(ctx, chatOf(member), name)
		assert.ErrorContains(t, err, "no saved workflow is named", name)
	}
	_, _, err = server.loadSavedWorkflow(ctx, chatOf(outsider), "shared")
	assert.ErrorContains(t, err, "no saved workflow is named")
}
