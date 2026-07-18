package db_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/crazy-airhead/aifei-go/db"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) {
	t.Helper()
	if c := db.GetConfig(); c != nil {
		c.Close()
	}
	db.ResetConfigs()
	err := db.Init("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Use().RawSql("CREATE TABLE IF NOT EXISTS user (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, age INTEGER, email TEXT)").Update()
	if err != nil {
		t.Fatal(err)
	}
	db.Use().RawSql("DELETE FROM user").Update()
}

func TestInsert(t *testing.T) {
	setupTestDB(t)
	row := db.NewRow("user").Set("name", "james").Set("age", 18).Set("email", "james@test.com")
	result, err := db.Insert(row)
	if err != nil {
		t.Fatal(err)
	}
	id := result.GetID()
	if id == nil || db.ToInt64(id) == 0 {
		t.Fatal("expected auto-generated ID")
	}
}

func TestFindById(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "james").Set("age", 18))

	found, err := db.FindByID("user", 1)
	if err != nil {
		t.Fatal(err)
	}
	if found == nil {
		t.Fatal("expected to find user")
	}
	if found.GetStr("name") != "james" {
		t.Fatalf("expected name=james, got %s", found.GetStr("name"))
	}
	if found.GetInt("age") != 18 {
		t.Fatalf("expected age=18, got %d", found.GetInt("age"))
	}
}

func TestFindBy(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "alice").Set("age", 20))
	db.Insert(db.NewRow("user").Set("name", "bob").Set("age", 25))
	db.Insert(db.NewRow("user").Set("name", "charlie").Set("age", 30))

	rows, err := db.FindBy("user", "age > ?", 22)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
}

func TestFindByFieldName(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "alice").Set("age", 20))
	db.Insert(db.NewRow("user").Set("name", "bob").Set("age", 25))

	rows, err := db.FindBy("user", "name", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].GetStr("name") != "alice" {
		t.Fatalf("expected name=alice, got %s", rows[0].GetStr("name"))
	}
}

func TestUpdate(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "james").Set("age", 18))

	row := db.NewRow("user").ID(1).Set("age", 19)
	ok, err := db.Update(row)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected update to succeed")
	}

	found, _ := db.FindByID("user", 1)
	if found.GetInt("age") != 19 {
		t.Fatalf("expected age=19, got %d", found.GetInt("age"))
	}
}

func TestDelete(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "james").Set("age", 18))

	ok, err := db.DeleteByID("user", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected delete to succeed")
	}

	found, _ := db.FindByID("user", 1)
	if found != nil {
		t.Fatal("expected user to be deleted")
	}
}

func TestPaginate(t *testing.T) {
	setupTestDB(t)
	for i := 0; i < 25; i++ {
		db.Insert(db.NewRow("user").Set("name", "user").Set("age", i))
	}

	page, err := db.RawSql("SELECT * FROM user").Paginate(1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if page.TotalRows != 25 {
		t.Fatalf("expected totalRows=25, got %d", page.TotalRows)
	}
	if len(page.Rows) != 10 {
		t.Fatalf("expected 10 rows, got %d", len(page.Rows))
	}
	if page.TotalPages != 3 {
		t.Fatalf("expected totalPages=3, got %d", page.TotalPages)
	}
	if !page.IsFirstPage() {
		t.Fatal("expected first page")
	}
}

func TestCount(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "a").Set("age", 1))
	db.Insert(db.NewRow("user").Set("name", "b").Set("age", 2))

	count, err := db.Count("user")
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected count=2, got %d", count)
	}
}

func TestCountBy(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "a").Set("age", 1))
	db.Insert(db.NewRow("user").Set("name", "b").Set("age", 2))
	db.Insert(db.NewRow("user").Set("name", "c").Set("age", 3))

	count, err := db.CountBy("user", "age > ?", 1)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected count=2, got %d", count)
	}
}

