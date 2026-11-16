package chatd

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/database/pubsub"
	coderdpubsub "github.com/coder/coder/v2/coderd/pubsub"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/quartz"
)

// TestWorkflowWaitStepSeesFirstNotification checks that waitStep sees a
// step child that ends its turn between the first read of the child and
// the subscription of awaitSubagentCompletion. The mock clock never fires
// the fallback tick, so only the state notification can end the wait.
func TestWorkflowWaitStepSeesFirstNotification(t *testing.T) {
	t.Parallel()

	db, _ := dbtestutil.NewDB(t)
	ps := pubsub.NewInMemory()
	store := &endTurnOnFirstCheckStore{Store: db, ps: ps}
	server := newInternalTestServer(t, store, ps, chatprovider.ProviderAPIKeys{},
		withInternalTestServerClock(quartz.NewMock(t)),
		withInternalTestServerExperiments(codersdk.Experiments{codersdk.ExperimentForkWorkflows}))
	user, org, model := seedInternalChatDeps(t, db)
	f := &forkWorkflowFixture{db: db, ps: ps, server: server, user: user, org: org, model: model}

	ctx, cancel := context.WithCancel(chatdTestContext(t))
	defer cancel()
	parent := dbgen.Chat(t, db, database.Chat{
		OrganizationID:    org.ID,
		OwnerID:           user.ID,
		LastModelConfigID: model.ID,
		Status:            database.ChatStatusRunning,
	})
	child := dbgen.Chat(t, db, database.Chat{
		OrganizationID:    org.ID,
		OwnerID:           user.ID,
		ParentChatID:      uuid.NullUUID{UUID: parent.ID, Valid: true},
		LastModelConfigID: model.ID,
		Status:            database.ChatStatusRunning,
	})
	// The report is in place, so the step is done once the child waits.
	f.insert(t, child.ID, database.ChatMessageRoleAssistant,
		codersdk.ChatMessageToolCall("call-1", workflowReportToolName, json.RawMessage(`{"text":"hi"}`)))
	f.insert(t, child.ID, database.ChatMessageRoleTool,
		codersdk.ChatMessageToolResult("call-1", workflowReportToolName, json.RawMessage(`"Value accepted."`), false, false))
	store.arm(child.ID)

	run := &workflowRun{
		p:      server,
		id:     uuid.New(),
		parent: parent,
		seen:   map[string]int{},
		done:   make(chan struct{}),
	}
	step := &workflowStep{chatID: child.ID, title: "racing", status: stepStatusRunning}
	run.mu.Lock()
	run.steps = []*workflowStep{step}
	run.mu.Unlock()

	waited := make(chan error, 1)
	go func() { waited <- run.waitStep(ctx, step) }()
	short := testutil.Context(t, testutil.WaitShort)
	select {
	case err := <-waited:
		require.NoError(t, err)
	case <-short.Done():
		t.Fatal("waitStep missed the notification of a child that ended its turn before the wait subscribed")
	}
	require.NoError(t, store.result())
	run.mu.Lock()
	defer run.mu.Unlock()
	require.Equal(t, stepStatusReported, step.status)
	require.JSONEq(t, `{"text":"hi"}`, string(step.value))
}

