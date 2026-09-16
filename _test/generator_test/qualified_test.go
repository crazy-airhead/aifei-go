package generator_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/crazy-airhead/aifei-go/tools/generator"
)

// setupFlowDB creates a database with two flow-domain tables that share the
// sys_ prefix — the multi-table-one-package scenario Qualified mode targets.
func setupFlowDB(t *testing.T) *sql.DB {
	t.Helper()
	pool, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(`
		CREATE TABLE sys_flow_task (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			node_name TEXT NOT NULL,
			state TEXT
		);
		CREATE TABLE sys_flow_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id INTEGER NOT NULL,
			action TEXT
		)
	`)
	if err != nil {
		t.Fatal(err)
	}
	return pool
}

// newFlowGenerator builds a Generator mapping both sys_flow_* tables into the
// single "flow" package (包 = 领域), and returns its output directory.
func newFlowGenerator(t *testing.T, pool *sql.DB, qualified bool) (*generator.Generator, string) {
	t.Helper()
	tmpDir := t.TempDir()
	gen := generator.New(pool, &generator.SQLiteMetaDialect{}, tmpDir, "example/db/flowpkg")
	gen.TablePrefix = "sys_"
	gen.Qualified = qualified
	gen.PkgNameFunc = func(tableName string) string {
		return "flow"
	}
	return gen, tmpDir
}

// TestGenerator_QualifiedMultiTablePackage generates two tables into one
// package with Qualified naming and verifies the table-qualified identifiers
// and per-table file names that keep them from colliding.
func TestGenerator_QualifiedMultiTablePackage(t *testing.T) {
	pool := setupFlowDB(t)
	defer pool.Close()

	gen, outDir := newFlowGenerator(t, pool, true)
	if err := gen.Generate(); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	for _, f := range []string{
		"init.go",
		"flow/base_flow_task.go", "flow/model_flow_task.go", "flow/dao_flow_task.go", "flow/service_flow_task.go",
		"flow/base_flow_log.go", "flow/model_flow_log.go", "flow/dao_flow_log.go", "flow/service_flow_log.go",
	} {
		if _, err := os.Stat(filepath.Join(outDir, f)); os.IsNotExist(err) {
			t.Errorf("expected file not found: %s", f)
		}
	}

	read := func(f string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(outDir, f))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	// base_flow_task.go: qualified identifiers, both in default and init path
	taskBase := read("flow/base_flow_task.go")
	for _, want := range []string{
		"package flow",
		"TableFlowTask = &db.Table",
		"db.RegisterTable(TableFlowTask)",
		"func NewFlowTaskWithRow(row *db.Row) *BaseFlowTask",
		"func initFlowTaskRow(row *db.Row) *db.Row",
		"func FlowTaskFromRow(row *db.Row) *FlowTask",
		"func FlowTaskFromRows(rows []*db.Row) []*FlowTask",
	} {
		if !strings.Contains(taskBase, want) {
			t.Errorf("base_flow_task.go missing %q", want)
		}
	}

	// base_flow_log.go: same package, distinct identifiers — no collision
	logBase := read("flow/base_flow_log.go")
	for _, want := range []string{"package flow", "TableFlowLog", "FlowLogFromRow"} {
		if !strings.Contains(logBase, want) {
			t.Errorf("base_flow_log.go missing %q", want)
		}
	}

	// dao_flow_task.go: typed Dao and batch IN under qualified names
	taskDao := read("flow/dao_flow_task.go")
	for _, want := range []string{
		"type FlowTaskDao struct",
		"func NewFlowTaskDao() *FlowTaskDao",
		"type FlowTaskPage struct",
		"func (d *FlowTaskDao) Paginate(pageNum, pageSize int) (*FlowTaskPage, error)",
		"func (d *FlowTaskDao) FindIn(field string, values ...interface{})",
		"func (d *FlowTaskDao) FindByIds(ids ...int)",
		"func (d *FlowTaskDao) DeleteByIds(ids ...int)",
		"func FlowTaskFindById(id int) (*FlowTask, error)",
		"func FlowTaskDeleteByIds(ids ...int)",
		"func FlowTaskCount() (int64, error)",
	} {
		if !strings.Contains(taskDao, want) {
			t.Errorf("dao_flow_task.go missing %q", want)
		}
	}

	// service_flow_task.go: qualified service type/prefix/listSql
	taskService := read("flow/service_flow_task.go")
	for _, want := range []string{
		"package flow",
		"FlowTaskServicePrefix",
		"type FlowTaskService struct",
		"flowTaskListSql",
		"NewFlowTaskDao()",
	} {
		if !strings.Contains(taskService, want) {
			t.Errorf("service_flow_task.go missing %q", want)
		}
	}

	// init.go imports the shared package exactly once
	initContent := read("init.go")
	if n := strings.Count(initContent, `"example/db/flowpkg/flow"`); n != 1 {
		t.Errorf("init.go should import flow package once, got %d:\n%s", n, initContent)
	}
}

