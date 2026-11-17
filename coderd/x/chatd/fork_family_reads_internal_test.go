package chatd

// This file is fork code: it tests that the interrupt cascade, the archive
// settle, spawn_agent and list_agents read a family with a fixed number of
// statements.

import (
	"context"
	"database/sql"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"cdr.dev/slog/v3/sloggers/slogtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbmetrics"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/codersdk"
)

// countedFamily is a nested subagents fixture whose server runs its queries
// through query metrics, so a test can count the statements outside
// transactions.
type countedFamily struct {
	nestedSubagentsFixture
	raw database.Store
	reg *prometheus.Registry
}

func newCountedFamily(t *testing.T) countedFamily {
	t.Helper()
	raw, ps, sqlDB := dbtestutil.NewDBWithSQLDB(t)
	reg := prometheus.NewRegistry()
	db := dbmetrics.NewQueryMetrics(raw, slogtest.Make(t, nil), reg)
	f := newNestedSubagentsFixtureOn(t, db, ps, sqlDB, codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}, Limits{})
	f.setStatus(t, f.root, database.ChatStatusWaiting, uuid.NullUUID{})
	return countedFamily{nestedSubagentsFixture: f, raw: raw, reg: reg}
}

// descendant inserts a child of parent in status.
func (f countedFamily) descendant(t *testing.T, parent database.Chat, status database.ChatStatus) database.Chat {
	t.Helper()
	return dbgen.Chat(t, f.raw, database.Chat{
		OrganizationID:    f.root.OrganizationID,
		OwnerID:           f.root.OwnerID,
		LastModelConfigID: f.root.LastModelConfigID,
		ParentChatID:      uuid.NullUUID{UUID: parent.ID, Valid: true},
		RootChatID:        uuid.NullUUID{UUID: f.root.ID, Valid: true},
		Status:            status,
	})
}

// threeLevels gives the root 4 idle children with 4 idle children each, 20
// descendants in all, and returns them parents first.
func (f countedFamily) threeLevels(t *testing.T) []database.Chat {
	t.Helper()
	var descendants []database.Chat
	for range 4 {
		child := f.descendant(t, f.root, database.ChatStatusWaiting)
		descendants = append(descendants, child)
		for range 4 {
			descendants = append(descendants, f.descendant(t, child, database.ChatStatusWaiting))
		}
	}
	return descendants
}

// counted returns the statements that fn runs outside transactions, by
// query name.
func (f countedFamily) counted(t *testing.T, fn func()) map[string]int {
	t.Helper()
	before := queryCounts(t, f.reg)
	fn()
	after := queryCounts(t, f.reg)
	delta := map[string]int{}
	for query, n := range after {
		if d := n - before[query]; d > 0 {
			delta[query] = d
		}
	}
	return delta
}

// The cascade reads the subtree and its statuses in one statement, so idle
// descendants cost no read and an active one costs only its interrupt.
func TestForkInterruptCascadeStatements(t *testing.T) {
	t.Parallel()

	t.Run("Leaf", func(t *testing.T) {
		t.Parallel()
		f := newCountedFamily(t)
		ctx := chatdTestContext(t)
		leaf := f.descendant(t, f.root, database.ChatStatusWaiting)

		counts := f.counted(t, func() {
			_, _, err := f.server.InterruptChat(ctx, leaf)
			require.NoError(t, err)
		})
		require.Equal(t, 1, counts["GetChatSubtreeIDs"], "counts: %v", counts)
		require.Zero(t, counts["GetChatByID"], "counts: %v", counts)
	})

	t.Run("IdleFamily", func(t *testing.T) {
		t.Parallel()
		f := newCountedFamily(t)
		ctx := chatdTestContext(t)
		f.threeLevels(t)

		counts := f.counted(t, func() {
			_, interrupted, err := f.server.InterruptChat(ctx, f.root)
			require.NoError(t, err)
			require.False(t, interrupted)
		})
		require.Equal(t, map[string]int{"GetChatSubtreeIDs": 1}, counts)
	})

	t.Run("ActiveDescendants", func(t *testing.T) {
		t.Parallel()
		f := newCountedFamily(t)
		ctx := chatdTestContext(t)
		descendants := f.threeLevels(t)
		owned := uuid.NullUUID{UUID: uuid.New(), Valid: true}
		// A child and a grandchild of another child run on a worker.
		active := []database.Chat{descendants[0], descendants[7]}
		for _, chat := range active {
			f.setStatus(t, chat, database.ChatStatusRunning, owned)
		}

		counts := f.counted(t, func() {
			_, interrupted, err := f.server.InterruptChat(ctx, f.root)
			require.NoError(t, err)
			require.True(t, interrupted)
		})
		require.Equal(t, 1, counts["GetChatSubtreeIDs"], "counts: %v", counts)
		require.Zero(t, counts["GetChatByID"], "counts: %v", counts)
		f.requireStatus(t, database.ChatStatusInterrupting, active...)
		f.requireStatus(t, database.ChatStatusWaiting, descendants[1], descendants[6])
	})
}