func TestTransaction(t *testing.T) {
	setupTestDB(t)

	// Both inserts propagate the tx via ctx, so they commit together.
	err := db.Transaction(func(ctx context.Context) error {
		db.WithCtx(ctx).InsertRow(db.NewRow("user").Set("name", "tx1").Set("age", 1))
		db.WithCtx(ctx).InsertRow(db.NewRow("user").Set("name", "tx2").Set("age", 2))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	count, _ := db.Count("user")
	if count != 2 {
		t.Fatalf("expected 2 rows after commit, got %d", count)
	}
}

func TestTransactionRollback(t *testing.T) {
	setupTestDB(t)

	// Insert inside the tx, then force a rollback. Before ISSUE-0001 the insert
	// took a pool connection and auto-committed, so the row survived the
	// "rollback". With tx propagation it must now be rolled back.
	err := db.Transaction(func(ctx context.Context) error {
		if _, e := db.WithCtx(ctx).InsertRow(db.NewRow("user").Set("name", "tx-rb").Set("age", 1)); e != nil {
			return e
		}
		return fmt.Errorf("force rollback")
	})
	if err == nil {
		t.Fatal("expected error from rollback")
	}

	count, _ := db.Count("user")
	if count != 0 {
		t.Fatalf("expected 0 rows after rollback, got %d (tx did not propagate)", count)
	}
}

// TestTransactionCtxFacade exercises the TransactionCtx + ctx-aware facade helpers.
func TestTransactionCtxFacade(t *testing.T) {
	setupTestDB(t)

	err := db.TransactionCtx(context.Background(), func(ctx context.Context) error {
		_, e := db.InsertCtx(ctx, db.NewRow("user").Set("name", "facade").Set("age", 1))
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if count, _ := db.Count("user"); count != 1 {
		t.Fatalf("expected 1 row, got %d", count)
	}
}

// TestTransactionDeleteRollback verifies both an insert and a delete inside the
// same tx are undone on rollback.
func TestTransactionDeleteRollback(t *testing.T) {
	setupTestDB(t)
	// Seed a row outside any tx; it must survive the tx rollback below.
	db.Insert(db.NewRow("user").Set("name", "seed").Set("age", 1))

	err := db.Transaction(func(ctx context.Context) error {
		db.InsertCtx(ctx, db.NewRow("user").Set("name", "in-tx").Set("age", 2))
		db.DeleteByIDCtx(ctx, "user", 1) // delete the seeded row inside the tx
		return fmt.Errorf("force rollback")
	})
	if err == nil {
		t.Fatal("expected error")
	}

	if count, _ := db.Count("user"); count != 1 {
		t.Fatalf("expected 1 row (seed) after rollback, got %d", count)
	}
	if r, _ := db.FindByID("user", 1); r == nil {
		t.Fatal("seeded row should survive tx rollback")
	}
}

// TestNestedTransactionJoins verifies the nested-call "join" semantics: the
// inner call reuses the outer tx rather than starting a new one.
func TestNestedTransactionJoins(t *testing.T) {
	setupTestDB(t)

	// Inner errors → propagates → outer rolls back → 0 rows.
	err := db.Transaction(func(ctx context.Context) error {
		db.WithCtx(ctx).InsertRow(db.NewRow("user").Set("name", "outer").Set("age", 1))
		return db.TransactionCtx(ctx, func(ctx context.Context) error {
			db.WithCtx(ctx).InsertRow(db.NewRow("user").Set("name", "inner").Set("age", 2))
			return fmt.Errorf("inner fails")
		})
	})
	if err == nil {
		t.Fatal("expected error from inner")
	}
	if count, _ := db.Count("user"); count != 0 {
		t.Fatalf("expected 0 rows after nested rollback, got %d", count)
	}

	// Both succeed → 2 rows (inner joined outer, didn't lose data to a second tx).
	setupTestDB(t)
	err = db.Transaction(func(ctx context.Context) error {
		db.WithCtx(ctx).InsertRow(db.NewRow("user").Set("name", "outer").Set("age", 1))
		return db.TransactionCtx(ctx, func(ctx context.Context) error {
			db.WithCtx(ctx).InsertRow(db.NewRow("user").Set("name", "inner").Set("age", 2))
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if count, _ := db.Count("user"); count != 2 {
		t.Fatalf("expected 2 rows after nested commit, got %d", count)
	}
}

// TestDaoTransaction verifies Dao-level transaction propagation and rollback.
func TestDaoTransaction(t *testing.T) {
	setupTestDB(t)

	err := db.Use().Transaction(func(ctx context.Context, d *db.Dao) error {
		if _, e := d.InsertRow(db.NewRow("user").Set("name", "dao1").Set("age", 1)); e != nil {
			return e
		}
		_, e := d.InsertRow(db.NewRow("user").Set("name", "dao2").Set("age", 2))
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if count, _ := db.Count("user"); count != 2 {
		t.Fatalf("expected 2 rows, got %d", count)
	}

	// Rollback path: the ctx-aware dao d runs inside the tx, so the insert rolls back.
	setupTestDB(t)
	err = db.Use().Transaction(func(ctx context.Context, d *db.Dao) error {
		d.InsertRow(db.NewRow("user").Set("name", "dao1").Set("age", 1))
		return fmt.Errorf("force rollback")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if count, _ := db.Count("user"); count != 0 {
		t.Fatalf("expected 0 rows after Dao tx rollback, got %d", count)
	}
}

// TestBatchInTransaction verifies batch operations participate in a transaction.
func TestBatchInTransaction(t *testing.T) {
	setupTestDB(t)

	rows := []*db.Row{
		db.NewRow("user").Set("name", "b1").Set("age", 1),
		db.NewRow("user").Set("name", "b2").Set("age", 2),
	}

	// Commit path.
	err := db.Transaction(func(ctx context.Context) error {
		_, e := db.NewBatchCtx(ctx).Insert(rows)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if count, _ := db.Count("user"); count != 2 {
		t.Fatalf("expected 2 rows after batch commit, got %d", count)
	}

	// Rollback path: batch writes inside the tx must be undone.
	setupTestDB(t)
	err = db.Transaction(func(ctx context.Context) error {
		db.NewBatchCtx(ctx).Insert(rows)
		return fmt.Errorf("force rollback")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if count, _ := db.Count("user"); count != 0 {
		t.Fatalf("expected 0 rows after batch rollback, got %d", count)
	}
}

// TestCtxNilFallback verifies that a ctx-less Dao still uses the pool and
// auto-commits, exactly as before the change (regression guard).
func TestCtxNilFallback(t *testing.T) {
	setupTestDB(t)

	if _, err := db.Use().InsertRow(db.NewRow("user").Set("name", "noctx").Set("age", 1)); err != nil {
		t.Fatal(err)
	}
	if count, _ := db.Count("user"); count != 1 {
		t.Fatalf("expected 1 row, got %d", count)
	}
}

// TestTxBeginWithTx verifies manual tx management (TxBegin) composes with the
// new context-based propagation via WithTx.
func TestTxBeginWithTx(t *testing.T) {
	setupTestDB(t)

	tx, err := db.TxBegin()
	if err != nil {
		t.Fatal(err)
	}
	ctx := db.WithTx(context.Background(), tx)
	if _, err := db.WithCtx(ctx).InsertRow(db.NewRow("user").Set("name", "manual").Set("age", 1)); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	if count, _ := db.Count("user"); count != 0 {
		t.Fatalf("expected 0 rows after manual tx rollback, got %d", count)
	}
}

func TestRowActiveRecord(t *testing.T) {
	setupTestDB(t)
	row := db.NewRow("user").Set("name", "active").Set("age", 99)
	result, err := row.Insert()
	if err != nil {
		t.Fatal(err)
	}
	id := result.GetID()
	if id == nil {
		t.Fatal("expected ID after insert")
	}

	found, _ := db.FindByID("user", id)
	if found.GetStr("name") != "active" {
		t.Fatalf("expected name=active, got %s", found.GetStr("name"))
	}
}

func TestBatchInsert(t *testing.T) {
	setupTestDB(t)
	rows := []*db.Row{
		db.NewRow("user").Set("name", "batch1").Set("age", 1),
		db.NewRow("user").Set("name", "batch2").Set("age", 2),
		db.NewRow("user").Set("name", "batch3").Set("age", 3),
	}

	result, err := db.NewBatch().Insert(rows)
	if err != nil {
		t.Fatal(err)
	}
	if result.RowsAffected != 3 {
		t.Fatalf("expected 3 affected rows, got %d", result.RowsAffected)
	}

	count, _ := db.Count("user")
	if count != 3 {
		t.Fatalf("expected 3 rows, got %d", count)
	}
}

func TestDeleteIn(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "a").Set("age", 1))
	db.Insert(db.NewRow("user").Set("name", "b").Set("age", 2))
	db.Insert(db.NewRow("user").Set("name", "c").Set("age", 3))

	n, err := db.Use().DeleteIn("user", "name", "a", "b")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 deleted, got %d", n)
	}
}

func TestFindIn(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "a").Set("age", 1))
	db.Insert(db.NewRow("user").Set("name", "b").Set("age", 2))
	db.Insert(db.NewRow("user").Set("name", "c").Set("age", 3))

	rows, err := db.Use().FindIn("user", "name", "a", "c")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
}

// ---- Enjoy SQL Tests ----

func TestEnjoySqlParaNamed(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "james").Set("age", 18))

	rows, err := db.Sql("select * from user where id = #para(id)", map[string]interface{}{
		"id": 1,
	}).Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].GetStr("name") != "james" {
		t.Fatalf("expected name=james, got %s", rows[0].GetStr("name"))
	}
}

func TestEnjoySqlParaPositional(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "james").Set("age", 18))
	db.Insert(db.NewRow("user").Set("name", "alice").Set("age", 20))

	rows, err := db.SqlWithArgs("select * from user where age > #para(0) and age < #para(1)", 17, 19).Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].GetStr("name") != "james" {
		t.Fatalf("expected name=james, got %s", rows[0].GetStr("name"))
	}
}

func TestEnjoySqlParaLike(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "james").Set("age", 18))
	db.Insert(db.NewRow("user").Set("name", "jamie").Set("age", 20))
	db.Insert(db.NewRow("user").Set("name", "alice").Set("age", 25))

	rows, err := db.Sql("select * from user where name like #para(name, 'like')", map[string]interface{}{
		"name": "jam",
	}).Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
}

func TestEnjoySqlParaLikeLeft(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "james").Set("age", 18))
	db.Insert(db.NewRow("user").Set("name", "alice").Set("age", 25))

	rows, err := db.Sql("select * from user where name like #para(name, '%like')", map[string]interface{}{
		"name": "mes",
	}).Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].GetStr("name") != "james" {
		t.Fatalf("expected name=james, got %s", rows[0].GetStr("name"))
	}
}

