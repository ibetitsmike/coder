package chatd

// This file is fork code: it tests the subagent capacity limits of the
// fork-nested-subagents experiment.

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
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
