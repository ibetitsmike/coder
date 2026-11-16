package dbpurge_test

// This file is fork code: it checks the purge of fork tables.

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"cdr.dev/slog/v3/sloggers/slogtest"
	"github.com/coder/coder/v2/coderd/coderdtest/promhelp"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbpurge"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/quartz"
)

func TestDeleteOldChatSecretAnswers(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(t, dbtestutil.WithDumpOnFailure())
	logger := slogtest.Make(t, &slogtest.Options{IgnoreErrors: true})
	clk := quartz.NewMock(t)
	// The TTL runs on the database clock, not on the purge's clock.
	now := time.Now()

	org := dbgen.Organization(t, db, database.Organization{})
	owner := dbgen.User(t, db, database.User{})
	model := dbgen.ChatModelConfig(t, db, database.ChatModelConfig{OrganizationID: org.ID})
	chat := dbgen.Chat(t, db, database.Chat{OrganizationID: org.ID, OwnerID: owner.ID, LastModelConfigID: model.ID})
	fdb, err := forkdb.From(db)
	require.NoError(t, err)
	for callID, createdAt := range map[string]time.Time{
		"call_old":    now.Add(-25 * time.Hour),
		"call_recent": now.Add(-23 * time.Hour),
	} {
		require.NoError(t, fdb.InsertChatSecretAnswer(ctx, forkdb.InsertChatSecretAnswerParams{
			ChatID: chat.ID, ToolCallID: callID, Value: "value",
		}))
		_, err := sqlDB.ExecContext(ctx,
			`UPDATE chat_secret_answers SET created_at = $1 WHERE chat_id = $2 AND tool_call_id = $3`,
			createdAt, chat.ID, callID)
		require.NoError(t, err)
	}

	done := awaitDoTick(ctx, t, clk)
	reg := prometheus.NewRegistry()
	closer := dbpurge.New(ctx, logger, db, &codersdk.DeploymentValues{}, reg, dbpurge.WithClock(clk))
	defer closer.Close()
	<-done

	require.Eventually(t, func() bool {
		rows, err := sqlDB.QueryContext(ctx, `SELECT tool_call_id FROM chat_secret_answers WHERE chat_id = $1`, chat.ID)
		if !assert.NoError(t, err) {
			return false
		}
		defer rows.Close()
		var callIDs []string
		for rows.Next() {
			var callID string
			if !assert.NoError(t, rows.Scan(&callID)) {
				return false
			}
			callIDs = append(callIDs, callID)
		}
		if !assert.NoError(t, rows.Err()) {
			return false
		}
		return len(callIDs) == 1 && callIDs[0] == "call_recent"
	}, testutil.WaitShort, testutil.IntervalFast, "the purge deletes only the answer older than the TTL")
	require.Equal(t, 1, promhelp.CounterValue(t, reg, "coderd_dbpurge_records_purged_total", prometheus.Labels{
		"record_type": "chat_secret_answers",
	}))
}

func TestDeleteOldChatWakeupRunCounts(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(t, dbtestutil.WithDumpOnFailure())
	logger := slogtest.Make(t, &slogtest.Options{IgnoreErrors: true})
	clk := quartz.NewMock(t)
	// The purge age runs on the database clock, not on the purge's clock.
	now := time.Now().UTC().Truncate(time.Hour)

	org := dbgen.Organization(t, db, database.Organization{})
	owner := dbgen.User(t, db, database.User{})
	model := dbgen.ChatModelConfig(t, db, database.ChatModelConfig{OrganizationID: org.ID})
	chat := dbgen.Chat(t, db, database.Chat{OrganizationID: org.ID, OwnerID: owner.ID, LastModelConfigID: model.ID})
	oldHour := now.Add(-8 * 24 * time.Hour)
	recentHour := now.Add(-6 * 24 * time.Hour)
	for _, hour := range []time.Time{oldHour, recentHour} {
		_, err := sqlDB.ExecContext(ctx,
			`INSERT INTO chat_wakeup_run_counts (chat_id, hour, organization_id, runs) VALUES ($1, $2, $3, 3)`,
			chat.ID, hour, org.ID)
		require.NoError(t, err)
	}

	done := awaitDoTick(ctx, t, clk)
	closer := dbpurge.New(ctx, logger, db, &codersdk.DeploymentValues{}, prometheus.NewRegistry(), dbpurge.WithClock(clk))
	defer closer.Close()
	<-done

	var hours []time.Time
	rows, err := sqlDB.QueryContext(ctx, `SELECT hour FROM chat_wakeup_run_counts WHERE chat_id = $1`, chat.ID)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var hour time.Time
		require.NoError(t, rows.Scan(&hour))
		hours = append(hours, hour.UTC())
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []time.Time{recentHour}, hours, "the purge deletes only the counts older than 7 days")
}
