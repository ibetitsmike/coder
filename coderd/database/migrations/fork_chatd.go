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
	"strings"
	"sync"

	"github.com/golang-migrate/migrate/v4"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database/migrations/chatdmig"
)

var loadGraph = sync.OnceValues(func() (*chatdmig.Graph, error) {
	return chatdmig.Load(migrations)
})

// LoadGraph returns the chatd graph of this build.
func LoadGraph() (*chatdmig.Graph, error) {
	return loadGraph()
}

// UpAll runs upstream's numbered migrations and the chatd graph.
func UpAll(db *sql.DB) error {
	g, err := LoadGraph()
	if err != nil {
		return xerrors.Errorf("load chatd graph: %w", err)
	}
	return UpGraph(db, g)
}

// UpGraph moves core one version at a time. Before each step, it applies
// every chatd node that is ready. A routed version is a no-op in core: its
// SQL runs as a chatd node.
func UpGraph(db *sql.DB, g *chatdmig.Graph) (retErr error) {
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
	_, m, err := setup(db, g.CoreFS(migrations))
	if err != nil {
		return xerrors.Errorf("migrate setup: %w", err)
	}
	defer func() {
		srcErr, dbErr := m.Close()
		if retErr == nil {
			retErr = errors.Join(srcErr, dbErr)
		}
	}()

	for {
		version, dirty, err := m.Version()
		switch {
		case errors.Is(err, migrate.ErrNilVersion):
			version = 0
		case err != nil:
			return xerrors.Errorf("core version: %w", err)
		case dirty:
			return xerrors.Errorf("core version %d is dirty", version)
		}
		if err := runner.ApplyReady(ctx, version); err != nil {
			return xerrors.Errorf("chatd after core %d: %w", version, err)
		}
		err = m.Steps(1)
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, migrate.ErrNoChange) {
			return nil
		}
		if err != nil {
			return xerrors.Errorf("core step after %d: %w", version, err)
		}
	}
}

// EnsureCleanAll is EnsureClean for core and the chatd graph.
func EnsureCleanAll(db *sql.DB) error {
	if err := EnsureClean(db); err != nil {
		return err
	}
	g, err := LoadGraph()
	if err != nil {
		return xerrors.Errorf("load chatd graph: %w", err)
	}
	pending, err := g.Pending(context.Background(), db)
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
