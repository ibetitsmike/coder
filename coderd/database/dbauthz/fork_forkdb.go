package dbauthz

// This file is fork code: it authorizes the fork queries of package forkdb
// like the methods of querier authorize upstream's queries. See
// ../migrations/chatdmig/README.md#fork-queries.

import (
	"context"

	"github.com/google/uuid"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
)

var _ forkdb.Wrapper = (*querier)(nil)

func (q *querier) ForkUnwrap() database.Store {
	return q.db
}

func (q *querier) ForkWrap(s forkdb.Store) forkdb.Store {
	return &forkQuerier{q: q, db: s}
}

// forkQuerier authorizes each fork query. It does not embed forkdb.Store, so
// a new fork query does not compile until it is authorized here.
type forkQuerier struct {
	q  *querier
	db forkdb.Store
}

var _ forkdb.Store = (*forkQuerier)(nil)

// GetChatSubagentByChatID authorizes through the owning chat.
func (f *forkQuerier) GetChatSubagentByChatID(ctx context.Context, chatID uuid.UUID) (forkdb.ChatSubagent, error) {
	if _, err := f.q.GetChatByID(ctx, chatID); err != nil {
		return forkdb.ChatSubagent{}, err
	}
	return f.db.GetChatSubagentByChatID(ctx, chatID)
}
