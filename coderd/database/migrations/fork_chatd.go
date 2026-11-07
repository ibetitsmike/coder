package migrations

// This file is fork code: ibetitsmike/coder keeps the Coder Agents tables in
// the chatd schema. See chatdmig/README.md.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/golang-migrate/migrate/v4"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"

	"github.com/coder/coder/v2/coderd/database/migrations/chatdmig"
)

var loadGraph = sync.OnceValues(func() (*chatdmig.Graph, error) {
	return chatdmig.Load(migrations)
})

// LoadGraph returns the chatd graph of this build.
func LoadGraph() (*chatdmig.Graph, error) {
	return loadGraph()
}

// UpOption configures UpAll, UpGraph and RunBackground.
type UpOption func(*upConfig)

type upConfig struct {
	logger          slog.Logger
	lock            chatdmig.LockPolicy
	deferBackground bool
}

func newUpConfig(opts []UpOption) upConfig {
	cfg := upConfig{logger: slog.Make(), lock: chatdmig.DefaultLockPolicy}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// WithLogger logs applied nodes and lock retries.
func WithLogger(logger slog.Logger) UpOption {
	return func(c *upConfig) { c.logger = logger }
}

// WithLockPolicy replaces chatdmig.DefaultLockPolicy.
func WithLockPolicy(p chatdmig.LockPolicy) UpOption {
	return func(c *upConfig) { c.lock = p }
}

// DeferBackground leaves background nodes to RunBackground.
func DeferBackground() UpOption {
	return func(c *upConfig) { c.deferBackground = true }
}

// UpAll runs upstream's numbered migrations and the chatd graph. Without
// DeferBackground, it also runs the background nodes, as tests need.
func UpAll(db *sql.DB, opts ...UpOption) error {
	g, err := LoadGraph()
	if err != nil {
		return xerrors.Errorf("load chatd graph: %w", err)
	}
	return UpGraph(db, g, opts...)
}

// ServerMigrate is the migrate step of coder server. It runs the startup
// migrations, then runs the background nodes in a goroutine that stops with
// ctx.
func ServerMigrate(ctx context.Context, logger slog.Logger) func(*sql.DB) error {
	return func(db *sql.DB) error {
		g, err := LoadGraph()
		if err != nil {
			return xerrors.Errorf("load chatd graph: %w", err)
		}
		if err := UpGraph(db, g, WithLogger(logger), DeferBackground()); err != nil {
			return err
		}
		go func() {
			err := RunBackground(ctx, db, g, WithLogger(logger))
			if err != nil && ctx.Err() == nil {
				logger.Error(ctx, "background chatd migrations failed, they run again on the next start", slog.Error(err))
			}
		}()
		return nil
	}
}

// UpGraph moves core one version at a time. Before each step, it applies
// every chatd node that is ready. A routed version is a no-op in core: its
// SQL runs as a chatd node.
//
// Each core step and each node commits on its own, unlike migrations.Up,
// which runs all pending versions in one transaction. Locks last for one
// migration, and a server that dies keeps the steps it finished. A failed
// upgrade can stop between two versions, so roll back by restoring a
// snapshot, not by starting the old binary.
//
// Every transaction sets lock_timeout from the lock policy and retries after
// a lock timeout or a deadlock.
func UpGraph(db *sql.DB, g *chatdmig.Graph, opts ...UpOption) (retErr error) {
	cfg := newUpConfig(opts)
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return xerrors.Errorf("get connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", chatdmig.LockID); err != nil {
		return xerrors.Errorf("acquire chatd lock: %w", err)
	}
	defer func() {
		_, _ = conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", chatdmig.LockID)
	}()

	runner, err := g.NewRunner(ctx, conn)
	if err != nil {
		return err
	}
	runner.Lock, runner.Logger = cfg.lock, cfg.logger
	var phases []chatdmig.Phase
	if cfg.deferBackground {
		phases = []chatdmig.Phase{chatdmig.PhaseStartup}
	}

	core := g.CoreFS(migrations, cfg.lock.SetLocal())
	var m *migrate.Migrate
	openMigrate := func() error {
		if m != nil {
			return nil
		}
		var err error
		if _, m, err = setup(db, core); err != nil {
			return xerrors.Errorf("migrate setup: %w", err)
		}
		return nil
	}
	closeMigrate := func() error {
		if m == nil {
			return nil
		}
		srcErr, dbErr := m.Close()
		m = nil
		return errors.Join(srcErr, dbErr)
	}
	defer func() {
		if err := closeMigrate(); retErr == nil {
			retErr = err
		}
	}()

	for {
		if err := openMigrate(); err != nil {
			return err
		}
		version, dirty, err := m.Version()
		switch {
		case errors.Is(err, migrate.ErrNilVersion):
			version = 0
		case err != nil:
			return xerrors.Errorf("core version: %w", err)
		case dirty:
			return xerrors.Errorf("core version %d is dirty", version)
		}
		if err := runner.ApplyReady(ctx, version, phases...); err != nil {
			return xerrors.Errorf("chatd after core %d: %w", version, err)
		}
		err = cfg.lock.Retry(ctx, func() error {
			if err := openMigrate(); err != nil {
				return err
			}
			err := m.Steps(1)
			if err != nil && !isLastStep(err) {
				// The failed commit leaves golang-migrate locked.
				_ = closeMigrate()
			}
			return err
		}, func(retry int, err error) {
			cfg.logger.Warn(ctx, "core migration is waiting for a lock, retrying",
				slog.F("after_version", version), slog.F("retry", retry), slog.Error(err))
		})
		if isLastStep(err) {
			return nil
		}
		if err != nil {
			return xerrors.Errorf("core step after %d: %w", version, err)
		}
	}
}

func isLastStep(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, migrate.ErrNoChange)
}

