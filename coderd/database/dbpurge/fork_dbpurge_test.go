package dbpurge_test

// This file is fork code: it checks the purge of fork tables.

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
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

func TestDeleteExpiredPluginLinkCodes(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(t, dbtestutil.WithDumpOnFailure())
	logger := slogtest.Make(t, &slogtest.Options{IgnoreErrors: true})
	clk := quartz.NewMock(t)
	// Expiry runs on the database clock, not on the purge's clock.
	now := time.Now()

	org := dbgen.Organization(t, db, database.Organization{})
	user := dbgen.User(t, db, database.User{})
	file := dbgen.File(t, db, database.File{CreatedBy: user.ID})
	plugin := dbgen.Plugin(t, db, forkdb.Plugin{OrganizationID: org.ID, FileID: file.ID, CreatedBy: user.ID})
	fdb, err := forkdb.From(db)
	require.NoError(t, err)
	for code, expiresAt := range map[string]time.Time{
		"expired": now.Add(-time.Minute),
		"valid":   now.Add(time.Hour),
	} {
		require.NoError(t, fdb.InsertPluginLinkCode(ctx, forkdb.InsertPluginLinkCodeParams{
			CodeHash: []byte(code), PluginID: plugin.ID, UserID: user.ID, ExpiresAt: expiresAt,
		}))
	}

	done := awaitDoTick(ctx, t, clk)
	closer := dbpurge.New(ctx, logger, db, &codersdk.DeploymentValues{}, prometheus.NewRegistry(), dbpurge.WithClock(clk))
	defer closer.Close()
	<-done

	codes := queryForkStrings(ctx, t, sqlDB, `SELECT convert_from(code_hash, 'UTF8') FROM plugin_link_codes WHERE plugin_id = $1`, plugin.ID)
	require.Equal(t, []string{"valid"}, codes, "the purge deletes only the expired link code")
}

func TestDeleteOldWorkflowRuns(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(t, dbtestutil.WithDumpOnFailure())
	logger := slogtest.Make(t, &slogtest.Options{IgnoreErrors: true})
	clk := quartz.NewMock(t)
	// The purge age runs on the database clock, not on the purge's clock.
	now := time.Now()

	org := dbgen.Organization(t, db, database.Organization{})
	owner := dbgen.User(t, db, database.User{})
	model := dbgen.ChatModelConfig(t, db, database.ChatModelConfig{OrganizationID: org.ID})
	chat := dbgen.Chat(t, db, database.Chat{OrganizationID: org.ID, OwnerID: owner.ID, LastModelConfigID: model.ID})
	fdb, err := forkdb.From(db)
	require.NoError(t, err)
	runs := []struct {
		script    string
		status    string
		updatedAt time.Time
	}{
		{"old_completed", "completed", now.Add(-31 * 24 * time.Hour)},
		{"old_failed", "failed", now.Add(-31 * 24 * time.Hour)},
		{"old_stopped", "stopped", now.Add(-31 * 24 * time.Hour)},
		{"old_interrupted", "interrupted", now.Add(-31 * 24 * time.Hour)},
		// A live replica still writes a running run, however old.
		{"old_running", "running", now.Add(-31 * 24 * time.Hour)},
		{"recent_completed", "completed", now.Add(-29 * 24 * time.Hour)},
		// A step report to an interrupted run restarts its retention clock.
		{"reported_interrupted", "interrupted", now.Add(-31 * 24 * time.Hour)},
	}
	var oldRunID, reportedRunID uuid.UUID
	for _, run := range runs {
		id := uuid.New()
		switch run.script {
		case "old_completed":
			oldRunID = id
		case "reported_interrupted":
			reportedRunID = id
		}
		require.NoError(t, fdb.InsertWorkflowRun(ctx, forkdb.InsertWorkflowRunParams{
			ID: id, ChatID: chat.ID, ModelConfigID: model.ID, Script: run.script, Args: json.RawMessage(`{}`), Status: run.status,
		}))
		_, err := sqlDB.ExecContext(ctx, `UPDATE workflow_runs SET updated_at = $1 WHERE id = $2`, run.updatedAt, id)
		require.NoError(t, err)
	}
	require.NoError(t, fdb.InsertWorkflowStep(ctx, forkdb.InsertWorkflowStepParams{
		ID: uuid.New(), RunID: oldRunID, SpecKey: "step", Status: "reported",
	}))
	reportedStepID := uuid.New()
	require.NoError(t, fdb.InsertWorkflowStep(ctx, forkdb.InsertWorkflowStepParams{
		ID: reportedStepID, RunID: reportedRunID, SpecKey: "step", Status: "running",
	}))
	require.NoError(t, fdb.UpdateWorkflowStep(ctx, forkdb.UpdateWorkflowStepParams{
		ID: reportedStepID, Status: "reported",
	}))

	done := awaitDoTick(ctx, t, clk)
	closer := dbpurge.New(ctx, logger, db, &codersdk.DeploymentValues{}, prometheus.NewRegistry(), dbpurge.WithClock(clk))
	defer closer.Close()
	<-done

	scripts := queryForkStrings(ctx, t, sqlDB, `SELECT script FROM workflow_runs WHERE chat_id = $1 ORDER BY script`, chat.ID)
	require.Equal(t, []string{"old_running", "recent_completed", "reported_interrupted"}, scripts,
		"the purge deletes the ended and interrupted runs older than 30 days")
	steps := queryForkStrings(ctx, t, sqlDB, `SELECT spec_key FROM workflow_steps WHERE run_id = $1`, oldRunID)
	require.Empty(t, steps, "the steps of a purged run cascade")
}

