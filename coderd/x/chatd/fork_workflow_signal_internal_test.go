package chatd

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
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
}
