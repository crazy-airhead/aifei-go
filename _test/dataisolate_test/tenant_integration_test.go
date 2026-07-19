package dataisolate_test

import (
	"context"
	"strings"
	"testing"

	"github.com/crazy-airhead/aifei-go/db"
	"github.com/crazy-airhead/aifei-go/plugins/dataisolate"
)

const tenantYAML = `
dataisolate:
  policies: [tenant]
  tenant:
    strategy: shared
    column: tenant_id
    scope: { mode: auto }
  configs: [main]
`

// seedOrders inserts rows for two tenants using a bypass context.
func seedOrders(t *testing.T) {
	t.Helper()
	resetTables(t)
	bypass := dataisolate.Bypass(context.Background())
	mustInsert := func(tid, status string, creator int) {
		r := db.NewRow("orders").Set("tenant_id", tid).Set("status", status).Set("creator_id", creator)
		if _, err := db.WithCtx(bypass).InsertRow(r); err != nil {
			t.Fatalf("seed insert: %v", err)
		}
	}
	mustInsert("acme", "open", 1)
	mustInsert("acme", "closed", 2)
	mustInsert("globex", "open", 3)
	mustInsert("acme", "open", 3)
}

func tenantCtx0(tid string) context.Context {
	return dataisolate.WithPrincipal(context.Background(), &dataisolate.Principal{TenantID: tid})
}

func tenantCtx(tid string, uid int) context.Context {
	return dataisolate.WithPrincipal(context.Background(), &dataisolate.Principal{TenantID: tid, UserID: uid})
}

// TestTenantInsertStamps: an insert that omits tenant_id is stamped from the principal.
func TestTenantInsertStamps(t *testing.T) {
	startPlugin(t, tenantYAML)
	resetTables(t)
	ctx := tenantCtx("acme", 1)

	row := db.NewRow("orders").Set("status", "open")
	if _, err := db.WithCtx(ctx).InsertRow(row); err != nil {
		t.Fatalf("insert: %v", err)
	}
	rows, err := db.WithCtx(ctx).FindBy("orders", "id > ?", 0)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(rows) != 1 || rows[0].GetStr("tenant_id") != "acme" {
		t.Fatalf("expected 1 acme row, got %#v", rows)
	}
}

// TestTenantCrossTenantInvisible: each tenant sees only its own rows.
func TestTenantCrossTenantInvisible(t *testing.T) {
	startPlugin(t, tenantYAML)
	seedOrders(t)

	acme, err := db.WithCtx(tenantCtx("acme", 1)).FindBy("orders", "id > ?", 0)
	if err != nil {
		t.Fatalf("acme find: %v", err)
	}
	if len(acme) != 2 {
		t.Fatalf("acme should see 2 rows, saw %d", len(acme))
	}
	for _, r := range acme {
		if r.GetStr("tenant_id") != "acme" {
			t.Fatalf("acme saw foreign tenant %q", r.GetStr("tenant_id"))
		}
	}

	globex, err := db.WithCtx(tenantCtx("globex", 3)).FindBy("orders", "id > ?", 0)
	if err != nil {
		t.Fatalf("globex find: %v", err)
	}
	if len(globex) != 1 {
		t.Fatalf("globex should see 1 row, saw %d", len(globex))
	}
}

