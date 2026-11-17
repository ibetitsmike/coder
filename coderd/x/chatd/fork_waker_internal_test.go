package chatd

import (
	"context"
	"database/sql"
	"encoding/json"
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
	"github.com/coder/coder/v2/codersdk/workspacesdk"
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
// deliveries. Its waker has one slot. Like the loop of a running waker, it
// then passes over each chat that a delivery poked or that a pass skipped
// for want of a slot, until none is left.
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
		for _, chatID := range k.skipOrder {
			pending[chatID] = struct{}{}
		}
		k.skipped, k.skipOrder = make(map[uuid.UUID]struct{}), nil
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
	clock := quartz.NewMock(t)
	clock.Set(wakeupTestSlot.Add(time.Second))
	// The tools of the server read the time from a clock of their own, so
	// the tickers of the server do not run on the clock of the waker.
	serverClock := quartz.NewMock(t)
	serverClock.Set(clock.Now())
	h := &wakerHarness{
		server: newUnstartedServer(t, f.pubsub, f.db, withInternalTestServerClock(serverClock)),
		clock:  clock,
		fdb:    &countingForkStore{Store: inner},
	}
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

// A wait_for_events wake-up of an idle parent is delivered by the commit
// that ends the turn of the child it watches, with no clock advance.
func TestWakerDeliversWhenChildFinishes(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	parent := f.createWakeupChat(t)
	child := f.createChildAgent(t, parent, "child", database.ChatStatusRunning)
	expiresAt := wakeupTestSlot.Add(24 * time.Hour)
	wakeup := f.insertWakeup(t, parent, forkdb.InsertChatWakeupParams{
		Kind:      forkdb.ChatWakeupKindEvents,
		ExpiresAt: sql.NullTime{Time: expiresAt, Valid: true},
	}, childDoneEvents(child)...)
	h := f.startWaker(t, nil)
	start := h.clock.Now()
	testutil.Eventually(ctx, t, func(context.Context) bool {
		return expiresAt.Equal(f.wakeup(t, wakeup.ID).NextRunAt.Time)
	}, testutil.IntervalFast, "the first pass must move the run time to the expiry")
	require.Empty(t, f.wakeMessages(t, parent, wakeup.Prompt), "the child is running")

	finishTurnThrough(t, f, h.server.pubsub, child.ID)
	testutil.Eventually(ctx, t, func(context.Context) bool {
		return len(f.wakeMessages(t, parent, wakeup.Prompt)) == 1
	}, testutil.IntervalFast, "the end of the child's turn must deliver the wake-up")
	require.Equal(t, []string{child.ID.String()}, f.deliveredWatches(t, wakeup.ID))
	require.True(t, start.Equal(h.clock.Now()), "no clock advance")
	got := f.wakeup(t, wakeup.ID)
	require.Equal(t, forkdb.ChatWakeupStatusCompleted, got.Status)
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

// wakerIdle reports whether the waker of h runs no delivery.
func (h *wakerHarness) wakerIdle() bool {
	h.waker.mu.Lock()
	defer h.waker.mu.Unlock()
	return len(h.waker.inflight) == 0
}

// wakerTimerAt returns the run time the timer of the waker of h is armed for.
func (h *wakerHarness) wakerTimerAt() time.Time {
	h.waker.mu.Lock()
	defer h.waker.mu.Unlock()
	return h.waker.timerAt
}

// setNextRunAt moves the run time of the wake-up.
func (f *workerTestFixture) setNextRunAt(t *testing.T, wakeupID uuid.UUID, at time.Time) {
	t.Helper()
	_, err := f.sqlDB.ExecContext(testutil.Context(t, testutil.WaitShort), "UPDATE chat_wakeups SET next_run_at = $2 WHERE id = $1", wakeupID, at)
	require.NoError(t, err)
}

// While slow deliveries hold every slot, a pass skips the wake-up it has no
// slot for and still arms the timer, so the loop keeps serving pokes, the
// timer and the sweep. The first delivery to end pokes the skipped chat.
func TestWakerFullSlotsDoNotStallTheLoop(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		slow int
	}{
		{name: "SlotsFull", slow: forkWakerDeliveries},
		{name: "SlotLeft", slow: forkWakerDeliveries - 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newWorkerTestFixture(t)
			ctx := testutil.Context(t, testutil.WaitLong)
			slow := make(map[uuid.UUID]forkdb.ChatWakeup)
			var slowChats []database.Chat
			for i := range tc.slow {
				chat := f.createWakeupChat(t)
				wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{Kind: forkdb.ChatWakeupKindDelay})
				// The slow wake-ups are listed first.
				f.setNextRunAt(t, wakeup.ID, wakeupTestSlot.Add(-time.Duration(tc.slow-i)*time.Second))
				slow[wakeup.ID] = wakeup
				slowChats = append(slowChats, chat)
			}
			plain := f.createWakeupChat(t)
			plainWakeup := f.insertWakeup(t, plain, forkdb.InsertChatWakeupParams{Kind: forkdb.ChatWakeupKindDelay})
			later := f.createWakeupChat(t)
			laterWakeup := f.insertWakeup(t, later, forkdb.InsertChatWakeupParams{Kind: forkdb.ChatWakeupKindDelay})
			laterAt := wakeupTestSlot.Add(30 * time.Second)
			f.setNextRunAt(t, laterWakeup.ID, laterAt)

			release := make(chan struct{})
			var started atomic.Int64
			var timerTrap *quartz.Trap
			h := f.startWaker(t, func(h *wakerHarness) {
				timerTrap = h.clock.Trap().AfterFunc("chatd", "waker", "timer")
				h.waker.deliver = func(ctx context.Context, fdb forkdb.Store, w forkdb.ChatWakeup, now time.Time) error {
					if _, ok := slow[w.ID]; ok {
						started.Add(1)
						select {
						case <-release:
						case <-ctx.Done():
							return ctx.Err()
						}
					}
					return h.server.deliverWakeupSafely(ctx, fdb, w, now)
				}
			})
			defer timerTrap.Close()

			// The first pass ends while the slow deliveries run, and arms
			// the timer for the later wake-up.
			call := timerTrap.MustWait(ctx)
			require.Equal(t, laterAt.Sub(h.clock.Now()), call.Duration)
			call.MustRelease(ctx)
			testutil.Eventually(ctx, t, func(context.Context) bool {
				return started.Load() == int64(tc.slow)
			}, testutil.IntervalFast, "every slow delivery must start")
			if tc.slow == forkWakerDeliveries {
				require.Zero(t, f.countPromptMessages(t, plain, plainWakeup.Prompt), "no slot is free")
			} else {
				testutil.Eventually(ctx, t, func(context.Context) bool {
					return f.countPromptMessages(t, plain, plainWakeup.Prompt) == 1
				}, testutil.IntervalFast, "the free slot must deliver the wake-up")
			}

			close(release)
			testutil.Eventually(ctx, t, func(context.Context) bool {
				return f.countPromptMessages(t, plain, plainWakeup.Prompt) == 1
			}, testutil.IntervalFast, "the end of a slow delivery must deliver the skipped wake-up")
			for _, chat := range slowChats {
				testutil.Eventually(ctx, t, func(context.Context) bool {
					return len(f.wakeMessages(t, chat, "wake up ")) == 1
				}, testutil.IntervalFast, "each slow wake-up is delivered once")
			}
			require.Equal(t, []string{"delivered"}, f.wakeupRunStatuses(t, plainWakeup.ID))
			require.Zero(t, f.countPromptMessages(t, later, laterWakeup.Prompt))
		})
	}
}

