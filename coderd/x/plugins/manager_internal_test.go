package plugins

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3/sloggers/slogtest"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbmock"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/database/forkdb/forkdbmock"
	"github.com/coder/coder/v2/coderd/database/pubsub"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/quartz"
)

// fakeLeases is the plugin table as the lease loop sees it.
type fakeLeases struct {
	mu      sync.Mutex
	summary forkdb.GetPluginLeaseSummaryRow
	// table holds the enabled plugins this replica may lease. mine holds
	// the ones it leased.
	table    []forkdb.Plugin
	mine     map[uuid.UUID]bool
	summarys int
	renews   int
	acquires int
	// fullRows counts the plugin rows, config included, that the store
	// returned: rows a real store decrypts.
	fullRows int
	reads    int
	// onAcquire runs once inside the next AcquirePluginLeases, as a slow
	// query.
	onAcquire func()
}

func (f *fakeLeases) set(summary forkdb.GetPluginLeaseSummaryRow, table ...forkdb.Plugin) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.summary = summary
	f.table = table
}

// calls reports the summary reads and lease acquisitions so far.
func (f *fakeLeases) calls() (summarys, acquires int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.summarys, f.acquires
}

func (f *fakeLeases) renew() []forkdb.RenewPluginLeasesRow {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renews++
	var rows []forkdb.RenewPluginLeasesRow
	for _, p := range f.table {
		if f.mine[p.ID] {
			rows = append(rows, forkdb.RenewPluginLeasesRow{ID: p.ID, UpdatedAt: p.UpdatedAt})
		}
	}
	return rows
}

func (f *fakeLeases) acquire(arg forkdb.AcquirePluginLeasesParams) []forkdb.Plugin {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acquires++
	if fn := f.onAcquire; fn != nil {
		f.onAcquire = nil
		fn()
	}
	var rows []forkdb.Plugin
	for _, p := range f.table {
		if !f.mine[p.ID] && len(rows) < int(arg.MaxPlugins) {
			f.mine[p.ID] = true
			rows = append(rows, p)
		}
	}
	f.fullRows += len(rows)
	return rows
}

func (f *fakeLeases) get(id uuid.UUID) (forkdb.Plugin, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	for _, p := range f.table {
		if p.ID == id {
			f.fullRows++
			return p, nil
		}
	}
	return forkdb.Plugin{}, sql.ErrNoRows
}

type leaseHarness struct {
	clock  *quartz.Mock
	trap   *quartz.Trap
	ps     pubsub.Pubsub
	db     *dbmock.MockStore
	leases *fakeLeases
	m      *Manager
}

// startLeaseHarness starts a Manager on a mock clock and a fake plugin
// table, with every timer of the lease loop trapped.
func startLeaseHarness(t *testing.T, ps pubsub.Pubsub) *leaseHarness {
	t.Helper()
	ctrl := gomock.NewController(t)
	fdb := forkdbmock.NewMockStore(ctrl)
	leases := &fakeLeases{mine: map[uuid.UUID]bool{}}
	fdb.EXPECT().GetPluginLeaseSummary(gomock.Any(), gomock.Any()).DoAndReturn(
		func(context.Context, uuid.UUID) (forkdb.GetPluginLeaseSummaryRow, error) {
			leases.mu.Lock()
			defer leases.mu.Unlock()
			leases.summarys++
			return leases.summary, nil
		}).AnyTimes()
	fdb.EXPECT().RenewPluginLeases(gomock.Any(), gomock.Any()).DoAndReturn(
		func(context.Context, forkdb.RenewPluginLeasesParams) ([]forkdb.RenewPluginLeasesRow, error) {
			return leases.renew(), nil
		}).AnyTimes()
	fdb.EXPECT().AcquirePluginLeases(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg forkdb.AcquirePluginLeasesParams) ([]forkdb.Plugin, error) {
			return leases.acquire(arg), nil
		}).AnyTimes()
	fdb.EXPECT().GetPluginByID(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, id uuid.UUID) (forkdb.Plugin, error) {
			return leases.get(id)
		}).AnyTimes()
	// A held plugin in these tests has a manifest that does not decode, so
	// its runner stops at once and stores the error.
	fdb.EXPECT().UpdatePluginLastError(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	fdb.EXPECT().ReleasePluginLeases(gomock.Any(), gomock.Any()).Return(nil).Times(1)

	db := dbmock.NewMockStore(ctrl)
	clock := quartz.NewMock(t)
	trap := clock.Trap().NewTimer("plugins", "lease")
	t.Cleanup(trap.Close)
	m := New(Options{
		// The broken plugins log an error when their runner stops.
		Logger:       slogtest.Make(t, &slogtest.Options{IgnoreErrors: true}),
		Database:     db,
		ForkDatabase: fdb,
		Pubsub:       ps,
		ReplicaID:    uuid.New(),
		Clock:        clock,
	})
	m.Start()
	t.Cleanup(func() { require.NoError(t, m.Close()) })
	return &leaseHarness{clock: clock, trap: trap, ps: ps, db: db, leases: leases, m: m}
}

