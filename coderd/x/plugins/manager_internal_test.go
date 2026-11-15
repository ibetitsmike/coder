package plugins

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3/sloggers/slogtest"

	"github.com/coder/coder/v2/coderd/database/dbmock"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/database/forkdb/forkdbmock"
	"github.com/coder/coder/v2/coderd/database/pubsub"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/quartz"
)

// fakeLeases is the plugin table as the lease loop sees it.
type fakeLeases struct {
	mu       sync.Mutex
	summary  forkdb.GetPluginLeaseSummaryRow
	held     []forkdb.Plugin
	summarys int
	acquires int
}

func (f *fakeLeases) set(summary forkdb.GetPluginLeaseSummaryRow, held ...forkdb.Plugin) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.summary = summary
	f.held = held
}

// calls reports the summary reads and lease acquisitions so far.
func (f *fakeLeases) calls() (summarys, acquires int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.summarys, f.acquires
}

type leaseHarness struct {
	clock  *quartz.Mock
	trap   *quartz.Trap
	ps     pubsub.Pubsub
	leases *fakeLeases
	m      *Manager
}

// startLeaseHarness starts a Manager on a mock clock and a fake plugin
// table, with every timer of the lease loop trapped.
func startLeaseHarness(t *testing.T, ps pubsub.Pubsub) *leaseHarness {
	t.Helper()
	ctrl := gomock.NewController(t)
	fdb := forkdbmock.NewMockStore(ctrl)
	leases := &fakeLeases{}
	fdb.EXPECT().GetPluginLeaseSummary(gomock.Any(), gomock.Any()).DoAndReturn(
		func(context.Context, uuid.UUID) (forkdb.GetPluginLeaseSummaryRow, error) {
			leases.mu.Lock()
			defer leases.mu.Unlock()
			leases.summarys++
			return leases.summary, nil
		}).AnyTimes()
	fdb.EXPECT().AcquirePluginLeases(gomock.Any(), gomock.Any()).DoAndReturn(
		func(context.Context, forkdb.AcquirePluginLeasesParams) ([]forkdb.Plugin, error) {
			leases.mu.Lock()
			defer leases.mu.Unlock()
			leases.acquires++
			return leases.held, nil
		}).AnyTimes()
	// A held plugin in these tests has a manifest that does not decode, so
	// its runner stops at once and stores the error.
	fdb.EXPECT().UpdatePluginLastError(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	fdb.EXPECT().ReleasePluginLeases(gomock.Any(), gomock.Any()).Return(nil).Times(1)

	clock := quartz.NewMock(t)
	trap := clock.Trap().NewTimer("plugins", "lease")
	t.Cleanup(trap.Close)
	m := New(Options{
		// The broken plugins log an error when their runner stops.
		Logger:       slogtest.Make(t, &slogtest.Options{IgnoreErrors: true}),
		Database:     dbmock.NewMockStore(ctrl),
		ForkDatabase: fdb,
		Pubsub:       ps,
		ReplicaID:    uuid.New(),
		Clock:        clock,
	})
	m.Start()
	t.Cleanup(func() { require.NoError(t, m.Close()) })
	return &leaseHarness{clock: clock, trap: trap, ps: ps, leases: leases, m: m}
}

// next waits for the lease loop to set its timer and checks the interval.
func (h *leaseHarness) next(ctx context.Context, t *testing.T, want time.Duration) {
	t.Helper()
	call := h.trap.MustWait(ctx)
	require.Equal(t, want, call.Duration)
	call.MustRelease(ctx)
}

func (h *leaseHarness) fire(ctx context.Context, d time.Duration) {
	h.clock.Advance(d).MustWait(ctx)
}

// waitRunners waits for the runners of held plugins to stop, so none
// touches the store after the test.
func (h *leaseHarness) waitRunners(ctx context.Context, t *testing.T) {
	t.Helper()
	h.m.mu.Lock()
	runners := make([]*runner, 0, len(h.m.runners))
	for _, r := range h.m.runners {
		runners = append(runners, r)
	}
	h.m.mu.Unlock()
	for _, r := range runners {
		testutil.TryReceive(ctx, t, r.done)
	}
}

func brokenPlugin() forkdb.Plugin {
	return forkdb.Plugin{ID: uuid.New(), Manifest: []byte(`not json`)}
}

// TestManagerIdleIssuesNoWrites verifies a replica with no enabled plugin
// reads the plugin table once every idle interval and writes nothing. Before
// the idle wait it ran AcquirePluginLeases, an UPDATE, every lease interval:
// 60 statements in 10 minutes.
func TestManagerIdleIssuesNoWrites(t *testing.T) {
	t.Parallel()
	ctx := testutil.Context(t, testutil.WaitLong)
	h := startLeaseHarness(t, pubsub.NewInMemory())

	h.next(ctx, t, defaultIdleInterval)
	for range 2 {
		h.fire(ctx, defaultIdleInterval)
		h.next(ctx, t, defaultIdleInterval)
	}
	summarys, acquires := h.leases.calls()
	// One check at start and one each idle interval: two in 10 minutes.
	require.Equal(t, 3, summarys)
	require.Zero(t, acquires)
}

// TestManagerIdlePicksUpChanges verifies an idle replica takes a plugin as
// soon as plugins:changed reports it, finds one whose message was lost by
// the next idle check, and goes back to idle once the plugin is disabled.
func TestManagerIdlePicksUpChanges(t *testing.T) {
	t.Parallel()
	ctx := testutil.Context(t, testutil.WaitLong)
	h := startLeaseHarness(t, pubsub.NewInMemory())
	h.next(ctx, t, defaultIdleInterval)

	// Enabled: the message wakes the loop before the idle timer fires.
	h.leases.set(forkdb.GetPluginLeaseSummaryRow{Enabled: 1, Acquirable: 1}, brokenPlugin())
	require.NoError(t, h.ps.Publish(ChangedChannel, nil))
	h.next(ctx, t, defaultLeaseInterval)
	h.waitRunners(ctx, t)
	_, acquires := h.leases.calls()
	require.Equal(t, 1, acquires)

	// Disabled: the running replica renews, loses the lease, and checks
	// the summary on the next lease interval.
	h.leases.set(forkdb.GetPluginLeaseSummaryRow{})
	require.NoError(t, h.ps.Publish(ChangedChannel, nil))
	h.next(ctx, t, defaultLeaseInterval)
	h.fire(ctx, defaultLeaseInterval)
	h.next(ctx, t, defaultIdleInterval)
	_, acquires = h.leases.calls()
	require.Equal(t, 2, acquires)

	// Enabled with the message lost: the idle check finds it.
	h.leases.set(forkdb.GetPluginLeaseSummaryRow{Enabled: 1, Acquirable: 1}, brokenPlugin())
	h.fire(ctx, defaultIdleInterval)
	h.next(ctx, t, defaultLeaseInterval)
	h.waitRunners(ctx, t)
	_, acquires = h.leases.calls()
	require.Equal(t, 3, acquires)
}

// TestManagerStandbyWatchesLeases verifies a replica whose plugins another
// replica holds checks every lease interval, so it takes over an expired
// lease in time, and takes no lease while none is free.
func TestManagerStandbyWatchesLeases(t *testing.T) {
	t.Parallel()
	ctx := testutil.Context(t, testutil.WaitLong)
	h := startLeaseHarness(t, pubsub.NewInMemory())
	h.next(ctx, t, defaultIdleInterval)

	h.leases.set(forkdb.GetPluginLeaseSummaryRow{Enabled: 1, Acquirable: 0})
	require.NoError(t, h.ps.Publish(ChangedChannel, nil))
	h.next(ctx, t, defaultLeaseInterval)
	h.fire(ctx, defaultLeaseInterval)
	h.next(ctx, t, defaultLeaseInterval)
	_, acquires := h.leases.calls()
	require.Zero(t, acquires)

	// The lease of the other replica expired.
	h.leases.set(forkdb.GetPluginLeaseSummaryRow{Enabled: 1, Acquirable: 1}, brokenPlugin())
	h.fire(ctx, defaultLeaseInterval)
	h.next(ctx, t, defaultLeaseInterval)
	h.waitRunners(ctx, t)
	_, acquires = h.leases.calls()
	require.Equal(t, 1, acquires)
}

type failingSubscribe struct{ pubsub.Pubsub }

func (failingSubscribe) Subscribe(string, pubsub.Listener) (func(), error) {
	return nil, xerrors.New("subscribe failed")
}

// TestManagerIdleWithoutSubscription verifies a replica that could not
// subscribe to plugins:changed keeps checking every lease interval, since
// no message would report a change.
func TestManagerIdleWithoutSubscription(t *testing.T) {
	t.Parallel()
	ctx := testutil.Context(t, testutil.WaitLong)
	h := startLeaseHarness(t, failingSubscribe{pubsub.NewInMemory()})
	h.next(ctx, t, defaultLeaseInterval)
	h.fire(ctx, defaultLeaseInterval)
	h.next(ctx, t, defaultLeaseInterval)
	_, acquires := h.leases.calls()
	require.Zero(t, acquires)
}