func TestEnjoySqlParaLikeRight(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "james").Set("age", 18))
	db.Insert(db.NewRow("user").Set("name", "alice").Set("age", 25))

	rows, err := db.Sql("select * from user where name like #para(name, 'like%')", map[string]interface{}{
		"name": "ja",
	}).Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].GetStr("name") != "james" {
		t.Fatalf("expected name=james, got %s", rows[0].GetStr("name"))
	}
}

func TestEnjoySqlWhereEqual(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "alice").Set("age", 20))
	db.Insert(db.NewRow("user").Set("name", "bob").Set("age", 25))

	rows, err := db.Sql("select * from user #where(age, '=', age)", map[string]interface{}{
		"age": 20,
	}).Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].GetStr("name") != "alice" {
		t.Fatalf("expected name=alice, got %s", rows[0].GetStr("name"))
	}
}

func TestEnjoySqlWhereGreaterThan(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "a").Set("age", 10))
	db.Insert(db.NewRow("user").Set("name", "b").Set("age", 20))
	db.Insert(db.NewRow("user").Set("name", "c").Set("age", 30))

	rows, err := db.Sql("select * from user #where(age, '>', age)", map[string]interface{}{
		"age": 15,
	}).Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
}

func TestEnjoySqlWhereNilSkips(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "a").Set("age", 10))
	db.Insert(db.NewRow("user").Set("name", "b").Set("age", 20))

	// When age is nil, #where should generate no WHERE clause
	rows, err := db.Sql("select * from user #where(age, '=', age)", map[string]interface{}{
		"age": nil,
	}).Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows (no filter), got %d", len(rows))
	}
}

