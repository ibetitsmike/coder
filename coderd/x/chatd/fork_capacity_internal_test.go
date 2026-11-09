package chatd

// This file is fork code: it tests the subagent capacity limits of the
// fork-nested-subagents experiment.

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
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

// createForkChat creates a running chat in the organization under parent, a
// root chat when parent is not valid.
func createForkChat(t *testing.T, f *workerTestFixture, organizationID uuid.UUID, parent, root uuid.NullUUID) database.Chat {
	t.Helper()
	res, err := chatstate.CreateChat(testutil.Context(t, testutil.WaitShort), f.db, f.pubsub, chatstate.CreateChatInput{
		OrganizationID:    organizationID,
		OwnerID:           f.user.ID,
		LastModelConfigID: f.model.ID,
		Title:             "test",
		ClientType:        database.ChatClientTypeApi,
		ParentChatID:      parent,
		RootChatID:        root,
		InitialStatus:     database.ChatStatusRunning,
		InitialMessages:   []chatstate.Message{userTextMessage(t, "hello", f.user.ID, f.model.ID)},
	})
	require.NoError(t, err)
	return res.Chat
}

// createForkFamily creates a root chat in the organization, owned by another
// replica so it is no acquisition candidate, and the given number of runnable
// subagents under it.
func createForkFamily(t *testing.T, f *workerTestFixture, organizationID uuid.UUID, children int) (database.Chat, []database.Chat) {
	t.Helper()
	root := createForkChat(t, f, organizationID, uuid.NullUUID{}, uuid.NullUUID{})
	acquireChat(t, f, root.ID, uuid.New(), uuid.New())
	rootID := uuid.NullUUID{UUID: root.ID, Valid: true}
	subagents := make([]database.Chat, 0, children)
	for range children {
		subagents = append(subagents, createForkChat(t, f, organizationID, rootID, rootID))
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

// startNestedSubagentsWorker starts a worker whose server has the
// fork-nested-subagents experiment on, so it interleaves subagent candidates.
func startNestedSubagentsWorker(t *testing.T, f *workerTestFixture, opts chatWorkerOptions) {
	t.Helper()
	server, err := New(f.pubsub, Config{
		Logger:      testutil.Logger(t),
		Database:    f.db,
		ReplicaID:   uuid.New(),
		Experiments: codersdk.Experiments{codersdk.ExperimentForkNestedSubagents},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = server.Close() })
	worker, err := newChatWorker(server, opts)
	require.NoError(t, err)
	require.NoError(t, worker.Start(context.Background()))
	t.Cleanup(func() { require.NoError(t, worker.Close()) })
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
	startNestedSubagentsWorker(t, f, opts)

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
	startNestedSubagentsWorker(t, f, opts)

	starter.waitCall(t, taskKindGeneration, other[0].ID)
	requireUnowned(t, f, waiting)
}

// A coordinator that holds its family's only slot while it waits for its own
// child must get that child admitted, even when more older siblings queue than
// the acquisition candidates hold: only the child excludes the coordinator.
func TestForkCapacitySlotHolderChildPrecedesQueuedSiblings(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	setOrgSubagentCapacity(t, f.db, f.org.ID, 0, 1)
	starter := newRecordingTaskStarter()
	opts := testOptions(t, f, starter)
	opts.AgentCapacityLimiter = testForkCapacityLimiter(codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}, ForkLimits{})

	root, children := createForkFamily(t, f, f.org.ID, 2*int(opts.AcquisitionBatchSize)+3)
	coordinator, siblings := children[0], children[1:]
	// Another replica runs the coordinator in the family's only slot.
	acquireChat(t, f, coordinator.ID, uuid.New(), uuid.New())
	grandchild := createForkChat(t, f, f.org.ID,
		uuid.NullUUID{UUID: coordinator.ID, Valid: true},
		uuid.NullUUID{UUID: root.ID, Valid: true})
	makeNewestCandidate(t, f, grandchild.ID)
	startNestedSubagentsWorker(t, f, opts)

	starter.waitCall(t, taskKindGeneration, grandchild.ID)
	requireUnowned(t, f, siblings)
}

// Unowned interrupting subagents of a capped family rank first by status, so
// they must not fill the candidates of a status-major order either. Without
// the experiment, candidates keep upstream's status and age order.
func TestForkAcquisitionCandidatesInterleaveBeforeStatus(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)

	_, familyA := createForkFamily(t, f, f.org.ID, 3)
	for _, chat := range familyA {
		forceExecutionState(t, f, chat.ID, database.ChatStatusInterrupting, false)
	}
	_, familyB := createForkFamily(t, f, f.org.ID, 1)
	makeNewestCandidate(t, f, familyB[0].ID)

	candidates := func(interleave bool) []uuid.UUID {
		rows, err := f.db.GetChatWorkerAcquisitionCandidates(ctx, database.GetChatWorkerAcquisitionCandidatesParams{
			StaleSeconds:        30,
			LimitCount:          2,
			InterleaveSubagents: interleave,
		})
		require.NoError(t, err)
		ids := make([]uuid.UUID, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		return ids
	}
	interleaved := candidates(true)
	require.Len(t, interleaved, 2)
	require.Contains(t, interleaved, familyB[0].ID)
	upstreamOrder := candidates(false)
	require.Len(t, upstreamOrder, 2)
	require.NotContains(t, upstreamOrder, familyB[0].ID)
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

// capacityWaitFixture is a family whose only concurrency slot is held by
// occupant, so waiting stays unowned.
type capacityWaitFixture struct {
	admissionFixture
	root     database.Chat
	occupant database.Chat
	waiting  database.Chat
}

func newCapacityWaitFixture(t *testing.T) capacityWaitFixture {
	t.Helper()
	f := newAdmissionFixture(t)
	setOrgSubagentCapacity(t, f.db, f.org.ID, 0, 1)
	root := f.chat(t, database.Chat{})
	occupant := f.occupiedSubagent(t, root)
	waiting := f.chat(t, database.Chat{
		ParentChatID: uuid.NullUUID{UUID: root.ID, Valid: true},
		RootChatID:   uuid.NullUUID{UUID: root.ID, Valid: true},
	})
	return capacityWaitFixture{admissionFixture: f, root: root, occupant: occupant, waiting: waiting}
}

func TestForkChatQueuedForCapacity(t *testing.T) {
	t.Parallel()
	f := newCapacityWaitFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	// The upstream pool is unlocked, so only the fork limits can queue.
	server := func(experiments codersdk.Experiments) *Server {
		return &Server{
			db:                     f.db,
			experiments:            experiments,
			inFlightChatStaleAfter: 30 * time.Second,
			agentCapacityLimiter:   newAgentCapacityLimiter(staticAgentCapacityUnlock(true), 30),
		}
	}
	nested := server(codersdk.Experiments{codersdk.ExperimentForkNestedSubagents})
	queued := func(p *Server, chat database.Chat) bool {
		t.Helper()
		got, err := p.ChatQueuedForCapacity(ctx, chat)
		require.NoError(t, err)
		return got
	}

	require.True(t, queued(nested, f.waiting))
	require.False(t, queued(nested, f.root))
	require.False(t, queued(server(upstreamExperimentsKnown()), f.waiting))
	// Only a running, unarchived subagent waits for a slot.
	archived := f.waiting
	archived.Archived = true
	require.False(t, queued(nested, archived))
	idle := f.waiting
	idle.Status = database.ChatStatusWaiting
	require.False(t, queued(nested, idle))

	// The family is over its limit, as after the limit is lowered, yet the
	// chats that hold a slot do not wait for one.
	admitted := f.occupiedSubagent(t, f.root)
	require.False(t, queued(nested, f.occupant))
	require.False(t, queued(nested, admitted))

	// The owners are gone, so their slots are free.
	require.NoError(t, f.db.DeleteAllChatHeartbeats(ctx, f.occupant.ID))
	require.NoError(t, f.db.DeleteAllChatHeartbeats(ctx, admitted.ID))
	require.False(t, queued(nested, f.waiting))
}

// With nested subagents on, the fork limiter applies upstream's subagent
// pool without the candidate's ancestors, so its answer replaces upstream's.
func TestForkChatQueuedForCapacityUpstreamPool(t *testing.T) {
	t.Parallel()
	f := newAdmissionFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	pool := newAgentCapacityLimiter(staticAgentCapacityUnlock(false), 30)
	pool.subagentCapacity = 1
	server := func(experiments codersdk.Experiments) *Server {
		return &Server{
			db:                     f.db,
			experiments:            experiments,
			inFlightChatStaleAfter: 30 * time.Second,
			agentCapacityLimiter:   pool,
		}
	}
	queued := func(p *Server, chat database.Chat) bool {
		t.Helper()
		got, err := p.ChatQueuedForCapacity(ctx, chat)
		require.NoError(t, err)
		return got
	}
	// The coordinator holds the only pool slot while it waits for its child.
	root := f.occupiedRoot(t)
	coordinator := f.occupiedSubagent(t, root)
	child := f.chat(t, database.Chat{
		ParentChatID: uuid.NullUUID{UUID: coordinator.ID, Valid: true},
		RootChatID:   uuid.NullUUID{UUID: root.ID, Valid: true},
	})
	// A subagent of another family has no ancestor in the pool.
	otherRoot := f.occupiedRoot(t)
	other := f.chat(t, database.Chat{
		ParentChatID: uuid.NullUUID{UUID: otherRoot.ID, Valid: true},
		RootChatID:   uuid.NullUUID{UUID: otherRoot.ID, Valid: true},
	})

	nested := server(codersdk.Experiments{codersdk.ExperimentForkNestedSubagents})
	require.False(t, queued(nested, child))
	require.True(t, queued(server(upstreamExperimentsKnown()), child))
	require.True(t, queued(nested, other))
}

func TestForkListAgentsWaitingForCapacity(t *testing.T) {
	t.Parallel()
	f := newCapacityWaitFixture(t)
	ctx := chatdTestContext(t)

	for name, tc := range map[string]struct {
		experiments codersdk.Experiments
		reported    bool
	}{
		"NestedSubagents": {experiments: codersdk.Experiments{codersdk.ExperimentForkNestedSubagents}, reported: true},
		"ChatTodosOnly":   {experiments: codersdk.Experiments{codersdk.ExperimentForkChatTodos}},
		"Upstream":        {experiments: upstreamExperimentsKnown()},
	} {
		server := newInternalTestServer(t, f.db, f.ps, chatprovider.ProviderAPIKeys{}, withInternalTestServerExperiments(tc.experiments))
		tools, err := server.subagentTools(ctx, func() database.Chat { return f.root }, f.root.LastModelConfigID)
		require.NoError(t, err)
		listTool := findToolByName(tools, "list_agents")
		require.NotNil(t, listTool)
		require.Equal(t, tc.reported, strings.Contains(listTool.Info().Description, "waiting_for_capacity"), name)
		resp := runSubagentTool(ctx, t, server, f.root, f.root.LastModelConfigID, "list_agents", listAgentsArgs{})
		require.False(t, resp.IsError, resp.Content)
		var got struct {
			Agents []map[string]json.RawMessage `json:"agents"`
		}
		require.NoError(t, json.Unmarshal([]byte(resp.Content), &got))
		waiting := map[string]string{}
		for _, agent := range got.Agents {
			var chatID string
			require.NoError(t, json.Unmarshal(agent["chat_id"], &chatID))
			if raw, ok := agent["waiting_for_capacity"]; ok {
				waiting[chatID] = string(raw)
			}
		}
		want := map[string]string{}
		if tc.reported {
			want[f.waiting.ID.String()] = "true"
		}
		require.Equal(t, want, waiting, name)
	}
}
