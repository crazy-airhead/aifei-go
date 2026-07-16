package db

import (
	"context"
	"database/sql"
)

// Transaction executes fn in a database transaction on the default config.
//
// fn receives a context.Context carrying the active *sql.Tx; every db call
// inside fn that propagates this ctx (e.g. db.WithCtx(ctx)) participates in the
// same transaction. If fn returns a non-nil error the transaction is rolled
// back, otherwise it is committed.
//
// Go has no thread-local storage, so joining the transaction is explicit: pass
// the ctx received by fn to every db call. Calls that ignore ctx (e.g.
// db.Insert) use the pool and auto-commit independently, as before.
//
// NOTE: the callback signature changed from func() error to
// func(context.Context) error.
func Transaction(fn func(ctx context.Context) error) error {
	return TransactionCtx(context.Background(), fn)
}

// TransactionWithID is like Transaction but for a specific config ID.
func TransactionWithID(configID string, fn func(ctx context.Context) error) error {
	return TransactionCtxWithID(configID, context.Background(), fn)
}

// TransactionCtx executes fn in a transaction derived from ctx. The *sql.Tx is
// carried by the context passed to fn, so db calls that propagate it
// (db.WithCtx(ctx)) run in the same transaction. If ctx already carries a tx (a
// nested call), fn joins that outer transaction and is neither committed nor
// rolled back here — the error is simply propagated for the outermost owner to
// act on.
func TransactionCtx(ctx context.Context, fn func(ctx context.Context) error) error {
	return TransactionCtxWithID(defaultConfigID, ctx, fn)
}

// TransactionCtxWithID is like TransactionCtx but for a specific config ID.
func TransactionCtxWithID(configID string, ctx context.Context, fn func(ctx context.Context) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	// Nested call: join the outer transaction instead of beginning a new one.
	if _, ok := txFromContext(ctx); ok {
		return fn(ctx)
	}
	config := GetConfig(configID)
	pool, err := config.Pool()
	if err != nil {
		return err
	}
	tx, err := pool.Begin()
	if err != nil {
		return err
	}
	txCtx := withTx(ctx, tx)
	if err := fn(txCtx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// TxBegin begins a transaction and returns *sql.Tx for manual management. Pair
// it with db.WithTx to propagate the tx via context:
//
//	tx, _ := db.TxBegin()
//	ctx := db.WithTx(c, tx)
//	db.WithCtx(ctx).InsertRow(row)
//	tx.Commit() // or tx.Rollback()
func TxBegin(configID ...string) (*sql.Tx, error) {
	config := GetConfig(configID...)
	pool, err := config.Pool()
	if err != nil {
		return nil, err
	}
	return pool.Begin()
}
