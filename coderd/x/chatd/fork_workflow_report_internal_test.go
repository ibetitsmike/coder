package chatd

import (
	"context"
	"encoding/json"
	"testing"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/sqlc-dev/pqtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
	"github.com/coder/coder/v2/codersdk"
)

func TestForkWorkflowReport(t *testing.T) {
	t.Parallel()

	schema := json.RawMessage(`{"type":"object","required":["bugs"],"properties":{"bugs":{"type":"array","items":{"type":"string"}}}}`)

	t.Run("ValidValueAccepted", func(t *testing.T) {
		t.Parallel()
		tool, err := newWorkflowReportTool(schema)
		require.NoError(t, err)
		assert.Equal(t, []string{"bugs"}, tool.Info().Required)

		resp, err := tool.Run(context.Background(), fantasy.ToolCall{Input: `{"bugs":["nil map write"]}`})
		require.NoError(t, err)
		assert.False(t, resp.IsError, resp.Content)
	})

	t.Run("InvalidValueRejected", func(t *testing.T) {
		t.Parallel()
		tool, err := newWorkflowReportTool(schema)
		require.NoError(t, err)

		resp, err := tool.Run(context.Background(), fantasy.ToolCall{Input: `{"bugs":"nil map write"}`})
		require.NoError(t, err)
		assert.True(t, resp.IsError)
		assert.Contains(t, resp.Content, "does not match the schema")
	})

	t.Run("SchemaChecks", func(t *testing.T) {
		t.Parallel()
		_, err := checkStepSchema(json.RawMessage(`{"type":"array"}`))
		require.ErrorContains(t, err, `"type": "object"`)
		_, err = checkStepSchema(json.RawMessage(`{"$schema":"http://json-schema.org/draft-04/schema#","type":"object"}`))
		require.ErrorContains(t, err, "unsupported $schema")
		_, err = checkStepSchema(json.RawMessage(`{"$schema":"http://json-schema.org/draft-07/schema#","type":"object"}`))
		require.NoError(t, err)
	})

	t.Run("InlinesLocalRefs", func(t *testing.T) {
		t.Parallel()
		// Fantasy rebuilds the provider schema from the properties alone, so
		// a $defs-based shape must be inlined or the provider sees a dangling
		// reference.
		withDefs := json.RawMessage(`{` +
			`"type":"object",` +
			`"$defs":{"result":{"type":"object","properties":{"score":{"type":"integer"}},"required":["score"]}},` +
			`"required":["result"],` +
			`"properties":{"result":{"$ref":"#/$defs/result"}}}`)
		tool, err := newWorkflowReportTool(withDefs)
		require.NoError(t, err)

		raw, err := json.Marshal(tool.Info().Parameters)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "$ref", "the parameter shape must not carry references")
		assert.NotContains(t, string(raw), "$defs")
		assert.JSONEq(t, `{"result":{"type":"object","properties":{"score":{"type":"integer"}},"required":["score"]}}`, string(raw))
		assert.Equal(t, []string{"result"}, tool.Info().Required)

		resp, err := tool.Run(context.Background(), fantasy.ToolCall{Input: `{"result":{"score":3}}`})
		require.NoError(t, err)
		assert.False(t, resp.IsError, resp.Content)
		resp, err = tool.Run(context.Background(), fantasy.ToolCall{Input: `{"result":{"score":"high"}}`})
		require.NoError(t, err)
		assert.True(t, resp.IsError)
	})

	t.Run("RejectsUninlinableRefs", func(t *testing.T) {
		t.Parallel()
		recursive := json.RawMessage(`{` +
			`"type":"object",` +
			`"$defs":{"node":{"type":"object","properties":{"child":{"$ref":"#/$defs/node"}}}},` +
			`"properties":{"node":{"$ref":"#/$defs/node"}}}`)
		_, err := newWorkflowReportTool(recursive)
		require.ErrorContains(t, err, "recursive")

		siblings := json.RawMessage(`{` +
			`"type":"object",` +
			`"$defs":{"tags":{"type":"array","items":{"type":"string"}}},` +
			`"properties":{"tags":{"$ref":"#/$defs/tags","description":"duplicate"}}}`)
		_, err = newWorkflowReportTool(siblings)
		require.ErrorContains(t, err, "sibling")

		remote := json.RawMessage(`{` +
			`"type":"object",` +
			`"properties":{"tags":{"$ref":"https://example.com/tags.json"}}}`)
		_, err = newWorkflowReportTool(remote)
		require.ErrorContains(t, err, "not a local reference")
	})

	t.Run("PreservesDataKeywords", func(t *testing.T) {
		t.Parallel()
		// const, default, enum and examples carry data, not schemas: a
		// $ref-shaped object inside them is literal JSON, so the inliner must
		// copy it verbatim, and the resolver must not load it as a reference.
		data := json.RawMessage(`{` +
			`"type":"object",` +
			`"$defs":{"literal":{"type":"string"}},` +
			`"properties":{` +
			`"pick":{"const":{"$ref":"https://example.com/const.json"}},` +
			`"one":{"enum":[{"$ref":"#/$defs/literal"},"plain"]},` +
			`"fallback":{"default":{"$ref":"#/$defs/literal"}},` +
			`"samples":{"examples":[{"$ref":"#/$defs/literal"}]}}}`)
		tool, err := newWorkflowReportTool(data)
		require.NoError(t, err)

		raw, err := json.Marshal(tool.Info().Parameters)
		require.NoError(t, err)
		assert.JSONEq(t, `{`+
			`"pick":{"const":{"$ref":"https://example.com/const.json"}},`+
			`"one":{"enum":[{"$ref":"#/$defs/literal"},"plain"]},`+
			`"fallback":{"default":{"$ref":"#/$defs/literal"}},`+
			`"samples":{"examples":[{"$ref":"#/$defs/literal"}]}}`, string(raw))
	})

	t.Run("OnlyStepChildrenGetTool", func(t *testing.T) {
		t.Parallel()
		raw, err := workflowStepTools(schema)
		require.NoError(t, err)
		tools := pqtype.NullRawMessage{RawMessage: raw, Valid: true}

		root := database.Chat{ID: uuid.New(), DynamicTools: tools}
		got, err := appendWorkflowReportTool(nil, root)
		require.NoError(t, err)
		assert.Empty(t, got, "a root chat must not get workflow_report")
		assert.Empty(t, forkStopAfterWorkflowReport(nil, root), "a root chat must not stop after workflow_report")

		child := root
		child.ParentChatID = uuid.NullUUID{UUID: uuid.New(), Valid: true}
		child.Labels = database.StringMap{workflowRunLabel: uuid.NewString()}
		got, err = appendWorkflowReportTool(nil, child)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, workflowReportToolName, got[0].Info().Name)
		stop := forkStopAfterWorkflowReport(map[string]struct{}{"other": {}}, child)
		assert.Contains(t, stop, workflowReportToolName)
		assert.Contains(t, stop, "other")
		assert.True(t, exploreToolAllowed(got[0]), "explore steps must keep workflow_report")

		// The parsers treat the step's workflow_report calls as server-side,
		// so the client never sees them as actions.
		assert.Empty(t, dynamicToolNamesFromChat(child))

		// A child without the run label is not a step the fork created: a
		// caller-supplied workflow_report def keeps its upstream client-side
		// behavior.
		noLabel := child
		noLabel.Labels = nil
		got, err = appendWorkflowReportTool(nil, noLabel)
		require.NoError(t, err)
		assert.Empty(t, got)
		assert.Empty(t, forkStopAfterWorkflowReport(nil, noLabel))
		assert.Contains(t, dynamicToolNamesFromChat(noLabel), workflowReportToolName)

		// The shared parser stays upstream: it has no chat, so callers with
		// the chat apply the step filter themselves.
		names, err := parseDynamicToolNames(tools)
		require.NoError(t, err)
		assert.Contains(t, names, workflowReportToolName)
	})

	t.Run("BrokenDynamicToolsColumnIsIgnored", func(t *testing.T) {
		t.Parallel()
		child := database.Chat{
			ID:           uuid.New(),
			ParentChatID: uuid.NullUUID{UUID: uuid.New(), Valid: true},
			DynamicTools: pqtype.NullRawMessage{RawMessage: []byte("{broken"), Valid: true},
		}
		got, err := appendWorkflowReportTool(nil, child)
		require.NoError(t, err)
		assert.Empty(t, got)
		assert.Empty(t, forkStopAfterWorkflowReport(nil, child))
	})

	t.Run("StepIDs", func(t *testing.T) {
		t.Parallel()
		runID := uuid.New()
		assert.Equal(t, workflowStepID(runID, "k", 0), workflowStepID(runID, "k", 0))
		assert.NotEqual(t, workflowStepID(runID, "k", 0), workflowStepID(runID, "k", 1))
		assert.NotEqual(t, workflowStepID(runID, "k", 0), workflowStepID(uuid.New(), "k", 0))
		callID := uuid.New()
		assert.Equal(t, workflowRunID(callID), workflowRunID(callID))
	})

	t.Run("ForkChildChatID", func(t *testing.T) {
		t.Parallel()
		opts := childSubagentChatOptions{}
		first := opts.forkChildChatID()
		assert.NotEqual(t, uuid.Nil, first)
		assert.NotEqual(t, first, opts.forkChildChatID(), "each non-step spawn gets a fresh ID")

		opts.fork = &forkChildChatOverrides{chatID: workflowStepID(uuid.New(), "k", 0)}
		assert.Equal(t, opts.fork.chatID, opts.forkChildChatID())
		assert.Equal(t, opts.fork.chatID, opts.forkChildChatID(), "the step ID is stable")
	})
}

