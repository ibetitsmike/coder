package chatd

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/sqlc-dev/pqtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	coderdpubsub "github.com/coder/coder/v2/coderd/pubsub"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/quartz"
)

// historyCountingStore counts the history reads of each chat.
type historyCountingStore struct {
	database.Store

	mu    sync.Mutex
	reads map[uuid.UUID]int
}

func (s *historyCountingStore) ForkUnwrap() database.Store { return s.Store }

func (*historyCountingStore) ForkWrap(inner forkdb.Store) forkdb.Store { return inner }

func (s *historyCountingStore) GetChatMessagesByChatID(
	ctx context.Context, arg database.GetChatMessagesByChatIDParams,
) ([]database.ChatMessage, error) {
	s.mu.Lock()
	if s.reads == nil {
		s.reads = map[uuid.UUID]int{}
	}
	s.reads[arg.ChatID]++
	s.mu.Unlock()
	return s.Store.GetChatMessagesByChatID(ctx, arg)
}

func (s *historyCountingStore) historyReads(chatID uuid.UUID) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads[chatID]
}

const signalOneStepScript = `
def main(args):
    return agent("Say hi.", title = "hi")
`

// TestForkWorkflowStepSignal covers how a waiting step learns of its
// report: the report tool wakes it, on the owner's process or through the
// owner channel, and only a resume reads the child's history.
func TestForkWorkflowStepSignal(t *testing.T) {
	t.Parallel()

	// One step from its spawn to its report reads no history of its
	// child: the report tool hands the value to the run.
	t.Run("ReportWakesTheStepWithoutReadingHistory", func(t *testing.T) {
		t.Parallel()
		db, ps := dbtestutil.NewDB(t)
		store := &historyCountingStore{Store: db}
		mock := quartz.NewMock(t)
		server := newInternalTestServer(t, store, ps, chatprovider.ProviderAPIKeys{},
			withInternalTestServerExperiments(codersdk.Experiments{codersdk.ExperimentForkWorkflows}),
			withInternalTestServerClock(mock))
		user, org, model := seedInternalChatDeps(t, db)
		f := &forkWorkflowFixture{db: db, ps: ps, server: server, user: user, org: org, model: model}
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: signalOneStepScript})
		step := f.steps(ctx, t, run, 1)[0]
		childID := uuid.MustParse(step.ChatID)

		// The child keeps running: only the report can end the wait, as
		// the mock clock fires no fallback poll.
		resp := f.callReport(ctx, t, server, childID, `{"text":"hi"}`)
		require.False(t, resp.IsError, resp.Content)
		assert.Equal(t, reportAcceptedText, resp.Content)
		res := testutil.RequireReceive(ctx, t, awaitRegistered(ctx, t, run))
		require.Equal(t, workflowStatusCompleted, res.Status, res.Error)
		assert.JSONEq(t, `{"text":"hi"}`, string(res.Result))
		assert.Zero(t, store.historyReads(childID), "the step read the history of its child")

		stepRow, err := forkWorkflowDB(t, db).GetWorkflowStep(ctx, childID)
		require.NoError(t, err)
		assert.Equal(t, stepStatusReported, stepRow.Status)
		assert.JSONEq(t, `{"text":"hi"}`, string(stepRow.Value.RawMessage))
	})

	// The run takes the first report: a later one cannot change a value
	// the script may already hold, on this process or another one, and
	// the journal keeps the first.
	t.Run("FirstReportWins", func(t *testing.T) {
		t.Parallel()
		f, mock := newForkWorkflowFixtureWithClock(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: `
def main(args):
    one = agent("One.", title = "one")
    two = agent("Two.", title = "two")
    return [one, two]
`})
		firstID := uuid.MustParse(f.steps(ctx, t, run, 1)[0].ChatID)
		other := f.peerServer(t, mock)

		resp := f.callReport(ctx, t, f.server, firstID, `{"text":"first"}`)
		require.False(t, resp.IsError, resp.Content)
		secondID := uuid.MustParse(f.steps(ctx, t, run, 2)[1].ChatID)
		for _, server := range []*Server{f.server, other} {
			resp = f.callReport(ctx, t, server, firstID, `{"text":"again"}`)
			require.False(t, resp.IsError, resp.Content)
			assert.Equal(t, reportKeptText, resp.Content)
		}
		stepRow, err := forkWorkflowDB(t, f.db).GetWorkflowStep(ctx, firstID)
		require.NoError(t, err)
		assert.JSONEq(t, `{"text":"first"}`, string(stepRow.Value.RawMessage))

		resp = f.callReport(ctx, t, f.server, secondID, `{"text":"two"}`)
		require.False(t, resp.IsError, resp.Content)
		res := testutil.RequireReceive(ctx, t, awaitRegistered(ctx, t, run))
		require.Equal(t, workflowStatusCompleted, res.Status, res.Error)
		assert.JSONEq(t, `[{"text":"first"},{"text":"two"}]`, string(res.Result))
	})

	// A report that the run's step has not been added for yet waits for
	// the step, which takes it once the run adds it.
	t.Run("EarlyReportWaitsForItsStep", func(t *testing.T) {
		t.Parallel()
		run := &workflowRun{id: uuid.New(), seen: map[string]int{}, done: make(chan struct{})}
		childID := uuid.New()
		resp, ok := run.acceptReport(chatdTestContext(t), childID, `{"text":"early"}`)
		require.True(t, ok)
		assert.Equal(t, reportAcceptedText, resp.Content)
		resp, ok = run.acceptReport(chatdTestContext(t), childID, `{"text":"late"}`)
		require.True(t, ok)
		assert.Equal(t, reportKeptText, resp.Content, "the first early report wins")

		step := &workflowStep{id: childID, chatID: childID, status: stepStatusRunning}
		run.mu.Lock()
		applied := run.addStepLocked(step)
		reported, _ := step.signalsLocked()
		run.mu.Unlock()
		require.True(t, applied)
		assert.Equal(t, stepStatusReported, step.status)
		assert.JSONEq(t, `{"text":"early"}`, string(step.value))
		select {
		case <-reported:
		default:
			t.Fatal("the early report did not wake the step")
		}
		assert.Empty(t, run.earlyReports)
	})

	// A report that another process journaled first wins over a later
	// one on the owner's process, even before the owner event arrives.
	t.Run("AJournaledReportWinsOverALocalOne", func(t *testing.T) {
		t.Parallel()
		f, _ := newForkWorkflowFixtureWithClock(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: signalOneStepScript})
		childID := uuid.MustParse(f.steps(ctx, t, run, 1)[0].ChatID)
		writePeerReport(ctx, t, f.db, childID, `{"text":"peer"}`)

		resp := f.callReport(ctx, t, f.server, childID, `{"text":"local"}`)
		require.False(t, resp.IsError, resp.Content)
		assert.Equal(t, reportKeptText, resp.Content)
		res := testutil.RequireReceive(ctx, t, awaitRegistered(ctx, t, run))
		require.Equal(t, workflowStatusCompleted, res.Status, res.Error)
		assert.JSONEq(t, `{"text":"peer"}`, string(res.Result))
		stepRow, err := forkWorkflowDB(t, f.db).GetWorkflowStep(ctx, childID)
		require.NoError(t, err)
		assert.JSONEq(t, `{"text":"peer"}`, string(stepRow.Value.RawMessage))
	})

	// A child that fails after another process journaled its report does
	// not fail the step: the owner event may lag the child's status, and
	// the step reads its row before it fails.
	t.Run("ChildErrorKeepsAPeerReport", func(t *testing.T) {
		t.Parallel()
		f, _ := newForkWorkflowFixtureWithClock(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: signalOneStepScript})
		childID := uuid.MustParse(f.steps(ctx, t, run, 1)[0].ChatID)
		writePeerReport(ctx, t, f.db, childID, `{"text":"peer"}`)

		setChatStatus(ctx, t, f.db, childID, database.ChatStatusError, "boom")
		require.NoError(t, f.ps.Publish(coderdpubsub.ChatStateUpdateChannel(childID), []byte("{}")))
		res := testutil.RequireReceive(ctx, t, awaitRegistered(ctx, t, run))
		require.Equal(t, workflowStatusCompleted, res.Status, res.Error)
		assert.JSONEq(t, `{"text":"peer"}`, string(res.Result))
		stepRow, err := forkWorkflowDB(t, f.db).GetWorkflowStep(ctx, childID)
		require.NoError(t, err)
		assert.Equal(t, stepStatusReported, stepRow.Status)
	})

	// A report that another process accepts lands in the journal and
	// reaches the owner over its channel: the owner's step ends while the
	// child still runs.
	t.Run("ReportOnAnotherProcessReachesTheOwner", func(t *testing.T) {
		t.Parallel()
		f, mock := newForkWorkflowFixtureWithClock(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: signalOneStepScript})
		childID := uuid.MustParse(f.steps(ctx, t, run, 1)[0].ChatID)
		other := f.peerServer(t, mock)

		resp := f.callReport(ctx, t, other, childID, `{"text":"remote"}`)
		require.False(t, resp.IsError, resp.Content)
		assert.Nil(t, other.workflows.get(run.id), "the other process executes nothing")
		res := testutil.RequireReceive(ctx, t, awaitRegistered(ctx, t, run))
		require.Equal(t, workflowStatusCompleted, res.Status, res.Error)
		assert.JSONEq(t, `{"text":"remote"}`, string(res.Result))
	})

	// A resume reads the history of an open step's child once: a report
	// that reached the history but not the journal becomes the value.
	t.Run("ResumeTakesAReportFromTheHistory", func(t *testing.T) {
		t.Parallel()
		f, mock := newForkWorkflowFixtureWithClock(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: signalOneStepScript})
		childID := uuid.MustParse(f.steps(ctx, t, run, 1)[0].ChatID)
		fdb := forkWorkflowDB(t, f.db)

		// The process died after the child's report reached its history
		// and before the journal recorded it.
		crashWorkflowServer(t, f.server)
		f.insert(t, childID, database.ChatMessageRoleAssistant,
			codersdk.ChatMessageToolCall("call-1", workflowReportToolName, json.RawMessage(`{"text":"history"}`)))
		f.insert(t, childID, database.ChatMessageRoleTool,
			codersdk.ChatMessageToolResult("call-1", workflowReportToolName,
				json.RawMessage(`"`+reportAcceptedText+`"`), false, false))
		next := f.peerServer(t, mock)
		require.Equal(t, workflowStatusInterrupted, journalRunStatus(ctx, t, fdb, run.id))

		resumed, err := next.startWorkflowRun(ctx, parent, f.model.ID,
			runWorkflowArgs{ResumeRunID: run.id.String()}, uuid.New())
		require.NoError(t, err)
		res := testutil.RequireReceive(ctx, t, awaitRegistered(ctx, t, resumed))
		require.Equal(t, workflowStatusCompleted, res.Status, res.Error)
		assert.JSONEq(t, `{"text":"history"}`, string(res.Result))
		stepRow, err := fdb.GetWorkflowStep(ctx, childID)
		require.NoError(t, err)
		assert.Equal(t, stepStatusReported, stepRow.Status, "the resume journals the value it found")
	})

	// A resume that could not read the history of an open step reads it
	// again before it treats the stopped child as unreported.
	t.Run("ResumeRetriesAFailedHistoryScan", func(t *testing.T) {
		t.Parallel()
		f, mock := newForkWorkflowFixtureWithClock(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: signalOneStepScript})
		childID := uuid.MustParse(f.steps(ctx, t, run, 1)[0].ChatID)

		crashWorkflowServer(t, f.server)
		f.insert(t, childID, database.ChatMessageRoleAssistant,
			codersdk.ChatMessageToolCall("call-1", workflowReportToolName, json.RawMessage(`{"text":"history"}`)))
		f.insert(t, childID, database.ChatMessageRoleTool,
			codersdk.ChatMessageToolResult("call-1", workflowReportToolName,
				json.RawMessage(`"`+reportAcceptedText+`"`), false, false))
		setChatStatus(ctx, t, f.db, childID, database.ChatStatusWaiting, "")
		store := &failingHistoryStore{Store: f.db, chatID: childID}
		store.fails.Store(1)
		next := newInternalTestServer(t, store, f.ps, chatprovider.ProviderAPIKeys{},
			withInternalTestServerExperiments(codersdk.Experiments{codersdk.ExperimentForkWorkflows}),
			withInternalTestServerClock(mock))
		fdb := forkWorkflowDB(t, f.db)
		require.Equal(t, workflowStatusInterrupted, journalRunStatus(ctx, t, fdb, run.id))

		resumed, err := next.startWorkflowRun(ctx, parent, f.model.ID,
			runWorkflowArgs{ResumeRunID: run.id.String()}, uuid.New())
		require.NoError(t, err)
		res := testutil.RequireReceive(ctx, t, awaitRegistered(ctx, t, resumed))
		require.Equal(t, workflowStatusCompleted, res.Status, res.Error)
		assert.JSONEq(t, `{"text":"history"}`, string(res.Result))
		assert.Zero(t, store.fails.Load(), "the first history read did not fail")
	})

	// A step that a resume adopts from another runner's row reads the
	// child's history too: the child may have reported into it before the
	// report reached the row.
	t.Run("AdoptedStepTakesAReportFromTheHistory", func(t *testing.T) {
		t.Parallel()
		f := newForkWorkflowFixture(t)
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: signalOneStepScript})
		childID := uuid.MustParse(f.steps(ctx, t, run, 1)[0].ChatID)

		restarted := f.restartWorkflowServer(t)
		fdb := forkWorkflowDB(t, f.db)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return journalRunStatus(ctx, t, fdb, run.id) == workflowStatusInterrupted
		}, testutil.IntervalFast)
		f.insert(t, childID, database.ChatMessageRoleAssistant,
			codersdk.ChatMessageToolCall("call-1", workflowReportToolName, json.RawMessage(`{"text":"history"}`)))
		f.insert(t, childID, database.ChatMessageRoleTool,
			codersdk.ChatMessageToolResult("call-1", workflowReportToolName,
				json.RawMessage(`"`+reportAcceptedText+`"`), false, false))

		// The resumed run rehydrates as if the first runner had not yet
		// journaled the step, so startStep adopts the row.
		claimed, ok, err := restarted.server.workflowJournal().claimRun(ctx, run.id)
		require.NoError(t, err)
		require.True(t, ok)
		resumed, err := restarted.server.rehydrateWorkflowRun(ctx, parent, claimed)
		require.NoError(t, err)
		require.Len(t, resumed.steps, 1)
		resumed.steps = nil

		adopted, err := resumed.startStep(ctx, stepSpec{Prompt: "Say hi.", Title: "hi"})
		require.NoError(t, err)
		assert.Equal(t, childID, adopted.id)
		assert.Equal(t, stepStatusReported, adopted.status)
		assert.JSONEq(t, `{"text":"history"}`, string(adopted.value))
		stepRow, err := fdb.GetWorkflowStep(ctx, childID)
		require.NoError(t, err)
		assert.Equal(t, stepStatusReported, stepRow.Status)
	})

	// A step whose child is reused without a journal row, as after a crash
	// between the child and its row, takes the report the child already
	// sent into its history.
	t.Run("ReusedChildWithoutARowTakesItsHistoryReport", func(t *testing.T) {
		t.Parallel()
		db, ps, sqlDB := dbtestutil.NewDBWithSQLDB(t)
		server := newInternalTestServer(t, db, ps, chatprovider.ProviderAPIKeys{},
			withInternalTestServerExperiments(codersdk.Experiments{codersdk.ExperimentForkWorkflows}))
		user, org, model := seedInternalChatDeps(t, db)
		f := &forkWorkflowFixture{db: db, ps: ps, server: server, user: user, org: org, model: model}
		ctx := chatdTestContext(t)
		parent := f.parentStatus(ctx, t, database.ChatStatusRunning)
		run := f.start(ctx, t, parent, runWorkflowArgs{Script: signalOneStepScript})
		childID := uuid.MustParse(f.steps(ctx, t, run, 1)[0].ChatID)

		restarted := f.restartWorkflowServer(t)
		fdb := forkWorkflowDB(t, f.db)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return journalRunStatus(ctx, t, fdb, run.id) == workflowStatusInterrupted
		}, testutil.IntervalFast)
		f.insert(t, childID, database.ChatMessageRoleAssistant,
			codersdk.ChatMessageToolCall("call-1", workflowReportToolName, json.RawMessage(`{"text":"history"}`)))
		f.insert(t, childID, database.ChatMessageRoleTool,
			codersdk.ChatMessageToolResult("call-1", workflowReportToolName,
				json.RawMessage(`"`+reportAcceptedText+`"`), false, false))
		_, err := sqlDB.ExecContext(ctx, "DELETE FROM workflow_steps WHERE id = $1", childID)
		require.NoError(t, err)

		claimed, ok, err := restarted.server.workflowJournal().claimRun(ctx, run.id)
		require.NoError(t, err)
		require.True(t, ok)
		resumed, err := restarted.server.rehydrateWorkflowRun(ctx, parent, claimed)
		require.NoError(t, err)
		require.Empty(t, resumed.steps)

		step, err := resumed.startStep(ctx, stepSpec{Prompt: "Say hi.", Title: "hi"})
		require.NoError(t, err)
		assert.Equal(t, childID, step.chatID)
		require.True(t, step.reused, "the step reuses the child")
		assert.Equal(t, stepStatusReported, step.status)
		assert.JSONEq(t, `{"text":"history"}`, string(step.value))
		stepRow, err := fdb.GetWorkflowStep(ctx, childID)
		require.NoError(t, err)
		assert.Equal(t, stepStatusReported, stepRow.Status)
	})
}

