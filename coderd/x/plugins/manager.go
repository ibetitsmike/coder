// Package plugins runs sandboxed Wasm plugins for Coder Agents chats. Each
// plugin runs in its own plugin-host child process. The Manager holds a
// lease for each plugin it runs, so one replica runs each plugin.
//
// The Manager renews its leases every lease interval while it runs a plugin.
// RenewPluginLeases returns only ids and updated_at, so a renewal reads and
// decrypts no config: the Manager reads a full row only when a plugin
// changed. AcquirePluginLeases takes only plugins no replica holds.
// A replica that runs none reads GetPluginLeaseSummary instead, which takes
// no lock and writes nothing, and calls AcquirePluginLeases only when a
// plugin is free to lease. It keeps checking every lease interval while
// another replica holds an enabled plugin, so it can take over an expired
// lease. With no enabled plugin it waits for a plugins:changed message and
// checks once every idle interval in case a message was lost.
//
// Each runner keeps a delivery cursor per bound chat. A runner binds every
// chat of its linked users when it subscribes to a new user, after a link
// is created, and once every bind interval as a fallback. A created chat
// watch event binds that one chat. The scan every scan interval finds due
// chats, and binds every chat only after a full bind failed or pubsub
// dropped chat watch events.
package plugins

import (
	"context"
	"net/http"
	"net/url"
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
	"github.com/coder/coder/v2/coderd/x/plugins/pluginhost"
	"github.com/coder/coder/v2/coderd/x/plugins/sandbox"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/quartz"
)

// ChangedChannel is the pubsub channel that tells each Manager to check its
// leases at once, for example after an install, an update or a delete.
const ChangedChannel = "plugins:changed"

const (
	defaultLeaseInterval = 10 * time.Second
	defaultLeaseDuration = 30 * time.Second
	// defaultIdleInterval is how often a replica checks for plugins while
	// none is enabled. A plugins:changed message wakes it at once; the check
	// only finds a change whose message was lost.
	defaultIdleInterval = 5 * time.Minute
	defaultMaxPlugins   = 50
)

// StartHostFunc starts one plugin-host process.
type StartHostFunc func(ctx context.Context) (*pluginhost.Client, error)

// Options configures a Manager.
type Options struct {
	Logger   slog.Logger
	Database database.Store
	// ForkDatabase runs the plugin queries. It is forkdb.From(Database).
	ForkDatabase forkdb.Store
	Pubsub       pubsub.Pubsub
	ReplicaID    uuid.UUID
	// AccessURL is the base URL for host calls into the chat API. The
	// requests never leave the process: Handler serves them.
	AccessURL *url.URL
	// Handler returns the coderd root handler. It is a func because the
	// router is built after the Manager.
	Handler func() http.Handler
	// HTTPClient is the base client for http_fetch. safedial guards it.
	HTTPClient *http.Client
	// StartHost starts a plugin-host process. The default runs
	// `coder plugin-host` from the current executable.
	StartHost StartHostFunc
	Limits    sandbox.Limits
	Clock     quartz.Clock
	// ReplicaAddress returns the internal address of another replica. A
	// replica that does not run a plugin forwards the plugin's webhooks to
	// the replica that does. Nil means one replica runs every plugin.
	ReplicaAddress func(ctx context.Context, replicaID uuid.UUID) (string, bool)
	// ReplicaHTTPClient forwards webhooks to other replicas.
	ReplicaHTTPClient *http.Client

	LeaseInterval time.Duration
	LeaseDuration time.Duration
	IdleInterval  time.Duration
	MaxPlugins    int32
}

// Manager runs the plugins that this replica holds a lease for.
type Manager struct {
	opts   Options
	logger slog.Logger
	db     database.Store
	fdb    forkdb.Store
	clock  quartz.Clock

	ctx    context.Context
	cancel context.CancelFunc
	wake   chan struct{}
	done   chan struct{}

	mu      sync.Mutex
	runners map[uuid.UUID]*runner
	closed  bool
}

