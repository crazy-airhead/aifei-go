package main_test

import (
	"testing"

	"github.com/crazy-airhead/aifei-go/_example/demo/internal/user"
	"github.com/crazy-airhead/aifei-go/db"

	_ "modernc.org/sqlite"
)

func setupTest(t *testing.T) {
	t.Helper()
	db.ResetConfigs()
	// Use a temp file for isolation
	err := db.Init("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.RawSql(`CREATE TABLE IF NOT EXISTS user (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		age INTEGER DEFAULT 0,
		email TEXT,
		created_at TEXT DEFAULT CURRENT_TIMESTAMP
	)`).Update()
}

func TestGeneratedInsert(t *testing.T) {
	setupTest(t)

	u := user.New().Name_("james").Age_(28).Email_("james@test.com")
	result, err := u.Insert()
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	if result.GetID() == nil {
		t.Fatal("Expected auto-generated ID after insert")
	}
	t.Logf("Inserted user with ID: %v", result.GetID())
}

func TestGeneratedFindById(t *testing.T) {
	setupTest(t)

	// Insert a test row
	u := user.New().Name_("alice").Age_(25)
	u.Insert()

	// Find by ID
	found, err := user.FindById(1)
	if err != nil {
		t.Fatalf("FindById failed: %v", err)
	}
	if found == nil {
		t.Fatal("Expected to find user with ID 1")
	}
	if found.Name() != "alice" {
		t.Errorf("Expected name 'alice', got '%s'", found.Name())
	}
	if found.Age() != 25 {
		t.Errorf("Expected age 25, got %d", found.Age())
	}
}

func TestGeneratedUpdate(t *testing.T) {
	setupTest(t)

	user.New().Name_("bob").Age_(30).Insert()

	u, err := user.FindById(1)
	if err != nil || u == nil {
		t.Fatal("Expected to find user")
	}

	u.SetName("bob updated").SetAge(31)
	updated, err := u.Update()
	if err != nil || !updated {
		t.Fatal("Update failed")
	}

	// Verify
	u2, _ := user.FindById(1)
	if u2.Name() != "bob updated" {
		t.Errorf("Expected 'bob updated', got '%s'", u2.Name())
	}
	if u2.Age() != 31 {
		t.Errorf("Expected age 31, got %d", u2.Age())
	}
}

func TestGeneratedDelete(t *testing.T) {
	setupTest(t)

	user.New().Name_("charlie").Insert()

	deleted, err := user.DeleteById(1)
	if err != nil || !deleted {
		t.Fatal("DeleteById failed")
	}

	u, err := user.FindById(1)
	if err != nil {
		t.Fatal(err)
	}
	if u != nil {
		t.Fatal("Expected nil after delete")
	}
}

func TestGeneratedFindBy(t *testing.T) {
	setupTest(t)

	user.New().Name_("dave").Age_(20).Insert()
	user.New().Name_("eve").Age_(25).Insert()
	user.New().Name_("frank").Age_(30).Insert()

	// FindBy
	users, err := user.FindBy("age > ?", 22)
	if err != nil {
		t.Fatalf("FindBy failed: %v", err)
	}
	if len(users) != 2 {
		t.Errorf("Expected 2 users, got %d", len(users))
	}

	// Count
	count, err := user.CountBy("age > ?", 22)
	if err != nil {
		t.Fatalf("CountBy failed: %v", err)
	}
	if count != 2 {
		t.Errorf("Expected count 2, got %d", count)
	}
}

func TestGeneratedShortSetters(t *testing.T) {
	setupTest(t)

	// Chain short setters
	u := user.New().Name_("grace").Age_(35).Email_("grace@test.com")
	u.Insert()

	found, _ := user.FindById(1)
	if found.Name() != "grace" {
		t.Errorf("Expected 'grace', got '%s'", found.Name())
	}
	if found.Email() != "grace@test.com" {
		t.Errorf("Expected 'grace@test.com', got '%s'", found.Email())
	}
}

// TestGeneratedTypedDao exercises the typed Dao returned by NewDao() directly —
// the Sql().Find() chain yields []*User (not []*db.Row), and the table is bound
// so no table name is passed. This is the aifei-vip `Vip.sql(...).paginate()`
// idiom, now available in generated code.
func TestGeneratedTypedDao(t *testing.T) {
	setupTest(t)

	user.New().Name_("tyler").Age_(40).Insert()
	user.New().Name_("tyler").Age_(22).Insert()
	user.New().Name_("uma").Age_(33).Insert()

	byName := `SELECT * FROM user
#where(name, '=', name)
ORDER BY id DESC`

	// typed Sql().Find() -> []*User
	users, err := user.NewDao().Sql(byName, map[string]interface{}{"name": "tyler"}).Find()
	if err != nil {
		t.Fatalf("typed Find failed: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 tylers, got %d", len(users))
	}
	if users[0].Name() != "tyler" { // typed getter works on *User
		t.Errorf("expected name 'tyler', got '%s'", users[0].Name())
	}

	// typed FindFirst -> *User
	first, err := user.NewDao().Sql(`SELECT * FROM user ORDER BY id ASC`, map[string]interface{}{}).FindFirst()
	if err != nil || first == nil {
		t.Fatalf("typed FindFirst failed: %v", err)
	}
	if first.Name() != "tyler" {
		t.Errorf("expected first name 'tyler', got '%s'", first.Name())
	}

	// typed FindByID (method, not the package func) -> *User
	got, err := user.NewDao().FindByID(1)
	if err != nil || got == nil {
		t.Fatalf("typed FindByID failed: %v", got)
	}

	// typed Paginate -> *db.Page
	page, err := user.NewDao().Sql(`SELECT * FROM user`, map[string]interface{}{}).Paginate(1, 2)
	if err != nil {
		t.Fatalf("typed Paginate failed: %v", err)
	}
	if page.TotalRows != 3 {
		t.Errorf("expected total 3, got %d", page.TotalRows)
	}
	if len(page.Rows) != 2 {
		t.Errorf("expected 2 rows on page 1, got %d", len(page.Rows))
	}

	// typed Count (method, no table arg)
	c, err := user.NewDao().Count()
	if err != nil {
		t.Fatalf("typed Count failed: %v", err)
	}
	if c != 3 {
		t.Errorf("expected count 3, got %d", c)
	}

	// typed FindBy -> []*User
	matched, err := user.NewDao().FindBy("age > ?", 30)
	if err != nil {
		t.Fatalf("typed FindBy failed: %v", err)
	}
	if len(matched) != 2 { // tyler(40) + uma(33)
		t.Errorf("expected 2 matched, got %d", len(matched))
	}
}
