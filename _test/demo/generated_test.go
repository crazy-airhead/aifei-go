package main_test

import (
	"testing"

	"github.com/crazy-airhead/aifei-go/_test/demo/internal/user"
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
	db.RawSql(`CREATE TABLE IF NOT EXISTS sys_flow_task (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		title TEXT NOT NULL,
		user_id INTEGER NOT NULL,
		state TEXT DEFAULT 'pending',
		created_at TEXT DEFAULT CURRENT_TIMESTAMP
	)`).Update()
	db.RawSql(`CREATE TABLE IF NOT EXISTS sys_flow_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id INTEGER NOT NULL,
		user_id INTEGER NOT NULL,
		action TEXT,
		created_at TEXT DEFAULT CURRENT_TIMESTAMP
	)`).Update()
}

func TestGeneratedInsert(t *testing.T) {
	setupTest(t)

	u := user.NewUser().Name_("james").Age_(28).Email_("james@test.com")
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
	u := user.NewUser().Name_("alice").Age_(25)
	u.Insert()

	// Find by ID
	found, err := user.UserFindById(1)
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

	user.NewUser().Name_("bob").Age_(30).Insert()

	u, err := user.UserFindById(1)
	if err != nil || u == nil {
		t.Fatal("Expected to find user")
	}

	u.SetName("bob updated").SetAge(31)
	updated, err := u.Update()
	if err != nil || !updated {
		t.Fatal("Update failed")
	}

	// Verify
	u2, _ := user.UserFindById(1)
	if u2.Name() != "bob updated" {
		t.Errorf("Expected 'bob updated', got '%s'", u2.Name())
	}
	if u2.Age() != 31 {
		t.Errorf("Expected age 31, got %d", u2.Age())
	}
}

func TestGeneratedDelete(t *testing.T) {
	setupTest(t)

	user.NewUser().Name_("charlie").Insert()

	deleted, err := user.UserDeleteById(1)
	if err != nil || !deleted {
		t.Fatal("DeleteById failed")
	}

	u, err := user.UserFindById(1)
	if err != nil {
		t.Fatal(err)
	}
	if u != nil {
		t.Fatal("Expected nil after delete")
	}
}

func TestGeneratedFindBy(t *testing.T) {
	setupTest(t)

	user.NewUser().Name_("dave").Age_(20).Insert()
	user.NewUser().Name_("eve").Age_(25).Insert()
	user.NewUser().Name_("frank").Age_(30).Insert()

	// FindBy
	users, err := user.UserFindBy("age > ?", 22)
	if err != nil {
		t.Fatalf("FindBy failed: %v", err)
	}
	if len(users) != 2 {
		t.Errorf("Expected 2 users, got %d", len(users))
	}

	// Count
	count, err := user.UserCountBy("age > ?", 22)
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
	u := user.NewUser().Name_("grace").Age_(35).Email_("grace@test.com")
	u.Insert()

	found, _ := user.UserFindById(1)
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

	user.NewUser().Name_("tyler").Age_(40).Insert()
	user.NewUser().Name_("tyler").Age_(22).Insert()
	user.NewUser().Name_("uma").Age_(33).Insert()

	byName := `SELECT * FROM user
#where(name, '=', name)
ORDER BY id DESC`

	// typed Sql().Find() -> []*User
	users, err := user.NewUserDao().Sql(byName, map[string]interface{}{"name": "tyler"}).Find()
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
	first, err := user.NewUserDao().Sql(`SELECT * FROM user ORDER BY id ASC`, map[string]interface{}{}).FindFirst()
	if err != nil || first == nil {
		t.Fatalf("typed FindFirst failed: %v", err)
	}
	if first.Name() != "tyler" {
		t.Errorf("expected first name 'tyler', got '%s'", first.Name())
	}

	// typed FindByID (method, not the package func) -> *User
	got, err := user.NewUserDao().FindByID(1)
	if err != nil || got == nil {
		t.Fatalf("typed FindByID failed: %v", got)
	}

	// typed Paginate -> *db.Page
	page, err := user.NewUserDao().Sql(`SELECT * FROM user`, map[string]interface{}{}).Paginate(1, 2)
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
	c, err := user.NewUserDao().Count()
	if err != nil {
		t.Fatalf("typed Count failed: %v", err)
	}
	if c != 3 {
		t.Errorf("expected count 3, got %d", c)
	}

	// typed FindBy -> []*User
	matched, err := user.NewUserDao().FindBy("age > ?", 30)
	if err != nil {
		t.Fatalf("typed FindBy failed: %v", err)
	}
	if len(matched) != 2 { // tyler(40) + uma(33)
		t.Errorf("expected 2 matched, got %d", len(matched))
	}
}

