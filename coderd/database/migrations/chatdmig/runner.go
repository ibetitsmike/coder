package chatdmig

import (
	"bytes"
	"context"
	"database/sql"
	"io/fs"
	"time"

	"github.com/google/uuid"
	"golang.org/x/xerrors"
)

// LockID is a session advisory lock that UpGraph holds for the whole run, so
// two servers never interleave core steps and chatd nodes.
const LockID = int64(7340318204576120151)

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
	return &Runner{g: g, conn: conn, applied: applied}, nil
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

// ApplyReady applies, in graph order, each node whose parents ran and whose
// core versions are at most coreVersion.
func (r *Runner) ApplyReady(ctx context.Context, coreVersion uint) error {
	for _, n := range r.g.Nodes {
		if _, ok := r.applied[n.ID]; ok || !r.ready(n, coreVersion) {
			continue
		}
		if err := r.apply(ctx, n); err != nil {
			return xerrors.Errorf("node %s: %w", n.Name, err)
		}
		r.applied[n.ID] = n.Checksum
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

func (r *Runner) apply(ctx context.Context, n *Node) error {
	tx, err := r.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// SearchPath is checked against an identifier list when the node loads.
	if _, err := tx.ExecContext(ctx, "SET LOCAL search_path TO "+n.SearchPath); err != nil {
		return xerrors.Errorf("set search_path: %w", err)
	}
	if _, err := tx.ExecContext(ctx, string(n.SQL)); err != nil {
		return err
	}
	var version sql.NullInt64
	if n.Source == SourceUpstream {
		version = sql.NullInt64{Int64: int64(n.UpstreamVersion), Valid: true}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO chatd.migration_nodes (id, name, source, upstream_version, checksum) VALUES ($1, $2, $3, $4, $5)`,
		n.ID, n.Name, string(n.Source), version, n.Checksum,
	); err != nil {
		return xerrors.Errorf("record node: %w", err)
	}
	return tx.Commit()
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

// Pending lists the nodes that db has not applied.
func (g *Graph) Pending(ctx context.Context, db *sql.DB) ([]string, error) {
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
		if _, ok := applied[n.ID]; !ok {
			pending = append(pending, n.Name)
		}
	}
	return pending, nil
}

const routedStub = "-- chatdmig: this version runs as a chatd node.\nSELECT 1;\n"

// CoreFS returns upstream with each chatd or mixed version replaced by a
// no-op, so golang-migrate still counts every version.
func (g *Graph) CoreFS(upstream fs.FS) fs.FS {
	stub := map[string]bool{}
	for _, r := range g.Routes {
		if r.Ledger == LedgerCore {
			continue
		}
		up := g.files[r.Version]
		stub[up] = true
		stub[up[:len(up)-len(".up.sql")]+".down.sql"] = true
	}
	return overlayFS{base: upstream, stub: stub}
}

type overlayFS struct {
	base fs.FS
	stub map[string]bool
}

func (o overlayFS) Open(name string) (fs.File, error) {
	if !o.stub[name] {
		return o.base.Open(name)
	}
	if _, err := fs.Stat(o.base, name); err != nil {
		return nil, err
	}
	return &stubFile{name: name, Reader: bytes.NewReader([]byte(routedStub))}, nil
}

func (o overlayFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return fs.ReadDir(o.base, name)
}

type stubFile struct {
	name string
	*bytes.Reader
}

func (f *stubFile) Stat() (fs.FileInfo, error) { return stubInfo{f.name}, nil }
func (*stubFile) Close() error                 { return nil }

type stubInfo struct{ name string }

func (i stubInfo) Name() string     { return i.name }
func (stubInfo) Size() int64        { return int64(len(routedStub)) }
func (stubInfo) Mode() fs.FileMode  { return 0o444 }
func (stubInfo) ModTime() time.Time { return time.Time{} }
func (stubInfo) IsDir() bool        { return false }
func (stubInfo) Sys() any           { return nil }
