package forkdb_test

import (
	"bytes"
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

func TestGetSubagentCapacityCounts(t *testing.T) {
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
	otherOrgChild := chat(orgB, &otherOrgRoot, 0)

	// One statement counts candidates of different families and
	// organizations. A root is not a subagent, so it gets no row.
	rows, err := fdb.GetSubagentCapacityCounts(ctx, forkdb.GetSubagentCapacityCountsParams{
		CandidateIds: []uuid.UUID{candidate.ID, sibling.ID, otherOrgChild.ID, root.ID},
		StaleSeconds: 60,
	})
	require.NoError(t, err)
	got := map[uuid.UUID]forkdb.GetSubagentCapacityCountsRow{}
	for _, row := range rows {
		got[row.ChatID] = row
	}
	require.Equal(t, map[uuid.UUID]forkdb.GetSubagentCapacityCountsRow{
		// Roots, the stale child, the candidate and its parent do not count.
		candidate.ID: {ChatID: candidate.ID, ActiveCount: 3, ActiveOrganizationCount: 2, ActiveFamilyCount: 1},
		// Only the lineage is excluded: a sibling counts the parent and its
		// child.
		sibling.ID: {ChatID: sibling.ID, ActiveCount: 4, ActiveOrganizationCount: 3, ActiveFamilyCount: 2},
		// The organization and family counts follow each candidate.
		otherOrgChild.ID: {ChatID: otherOrgChild.ID, ActiveCount: 4, ActiveOrganizationCount: 0, ActiveFamilyCount: 0},
	}, got)
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

func TestGetSubagentCapacityCountsAwaitingOnly(t *testing.T) {
	t.Parallel()

	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(t)
	fdb := mustFrom(t, db)
	ctx := testutil.Context(t, testutil.WaitShort)

	orgA := dbgen.Organization(t, db, database.Organization{}).ID
	orgB := dbgen.Organization(t, db, database.Organization{}).ID
	ownerID := dbgen.User(t, db, database.User{}).ID
	modelID := dbgen.ChatModelConfig(t, db, database.ChatModelConfig{}).ID
	// chat creates a chat under parent. A nil age leaves it unowned; otherwise
	// a runner whose heartbeat is age old owns it.
	chat := func(orgID uuid.UUID, parent *database.Chat, status database.ChatStatus, age *time.Duration) database.Chat {
		seed := database.Chat{OrganizationID: orgID, OwnerID: ownerID, LastModelConfigID: modelID, Status: status}
		if parent != nil {
			seed.ParentChatID = uuid.NullUUID{UUID: parent.ID, Valid: true}
			seed.RootChatID = parent.RootChatID
			if !parent.RootChatID.Valid {
				seed.RootChatID = seed.ParentChatID
			}
		}
		c := dbgen.Chat(t, db, seed)
		if age == nil {
			return c
		}
		runnerID := uuid.New()
		_, err := sqlDB.ExecContext(ctx, "UPDATE chats SET worker_id = $2, runner_id = $3 WHERE id = $1", c.ID, uuid.New(), runnerID)
		require.NoError(t, err)
		_, err = sqlDB.ExecContext(ctx,
			"INSERT INTO chat_heartbeats (chat_id, runner_id, heartbeat_at) VALUES ($1, $2, NOW() - ($3 * INTERVAL '1 second'))",
			c.ID, runnerID, int(age.Seconds()))
		require.NoError(t, err)
		return c
	}
	fresh, stale := time.Duration(0), time.Hour
	running := database.ChatStatusRunning
	root := chat(orgA, nil, running, &fresh)
	coordinator := chat(orgA, &root, running, &fresh)
	worker := chat(orgA, &coordinator, running, nil)
	sibling := chat(orgA, &root, running, nil)
	abandoned := chat(orgA, &root, running, &stale)
	waiting := chat(orgA, &root, database.ChatStatusWaiting, nil)
	archived := chat(orgA, &root, running, nil)
	_, err := sqlDB.ExecContext(ctx, "UPDATE chats SET archived = true WHERE id = $1", archived.ID)
	require.NoError(t, err)
	otherRoot := chat(orgA, nil, running, nil)
	_ = chat(orgA, &otherRoot, running, &fresh)
	otherWaiter := chat(orgA, &otherRoot, running, nil)
	otherOrgRoot := chat(orgB, nil, running, nil)
	_ = chat(orgB, &otherOrgRoot, running, &fresh)

	counts := func(awaitingOnly bool, candidates ...database.Chat) map[uuid.UUID]forkdb.GetSubagentCapacityCountsRow {
		t.Helper()
		ids := make([]uuid.UUID, 0, len(candidates))
		for _, c := range candidates {
			ids = append(ids, c.ID)
		}
		rows, err := fdb.GetSubagentCapacityCounts(ctx, forkdb.GetSubagentCapacityCountsParams{
			CandidateIds: ids,
			AwaitingOnly: awaitingOnly,
			StaleSeconds: 60,
		})
		require.NoError(t, err)
		got := map[uuid.UUID]forkdb.GetSubagentCapacityCountsRow{}
		for _, row := range rows {
			got[row.ChatID] = row
		}
		return got
	}
	// Only the unarchived running chats that no fresh worker owns get a row:
	// unowned and stale ones. Each row matches the count without the filter,
	// which excludes the chat's active ancestors.
	got := counts(true, root, coordinator, worker, sibling, abandoned, waiting, archived, otherWaiter)
	want := counts(false, worker, sibling, abandoned, otherWaiter)
	require.Len(t, want, 4)
	require.Equal(t, want, got)
	require.Equal(t, forkdb.GetSubagentCapacityCountsRow{
		ChatID:                  worker.ID,
		ActiveCount:             2,
		ActiveOrganizationCount: 1,
		ActiveFamilyCount:       0,
	}, got[worker.ID])
	require.Equal(t, forkdb.GetSubagentCapacityCountsRow{
		ChatID:                  otherWaiter.ID,
		ActiveCount:             3,
		ActiveOrganizationCount: 2,
		ActiveFamilyCount:       1,
	}, got[otherWaiter.ID])

	// With root_chat_id, the query selects the family's candidates itself:
	// the same rows as listing every chat of the family, and no row of
	// another family.
	rows, err := fdb.GetSubagentCapacityCounts(ctx, forkdb.GetSubagentCapacityCountsParams{
		RootChatID:   uuid.NullUUID{UUID: root.ID, Valid: true},
		AwaitingOnly: true,
		StaleSeconds: 60,
	})
	require.NoError(t, err)
	wantFamily := counts(true, root, coordinator, worker, sibling, abandoned, waiting, archived)
	require.Len(t, rows, 3)
	require.Len(t, wantFamily, 3)
	for i, row := range rows {
		require.Equal(t, wantFamily[row.ChatID], row)
		if i > 0 {
			require.Negative(t, bytes.Compare(rows[i-1].ChatID[:], row.ChatID[:]), "rows are in ID order")
		}
	}
}
