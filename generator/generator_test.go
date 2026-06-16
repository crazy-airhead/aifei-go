package generator

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/crazy-airhead/aifei-go/db"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	pool, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	_, err = pool.Exec(`
		CREATE TABLE user (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			age INTEGER DEFAULT 0,
			email TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestMetaReader_Read(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	dialect := &SQLiteMetaDialect{}
	reader := NewMetaReader()

	infos, err := reader.Read(pool, dialect)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	if len(infos) != 1 {
		t.Fatalf("expected 1 table, got %d", len(infos))
	}

	info := infos[0]
	if info.Name != "user" {
		t.Errorf("expected table name 'user', got '%s'", info.Name)
	}
	if len(info.PrimaryKey) != 1 || info.PrimaryKey[0] != "id" {
		t.Errorf("expected primary key ['id'], got %v", info.PrimaryKey)
	}
	if len(info.Fields) != 5 {
		t.Errorf("expected 5 fields, got %d: %v", len(info.Fields), info.Fields[0])
	}

	// SQLite driver reports most types as "TEXT" or "INTEGER" in DatabaseTypeName
	// Just verify we got field data for each column
	for _, f := range info.Fields {
		if f.Name == "" {
			t.Error("field name should not be empty")
		}
		if f.GoType == "" {
			t.Errorf("field %s should have a Go type", f.Name)
		}
		if f.AttrName == "" {
			t.Errorf("field %s should have an AttrName", f.Name)
		}
	}
}

func TestTemplateRendering(t *testing.T) {
	// Build test data using the same structures the generator uses
	baseGen := NewBaseGenerator()

	info := &TableInfo{
		Name:       "user",
		PkgName:    "user",
		StructName: "User",
		BaseName:   "BaseUser",
		PrimaryKey: []string{"id"},
		Fields: []*FieldInfo{
			{Name: "id", GoType: "int", AttrName: "Id"},
			{Name: "name", GoType: "string", AttrName: "Name"},
			{Name: "age", GoType: "int", AttrName: "Age"},
		},
	}

	engine := NewEngine()

	// Test base template
	baseData := baseGen.buildData(info)
	content := engine.RenderTemplate(baseTemplateContent, baseData)
	t.Logf("base.go content:\n%s", content)

	if !strings.Contains(content, "package user") {
		t.Error("base.go should contain 'package user'")
	}
	if !strings.Contains(content, "Table = &db.Table") {
		t.Error("base.go should contain db.Table definition")
	}
	if !strings.Contains(content, "db.RegisterTable(Table)") {
		t.Error("base.go should contain init() with db.RegisterTable")
	}
	if !strings.Contains(content, "type BaseUser struct") {
		t.Error("base.go should contain BaseUser struct")
	}
	if !strings.Contains(content, "Id() int") {
		t.Error("base.go should contain Id() getter")
	}
	if !strings.Contains(content, "SetId(v int)") {
		t.Error("base.go should contain SetId() setter")
	}
	if !strings.Contains(content, "Name_(v string)") {
		t.Error("base.go should contain Name_() short setter")
	}
	if !strings.Contains(content, "func (r *BaseUser) Insert()") {
		t.Error("base.go should contain Insert() method")
	}

	// Test model template
	modelData := map[string]interface{}{
		"pkgName":    info.PkgName,
		"tableName":  info.Name,
		"structName": info.StructName,
		"baseName":   info.BaseName,
	}
	modelContent := engine.RenderTemplate(modelTemplateContent, modelData)
	if !strings.Contains(modelContent, "type User struct") {
		t.Error("model.go should contain User struct, got:", modelContent)
	}

	// Test dao template
	daoGen := NewDaoGenerator()
	daoData := daoGen.buildData(info)
	daoContent := engine.RenderTemplate(daoTemplateContent, daoData)
	if !strings.Contains(daoContent, "type Dao struct") {
		t.Error("dao.go should contain Dao struct, got:", daoContent)
	}
	if !strings.Contains(daoContent, "func FindById") {
		t.Error("dao.go should contain FindById function, got:", daoContent)
	}
}

func TestGenerator_Generate(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	tmpDir, err := os.MkdirTemp("", "aifei-gen-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dialect := &SQLiteMetaDialect{}
	gen := New(pool, dialect, tmpDir, "example/db")

	if err := gen.Generate(); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Verify generated files exist
	expectedFiles := []string{
		"tables.go",
		"user/base.go",
		"user/user.go",
		"user/dao.go",
		"user/service.go",
	}
	for _, f := range expectedFiles {
		path := filepath.Join(tmpDir, f)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected file not found: %s", path)
		}
	}

	// Verify tables.go uses blank imports for self-registration
	tablesContent, _ := os.ReadFile(filepath.Join(tmpDir, "tables.go"))
	t.Logf("tables.go:\n%s", string(tablesContent))
	if !strings.Contains(string(tablesContent), `_ "example/db/user"`) {
		t.Error("tables.go should contain blank import for user package")
	}

	// Verify base.go content
	baseContent, _ := os.ReadFile(filepath.Join(tmpDir, "user/base.go"))
	t.Logf("base.go:\n%s", string(baseContent))
	if !strings.Contains(string(baseContent), "Table = &db.Table") {
		t.Error("base.go should contain db.Table definition")
	}
	if !strings.Contains(string(baseContent), "db.RegisterTable(Table)") {
		t.Error("base.go should contain db.RegisterTable in init()")
	}
	if !strings.Contains(string(baseContent), "type BaseUser struct") {
		t.Error("base.go should contain BaseUser struct")
	}
	if !strings.Contains(string(baseContent), "Insert()") {
		t.Error("base.go should contain Insert() method")
	}
}

func TestTypeMapping(t *testing.T) {
	tm := NewTypeMapping()

	tests := map[string]string{
		"VARCHAR":  "string",
		"INT":      "int",
		"BIGINT":   "int64",
		"FLOAT":    "float64",
		"BOOL":     "bool",
		"DATETIME": "time.Time",
		"BLOB":     "[]byte",
		"UNKNOWN":  "string", // default fallback
	}

	for dbType, expected := range tests {
		result := tm.GetType(dbType)
		if result != expected {
			t.Errorf("TypeMapping[%s] = %s, want %s", dbType, result, expected)
		}
	}

	// Test custom mapping
	tm.AddMapping("MONEY", "float64")
	if tm.GetType("MONEY") != "float64" {
		t.Error("custom type mapping failed")
	}
}

func TestFieldToAttr(t *testing.T) {
	tests := map[string]string{
		"user_id":    "UserId",
		"created_at": "CreatedAt",
		"name":       "Name",
		"id":         "Id",
		"a_b_c":      "ABC",
		"":           "",
		"_test":      "Test",
	}

	for input, expected := range tests {
		result := FieldToAttr(input)
		if result != expected {
			t.Errorf("FieldToAttr(%q) = %q, want %q", input, result, expected)
		}
	}
}

func TestGoKeyword(t *testing.T) {
	if !IsGoKeyword("type") {
		t.Error("'type' should be a Go keyword")
	}
	if !IsGoKeyword("select") {
		t.Error("'select' should be a Go keyword")
	}
	if IsGoKeyword("username") {
		t.Error("'username' should NOT be a Go keyword")
	}

	if e := EscapeKeyword("type"); e != "type_" {
		t.Errorf("EscapeKeyword('type') = %q, want 'type_'", e)
	}
	if e := EscapeKeyword("name"); e != "name" {
		t.Errorf("EscapeKeyword('name') = %q, want 'name'", e)
	}
}

func TestMetaDialectCreation(t *testing.T) {
	d := NewMetaDialect(db.NewDialect("sqlite"))
	if _, ok := d.(*SQLiteMetaDialect); !ok {
		t.Errorf("expected *SQLiteMetaDialect, got %T", d)
	}

	d = NewMetaDialect(db.NewDialect("mysql"))
	if _, ok := d.(*MySQLMetaDialect); !ok {
		t.Errorf("expected *MySQLMetaDialect, got %T", d)
	}

	d = NewMetaDialect(db.NewDialect("postgres"))
	if _, ok := d.(*PostgresMetaDialect); !ok {
		t.Errorf("expected *PostgresMetaDialect, got %T", d)
	}
}

func TestWhitelistBlacklist(t *testing.T) {
	reader := NewMetaReader()

	// Whitelist
	reader.AddWhitelist("user")
	if !reader.shouldProcess("user") {
		t.Error("user should be processed (whitelisted)")
	}
	if !reader.shouldProcess("USER") {
		t.Error("USER should be processed (case-insensitive)")
	}
	if reader.shouldProcess("order") {
		t.Error("order should NOT be processed (not whitelisted)")
	}

	// Blacklist
	reader2 := NewMetaReader().AddBlacklist("migrations")
	if reader2.shouldProcess("migrations") {
		t.Error("migrations should NOT be processed (blacklisted)")
	}
	if !reader2.shouldProcess("user") {
		t.Error("user should be processed (not blacklisted)")
	}
}
