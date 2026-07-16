package db

import (
	"context"
	"database/sql"
)

// DBConn abstracts the Exec/Query/Prepare methods common to both *sql.DB (the
// connection pool) and *sql.Tx (a single transaction). Executors obtain a
// DBConn via Config.runner(ctx): inside a transaction the *sql.Tx stored in ctx
// is returned; otherwise the pool is used. This lets every executor participate
// in an outer transaction transparently once the caller propagates the tx via
// context (see Transaction / WithCtx).
type DBConn interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
	Query(query string, args ...interface{}) (*sql.Rows, error)
	QueryRow(query string, args ...interface{}) *sql.Row
	Prepare(query string) (*sql.Stmt, error)
}

// Compile-time guarantees that the pool and a transaction both satisfy DBConn.
var (
	_ DBConn = (*sql.DB)(nil)
	_ DBConn = (*sql.Tx)(nil)
)

// txKey is the unexported context key carrying the active *sql.Tx.
type txKey struct{}

// withTx returns a ctx carrying tx. If tx is nil, ctx is returned unchanged.
// A nil ctx is treated as context.Background() so callers (e.g. a freshly
// created Dao with no ctx) never hit "cannot create context from nil parent".
func withTx(ctx context.Context, tx *sql.Tx) context.Context {
	if tx == nil {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, txKey{}, tx)
}

// txFromContext returns the *sql.Tx carried by ctx, if any. A nil ctx is safe
// and reports no transaction.
func txFromContext(ctx context.Context) (*sql.Tx, bool) {
	if ctx == nil {
		return nil, false
	}
	tx, ok := ctx.Value(txKey{}).(*sql.Tx)
	return tx, ok
}

// WithTx returns a derivative of ctx that carries tx so that db.WithCtx(ctx)
// (and every executor reached through it) reuses tx instead of the pool. This is
// the escape hatch for callers that manage a transaction manually via TxBegin.
// Passing a nil tx leaves ctx unchanged.
func WithTx(ctx context.Context, tx *sql.Tx) context.Context {
	return withTx(ctx, tx)
}

// TxFromContext reports the *sql.Tx carried by ctx, if any.
func TxFromContext(ctx context.Context) (*sql.Tx, bool) {
	return txFromContext(ctx)
}
