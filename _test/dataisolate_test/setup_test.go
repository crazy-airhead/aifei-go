package dataisolate_test

import (
	"context"
	"os"
	"reflect"
	"testing"

	"github.com/crazy-airhead/aifei-go/config"
	"github.com/crazy-airhead/aifei-go/db"
	dataisolate "github.com/crazy-airhead/aifei-go/plugins/dataisolate"
	_ "modernc.org/sqlite"
)

// TestMain initializes a shared in-memory sqlite database, creates the test tables, and
// registers their db.Table metadata. Both the rewriter unit tests (metadata only) and
// the integration tests (real queries) draw on this setup.
func TestMain(m *testing.M) {
	registerTableMeta("orders", "tenant_id", "id", "status", "creator_id", "dept_id", "region_id")
	registerTableMeta("users", "tenant_id", "id", "name", "password", "creator_id", "dept_id")
	registerTableMeta("logs", "id", "msg") // no tenant_id → global
	if err := initDB(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

// registerTableMeta registers db.Table metadata (FieldTypes + PrimaryKeys) for a table.
func registerTableMeta(name string, cols ...string) {
	ft := make(map[string]reflect.Type, len(cols))
	for _, c := range cols {
		ft[c] = reflect.TypeOf("")
	}
	db.RegisterTable(&db.Table{Name: name, Fields: joinComma(cols), PrimaryKeys: []string{"id"}, FieldTypes: ft})
}

func joinComma(cols []string) string {
	out := ""
	for i, c := range cols {
		if i > 0 {
			out += ","
		}
		out += c
	}
	return out
}

func initDB() error {
	if err := db.Init("sqlite", "file::memory:?cache=shared"); err != nil {
		return err
	}
	ddl := []string{
		`CREATE TABLE orders (id INTEGER PRIMARY KEY AUTOINCREMENT, tenant_id TEXT, status TEXT, creator_id INTEGER, dept_id INTEGER, region_id TEXT)`,
		`CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, tenant_id TEXT, name TEXT, password TEXT, creator_id INTEGER, dept_id INTEGER)`,
		`CREATE TABLE logs (id INTEGER PRIMARY KEY AUTOINCREMENT, msg TEXT)`,
	}
	for _, q := range ddl {
		if _, err := db.Use().RawSql(q).Update(); err != nil {
			return err
		}
	}
	return nil
}

// resetTables wipes the test tables so tests are independent.
func resetTables(t *testing.T) {
	t.Helper()
	// bypass isolation while seeding
	ctx := dataisolate.Bypass(context.Background())
	for _, tbl := range []string{"orders", "users", "logs"} {
		if _, err := db.WithCtx(ctx).RawSql("DELETE FROM " + tbl).Update(); err != nil {
			t.Fatalf("reset %s: %v", tbl, err)
		}
	}
}

// startPlugin configures the dataisolate.* props from yaml, optionally configures the
// plugin (e.g. registers rule providers), then starts it (which installs the hook kit on
// the default "main" db config), and returns the manager.
func startPlugin(t *testing.T, yaml string, configure ...func(*dataisolate.Plugin)) *dataisolate.Manager {
	t.Helper()
	props := config.NewProps()
	if err := props.MergeYAML([]byte(yaml)); err != nil {
		t.Fatalf("merge yaml: %v", err)
	}
	config.SetProps(props)
	// Start is idempotent in production (called once); in tests each case re-installs on
	// the same "main" config, so reset any previously-installed hook first to avoid the
	// dataisolate hook nesting onto a stale one from a prior test.
	if c := db.GetConfig("main"); c != nil {
		c.HookKit = nil
	}
	p, err := dataisolate.NewPlugin(nil)
	if err != nil {
		t.Fatalf("new plugin: %v", err)
	}
	for _, c := range configure {
		c(p)
	}
	if err := p.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	return p.Manager()
}

// principalOf builds a Principal for tests.
func principalOf(tenant string, uid any) *dataisolate.Principal {
	return &dataisolate.Principal{TenantID: tenant, UserID: uid}
}
