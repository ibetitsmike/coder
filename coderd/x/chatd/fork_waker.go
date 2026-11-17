package chatd

// This file is fork code: the waker delivers the due chat wake-ups of the
// fork-chat-wakeups experiment. Each Server has one waker. It runs a pass
// over the due wake-ups when something may have made one due: a commit that
// changed a chat (a poke), the run time of the earliest wake-up (the timer),
// and a sweep every forkWakerSweepInterval that catches what the other two
// missed, such as a change on another replica. See "Wake-up delivery" in
// ARCHITECTURE.md.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/database/pubsub"
	coderdpubsub "github.com/coder/coder/v2/coderd/pubsub"
	"github.com/coder/quartz"
)

const (
	// forkWakerSweepInterval is how often the waker lists every due
	// wake-up, whatever poked it.
	forkWakerSweepInterval = time.Minute
	// forkWakerDeliveries bounds the deliveries that run at once, so a slow
	// send cannot hold up the others for long.
	forkWakerDeliveries = 8
	// forkWakerBatch is how many due wake-ups one statement lists.
	forkWakerBatch = 64
	// wakeupDeliveryTimeout bounds one delivery.
	wakeupDeliveryTimeout = 2 * time.Minute
)

// errWakeupNotDue reports a wake-up that changed, or that another
// transaction holds, since the waker listed it as due. The delivery writes
// nothing: whoever changed the wake-up decides it.
var errWakeupNotDue = xerrors.New("chat wake-up: no longer due")

// chatUpdateChannelPrefix prefixes the pubsub channel of each chat that
// chatstate publishes to after a commit that changed the chat.
var chatUpdateChannelPrefix = strings.TrimSuffix(coderdpubsub.ChatStateUpdateChannel(uuid.Nil), uuid.Nil.String())

// forkWakingPubsub pokes its waker with the chat of each chat state update
// it publishes. chatstate publishes those after the outermost commit, so a
// poke never runs before the change it reports is visible.
type forkWakingPubsub struct {
	pubsub.Pubsub
	waker *forkWaker
}

// withForkWaker wraps the pubsub of a Server so the chat state changes of
// the Server poke its waker. The wrapper only forwards until the waker runs.
func withForkWaker(ps pubsub.Pubsub) pubsub.Pubsub {
	if ps == nil {
		return nil
	}
	return &forkWakingPubsub{Pubsub: ps, waker: newForkWaker()}
}

func (p *forkWakingPubsub) Publish(event string, message []byte) error {
	err := p.Pubsub.Publish(event, message)
	if id, ok := strings.CutPrefix(event, chatUpdateChannelPrefix); ok {
		if chatID, parseErr := uuid.Parse(id); parseErr == nil {
			p.waker.poke(chatID)
		}
	}
	return err
}

// forkWaker returns the waker of p, or nil when p was built without one.
func (p *Server) forkWaker() *forkWaker {
	if ps, ok := p.pubsub.(*forkWakingPubsub); ok {
		return ps.waker
	}
	return nil
}

// ForkPokeChatWakeups asks the waker of p for a pass over the wake-ups of
// chatID, after a commit that may have made one due outside chatstate, such
// as a process watch that a workspace agent fired.
func (p *Server) ForkPokeChatWakeups(chatID uuid.UUID) {
	p.forkWaker().poke(chatID)
}

// forkWaker delivers due wake-ups. A pass lists the due wake-ups of one chat
// (after a poke) or of every chat (a full pass, after the timer or the
// sweep), and starts a delivery for each, at most one per chat at a time.
type forkWaker struct {
	// pokes and wake carry a signal to the loop. A poke records its chat in
	// pending, the timer sets full.
	pokes chan struct{}
	wake  chan struct{}

	// Set by bind.
	server  *Server
	store   database.Store
	clock   quartz.Clock
	logger  slog.Logger
	deliver func(ctx context.Context, fdb forkdb.Store, wakeup forkdb.ChatWakeup, now time.Time) error
	slots   chan struct{}
	wg      sync.WaitGroup

	mu      sync.Mutex
	started bool
	running bool
	pending map[uuid.UUID]struct{}
	full    bool
	// skipped holds the chats whose due wake-ups a pass skipped because
	// every slot was taken, and skipOrder the same chats in the order they
	// were skipped. A freed slot goes to the chat that waited longest.
	skipped   map[uuid.UUID]struct{}
	skipOrder []uuid.UUID
	// inflight holds the chats with a running delivery. The value records
	// whether a pass found more for the chat meanwhile.
	inflight map[uuid.UUID]bool
	timer    *quartz.Timer
	timerAt  time.Time
}

