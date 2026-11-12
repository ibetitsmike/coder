package agentcontext

import (
	"context"
	"errors"
	"time"

	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/quartz"
)

// PushRequest is the wire-format-independent payload the
// Manager hands to a Pusher. It mirrors the protobuf
// PushContextStateRequest message reserved in the RFC.
//
// Keeping the shape in plain Go lets this package compile
// without bumping the drpc proto version. The follow-up
// integration change can add a thin adapter that converts
// PushRequest to proto and back.
type PushRequest struct {
	Version       uint64
	AggregateHash [32]byte
	Resources     []Resource
	Initial       bool
	SnapshotError string
	// CapabilityGeneration is nonzero only on pushes sent inside a
	// capability negotiation window (a new push connection reset the
	// advertisement, until its first processed response; see
	// Manager.resyncNegotiating): the push is a non-persisting
	// capability probe the server stores nothing for, because the
	// settling push that ends the negotiation defines the baseline
	// chats refresh against.
	CapabilityGeneration uint64
}

// PushResponse is the wire-format-independent return value of
// a push.
type PushResponse struct {
	Accepted bool
	// ForkPluginPacks mirrors the server's push-response capability: the
	// serving coderd knows the fork-plugin-packs experiment and will
	// treat pack-contributed rows accordingly. Servers that predate the
	// capability leave it false, and pack emission stays off.
	ForkPluginPacks bool
	// ForkAgentDefinitions mirrors the fork-agent-definitions capability:
	// while it is false the resolver emits visible pack agent rows as
	// excluded, because chatd drops every subagent row and the rows would
	// only occupy snapshot caps. Servers that predate the capability
	// leave it false.
	ForkAgentDefinitions bool
	// ForkGlobRules mirrors the fork-glob-rules capability: glob-scoped
	// rules are pushed only while it is set, because a server that
	// predates it would inject them on every turn.
	ForkGlobRules bool
}

// Pusher delivers snapshots to coderd. Concrete implementations
// wrap a drpc client (Agent API v2.10 and later) or, in tests,
// a recording in-memory fake.
//
// PushContextState must respect ctx cancellation; the Manager
// retries on transient errors with backoff but stops on
// ErrPushUnimplemented.
type Pusher interface {
	PushContextState(ctx context.Context, req *PushRequest) (*PushResponse, error)
}

// ErrPushUnimplemented signals that the coderd peer does not
// implement PushContextState. RunPush stops pushing for the
// remainder of the connection.
var ErrPushUnimplemented = xerrors.New("agentcontext: PushContextState unimplemented")

// Default backoff timings for pushWithRetry. Exposed as named
// constants (rather than inline literals) so godoc shows them
// and a second push loop, if it ever appears, can reuse them.
const (
	DefaultPushInitialBackoff = 250 * time.Millisecond
	DefaultPushMaxBackoff     = 30 * time.Second
)

// PushOptions parameterizes RunPush.
type PushOptions struct {
	// Logger receives push success/failure diagnostics.
	Logger slog.Logger
	// InitialBackoff is the wait before the first retry.
	// Default 250ms.
	InitialBackoff time.Duration
	// MaxBackoff caps the retry wait. Default 30s.
	MaxBackoff time.Duration
	// Clock is the time source for retry backoffs. Optional;
	// defaults to the Manager's clock so tests can trap waits
	// with quartz instead of real sleeps.
	Clock quartz.Clock
}

