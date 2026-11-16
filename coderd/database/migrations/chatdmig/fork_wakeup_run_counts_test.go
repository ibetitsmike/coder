package chatdmig_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/migrations"
	"github.com/coder/coder/v2/coderd/database/migrations/chatdmig"
)

// TestWakeupRunCountsBackfill runs the node that adds the hourly wake-up run
// counts on a database with runs. The delivered runs of the last 25 hours
// count in the UTC hour they were created in; other runs do not. A run that a
// replica without the counts inserts after the node, with a plain insert into
// chat_wakeup_runs, counts through the trigger.
func TestWakeupRunCountsBackfill(t *testing.T) {
	t.Parallel()
	admin := adminURL(t)
	g, err := migrations.LoadGraph()
	require.NoError(t, err)
	var node *chatdmig.Node
	for _, n := range g.Nodes {
		if strings.HasSuffix(n.Name, "_chat_wakeup_run_counts") {
			node = n
		}
	}
	require.NotNil(t, node)

	_, sqlDB := createDB(t, admin)
	require.NoError(t, migrations.UpGraph(sqlDB, withoutNode(g, node)))
	db := database.New(sqlDB)

	model := dbgen.ChatModelConfig(t, db, database.ChatModelConfig{})
	chat := dbgen.Chat(t, db, database.Chat{
		OrganizationID:    dbgen.Organization(t, db, database.Organization{}).ID,
		OwnerID:           dbgen.User(t, db, database.User{}).ID,
		LastModelConfigID: model.ID,
	})
	wakeupID := uuid.New()
	_, err = sqlDB.Exec(`INSERT INTO chat_wakeups (id, chat_id, organization_id, created_by, source, kind, prompt, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'schedule_wakeup', 'delay', 'wake', 'completed', now(), now())`,
		wakeupID, chat.ID, chat.OrganizationID, chat.OwnerID)
	require.NoError(t, err)
	// Run times relative to the database clock, which the node reads.
	var dbNow time.Time
	require.NoError(t, sqlDB.QueryRow(`SELECT now()`).Scan(&dbNow))
	hour := dbNow.UTC().Truncate(time.Hour).Add(-2 * time.Hour)
	insertRun := func(status string, at time.Time) {
		t.Helper()
		_, err := sqlDB.Exec(`INSERT INTO chat_wakeup_runs (id, wakeup_id, chat_id, organization_id, run_key, status, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $7)`, uuid.New(), wakeupID, chat.ID, chat.OrganizationID, uuid.NewString(), status, at)
		require.NoError(t, err)
	}
	insertRun("delivered", hour.Add(time.Minute))
	insertRun("delivered", hour.Add(59*time.Minute))
	insertRun("delivered", hour.Add(time.Hour))
	insertRun("failed", hour.Add(2*time.Minute))
	insertRun("skipped", hour.Add(3*time.Minute))
	insertRun("delivered", dbNow.Add(-26*time.Hour))

	require.NoError(t, migrations.UpAll(sqlDB))
	require.NoError(t, migrations.EnsureCleanAll(sqlDB))
	insertRun("delivered", hour.Add(time.Hour+time.Minute))
	insertRun("failed", hour.Add(time.Hour+2*time.Minute))

	rows, err := sqlDB.Query(`SELECT hour, organization_id, runs FROM chat_wakeup_run_counts WHERE chat_id = $1 ORDER BY hour`, chat.ID)
	require.NoError(t, err)
	defer rows.Close()
	type count struct {
		Hour           time.Time
		OrganizationID uuid.UUID
		Runs           int
	}
	var got []count
	for rows.Next() {
		var c count
		require.NoError(t, rows.Scan(&c.Hour, &c.OrganizationID, &c.Runs))
		c.Hour = c.Hour.UTC()
		got = append(got, c)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []count{
		{Hour: hour, OrganizationID: chat.OrganizationID, Runs: 2},
		{Hour: hour.Add(time.Hour), OrganizationID: chat.OrganizationID, Runs: 2},
	}, got)
}
