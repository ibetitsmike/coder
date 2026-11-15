package chatdmig_test

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/migrations"
	"github.com/coder/coder/v2/coderd/database/migrations/chatdmig"
)

// TestWorkflowRunDetailBackfill runs the node that adds the live logs column
// and the per-step reused flag of the workflow journal. It keeps every row
// and marks reused only the steps that the old result snapshot reported as
// replayed.
func TestWorkflowRunDetailBackfill(t *testing.T) {
	t.Parallel()
	admin := adminURL(t)
	g, err := migrations.LoadGraph()
	require.NoError(t, err)
	var node *chatdmig.Node
	for _, n := range g.Nodes {
		if strings.HasSuffix(n.Name, "_workflow_run_detail") {
			node = n
		}
	}
	require.NotNil(t, node)

	_, sqlDB := createDB(t, admin)
	require.NoError(t, migrations.UpGraph(sqlDB, withoutNode(g, node)))
	db := database.New(sqlDB)

	model := dbgen.ChatModelConfig(t, db, database.ChatModelConfig{})
	seed := database.Chat{
		OrganizationID:    dbgen.Organization(t, db, database.Organization{}).ID,
		OwnerID:           dbgen.User(t, db, database.User{}).ID,
		LastModelConfigID: model.ID,
	}
	chat := dbgen.Chat(t, db, seed)

	// A terminal run whose snapshot marks one of its two steps reused.
	terminalID := uuid.New()
	_, err = sqlDB.Exec(`INSERT INTO workflow_runs (id, chat_id, model_config_id, script, args, status, result)
		VALUES ($1, $2, $3, 'def main(args): pass', '{"files":["a.go"]}', 'completed', $4)`,
		terminalID, chat.ID, model.ID,
		runSnapshot(terminalID, map[uuid.UUID]bool{stepID(terminalID, 0): true, stepID(terminalID, 1): false}))
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		_, err = sqlDB.Exec(`INSERT INTO workflow_steps (id, run_id, spec_key, occurrence, ordinal, title, status)
			VALUES ($1, $2, $3, 0, $4, $5, 'reported')`,
			stepID(terminalID, i), terminalID, "spec"+strconv.Itoa(i), i, "step "+strconv.Itoa(i))
		require.NoError(t, err)
	}

	// An interrupted run with an open step: the snapshot is NULL, so the
	// step keeps the default and the row keeps its status.
	liveID := uuid.New()
	_, err = sqlDB.Exec(`INSERT INTO workflow_runs (id, chat_id, model_config_id, script, status)
		VALUES ($1, $2, $3, 'def main(args): pass', 'interrupted')`, liveID, chat.ID, model.ID)
	require.NoError(t, err)
	_, err = sqlDB.Exec(`INSERT INTO workflow_steps (id, run_id, spec_key, occurrence, ordinal, title, status)
		VALUES ($1, $2, 'only', 0, 0, 'step only', 'running')`, stepID(liveID, 0), liveID)
	require.NoError(t, err)

	require.NoError(t, migrations.UpAll(sqlDB))
	require.NoError(t, migrations.EnsureCleanAll(sqlDB))

	var status string
	var logs json.RawMessage
	require.NoError(t, sqlDB.QueryRow(`SELECT status, logs FROM workflow_runs WHERE id = $1`, terminalID).
		Scan(&status, &logs))
	require.Equal(t, "completed", status, "the node keeps the run and its status")
	require.JSONEq(t, `[]`, string(logs), "a terminal run keeps its logs in the snapshot")

	var reused bool
	require.NoError(t, sqlDB.QueryRow(`SELECT reused FROM workflow_steps WHERE id = $1`, stepID(terminalID, 0)).
		Scan(&reused))
	require.True(t, reused, "the snapshot marks the step reused")

	require.NoError(t, sqlDB.QueryRow(`SELECT reused FROM workflow_steps WHERE id = $1`, stepID(terminalID, 1)).
		Scan(&reused))
	require.False(t, reused, "the snapshot marks the step not reused")

	require.NoError(t, sqlDB.QueryRow(`SELECT status, logs FROM workflow_runs WHERE id = $1`, liveID).
		Scan(&status, &logs))
	require.Equal(t, "interrupted", status, "the node keeps an interrupted run open")
	require.JSONEq(t, `[]`, string(logs))

	require.NoError(t, sqlDB.QueryRow(`SELECT reused FROM workflow_steps WHERE id = $1`, stepID(liveID, 0)).
		Scan(&reused))
	require.False(t, reused, "a step without a snapshot stays not reused")
}

// runSnapshot builds the result snapshot of a terminal run, the way the old
// journal wrote it, with the reuse flag of each step.
func runSnapshot(runID uuid.UUID, reused map[uuid.UUID]bool) string {
	steps := `{"run_id":` + strconv.Quote(runID.String()) + `,"status":"completed","steps":[`
	first := true
	for id, isReused := range reused {
		if !first {
			steps += ","
		}
		first = false
		steps += `{"chat_id":` + strconv.Quote(id.String()) + `,"title":"step","status":"reported"`
		if isReused {
			steps += `,"reused":true`
		}
		steps += `}`
	}
	return steps + `],"logs":["all done"],"result":{}}`
}

// stepID builds the deterministic step ID the engine derives from the run ID
// and the step's occurrence. The backfill only matches on the ID, so any
// distinct ID per run and occurrence works.
func stepID(runID uuid.UUID, occurrence int) uuid.UUID {
	// uuid.NewSHA1 derives a stable ID from the run ID and the occurrence.
	return uuid.NewSHA1(runID, []byte("step-"+strconv.Itoa(occurrence)))
}