// RunPush ships the current snapshot to the Pusher, then ships
// every subsequent snapshot whenever the Manager broadcasts a
// change. RunPush returns when ctx is canceled, when the
// Manager is closed, or when the Pusher signals
// ErrPushUnimplemented.
//
// The first push is always sent with Initial=true so coderd can
// distinguish a fresh boot from a drift event.
//
// Each response also updates the Manager's pack-rows-advertised flag
// (fork_plugin_packs): the resolver emits plugin-pack rows only while the
// server advertises the capability, a flip in either direction triggers
// an immediate resync, and every new connection starts unadvertised so a
// reconnect to an older replica never leads with a pack-bearing snapshot.
// The first connection of a Manager negotiates the same way, so a
// restarted agent never leads with a pack-free settled snapshot either.
// A negotiation push (nonzero capability generation) always settles with
// a synchronous generation-zero successor resync, even when no capability
// flips, so chat hydration resumes after the non-mutating exchange. A
// reset whose resync is superseded by a concurrent resolve fails the
// connection, because the snapshot it gets back is not its probe.
func (m *Manager) RunPush(ctx context.Context, p Pusher, opts PushOptions) error {
	if p == nil {
		return xerrors.New("agentcontext: Pusher is required")
	}
	logger := opts.Logger
	initialBackoff := opts.InitialBackoff
	if initialBackoff <= 0 {
		initialBackoff = DefaultPushInitialBackoff
	}
	maxBackoff := opts.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = DefaultPushMaxBackoff
	}
	clock := opts.Clock
	if clock == nil {
		clock = m.clock
	}

	// Every connection starts unadvertised: the capability the previous
	// peer reported says nothing about the peer now answering, and a
	// snapshot that still carries pack rows must not lead this
	// connection's initial push to a replica that never asked for them.
	// The reset opens a negotiation window and rebuilds the snapshot
	// pack-free; every snapshot published inside the window carries a
	// nonzero capability generation, so any push before the peer's
	// capabilities are known is a capability probe that coderd persists
	// nothing for, concurrent resolves included. The window also stays
	// open when a previous connection's reset never settled, so a
	// retained pack-bearing snapshot can never be pushed as settled
	// inventory. The settling push that ends the window (generation 0,
	// once the peer's capabilities are known) defines the baseline
	// chats refresh against.
	firstConnection := !m.pushConnected.Swap(true)
	globRulesWere := m.globRulesAdvertised.Swap(false)
	if m.packRowsAdvertised.Swap(false) || globRulesWere || m.negotiating.Load() || firstConnection {
		snap, err := m.resyncNegotiating(ctx)
		if err != nil {
			// The reset could not rebuild the snapshot (for example the
			// connection's context was already canceled). The window
			// stays open so the next connection retries the reset;
			// returning the error keeps this connection from pushing
			// the retained pack-bearing snapshot as settled inventory.
			logger.Warn(ctx, "capability reset resync failed", slog.Error(err))
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			return xerrors.Errorf("capability reset resync: %w", err)
		}
		if snap.Version != 0 && snap.CapabilityGeneration == 0 {
			// A concurrent resolve superseded this reset's pass, so
			// Resync's stale-epoch path returned the previously
			// published snapshot instead of the probe this reset
			// built. That snapshot can be the retained pack-bearing
			// inventory, which must never lead this connection's
			// pushes; the window stays open so the next connection
			// retries the reset.
			logger.Warn(ctx, "capability reset superseded by a settled snapshot")
			return xerrors.Errorf("capability reset superseded by a settled snapshot")
		}
	}
	m.packAgentDefinitionsAdvertised.Swap(false)

	changes, unsub := m.SubscribeChanges()
	defer unsub()

	// advertise records the capability the server last reported, closes
	// the connection's negotiation window once the push was processed,
	// and produces the settled successor: the reported capabilities are
	// swapped in while the window is still open (a resolve that races
	// there publishes a probe, never settled inventory), then the window
	// closes and a synchronous resync publishes the first generation-zero
	// snapshot with the processed capabilities, which the push loop sends
	// as settled inventory. Without it, a probe whose response flips
	// nothing would leave the negotiation open and chats created while
	// disconnected unhydrated, and a resolve that raced between the close
	// and an asynchronous settle could publish a generation-zero snapshot
	// built from the reset defaults.
	advertise := func(resp *PushResponse, negotiated bool) {
		flipped := false
		if resp != nil {
			was := m.packRowsAdvertised.Swap(resp.ForkPluginPacks)
			if resp.ForkPluginPacks != was {
				logger.Debug(ctx, "server flipped plugin-pack advertisement; re-resolving")
				flipped = true
			}
			if resp.ForkAgentDefinitions != m.packAgentDefinitionsAdvertised.Swap(resp.ForkAgentDefinitions) {
				logger.Debug(ctx, "server flipped the agent-definitions advertisement; re-resolving")
				flipped = true
			}
			if resp.ForkGlobRules != m.globRulesAdvertised.Swap(resp.ForkGlobRules) {
				logger.Debug(ctx, "server flipped the glob-rules advertisement; re-resolving")
				flipped = true
			}
		}
		if negotiated {
			// End the window even when the response carries no
			// capabilities: a nil response leaves the reset defaults in
			// force, which is safe to settle on.
			m.negotiating.Store(false)
			if _, err := m.Resync(ctx); err != nil {
				logger.Warn(ctx, "settling capability negotiation failed", slog.Error(err))
				m.Trigger()
			}
			return
		}
		if flipped {
			logger.Debug(ctx, "capability advertisement flipped; re-resolving")
			m.Trigger()
		}
	}

	// Until SetReady the snapshot is version 0: wait, don't push it.
	initial := true
	for {
		snap := m.Snapshot()
		if snap.Version == 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-m.closedCh:
				return nil
			case <-changes:
			}
			continue
		}
		req := snapshotToPushRequest(snap, initial)

		resp, err := pushWithRetry(ctx, p, req, initialBackoff, maxBackoff, clock, logger)
		switch {
		case err == nil:
			// A probe persists nothing, so the settled successor keeps
			// Initial=true: after an agent restart its version can be
			// below the one coderd stored for the previous process.
			if req.CapabilityGeneration == 0 {
				initial = false
			}
			advertise(resp, req.CapabilityGeneration != 0)
		case errors.Is(err, ErrPushUnimplemented):
			logger.Warn(ctx, "coderd peer does not implement PushContextState; stopping")
			// No negotiation can complete against this peer; close the
			// window so the local snapshot stops carrying probe markers.
			m.negotiating.Store(false)
			if _, err := m.Resync(ctx); err != nil {
				logger.Warn(ctx, "resync after unimplemented push failed", slog.Error(err))
			}
			return nil
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return ctx.Err()
		default:
			// Should be unreachable: pushWithRetry only
			// returns terminal errors. Log and continue.
			logger.Warn(ctx, "push terminated with non-retried error", slog.Error(err))
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-m.closedCh:
			return nil
		case <-changes:
			// Shutdown comes from closedCh or ctx; the
			// subscriber channel is never closed by
			// SubscribeChanges.
		}
	}
}

