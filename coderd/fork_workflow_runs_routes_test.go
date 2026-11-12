package coderd_test

// This file is fork code: it covers the workflow run routes of the
// fork-workflows experiment. The run rows are seeded through forkdb directly;
// the stop of a live run is covered by the chatd engine tests.

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/sqlc-dev/pqtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/serpent"
)

// forkWorkflowSystemContext is the actor of the fixture writes: the journal
// rows are chatd-internal state, which the API subject cannot write.
func forkWorkflowSystemContext(t *testing.T) context.Context {
	return dbauthz.AsSystemRestricted(testutil.Context(t, testutil.WaitLong))
}

// requireForkStatus asserts that err is a codersdk error with code.
func requireForkStatus(t *testing.T, err error, code int) {
	t.Helper()
	var sdkErr *codersdk.Error
	require.ErrorAs(t, err, &sdkErr)
	require.Equal(t, code, sdkErr.StatusCode())
}

// newForkWorkflowRunsAPI starts coderd with fork-workflows optionally enabled
// and the chat worker stopped, and returns the raw fork store for fixtures.
func newForkWorkflowRunsAPI(t *testing.T, enabled bool) (*codersdk.Client, database.Store, codersdk.CreateFirstUserResponse) {
	t.Helper()

	db, ps := dbtestutil.NewDB(t)
	values := coderdtest.DeploymentValues(t)
	if enabled {
		values.Experiments = serpent.StringArray{string(codersdk.ExperimentForkWorkflows)}
	}
	client, _, _ := coderdtest.NewWithAPI(t, &coderdtest.Options{
		DeploymentValues:   values,
		Database:           db,
		Pubsub:             ps,
		ChatWorkerDisabled: true,
	})
	user := coderdtest.CreateFirstUser(t, client)
	return client, db, user
}

// insertForkWorkflowRun journals a completed run with one reported step, the
// way a finished run reads to the API.
func insertForkWorkflowRun(t *testing.T, db database.Store, chat database.Chat) forkdb.WorkflowRun {
	t.Helper()

	fdb, err := forkdb.From(db)
	require.NoError(t, err)
	runID := uuid.New()
	err = fdb.InsertWorkflowRun(forkWorkflowSystemContext(t), forkdb.InsertWorkflowRunParams{
		ID:            runID,
		ChatID:        chat.ID,
		ModelConfigID: chat.LastModelConfigID,
		Script:        "def main(args):\n    pass\n",
		Args:          []byte(`{}`),
		Status:        "running",
	})
	require.NoError(t, err)
	err = fdb.InsertWorkflowStep(forkWorkflowSystemContext(t), forkdb.InsertWorkflowStepParams{
		ID:         uuid.New(),
		RunID:      runID,
		SpecKey:    "s1",
		Occurrence: 0,
		Ordinal:    0,
		Title:      "step one",
		Status:     "reported",
		Value:      pqtype.NullRawMessage{RawMessage: json.RawMessage(`{"text":"done"}`), Valid: true},
	})
	require.NoError(t, err)
	snapshot := `{"run_id":` + strconv.Quote(runID.String()) + `,"status":"completed","steps":[],"logs":["all done"],"result":{"ok":true}}`
	err = fdb.UpdateWorkflowRunStatus(forkWorkflowSystemContext(t), forkdb.UpdateWorkflowRunStatusParams{
		ID:     runID,
		Status: "completed",
		Error:  "",
		Result: pqtype.NullRawMessage{RawMessage: json.RawMessage(snapshot), Valid: true},
	})
	require.NoError(t, err)
	row, err := fdb.GetWorkflowRun(forkWorkflowSystemContext(t), runID)
	require.NoError(t, err)
	return row
}

