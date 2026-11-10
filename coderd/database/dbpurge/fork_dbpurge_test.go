package dbpurge_test

// This file is fork code: it checks the purge of fork tables.

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"cdr.dev/slog/v3/sloggers/slogtest"
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
	now := clk.Now().UTC()

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
	closer := dbpurge.New(ctx, logger, db, &codersdk.DeploymentValues{}, prometheus.NewRegistry(), dbpurge.WithClock(clk))
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
}