// A freed slot goes to the chat that was skipped first. The others stay
// skipped with no query, so a large backlog costs about one query and one
// pass per freed slot.
func TestWakerFreedSlotCostsOneList(t *testing.T) {
	t.Parallel()
	const backlog = 20
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	var chats []uuid.UUID
	for i := range forkWakerDeliveries + backlog {
		chat := f.createWakeupChat(t)
		wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{Kind: forkdb.ChatWakeupKindDelay})
		// The wake-ups are listed in this order, and the first ones take
		// every slot.
		f.setNextRunAt(t, wakeup.ID, wakeupTestSlot.Add(-time.Duration(forkWakerDeliveries+backlog-i)*time.Second))
		chats = append(chats, chat.ID)
	}

	// Every delivery holds its slot until it gets a value from releaseOne
	// or the test ends. It reports a busy chat, so its end does not poke
	// its own chat.
	releaseOne := make(chan struct{})
	var started atomic.Int64
	var startedMu sync.Mutex
	var startedChats []uuid.UUID
	h := f.startWaker(t, func(h *wakerHarness) {
		h.waker.deliver = func(ctx context.Context, _ forkdb.Store, w forkdb.ChatWakeup, _ time.Time) error {
			startedMu.Lock()
			startedChats = append(startedChats, w.ChatID)
			startedMu.Unlock()
			started.Add(1)
			select {
			case <-releaseOne:
				return errWakeupChatBusy
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	})
	skipped := func() int {
		h.waker.mu.Lock()
		defer h.waker.mu.Unlock()
		return len(h.waker.skipped)
	}

	testutil.Eventually(ctx, t, func(context.Context) bool {
		return started.Load() == forkWakerDeliveries && skipped() == backlog
	}, testutil.IntervalFast, "the first pass must fill every slot and skip the rest")
	require.EqualValues(t, 1, h.fdb.lists.Load())

	select {
	case releaseOne <- struct{}{}:
	case <-ctx.Done():
		t.Fatal("no delivery took the release")
	}
	testutil.Eventually(ctx, t, func(context.Context) bool {
		return started.Load() == forkWakerDeliveries+1 && skipped() == backlog-1
	}, testutil.IntervalFast, "the freed slot must deliver one skipped wake-up and skip the rest again")
	require.EqualValues(t, 2, h.fdb.lists.Load(), "one freed slot, one more query")
	startedMu.Lock()
	defer startedMu.Unlock()
	require.Equal(t, chats[forkWakerDeliveries], startedChats[forkWakerDeliveries], "the chat skipped first gets the slot")
	h.waker.mu.Lock()
	defer h.waker.mu.Unlock()
	require.Equal(t, chats[forkWakerDeliveries+1:], h.waker.skipOrder, "the others wait in the order they were skipped")
	require.Empty(t, h.waker.pending, "a freed slot pokes no skipped chat")
}

// A freed slot goes to the chat that waits in the queue, not to a chat
// poked in the same drain, so fresh pokes cannot postpone a queued chat.
func TestWakerQueueBeforeFreshPokes(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	queued := f.createWakeupChat(t)
	poked := f.createWakeupChat(t)
	for _, chat := range []database.Chat{queued, poked} {
		wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{Kind: forkdb.ChatWakeupKindDelay})
		f.setNextRunAt(t, wakeup.ID, wakeupTestSlot.Add(-time.Second))
	}
	inner, err := forkdb.From(f.db)
	require.NoError(t, err)
	fdb := &countingForkStore{Store: inner}
	clock := quartz.NewMock(t)
	clock.Set(wakeupTestSlot.Add(time.Second))

	release := make(chan struct{})
	var mu sync.Mutex
	var delivered []uuid.UUID
	k := newForkWaker()
	k.deliver = func(ctx context.Context, _ forkdb.Store, w forkdb.ChatWakeup, _ time.Time) error {
		mu.Lock()
		delivered = append(delivered, w.ChatID)
		mu.Unlock()
		select {
		case <-release:
		case <-ctx.Done():
		}
		return errWakeupChatBusy
	}
	k.bind(newUnstartedServer(t, f.pubsub, f.db), f.db, clock, testutil.Logger(t))
	t.Cleanup(func() {
		close(release)
		k.wg.Wait()
	})
	// One slot is free, queued waits for it, and a poke for the other chat
	// came in with the end of the delivery that freed it.
	for range forkWakerDeliveries - 1 {
		k.slots <- struct{}{}
	}
	k.mu.Lock()
	k.running = true
	k.skipLocked(queued.ID)
	k.pending[poked.ID] = struct{}{}
	k.mu.Unlock()

	//nolint:gocritic // The waker delivers the wake-ups of every chat.
	k.drain(dbauthz.AsChatd(ctx), fdb)
	testutil.Eventually(ctx, t, func(context.Context) bool {
		mu.Lock()
		defer mu.Unlock()
		return len(delivered) > 0
	}, testutil.IntervalFast, "the free slot must start a delivery")
	mu.Lock()
	require.Equal(t, []uuid.UUID{queued.ID}, delivered, "the queued chat gets the slot")
	mu.Unlock()
	k.mu.Lock()
	defer k.mu.Unlock()
	require.Equal(t, []uuid.UUID{poked.ID}, k.skipOrder, "the poked chat waits behind the queue")
	require.EqualValues(t, 1, fdb.lists.Load(), "one free slot, one query")
}

