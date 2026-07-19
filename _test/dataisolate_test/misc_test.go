package dataisolate_test

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/crazy-airhead/aifei-go/aifei"
	"github.com/crazy-airhead/aifei-go/db"
	aifeihttp "github.com/crazy-airhead/aifei-go/http"
	dataisolate "github.com/crazy-airhead/aifei-go/plugins/dataisolate"
	"github.com/crazy-airhead/aifei-go/server"
)

// ---- Batch processing (db change #4) ----

// TestBatchInsertStamps: NewBatchCtx(ctx).Insert stamps tenant_id on every row.
func TestBatchInsertStamps(t *testing.T) {
	startPlugin(t, tenantYAML)
	resetTables(t)
	ctx := tenantCtx("acme", 1)
	rows := []*db.Row{
		db.NewRow("orders").Set("status", "a"),
		db.NewRow("orders").Set("status", "b"),
	}
	res, err := db.NewBatchCtx(ctx).Insert(rows)
	if err != nil {
		t.Fatalf("batch insert: %v", err)
	}
	if res.RowsAffected != 2 {
		t.Fatalf("expected 2 affected, got %d", res.RowsAffected)
	}
	got, err := db.WithCtx(ctx).FindBy("orders", "id > ?", 0)
	if err != nil {
		t.Fatalf("verify find: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(got))
	}
	for _, r := range got {
		if r.GetStr("tenant_id") != "acme" {
			t.Fatalf("row not stamped with tenant: %v", r.Get("tenant_id"))
		}
	}
}

// TestBatchUpdateScoped: a batch UPDATE is scoped to the tenant; a cross-tenant id is
// not modified.
func TestBatchUpdateScoped(t *testing.T) {
	startPlugin(t, tenantYAML)
	seedOrders(t)
	bypass := dataisolate.Bypass(context.Background())

	acmeRows, err := db.WithCtx(bypass).FindBy("orders", "tenant_id = ?", "acme")
	if err != nil || len(acmeRows) != 3 {
		t.Fatalf("seed acme: err=%v rows=%d", err, len(acmeRows))
	}
	globexRows, err := db.WithCtx(bypass).FindBy("orders", "tenant_id = ?", "globex")
	if err != nil || len(globexRows) != 1 {
		t.Fatalf("seed globex: err=%v rows=%d", err, len(globexRows))
	}
	globexID := globexRows[0].GetInt("id")

	for _, r := range acmeRows {
		r.Set("status", "updated")
	}
	// sneak a row with globex's id but acting as acme — must be scoped out (0 affected)
	sneak := db.NewRow("orders")
	sneak.Put("id", globexID)
	sneak.Set("status", "hacked")

	res, err := db.NewBatchCtx(tenantCtx("acme", 1)).Update(append(acmeRows, sneak))
	if err != nil {
		t.Fatalf("batch update: %v", err)
	}
	if res.RowsAffected != 3 {
		t.Fatalf("expected 3 affected (acme only), got %d", res.RowsAffected)
	}
	// globex row must be untouched
	globexAfter, err := db.WithCtx(bypass).FindBy("orders", "tenant_id = ?", "globex")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if globexAfter[0].GetStr("status") == "hacked" {
		t.Fatal("globex row was modified across the tenant boundary")
	}
}

// ---- db.Sql directive path + double-injection guard ----

// TestSqlDirectiveTenant: dataisolate.Sql seeds tenantId into the data map so a #where
// directive filters on it; the hook does not double-inject tenant_id.
func TestSqlDirectiveTenant(t *testing.T) {
	startPlugin(t, tenantYAML)
	seedOrders(t)

	var seen string
	db.GetConfig("main").Printer = func(s string, _ ...interface{}) { seen += s + "\n" }

	rows, err := dataisolate.Sql(tenantCtx("acme", 1),
		`SELECT * FROM orders #where(tenant_id, "=", tenantId)`, nil).Find()
	db.GetConfig("main").Printer = nil
	if err != nil {
		t.Fatalf("sql find: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 acme rows, got %d", len(rows))
	}
	// guard: the executed SQL references tenant_id exactly once (no double injection)
	if c := subcount(seen, "tenant_id"); c != 1 {
		t.Fatalf("expected tenant_id to appear once in rendered SQL, got %d\n%s", c, seen)
	}
}

// TestSqlDirectiveBypassOmits: under bypass the tenantId var is nil so #where omits the
// predicate (all rows returned).
func TestSqlDirectiveBypassOmits(t *testing.T) {
	startPlugin(t, tenantYAML)
	seedOrders(t)
	rows, err := dataisolate.Sql(dataisolate.Bypass(context.Background()),
		`SELECT * FROM orders #where(tenant_id, "=", tenantId)`, nil).Find()
	if err != nil {
		t.Fatalf("bypass sql: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("bypass should see all 4 rows, got %d", len(rows))
	}
}

func subcount(s, sub string) int {
	n := 0
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			n++
			i += len(sub) - 1
		}
	}
	return n
}