func newForkWaker() *forkWaker {
	return &forkWaker{
		pokes:    make(chan struct{}, 1),
		wake:     make(chan struct{}, 1),
		slots:    make(chan struct{}, forkWakerDeliveries),
		pending:  make(map[uuid.UUID]struct{}),
		skipped:  make(map[uuid.UUID]struct{}),
		inflight: make(map[uuid.UUID]bool),
	}
}

func (k *forkWaker) bind(server *Server, store database.Store, clock quartz.Clock, logger slog.Logger) {
	k.server, k.store, k.clock, k.logger = server, store, clock, logger
	if k.deliver == nil {
		k.deliver = server.deliverWakeupSafely
	}
}

// poke asks for a pass over the wake-ups of chatID. It does nothing while
// the waker does not run.
func (k *forkWaker) poke(chatID uuid.UUID) {
	if k == nil {
		return
	}
	k.mu.Lock()
	if !k.running {
		k.mu.Unlock()
		return
	}
	k.pending[chatID] = struct{}{}
	k.mu.Unlock()
	select {
	case k.pokes <- struct{}{}:
	default:
	}
}

// run delivers due wake-ups until ctx ends, then waits for the running
// deliveries. One run is active at a time: a call while another runs returns
// at once, and a call after the last run returned starts the waker again.
func (k *forkWaker) run(ctx context.Context, server *Server, store database.Store, clock quartz.Clock, logger slog.Logger) {
	k.mu.Lock()
	if k.started {
		k.mu.Unlock()
		return
	}
	k.started = true
	k.bind(server, store, clock, logger)
	k.running = true
	k.full = true
	k.mu.Unlock()
	defer func() {
		k.mu.Lock()
		k.running = false
		if k.timer != nil {
			k.timer.Stop("chatd", "waker", "timer")
			k.timer = nil
		}
		k.timerAt = time.Time{}
		k.mu.Unlock()
		k.wg.Wait()
		k.mu.Lock()
		k.started = false
		k.pending = make(map[uuid.UUID]struct{})
		k.skipped, k.skipOrder = make(map[uuid.UUID]struct{}), nil
		k.mu.Unlock()
	}()

	//nolint:gocritic // The waker delivers the wake-ups of every chat.
	ctx = dbauthz.AsChatd(ctx)
	fdb, err := forkdb.From(store)
	if err != nil {
		k.logger.Warn(ctx, "chat waker fork queries unavailable", slogError(err))
		return
	}
	sweep := clock.NewTicker(forkWakerSweepInterval, "chatd", "waker", "sweep")
	defer sweep.Stop("chatd", "waker", "sweep")
	processSweep := clock.NewTicker(processSweepInterval, "chatd", "waker", "process-sweep")
	defer processSweep.Stop("chatd", "waker", "process-sweep")
	for {
		k.drain(ctx, fdb)
		select {
		case <-ctx.Done():
			return
		case <-k.pokes:
		case <-k.wake:
		case <-sweep.C:
			k.mu.Lock()
			k.full = true
			k.mu.Unlock()
		case <-processSweep.C:
			k.sweepProcessWatches(ctx, fdb)
		}
	}
}

