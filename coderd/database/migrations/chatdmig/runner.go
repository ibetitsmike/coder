package chatdmig

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"io/fs"
	"slices"
	"time"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
)

// LockID is a session advisory lock that UpGraph holds for the whole run, so
// two servers never interleave core steps and chatd nodes.
const LockID = int64(7340318204576120151)

// BackgroundLockID is the session advisory lock of the background runner, so
// one server at a time runs background nodes.
const BackgroundLockID = LockID + 1

const ledgerSQL = `
CREATE SCHEMA IF NOT EXISTS chatd;
CREATE TABLE IF NOT EXISTS chatd.migration_nodes (
	id               uuid PRIMARY KEY,
	name             text NOT NULL,
	source           text NOT NULL CHECK (source IN ('fork', 'upstream')),
	upstream_version int,
	checksum         bytea NOT NULL,
	applied_at       timestamptz NOT NULL DEFAULT now()
);
`

// Runner applies graph nodes on one connection.
type Runner struct {
	g       *Graph
	conn    *sql.Conn
	applied map[uuid.UUID][]byte

	// Lock bounds the lock waits of each transaction.
	Lock   LockPolicy
	Logger slog.Logger
}

// NewRunner creates the ledger if needed and reads the applied nodes. It
// refuses a fork node whose SQL changed after it ran.
func (g *Graph) NewRunner(ctx context.Context, conn *sql.Conn) (*Runner, error) {
	if _, err := conn.ExecContext(ctx, ledgerSQL); err != nil {
		return nil, xerrors.Errorf("create ledger: %w", err)
	}
	applied, err := readLedger(ctx, conn)
	if err != nil {
		return nil, err
	}
	for id, sum := range applied {
		n := g.byID[id]
		// golang-migrate never reads an applied file again, so a changed
		// upstream node is not an error. A changed fork node is a mistake.
		if n != nil && n.Source == SourceFork && !bytes.Equal(sum, n.Checksum) {
			return nil, xerrors.Errorf("fork node %s changed after it ran", n.Name)
		}
	}
	return &Runner{g: g, conn: conn, applied: applied, Lock: DefaultLockPolicy, Logger: slog.Make()}, nil
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func readLedger(ctx context.Context, q querier) (map[uuid.UUID][]byte, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, checksum FROM chatd.migration_nodes`)
	if err != nil {
		return nil, xerrors.Errorf("read ledger: %w", err)
	}
	defer rows.Close()
	applied := map[uuid.UUID][]byte{}
	for rows.Next() {
		var id uuid.UUID
		var sum []byte
		if err := rows.Scan(&id, &sum); err != nil {
			return nil, xerrors.Errorf("scan ledger: %w", err)
		}
		applied[id] = sum
	}
	return applied, rows.Err()
}

// ApplyReady applies, in graph order, each node of the given phases whose
// parents ran and whose core versions are at most coreVersion. Without
// phases, it applies nodes of every phase.
func (r *Runner) ApplyReady(ctx context.Context, coreVersion uint, phases ...Phase) error {
	for _, n := range r.g.Nodes {
		if _, ok := r.applied[n.ID]; ok || !r.ready(n, coreVersion) {
			continue
		}
		if len(phases) > 0 && !slices.Contains(phases, n.Phase) {
			continue
		}
		start := time.Now()
		batches, err := r.apply(ctx, n)
		if err != nil {
			return xerrors.Errorf("node %s: %w", n.Name, err)
		}
		r.applied[n.ID] = n.Checksum
		r.Logger.Info(ctx, "applied chatd node", slog.F("node", n.Name), slog.F("phase", n.Phase),
			slog.F("batches", batches), slog.F("duration", time.Since(start)))
	}
	return nil
}

func (r *Runner) ready(n *Node, coreVersion uint) bool {
	for _, req := range n.Requires {
		if req.Node != uuid.Nil {
			if _, ok := r.applied[req.Node]; !ok {
				return false
			}
		} else if req.CoreVersion > coreVersion {
			return false
		}
	}
	return true
}

// apply runs a node and returns the number of transactions that committed.
func (r *Runner) apply(ctx context.Context, n *Node) (int, error) {
	if n.NoTransaction {
		return 1, r.applyOutsideTx(ctx, n)
	}
	for batches := 1; ; batches++ {
		var rows int64
		err := r.Lock.Retry(ctx, func() error {
			var err error
			rows, err = r.applyTx(ctx, n)
			return err
		}, func(retry int, err error) {
			r.Logger.Warn(ctx, "chatd node is waiting for a lock, retrying",
				slog.F("node", n.Name), slog.F("retry", retry), slog.Error(err))
		})
		if err != nil {
			return batches - 1, err
		}
		if !n.Repeat || rows == 0 {
			return batches, nil
		}
		r.Logger.Debug(ctx, "chatd node batch", slog.F("node", n.Name), slog.F("batch", batches), slog.F("rows", rows))
	}
}

// applyTx runs a node in one transaction. It records the node, unless the
// node repeats and its statement changed rows.
func (r *Runner) applyTx(ctx context.Context, n *Node) (int64, error) {
	tx, err := r.conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	// SearchPath is checked against an identifier list when the node loads.
	if _, err := tx.ExecContext(ctx, "SET LOCAL search_path TO "+n.SearchPath); err != nil {
		return 0, xerrors.Errorf("set search_path: %w", err)
	}
	if q := r.Lock.SetLocal(); q != "" {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return 0, xerrors.Errorf("set lock_timeout: %w", err)
		}
	}
	res, err := tx.ExecContext(ctx, string(n.SQL))
	if err != nil {
		return 0, err
	}
	var rows int64
	if n.Repeat {
		if rows, err = res.RowsAffected(); err != nil {
			return 0, err
		}
	}
	if rows == 0 {
		if err := r.record(ctx, tx, n); err != nil {
			return 0, err
		}
	}
	return rows, tx.Commit()
}

// applyOutsideTx runs the one statement of a node outside a transaction. It
// sets no lock_timeout: CREATE INDEX CONCURRENTLY does not block reads or
// writes while it waits, and a timeout would leave an invalid index.
func (r *Runner) applyOutsideTx(ctx context.Context, n *Node) error {
	if _, err := r.conn.ExecContext(ctx, "SET search_path TO "+n.SearchPath); err != nil {
		return xerrors.Errorf("set search_path: %w", err)
	}
	// The connection goes back to the pool after the run.
	defer func() { _, _ = r.conn.ExecContext(context.WithoutCancel(ctx), "RESET search_path") }()
	if _, err := r.conn.ExecContext(ctx, string(n.SQL)); err != nil {
		return err
	}
	// A failed CREATE INDEX CONCURRENTLY leaves an invalid index, and IF NOT
	// EXISTS skips it on the next attempt. Refuse to record such a node.
	var invalid string
	if err := r.conn.QueryRowContext(ctx, `
SELECT coalesce(string_agg(format('%I.%I', n.nspname, c.relname), ', '), '')
FROM pg_index i
JOIN pg_class c ON c.oid = i.indexrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE NOT i.indisvalid AND n.nspname = ANY (current_schemas(false))`).Scan(&invalid); err != nil {
		return xerrors.Errorf("check indexes: %w", err)
	}
	if invalid != "" {
		return xerrors.Errorf("invalid index %s, left by a failed CREATE INDEX CONCURRENTLY: drop it with DROP INDEX CONCURRENTLY and start again", invalid)
	}
	return r.record(ctx, r.conn, n)
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (*Runner) record(ctx context.Context, e execer, n *Node) error {
	var version sql.NullInt64
	if n.Source == SourceUpstream {
		version = sql.NullInt64{Int64: int64(n.UpstreamVersion), Valid: true}
	}
	if _, err := e.ExecContext(ctx,
		`INSERT INTO chatd.migration_nodes (id, name, source, upstream_version, checksum) VALUES ($1, $2, $3, $4, $5)`,
		n.ID, n.Name, string(n.Source), version, n.Checksum,
	); err != nil {
		return xerrors.Errorf("record node: %w", err)
	}
	return nil
}

// WithoutForkNodes keeps the adopt node and the routed upstream nodes. The
// result must produce upstream's schema, with chat objects in chatd.
func (g *Graph) WithoutForkNodes() *Graph {
	out := *g
	out.Nodes = nil
	for _, n := range g.Nodes {
		if n.Source == SourceUpstream || n.ID == AdoptNodeID {
			out.Nodes = append(out.Nodes, n)
		}
	}
	return &out
}

// Pending lists the nodes of the given phases that db has not applied.
// Without phases, it lists nodes of every phase.
func (g *Graph) Pending(ctx context.Context, db *sql.DB, phases ...Phase) ([]string, error) {
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('chatd.migration_nodes') IS NOT NULL`).Scan(&exists); err != nil {
		return nil, xerrors.Errorf("find ledger: %w", err)
	}
	applied := map[uuid.UUID][]byte{}
	if exists {
		var err error
		applied, err = readLedger(ctx, db)
		if err != nil {
			return nil, err
		}
	}
	var pending []string
	for _, n := range g.Nodes {
		if len(phases) > 0 && !slices.Contains(phases, n.Phase) {
			continue
		}
		if _, ok := applied[n.ID]; !ok {
			pending = append(pending, n.Name)
		}
	}
	return pending, nil
}

