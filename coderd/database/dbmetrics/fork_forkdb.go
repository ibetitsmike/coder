package dbmetrics

// This file is fork code: it measures the fork queries of package forkdb like
// queryMetricsStore measures upstream's queries. See
// ../migrations/chatdmig/README.md#fork-queries.

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
	"github.com/coder/coder/v2/coderd/httpmw"
)

var (
	_ forkdb.Wrapper = metricsStore{}
	_ forkdb.Wrapper = queryMetricsStore{}
)

func (m metricsStore) ForkUnwrap() database.Store {
	return m.Store
}

// ForkWrap returns s as it is: metricsStore measures only transactions, and
// forkdb.InTx runs them through InTx of the store.
func (metricsStore) ForkWrap(s forkdb.Store) forkdb.Store {
	return s
}

func (m queryMetricsStore) ForkUnwrap() database.Store {
	return m.s
}

func (m queryMetricsStore) ForkWrap(s forkdb.Store) forkdb.Store {
	return forkQueryMetricsStore{m: m, s: s}
}

// forkQueryMetricsStore does not embed forkdb.Store, so a new fork query does
// not compile until it is measured here.
type forkQueryMetricsStore struct {
	m queryMetricsStore
	s forkdb.Store
}

var _ forkdb.Store = forkQueryMetricsStore{}

func (f forkQueryMetricsStore) observe(ctx context.Context, query string, start time.Time) {
	f.m.queryLatencies.WithLabelValues(query).Observe(time.Since(start).Seconds())
	f.m.queryCounts.WithLabelValues(httpmw.ExtractHTTPRoute(ctx), httpmw.ExtractHTTPMethod(ctx), query).Inc()
}

func (f forkQueryMetricsStore) GetChatSubagentByChatID(ctx context.Context, chatID uuid.UUID) (forkdb.ChatSubagent, error) {
	defer f.observe(ctx, "GetChatSubagentByChatID", time.Now())
	return f.s.GetChatSubagentByChatID(ctx, chatID)
}

func (f forkQueryMetricsStore) InsertChatSubagent(ctx context.Context, arg forkdb.InsertChatSubagentParams) (forkdb.ChatSubagent, error) {
	defer f.observe(ctx, "InsertChatSubagent", time.Now())
	return f.s.InsertChatSubagent(ctx, arg)
}