// RunBackground applies the background nodes of g that are ready. It waits
// for chatdmig.BackgroundLockID, so one server at a time runs them.
func RunBackground(ctx context.Context, db *sql.DB, g *chatdmig.Graph, opts ...UpOption) error {
	if !slices.ContainsFunc(g.Nodes, func(n *chatdmig.Node) bool { return n.Phase == chatdmig.PhaseBackground }) {
		return nil
	}
	cfg := newUpConfig(opts)
	conn, err := db.Conn(ctx)
	if err != nil {
		return xerrors.Errorf("get connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", chatdmig.BackgroundLockID); err != nil {
		return xerrors.Errorf("acquire background lock: %w", err)
	}
	defer func() {
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", chatdmig.BackgroundLockID)
	}()

	var version uint
	if err := conn.QueryRowContext(ctx, "SELECT version FROM schema_migrations").Scan(&version); err != nil {
		return xerrors.Errorf("core version: %w", err)
	}
	runner, err := g.NewRunner(ctx, conn)
	if err != nil {
		return err
	}
	runner.Lock, runner.Logger = cfg.lock, cfg.logger
	return runner.ApplyReady(ctx, version, chatdmig.PhaseBackground)
}

// EnsureCleanAll is EnsureClean for core and the startup nodes of the chatd
// graph.
func EnsureCleanAll(db *sql.DB) error {
	if err := EnsureClean(db); err != nil {
		return err
	}
	g, err := LoadGraph()
	if err != nil {
		return xerrors.Errorf("load chatd graph: %w", err)
	}
	pending, err := g.Pending(context.Background(), db, chatdmig.PhaseStartup)
	if err != nil {
		return err
	}
	if len(pending) > 0 {
		return xerrors.Errorf("database needs chatd migration: %s", strings.Join(pending, ", "))
	}
	return nil
}

// GetAllMigrationsHash changes when an upstream migration or the chatd graph
// changes.
func GetAllMigrationsHash() string {
	g, err := LoadGraph()
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(GetMigrationsHash()+g.Hash())))
}

var searchPathOption = regexp.MustCompile(`search_path=([^\s]+)`)

// WithChatdSearchPath adds chatd to the search path of a connection string.
// It uses the libpq "options" parameter, so lib/pq, psql and pg_dump all
// accept the result.
func WithChatdSearchPath(dsn string) string {
	if !strings.HasPrefix(dsn, "postgres://") && !strings.HasPrefix(dsn, "postgresql://") {
		if strings.Contains(dsn, "search_path") {
			return dsn
		}
		return dsn + " options='-c search_path=public,chatd'"
	}
	u, err := url.Parse(dsn)
	if err != nil {
		// sql.Open reports the error.
		return dsn
	}
	q := u.Query()
	if sp := q.Get("search_path"); sp != "" {
		if !hasSchema(sp, "chatd") {
			q.Set("search_path", sp+",chatd")
		}
	} else if opts := q.Get("options"); searchPathOption.MatchString(opts) {
		q.Set("options", searchPathOption.ReplaceAllStringFunc(opts, func(m string) string {
			if hasSchema(strings.TrimPrefix(m, "search_path="), "chatd") {
				return m
			}
			return m + ",chatd"
		}))
	} else {
		q.Set("options", strings.TrimSpace(opts+" -c search_path=public,chatd"))
	}
	// libpq does not decode "+" as a space. Encode escapes a real "+".
	u.RawQuery = strings.ReplaceAll(q.Encode(), "+", "%20")
	return u.String()
}

func hasSchema(searchPath, schema string) bool {
	for _, s := range strings.Split(searchPath, ",") {
		if strings.TrimSpace(s) == schema {
			return true
		}
	}
	return false
}