// TestTenantFindAll filters across the whole table.
func TestTenantFindAll(t *testing.T) {
	startPlugin(t, tenantYAML)
	seedOrders(t)
	rows, err := db.WithCtx(tenantCtx("acme", 1)).FindBy("orders", "id > ?", 0)
	if err != nil {
		t.Fatalf("findAll: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("acme findAll should return 2, got %d", len(rows))
	}
}

// TestTenantFindByIDScoped: a cross-tenant id is not visible via FindByID.
func TestTenantFindByIDScoped(t *testing.T) {
	startPlugin(t, tenantYAML)
	seedOrders(t)
	bypass := dataisolate.Bypass(context.Background())

	globexRows, err := db.WithCtx(bypass).FindBy("orders", "tenant_id = ?", "globex")
	if err != nil || len(globexRows) != 1 {
		t.Fatalf("seed globex lookup: err=%v rows=%d", err, len(globexRows))
	}
	globexID := globexRows[0].GetInt("id")

	// acme must not see globex's order
	row, err := db.WithCtx(tenantCtx("acme", 1)).FindByID("orders", globexID)
	if err != nil {
		t.Fatalf("findByID cross-tenant: %v", err)
	}
	if row != nil {
		t.Fatalf("acme must not see globex order id=%d", globexID)
	}

	// acme sees its own order
	acmeRows, err := db.WithCtx(bypass).FindBy("orders", "tenant_id = ?", "acme")
	if err != nil || len(acmeRows) == 0 {
		t.Fatalf("seed acme lookup: err=%v rows=%d", err, len(acmeRows))
	}
	acmeID := acmeRows[0].GetInt("id")
	row, err = db.WithCtx(tenantCtx("acme", 1)).FindByID("orders", acmeID)
	if err != nil {
		t.Fatalf("findByID own: %v", err)
	}
	if row == nil {
		t.Fatalf("acme should see its own order id=%d", acmeID)
	}
}

// TestTenantUpdateDeleteScoped: deleting by a cross-tenant id affects nothing.
func TestTenantUpdateDeleteScoped(t *testing.T) {
	startPlugin(t, tenantYAML)
	seedOrders(t)
	bypass := dataisolate.Bypass(context.Background())

	globexRows, err := db.WithCtx(bypass).FindBy("orders", "tenant_id = ?", "globex")
	if err != nil || len(globexRows) != 1 {
		t.Fatalf("seed globex lookup: err=%v rows=%d", err, len(globexRows))
	}
	globexID := globexRows[0].GetInt("id")

	// acme tries to delete globex's order by id
	ok, err := db.WithCtx(tenantCtx("acme", 1)).DeleteByID("orders", globexID)
	if err != nil {
		t.Fatalf("delete cross-tenant: %v", err)
	}
	if ok {
		t.Fatal("acme must not delete globex order (should be 0 affected)")
	}
	// globex still has its order
	globex, err := db.WithCtx(bypass).FindBy("orders", "tenant_id = ?", "globex")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(globex) != 1 {
		t.Fatalf("globex order should still exist, got %d", len(globex))
	}
}

// TestTenantPaginateBothSegments rewrites both the count and the data query.
func TestTenantPaginateBothSegments(t *testing.T) {
	startPlugin(t, tenantYAML)
	seedOrders(t)
	page, err := db.WithCtx(tenantCtx("acme", 1)).RawSql("SELECT * FROM orders").Paginate(1, 10)
	if err != nil {
		t.Fatalf("paginate: %v", err)
	}
	if page.TotalRows != 2 {
		t.Fatalf("acme count should be 2, got %d", page.TotalRows)
	}
	if len(page.Rows) != 2 {
		t.Fatalf("acme page should have 2 rows, got %d", len(page.Rows))
	}
}

// TestTenantBypassSeesAll: a bypass context skips isolation.
func TestTenantBypassSeesAll(t *testing.T) {
	startPlugin(t, tenantYAML)
	seedOrders(t)
	bypass := dataisolate.Bypass(context.Background())
	rows, err := db.WithCtx(bypass).FindBy("orders", "id > ?", 0)
	if err != nil {
		t.Fatalf("bypass find: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("bypass should see all 3 rows, got %d", len(rows))
	}
}

// TestTenantEnforceFailsClosed: with enforce=true, a controlled query without a principal
// errors rather than leaking.
func TestTenantEnforceFailsClosed(t *testing.T) {
	startPlugin(t, `
dataisolate:
  policies: [tenant]
  enforce: true
  tenant: { strategy: shared, column: tenant_id, scope: { mode: auto } }
  configs: [main]
`)
	seedOrders(t)
	_, err := db.WithCtx(context.Background()).FindBy("orders", "id > ?", 0)
	if err == nil {
		t.Fatal("expected error with enforce=true and no principal, got nil")
	}
	if !strings.Contains(err.Error(), "principal") {
		t.Fatalf("expected principal-related error, got %v", err)
	}
}

// TestTenantGlobalTableUntouched: a table without the tenant column passes through.
func TestTenantGlobalTableUntouched(t *testing.T) {
	startPlugin(t, tenantYAML)
	resetTables(t)
	bypass := dataisolate.Bypass(context.Background())
	if _, err := db.WithCtx(bypass).InsertRow(db.NewRow("logs").Set("msg", "hello")); err != nil {
		t.Fatalf("seed log: %v", err)
	}
	rows, err := db.WithCtx(tenantCtx("acme", 1)).FindBy("logs", "id > ?", 0)
	if err != nil {
		t.Fatalf("logs find: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("global table should be visible to any tenant, got %d", len(rows))
	}
}

// TestTenantRawSqlIsolated: a raw-SQL Find is also isolated (BeforeQuery + BeforeFind).
func TestTenantRawSqlIsolated(t *testing.T) {
	startPlugin(t, tenantYAML)
	seedOrders(t)
	rows, err := db.WithCtx(tenantCtx0("acme")).
		Sql("SELECT * FROM orders WHERE status = #para(status)", db.OfKv("status", "open")).Find()
	if err != nil {
		t.Fatalf("raw find: %v", err)
	}
	// acme has one open order (globex's open order must be excluded)
	if len(rows) != 1 || rows[0].GetStr("tenant_id") != "acme" {
		t.Fatalf("expected 1 acme open order, got %#v", rows)
	}
}