// Each change that makes a watched child done delivers the events wake-up
// of its idle parent at once: it publishes the chat update of the child or
// pokes the child itself. Without pokes, the next full pass delivers it.
func TestWakerChildDonePathsDeliver(t *testing.T) {
	t.Parallel()
	// childWakeup gives the child an events wake-up of its own that watches
	// a running grandchild, so the child waits for events and is not done.
	// The wake-up expires at the returned time.
	childWakeup := func(t *testing.T, f *workerTestFixture, child database.Chat) (forkdb.ChatWakeup, time.Time) {
		grandchild := f.createChildAgent(t, child, "grandchild", database.ChatStatusRunning)
		expiresAt := wakeupTestSlot.Add(10 * time.Second)
		wakeup := f.insertWakeup(t, child, forkdb.InsertChatWakeupParams{
			Kind:      forkdb.ChatWakeupKindEvents,
			ExpiresAt: sql.NullTime{Time: expiresAt, Valid: true},
		}, childDoneEvents(grandchild)...)
		f.setNextRunAt(t, wakeup.ID, expiresAt)
		return wakeup, expiresAt
	}
	ask := func(t *testing.T, f *workerTestFixture, child database.Chat) {
		fdb, err := forkdb.From(f.db)
		require.NoError(t, err)
		_, err = fdb.UpsertChatDescendantQuestion(testutil.Context(t, testutil.WaitShort), forkdb.UpsertChatDescendantQuestionParams{
			ChatID: child.ID, ToolCallID: uuid.NewString(), Questions: json.RawMessage(`[{"header":"DB","question":"Which database?"}]`),
		})
		require.NoError(t, err)
	}
	type scenario struct {
		name string
		// setup makes child not done and returns the run time the first
		// pass arms the timer for, or the zero time for the parent's
		// expiry.
		setup func(t *testing.T, f *workerTestFixture, child database.Chat) time.Time
		// change makes child done.
		change func(t *testing.T, f *workerTestFixture, h *wakerHarness, parent, child database.Chat, timerAt time.Time)
	}
	for _, sc := range []scenario{
		{
			// The final poll of the child's wake-up at its expiry finds
			// nothing and completes it, and the delivery pokes the child.
			name: "ChildWakeupExpires",
			setup: func(t *testing.T, f *workerTestFixture, child database.Chat) time.Time {
				_, expiresAt := childWakeup(t, f, child)
				return expiresAt
			},
			change: func(t *testing.T, f *workerTestFixture, h *wakerHarness, _, child database.Chat, timerAt time.Time) {
				ctx := testutil.Context(t, testutil.WaitLong)
				h.clock.Advance(timerAt.Sub(h.clock.Now())).MustWait(ctx)
				// The timer only signals the loop, so wait for the
				// delivery that completes the child's wake-up.
				fdb, err := forkdb.From(f.db)
				require.NoError(t, err)
				testutil.Eventually(ctx, t, func(ctx context.Context) bool {
					wakeups, err := fdb.ListChatWakeupsByChatID(ctx, forkdb.ListChatWakeupsByChatIDParams{ChatID: child.ID})
					return err == nil && len(wakeups) == 1 && wakeups[0].Status == forkdb.ChatWakeupStatusCompleted
				}, testutil.IntervalFast, "the child's wake-up must complete at its expiry")
			},
		},
		{
			// A control cancels the child's wake-up and pokes the child.
			name: "ChildWakeupCanceled",
			setup: func(t *testing.T, f *workerTestFixture, child database.Chat) time.Time {
				_, expiresAt := childWakeup(t, f, child)
				return expiresAt
			},
			change: func(t *testing.T, f *workerTestFixture, h *wakerHarness, _, child database.Chat, _ time.Time) {
				ctx := testutil.Context(t, testutil.WaitShort)
				fdb, err := forkdb.From(f.db)
				require.NoError(t, err)
				wakeups, err := fdb.ListChatWakeupsByChatID(ctx, forkdb.ListChatWakeupsByChatIDParams{ChatID: child.ID})
				require.NoError(t, err)
				require.Len(t, wakeups, 1)
				_, after, err := h.server.SetChatWakeupStatus(ctx, child, wakeups[0].ID, forkdb.ChatWakeupStatusCanceled)
				require.NoError(t, err)
				require.Equal(t, forkdb.ChatWakeupStatusCanceled, after.Status)
			},
		},
		{
			// The answer to the child's question starts a turn of the
			// child, and the end of that turn publishes.
			name: "QuestionAnswered",
			setup: func(t *testing.T, f *workerTestFixture, child database.Chat) time.Time {
				ask(t, f, child)
				return time.Time{}
			},
			change: func(t *testing.T, f *workerTestFixture, h *wakerHarness, parent, child database.Chat, _ time.Time) {
				ctx := testutil.Context(t, testutil.WaitShort)
				_, err := h.server.SendMessage(ctx, SendMessageOptions{
					ChatID:       child.ID,
					CreatedBy:    child.OwnerID,
					Content:      []codersdk.ChatMessagePart{codersdk.ChatMessageText("Use Postgres")},
					BusyBehavior: SendMessageBusyBehaviorQueue,
				})
				require.NoError(t, err)
				got, err := f.db.GetChatByID(ctx, child.ID)
				require.NoError(t, err)
				require.Equal(t, database.ChatStatusRunning, got.Status, "the answer starts a turn")
				finishTurnThrough(t, f, h.server.pubsub, child.ID)
			},
		},
		{
			// archive_agent archives the child that waits for an answer.
			name: "ChildArchived",
			setup: func(t *testing.T, f *workerTestFixture, child database.Chat) time.Time {
				ask(t, f, child)
				return time.Time{}
			},
			change: func(t *testing.T, f *workerTestFixture, h *wakerHarness, parent, child database.Chat, _ time.Time) {
				ctx := testutil.Context(t, testutil.WaitShort)
				_, err := h.server.archiveAgent(ctx, parent, archiveAgentArgs{ChatID: child.ID.String()})
				require.NoError(t, err)
			},
		},
	} {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()
			for _, pokes := range []bool{true, false} {
				name := "Pokes"
				if !pokes {
					name = "NoPokes"
				}
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					f := newWorkerTestFixture(t)
					ctx := testutil.Context(t, testutil.WaitLong)
					parent := f.createWakeupChat(t)
					child := f.createChildAgent(t, parent, "child", database.ChatStatusWaiting)
					expiresAt := wakeupTestSlot.Add(24 * time.Hour)
					wakeup := f.insertWakeup(t, parent, forkdb.InsertChatWakeupParams{
						Kind:      forkdb.ChatWakeupKindEvents,
						ExpiresAt: sql.NullTime{Time: expiresAt, Valid: true},
					}, childDoneEvents(child)...)
					f.setNextRunAt(t, wakeup.ID, expiresAt)
					timerAt := sc.setup(t, f, child)
					if timerAt.IsZero() {
						timerAt = expiresAt
					}
					h := f.startWaker(t, func(h *wakerHarness) {
						h.server.experiments = codersdk.Experiments{codersdk.ExperimentForkChatWakeups}
						if !pokes {
							h.waker.pokes = nil
						}
					})
					testutil.Eventually(ctx, t, func(context.Context) bool {
						return timerAt.Equal(h.wakerTimerAt())
					}, testutil.IntervalFast, "the first pass must arm the timer")
					require.Empty(t, f.wakeMessages(t, parent, wakeup.Prompt), "the child is not done")

					sc.change(t, f, h, parent, child, timerAt)
					if pokes {
						testutil.Eventually(ctx, t, func(context.Context) bool {
							return len(f.wakeMessages(t, parent, wakeup.Prompt)) == 1
						}, testutil.IntervalFast, "the change must deliver the wake-up")
					} else {
						testutil.Eventually(ctx, t, func(context.Context) bool {
							return h.wakerIdle()
						}, testutil.IntervalFast, "the deliveries must end")
						require.Empty(t, f.wakeMessages(t, parent, wakeup.Prompt), "a poke cannot reach the loop")
						// The next clock event is the sweep, or the timer of
						// a wake-up the change ended.
						_, w := h.clock.AdvanceNext()
						w.MustWait(ctx)
						testutil.Eventually(ctx, t, func(context.Context) bool {
							return len(f.wakeMessages(t, parent, wakeup.Prompt)) == 1
						}, testutil.IntervalFast, "the next full pass must deliver the wake-up")
					}
					require.Equal(t, []string{child.ID.String()}, f.deliveredWatches(t, wakeup.ID))
				})
			}
		})
	}
}