// New makes a Manager. Call Start to run plugins.
func New(opts Options) *Manager {
	if opts.Clock == nil {
		opts.Clock = quartz.NewReal()
	}
	if opts.LeaseInterval == 0 {
		opts.LeaseInterval = defaultLeaseInterval
	}
	if opts.LeaseDuration == 0 {
		opts.LeaseDuration = defaultLeaseDuration
	}
	if opts.IdleInterval == 0 {
		opts.IdleInterval = defaultIdleInterval
	}
	if opts.MaxPlugins == 0 {
		opts.MaxPlugins = defaultMaxPlugins
	}
	if opts.Limits == (sandbox.Limits{}) {
		opts.Limits = sandbox.DefaultLimits()
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{}
	}
	if opts.ReplicaHTTPClient == nil {
		opts.ReplicaHTTPClient = http.DefaultClient
	}
	if opts.StartHost == nil {
		logger := opts.Logger.Named("host")
		opts.StartHost = func(ctx context.Context) (*pluginhost.Client, error) {
			return pluginhost.StartProcess(ctx, logger, pluginhost.ProcessOptions{})
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		opts:    opts,
		logger:  opts.Logger,
		db:      opts.Database,
		fdb:     opts.ForkDatabase,
		clock:   opts.Clock,
		ctx:     ctx,
		cancel:  cancel,
		wake:    make(chan struct{}, 1),
		done:    make(chan struct{}),
		runners: map[uuid.UUID]*runner{},
	}
}

// Start runs the lease loop in the background.
func (m *Manager) Start() {
	go m.loop()
}

// Close stops all plugins and releases the leases of this replica.
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	m.mu.Unlock()

	m.cancel()
	<-m.done
	return nil
}

// Wake makes the lease loop run at once.
func (m *Manager) Wake() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Manager) loop() {
	defer close(m.done)
	//nolint:gocritic // The manager leases plugins across every organization.
	ctx := dbauthz.AsSystemRestricted(m.ctx)

	// A drop report may have lost a change, so it wakes the loop too.
	cancelSub, err := m.opts.Pubsub.SubscribeWithErr(ChangedChannel, func(context.Context, []byte, error) { m.Wake() })
	subscribed := err == nil
	if err != nil {
		m.logger.Error(ctx, "subscribe to plugin changes", slog.Error(err))
	} else {
		defer cancelSub()
	}

	for {
		start := m.clock.Now()
		idle := m.reconcile(ctx)
		// Count the time reconcile took, so a slow renewal does not push
		// the next one past the lease expiry.
		next := max(m.opts.LeaseInterval-m.clock.Since(start), 0)
		// Without the subscription no message reports a change, so an idle
		// replica keeps the short interval.
		if idle && subscribed {
			next = m.opts.IdleInterval
		}
		timer := m.clock.NewTimer(next, "plugins", "lease")
		select {
		case <-m.ctx.Done():
			timer.Stop()
			m.stopAll()
			// Use a fresh context: the Manager context is done.
			//nolint:gocritic // Releasing this replica's leases needs the same system access.
			releaseCtx, cancel := context.WithTimeout(dbauthz.AsSystemRestricted(context.Background()), 5*time.Second)
			if err := m.fdb.ReleasePluginLeases(releaseCtx, m.opts.ReplicaID); err != nil {
				m.logger.Warn(releaseCtx, "release plugin leases", slog.Error(err))
			} else if err := m.opts.Pubsub.Publish(ChangedChannel, nil); err != nil {
				// Other replicas still take over on their next lease check.
				m.logger.Warn(releaseCtx, "publish released plugin leases", slog.Error(err))
			}
			cancel()
			return
		case <-timer.C:
		case <-m.wake:
			timer.Stop()
		}
	}
}

// reconcile renews the leases of this replica, takes free leases, and makes
// the set of runners match the leases. It reports whether the replica is
// idle: it runs nothing and no plugin is enabled.
func (m *Manager) reconcile(ctx context.Context) bool {
	m.mu.Lock()
	running := len(m.runners) > 0
	m.mu.Unlock()
	if !running {
		// No lease to renew, so write only when a plugin is free to take.
		summary, err := m.fdb.GetPluginLeaseSummary(ctx, m.opts.ReplicaID)
		if err != nil {
			if ctx.Err() == nil {
				m.logger.Error(ctx, "get plugin lease summary", slog.Error(err))
			}
			return false
		}
		if summary.Acquirable == 0 {
			return summary.Enabled == 0
		}
	}
	held, ok := m.leases(ctx)
	if !ok {
		return false
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return false
	}
	keep := make(map[uuid.UUID]struct{}, len(held))
	for _, l := range held {
		keep[l.id] = struct{}{}
		if l.plugin == nil {
			// The row could not be read: keep what runs and retry next time.
			continue
		}
		p := *l.plugin
		prev, ok := m.runners[p.ID]
		if ok && !prev.exited() && prev.plugin.UpdatedAt.Equal(p.UpdatedAt) {
			continue
		}
		if ok {
			prev.stop()
		}
		// The new runner waits for the previous one to exit, so two
		// host processes never run the same plugin at once.
		m.runners[p.ID] = m.startRunner(p, prev)
	}
	for id, r := range m.runners {
		if _, ok := keep[id]; !ok {
			r.stop()
			delete(m.runners, id)
		}
	}
	return false
}

// heldPlugin is a plugin this replica holds a lease for. plugin is nil when
// its row could not be read.
type heldPlugin struct {
	id     uuid.UUID
	plugin *forkdb.Plugin
}

