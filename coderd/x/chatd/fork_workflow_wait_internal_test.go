package chatd

import (
	"context"
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

// TestWorkflowWaitStepSeesFirstNotification checks that the wait of a step
// sees a child that ends its turn right after the wait first read it. The
// wait subscribes to the child's state before that read, so the state
// notification ends the wait; the mock clock never fires the fallback
// tick.
func TestWorkflowWaitStepSeesFirstNotification(t *testing.T) {
	t.Parallel()

	db, _ := dbtestutil.NewDB(t)
	ps := pubsub.NewInMemory()
	store := &endTurnOnFirstCheckStore{Store: db, ps: ps}
	server := newInternalTestServer(t, store, ps, chatprovider.ProviderAPIKeys{},
		withInternalTestServerClock(quartz.NewMock(t)),
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
	step := &workflowStep{id: child.ID, chatID: child.ID, title: "racing", status: stepStatusRunning}
	run.mu.Lock()
	run.steps = []*workflowStep{step}
	run.mu.Unlock()

	type outcome struct {
		stopped bool
		err     error
	}
	waited := make(chan outcome, 1)
	go func() {
		stopped, err := run.awaitStepChild(ctx, step, server.clock.Now().Add(time.Hour))
		waited <- outcome{stopped: stopped, err: err}
	}()
	short := testutil.Context(t, testutil.WaitShort)
	select {
	case o := <-waited:
		require.NoError(t, o.err)
		require.True(t, o.stopped, "the child stopped without a report")
	case <-short.Done():
		t.Fatal("the step wait missed the notification of a child that ended its turn after the first read")
	}
	require.NoError(t, store.result())
}

// TestWorkflowAwaitStepChildKeepsDeadline checks that a step child whose
// state changes on every check does not keep the wait running past the step
// deadline: each change makes the wait read the child again, and the wait
// still ends at the deadline so waitStep can fail the step.
func TestWorkflowAwaitStepChildKeepsDeadline(t *testing.T) {
	t.Parallel()

	db, _ := dbtestutil.NewDB(t)
	ps := pubsub.NewInMemory()
	store := &publishOnEveryCheckStore{Store: db, ps: ps}
	mClock := quartz.NewMock(t)
	server := newInternalTestServer(t, store, ps, chatprovider.ProviderAPIKeys{},
		withInternalTestServerExperiments(codersdk.Experiments{codersdk.ExperimentForkWorkflows}),
		withInternalTestServerClock(mClock))
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
	// The deadline comes before the 5 s fallback poll, so the wait timers
	// are the next clock event.
	deadline := mClock.Now().Add(time.Second)
	step := &workflowStep{id: child.ID, chatID: child.ID, title: "busy", status: stepStatusRunning}
	go func() {
		_, err := run.awaitStepChild(ctx, step, deadline)
		waited <- err
	}()
	short := testutil.Context(t, testutil.WaitShort)
	testutil.Eventually(short, t, func(context.Context) bool {
		return store.count() > 1
	}, testutil.IntervalFast, "the state changes restarted the wait")
	select {
	case err := <-waited:
		t.Fatalf("awaitStepChild returned before the step deadline: %v", err)
	default:
	}
	mClock.Advance(time.Second).MustWait(short)
	select {
	case err := <-waited:
		require.ErrorIs(t, err, ErrSubagentWaitTimeout)
	case <-short.Done():
		t.Fatal("awaitStepChild kept restarting past the step deadline")
	}
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
	return fn != nil && (strings.HasSuffix(fn.Name(), ".checkSubagentCompletion") ||
		strings.HasSuffix(fn.Name(), ".stepChildStopped"))
}