// A wake-up that schedule_wakeup creates for an idle chat arms the timer at
// once. Without pokes, the sweep arms it.
func TestWakerScheduleWakeupArmsTimer(t *testing.T) {
	t.Parallel()
	for _, pokes := range []bool{true, false} {
		name := "Pokes"
		if !pokes {
			name = "NoPokes"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newWorkerTestFixture(t)
			ctx := testutil.Context(t, testutil.WaitLong)
			chat := f.createWakeupChat(t)
			var timerTrap *quartz.Trap
			h := f.startWaker(t, func(h *wakerHarness) {
				h.server.experiments = codersdk.Experiments{codersdk.ExperimentForkChatWakeups}
				timerTrap = h.clock.Trap().AfterFunc("chatd", "waker", "timer")
				if !pokes {
					h.waker.pokes = nil
				}
			})
			defer timerTrap.Close()
			tools, err := h.server.appendForkTools(ctx, nil, chat, chat.LastModelConfigID)
			require.NoError(t, err)
			runChatTodoTool(workspacesdk.WithToolCallID(ctx, uuid.New()), t, tools, scheduleWakeupToolName, scheduleWakeupArgs{Prompt: "check the build", DelaySeconds: new(120)}, false)

			want := 120 * time.Second
			if !pokes {
				require.True(t, h.wakerTimerAt().IsZero(), "a poke cannot reach the loop")
				h.clock.Advance(forkWakerSweepInterval).MustWait(ctx)
				want -= forkWakerSweepInterval
			}
			call := timerTrap.MustWait(ctx)
			require.Equal(t, want, call.Duration)
			call.MustRelease(ctx)
		})
	}
}