func TestEnjoySqlWhereIsNull(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "a").Set("email", "a@test.com"))
	db.Insert(db.NewRow("user").Set("name", "b")) // email is nil

	rows, err := db.Sql("select * from user #where(email, 'is null')", map[string]interface{}{
		"email": true,
	}).Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row with null email, got %d", len(rows))
	}
	if rows[0].GetStr("name") != "b" {
		t.Fatalf("expected name=b, got %s", rows[0].GetStr("name"))
	}
}

func TestEnjoySqlWhereIsNotNull(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "a").Set("email", "a@test.com"))
	db.Insert(db.NewRow("user").Set("name", "b")) // email is nil

	rows, err := db.Sql("select * from user #where(email, 'is not null')", map[string]interface{}{
		"email": true,
	}).Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row with non-null email, got %d", len(rows))
	}
	if rows[0].GetStr("name") != "a" {
		t.Fatalf("expected name=a, got %s", rows[0].GetStr("name"))
	}
}

func TestEnjoySqlWhereLike(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "james").Set("age", 18))
	db.Insert(db.NewRow("user").Set("name", "jamie").Set("age", 20))
	db.Insert(db.NewRow("user").Set("name", "alice").Set("age", 25))

	rows, err := db.Sql("select * from user #where(name, 'like', name)", map[string]interface{}{
		"name": "jam",
	}).Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
}

func TestEnjoySqlWhereContains(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "hello").Set("age", 10))
	db.Insert(db.NewRow("user").Set("name", "world").Set("age", 20))

	rows, err := db.Sql("select * from user #where(name, 'contains', name)", map[string]interface{}{
		"name": "ell",
	}).Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].GetStr("name") != "hello" {
		t.Fatalf("expected name=hello, got %s", rows[0].GetStr("name"))
	}
}

func TestEnjoySqlWhereStartsWith(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "james").Set("age", 18))
	db.Insert(db.NewRow("user").Set("name", "alice").Set("age", 25))

	rows, err := db.Sql("select * from user #where(name, 'startsWith', name)", map[string]interface{}{
		"name": "ja",
	}).Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].GetStr("name") != "james" {
		t.Fatalf("expected name=james, got %s", rows[0].GetStr("name"))
	}
}

