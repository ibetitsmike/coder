package plugins

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"sync/atomic"
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
	"github.com/coder/coder/v2/coderd/x/plugins/pluginhost"
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
	// lastErrors holds every non-empty last_error the runners stored.
	lastErrors []string
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
func startLeaseHarness(t *testing.T, ps pubsub.Pubsub, opts ...func(*Options)) *leaseHarness {
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
	fdb.EXPECT().UpdatePluginLastError(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg forkdb.UpdatePluginLastErrorParams) error {
			if arg.LastError != "" {
				leases.mu.Lock()
				leases.lastErrors = append(leases.lastErrors, arg.LastError)
				leases.mu.Unlock()
			}
			return nil
		}).AnyTimes()
	fdb.EXPECT().ReleasePluginLeases(gomock.Any(), gomock.Any()).Return(nil).Times(1)

	db := dbmock.NewMockStore(ctrl)
	clock := quartz.NewMock(t)
	trap := clock.Trap().NewTimer("plugins", "lease")
	t.Cleanup(trap.Close)
	o := Options{
		// The broken plugins log an error when their runner stops.
		Logger:       slogtest.Make(t, &slogtest.Options{IgnoreErrors: true}),
		Database:     db,
		ForkDatabase: fdb,
		Pubsub:       ps,
		ReplicaID:    uuid.New(),
		Clock:        clock,
	}
	for _, opt := range opts {
		opt(&o)
	}
	m := New(o)
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

// TestManagerRestartBackoff verifies a plugin whose host fails at start is
// started again after 10, 20, 40 and 80 seconds, reads its module once per
// attempt and stores each delay in last_error. A start that takes 5 minutes
// and fails does not reset the delay, and a changed plugin starts at once.
// Before the backoff the plugin was reloaded every lease interval.
func TestManagerRestartBackoff(t *testing.T) {
	t.Parallel()
	ctx := testutil.Context(t, testutil.WaitLong)

	var hosts atomic.Int32
	// The fifth host takes until release closes to fail.
	entered := make(chan struct{})
	release := make(chan struct{})
	h := startLeaseHarness(t, pubsub.NewInMemory(), func(o *Options) {
		o.StartHost = func(ctx context.Context) (*pluginhost.Client, error) {
			if hosts.Add(1) == 5 {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return nil, xerrors.New("host exited at start")
		}
	})
	plugin := forkdb.Plugin{
		ID:        uuid.New(),
		FileID:    uuid.New(),
		Manifest:  []byte(`{}`),
		Grants:    []byte(`{}`),
		UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	start := h.clock.Now()
	var (
		startsMu sync.Mutex
		starts   []time.Duration
	)
	h.db.EXPECT().GetFileByID(gomock.Any(), plugin.FileID).DoAndReturn(
		func(context.Context, uuid.UUID) (database.File, error) {
			startsMu.Lock()
			defer startsMu.Unlock()
			starts = append(starts, h.clock.Since(start))
			return database.File{}, nil
		}).AnyTimes()
	h.next(ctx, t, defaultIdleInterval)

	h.leases.set(forkdb.GetPluginLeaseSummaryRow{Enabled: 1, Acquirable: 1}, plugin)
	require.NoError(t, h.ps.Publish(ChangedChannel, nil))
	h.next(ctx, t, defaultLeaseInterval)
	// step waits for the runner to stop, then runs one lease interval.
	step := func() {
		h.waitRunners(ctx, t)
		h.fire(ctx, defaultLeaseInterval)
		h.next(ctx, t, defaultLeaseInterval)
	}
	// Attempts at 0, 10, 30, 70 and 150 seconds.
	for range 15 {
		step()
	}
	testutil.TryReceive(ctx, t, entered)
	// The fifth start takes 5 minutes and fails. The runner never became
	// ready, so the delay doubles to 160 seconds: no attempt at 460 seconds.
	for range 30 {
		h.fire(ctx, defaultLeaseInterval)
		h.next(ctx, t, defaultLeaseInterval)
	}
	close(release)
	step()
	h.waitRunners(ctx, t)

	// The plugin changed during the backoff: it starts at once.
	changed := plugin
	changed.UpdatedAt = plugin.UpdatedAt.Add(time.Second)
	h.leases.set(forkdb.GetPluginLeaseSummaryRow{Enabled: 1, Acquirable: 1}, changed)
	require.NoError(t, h.ps.Publish(ChangedChannel, nil))
	h.next(ctx, t, defaultLeaseInterval)
	h.waitRunners(ctx, t)

	s := func(n int) time.Duration { return time.Duration(n) * time.Second }
	startsMu.Lock()
	require.Equal(t, []time.Duration{0, s(10), s(30), s(70), s(150), s(460)}, starts)
	startsMu.Unlock()
	require.EqualValues(t, 6, hosts.Load(), "one module read per attempt")
	h.leases.mu.Lock()
	lastErrors := h.leases.lastErrors
	h.leases.mu.Unlock()
	delays := make([]string, 0, len(lastErrors))
	for _, msg := range lastErrors {
		require.Contains(t, msg, "host exited at start")
		delay, _, _ := strings.Cut(strings.TrimPrefix(msg, "restarting in "), " ")
		delays = append(delays, delay)
	}
	require.Equal(t, []string{"10s", "20s", "40s", "1m20s", "2m40s", "10s"}, delays)
}

func TestRestartDelay(t *testing.T) {
	t.Parallel()
	for failures, want := range []time.Duration{
		10 * time.Second, 20 * time.Second, 40 * time.Second, 80 * time.Second,
		160 * time.Second, 320 * time.Second, 10 * time.Minute, 10 * time.Minute,
	} {
		require.Equal(t, want, restartDelay(failures), "failures %d", failures)
	}
	require.Equal(t, 10*time.Minute, restartDelay(1000))
}

// TestRunnerFailedResetsAfterUptime verifies only a runner that stayed up
// for 5 minutes after it became ready resets the failure count.
func TestRunnerFailedResetsAfterUptime(t *testing.T) {
	t.Parallel()
	clock := quartz.NewMock(t)
	m := &Manager{clock: clock}
	for _, tc := range []struct {
		name string
		up   time.Duration
		// ready is false for a runner that failed before it became ready.
		ready bool
		want  time.Duration
	}{
		{name: "NeverReady", want: 80 * time.Second},
		{name: "ShortUptime", ready: true, up: restartResetAfter - time.Second, want: 80 * time.Second},
		{name: "LongUptime", ready: true, up: restartResetAfter, want: restartBaseDelay},
	} {
		r := &runner{m: m, failures: 3}
		if tc.ready {
			r.upAt = clock.Now().Add(-tc.up)
		}
		require.Equal(t, tc.want, r.failed(), tc.name)
		require.Equal(t, clock.Now().Add(tc.want), r.restartAt, tc.name)
	}
}