// drain runs the passes that the signals since the last drain asked for. A
// full pass covers every poked chat. The free slots go first to the skipped
// chats, the one that waited longest first, and a chat poked while others
// wait joins the end of their queue. While every slot is taken, a poked chat
// is recorded as skipped with no query. So a freed slot costs about one
// pass, however many chats wait, and fresh pokes cannot postpone a skipped
// chat.
func (k *forkWaker) drain(ctx context.Context, fdb forkdb.Store) {
	k.mu.Lock()
	full, pending := k.full, k.pending
	k.full, k.pending = false, make(map[uuid.UUID]struct{})
	if !full && len(k.skipOrder) > 0 {
		for chatID := range pending {
			k.skipLocked(chatID)
		}
		pending = nil
	}
	k.mu.Unlock()
	now := dbtime.Time(k.clock.Now("chatd", "waker")).UTC()
	k.serveSkipped(ctx, fdb, now)
	if full {
		upcoming, ok := k.pass(ctx, fdb, uuid.NullUUID{}, now)
		if ok {
			k.setTimer(upcoming)
		}
	} else {
		for chatID := range pending {
			if ctx.Err() != nil {
				return
			}
			if k.skipWhileFull(chatID) {
				continue
			}
			k.chatPass(ctx, fdb, chatID, now)
		}
	}
	k.serveSkipped(ctx, fdb, now)
}

// serveSkipped runs a pass over the skipped chats, oldest first, while a
// slot is free.
func (k *forkWaker) serveSkipped(ctx context.Context, fdb forkdb.Store, now time.Time) {
	for ctx.Err() == nil {
		chatID, ok := k.nextSkipped()
		if !ok {
			return
		}
		k.chatPass(ctx, fdb, chatID, now)
	}
}

// chatPass runs a pass over the wake-ups of chatID.
func (k *forkWaker) chatPass(ctx context.Context, fdb forkdb.Store, chatID uuid.UUID, now time.Time) {
	upcoming, ok := k.pass(ctx, fdb, uuid.NullUUID{UUID: chatID, Valid: true}, now)
	if ok {
		k.advanceTimer(upcoming)
	}
}

// skipWhileFull records chatID as skipped and returns true when every slot
// is taken.
func (k *forkWaker) skipWhileFull(chatID uuid.UUID) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	if len(k.slots) < cap(k.slots) {
		return false
	}
	k.skipLocked(chatID)
	return true
}

// skipLocked records chatID as skipped, once.
func (k *forkWaker) skipLocked(chatID uuid.UUID) {
	if _, ok := k.skipped[chatID]; ok {
		return
	}
	k.skipped[chatID] = struct{}{}
	k.skipOrder = append(k.skipOrder, chatID)
}

// nextSkipped removes and returns the chat that was skipped first, while a
// slot is free. Only the loop takes slots, so the slot is still free when
// the pass over that chat dispatches.
func (k *forkWaker) nextSkipped() (uuid.UUID, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if len(k.skipOrder) == 0 || len(k.slots) == cap(k.slots) {
		return uuid.UUID{}, false
	}
	chatID := k.skipOrder[0]
	k.skipOrder = k.skipOrder[1:]
	if len(k.skipOrder) == 0 {
		k.skipOrder = nil
	}
	delete(k.skipped, chatID)
	return chatID, true
}

// pass starts a delivery for each wake-up of chatID, or of every chat, that
// is due at now. It returns the run time of the earliest active wake-up that
// is not due, and false when the list failed.
func (k *forkWaker) pass(ctx context.Context, fdb forkdb.Store, chatID uuid.NullUUID, now time.Time) (time.Time, bool) {
	start := k.clock.Now("chatd", "waker")
	arg := forkdb.ListDueChatWakeupsParams{ChatID: chatID, Now: now, LimitCount: forkWakerBatch}
	var upcoming time.Time
	for {
		rows, err := fdb.ListDueChatWakeups(ctx, arg)
		if err != nil {
			if ctx.Err() == nil {
				k.logger.Warn(ctx, "chat waker could not list due wake-ups", slogError(err))
			}
			return time.Time{}, false
		}
		due := 0
		for _, row := range rows {
			if !row.Due {
				upcoming = row.ChatWakeup.NextRunAt.Time
				continue
			}
			due++
			arg.AfterRunAt, arg.AfterID = row.ChatWakeup.NextRunAt.Time, row.ChatWakeup.ID
			k.dispatch(ctx, fdb, row.ChatWakeup, now, start)
		}
		if due < forkWakerBatch || ctx.Err() != nil {
			return upcoming, true
		}
	}
}

