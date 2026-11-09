package forkdb_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/testutil"
)

func TestCountActiveSubagentsForCapacity(t *testing.T) {
	t.Parallel()

	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitShort)

	orgA := dbgen.Organization(t, db, database.Organization{}).ID
	orgB := dbgen.Organization(t, db, database.Organization{}).ID
	ownerID := dbgen.User(t, db, database.User{}).ID
	modelID := dbgen.ChatModelConfig(t, db, database.ChatModelConfig{}).ID
	// chat creates a chat owned by a runner whose heartbeat is age old.
	chat := func(orgID uuid.UUID, parent *database.Chat, age time.Duration) database.Chat {
		seed := database.Chat{OrganizationID: orgID, OwnerID: ownerID, LastModelConfigID: modelID, Status: database.ChatStatusRunning}
		if parent != nil {
			seed.ParentChatID = uuid.NullUUID{UUID: parent.ID, Valid: true}
			seed.RootChatID = parent.RootChatID
			if !parent.RootChatID.Valid {
				seed.RootChatID = seed.ParentChatID
			}
		}
		c := dbgen.Chat(t, db, seed)
		runnerID := uuid.New()
		_, err := sqlDB.ExecContext(ctx, "UPDATE chats SET worker_id = $2, runner_id = $3 WHERE id = $1", c.ID, uuid.New(), runnerID)
		require.NoError(t, err)
		_, err = sqlDB.ExecContext(ctx,
			"INSERT INTO chat_heartbeats (chat_id, runner_id, heartbeat_at) VALUES ($1, $2, NOW() - ($3 * INTERVAL '1 second'))",
			c.ID, runnerID, int(age.Seconds()))
		require.NoError(t, err)
		return c
	}
	root := chat(orgA, nil, 0)
	parent := chat(orgA, &root, 0)
	candidate := chat(orgA, &parent, 0)
	sibling := chat(orgA, &root, 0)
	_ = chat(orgA, &root, time.Hour)
	otherRoot := chat(orgA, nil, 0)
	_ = chat(orgA, &otherRoot, 0)
	otherOrgRoot := chat(orgB, nil, 0)
	_ = chat(orgB, &otherOrgRoot, 0)

	count := func(c database.Chat) forkdb.CountActiveSubagentsForCapacityRow {
		t.Helper()
		row, err := fdb.CountActiveSubagentsForCapacity(ctx, forkdb.CountActiveSubagentsForCapacityParams{
			ChatID:         c.ID,
			OrganizationID: orgA,
			RootChatID:     root.ID,
			StaleSeconds:   60,
		})
		require.NoError(t, err)
		return row
	}
	// Roots, the stale child, the candidate and its parent do not count.
	require.Equal(t, forkdb.CountActiveSubagentsForCapacityRow{
		ActiveCount:             3,
		ActiveOrganizationCount: 2,
		ActiveFamilyCount:       1,
	}, count(candidate))
	// Only the lineage is excluded: a sibling counts the parent and its
	// child.
	require.Equal(t, forkdb.CountActiveSubagentsForCapacityRow{
		ActiveCount:             4,
		ActiveOrganizationCount: 3,
		ActiveFamilyCount:       2,
	}, count(sibling))
}

func TestCountLiveSubagentsInFamily(t *testing.T) {
	t.Parallel()

	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitShort)

	orgID := dbgen.Organization(t, db, database.Organization{}).ID
	ownerID := dbgen.User(t, db, database.User{}).ID
	modelID := dbgen.ChatModelConfig(t, db, database.ChatModelConfig{}).ID
	chat := func(root *database.Chat, status database.ChatStatus, archived bool) database.Chat {
		seed := database.Chat{OrganizationID: orgID, OwnerID: ownerID, LastModelConfigID: modelID, Status: status}
		if root != nil {
			seed.ParentChatID = uuid.NullUUID{UUID: root.ID, Valid: true}
			seed.RootChatID = seed.ParentChatID
		}
		c := dbgen.Chat(t, db, seed)
		_, err := sqlDB.ExecContext(ctx, "UPDATE chats SET archived = $2 WHERE id = $1", c.ID, archived)
		require.NoError(t, err)
		return c
	}
	root := chat(nil, database.ChatStatusRunning, false)
	for _, status := range []database.ChatStatus{
		database.ChatStatusRunning,
		database.ChatStatusInterrupting,
		database.ChatStatusRequiresAction,
		database.ChatStatusPaused,
	} {
		_ = chat(&root, status, false)
	}
	_ = chat(&root, database.ChatStatusWaiting, false)
	_ = chat(&root, database.ChatStatusError, false)
	_ = chat(&root, database.ChatStatusRunning, true)
	otherRoot := chat(nil, database.ChatStatusRunning, false)
	_ = chat(&otherRoot, database.ChatStatusRunning, false)

	live, err := fdb.CountLiveSubagentsInFamily(ctx, root.ID)
	require.NoError(t, err)
	require.EqualValues(t, 4, live)
}