// next waits for the lease loop to set its timer and checks the interval.
func (h *leaseHarness) next(ctx context.Context, t *testing.T, want time.Duration) {
	t.Helper()
	call := h.trap.MustWait(ctx)
	// Release first, so a failed check does not block the loop and the
	// cleanup.
	call.MustRelease(ctx)
	require.Equal(t, want, call.Duration)
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

// TestManagerRenewalCountsReconcileTime verifies the time a renewal takes
// shortens the wait for the next one, so a slow query does not push the
// next renewal past the lease expiry.
func TestManagerRenewalCountsReconcileTime(t *testing.T) {
	t.Parallel()
	ctx := testutil.Context(t, testutil.WaitLong)
	h := startLeaseHarness(t, pubsub.NewInMemory())
	h.next(ctx, t, defaultIdleInterval)

	h.leases.mu.Lock()
	h.leases.summary = forkdb.GetPluginLeaseSummaryRow{Enabled: 1, Acquirable: 1}
	h.leases.table = []forkdb.Plugin{brokenPlugin()}
	h.leases.onAcquire = func() { h.clock.Advance(4 * time.Second) }
	h.leases.mu.Unlock()
	require.NoError(t, h.ps.Publish(ChangedChannel, nil))
	h.next(ctx, t, defaultLeaseInterval-4*time.Second)

	h.leases.mu.Lock()
	h.leases.onAcquire = func() { h.clock.Advance(defaultLeaseInterval + time.Second) }
	h.leases.mu.Unlock()
	h.fire(ctx, defaultLeaseInterval-4*time.Second)
	h.next(ctx, t, 0)
	// The timer fires at once and the next renewal is quick.
	h.next(ctx, t, defaultLeaseInterval)
	h.waitRunners(ctx, t)
}

type failingSubscribe struct{ pubsub.Pubsub }

func (failingSubscribe) Subscribe(string, pubsub.Listener) (func(), error) {
	return nil, xerrors.New("subscribe failed")
}

func (failingSubscribe) SubscribeWithErr(string, pubsub.ListenerWithErr) (func(), error) {
	return nil, xerrors.New("subscribe failed")
}

// captureSubscribe hands out each listener as pubsub calls it: a Listener
// never sees an error, a ListenerWithErr does.
type captureSubscribe struct {
	pubsub.Pubsub
	listeners chan pubsub.ListenerWithErr
}

func (c captureSubscribe) Subscribe(event string, l pubsub.Listener) (func(), error) {
	c.listeners <- func(ctx context.Context, msg []byte, err error) {
		if err == nil {
			l(ctx, msg)
		}
	}
	return c.Pubsub.Subscribe(event, l)
}

func (c captureSubscribe) SubscribeWithErr(event string, l pubsub.ListenerWithErr) (func(), error) {
	c.listeners <- l
	return c.Pubsub.SubscribeWithErr(event, l)
}

// TestManagerIdleWakesOnDroppedMessages verifies an idle replica checks at
// once when pubsub reports dropped plugins:changed messages, instead of
// waiting for the idle interval.
func TestManagerIdleWakesOnDroppedMessages(t *testing.T) {
	t.Parallel()
	ctx := testutil.Context(t, testutil.WaitLong)
	ps := captureSubscribe{Pubsub: pubsub.NewInMemory(), listeners: make(chan pubsub.ListenerWithErr, 1)}
	h := startLeaseHarness(t, ps)
	listener := testutil.TryReceive(ctx, t, ps.listeners)
	h.next(ctx, t, defaultIdleInterval)

	h.leases.set(forkdb.GetPluginLeaseSummaryRow{Enabled: 1, Acquirable: 1}, brokenPlugin())
	listener(ctx, nil, pubsub.ErrDroppedMessages)
	// The mock clock does not move, so only the wake starts this check.
	shortCtx := testutil.Context(t, testutil.WaitShort)
	h.next(shortCtx, t, defaultLeaseInterval)
	h.waitRunners(ctx, t)
	_, acquires := h.leases.calls()
	require.Equal(t, 1, acquires)
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

// TestManagerRenewalReadsNoRows verifies a renewal of held, unchanged
// plugins reads no full plugin row, so the store decrypts no config, and
// that a changed updated_at reads the row once and restarts the runner.
// Before RenewPluginLeases, AcquirePluginLeases returned and decrypted
// every held row on every lease interval.
func TestManagerRenewalReadsNoRows(t *testing.T) {
	t.Parallel()
	ctx := testutil.Context(t, testutil.WaitLong)
	h := startLeaseHarness(t, pubsub.NewInMemory())

	// The runner of a plugin that decodes waits in its first query until
	// it stops, so it stays up without a host process.
	starts := make(chan forkdb.Plugin, 4)
	plugin := forkdb.Plugin{
		ID:        uuid.New(),
		FileID:    uuid.New(),
		Manifest:  []byte(`{}`),
		Grants:    []byte(`{}`),
		Config:    `{"token":"one"}`,
		UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	h.db.EXPECT().GetFileByID(gomock.Any(), plugin.FileID).DoAndReturn(
		func(ctx context.Context, _ uuid.UUID) (database.File, error) {
			r, ok := h.m.runnerFor(plugin.ID)
			if ok {
				starts <- r.plugin
			}
			<-ctx.Done()
			return database.File{}, ctx.Err()
		}).AnyTimes()
	h.next(ctx, t, defaultIdleInterval)

	h.leases.set(forkdb.GetPluginLeaseSummaryRow{Enabled: 1, Acquirable: 1}, plugin)
	require.NoError(t, h.ps.Publish(ChangedChannel, nil))
	h.next(ctx, t, defaultLeaseInterval)
	started := testutil.TryReceive(ctx, t, starts)
	require.Equal(t, plugin.Config, started.Config)

	for range 3 {
		h.fire(ctx, defaultLeaseInterval)
		h.next(ctx, t, defaultLeaseInterval)
	}
	h.leases.mu.Lock()
	renews, reads, fullRows := h.leases.renews, h.leases.reads, h.leases.fullRows
	h.leases.mu.Unlock()
	require.Equal(t, 4, renews)
	require.Zero(t, reads, "a renewal of an unchanged plugin reads no row")
	require.Equal(t, 1, fullRows, "only the first lease returned a full row")
	require.Empty(t, starts, "the runner keeps running")

	// The config changed: one read, and the runner restarts with it.
	changed := plugin
	changed.Config = `{"token":"two"}`
	changed.UpdatedAt = plugin.UpdatedAt.Add(time.Second)
	h.leases.set(forkdb.GetPluginLeaseSummaryRow{Enabled: 1, Acquirable: 1}, changed)
	h.fire(ctx, defaultLeaseInterval)
	h.next(ctx, t, defaultLeaseInterval)
	started = testutil.TryReceive(ctx, t, starts)
	require.Equal(t, changed.Config, started.Config)

	h.fire(ctx, defaultLeaseInterval)
	h.next(ctx, t, defaultLeaseInterval)
	h.leases.mu.Lock()
	reads = h.leases.reads
	h.leases.mu.Unlock()
	require.Equal(t, 1, reads, "the changed plugin is read once")
	require.Empty(t, starts)
}