// TestForkWorkflowStepChat covers the deterministic child chat creation and
// the reuse path that a resumed run needs.
func TestForkWorkflowStepChat(t *testing.T) {
	t.Parallel()

	t.Run("CreatesAndReuses", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parent(ctx, t)

		runID := uuid.New()
		spec := stepSpec{Prompt: "Review a.go for bugs.", Type: subagentTypeGeneral, Title: "review"}
		stepID := workflowStepID(runID, "k", 0)

		child, reused, err := createWorkflowStepChat(ctx, f.server, parent, f.model.ID, runID, stepID, spec)
		require.NoError(t, err)
		assert.False(t, reused)
		assert.Equal(t, stepID, child.ID, "the child chat ID is the deterministic step ID")
		assert.Equal(t, parent.ID, child.ParentChatID.UUID)
		assert.Equal(t, "review", child.Title)
		assert.Equal(t, runID.String(), child.Labels[workflowRunLabel])
		require.True(t, child.DynamicTools.Valid)
		var defs []codersdk.DynamicTool
		require.NoError(t, json.Unmarshal(child.DynamicTools.RawMessage, &defs))
		require.Len(t, defs, 1)
		assert.Equal(t, workflowReportToolName, defs[0].Name)
		assert.False(t, child.PlanMode.Valid, "a step never runs in plan mode")

		// The same step ID reuses the chat without creating a new one.
		child2, reused, err := createWorkflowStepChat(ctx, f.server, parent, f.model.ID, runID, stepID, spec)
		require.NoError(t, err)
		assert.True(t, reused)
		assert.Equal(t, stepID, child2.ID)

		// A different spec key gives a different chat.
		other := spec
		other.Prompt = "Review b.go for bugs."
		otherID := workflowStepID(runID, "k2", 0)
		child3, reused, err := createWorkflowStepChat(ctx, f.server, parent, f.model.ID, runID, otherID, other)
		require.NoError(t, err)
		assert.False(t, reused)
		assert.Equal(t, otherID, child3.ID)
		assert.NotEqual(t, stepID, otherID)
	})

	t.Run("DefaultSchemaAndType", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parent(ctx, t)

		runID := uuid.New()
		child, reused, err := createWorkflowStepChat(ctx, f.server, parent, f.model.ID, runID,
			workflowStepID(runID, "k", 0), stepSpec{Prompt: "Say hi."})
		require.NoError(t, err)
		assert.False(t, reused)
		var defs []codersdk.DynamicTool
		require.NoError(t, json.Unmarshal(child.DynamicTools.RawMessage, &defs))
		require.Len(t, defs, 1)
		var schema struct {
			Type string `json:"type"`
		}
		require.NoError(t, json.Unmarshal(defs[0].InputSchema, &schema))
		assert.Equal(t, "object", schema.Type, "an empty schema gets the default object schema")
	})

	t.Run("RejectsUnknownTypeAndBadSchema", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parent(ctx, t)
		runID := uuid.New()

		_, _, err := createWorkflowStepChat(ctx, f.server, parent, f.model.ID, runID,
			workflowStepID(runID, "k", 0), stepSpec{Prompt: "x", Type: "reviewer"})
		require.ErrorContains(t, err, "type must be")

		_, _, err = createWorkflowStepChat(ctx, f.server, parent, f.model.ID, runID,
			workflowStepID(runID, "k", 0), stepSpec{Prompt: "x", Schema: json.RawMessage(`{"type":"array"}`)})
		require.ErrorContains(t, err, `"type": "object"`)
	})
}

