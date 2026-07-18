package db

import (
	"context"
	"database/sql"
	"errors"
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

// RollbackDecision lets a transaction's typed result request a rollback on the
// success path (no error). If the value returned by an atom implements it and
// ShouldRollback() reports true, the transaction is rolled back instead of
// committed (对照 Java transaction/RollbackDecision）。
//
// server.Out implements ShouldRollback() (true when code != 0), so a service can
// return its business result from a transaction and let the code drive rollback.
type RollbackDecision interface {
	ShouldRollback() bool
}

// ErrRollback is returned (alongside the atom's own result) when a transaction
// was actively rolled back: the atom called tx.Rollback(), or the returned result
// implemented RollbackDecision with ShouldRollback()==true.
var ErrRollback = errors.New("transaction rolled back")

// Tx is the control handle passed into an atom so it can actively request a
// rollback on the success path without returning an error (对照 Java
// Transaction.rollback()).
type Tx struct {
	rollback bool
}

// Rollback marks the transaction for rollback. The atom may still return its
// business result; TransactionOf returns (result, ErrRollback).
func (t *Tx) Rollback() { t.rollback = true }

// TransactionOf executes fn in a transaction on the default config and returns
// its typed business result. The *sql.Tx is propagated to fn via context, so
// every db call inside fn that propagates ctx (db.WithCtx(ctx)) participates.
//
// The transaction commits unless any of the following holds, in which case it is
// rolled back and (result, ErrRollback) — or (zero, err) — is returned:
//   - fn returns a non-nil error;
//   - the atom called tx.Rollback();
//   - the returned result implements RollbackDecision with ShouldRollback()==true.
//
// Nested calls (ctx already carries a tx) join the outer transaction; an active
// rollback mark or error is still surfaced to the caller so the outer owner can
// react.
func TransactionOf[R any](fn func(ctx context.Context, tx *Tx) (R, error)) (R, error) {
	return TransactionOfCtx(context.Background(), fn)
}

// TransactionOfWithID is like TransactionOf but for a specific config ID.
func TransactionOfWithID[R any](configID string, fn func(ctx context.Context, tx *Tx) (R, error)) (R, error) {
	return TransactionOfCtxWithID(configID, context.Background(), fn)
}

// TransactionOfCtx is like TransactionOf but derived from ctx.
func TransactionOfCtx[R any](ctx context.Context, fn func(ctx context.Context, tx *Tx) (R, error)) (R, error) {
	return TransactionOfCtxWithID(defaultConfigID, ctx, fn)
}

// TransactionOfCtxWithID is like TransactionOfCtx but for a specific config ID.
func TransactionOfCtxWithID[R any](configID string, ctx context.Context, fn func(ctx context.Context, tx *Tx) (R, error)) (R, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// Nested call: join the outer transaction instead of beginning a new one.
	if _, ok := txFromContext(ctx); ok {
		return runAtom(ctx, fn)
	}
	config := GetConfig(configID)
	pool, err := config.Pool()
	if err != nil {
		var zero R
		return zero, err
	}
	tx, err := pool.Begin()
	if err != nil {
		var zero R
		return zero, err
	}
	txCtx := withTx(ctx, tx)
	result, commit, err := runAtomDecision(txCtx, fn)
	if err != nil || !commit {
		tx.Rollback()
		if err != nil {
			return result, err
		}
		return result, ErrRollback
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}

// runAtom runs fn against an already-joined (nested) transaction. An active
// rollback mark or error is surfaced to the caller so the outer owner can react;
// commit/rollback itself is the outer owner's responsibility.
func runAtom[R any](ctx context.Context, fn func(ctx context.Context, tx *Tx) (R, error)) (R, error) {
	result, _, err := runAtomDecision(ctx, fn)
	return result, err
}

// runAtomDecision runs fn and reports whether the transaction may commit:
// commit==false (with a nil error) means an active rollback was requested.
func runAtomDecision[R any](ctx context.Context, fn func(ctx context.Context, tx *Tx) (R, error)) (R, bool, error) {
	t := &Tx{}
	result, err := fn(ctx, t)
	if err != nil {
		return result, false, err
	}
	if t.rollback {
		return result, false, nil
	}
	if rd, ok := any(result).(RollbackDecision); ok && rd.ShouldRollback() {
		return result, false, nil
	}
	return result, true, nil
}
