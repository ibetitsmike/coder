package chatd

// This file is fork code: test doubles that count fork queries, for tests
// that assert how many journal writes a behavior does or does not do.

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"cdr.dev/slog/v3/sloggers/slogtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/rbac"
)

// phaseCountingStore implements forkdb.Store by embedding the inner store and
// overriding UpdateWorkflowRunPhases, so the inner store serves every other
// query. A pointer receiver keeps a plain copy of the struct from counting.
type phaseCountingStore struct {
	forkdb.Store
	phaseWrites atomic.Int64
}

func (s *phaseCountingStore) UpdateWorkflowRunPhases(
	ctx context.Context, arg forkdb.UpdateWorkflowRunPhasesParams,
) error {
	s.phaseWrites.Add(1)
	return s.Store.UpdateWorkflowRunPhases(ctx, arg)
}

var _ forkdb.Store = (*phaseCountingStore)(nil)

// forkDBSwap is a database.Store wrapper that serves fork queries through a
// fixed forkdb.Store instead of its base store's queries. It embeds the base
// store, so every upstream query reaches it unchanged; only the forkdb.Wrapper
// methods redirect, and From resolves fork queries to the swap first because
// it tests Wrapper before conn.
type forkDBSwap struct {
	database.Store
	fdb forkdb.Store
}

var _ forkdb.Wrapper = (*forkDBSwap)(nil)

func (w *forkDBSwap) ForkUnwrap() database.Store { return w.Store }

// ForkWrap returns the swap's fork store, not the inner one From unwraps
// to: the swap exists to serve fork queries through fdb, whatever the base
// store is.
func (w *forkDBSwap) ForkWrap(s forkdb.Store) forkdb.Store { return w.fdb }

// countPhaseWrites installs a phaseCountingStore as the journal store of
// server's db and returns the counter. The counting store rides in a
// forkDBSwap under a dbauthz wrapper, so every query keeps its
// authorization. It stays installed: the run's goroutines read server.db
// until the server closes.
func countPhaseWrites(t *testing.T, server *Server) *phaseCountingStore {
	t.Helper()
	prevDB := server.db
	inner, err := forkdb.From(prevDB)
	if err != nil {
		panic(err)
	}
	counting := &phaseCountingStore{Store: inner}
	server.db = dbauthz.New(
		&forkDBSwap{Store: prevDB, fdb: counting},
		rbac.NewStrictCachingAuthorizer(prometheus.NewRegistry()),
		slogtest.Make(t, &slogtest.Options{IgnoreErrors: true}),
		nil,
	)
	return counting
}
