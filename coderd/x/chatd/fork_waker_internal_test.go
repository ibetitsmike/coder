package chatd

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/x/chatd/chatstate"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/quartz"
)

// wakeupOnce runs one full pass of a waker of the worker's server at now and
// waits for its deliveries, one at a time.
func (w *chatWorker) wakeupOnce(ctx context.Context, now time.Time) {
	runWakerPass(ctx, w.server, w.opts.Store, w.opts.Clock, now, nil)
}

// runWakerPass runs one full pass of a new waker of server at now with
// deliver, or the real delivery when deliver is nil, and waits for its
// deliveries. Like the loop of a running waker, it then passes over each
// chat that a delivery poked, until none is left.
func runWakerPass(ctx context.Context, server *Server, store database.Store, clock quartz.Clock, now time.Time, deliver func(context.Context, forkdb.Store, forkdb.ChatWakeup, time.Time) error) {
	k := newForkWaker()
	k.slots = make(chan struct{}, 1)
	k.deliver = deliver
	k.bind(server, store, clock, server.logger)
	fdb, err := forkdb.From(store)
	if err != nil {
		panic(err)
	}
	k.mu.Lock()
	k.running = true
	k.mu.Unlock()
	//nolint:gocritic // The waker delivers the wake-ups of every chat.
	ctx = dbauthz.AsChatd(ctx)
	k.pass(ctx, fdb, uuid.NullUUID{}, now)
	k.wg.Wait()
	for range 10 {
		k.mu.Lock()
		pending := k.pending
		k.pending = make(map[uuid.UUID]struct{})
		k.mu.Unlock()
		if len(pending) == 0 {
			return
		}
		for chatID := range pending {
			k.pass(ctx, fdb, uuid.NullUUID{UUID: chatID, Valid: true}, now)
			k.wg.Wait()
		}
	}
	panic("runWakerPass: the deliveries keep poking")
}

// countingForkStore counts the statements that list due wake-ups, after
// each one returns.
type countingForkStore struct {
	forkdb.Store
	lists atomic.Int64
}

func (s *countingForkStore) ListDueChatWakeups(ctx context.Context, arg forkdb.ListDueChatWakeupsParams) ([]forkdb.ListDueChatWakeupsRow, error) {
	rows, err := s.Store.ListDueChatWakeups(ctx, arg)
	s.lists.Add(1)
	return rows, err
}

// forkStoreOverride is a store whose fork queries are fdb.
type forkStoreOverride struct {
	database.Store
	fdb forkdb.Store
}

func (s forkStoreOverride) ForkUnwrap() database.Store         { return s.Store }
func (s forkStoreOverride) ForkWrap(forkdb.Store) forkdb.Store { return s.fdb }

// wakerHarness runs the waker of a real Server on a mock clock.
type wakerHarness struct {
	server *Server
	waker  *forkWaker
	clock  *quartz.Mock
	fdb    *countingForkStore
}

// startWaker runs the waker of a new Server of f. mutate changes the harness
// before the waker runs. startWaker returns once the first full pass listed the
// due wake-ups.
func (f *workerTestFixture) startWaker(t *testing.T, mutate func(*wakerHarness)) *wakerHarness {
	t.Helper()
	ctx := testutil.Context(t, testutil.WaitLong)
	inner, err := forkdb.From(f.db)
	require.NoError(t, err)
	h := &wakerHarness{
		server: newUnstartedServer(t, f.pubsub, f.db),
		clock:  quartz.NewMock(t),
		fdb:    &countingForkStore{Store: inner},
	}
	h.clock.Set(wakeupTestSlot.Add(time.Second))
	h.waker = h.server.forkWaker()
	require.NotNil(t, h.waker, "New wraps the pubsub with a waker")
	if mutate != nil {
		mutate(h)
	}
	sweepTrap := h.clock.Trap().NewTicker("chatd", "waker", "sweep")
	defer sweepTrap.Close()
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.waker.run(runCtx, h.server, forkStoreOverride{Store: f.db, fdb: h.fdb}, h.clock, testutil.Logger(t))
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	sweepTrap.MustWait(ctx).MustRelease(ctx)
	testutil.Eventually(ctx, t, func(context.Context) bool {
		return h.fdb.lists.Load() >= 1
	}, testutil.IntervalFast, "the first full pass must list the due wake-ups")
	return h
}