// failingHistoryStore fails the next fails history reads of chatID, the
// way a database outage fails them.
type failingHistoryStore struct {
	database.Store

	chatID uuid.UUID
	fails  atomic.Int32
}

func (s *failingHistoryStore) ForkUnwrap() database.Store { return s.Store }

func (*failingHistoryStore) ForkWrap(inner forkdb.Store) forkdb.Store { return inner }

func (s *failingHistoryStore) GetChatMessagesByChatID(
	ctx context.Context, arg database.GetChatMessagesByChatIDParams,
) ([]database.ChatMessage, error) {
	if arg.ChatID == s.chatID && s.fails.Load() > 0 && s.fails.Add(-1) >= 0 {
		return nil, xerrors.New("history outage")
	}
	return s.Store.GetChatMessagesByChatID(ctx, arg)
}

// writePeerReport journals a report for the step of chatID the way the
// workflow_report tool of another process does.
func writePeerReport(ctx context.Context, t *testing.T, db database.Store, chatID uuid.UUID, value string) {
	t.Helper()
	fdb := forkWorkflowDB(t, db)
	row, err := fdb.GetWorkflowStep(ctx, chatID)
	require.NoError(t, err)
	n, err := fdb.UpdateWorkflowStep(ctx, forkdb.UpdateWorkflowStepParams{
		ID:     row.ID,
		Status: stepStatusReported,
		Value:  pqtype.NullRawMessage{RawMessage: json.RawMessage(value), Valid: true},
		Reused: row.Reused,
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
}