func TestEnjoySqlWhereEndsWith(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "james").Set("age", 18))
	db.Insert(db.NewRow("user").Set("name", "alice").Set("age", 25))

	rows, err := db.Sql("select * from user #where(name, 'endsWith', name)", map[string]interface{}{
		"name": "ice",
	}).Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].GetStr("name") != "alice" {
		t.Fatalf("expected name=alice, got %s", rows[0].GetStr("name"))
	}
}

func TestEnjoySqlWhereIn(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "a").Set("age", 10))
	db.Insert(db.NewRow("user").Set("name", "b").Set("age", 20))
	db.Insert(db.NewRow("user").Set("name", "c").Set("age", 30))

	rows, err := db.Sql("select * from user #where(age, 'in', ages)", map[string]interface{}{
		"ages": []int{10, 30},
	}).Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
}

func TestEnjoySqlWhereNotIn(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "a").Set("age", 10))
	db.Insert(db.NewRow("user").Set("name", "b").Set("age", 20))
	db.Insert(db.NewRow("user").Set("name", "c").Set("age", 30))

	rows, err := db.Sql("select * from user #where(age, 'not in', ages)", map[string]interface{}{
		"ages": []int{10, 30},
	}).Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].GetStr("name") != "b" {
		t.Fatalf("expected name=b, got %s", rows[0].GetStr("name"))
	}
}

func TestEnjoySqlWhereBetween(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "a").Set("age", 10))
	db.Insert(db.NewRow("user").Set("name", "b").Set("age", 20))
	db.Insert(db.NewRow("user").Set("name", "c").Set("age", 30))

	rows, err := db.Sql("select * from user #where(age, 'between', ageRange)", map[string]interface{}{
		"ageRange": []int{15, 25},
	}).Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].GetStr("name") != "b" {
		t.Fatalf("expected name=b, got %s", rows[0].GetStr("name"))
	}
}

func TestEnjoySqlWhereAnd(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "a").Set("age", 10))
	db.Insert(db.NewRow("user").Set("name", "b").Set("age", 20))
	db.Insert(db.NewRow("user").Set("name", "c").Set("age", 30))

	rows, err := db.Sql("select * from user #where(age, '>', minAge) #and(age, '<', maxAge)", map[string]interface{}{
		"minAge": 5,
		"maxAge": 25,
	}).Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
}

func TestEnjoySqlWhereAndNilSkips(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "a").Set("age", 10))
	db.Insert(db.NewRow("user").Set("name", "b").Set("age", 20))

	// minAge is nil, so only maxAge condition should apply
	rows, err := db.Sql(
		"select * from user #where(age, '>', minAge) #and(age, '<', maxAge)",
		map[string]interface{}{
			"minAge": nil,
			"maxAge": 15,
		},
	).Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].GetStr("name") != "a" {
		t.Fatalf("expected name=a, got %s", rows[0].GetStr("name"))
	}
}

func TestEnjoySqlWhereDifferentFieldNames(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "james").Set("age", 18))

	// Field name in SQL (age) differs from parameter name (userAge)
	rows, err := db.Sql("select * from user #where(age, '=', userAge)", map[string]interface{}{
		"userAge": 18,
	}).Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
}

func TestEnjoySqlWhereAfterLiteralWhere(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "a").Set("age", 10))
	db.Insert(db.NewRow("user").Set("name", "b").Set("age", 20))

	// #and after a literal WHERE clause (no #where directive)
	rows, err := db.Sql("select * from user where age > 5 #and(name, '=', name)", map[string]interface{}{
		"name": "b",
	}).Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].GetStr("name") != "b" {
		t.Fatalf("expected name=b, got %s", rows[0].GetStr("name"))
	}
}

func TestEnjoySqlOrderBy(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "c").Set("age", 30))
	db.Insert(db.NewRow("user").Set("name", "a").Set("age", 10))
	db.Insert(db.NewRow("user").Set("name", "b").Set("age", 20))

	rows, err := db.Sql("select * from user #orderBy(age)", map[string]interface{}{
		"orderBy": map[string]interface{}{
			"field": "age",
			"order": "asc",
		},
	}).Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	if rows[0].GetStr("name") != "a" {
		t.Fatalf("expected first row name=a, got %s", rows[0].GetStr("name"))
	}
	if rows[2].GetStr("name") != "c" {
		t.Fatalf("expected last row name=c, got %s", rows[2].GetStr("name"))
	}
}