// finishTurnThrough ends the turn of chatID through publisher, the way a
// runner of the server does.
func finishTurnThrough(t *testing.T, f *workerTestFixture, publisher chatstate.Publisher, chatID uuid.UUID) {
	t.Helper()
	ctx := testutil.Context(t, testutil.WaitShort)
	require.NoError(t, chatstate.NewChatMachine(f.db, publisher, chatID).Update(ctx, func(tx *chatstate.Tx, _ database.Store) error {
		_, err := tx.FinishTurn(chatstate.FinishTurnInput{})
		return err
	}))
}

// A due wake-up of a busy chat is delivered by the commit that ends the
// turn, with no clock advance.
func TestWakerDeliversWhenTurnEnds(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	chat := f.createWakeupChat(t)
	forceExecutionState(t, f, chat.ID, database.ChatStatusRunning, false)
	wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
		Kind:            forkdb.ChatWakeupKindInterval,
		IntervalSeconds: sql.NullInt32{Int32: 60, Valid: true},
	})
	h := f.startWaker(t, nil)
	require.Zero(t, f.countPromptMessages(t, chat, wakeup.Prompt), "a busy chat gets nothing")

	finishTurnThrough(t, f, h.server.pubsub, chat.ID)
	testutil.Eventually(ctx, t, func(context.Context) bool {
		return f.countPromptMessages(t, chat, wakeup.Prompt) == 1
	}, testutil.IntervalFast, "the end of the turn must deliver the wake-up")
	require.Equal(t, []string{"delivered"}, f.wakeupRunStatuses(t, wakeup.ID))
}

// A wake-up that turns due later is delivered by the timer at its run time.
func TestWakerTimerDeliversAtRunTime(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	chat := f.createWakeupChat(t)
	wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{Kind: forkdb.ChatWakeupKindDelay})
	runAt := wakeupTestSlot.Add(10 * time.Second)
	_, err := f.sqlDB.ExecContext(ctx, "UPDATE chat_wakeups SET next_run_at = $2 WHERE id = $1", wakeup.ID, runAt)
	require.NoError(t, err)
	var timerTrap *quartz.Trap
	h := f.startWaker(t, func(h *wakerHarness) {
		timerTrap = h.clock.Trap().AfterFunc("chatd", "waker", "timer")
	})
	defer timerTrap.Close()
	// The first pass arms the timer for the run time.
	call := timerTrap.MustWait(ctx)
	require.Equal(t, runAt.Sub(h.clock.Now()), call.Duration)
	call.MustRelease(ctx)
	require.Zero(t, f.countPromptMessages(t, chat, wakeup.Prompt))

	h.clock.Advance(runAt.Sub(h.clock.Now())).MustWait(ctx)
	testutil.Eventually(ctx, t, func(context.Context) bool {
		return f.countPromptMessages(t, chat, wakeup.Prompt) == 1
	}, testutil.IntervalFast, "the timer must deliver the wake-up at its run time")
}

// Without pokes, the sweep still delivers within one sweep interval.
func TestWakerSweepDeliversWithoutPokes(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	chat := f.createWakeupChat(t)
	forceExecutionState(t, f, chat.ID, database.ChatStatusRunning, false)
	wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{Kind: forkdb.ChatWakeupKindDelay})
	h := f.startWaker(t, func(h *wakerHarness) { h.waker.pokes = nil })

	finishTurnThrough(t, f, h.server.pubsub, chat.ID)
	require.EqualValues(t, 1, h.fdb.lists.Load(), "a poke cannot reach the loop")
	require.Zero(t, f.countPromptMessages(t, chat, wakeup.Prompt))

	h.clock.Advance(forkWakerSweepInterval).MustWait(ctx)
	testutil.Eventually(ctx, t, func(context.Context) bool {
		return f.countPromptMessages(t, chat, wakeup.Prompt) == 1
	}, testutil.IntervalFast, "the sweep must deliver the wake-up")
}