const routedStub = "-- chatdmig: this version runs as a chatd node.\nSELECT 1;\n"

// CoreFS returns upstream with each chatd or mixed version replaced by a
// no-op, so golang-migrate still counts every version. Every other up
// migration starts with prelude.
func (g *Graph) CoreFS(upstream fs.FS, prelude string) fs.FS {
	stub := map[string]bool{}
	for _, r := range g.Routes {
		if r.Ledger == LedgerCore {
			continue
		}
		up := g.files[r.Version]
		stub[up] = true
		stub[up[:len(up)-len(".up.sql")]+".down.sql"] = true
	}
	return overlayFS{base: upstream, stub: stub, prelude: prelude}
}

type overlayFS struct {
	base    fs.FS
	stub    map[string]bool
	prelude string
}

func (o overlayFS) Open(name string) (fs.File, error) {
	switch {
	case o.stub[name]:
		if _, err := fs.Stat(o.base, name); err != nil {
			return nil, err
		}
		return newMemFile(name, []byte(routedStub)), nil
	case o.prelude != "" && upstreamFile.MatchString(name):
		f, err := o.base.Open(name)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		body, err := io.ReadAll(f)
		if err != nil {
			return nil, err
		}
		return newMemFile(name, append([]byte(o.prelude), body...)), nil
	default:
		return o.base.Open(name)
	}
}

func (o overlayFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return fs.ReadDir(o.base, name)
}

type memFile struct {
	name string
	size int64
	*bytes.Reader
}

func newMemFile(name string, body []byte) *memFile {
	return &memFile{name: name, size: int64(len(body)), Reader: bytes.NewReader(body)}
}

func (f *memFile) Stat() (fs.FileInfo, error) { return memInfo{f.name, f.size}, nil }
func (*memFile) Close() error                 { return nil }

type memInfo struct {
	name string
	size int64
}

func (i memInfo) Name() string     { return i.name }
func (i memInfo) Size() int64      { return i.size }
func (memInfo) Mode() fs.FileMode  { return 0o444 }
func (memInfo) ModTime() time.Time { return time.Time{} }
func (memInfo) IsDir() bool        { return false }
func (memInfo) Sys() any           { return nil }