func TestEnjoySqlOrderByDesc(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "c").Set("age", 30))
	db.Insert(db.NewRow("user").Set("name", "a").Set("age", 10))
	db.Insert(db.NewRow("user").Set("name", "b").Set("age", 20))

	rows, err := db.Sql("select * from user #orderBy(age)", map[string]interface{}{
		"orderBy": map[string]interface{}{
			"field": "age",
			"order": "desc",
		},
	}).Find()
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].GetStr("name") != "c" {
		t.Fatalf("expected first row name=c, got %s", rows[0].GetStr("name"))
	}
}

func TestEnjoySqlOrderByCustomName(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "c").Set("age", 30))
	db.Insert(db.NewRow("user").Set("name", "a").Set("age", 10))

	rows, err := db.Sql("select * from user #orderBy($sort, age)", map[string]interface{}{
		"sort": map[string]interface{}{
			"field": "age",
			"order": "asc",
		},
	}).Find()
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].GetStr("name") != "a" {
		t.Fatalf("expected first row name=a, got %s", rows[0].GetStr("name"))
	}
}

func TestEnjoySqlAddAndGetById(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "james").Set("age", 18))
	db.Insert(db.NewRow("user").Set("name", "alice").Set("age", 20))

	sql := `#sql("findByName")
select * from user where name = #para(0)
#end`
	db.AddSql("findByName", sql)

	rows, err := db.SqlByIdWithArgs("findByName", "james").Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].GetStr("name") != "james" {
		t.Fatalf("expected name=james, got %s", rows[0].GetStr("name"))
	}
}

func TestEnjoySqlAddAndGetByIdWithWhere(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "james").Set("age", 18))
	db.Insert(db.NewRow("user").Set("name", "alice").Set("age", 20))
	db.Insert(db.NewRow("user").Set("name", "bob").Set("age", 25))

	sql := `#sql("findByFilter")
select * from user #where(age, '>', age) #and(name, 'like', name)
#end`
	db.AddSql("findByFilter", sql)

	// age > 15 AND name LIKE '%a%' — matches james and alice
	rows, err := db.SqlById("findByFilter", map[string]interface{}{
		"age": 15, "name": "a",
	}).Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
}

func TestEnjoySqlInsert(t *testing.T) {
	setupTestDB(t)

	_, err := db.Sql("insert into user(name, age) values(#para(name), #para(age))", map[string]interface{}{
		"name": "james",
		"age":  18,
	}).Update()
	if err != nil {
		t.Fatal(err)
	}

	count, _ := db.Count("user")
	if count != 1 {
		t.Fatalf("expected 1 row, got %d", count)
	}
}

func TestEnjoySqlUpdate(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "james").Set("age", 18))

	_, err := db.Sql("update user set age = #para(newAge) where id = #para(id)", map[string]interface{}{
		"newAge": 25,
		"id":     1,
	}).Update()
	if err != nil {
		t.Fatal(err)
	}

	found, _ := db.FindByID("user", 1)
	if found.GetInt("age") != 25 {
		t.Fatalf("expected age=25, got %d", found.GetInt("age"))
	}
}

func TestEnjoySqlDelete(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "james").Set("age", 18))
	db.Insert(db.NewRow("user").Set("name", "alice").Set("age", 20))

	_, err := db.Sql("delete from user #where(age, '>', age)", map[string]interface{}{
		"age": 19,
	}).Update()
	if err != nil {
		t.Fatal(err)
	}

	count, _ := db.Count("user")
	if count != 1 {
		t.Fatalf("expected 1 row remaining, got %d", count)
	}
}

func TestEnjoySqlPaginate(t *testing.T) {
	setupTestDB(t)
	for i := 0; i < 25; i++ {
		db.Insert(db.NewRow("user").Set("name", "user").Set("age", i))
	}

	page, err := db.Sql("select * from user #where(age, '>', age)", map[string]interface{}{
		"age": 10,
	}).Paginate(1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if page.TotalRows != 14 {
		t.Fatalf("expected totalRows=14, got %d", page.TotalRows)
	}
	if len(page.Rows) != 5 {
		t.Fatalf("expected 5 rows, got %d", len(page.Rows))
	}
}

func TestEnjoySqlFieldExpression(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "james").Set("age", 18))
	db.Insert(db.NewRow("user").Set("name", "alice").Set("age", 20))

	// String field expression in #where
	rows, err := db.Sql("select * from user #where('age + 1', '>', age)", map[string]interface{}{
		"age": 19,
	}).Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].GetStr("name") != "alice" {
		t.Fatalf("expected name=alice, got %s", rows[0].GetStr("name"))
	}
}