// dispatch starts the delivery of wakeup in its own goroutine when a slot is
// free. A chat with a running delivery is marked, and the end of that
// delivery pokes it again. When every slot is taken, dispatch skips the
// wake-up and records its chat, and the next drain after a slot frees runs
// a pass over that chat. So the loop never waits for a slot, and pokes, the
// timer and the sweep keep running while slow deliveries hold every slot.
func (k *forkWaker) dispatch(ctx context.Context, fdb forkdb.Store, wakeup forkdb.ChatWakeup, now, start time.Time) {
	k.mu.Lock()
	if _, ok := k.inflight[wakeup.ChatID]; ok {
		k.inflight[wakeup.ChatID] = true
		k.mu.Unlock()
		return
	}
	select {
	case k.slots <- struct{}{}:
	default:
		k.skipLocked(wakeup.ChatID)
		k.mu.Unlock()
		return
	}
	k.inflight[wakeup.ChatID] = false
	k.mu.Unlock()

	k.wg.Go(func() {
		// The delivery's own time includes the time since the pass started.
		deliverNow := now.Add(k.clock.Since(start, "chatd", "waker"))
		deliverCtx, cancel := context.WithTimeout(ctx, wakeupDeliveryTimeout)
		err := k.deliver(deliverCtx, fdb, wakeup, deliverNow)
		cancel()
		switch {
		case err == nil, ctx.Err() != nil:
		case errors.Is(err, errWakeupChatBusy):
			// Nothing was written, and the end of the turn pokes the chat.
		case errors.Is(err, errWakeupNotDue):
			k.logger.Debug(ctx, "chat wake-up changed before its delivery",
				slog.F("wakeup_id", wakeup.ID),
				slog.F("chat_id", wakeup.ChatID),
			)
		default:
			k.logger.Warn(ctx, "chat wake-up delivery failed",
				slog.F("wakeup_id", wakeup.ID),
				slog.F("chat_id", wakeup.ChatID),
				slog.Error(err),
			)
		}
		// The slot and the skipped chats change under one lock, so a pass
		// that skips a wake-up either finds this slot free or records a chat
		// that this delivery signals the loop for.
		k.mu.Lock()
		<-k.slots
		again := k.inflight[wakeup.ChatID]
		delete(k.inflight, wakeup.ChatID)
		refill := len(k.skipped) > 0
		k.mu.Unlock()
		if refill {
			select {
			case k.pokes <- struct{}{}:
			default:
			}
		}
		// A delivery that ended its occurrence may have set the next run
		// time, so the chat gets a pass that moves the timer. A failure or
		// a busy chat waits for the sweep or the end of the turn, unless a
		// pass found more meanwhile.
		if err == nil || again {
			k.poke(wakeup.ChatID)
		}
	})
}

// advanceTimer moves the timer to the run time at when that is earlier. A
// pass over one chat calls it, since it did not see the other chats.
func (k *forkWaker) advanceTimer(at time.Time) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if at.IsZero() || (!k.timerAt.IsZero() && !at.Before(k.timerAt)) {
		return
	}
	k.armTimerLocked(at)
}

// setTimer arms the timer for the run time at, the earliest one that a full
// pass found, or none when at is zero. A full pass saw every chat, so it
// replaces the timer.
func (k *forkWaker) setTimer(at time.Time) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.armTimerLocked(at)
}

func (k *forkWaker) armTimerLocked(at time.Time) {
	if !k.running {
		return
	}
	if k.timer != nil {
		k.timer.Stop("chatd", "waker", "timer")
		k.timer = nil
	}
	k.timerAt = at
	if at.IsZero() {
		return
	}
	k.timer = k.clock.AfterFunc(at.Sub(k.clock.Now("chatd", "waker")), func() {
		k.mu.Lock()
		k.full = true
		k.timerAt = time.Time{}
		k.mu.Unlock()
		select {
		case k.wake <- struct{}{}:
		default:
		}
	}, "chatd", "waker", "timer")
}

// deliverWakeupSafely delivers wakeup and turns a panic into an error.
func (p *Server) deliverWakeupSafely(ctx context.Context, fdb forkdb.Store, wakeup forkdb.ChatWakeup, now time.Time) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = xerrors.Errorf("chat wake-up panic: %v", recovered)
		}
	}()
	return p.deliverWakeup(ctx, fdb, wakeup, now)
}
