package chatd

// This file is fork code: it tests the subagent capacity limits of the
// fork-nested-subagents experiment.

import (
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatstate"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
)

// testForkCapacityLimiter wraps an unlocked upstream limiter, so only the
// fork limits can refuse.
func testForkCapacityLimiter(experiments codersdk.Experiments, limits ForkLimits) AgentCapacityLimiter {
	return testForkCapacityLimiterWithInner(experiments, limits, newAgentCapacityLimiter(staticAgentCapacityUnlock(true), 30))
}

func testForkCapacityLimiterWithInner(experiments codersdk.Experiments, limits ForkLimits, inner AgentCapacityLimiter) AgentCapacityLimiter {
	server := &Server{
		experiments:            experiments,
		chatLimits:             Limits{Fork: limits},
		inFlightChatStaleAfter: 30 * time.Second,
	}
	return server.forkCapacityLimiter(inner)
}

func setOrgSubagentCapacity(t *testing.T, db database.Store, organizationID uuid.UUID, maxConcurrent, maxConcurrentPerFamily int32) {
	t.Helper()
	fdb, err := forkdb.From(db)
	require.NoError(t, err)
	nullLimit := func(v int32) sql.NullInt32 { return sql.NullInt32{Int32: v, Valid: v > 0} }
	_, err = fdb.UpsertChatOrganizationSubagentLimits(testutil.Context(t, testutil.WaitShort), forkdb.UpsertChatOrganizationSubagentLimitsParams{
		OrganizationID:         organizationID,
		MaxConcurrent:          nullLimit(maxConcurrent),
		MaxConcurrentPerFamily: nullLimit(maxConcurrentPerFamily),
	})
	require.NoError(t, err)
}

// createForkFamily creates a root chat in the organization, owned by another
// replica so it is no acquisition candidate, and the given number of runnable
// subagents under it.
func createForkFamily(t *testing.T, f *workerTestFixture, organizationID uuid.UUID, children int) (database.Chat, []database.Chat) {
	t.Helper()
	ctx := testutil.Context(t, testutil.WaitShort)
	create := func(parent uuid.NullUUID) database.Chat {
		res, err := chatstate.CreateChat(ctx, f.db, f.pubsub, chatstate.CreateChatInput{
			OrganizationID:    organizationID,
			OwnerID:           f.user.ID,
			LastModelConfigID: f.model.ID,
			Title:             "test",
			ClientType:        database.ChatClientTypeApi,
			ParentChatID:      parent,
			RootChatID:        parent,
			InitialStatus:     database.ChatStatusRunning,
			InitialMessages:   []chatstate.Message{userTextMessage(t, "hello", f.user.ID, f.model.ID)},
		})
		require.NoError(t, err)
		return res.Chat
	}
	root := create(uuid.NullUUID{})
	acquireChat(t, f, root.ID, uuid.New(), uuid.New())
	subagents := make([]database.Chat, 0, children)
	for range children {
		subagents = append(subagents, create(uuid.NullUUID{UUID: root.ID, Valid: true}))
	}
	return root, subagents
}

// makeNewestCandidate moves the chat behind every other candidate in age
// order, the position where it starved behind a capped backlog.
func makeNewestCandidate(t *testing.T, f *workerTestFixture, chatID uuid.UUID) {
	t.Helper()
	_, err := f.sqlDB.ExecContext(testutil.Context(t, testutil.WaitShort),
		"UPDATE chats SET updated_at = NOW() + INTERVAL '1 hour' WHERE id = $1", chatID)
	require.NoError(t, err)
}

func requireUnowned(t *testing.T, f *workerTestFixture, chats []database.Chat) {
	t.Helper()
	ctx := testutil.Context(t, testutil.WaitShort)
	for _, c := range chats {
		chat, err := f.db.GetChatByID(ctx, c.ID)
		require.NoError(t, err)
		require.False(t, chat.WorkerID.Valid, "a subagent over its limit must stay unowned")
	}
}

// A full family whose waiting subagents outnumber the acquisition candidates
// must not keep the worker from admitting the subagents of other families.
func TestForkCapacityFullFamilyDoesNotBlockOtherFamilies(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	setOrgSubagentCapacity(t, f.db, f.org.ID, 0, 1)
	starter := newRecordingTaskStarter()
	opts := testOptions(t, f, starter)
	opts.AgentCapacityLimiter = testForkCapacityLimiter(codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}, ForkLimits{})

	_, familyA := createForkFamily(t, f, f.org.ID, 2*int(opts.AcquisitionBatchSize)+2)
	// Another replica runs the only slot of family A.
	acquireChat(t, f, familyA[0].ID, uuid.New(), uuid.New())
	_, familyB := createForkFamily(t, f, f.org.ID, 1)
	makeNewestCandidate(t, f, familyB[0].ID)
	startWorker(t, opts)

	starter.waitCall(t, taskKindGeneration, familyB[0].ID)
	requireUnowned(t, f, familyA[1:])
}

