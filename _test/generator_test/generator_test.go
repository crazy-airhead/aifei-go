package generator_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/crazy-airhead/aifei-go/db"
	"github.com/crazy-airhead/aifei-go/tools/generator"
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

	_, err := pool.Exec(`
		CREATE TABLE sys_login_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			login_time DATETIME DEFAULT CURRENT_TIMESTAMP,
			ip TEXT
		)
	`)
	if err != nil {
		t.Fatal(err)
	}

	dialect := &generator.SQLiteMetaDialect{}
	reader := generator.NewMetaReader()

	infos, err := reader.Read(pool, dialect)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	if len(infos) != 2 {
		t.Fatalf("expected 2 tables, got %d", len(infos))
	}

	info := infos[0]
	if info.Name != "sys_login_log" {
		t.Errorf("expected table name 'sys_login_log', got '%s'", info.Name)
	}
	if len(info.PrimaryKey) != 1 || info.PrimaryKey[0] != "id" {
		t.Errorf("expected primary key ['id'], got %v", info.PrimaryKey)
	}
	if len(info.Fields) != 4 {
		t.Errorf("expected 4 fields, got %d: %v", len(info.Fields), info.Fields[0])
	}

	// SQLite driver reports most types as "TEXT" or "INTEGER" in DatabaseTypeName
	// Just verify we got field data for each column
	for _, info := range infos {
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
}

func TestGenerator_Generate(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	_, err := pool.Exec(`
		CREATE TABLE sys_login_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			login_time DATETIME DEFAULT CURRENT_TIMESTAMP,
			ip TEXT
		)
	`)
	if err != nil {
		t.Fatal(err)
	}

	tmpDir, err := os.MkdirTemp("", "aifei-gen-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dialect := &generator.SQLiteMetaDialect{}
	gen := generator.New(pool, dialect, tmpDir, "example/db")
	gen.TablePrefix = "sys_"

	if err := gen.Generate(); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Verify generated files exist (user table - no prefix to strip)
	expectedFiles := []string{
		"tables.go",
		"user/base.go",
		"user/model.go",
		"user/dao.go",
		"user/service.go",
		"loginlog/base.go",
		"loginlog/model.go",
		"loginlog/dao.go",
		"loginlog/service.go",
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
	if !strings.Contains(string(tablesContent), `_ "example/db/loginlog"`) {
		t.Error("tables.go should contain blank import for loginlog package")
	}

	// Verify base.go content for user
	baseContent, _ := os.ReadFile(filepath.Join(tmpDir, "user/base.go"))
	t.Logf("user/base.go:\n%s", string(baseContent))
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

	// Verify loginlog package (prefix stripped: sys_login_log → login_log)
	loginlogBase, _ := os.ReadFile(filepath.Join(tmpDir, "loginlog/base.go"))
	t.Logf("loginlog/base.go:\n%s", string(loginlogBase))
	if !strings.Contains(string(loginlogBase), "package loginlog") {
		t.Error("loginlog/base.go should have package loginlog")
	}
	if !strings.Contains(string(loginlogBase), "type BaseLoginLog struct") {
		t.Error("loginlog/base.go should contain BaseLoginLog struct")
	}
	// Verify the table name is the original (un-prefixed) one. We match the
	// quoted value rather than exact column alignment, which shifts when new
	// db.Table fields (e.g. GeneratedColumns) are added.
	if !strings.Contains(string(loginlogBase), "\"sys_login_log\"") {
		t.Error("loginlog/base.go should use original table name sys_login_log")
	}

	// Verify service.go uses camelCase ServicePrefix
	loginlogService, _ := os.ReadFile(filepath.Join(tmpDir, "loginlog/service.go"))
	t.Logf("loginlog/service.go:\n%s", string(loginlogService))
	if !strings.Contains(string(loginlogService), "package loginlog") {
		t.Error("loginlog/service.go should have package loginlog")
	}
	if !strings.Contains(string(loginlogService), "ServicePrefix = \"/loginLog\"") {
		t.Error("loginlog/service.go should have camelCase ServicePrefix, got:", string(loginlogService))
	}
}

func TestTypeMapping(t *testing.T) {
	tm := generator.NewTypeMapping()

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
		result := generator.FieldToAttr(input)
		if result != expected {
			t.Errorf("FieldToAttr(%q) = %q, want %q", input, result, expected)
		}
	}
}

func TestGoKeyword(t *testing.T) {
	if !generator.IsGoKeyword("type") {
		t.Error("'type' should be a Go keyword")
	}
	if !generator.IsGoKeyword("select") {
		t.Error("'select' should be a Go keyword")
	}
	if generator.IsGoKeyword("username") {
		t.Error("'username' should NOT be a Go keyword")
	}

	if e := generator.EscapeKeyword("type"); e != "type_" {
		t.Errorf("EscapeKeyword('type') = %q, want 'type_'", e)
	}
	if e := generator.EscapeKeyword("name"); e != "name" {
		t.Errorf("EscapeKeyword('name') = %q, want 'name'", e)
	}
}

func TestMetaDialectCreation(t *testing.T) {
	d := generator.NewMetaDialect(db.NewDialect("sqlite"))
	if _, ok := d.(*generator.SQLiteMetaDialect); !ok {
		t.Errorf("expected *SQLiteMetaDialect, got %T", d)
	}

	d = generator.NewMetaDialect(db.NewDialect("mysql"))
	if _, ok := d.(*generator.MySQLMetaDialect); !ok {
		t.Errorf("expected *MySQLMetaDialect, got %T", d)
	}

	d = generator.NewMetaDialect(db.NewDialect("postgres"))
	if _, ok := d.(*generator.PostgresMetaDialect); !ok {
		t.Errorf("expected *PostgresMetaDialect, got %T", d)
	}
}

// fieldType finds the Go type of a column within a table read by MetaReader.
func fieldType(infos []*generator.TableInfo, table, column string) string {
	for _, ti := range infos {
		if ti.Name != table {
			continue
		}
		for _, f := range ti.Fields {
			if f.Name == column {
				return f.GoType
			}
		}
	}
	return ""
}

func TestResolveNullable(t *testing.T) {
	pool := setupTestDB(t) // user.email is a NULL-able TEXT column
	defer pool.Close()

	dialect := &generator.SQLiteMetaDialect{}

	// Default: plain Go types. The NULL-able 'email' column stays "string".
	defInfos, err := generator.NewMetaReader().Read(pool, dialect)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if got := fieldType(defInfos, "user", "email"); got != "string" {
		t.Errorf("default: user.email should be plain string, got %s", got)
	}

	// ResolveNullable=true: NULL-able column maps to sql.NullString.
	nullableReader := generator.NewMetaReader()
	nullableReader.ResolveNullable = true
	nullableInfos, err := nullableReader.Read(pool, dialect)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if got := fieldType(nullableInfos, "user", "email"); got != "sql.NullString" {
		t.Errorf("ResolveNullable: user.email should be sql.NullString, got %s", got)
	}
}
