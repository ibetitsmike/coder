// Package forkdb holds the queries of fork-only tables and columns, generated
// by sqlc from queries/*.sql. Upstream's queries.sql.go never changes, so an
// upstream sync does not conflict. See
// ../migrations/chatdmig/README.md#fork-queries.
//
// A fork query runs on the same connection or transaction as a
// database.Store and behind the same wrappers: dbauthz authorizes it,
// dbmetrics measures it and dbcrypt encrypts its secrets. Use From to get the
// fork queries of a store and InTx for a transaction that spans both.
package forkdb

import (
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
)

// Store runs the fork queries.
type Store interface {
	Querier
}

// Wrapper is a database.Store that wraps another store, for example dbauthz
// or dbcrypt. From wraps the fork queries of the inner store the same way.
// A store that wraps another without implementing Wrapper makes From fail,
// so no fork query skips a wrapper by accident.
type Wrapper interface {
	// ForkUnwrap returns the store that this store wraps.
	ForkUnwrap() database.Store
	// ForkWrap wraps the fork queries of the store that ForkUnwrap returns.
	ForkWrap(Store) Store
}

// conn is the store at the bottom of the wrapper chain. database.New returns
// one.
type conn interface {
	ForkDBTX() database.DBTX
}

// From returns the fork queries of db. They run on the connection or
// transaction of db, behind the same wrappers.
func From(db database.Store) (Store, error) {
	switch s := db.(type) {
	case Wrapper:
		inner, err := From(s.ForkUnwrap())
		if err != nil {
			return nil, err
		}
		return s.ForkWrap(inner), nil
	case conn:
		return New(s.ForkDBTX()), nil
	default:
		return nil, xerrors.Errorf("database store %T does not support fork queries", db)
	}
}

// InTx runs fn in one transaction of db. tx runs upstream queries and ftx
// runs fork queries, both in that transaction.
func InTx(db database.Store, fn func(tx database.Store, ftx Store) error, opts *database.TxOptions) error {
	return db.InTx(func(tx database.Store) error {
		ftx, err := From(tx)
		if err != nil {
			return xerrors.Errorf("fork queries of transaction: %w", err)
		}
		return fn(tx, ftx)
	}, opts)
}
