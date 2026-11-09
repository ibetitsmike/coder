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
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
)

// testForkCapacityLimiter wraps an unlocked upstream limiter, so only the
// fork limits can refuse.
func testForkCapacityLimiter(experiments codersdk.Experiments, limits ForkLimits) AgentCapacityLimiter {
	server := &Server{
		experiments:            experiments,
		chatLimits:             Limits{Fork: limits},
		inFlightChatStaleAfter: 30 * time.Second,
	}
	return server.forkCapacityLimiter(newAgentCapacityLimiter(staticAgentCapacityUnlock(true), 30))
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

// A full family must not keep the worker from admitting the subagents of
// other families in the same acquisition pass.
func TestForkCapacityFullFamilyDoesNotBlockOtherFamilies(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	setOrgSubagentCapacity(t, f.db, f.org.ID, 0, 1)

	rootA := f.createRunningChat(t)
	rootB := f.createRunningChat(t)
	// Another replica runs the only slot of family A.
	occupant := f.createRunningSubagentChat(t, rootA.ID)
	acquireChat(t, f, occupant.ID, uuid.New(), uuid.New())
	// Created first, so the worker sees it before the child of root B.
	waiting := f.createRunningSubagentChat(t, rootA.ID)
	other := f.createRunningSubagentChat(t, rootB.ID)

	starter := newRecordingTaskStarter()
	opts := testOptions(t, f, starter)
	opts.AgentCapacityLimiter = testForkCapacityLimiter(codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}, ForkLimits{})
	startWorker(t, opts)

	starter.waitCall(t, taskKindGeneration, other.ID)
	chat, err := f.db.GetChatByID(ctx, waiting.ID)
	require.NoError(t, err)
	require.False(t, chat.WorkerID.Valid, "a subagent over its family limit must stay unowned")
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