// ---- Middleware / resolver ----

// TestMiddlewareHeaderResolver: the middleware resolves the principal from the
// X-Tenant-ID header and writes it into the request context.
func TestMiddlewareHeaderResolver(t *testing.T) {
	startPlugin(t, tenantYAML)

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Tenant-ID", "acme")
	in := &aifeihttp.HttpContext{Request: r}

	var captured *dataisolate.Principal
	h := dataisolate.Middleware()
	h(func(in aifei.Input) aifei.Output {
		captured, _ = dataisolate.PrincipalFrom(in.Context())
		return server.Ok()
	})(in)

	if captured == nil || captured.TenantID != "acme" {
		t.Fatalf("header resolver did not populate principal: %v", captured)
	}
}

// TestMiddlewareSubdomainResolver: the resolver derives the tenant from the Host subdomain.
func TestMiddlewareSubdomainResolver(t *testing.T) {
	startPlugin(t, tenantYAML)
	r := httptest.NewRequest("GET", "/", nil)
	r.Host = "globex.example.com"
	in := &aifeihttp.HttpContext{Request: r}

	var captured *dataisolate.Principal
	h := dataisolate.Middleware()
	h(func(in aifei.Input) aifei.Output {
		captured, _ = dataisolate.PrincipalFrom(in.Context())
		return server.Ok()
	})(in)

	if captured == nil || captured.TenantID != "globex" {
		t.Fatalf("subdomain resolver did not populate principal: %v", captured)
	}
}

// ---- Strategy ①/②: database-per-tenant routing (zero SQL rewrite) ----

const routingYAML = `
dataisolate:
  policies: [tenant]
  tenant:
    strategy: database
    column: tenant_id
    tenants:
      acme: { config: tenant_acme }
      globex: { config: tenant_globex }
`

// TestStrategyDatabaseRouting: Use(ctx) routes to the tenant's own db.Config; each tenant
// sees only its own data with no SQL rewriting.
func TestStrategyDatabaseRouting(t *testing.T) {
	dir := t.TempDir()
	initTenantDB := func(configID, tid string, n int) {
		dsn := "file:" + filepath.Join(dir, configID+".db")
		if err := db.InitWithID(configID, "sqlite", dsn); err != nil {
			t.Fatalf("init %s: %v", configID, err)
		}
		if _, err := db.UseWithID(configID).RawSql(
			"CREATE TABLE orders (id INTEGER PRIMARY KEY AUTOINCREMENT, tenant_id TEXT, status TEXT, creator_id INTEGER, dept_id INTEGER, region_id TEXT)").Update(); err != nil {
			t.Fatalf("create %s: %v", configID, err)
		}
		for i := 0; i < n; i++ {
			r := db.NewRow("orders").Set("tenant_id", tid).Set("status", "ok")
			if _, err := db.UseWithID(configID).InsertRow(r); err != nil {
				t.Fatalf("seed %s: %v", configID, err)
			}
		}
	}
	initTenantDB("tenant_acme", "acme", 2)
	initTenantDB("tenant_globex", "globex", 3)

	startPlugin(t, routingYAML)
	// strategy "database" installs no hook; isolation is purely via routing.

	acmeRows, err := dataisolate.Use(tenantCtx("acme", 1)).FindBy("orders", "id > ?", 0)
	if err != nil {
		t.Fatalf("acme route find: %v", err)
	}
	if len(acmeRows) != 2 {
		t.Fatalf("acme db should have 2 rows, got %d", len(acmeRows))
	}
	globexRows, err := dataisolate.Use(tenantCtx("globex", 1)).FindBy("orders", "id > ?", 0)
	if err != nil {
		t.Fatalf("globex route find: %v", err)
	}
	if len(globexRows) != 3 {
		t.Fatalf("globex db should have 3 rows, got %d", len(globexRows))
	}
}

// TestStrategyDatabaseBypass: without a principal/tenant, Use falls back to the default
// config (no panic).
func TestStrategyDatabaseFallback(t *testing.T) {
	startPlugin(t, routingYAML)
	if dao := dataisolate.Use(context.Background()); dao == nil {
		t.Fatal("Use with no principal should fall back to default config, got nil dao")
	}
}
