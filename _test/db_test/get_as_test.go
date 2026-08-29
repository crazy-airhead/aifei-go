package db_test

import (
	"testing"
	"time"

	"github.com/crazy-airhead/aifei-go/db"
)

// ---- Row.GetAs / GetAsE / GetAsDefault ----

func TestRowGetAsLoose(t *testing.T) {
	row := db.NewRow("user").
		Set("name", "james").
		Set("age", 18).
		Set("score", 92.5).
		Set("active", true).
		Set("born", time.Date(2026, 8, 28, 10, 0, 0, 0, time.Local))

	// Direct scalars
	if got := row.GetAs[string]("name"); got != "james" {
		t.Errorf("GetAs[string](name) = %q, want james", got)
	}
	if got := row.GetAs[int]("age"); got != 18 {
		t.Errorf("GetAs[int](age) = %d, want 18", got)
	}
	if got := row.GetAs[float64]("score"); got != 92.5 {
		t.Errorf("GetAs[float64](score) = %v, want 92.5", got)
	}
	if got := row.GetAs[bool]("active"); got != true {
		t.Errorf("GetAs[bool](active) = %v, want true", got)
	}

	// Loose cross-kind conversion, same semantics as the GetStr/GetInt family
	if got := row.GetAs[string]("age"); got != "18" {
		t.Errorf("GetAs[string](age) = %q, want \"18\" (loose)", got)
	}
	if got := row.GetAs[int]("score"); got != 92 {
		t.Errorf("GetAs[int](score) = %d, want 92 (loose)", got)
	}
	if got := row.GetAs[time.Time]("born"); !got.Equal(time.Date(2026, 8, 28, 10, 0, 0, 0, time.Local)) {
		t.Errorf("GetAs[time.Time](born) = %v, want 2026-08-28 10:00", got)
	}
	if got := row.GetAs[[]byte]("name"); string(got) != "james" {
		t.Errorf("GetAs[[]byte](name) = %q, want james", got)
	}

	// Missing and dirty values yield the zero T (loose)
	if got := row.GetAs[string]("absent"); got != "" {
		t.Errorf("GetAs[string](absent) = %q, want empty", got)
	}
	dirty := db.NewRow("user").Set("age", "abc")
	if got := dirty.GetAs[int]("age"); got != 0 {
		t.Errorf("GetAs[int](dirty age) = %d, want 0", got)
	}

	// Default kicks in on nil only (mirrors the *Default family)
	if got := row.GetAsDefault("absent", "fallback"); got != "fallback" {
		t.Errorf("GetAsDefault(absent) = %q, want fallback", got)
	}
	if got := row.GetAsDefault("name", "fallback"); got != "james" {
		t.Errorf("GetAsDefault(name) = %q, want james", got)
	}
}

func TestRowGetAsStrict(t *testing.T) {
	row := db.NewRow("user").
		Set("name", "james").
		Set("age", 18).
		Set("score", 92.5).
		Set("blob", []byte("raw"))

	// NULL/missing yields (zero, nil) — the GetTimeE convention
	if got, err := row.GetAsE[int]("absent"); err != nil || got != 0 {
		t.Errorf("GetAsE[int](absent) = (%d, %v), want (0, nil)", got, err)
	}

	// Same-type and numeric-family cross-width pass untouched
	if got, err := row.GetAsE[int]("age"); err != nil || got != 18 {
		t.Errorf("GetAsE[int](age) = (%d, %v), want (18, nil)", got, err)
	}
	if got, err := row.GetAsE[int64]("score"); err != nil || got != 92 {
		t.Errorf("GetAsE[int64](score) = (%d, %v), want (92, nil)", got, err)
	}
	if got, err := row.GetAsE[float64]("age"); err != nil || got != 18 {
		t.Errorf("GetAsE[float64](age) = (%v, %v), want (18, nil)", got, err)
	}
	// string from []byte is a live representation, not silent coercion
	if got, err := row.GetAsE[string]("blob"); err != nil || got != "raw" {
		t.Errorf("GetAsE[string](blob) = (%q, %v), want (raw, nil)", got, err)
	}

	// Silent coercion is rejected: number → string, non-numeric → int
	if _, err := row.GetAsE[string]("age"); err == nil {
		t.Error("GetAsE[string](age) should reject numeric→string coercion")
	}
	dirty := db.NewRow("user").Set("age", "abc")
	if _, err := dirty.GetAsE[int]("age"); err == nil {
		t.Error("GetAsE[int](dirty) should reject unparseable string")
	}
}

// ---- Kv.GetAs / GetAsE ----