// TestGenerator_QualifiedRequiredForSharedPackage verifies the fail-fast
// guard: mapping several tables into one package without Qualified naming is
// rejected with a pointer to the fix, not broken colliding code.
func TestGenerator_QualifiedRequiredForSharedPackage(t *testing.T) {
	pool := setupFlowDB(t)
	defer pool.Close()

	gen, _ := newFlowGenerator(t, pool, false)
	err := gen.Generate()
	if err == nil {
		t.Fatal("expected error when multiple tables share a package without Qualified")
	}
	for _, want := range []string{"sys_flow_log", "sys_flow_task", "Qualified"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

// TestGenerator_ForceOverwrites verifies model/dao/service keep hand edits on
// re-generation by default, and Force discards them.
func TestGenerator_ForceOverwrites(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	tmpDir := t.TempDir()
	gen := generator.New(pool, &generator.SQLiteMetaDialect{}, tmpDir, "example/db")
	if err := gen.Generate(); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	modelPath := filepath.Join(tmpDir, "user/model.go")
	const marker = "// HAND-EDITED MARKER"
	if err := os.WriteFile(modelPath, []byte("package user\n\n"+marker+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Without Force the hand edit survives
	if err := gen.Generate(); err != nil {
		t.Fatalf("re-Generate failed: %v", err)
	}
	kept, _ := os.ReadFile(modelPath)
	if !strings.Contains(string(kept), marker) {
		t.Error("model.go should keep hand edits when Force is off")
	}

	// With Force the file is regenerated
	gen.Force = true
	if err := gen.Generate(); err != nil {
		t.Fatalf("force Generate failed: %v", err)
	}
	forced, _ := os.ReadFile(modelPath)
	if strings.Contains(string(forced), marker) {
		t.Error("model.go should be overwritten when Force is on")
	}
	if !strings.Contains(string(forced), "func New() *User") {
		t.Error("forced model.go should contain the generated constructor")
	}
}

// TestGenerator_CustomServiceTemplate verifies ServiceGenerator.Template
// replaces the built-in template while receiving the same data map (per-table
// keys plus the names.go identifier keys).
func TestGenerator_CustomServiceTemplate(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	tmpDir := t.TempDir()
	gen := generator.New(pool, &generator.SQLiteMetaDialect{}, tmpDir, "example/db")
	gen.ConfigServiceGenerator(func(s *generator.ServiceGenerator) {
		s.Template = `package #(pkgName)

// Custom wiring for table "#(tableName)".
const #(prefixConst) = "/custom/#(servicePath)"

func load#(structName)() *#(structName) {
	return #(newFn)()
}
`
	})
	if err := gen.Generate(); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(tmpDir, "user/service.go"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{
		`const ServicePrefix = "/custom/user"`,
		"func loadUser() *User {",
		"return New()",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("custom service.go missing %q:\n%s", want, got)
		}
	}
}

// TestGenerator_DefaultModeNewContent pins the intentionally added APIs in
// default (one-table-per-package) output: the exported FromRow bridge and the
// typed batch-IN / page queries. Everything else must stay byte-identical to
// the pre-existing output (covered by TestGenerator_Generate's assertions).
func TestGenerator_DefaultModeNewContent(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	tmpDir := t.TempDir()
	gen := generator.New(pool, &generator.SQLiteMetaDialect{}, tmpDir, "example/db")
	if err := gen.Generate(); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	base, err := os.ReadFile(filepath.Join(tmpDir, "user/base.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"func FromRow(row *db.Row) *User {",
		"func FromRows(rows []*db.Row) []*User {",
	} {
		if !strings.Contains(string(base), want) {
			t.Errorf("base.go missing %q", want)
		}
	}

	dao, err := os.ReadFile(filepath.Join(tmpDir, "user/dao.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"func (d *Dao) FindIn(field string, values ...interface{})",
		"func (d *Dao) FindByIds(ids ...int)",
		"func (d *Dao) DeleteByIds(ids ...int)",
		"func FindByIds(ids ...int)",
		"func DeleteByIds(ids ...int)",
		"type UserPage struct",
		"func (d *Dao) Paginate(pageNum, pageSize int) (*UserPage, error)",
		"Rows:       FromRows(page.Rows)",
	} {
		if !strings.Contains(string(dao), want) {
			t.Errorf("dao.go missing %q", want)
		}
	}
}
