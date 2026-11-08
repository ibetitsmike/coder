package database

// This file is fork code: ibetitsmike/coder runs its own queries from package
// forkdb. See migrations/chatdmig/README.md#fork-queries.

// ForkDBTX returns the connection or transaction that the store runs queries
// on, so forkdb.From can run fork queries on it.
func (q *sqlQuerier) ForkDBTX() DBTX {
	return q.db
}
