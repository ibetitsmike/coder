// Package plugins runs sandboxed Wasm plugins for Coder Agents chats. Each
// plugin runs in its own plugin-host child process. The Manager holds a
// lease for each plugin it runs, so one replica runs each plugin.
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
	defaultMaxPlugins    = 50
)

// StartHostFunc starts one plugin-host process.
type StartHostFunc func(ctx context.Context) (*pluginhost.Client, error)

// Options configures a Manager.
type Options struct {
	Logger    slog.Logger
	Database  database.Store
	Pubsub    pubsub.Pubsub
	ReplicaID uuid.UUID
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

	LeaseInterval time.Duration
	LeaseDuration time.Duration
	MaxPlugins    int32
}

// Manager runs the plugins that this replica holds a lease for.
type Manager struct {
	opts   Options
	logger slog.Logger
	db     database.Store
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
	if opts.MaxPlugins == 0 {
		opts.MaxPlugins = defaultMaxPlugins
	}
	if opts.Limits == (sandbox.Limits{}) {
		opts.Limits = sandbox.DefaultLimits()
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{}
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

	cancelSub, err := m.opts.Pubsub.Subscribe(ChangedChannel, func(context.Context, []byte) { m.Wake() })
	if err != nil {
		m.logger.Error(ctx, "subscribe to plugin changes", slog.Error(err))
	} else {
		defer cancelSub()
	}

	ticker := m.clock.NewTicker(m.opts.LeaseInterval, "plugins", "lease")
	defer ticker.Stop()
	for {
		m.reconcile(ctx)
		select {
		case <-m.ctx.Done():
			m.stopAll()
			// Use a fresh context: the Manager context is done.
			//nolint:gocritic // Releasing this replica's leases needs the same system access.
			releaseCtx, cancel := context.WithTimeout(dbauthz.AsSystemRestricted(context.Background()), 5*time.Second)
			if err := m.db.ReleasePluginLeases(releaseCtx, m.opts.ReplicaID); err != nil {
				m.logger.Warn(releaseCtx, "release plugin leases", slog.Error(err))
			}
			cancel()
			return
		case <-ticker.C:
		case <-m.wake:
		}
	}
}

// reconcile renews the leases of this replica, takes free leases, and makes
// the set of runners match the leases.
func (m *Manager) reconcile(ctx context.Context) {
	held, err := m.db.AcquirePluginLeases(ctx, database.AcquirePluginLeasesParams{
		RunnerID:     m.opts.ReplicaID,
		LeaseSeconds: int32(m.opts.LeaseDuration.Seconds()),
		MaxPlugins:   m.opts.MaxPlugins,
	})
	if err != nil {
		if ctx.Err() == nil {
			m.logger.Error(ctx, "acquire plugin leases", slog.Error(err))
		}
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	keep := make(map[uuid.UUID]struct{}, len(held))
	for _, p := range held {
		keep[p.ID] = struct{}{}
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

// ErrNotRunningHere means another replica holds the lease of the plugin, or
// no replica runs it.
var ErrNotRunningHere = xerrors.New("the plugin does not run on this replica")

// Webhook sends an HTTP request to the on_http export of a plugin. It
// returns ErrNotRunningHere if this replica does not run the plugin.
func (m *Manager) Webhook(ctx context.Context, pluginID uuid.UUID, req *http.Request) (*WebhookResponse, error) {
	r, ok := m.runnerFor(pluginID)
	if !ok {
		return nil, ErrNotRunningHere
	}
	return r.webhook(ctx, req)
}
