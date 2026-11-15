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
	// read of a background process or a pull request cannot hold up the
	// others for long.
	forkWakerDeliveries = 8
	// forkWakerBatch is how many due wake-ups one statement lists.
	forkWakerBatch = 64
	// wakeupDeliveryTimeout bounds one delivery, including the reads of an
	// events wake-up.
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
// deliveries. A waker runs once.
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
		}
		k.mu.Unlock()
		k.wg.Wait()
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
		}
	}
}

// drain runs the passes that the signals since the last drain asked for. A
// full pass covers every poked chat.
func (k *forkWaker) drain(ctx context.Context, fdb forkdb.Store) {
	k.mu.Lock()
	full, pending := k.full, k.pending
	k.full, k.pending = false, make(map[uuid.UUID]struct{})
	k.mu.Unlock()
	now := dbtime.Time(k.clock.Now("chatd", "waker")).UTC()
	if full {
		upcoming, ok := k.pass(ctx, fdb, uuid.NullUUID{}, now)
		if ok {
			k.setTimer(upcoming)
		}
		return
	}
	for chatID := range pending {
		if ctx.Err() != nil {
			return
		}
		upcoming, ok := k.pass(ctx, fdb, uuid.NullUUID{UUID: chatID, Valid: true}, now)
		if ok {
			k.advanceTimer(upcoming)
		}
	}
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

// dispatch starts the delivery of wakeup in its own goroutine once a slot is
// free. A chat with a running delivery is marked, and the end of that
// delivery pokes it again.
func (k *forkWaker) dispatch(ctx context.Context, fdb forkdb.Store, wakeup forkdb.ChatWakeup, now, start time.Time) {
	k.mu.Lock()
	if _, ok := k.inflight[wakeup.ChatID]; ok {
		k.inflight[wakeup.ChatID] = true
		k.mu.Unlock()
		return
	}
	k.inflight[wakeup.ChatID] = false
	k.mu.Unlock()

	select {
	case k.slots <- struct{}{}:
	case <-ctx.Done():
		k.mu.Lock()
		delete(k.inflight, wakeup.ChatID)
		k.mu.Unlock()
		return
	}
	k.wg.Go(func() {
		defer func() { <-k.slots }()
		// The delivery's own time includes the time it waited for a slot.
		deliverNow := now.Add(k.clock.Since(start, "chatd", "waker"))
		deliverCtx, cancel := context.WithTimeout(withWakeupDeadline(ctx, time.Now().Add(wakeupDeliveryTimeout)), wakeupDeliveryTimeout)
		err := k.deliver(deliverCtx, fdb, wakeup, deliverNow)
		cancel()
		switch {
		case err == nil, ctx.Err() != nil:
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
		k.mu.Lock()
		again := k.inflight[wakeup.ChatID]
		delete(k.inflight, wakeup.ChatID)
		k.mu.Unlock()
		// A delivery that ended its occurrence may have set the next run
		// time, so the chat gets a pass that moves the timer. A failure
		// waits for the sweep, unless a pass found more meanwhile.
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

type wakeupDeadlineKey struct{}

// withWakeupDeadline records the deadline of a delivery in ctx. The deadline
// of ctx itself may come from elsewhere, so wakeupReadFits reads this one.
func withWakeupDeadline(ctx context.Context, deadline time.Time) context.Context {
	return context.WithValue(ctx, wakeupDeadlineKey{}, deadline)
}

// wakeupReadFits reports whether the delivery in ctx has time left for a read
// that may take up to timeout and for what follows it. A ctx without a
// delivery deadline always has time.
func wakeupReadFits(ctx context.Context, timeout time.Duration) bool {
	deadline, ok := ctx.Value(wakeupDeadlineKey{}).(time.Time)
	return !ok || time.Until(deadline) >= 2*timeout
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