func TestKvGetAs(t *testing.T) {
	kv := db.Kv{"name": "james", "age": 18, "score": 92.5}

	if got := kv.GetAs[string]("name"); got != "james" {
		t.Errorf("Kv.GetAs[string](name) = %q", got)
	}
	if got := kv.GetAs[int]("age"); got != 18 {
		t.Errorf("Kv.GetAs[int](age) = %d", got)
	}
	if got := kv.GetAs[string]("score"); got != "92.5" {
		t.Errorf("Kv.GetAs[string](score) = %q, want 92.5 (loose)", got)
	}
	if got := kv.GetAs[int]("absent"); got != 0 {
		t.Errorf("Kv.GetAs[int](absent) = %d, want 0", got)
	}

	if got, err := kv.GetAsE[int]("age"); err != nil || got != 18 {
		t.Errorf("Kv.GetAsE[int](age) = (%d, %v)", got, err)
	}
	if _, err := kv.GetAsE[string]("age"); err == nil {
		t.Error("Kv.GetAsE[string](age) should reject coercion")
	}
}

// ---- Dao generic terminals ----

// testUser mirrors a generated model: it embeds *db.Row and adopts queried
// rows via InitRow, satisfying db.RowEntity.
type testUser struct {
	*db.Row
}

func (u *testUser) InitRow(row *db.Row) { u.Row = row }

func setupGetAsUsers(t *testing.T) {
	t.Helper()
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "james").Set("age", 18))
	db.Insert(db.NewRow("user").Set("name", "bond").Set("age", 42))
	db.Insert(db.NewRow("user").Set("name", "kid").Set("age", 8))
}

func TestDaoFindAs(t *testing.T) {
	setupGetAsUsers(t)

	users, err := db.Use().Table("user").RawSql("select * from user where age > ?", 10).FindAs[testUser]()
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}
	if users[0].GetStr("name") != "james" || users[1].GetStr("name") != "bond" {
		t.Errorf("typed names = %q, %q", users[0].GetStr("name"), users[1].GetStr("name"))
	}

	// Empty result yields an empty (non-error) typed slice
	none, err := db.Use().RawSql("select * from user where age > ?", 100).FindAs[testUser]()
	if err != nil || len(none) != 0 {
		t.Errorf("empty FindAs = (%d rows, %v), want (0, nil)", len(none), err)
	}
}

func TestDaoFindFirstAsAndFindOneAs(t *testing.T) {
	setupGetAsUsers(t)

	first, err := db.Use().Table("user").RawSql("select * from user order by age desc").FindFirstAs[testUser]()
	if err != nil || first == nil {
		t.Fatalf("FindFirstAs = (%v, %v)", first, err)
	}
	if first.GetStr("name") != "bond" {
		t.Errorf("FindFirstAs name = %q, want bond", first.GetStr("name"))
	}

	// No match yields (nil, nil), matching FindFirst
	miss, err := db.Use().RawSql("select * from user where age > ?", 100).FindFirstAs[testUser]()
	if err != nil || miss != nil {
		t.Errorf("FindFirstAs no-match = (%v, %v), want (nil, nil)", miss, err)
	}

	// FindOneAs: exactly one row
	one, err := db.Use().RawSql("select * from user where name = ?", "james").FindOneAs[testUser]()
	if err != nil || one == nil {
		t.Fatalf("FindOneAs = (%v, %v)", one, err)
	}
	if one.GetInt("age") != 18 {
		t.Errorf("FindOneAs age = %d, want 18", one.GetInt("age"))
	}
	if _, err := db.Use().RawSql("select * from user").FindOneAs[testUser](); err == nil {
		t.Error("FindOneAs with 3 rows should error (not-one)")
	}
}

func TestDaoPaginateAs(t *testing.T) {
	setupGetAsUsers(t)

	page, err := db.Use().Table("user").RawSql("select * from user order by id").PaginateAs[testUser](2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if page.TotalRows != 3 || page.TotalPages != 2 || page.PageNum != 2 {
		t.Fatalf("page meta = rows %d pages %d num %d", page.TotalRows, page.TotalPages, page.PageNum)
	}
	if len(page.Rows) != 1 || page.Rows[0].GetStr("name") != "kid" {
		t.Errorf("page 2 rows = %d, first = %q", len(page.Rows), page.Rows[0].GetStr("name"))
	}
}

func TestDaoFindByAsAndByIDAs(t *testing.T) {
	setupGetAsUsers(t)

	users, err := db.Use().FindByAs[testUser]("user", "age > ?", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 {
		t.Fatalf("FindByAs = %d users, want 2", len(users))
	}

	first, err := db.Use().FindFirstByAs[testUser]("user", "name", "bond")
	if err != nil || first == nil || first.GetInt("age") != 42 {
		t.Fatalf("FindFirstByAs = (%v, %v)", first, err)
	}

	byID, err := db.Use().FindByIDAs[testUser]("user", 1)
	if err != nil || byID == nil || byID.GetStr("name") != "james" {
		t.Fatalf("FindByIDAs = (%v, %v)", byID, err)
	}

	miss, err := db.Use().FindByIDAs[testUser]("user", 999)
	if err != nil || miss != nil {
		t.Errorf("FindByIDAs no-match = (%v, %v), want (nil, nil)", miss, err)
	}
}