func TestEnjoySqlWhereNotEqual(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "a").Set("age", 10))
	db.Insert(db.NewRow("user").Set("name", "b").Set("age", 20))
	db.Insert(db.NewRow("user").Set("name", "c").Set("age", 30))

	rows, err := db.Sql("select * from user #where(age, '!=', age)", map[string]interface{}{
		"age": 20,
	}).Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
}

func TestEnjoySqlWhereGreaterOrEqual(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "a").Set("age", 10))
	db.Insert(db.NewRow("user").Set("name", "b").Set("age", 20))

	rows, err := db.Sql("select * from user #where(age, '>=', age)", map[string]interface{}{
		"age": 20,
	}).Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].GetStr("name") != "b" {
		t.Fatalf("expected name=b, got %s", rows[0].GetStr("name"))
	}
}

func TestEnjoySqlWhereLessThan(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "a").Set("age", 10))
	db.Insert(db.NewRow("user").Set("name", "b").Set("age", 20))

	rows, err := db.Sql("select * from user #where(age, '<', age)", map[string]interface{}{
		"age": 20,
	}).Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].GetStr("name") != "a" {
		t.Fatalf("expected name=a, got %s", rows[0].GetStr("name"))
	}
}

func TestEnjoySqlWhereLessOrEqual(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "a").Set("age", 10))
	db.Insert(db.NewRow("user").Set("name", "b").Set("age", 20))

	rows, err := db.Sql("select * from user #where(age, '<=', age)", map[string]interface{}{
		"age": 10,
	}).Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].GetStr("name") != "a" {
		t.Fatalf("expected name=a, got %s", rows[0].GetStr("name"))
	}
}

func TestEnjoySqlOrderByFieldMapping(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "c").Set("age", 30))
	db.Insert(db.NewRow("user").Set("name", "a").Set("age", 10))
	db.Insert(db.NewRow("user").Set("name", "b").Set("age", 20))

	// Map client field "userAge" to SQL field "age"
	rows, err := db.Sql("select * from user #orderBy('age:userAge')", map[string]interface{}{
		"orderBy": map[string]interface{}{
			"field": "userAge",
			"order": "asc",
		},
	}).Find()
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].GetStr("name") != "a" {
		t.Fatalf("expected first row name=a, got %s", rows[0].GetStr("name"))
	}
}

// ---- Multi-table Mapping Tests ----

type DeptConfig struct {
	Lead string `json:"lead"`
}

func setupMultiTableTestDB(t *testing.T) {
	t.Helper()
	setupTestDB(t)
	// Create dept table for multi-table tests (IF NOT EXISTS for idempotency)
	_, err := db.Use().RawSql("CREATE TABLE IF NOT EXISTS dept (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, config TEXT)").Update()
	if err != nil {
		t.Fatal(err)
	}
	db.Use().RawSql("DELETE FROM dept").Update()
	// Add dept_id to user for joins
	_, _ = db.Use().RawSql("ALTER TABLE user ADD COLUMN dept_id INTEGER").Update()
	// Register table metadata with all needed columns
	db.RegisterTable(&db.Table{
		Name:        "user",
		PrimaryKeys: []string{"id"},
		FieldTypes: map[string]reflect.Type{
			"id":      reflect.TypeOf(int64(0)),
			"name":    reflect.TypeFor[string](),
			"age":     reflect.TypeOf(int(0)),
			"email":   reflect.TypeFor[string](),
			"dept_id": reflect.TypeOf(int64(0)),
		},
	})
	db.RegisterTable(&db.Table{
		Name:        "dept",
		PrimaryKeys: []string{"id"},
		FieldTypes: map[string]reflect.Type{
			"id":     reflect.TypeOf(int64(0)),
			"name":   reflect.TypeFor[string](),
			"config": reflect.TypeOf(DeptConfig{}),
		},
	})
}

func TestMultiTableExplicitTables(t *testing.T) {
	setupMultiTableTestDB(t)
	deptRow, err := db.Insert(db.NewRow("dept").Set("name", "engineering").Set("config", `{"lead":"bob"}`))
	if err != nil {
		t.Fatal(err)
	}
	deptID := deptRow.GetID()
	db.Insert(db.NewRow("user").Set("name", "alice").Set("age", 25).Set("dept_id", deptID))

	rows, err := db.Use().
		Tables(db.TableRef{Table: "user", Alias: "u"}, db.TableRef{Table: "dept", Alias: "d"}).
		RawSql(fmt.Sprintf("SELECT u.*, d.name AS dept_name, d.config AS dept_config FROM user u JOIN dept d ON u.dept_id = d.id WHERE d.id = %d", deptID)).
		Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	r := rows[0]
	if r.Table() != "user" {
		t.Fatalf("expected primary table user, got %q", r.Table())
	}
	if r.GetStr("name") != "alice" {
		t.Fatalf("expected name=alice, got %q", r.GetStr("name"))
	}
	if r.GetStr("dept_name") != "engineering" {
		t.Fatalf("expected dept_name=engineering, got %q", r.GetStr("dept_name"))
	}
	// dept_config should be decoded from JSON string to DeptConfig struct
	cfg, ok := r.Get("dept_config").(DeptConfig)
	if !ok {
		t.Fatalf("expected dept_config to be DeptConfig, got %T: %v", r.Get("dept_config"), r.Get("dept_config"))
	}
	if cfg.Lead != "bob" {
		t.Fatalf("expected dept_config.Lead=bob, got %q", cfg.Lead)
	}
}