func TestForkWorkflowRunsRoutes(t *testing.T) {
	t.Parallel()

	t.Run("ExperimentOff", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitLong)
		client, db, user := newForkWorkflowRunsAPI(t, false)
		chat := newForkChatTodosChat(t, db, user)

		res, err := client.Request(ctx, http.MethodGet,
			"/api/v2/chats/fork/"+chat.ID.String()+"/workflow-runs", nil)
		require.NoError(t, err)
		defer res.Body.Close()
		require.Equal(t, http.StatusForbidden, res.StatusCode)
	})

	t.Run("ListAndDetail", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitLong)
		client, db, user := newForkWorkflowRunsAPI(t, true)
		chat := newForkChatTodosChat(t, db, user)
		run := insertForkWorkflowRun(t, db, chat)

		items, err := client.GetChatWorkflowRuns(ctx, chat.ID, codersdk.WorkflowRunListOptions{})
		require.NoError(t, err)
		require.Len(t, items, 1)
		assert.Equal(t, run.ID.String(), items[0].ID)
		assert.Equal(t, "completed", items[0].Status)
		assert.Equal(t, 1, items[0].StepCounts.Total)
		assert.Equal(t, 1, items[0].StepCounts.Done)
		assert.NotEmpty(t, items[0].ScriptPreview)
		assert.Empty(t, items[0].ScriptPreview[:0], "list items carry no full script")

		detail, err := client.GetChatWorkflowRun(ctx, chat.ID, uuid.MustParse(items[0].ID))
		require.NoError(t, err)
		require.Len(t, detail.Steps, 1)
		assert.Equal(t, "step one", detail.Steps[0].Title)
		assert.JSONEq(t, `{"text":"done"}`, string(detail.Steps[0].Value))
		require.NotEmpty(t, detail.Script)
		assert.Equal(t, "def main(args):\n    pass\n", detail.Script)
		assert.Equal(t, []string{"all done"}, detail.Logs)
		assert.JSONEq(t, `{"ok":true}`, string(detail.Result))
		require.NotNil(t, detail.FinishedAt)

		// A completed run answers 409 to a stop.
		err = client.StopChatWorkflowRun(ctx, chat.ID, run.ID)
		requireForkStatus(t, err, http.StatusConflict)

		// An unknown run is 404.
		err = client.StopChatWorkflowRun(ctx, chat.ID, uuid.New())
		requireForkStatus(t, err, http.StatusNotFound)
	})

	t.Run("MemberOwnChatOnly", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitLong)
		client, db, owner := newForkWorkflowRunsAPI(t, true)
		memberClient, member := coderdtest.CreateAnotherUser(t, client, owner.OrganizationID)
		otherClient, other := coderdtest.CreateAnotherUser(t, client, owner.OrganizationID)
		memberChat := dbgen.Chat(t, db, database.Chat{
			OrganizationID: owner.OrganizationID,
			OwnerID:        member.ID,
			LastModelConfigID: dbgen.ChatModelConfig(t, db, database.ChatModelConfig{
				OrganizationID: owner.OrganizationID,
			}).ID,
		})
		completed := insertForkWorkflowRun(t, db, memberChat)
		fdb, err := forkdb.From(db)
		require.NoError(t, err)
		liveID := uuid.New()
		require.NoError(t, fdb.InsertWorkflowRun(forkWorkflowSystemContext(t), forkdb.InsertWorkflowRunParams{
			ID:            liveID,
			ChatID:        memberChat.ID,
			ModelConfigID: memberChat.LastModelConfigID,
			Script:        "def main(args):\n    pass\n",
			Args:          []byte(`{}`),
			Status:        "running",
		}))

		// The member reads and stops the runs of their own chat.
		items, err := memberClient.GetChatWorkflowRuns(ctx, memberChat.ID, codersdk.WorkflowRunListOptions{})
		require.NoError(t, err)
		require.Len(t, items, 2)
		_, err = memberClient.GetChatWorkflowRun(ctx, memberChat.ID, completed.ID)
		require.NoError(t, err)

		// Another member sees neither the chat nor its runs, and cannot stop
		// them.
		_, err = otherClient.GetChatWorkflowRuns(ctx, memberChat.ID, codersdk.WorkflowRunListOptions{})
		requireForkStatus(t, err, http.StatusNotFound)
		_, err = otherClient.GetChatWorkflowRun(ctx, memberChat.ID, completed.ID)
		requireForkStatus(t, err, http.StatusNotFound)
		err = otherClient.StopChatWorkflowRun(ctx, memberChat.ID, liveID)
		requireForkStatus(t, err, http.StatusNotFound)
		assert.Equal(t, "running", forkWorkflowRunStatus(t, fdb, liveID), "a refused stop leaves the run running")

		err = memberClient.StopChatWorkflowRun(ctx, memberChat.ID, liveID)
		require.NoError(t, err)
		assert.Equal(t, "stopped", forkWorkflowRunStatus(t, fdb, liveID))

		// A run of another member's chat reads as missing through the
		// member's own chat.
		otherChat := dbgen.Chat(t, db, database.Chat{
			OrganizationID:    owner.OrganizationID,
			OwnerID:           other.ID,
			LastModelConfigID: memberChat.LastModelConfigID,
		})
		foreign := insertForkWorkflowRun(t, db, otherChat)
		_, err = memberClient.GetChatWorkflowRun(ctx, memberChat.ID, foreign.ID)
		requireForkStatus(t, err, http.StatusNotFound)

		// The member cannot reach the runs of the owner's chat either.
		ownerChat := newForkChatTodosChat(t, db, owner)
		_, err = memberClient.GetChatWorkflowRuns(ctx, ownerChat.ID, codersdk.WorkflowRunListOptions{})
		requireForkStatus(t, err, http.StatusNotFound)
	})
}

// forkWorkflowRunStatus reads the journaled status of a run.
func forkWorkflowRunStatus(t *testing.T, fdb forkdb.Store, runID uuid.UUID) string {
	t.Helper()
	row, err := fdb.GetWorkflowRun(forkWorkflowSystemContext(t), runID)
	require.NoError(t, err)
	return row.Status
}
