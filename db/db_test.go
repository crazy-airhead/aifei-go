package db

import (
	"fmt"
	"testing"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) {
	t.Helper()
	// Close old pool
	if c, ok := configs["main"]; ok && c.pool != nil {
		c.pool.Close()
	}
	delete(configs, "main")
	err := Init("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Use().SQL("CREATE TABLE IF NOT EXISTS user (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, age INTEGER, email TEXT)").Update()
	if err != nil {
		t.Fatal(err)
	}
	// Clean existing data
	Use().SQL("DELETE FROM user").Update()
}

func TestInsert(t *testing.T) {
	setupTestDB(t)
	row := NewRow("user").Set("name", "james").Set("age", 18).Set("email", "james@test.com")
	result, err := Insert(row)
	if err != nil {
		t.Fatal(err)
	}
	id := result.GetID()
	if id == nil || ToInt64(id) == 0 {
		t.Fatal("expected auto-generated ID")
	}
}

func TestFindById(t *testing.T) {
	setupTestDB(t)
	row := NewRow("user").Set("name", "james").Set("age", 18)
	Insert(row)

	found, err := FindByID("user", 1)
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
	Insert(NewRow("user").Set("name", "alice").Set("age", 20))
	Insert(NewRow("user").Set("name", "bob").Set("age", 25))
	Insert(NewRow("user").Set("name", "charlie").Set("age", 30))

	rows, err := FindBy("user", "age > ?", 22)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
}

func TestFindByFieldName(t *testing.T) {
	setupTestDB(t)
	Insert(NewRow("user").Set("name", "alice").Set("age", 20))
	Insert(NewRow("user").Set("name", "bob").Set("age", 25))

	rows, err := FindBy("user", "name", "alice")
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
	Insert(NewRow("user").Set("name", "james").Set("age", 18))

	row := NewRow("user").ID(1).Set("age", 19)
	ok, err := Update(row)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected update to succeed")
	}

	found, _ := FindByID("user", 1)
	if found.GetInt("age") != 19 {
		t.Fatalf("expected age=19, got %d", found.GetInt("age"))
	}
}

func TestDelete(t *testing.T) {
	setupTestDB(t)
	Insert(NewRow("user").Set("name", "james").Set("age", 18))

	ok, err := DeleteByID("user", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected delete to succeed")
	}

	found, _ := FindByID("user", 1)
	if found != nil {
		t.Fatal("expected user to be deleted")
	}
}

func TestPaginate(t *testing.T) {
	setupTestDB(t)
	for i := 0; i < 25; i++ {
		Insert(NewRow("user").Set("name", "user").Set("age", i))
	}

	page, err := SQL("SELECT * FROM user").Paginate(1, 10)
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
	Insert(NewRow("user").Set("name", "a").Set("age", 1))
	Insert(NewRow("user").Set("name", "b").Set("age", 2))

	count, err := Count("user")
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected count=2, got %d", count)
	}
}

func TestCountBy(t *testing.T) {
	setupTestDB(t)
	Insert(NewRow("user").Set("name", "a").Set("age", 1))
	Insert(NewRow("user").Set("name", "b").Set("age", 2))
	Insert(NewRow("user").Set("name", "c").Set("age", 3))

	count, err := CountBy("user", "age > ?", 1)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected count=2, got %d", count)
	}
}

func TestTransaction(t *testing.T) {
	setupTestDB(t)

	err := Transaction(func() error {
		Insert(NewRow("user").Set("name", "tx1").Set("age", 1))
		Insert(NewRow("user").Set("name", "tx2").Set("age", 2))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	count, _ := Count("user")
	if count != 2 {
		t.Fatalf("expected 2 rows after commit, got %d", count)
	}
}

func TestTransactionRollback(t *testing.T) {
	setupTestDB(t)

	err := Transaction(func() error {
		return fmt.Errorf("force rollback")
	})
	if err == nil {
		t.Fatal("expected error from rollback")
	}
}

func TestRowActiveRecord(t *testing.T) {
	setupTestDB(t)
	row := NewRow("user").Set("name", "active").Set("age", 99)
	result, err := row.Insert()
	if err != nil {
		t.Fatal(err)
	}
	id := result.GetID()
	if id == nil {
		t.Fatal("expected ID after insert")
	}

	found, _ := FindByID("user", id)
	if found.GetStr("name") != "active" {
		t.Fatalf("expected name=active, got %s", found.GetStr("name"))
	}
}

func TestRowTypeConvert(t *testing.T) {
	row := NewRow("user")
	row.Set("name", "test")
	row.Set("age", 25)
	row.Set("score", 98.5)
	row.Set("active", true)

	if row.GetStr("name") != "test" {
		t.Fatalf("expected test, got %s", row.GetStr("name"))
	}
	if row.GetInt("age") != 25 {
		t.Fatalf("expected 25, got %d", row.GetInt("age"))
	}
	if row.GetFloat64("score") != 98.5 {
		t.Fatalf("expected 98.5, got %f", row.GetFloat64("score"))
	}
	if !row.GetBool("active") {
		t.Fatal("expected true")
	}

	if row.GetStrDefault("missing", "default") != "default" {
		t.Fatal("expected default value")
	}
	if row.GetIntDefault("missing", 42) != 42 {
		t.Fatal("expected default int")
	}
}

func TestSQLBuilder(t *testing.T) {
	setupTestDB(t)
	Insert(NewRow("user").Set("name", "alice").Set("age", 20))
	Insert(NewRow("user").Set("name", "bob").Set("age", 25))
	Insert(NewRow("user").Set("name", "charlie").Set("age", 30))

	rows, err := NewSQL("SELECT * FROM user").Where("age > ?", 22).OrderBy("id DESC").Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
}

func TestBatchInsert(t *testing.T) {
	setupTestDB(t)
	rows := []*Row{
		NewRow("user").Set("name", "batch1").Set("age", 1),
		NewRow("user").Set("name", "batch2").Set("age", 2),
		NewRow("user").Set("name", "batch3").Set("age", 3),
	}

	result, err := NewBatch().Insert(rows)
	if err != nil {
		t.Fatal(err)
	}
	if result.RowsAffected != 3 {
		t.Fatalf("expected 3 affected rows, got %d", result.RowsAffected)
	}

	count, _ := Count("user")
	if count != 3 {
		t.Fatalf("expected 3 rows, got %d", count)
	}
}

func TestRowJSON(t *testing.T) {
	row := NewRow("user")
	row.Set("name", "test")
	row.Set("age", 25)

	data, err := row.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}

	row2 := NewRow("user")
	err = row2.UnmarshalJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if row2.GetStr("name") != "test" {
		t.Fatalf("expected test, got %s", row2.GetStr("name"))
	}
}

func TestDeleteIn(t *testing.T) {
	setupTestDB(t)
	Insert(NewRow("user").Set("name", "a").Set("age", 1))
	Insert(NewRow("user").Set("name", "b").Set("age", 2))
	Insert(NewRow("user").Set("name", "c").Set("age", 3))

	n, err := Use().DeleteIn("user", "name", "a", "b")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 deleted, got %d", n)
	}
}

func TestFindIn(t *testing.T) {
	setupTestDB(t)
	Insert(NewRow("user").Set("name", "a").Set("age", 1))
	Insert(NewRow("user").Set("name", "b").Set("age", 2))
	Insert(NewRow("user").Set("name", "c").Set("age", 3))

	rows, err := Use().FindIn("user", "name", "a", "c")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
}
