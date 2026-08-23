package db_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/crazy-airhead/aifei-go/db"

	_ "modernc.org/sqlite"
)

// TestTransactionIsolationOpts verifies the TxOption variadic path: explicit
// isolation opts go through BeginTx without disturbing commit/rollback
// semantics, and nested calls still join the outer transaction.
func TestTransactionIsolationOpts(t *testing.T) {
	setupTestDB(t)

	// Commit path with explicit default isolation.
	err := db.TransactionCtx(context.Background(), func(ctx context.Context) error {
		_, err := db.WithCtx(ctx).RawSql("SELECT COUNT(*) AS c FROM user").FindFirst()
		return err
	}, db.WithIsolation(sql.LevelDefault))
	if err != nil {
		t.Fatalf("commit path with opts: %v", err)
	}

	// Rollback path with opts: the insert must be undone.
	err = db.TransactionCtx(context.Background(), func(ctx context.Context) error {
		row := db.NewRow("user").Set("name", "isolation").Set("age", 1).Set("email", "i@t.com")
		if _, err := db.InsertCtx(ctx, row); err != nil {
			return err
		}
		return errors.New("force rollback")
	}, db.WithIsolation(sql.LevelDefault))
	if err == nil || err.Error() != "force rollback" {
		t.Fatalf("expected propagated error, got %v", err)
	}
	row, err := db.FindBy("user", "name", "isolation")
	if err != nil {
		t.Fatal(err)
	}
	if row != nil {
		t.Fatal("expected rolled-back insert to be absent")
	}

	// Nested call joins the outer transaction; inner opts are irrelevant.
	err = db.TransactionCtx(context.Background(), func(ctx context.Context) error {
		return db.TransactionCtx(ctx, func(ctx context.Context) error {
			_, err := db.WithCtx(ctx).RawSql("SELECT COUNT(*) AS c FROM user").FindFirst()
			return err
		}, db.WithIsolation(sql.LevelSerializable))
	}, db.WithIsolation(sql.LevelDefault))
	if err != nil {
		t.Fatalf("nested join: %v", err)
	}
}

// TestTransactionOfIsolationOpts covers the typed-result variant with opts.
func TestTransactionOfIsolationOpts(t *testing.T) {
	setupTestDB(t)

	type result struct {
		count int64
	}
	r, err := db.TransactionOfCtx(context.Background(), func(ctx context.Context, tx *db.Tx) (result, error) {
		row, err := db.WithCtx(ctx).RawSql("SELECT COUNT(*) AS c FROM user").FindFirst()
		if err != nil {
			return result{}, err
		}
		return result{count: row.GetInt64("c")}, nil
	}, db.WithIsolation(sql.LevelDefault))
	if err != nil {
		t.Fatal(err)
	}
	if r.count < 0 {
		t.Fatalf("unexpected count %d", r.count)
	}
}
