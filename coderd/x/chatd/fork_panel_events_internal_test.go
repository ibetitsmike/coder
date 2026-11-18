package chatd

// This file is fork code: it tests the fork_change watch events that let
// the fork panels refetch on a change instead of polling.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	coderdpubsub "github.com/coder/coder/v2/coderd/pubsub"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
	"github.com/coder/coder/v2/coderd/x/chatd/chatstate"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/testutil"
)

// TestForkChangeEvents checks that each committed write behind a fork panel
// publishes one fork_change event on the owner's watch stream, with the
// facet of the panel and the chat it belongs to.
func TestForkChangeEvents(t *testing.T) {
	t.Parallel()
	db, ps := dbtestutil.NewDB(t)
	server := newInternalTestServer(t, db, ps, chatprovider.ProviderAPIKeys{},
		withInternalTestServerExperiments(codersdk.Experiments{
			codersdk.ExperimentForkWorkflows, codersdk.ExperimentForkChatWakeups,
		}))
	user, org, model := seedInternalChatDeps(t, db)
	f := &forkWorkflowFixture{db: db, ps: ps, server: server, user: user, org: org, model: model}
	ctx := testutil.Context(t, testutil.WaitLong)
	root := f.parent(ctx, t)
	created, err := chatstate.CreateChat(ctx, db, ps, chatstate.CreateChatInput{
		OrganizationID:    org.ID,
		OwnerID:           user.ID,
		LastModelConfigID: model.ID,
		ParentChatID:      uuid.NullUUID{UUID: root.ID, Valid: true},
		RootChatID:        uuid.NullUUID{UUID: root.ID, Valid: true},
		Title:             "child",
		ClientType:        database.ChatClientTypeApi,
		InitialStatus:     database.ChatStatusRunning,
		InitialMessages:   []chatstate.Message{userTextMessage(t, "hello", user.ID, model.ID)},
	})
	require.NoError(t, err)
	child := created.Chat

	events := make(chan codersdk.ChatWatchEvent, 16)
	cancel, err := ps.Subscribe(coderdpubsub.ChatWatchEventChannel(user.ID), func(_ context.Context, payload []byte) {
		var event codersdk.ChatWatchEvent
		if json.Unmarshal(payload, &event) == nil && event.Kind == codersdk.ChatWatchEventKindForkChange {
			events <- event
		}
	})
	require.NoError(t, err)
	defer cancel()

	expect := func(chat database.Chat, facet codersdk.ChatWatchForkFacet, runID *uuid.UUID) {
		t.Helper()
		event := testutil.RequireReceive(ctx, t, events)
		require.Equal(t, uuid.Nil, event.Chat.ID, "old clients merge an empty chat, which changes nothing")
		require.NotNil(t, event.ForkChange)
		require.Equal(t, codersdk.ChatWatchForkChange{
			ChatID:        chat.ID,
			RootChatID:    root.ID,
			Facets:        []codersdk.ChatWatchForkFacet{facet},
			WorkflowRunID: runID,
		}, *event.ForkChange)
	}

	// A descendant's question reaches the root's page.
	err = server.recordDescendantQuestion(ctx, child.ID, "call-1",
		json.RawMessage(`[{"header":"h","question":"q?","options":[{"label":"a"},{"label":"b"}]}]`), false)
	require.NoError(t, err)
	expect(child, codersdk.ChatWatchForkFacetQuestions, nil)

	// A wake-up the agent schedules, and the user's pause of it.
	tools, err := server.appendForkTools(ctx, nil, root, root.LastModelConfigID)
	require.NoError(t, err)
	content := runChatTodoTool(workspacesdk.WithToolCallID(ctx, uuid.New()), t, tools, scheduleWakeupToolName,
		scheduleWakeupArgs{Prompt: "check", IntervalSeconds: new(60)}, false)
	expect(root, codersdk.ChatWatchForkFacetWakeups, nil)
	var scheduled scheduleWakeupResult
	require.NoError(t, json.Unmarshal([]byte(content), &scheduled))
	_, _, err = server.SetChatWakeupStatus(ctx, root, scheduled.ID, forkdb.ChatWakeupStatusPaused)
	require.NoError(t, err)
	expect(root, codersdk.ChatWatchForkFacetWakeups, nil)

	// The journal writes of a workflow run. The run is not registered, so
	// the step writes find its chat through the journal row.
	j := server.workflowJournal()
	run := &workflowRun{
		p: server, id: uuid.New(), parent: root, modelConfigID: model.ID,
		script: "def main(ctx):\n    pass\n", argsJSON: "{}",
	}
	require.NoError(t, j.insertRun(ctx, run))
	expect(root, codersdk.ChatWatchForkFacetWorkflows, &run.id)
	step := &workflowStep{id: uuid.New(), specKey: "review", status: stepStatusRunning}
	require.NoError(t, j.insertStep(ctx, run.id, step))
	expect(root, codersdk.ChatWatchForkFacetWorkflows, &run.id)
	step.status, step.value = stepStatusReported, json.RawMessage(`{"ok":true}`)
	j.updateStep(ctx, run.id, step)
	expect(root, codersdk.ChatWatchForkFacetWorkflows, &run.id)
	j.setLogs(ctx, run.id, 1, []string{"line"})
	expect(root, codersdk.ChatWatchForkFacetWorkflowLogs, &run.id)
	j.updateRunStatus(ctx, run.id, workflowStatusCompleted, "", nil, nil)
	expect(root, codersdk.ChatWatchForkFacetWorkflows, &run.id)

	// A write that changes no row publishes nothing: the next event is the
	// marker after it.
	j.updateStep(ctx, run.id, &workflowStep{id: uuid.New(), status: stepStatusFailed})
	server.publishForkChange(child, codersdk.ChatWatchForkFacetQuestions, nil)
	expect(child, codersdk.ChatWatchForkFacetQuestions, nil)
	select {
	case event := <-events:
		t.Fatalf("unexpected event %+v", event.ForkChange)
	default:
	}
}