// leases renews the leases of this replica and takes free ones. A renewal
// returns only ids and updated_at: the full row, with its decrypted config,
// is read only for a plugin that changed or that has no runner yet. It
// reports false when the renewal failed, so nothing changes this time.
func (m *Manager) leases(ctx context.Context) ([]heldPlugin, bool) {
	lease := int32(m.opts.LeaseDuration.Seconds())
	renewed, err := m.fdb.RenewPluginLeases(ctx, forkdb.RenewPluginLeasesParams{
		RunnerID:     m.opts.ReplicaID,
		LeaseSeconds: lease,
	})
	if err != nil {
		if ctx.Err() == nil {
			m.logger.Error(ctx, "renew plugin leases", slog.Error(err))
		}
		return nil, false
	}
	m.mu.Lock()
	running := make(map[uuid.UUID]forkdb.Plugin, len(m.runners))
	for id, r := range m.runners {
		running[id] = r.plugin
	}
	m.mu.Unlock()

	held := make([]heldPlugin, 0, len(renewed))
	for _, row := range renewed {
		if p, ok := running[row.ID]; ok && p.UpdatedAt.Equal(row.UpdatedAt) {
			held = append(held, heldPlugin{id: row.ID, plugin: &p})
			continue
		}
		p, err := m.fdb.GetPluginByID(ctx, row.ID)
		if err != nil {
			if ctx.Err() == nil {
				m.logger.Error(ctx, "get leased plugin", slog.F("plugin_id", row.ID), slog.Error(err))
			}
			held = append(held, heldPlugin{id: row.ID})
			continue
		}
		held = append(held, heldPlugin{id: row.ID, plugin: &p})
	}

	free := m.opts.MaxPlugins - int32(len(renewed)) //nolint:gosec // At most MaxPlugins rows are leased.
	if free <= 0 {
		return held, true
	}
	acquired, err := m.fdb.AcquirePluginLeases(ctx, forkdb.AcquirePluginLeasesParams{
		RunnerID:     m.opts.ReplicaID,
		LeaseSeconds: lease,
		MaxPlugins:   free,
	})
	if err != nil {
		if ctx.Err() == nil {
			m.logger.Error(ctx, "acquire plugin leases", slog.Error(err))
		}
		return held, true
	}
	for i := range acquired {
		held = append(held, heldPlugin{id: acquired[i].ID, plugin: &acquired[i]})
	}
	return held, true
}

func (m *Manager) stopAll() {
	m.mu.Lock()
	runners := m.runners
	m.runners = map[uuid.UUID]*runner{}
	m.mu.Unlock()
	for _, r := range runners {
		r.stop()
	}
	for _, r := range runners {
		<-r.done
	}
}

// runnerFor returns the runner of a plugin if this replica runs it.
func (m *Manager) runnerFor(pluginID uuid.UUID) (*runner, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runners[pluginID]
	if !ok || r.exited() {
		return nil, false
	}
	return r, true
}

// Inspect runs a module once, with no host calls, to read its manifest.
func (m *Manager) Inspect(ctx context.Context, wasm []byte) (codersdk.PluginManifest, error) {
	client, err := m.opts.StartHost(ctx)
	if err != nil {
		return codersdk.PluginManifest{}, xerrors.Errorf("start plugin host: %w", err)
	}
	defer client.Close()
	manifest, err := client.Load(ctx, wasm, m.opts.Limits)
	if err != nil {
		return codersdk.PluginManifest{}, xerrors.Errorf("load module: %w", err)
	}
	return ManifestFromProto(manifest), nil
}

// ErrNotRunningHere means no replica that this replica can reach runs the
// plugin.
var ErrNotRunningHere = xerrors.New("the plugin does not run on this replica")

// Webhook sends an HTTP request to the on_http export of a plugin. If
// another replica runs the plugin, Webhook forwards the request to it. It
// returns ErrNotRunningHere if no reachable replica runs the plugin.
func (m *Manager) Webhook(ctx context.Context, p forkdb.Plugin, req *http.Request) (*WebhookResponse, error) {
	relayed := req.Header.Get(RelaySourceHeader) != ""
	// The plugin sees the request as the sender sent it.
	req.Header.Del(RelaySourceHeader)
	if r, ok := m.runnerFor(p.ID); ok {
		return r.webhook(ctx, req)
	}
	// A relayed request is not forwarded again, so a lease that moves
	// while a request is in flight can't make a loop.
	leased := p.RunnerID.Valid && p.LeaseExpiresAt.Valid && p.LeaseExpiresAt.Time.After(dbtime.Now())
	if relayed || !leased || p.RunnerID.UUID == m.opts.ReplicaID || m.opts.ReplicaAddress == nil {
		return nil, ErrNotRunningHere
	}
	return m.forwardWebhook(ctx, p, req)
}