// TestGeneratedTypedBatchIN covers the typed IN queries: FindByIds /
// DeleteByIds (WHERE pk IN ...) and FindIn (IN over an arbitrary column).
func TestGeneratedTypedBatchIN(t *testing.T) {
	setupTest(t)

	user.NewUser().Name_("hank").Age_(20).Insert()
	user.NewUser().Name_("iris").Age_(25).Insert()
	user.NewUser().Name_("jack").Age_(30).Insert()
	user.NewUser().Name_("kate").Age_(35).Insert()

	// package-level FindByIds
	byIds, err := user.UserFindByIds(1, 3)
	if err != nil {
		t.Fatalf("FindByIds failed: %v", err)
	}
	if len(byIds) != 2 {
		t.Fatalf("expected 2 users, got %d", len(byIds))
	}
	names := map[string]bool{byIds[0].Name(): true, byIds[1].Name(): true}
	if !names["hank"] || !names["jack"] {
		t.Errorf("expected hank+jack, got %v", names)
	}

	// dao-level FindByIds
	byIds2, err := user.NewUserDao().FindByIds(2, 4)
	if err != nil || len(byIds2) != 2 {
		t.Fatalf("dao FindByIds failed: %v (%d rows)", err, len(byIds2))
	}

	// FindIn over a non-PK column
	aged, err := user.NewUserDao().FindIn("age", 25, 35)
	if err != nil {
		t.Fatalf("FindIn failed: %v", err)
	}
	if len(aged) != 2 { // iris(25) + kate(35)
		t.Errorf("expected 2 users, got %d", len(aged))
	}

	// DeleteByIds
	n, err := user.UserDeleteByIds(1, 2)
	if err != nil {
		t.Fatalf("DeleteByIds failed: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 deleted, got %d", n)
	}
	left, _ := user.UserCount()
	if left != 2 {
		t.Errorf("expected 2 remaining, got %d", left)
	}
}

// TestGeneratedTypedPaginate verifies Paginate returns a typed page: the
// metadata mirrors db.Page and Rows carries *User.
func TestGeneratedTypedPaginate(t *testing.T) {
	setupTest(t)

	user.NewUser().Name_("lena").Age_(20).Insert()
	user.NewUser().Name_("mark").Age_(25).Insert()
	user.NewUser().Name_("nina").Age_(30).Insert()

	page, err := user.NewUserDao().Sql(`SELECT * FROM user`, map[string]interface{}{}).Paginate(1, 2)
	if err != nil {
		t.Fatalf("typed Paginate failed: %v", err)
	}
	if page.TotalRows != 3 || page.TotalPages != 2 || page.PageNum != 1 || page.PageSize != 2 {
		t.Fatalf("unexpected page metadata: %+v", page)
	}
	if len(page.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(page.Rows))
	}
	if page.Rows[0].Name() != "lena" { // typed element access
		t.Errorf("expected first row 'lena', got '%s'", page.Rows[0].Name())
	}
}

// TestGeneratedFromRowBridge covers the exported typed-row bridge: raw SQL
// through db.Sql returns []*db.Row, and FromRow/FromRows wrap them into
// typed *User usable outside the generated package.
func TestGeneratedFromRowBridge(t *testing.T) {
	setupTest(t)

	user.NewUser().Name_("oscar").Age_(40).Insert()
	user.NewUser().Name_("pete").Age_(45).Insert()

	rows, err := db.SqlWithArgs(`SELECT * FROM user WHERE age > #para(0) ORDER BY id`, 42).Find()
	if err != nil {
		t.Fatalf("raw db.Sql failed: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 raw row, got %d", len(rows))
	}

	// single wrap
	u := user.UserFromRow(rows[0])
	if u == nil {
		t.Fatal("FromRow returned nil")
	}
	if u.Name() != "pete" {
		t.Errorf("expected 'pete', got '%s'", u.Name())
	}

	// slice wrap
	typed := user.UserFromRows(rows)
	if len(typed) != 1 || typed[0].Age() != 45 {
		t.Errorf("FromRows yielded wrong result: %v", typed)
	}

	// the wrapped row is fully initialized: Update() via the bridge works
	u.SetAge(46)
	if ok, err := u.Update(); err != nil || !ok {
		t.Fatalf("Update via FromRow-wrapped model failed: %v", err)
	}
	again, _ := user.UserFindById(u.Id())
	if again.Age() != 46 {
		t.Errorf("expected updated age 46, got %d", again.Age())
	}

	// nil-safety
	if user.UserFromRow(nil) != nil {
		t.Error("FromRow(nil) should be nil")
	}
}