// Two wakers on one database that both list a wake-up as due deliver it
// once.
func TestWakerTwoWakersDeliverOnce(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	chat := f.createWakeupChat(t)
	wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
		Kind:            forkdb.ChatWakeupKindInterval,
		IntervalSeconds: sql.NullInt32{Int32: 60, Valid: true},
	})

	// Both deliveries start only after both wakers listed the wake-up.
	var listed sync.WaitGroup
	listed.Add(2)
	var wg sync.WaitGroup
	for range 2 {
		server := newUnstartedServer(t, f.pubsub, f.db)
		wg.Go(func() {
			runWakerPass(ctx, server, f.db, quartz.NewReal(), wakeupTestSlot.Add(time.Second), func(ctx context.Context, fdb forkdb.Store, w forkdb.ChatWakeup, now time.Time) error {
				listed.Done()
				listed.Wait()
				return server.deliverWakeupSafely(ctx, fdb, w, now)
			})
		})
	}
	wg.Wait()

	require.Equal(t, 1, f.countPromptMessages(t, chat, wakeup.Prompt))
	queued, err := f.db.GetChatQueuedMessages(ctx, chat.ID)
	require.NoError(t, err)
	require.Empty(t, queued)
	require.Equal(t, []string{"delivered"}, f.wakeupRunStatuses(t, wakeup.ID))
	got := f.wakeup(t, wakeup.ID)
	require.EqualValues(t, 1, got.RunCount)
	require.True(t, wakeupTestSlot.Add(time.Minute).Equal(got.NextRunAt.Time), "one occurrence: %s", got.NextRunAt.Time)
}

// A wake-up paused after the waker listed it and before its delivery
// transaction delivers nothing.
func TestWakerPausedBeforeDelivery(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	chat := f.createWakeupChat(t)
	wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
		Kind:            forkdb.ChatWakeupKindInterval,
		IntervalSeconds: sql.NullInt32{Int32: 60, Valid: true},
	})
	server := newUnstartedServer(t, f.pubsub, f.db)

	var deliverErr error
	runWakerPass(ctx, server, f.db, quartz.NewReal(), wakeupTestSlot.Add(time.Second), func(ctx context.Context, fdb forkdb.Store, w forkdb.ChatWakeup, now time.Time) error {
		_, after, err := server.SetChatWakeupStatus(ctx, chat, w.ID, forkdb.ChatWakeupStatusPaused)
		if !assert.NoError(t, err) {
			return err
		}
		assert.Equal(t, forkdb.ChatWakeupStatusPaused, after.Status)
		deliverErr = server.deliverWakeupSafely(ctx, fdb, w, now)
		return deliverErr
	})

	require.ErrorIs(t, deliverErr, errWakeupNotDue)
	require.Zero(t, f.countPromptMessages(t, chat, wakeup.Prompt))
	require.Empty(t, f.wakeupRunStatuses(t, wakeup.ID))
	got := f.wakeup(t, wakeup.ID)
	require.Equal(t, forkdb.ChatWakeupStatusPaused, got.Status)
	require.Zero(t, got.RunCount)
}