// A wake-up that wait_for_events creates for an idle chat whose child is done
// already is delivered at once. Without pokes, the sweep delivers it.
func TestWakerWaitForEventsDeliversDoneChild(t *testing.T) {
	t.Parallel()
	for _, pokes := range []bool{true, false} {
		name := "Pokes"
		if !pokes {
			name = "NoPokes"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newWorkerTestFixture(t)
			ctx := testutil.Context(t, testutil.WaitLong)
			parent := f.createWakeupChat(t)
			child := f.createChildAgent(t, parent, "child", database.ChatStatusWaiting)
			h := f.startWaker(t, func(h *wakerHarness) {
				h.server.experiments = codersdk.Experiments{codersdk.ExperimentForkChatWakeups}
				if !pokes {
					h.waker.pokes = nil
				}
			})
			tools, err := h.server.appendForkTools(ctx, nil, parent, parent.LastModelConfigID)
			require.NoError(t, err)
			content := runChatTodoTool(workspacesdk.WithToolCallID(ctx, uuid.New()), t, tools, waitForEventsToolName, waitForEventsArgs{ChildChatIDs: []string{child.ID.String()}}, false)
			var result waitForEventsResult
			require.NoError(t, json.Unmarshal([]byte(content), &result), content)
			prompt := f.wakeup(t, result.ID).Prompt

			start := h.clock.Now()
			if !pokes {
				require.Empty(t, f.wakeMessages(t, parent, prompt), "a poke cannot reach the loop")
				h.clock.Advance(forkWakerSweepInterval).MustWait(ctx)
			}
			testutil.Eventually(ctx, t, func(context.Context) bool {
				return len(f.wakeMessages(t, parent, prompt)) == 1
			}, testutil.IntervalFast, "the wake-up must be delivered")
			require.Equal(t, []string{child.ID.String()}, f.deliveredWatches(t, result.ID))
			if pokes {
				require.True(t, start.Equal(h.clock.Now()), "no clock advance")
			}
		})
	}
}