// newForkWorkflowFixture builds a passive server with the fork-workflows
// experiment on, the way the workflow tests drive steps without a live turn.
type forkWorkflowFixture struct {
	db     database.Store
	server *Server
	user   database.User
	org    database.Organization
	model  database.ChatModelConfig
}

func newForkWorkflowFixture(t *testing.T) *forkWorkflowFixture {
	t.Helper()
	db, ps := dbtestutil.NewDB(t)
	server := newInternalTestServer(t, db, ps, chatprovider.ProviderAPIKeys{},
		withInternalTestServerExperiments(codersdk.Experiments{codersdk.ExperimentForkWorkflows}))
	user, org, model := seedInternalChatDeps(t, db)
	return &forkWorkflowFixture{db: db, server: server, user: user, org: org, model: model}
}

func (f *forkWorkflowFixture) parent(ctx context.Context, t *testing.T) database.Chat {
	t.Helper()
	parent, err := f.server.CreateChat(ctx, CreateOptions{
		OrganizationID:     f.org.ID,
		OwnerID:            f.user.ID,
		Title:              "workflow-parent-" + t.Name(),
		ModelConfigID:      f.model.ID,
		InitialUserContent: []codersdk.ChatMessagePart{codersdk.ChatMessageText("hello")},
	})
	require.NoError(t, err)
	return parent
}