func TestDeleteOldChatWakeups(t *testing.T) {
	t.Parallel()

	ctx := testutil.Context(t, testutil.WaitLong)
	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(t, dbtestutil.WithDumpOnFailure())
	logger := slogtest.Make(t, &slogtest.Options{IgnoreErrors: true})
	clk := quartz.NewMock(t)
	// The purge age runs on the database clock, not on the purge's clock.
	now := time.Now()

	org := dbgen.Organization(t, db, database.Organization{})
	owner := dbgen.User(t, db, database.User{})
	model := dbgen.ChatModelConfig(t, db, database.ChatModelConfig{OrganizationID: org.ID})
	chat := dbgen.Chat(t, db, database.Chat{OrganizationID: org.ID, OwnerID: owner.ID, LastModelConfigID: model.ID})
	fdb, err := forkdb.From(db)
	require.NoError(t, err)
	old := now.Add(-31 * 24 * time.Hour)
	for _, wakeup := range []struct {
		prompt    string
		status    forkdb.ChatWakeupStatus
		updatedAt time.Time
	}{
		{"old_completed", forkdb.ChatWakeupStatusCompleted, old},
		{"old_canceled", forkdb.ChatWakeupStatusCanceled, old},
		{"old_active", forkdb.ChatWakeupStatusActive, old},
		{"old_paused", forkdb.ChatWakeupStatusPaused, old},
		{"recent_completed", forkdb.ChatWakeupStatusCompleted, now.Add(-29 * 24 * time.Hour)},
	} {
		params := forkdb.InsertChatWakeupParams{
			ID: uuid.New(), ChatID: chat.ID, CreatedBy: owner.ID, Source: "schedule_wakeup",
			Kind: forkdb.ChatWakeupKindDelay, Prompt: wakeup.prompt, Timezone: "UTC", Status: wakeup.status,
			CreatedAt: wakeup.updatedAt, UpdatedAt: wakeup.updatedAt,
		}
		if wakeup.status == forkdb.ChatWakeupStatusActive {
			params.NextRunAt = sql.NullTime{Time: now.Add(time.Hour), Valid: true}
		}
		_, err := fdb.InsertChatWakeup(ctx, params)
		require.NoError(t, err)
	}

	done := awaitDoTick(ctx, t, clk)
	closer := dbpurge.New(ctx, logger, db, &codersdk.DeploymentValues{}, prometheus.NewRegistry(), dbpurge.WithClock(clk))
	defer closer.Close()
	<-done

	prompts := queryForkStrings(ctx, t, sqlDB, `SELECT prompt FROM chat_wakeups WHERE chat_id = $1 ORDER BY prompt`, chat.ID)
	require.Equal(t, []string{"old_active", "old_paused", "recent_completed"}, prompts,
		"the purge deletes the completed and canceled wake-ups older than 30 days")
}

// queryForkStrings returns the single text column of the rows of query.
func queryForkStrings(ctx context.Context, t *testing.T, sqlDB *sql.DB, query string, args ...any) []string {
	t.Helper()

	rows, err := sqlDB.QueryContext(ctx, query, args...)
	require.NoError(t, err)
	defer rows.Close()
	var values []string
	for rows.Next() {
		var value string
		require.NoError(t, rows.Scan(&value))
		values = append(values, value)
	}
	require.NoError(t, rows.Err())
	return values
}