// A delivery that finds its chat busy writes nothing and does not poke the
// chat again, so a chat that turns busy between the list and the send cannot
// spin the waker. The end of the turn pokes it instead.
func TestWakerBusyDeliveryDoesNotPoke(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	chat := f.createWakeupChat(t)
	wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{Kind: forkdb.ChatWakeupKindDelay})
	server := newUnstartedServer(t, f.pubsub, f.db)

	var deliveries atomic.Int64
	runWakerPass(ctx, server, f.db, quartz.NewReal(), wakeupTestSlot.Add(time.Second), func(ctx context.Context, fdb forkdb.Store, w forkdb.ChatWakeup, now time.Time) error {
		if deliveries.Add(1) > 1 {
			return server.deliverWakeupSafely(ctx, fdb, w, now)
		}
		// The chat is busy for the delivery and idle again right after,
		// with no commit that pokes. Only a poke by the delivery itself
		// could start a second one.
		forceExecutionState(t, f, chat.ID, database.ChatStatusRunning, false)
		err := server.deliverWakeupSafely(ctx, fdb, w, now)
		forceExecutionState(t, f, chat.ID, database.ChatStatusWaiting, false)
		return err
	})
	require.EqualValues(t, 1, deliveries.Load())
	require.Zero(t, f.countPromptMessages(t, chat, wakeup.Prompt))
}

// Wake-ups that wait for their busy chats cost one statement per sweep and
// no delivery.
func TestWakerWaitingWakeupsCostOneStatementPerSweep(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	for range 50 {
		chat := f.createWakeupChat(t)
		forceExecutionState(t, f, chat.ID, database.ChatStatusRunning, false)
		f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{
			Kind:            forkdb.ChatWakeupKindInterval,
			IntervalSeconds: sql.NullInt32{Int32: 60, Valid: true},
		})
	}
	var deliveries atomic.Int64
	h := f.startWaker(t, func(h *wakerHarness) {
		h.waker.deliver = func(context.Context, forkdb.Store, forkdb.ChatWakeup, time.Time) error {
			deliveries.Add(1)
			return nil
		}
	})
	start := h.fdb.lists.Load()
	require.EqualValues(t, 1, start)

	for i := range int64(10) {
		h.clock.Advance(forkWakerSweepInterval).MustWait(ctx)
		testutil.Eventually(ctx, t, func(context.Context) bool {
			return h.fdb.lists.Load() == start+i+1
		}, testutil.IntervalFast, "each sweep lists once")
	}
	require.EqualValues(t, 10, h.fdb.lists.Load()-start, "10 sweeps, 10 statements")
	require.Zero(t, deliveries.Load())
}

// The started worker runs the waker of its server when the experiment is
// enabled.
func TestWakerRunsInStartedWorker(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	mClock := quartz.NewMock(t)
	mClock.Set(wakeupTestSlot.Add(time.Second))
	first := f.createWakeupChat(t)
	firstWakeup := f.insertWakeup(t, first, forkdb.InsertChatWakeupParams{Kind: forkdb.ChatWakeupKindDelay})

	server := newUnstartedServer(t, f.pubsub, f.db)
	server.experiments = codersdk.Experiments{codersdk.ExperimentForkChatWakeups}
	opts := testOptions(t, f, nil)
	opts.Clock = mClock
	opts.Pubsub = server.pubsub
	worker, err := newChatWorker(server, opts)
	require.NoError(t, err)
	sweepTrap := mClock.Trap().NewTicker("chatd", "waker", "sweep")
	defer sweepTrap.Close()
	require.NoError(t, worker.Start(context.Background()))
	t.Cleanup(func() { require.NoError(t, worker.Close()) })

	sweepTrap.MustWait(ctx).MustRelease(ctx)
	testutil.Eventually(ctx, t, func(context.Context) bool {
		return f.countPromptMessages(t, first, firstWakeup.Prompt) == 1
	}, testutil.IntervalFast, "the first pass must deliver the due wake-up")

	// A wake-up written without a poke waits for the sweep.
	second := f.createWakeupChat(t)
	secondWakeup := f.insertWakeup(t, second, forkdb.InsertChatWakeupParams{Kind: forkdb.ChatWakeupKindDelay})
	mClock.Advance(forkWakerSweepInterval).MustWait(ctx)
	testutil.Eventually(ctx, t, func(context.Context) bool {
		return f.countPromptMessages(t, second, secondWakeup.Prompt) == 1
	}, testutil.IntervalFast, "the sweep must deliver the second wake-up")
}