// A full organization whose waiting subagents outnumber the acquisition
// candidates must not keep the worker from admitting another organization's.
func TestForkCapacityFullOrganizationDoesNotBlockOtherOrganizations(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	setOrgSubagentCapacity(t, f.db, f.org.ID, 1, 0)
	starter := newRecordingTaskStarter()
	opts := testOptions(t, f, starter)
	opts.AgentCapacityLimiter = testForkCapacityLimiter(codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}, ForkLimits{})

	// Another replica runs the only slot of the organization.
	_, occupant := createForkFamily(t, f, f.org.ID, 1)
	acquireChat(t, f, occupant[0].ID, uuid.New(), uuid.New())
	// One waiting subagent per family, so interleaving families alone
	// cannot make room for the other organization.
	waiting := make([]database.Chat, 0, 2*int(opts.AcquisitionBatchSize)+2)
	for range cap(waiting) {
		_, family := createForkFamily(t, f, f.org.ID, 1)
		waiting = append(waiting, family...)
	}
	otherOrg := dbgen.Organization(t, f.db, database.Organization{})
	_, other := createForkFamily(t, f, otherOrg.ID, 1)
	makeNewestCandidate(t, f, other[0].ID)
	startWorker(t, opts)

	starter.waitCall(t, taskKindGeneration, other[0].ID)
	requireUnowned(t, f, waiting)
}

func TestForkCapacityAdmission(t *testing.T) {
	t.Parallel()

	nested := codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}
	subagent := func(t *testing.T, f admissionFixture, parent database.Chat) database.Chat {
		t.Helper()
		root := parent.ID
		if parent.RootChatID.Valid {
			root = parent.RootChatID.UUID
		}
		return f.chat(t, database.Chat{
			ParentChatID: uuid.NullUUID{UUID: parent.ID, Valid: true},
			RootChatID:   uuid.NullUUID{UUID: root, Valid: true},
		})
	}

	t.Run("DeploymentLimitSpansOrganizations", func(t *testing.T) {
		t.Parallel()
		f := newAdmissionFixture(t)
		ctx := testutil.Context(t, testutil.WaitLong)
		f.occupiedSubagent(t, f.occupiedRoot(t))

		otherOrg := dbgen.Organization(t, f.db, database.Organization{})
		otherRoot := dbgen.Chat(t, f.db, database.Chat{OrganizationID: otherOrg.ID, OwnerID: f.owner.ID, LastModelConfigID: f.modelConfig.ID, Status: database.ChatStatusRunning})
		candidate := dbgen.Chat(t, f.db, database.Chat{
			OrganizationID:    otherOrg.ID,
			OwnerID:           f.owner.ID,
			LastModelConfigID: f.modelConfig.ID,
			Status:            database.ChatStatusRunning,
			ParentChatID:      uuid.NullUUID{UUID: otherRoot.ID, Valid: true},
			RootChatID:        uuid.NullUUID{UUID: otherRoot.ID, Valid: true},
		})
		admitted, err := testForkCapacityLimiter(nested, ForkLimits{MaxConcurrentSubagents: 1}).Admit(ctx, f.db, candidate)
		require.ErrorIs(t, err, errSkipAcquire)
		require.False(t, admitted)
	})

	t.Run("AncestorsDoNotCount", func(t *testing.T) {
		t.Parallel()
		f := newAdmissionFixture(t)
		ctx := testutil.Context(t, testutil.WaitLong)
		setOrgSubagentCapacity(t, f.db, f.org.ID, 1, 1)
		// The parent waits for its child in wait_agent while holding the
		// only slot of the organization and the family.
		parent := f.occupiedSubagent(t, f.occupiedRoot(t))

		admitted, err := testForkCapacityLimiter(nested, ForkLimits{}).Admit(ctx, f.db, subagent(t, f, parent))
		require.NoError(t, err)
		require.True(t, admitted)
	})

	t.Run("UpstreamPoolExcludesAncestors", func(t *testing.T) {
		t.Parallel()
		f := newAdmissionFixture(t)
		ctx := testutil.Context(t, testutil.WaitLong)
		pool := newAgentCapacityLimiter(staticAgentCapacityUnlock(false), 30)
		pool.subagentCapacity = 1
		limiter := testForkCapacityLimiterWithInner(nested, ForkLimits{}, pool)
		// The parent holds the only pool slot while it waits for its child.
		parent := f.occupiedSubagent(t, f.occupiedRoot(t))

		admitted, err := limiter.Admit(ctx, f.db, subagent(t, f, parent))
		require.NoError(t, err)
		require.True(t, admitted)

		admitted, err = limiter.Admit(ctx, f.db, subagent(t, f, f.occupiedRoot(t)))
		require.ErrorIs(t, err, errSkipAcquire)
		require.False(t, admitted)
	})

	t.Run("RootsAreNotCapped", func(t *testing.T) {
		t.Parallel()
		f := newAdmissionFixture(t)
		ctx := testutil.Context(t, testutil.WaitLong)
		setOrgSubagentCapacity(t, f.db, f.org.ID, 1, 1)
		f.occupiedSubagent(t, f.occupiedRoot(t))

		admitted, err := testForkCapacityLimiter(nested, ForkLimits{MaxConcurrentSubagents: 1}).Admit(ctx, f.db, f.chat(t, database.Chat{}))
		require.NoError(t, err)
		require.True(t, admitted)
	})

	t.Run("ExperimentOff", func(t *testing.T) {
		t.Parallel()
		f := newAdmissionFixture(t)
		ctx := testutil.Context(t, testutil.WaitLong)
		setOrgSubagentCapacity(t, f.db, f.org.ID, 1, 1)
		root := f.occupiedRoot(t)
		f.occupiedSubagent(t, root)

		admitted, err := testForkCapacityLimiter(upstreamExperimentsKnown(), ForkLimits{MaxConcurrentSubagents: 1}).Admit(ctx, f.db, subagent(t, f, root))
		require.NoError(t, err)
		require.True(t, admitted)
	})
}