// list_agents reads the descendants of any chat of a family in one
// statement, and an archived chat hides its subtree.
func TestForkListDescendantsStatements(t *testing.T) {
	t.Parallel()
	f := newCountedFamily(t)
	ctx := chatdTestContext(t)
	descendants := f.threeLevels(t)
	ids := func(chats []database.Chat) []uuid.UUID {
		got := make([]uuid.UUID, 0, len(chats))
		for _, chat := range chats {
			got = append(got, chat.ID)
		}
		return got
	}
	all := ids(descendants)

	var listed []database.Chat
	counts := f.counted(t, func() {
		var err error
		listed, err = f.server.listDescendantChats(ctx, f.root.ID, maxListedSubtreeLevels)
		require.NoError(t, err)
	})
	require.Equal(t, map[string]int{"GetDescendantChatsByRootIDs": 1}, counts)
	require.ElementsMatch(t, all, ids(listed))

	counts = f.counted(t, func() {
		var err error
		listed, err = f.server.listDescendantChats(ctx, descendants[0].ID, maxListedSubtreeLevels)
		require.NoError(t, err)
	})
	require.Equal(t, map[string]int{"GetDescendantChatsByRootIDs": 1}, counts)
	require.ElementsMatch(t, all[1:5], ids(listed), "only the subtree of the chat")

	listed, err := f.server.listDescendantChats(ctx, f.root.ID, 1)
	require.NoError(t, err)
	require.ElementsMatch(t, []uuid.UUID{all[0], all[5], all[10], all[15]}, ids(listed), "one level")

	_, err = f.raw.ArchiveChatByID(ctx, descendants[0].ID)
	require.NoError(t, err)
	listed, err = f.server.listDescendantChats(ctx, f.root.ID, maxListedSubtreeLevels)
	require.NoError(t, err)
	require.ElementsMatch(t, all[5:], ids(listed), "an archived child hides its subtree")
}

// One settle pass over a root with 20 descendants reads the subtree and every
// status in two statements and interrupts no member that only waits for
// its worker.
func TestForkArchiveSettlePassStatements(t *testing.T) {
	t.Parallel()
	f := newCountedFamily(t)
	ctx := chatdTestContext(t)
	descendants := f.threeLevels(t)
	owned := uuid.NullUUID{UUID: uuid.New(), Valid: true}
	busy := descendants[3]
	f.setStatus(t, busy, database.ChatStatusInterrupting, owned)

	cascadeCtx := context.WithValue(ctx, interruptCascadeKey{}, true)
	var active bool
	counts := f.counted(t, func() {
		var err error
		active, err = f.server.settleArchivePass(cascadeCtx, f.root.ID)
		require.NoError(t, err)
	})
	require.True(t, active)
	require.Equal(t, map[string]int{"GetChatSubtreeIDs": 1, "GetChatsByIDs": 1}, counts)
	f.requireStatus(t, database.ChatStatusInterrupting, busy)

	// Once the worker finishes, the next pass reports the family settled.
	f.setStatus(t, busy, database.ChatStatusWaiting, uuid.NullUUID{})
	counts = f.counted(t, func() {
		var err error
		active, err = f.server.settleArchivePass(cascadeCtx, f.root.ID)
		require.NoError(t, err)
	})
	require.False(t, active)
	require.Equal(t, map[string]int{"GetChatSubtreeIDs": 1, "GetChatsByIDs": 1}, counts)
}

// A settle pass interrupts a child spawned after the settle began, and
// finishes an interrupting member that no worker owns.
func TestForkArchiveSettlePassCoversLateAndUnownedMembers(t *testing.T) {
	t.Parallel()
	f := newCountedFamily(t)
	ctx := chatdTestContext(t)
	child := f.descendant(t, f.root, database.ChatStatusWaiting)
	late := f.descendant(t, child, database.ChatStatusWaiting)
	f.setStatus(t, late, database.ChatStatusRunning, uuid.NullUUID{UUID: uuid.New(), Valid: true})
	f.setStatus(t, child, database.ChatStatusInterrupting, uuid.NullUUID{})

	cascadeCtx := context.WithValue(ctx, interruptCascadeKey{}, true)
	active, err := f.server.settleArchivePass(cascadeCtx, f.root.ID)
	require.NoError(t, err)
	require.True(t, active)
	f.requireStatus(t, database.ChatStatusInterrupting, late)
	f.requireStatus(t, database.ChatStatusWaiting, child)
}