// pushWithRetry retries transient errors with exponential
// backoff capped at maxBackoff. It returns the successful attempt's response
// (nil when no attempt succeeded). The retry loop exits when:
//
//   - ctx is canceled (returns ctx.Err()).
//   - The Pusher returns nil (success).
//   - The Pusher returns ErrPushUnimplemented (propagated).
func pushWithRetry(
	ctx context.Context,
	p Pusher,
	req *PushRequest,
	initialBackoff, maxBackoff time.Duration,
	clock quartz.Clock,
	logger slog.Logger,
) (*PushResponse, error) {
	backoff := initialBackoff
	for {
		resp, err := p.PushContextState(ctx, req)
		if err == nil {
			if resp != nil && !resp.Accepted {
				// Out-of-order or replayed push. Do not
				// retry; the next change will redeliver
				// the snapshot with a higher version.
				logger.Debug(ctx, "push rejected, awaiting next change",
					slog.F("version", req.Version))
			}
			return resp, nil
		}
		if errors.Is(err, ErrPushUnimplemented) {
			return nil, err
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		logger.Warn(ctx, "push failed, retrying",
			slog.F("version", req.Version),
			slog.F("backoff", backoff),
			slog.Error(err))
		timer := clock.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// snapshotToPushRequest copies the Snapshot into the wire
// representation. The Resources slice is reused; callers must
// not mutate it.
func snapshotToPushRequest(s Snapshot, initial bool) *PushRequest {
	return &PushRequest{
		Version:              s.Version,
		AggregateHash:        s.AggregateHash,
		Resources:            s.Resources,
		Initial:              initial,
		SnapshotError:        s.SnapshotError,
		CapabilityGeneration: s.CapabilityGeneration,
	}
}