// A waker runs again after its run returned, and a second run while one runs
// returns at once.
func TestWakerRunsAgain(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	ctx := testutil.Context(t, testutil.WaitLong)
	clock := quartz.NewMock(t)
	clock.Set(wakeupTestSlot.Add(time.Second))
	server := newUnstartedServer(t, f.pubsub, f.db)
	k := server.forkWaker()
	require.NotNil(t, k)
	logger := testutil.Logger(t)
	sweepTrap := clock.Trap().NewTicker("chatd", "waker", "sweep")
	defer sweepTrap.Close()
	start := func() (context.CancelFunc, <-chan struct{}) {
		runCtx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			k.run(runCtx, server, f.db, clock, logger)
		}()
		return cancel, done
	}

	cancel, done := start()
	sweepTrap.MustWait(ctx).MustRelease(ctx)
	second := make(chan struct{})
	go func() {
		defer close(second)
		k.run(ctx, server, f.db, clock, logger)
	}()
	testutil.TryReceive(ctx, t, second)
	cancel()
	testutil.TryReceive(ctx, t, done)

	chat := f.createWakeupChat(t)
	wakeup := f.insertWakeup(t, chat, forkdb.InsertChatWakeupParams{Kind: forkdb.ChatWakeupKindDelay})
	cancel, done = start()
	defer func() {
		cancel()
		<-done
	}()
	sweepTrap.MustWait(ctx).MustRelease(ctx)
	testutil.Eventually(ctx, t, func(context.Context) bool {
		return f.countPromptMessages(t, chat, wakeup.Prompt) == 1
	}, testutil.IntervalFast, "the first pass of the new run must deliver the wake-up")
}