// spawnReadCounts counts the limit, depth and live count reads that the
// spawn validation makes, inside and outside the creation transaction.
// Reads of the same queries by capacity admission or by the orchestration
// prompt are not validation and are not counted.
type spawnReadCounts struct {
	limits, depth, live atomic.Int64
}

// fromSpawnValidation reports whether the query that calls it runs for
// forkValidateSpawnParent or for the checks in the creation transaction.
func fromSpawnValidation() bool {
	pcs := make([]uintptr, 32)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(3, pcs)])
	for {
		frame, more := frames.Next()
		name := frame.Function[strings.LastIndex(frame.Function, "/")+1:]
		switch {
		case strings.HasSuffix(name, ".subagentLimits"),
			strings.HasSuffix(name, ".subagentDepth"),
			strings.HasSuffix(name, ".validateNestedSpawnParent"),
			strings.HasSuffix(name, ".validateFamilyLiveSubagents"):
		case strings.HasSuffix(name, ".forkValidateSpawnParent"),
			strings.Contains(name, ".createSubagentChatWithID.func"):
			return true
		default:
			return false
		}
		if !more {
			return false
		}
	}
}

// spawnReadsStore hands out fork queries that count into counts, also in
// its transactions.
type spawnReadsStore struct {
	database.Store
	counts *spawnReadCounts
}

func (s spawnReadsStore) InTx(fn func(database.Store) error, opts *database.TxOptions) error {
	return s.Store.InTx(func(tx database.Store) error {
		return fn(spawnReadsStore{Store: tx, counts: s.counts})
	}, opts)
}

func (s spawnReadsStore) ForkUnwrap() database.Store { return s.Store }

func (s spawnReadsStore) ForkWrap(inner forkdb.Store) forkdb.Store {
	return spawnReadsForkStore{Store: inner, counts: s.counts}
}

type spawnReadsForkStore struct {
	forkdb.Store
	counts *spawnReadCounts
}

func (s spawnReadsForkStore) GetChatOrganizationSubagentLimits(ctx context.Context, organizationID uuid.UUID) (forkdb.ChatOrganizationSubagentLimit, error) {
	if fromSpawnValidation() {
		s.counts.limits.Add(1)
	}
	return s.Store.GetChatOrganizationSubagentLimits(ctx, organizationID)
}

func (s spawnReadsForkStore) GetChatSubagentByChatID(ctx context.Context, chatID uuid.UUID) (forkdb.ChatSubagent, error) {
	if fromSpawnValidation() {
		s.counts.depth.Add(1)
	}
	return s.Store.GetChatSubagentByChatID(ctx, chatID)
}

func (s spawnReadsForkStore) CountLiveSubagentsInFamily(ctx context.Context, rootChatID uuid.UUID) (int64, error) {
	if fromSpawnValidation() {
		s.counts.live.Add(1)
	}
	return s.Store.CountLiveSubagentsInFamily(ctx, rootChatID)
}

// spawn_agent checks the limits, the depth and the live count once on the
// reloaded parent, to fail before prompt hooks run, and once in the creation
// transaction, which decides.
func TestForkSpawnValidationReads(t *testing.T) {
	t.Parallel()
	raw, ps, sqlDB := dbtestutil.NewDBWithSQLDB(t)
	counts := &spawnReadCounts{}
	db := spawnReadsStore{Store: raw, counts: counts}
	f := newNestedSubagentsFixtureOn(t, db, ps, sqlDB, codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}, Limits{})
	ctx := chatdTestContext(t)
	_, err := f.fdb.UpsertChatOrganizationSubagentLimits(ctx, forkdb.UpsertChatOrganizationSubagentLimitsParams{
		OrganizationID:   f.root.OrganizationID,
		MaxLivePerFamily: sql.NullInt32{Int32: 5, Valid: true},
	})
	require.NoError(t, err)
	child, err := f.spawn(t, f.root, childSubagentChatOptions{})
	require.NoError(t, err)

	counts.limits.Store(0)
	counts.depth.Store(0)
	counts.live.Store(0)
	resp := runSpawnAgentTool(ctx, t, f.server, child, spawnAgentArgs{
		Type:   subagentTypeGeneral,
		Prompt: "delegate work",
	})
	require.False(t, resp.IsError, resp.Content)
	require.EqualValues(t, 2, counts.limits.Load(), "organization limit reads")
	require.EqualValues(t, 2, counts.depth.Load(), "parent depth reads")
	require.EqualValues(t, 2, counts.live.Load(), "family live count reads")
}