// TestWorkflowAwaitStepChildKeepsDeadline checks that a step child whose
// state changes on every check does not keep the wait running past the step
// deadline: each change restarts the wait, and the restarts stop at the
// deadline so waitStep can fail the step.
func TestWorkflowAwaitStepChildKeepsDeadline(t *testing.T) {
	t.Parallel()

	db, _ := dbtestutil.NewDB(t)
	ps := pubsub.NewInMemory()
	store := &publishOnEveryCheckStore{Store: db, ps: ps}
	server := newInternalTestServer(t, store, ps, chatprovider.ProviderAPIKeys{},
		withInternalTestServerExperiments(codersdk.Experiments{codersdk.ExperimentForkWorkflows}))
	user, org, model := seedInternalChatDeps(t, db)

	ctx, cancel := context.WithCancel(chatdTestContext(t))
	defer cancel()
	parent := dbgen.Chat(t, db, database.Chat{
		OrganizationID:    org.ID,
		OwnerID:           user.ID,
		LastModelConfigID: model.ID,
		Status:            database.ChatStatusRunning,
	})
	child := dbgen.Chat(t, db, database.Chat{
		OrganizationID:    org.ID,
		OwnerID:           user.ID,
		ParentChatID:      uuid.NullUUID{UUID: parent.ID, Valid: true},
		LastModelConfigID: model.ID,
		Status:            database.ChatStatusRunning,
	})
	store.arm(child.ID)
	run := &workflowRun{
		p:      server,
		id:     uuid.New(),
		parent: parent,
		seen:   map[string]int{},
		done:   make(chan struct{}),
	}

	waited := make(chan error, 1)
	deadline := time.Now().Add(300 * time.Millisecond)
	go func() { waited <- run.awaitStepChild(ctx, child.ID, deadline) }()
	short := testutil.Context(t, testutil.WaitShort)
	select {
	case err := <-waited:
		require.ErrorIs(t, err, ErrSubagentWaitTimeout)
	case <-short.Done():
		t.Fatal("awaitStepChild kept restarting past the step deadline")
	}
	require.Greater(t, store.count(), 1, "the state changes restarted the wait")
}

// publishOnEveryCheckStore publishes a state change of the armed child on
// every completion check of it, while the child keeps running.
type publishOnEveryCheckStore struct {
	database.Store
	ps pubsub.Pubsub

	mu      sync.Mutex
	childID uuid.UUID
	checks  int
}

func (s *publishOnEveryCheckStore) arm(childID uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.childID = childID
}

func (s *publishOnEveryCheckStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checks
}

func (s *publishOnEveryCheckStore) ForkUnwrap() database.Store { return s.Store }

func (*publishOnEveryCheckStore) ForkWrap(inner forkdb.Store) forkdb.Store { return inner }

func (s *publishOnEveryCheckStore) GetChatByID(ctx context.Context, id uuid.UUID) (database.Chat, error) {
	chat, err := s.Store.GetChatByID(ctx, id)
	if err != nil || !calledFromCompletionCheck() {
		return chat, err
	}
	s.mu.Lock()
	armed := id == s.childID
	if armed {
		s.checks++
	}
	s.mu.Unlock()
	if armed {
		_ = s.ps.Publish(coderdpubsub.ChatStateUpdateChannel(id), []byte("{}"))
	}
	return chat, nil
}

// endTurnOnFirstCheckStore ends the turn of the armed child on the first
// completion check of it, and returns the row it read before, so the check
// sees a running child. That is a child that ends its turn between the read
// and the subscription.
type endTurnOnFirstCheckStore struct {
	database.Store
	ps pubsub.Pubsub

	mu      sync.Mutex
	childID uuid.UUID
	fired   bool
	err     error
}

func (s *endTurnOnFirstCheckStore) arm(childID uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.childID = childID
}

func (s *endTurnOnFirstCheckStore) result() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.fired {
		return xerrors.New("the completion check never read the child")
	}
	return s.err
}

func (s *endTurnOnFirstCheckStore) ForkUnwrap() database.Store { return s.Store }

func (*endTurnOnFirstCheckStore) ForkWrap(inner forkdb.Store) forkdb.Store { return inner }

func (s *endTurnOnFirstCheckStore) GetChatByID(ctx context.Context, id uuid.UUID) (database.Chat, error) {
	chat, err := s.Store.GetChatByID(ctx, id)
	if err != nil || !calledFromCompletionCheck() {
		return chat, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fired || id != s.childID {
		return chat, nil
	}
	s.fired = true
	if _, err := s.UpdateChatStatus(ctx, database.UpdateChatStatusParams{
		ID:     id,
		Status: database.ChatStatusWaiting,
	}); err != nil {
		s.err = err
		return chat, nil
	}
	s.err = s.ps.Publish(coderdpubsub.ChatStateUpdateChannel(id), []byte("{}"))
	return chat, nil
}

func calledFromCompletionCheck() bool {
	pc, _, _, ok := runtime.Caller(2)
	if !ok {
		return false
	}
	fn := runtime.FuncForPC(pc)
	return fn != nil && strings.HasSuffix(fn.Name(), ".checkSubagentCompletion")
}
