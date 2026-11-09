package dbcrypt

// This file is fork code: it lets fork queries of package forkdb pass
// through dbCrypt. See
// coderd/database/migrations/chatdmig/README.md#fork-queries.

import (
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/forkdb"
)

var _ forkdb.Wrapper = (*dbCrypt)(nil)

func (db *dbCrypt) ForkUnwrap() database.Store {
	return db.Store
}

// ForkWrap returns s as it is: no fork table holds a secret yet.
func (*dbCrypt) ForkWrap(s forkdb.Store) forkdb.Store {
	return s
}