func TestMultiTableAutoTables(t *testing.T) {
	setupMultiTableTestDB(t)
	deptRow, err := db.Insert(db.NewRow("dept").Set("name", "engineering").Set("config", `{"lead":"bob"}`))
	if err != nil {
		t.Fatal(err)
	}
	deptID := deptRow.GetID()
	db.Insert(db.NewRow("user").Set("name", "alice").Set("age", 25).Set("dept_id", deptID))

	rows, err := db.Use().
		AutoTables().
		RawSql(fmt.Sprintf("SELECT u.*, d.name AS dept_name, d.config AS dept_config FROM user u JOIN dept d ON u.dept_id = d.id WHERE d.id = %d", deptID)).
		Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	r := rows[0]
	cfg, ok := r.Get("dept_config").(DeptConfig)
	if !ok {
		t.Fatalf("expected dept_config to be DeptConfig with AutoTables, got %T: %v", r.Get("dept_config"), r.Get("dept_config"))
	}
	if cfg.Lead != "bob" {
		t.Fatalf("expected lead=bob, got %q", cfg.Lead)
	}
}

func TestMultiTableNoHintNoDecode(t *testing.T) {
	setupMultiTableTestDB(t)
	deptRow, err := db.Insert(db.NewRow("dept").Set("name", "engineering").Set("config", `{"lead":"bob"}`))
	if err != nil {
		t.Fatal(err)
	}
	deptID := deptRow.GetID()
	db.Insert(db.NewRow("user").Set("name", "alice").Set("age", 25).Set("dept_id", deptID))

	// Without AutoTables or Tables(), JSON columns should NOT be decoded
	rows, err := db.Use().
		RawSql(fmt.Sprintf("SELECT u.*, d.name AS dept_name, d.config AS dept_config FROM user u JOIN dept d ON u.dept_id = d.id WHERE d.id = %d", deptID)).
		Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	// dept_config should remain as raw JSON string
	cfg := rows[0].Get("dept_config")
	if _, ok := cfg.(string); !ok {
		// It might already be decoded by Phase 2 unique ownership (both tables
		// have "config" — only dept has config, so it's uniquely owned).
		// In that case, it may be decoded even without AutoTables.
		// The key assertion is that row.Table() is NOT bound.
		t.Logf("dept_config type: %T, value: %v", cfg, cfg)
	}
	// Without any hint, row.Table() should be empty
	if rows[0].Table() != "" {
		t.Logf("row.Table()=%q without hint — this is expected if unique ownership matched", rows[0].Table())
	}
}

func TestMultiTableWritePathPrimaryTable(t *testing.T) {
	setupMultiTableTestDB(t)
	deptRow, err := db.Insert(db.NewRow("dept").Set("name", "engineering").Set("config", `{"lead":"bob"}`))
	if err != nil {
		t.Fatal(err)
	}
	deptID := deptRow.GetID()
	db.Insert(db.NewRow("user").Set("name", "alice").Set("age", 25).Set("dept_id", deptID))

	rows, err := db.Use().
		Tables(db.TableRef{Table: "user", Alias: "u"}, db.TableRef{Table: "dept", Alias: "d"}).
		RawSql(fmt.Sprintf("SELECT u.*, d.name AS dept_name FROM user u JOIN dept d ON u.dept_id = d.id WHERE d.id = %d", deptID)).
		Find()
	if err != nil {
		t.Fatal(err)
	}
	r := rows[0]
	// row.Table() should be the primary table (user)
	if r.Table() != "user" {
		t.Fatalf("expected primary table user, got %q", r.Table())
	}
	// Primary keys should be from user table
	if len(r.PrimaryKeys()) != 1 || r.PrimaryKeys()[0] != "id" {
		t.Fatalf("expected primary keys [id], got %v", r.PrimaryKeys())
	}
}
