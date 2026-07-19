package dataisolate_test

import (
	"context"
	"testing"

	"github.com/crazy-airhead/aifei-go/db"
	dataisolate "github.com/crazy-airhead/aifei-go/plugins/dataisolate"
)

// stubScopeProvider returns a rule computed by fn for the orders table only.
type stubScopeProvider struct {
	fn func(table string, p *dataisolate.Principal) (dataisolate.ScopeRule, bool)
}

func (s *stubScopeProvider) ScopeRule(table string, p *dataisolate.Principal) (dataisolate.ScopeRule, bool) {
	return s.fn(table, p)
}

// seedScopedOrders inserts acme orders with varied creator/dept, plus one globex order.
func seedScopedOrders(t *testing.T) {
	t.Helper()
	resetTables(t)
	bypass := dataisolate.Bypass(context.Background())
	ins := func(tid string, creator, dept int) {
		r := db.NewRow("orders").Set("tenant_id", tid).Set("status", "x").Set("creator_id", creator).Set("dept_id", dept)
		if _, err := db.WithCtx(bypass).InsertRow(r); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	ins("acme", 1, 10)
	ins("acme", 2, 10)
	ins("acme", 3, 20)
	ins("globex", 4, 10)
}

func scopeCtx(tid string, uid, dept int, deptTree []any, roles ...string) context.Context {
	return dataisolate.WithPrincipal(context.Background(), &dataisolate.Principal{
		TenantID: tid, UserID: uid, DeptID: dept, DeptTree: deptTree, Roles: roles,
	})
}

// TestScopeSelf: a viewer sees only rows they created (within their tenant).
func TestScopeSelf(t *testing.T) {
	provider := &stubScopeProvider{fn: func(table string, p *dataisolate.Principal) (dataisolate.ScopeRule, bool) {
		if table != "orders" {
			return dataisolate.ScopeRule{}, false
		}
		for _, r := range p.Roles {
			if r == "admin" {
				return dataisolate.ScopeRule{Type: dataisolate.ScopeAll}, true
			}
		}
		return dataisolate.ScopeRule{Type: dataisolate.ScopeSelf}, true
	}}
	startPlugin(t, `
dataisolate:
  policies: [tenant, scope]
  tenant: { strategy: shared, column: tenant_id, scope: { mode: auto } }
  configs: [main]
`, func(p *dataisolate.Plugin) { p.SetScopeProvider(provider) })
	seedScopedOrders(t)

	// user 1 (viewer): only their own order (creator=1)
	rows, err := db.WithCtx(scopeCtx("acme", 1, 10, nil, "viewer")).FindBy("orders", "id > ?", 0)
	if err != nil {
		t.Fatalf("self find: %v", err)
	}
	if len(rows) != 1 || rows[0].GetInt("creator_id") != 1 {
		t.Fatalf("viewer self should see 1 own row, got %d rows", len(rows))
	}

	// admin sees all acme orders
	rows, err = db.WithCtx(scopeCtx("acme", 9, 10, nil, "admin")).FindBy("orders", "id > ?", 0)
	if err != nil {
		t.Fatalf("admin find: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("admin should see all 3 acme rows, got %d", len(rows))
	}
}

// TestScopeDept: a user sees their whole department's rows.
func TestScopeDept(t *testing.T) {
	provider := &stubScopeProvider{fn: func(table string, p *dataisolate.Principal) (dataisolate.ScopeRule, bool) {
		if table != "orders" {
			return dataisolate.ScopeRule{}, false
		}
		return dataisolate.ScopeRule{Type: dataisolate.ScopeDept}, true
	}}
	startPlugin(t, `
dataisolate:
  policies: [tenant, scope]
  tenant: { strategy: shared, column: tenant_id, scope: { mode: auto } }
  configs: [main]
`, func(p *dataisolate.Plugin) { p.SetScopeProvider(provider) })
	seedScopedOrders(t)

	// dept 10 has 2 acme orders (creator 1, 2)
	rows, err := db.WithCtx(scopeCtx("acme", 1, 10, nil)).FindBy("orders", "id > ?", 0)
	if err != nil {
		t.Fatalf("dept find: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("dept 10 should see 2 rows, got %d", len(rows))
	}
}

// TestScopeDeptAndBelow uses an IN(...) over the pre-resolved dept tree.
func TestScopeDeptAndBelow(t *testing.T) {
	provider := &stubScopeProvider{fn: func(table string, p *dataisolate.Principal) (dataisolate.ScopeRule, bool) {
		if table != "orders" {
			return dataisolate.ScopeRule{}, false
		}
		return dataisolate.ScopeRule{Type: dataisolate.ScopeDeptAndBelow}, true
	}}
	startPlugin(t, `
dataisolate:
  policies: [tenant, scope]
  tenant: { strategy: shared, column: tenant_id, scope: { mode: auto } }
  configs: [main]
`, func(p *dataisolate.Plugin) { p.SetScopeProvider(provider) })
	seedScopedOrders(t)

	// dept tree [10, 20] covers all 3 acme orders
	rows, err := db.WithCtx(scopeCtx("acme", 1, 10, []any{10, 20})).FindBy("orders", "id > ?", 0)
	if err != nil {
		t.Fatalf("tree find: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("dept tree [10,20] should see 3 rows, got %d", len(rows))
	}
}

// ---- field isolation ----

// stubFieldProvider denies "password" for non-admins on the users table.
type stubFieldProvider struct{}

func (stubFieldProvider) Rule(table string, p *dataisolate.Principal) (dataisolate.FieldRule, bool) {
	if table != "users" {
		return dataisolate.FieldRule{}, false
	}
	for _, r := range p.Roles {
		if r == "admin" {
			return dataisolate.FieldRule{}, false // admin sees everything
		}
	}
	return dataisolate.FieldRule{Mode: dataisolate.FieldDenylist, Fields: []string{"password"}}, true
}

func seedUsers(t *testing.T) {
	t.Helper()
	resetTables(t)
	bypass := dataisolate.Bypass(context.Background())
	r := db.NewRow("users").Set("tenant_id", "acme").Set("name", "alice").Set("password", "secret")
	if _, err := db.WithCtx(bypass).InsertRow(r); err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

// TestFieldMaskNull: a regular user gets password masked to NULL; an admin sees it.
func TestFieldMaskNull(t *testing.T) {
	startPlugin(t, `
dataisolate:
  policies: [tenant, field]
  tenant: { strategy: shared, column: tenant_id, scope: { mode: auto } }
  field: { default_mask: null }
  configs: [main]
`, func(p *dataisolate.Plugin) { p.SetFieldProvider(stubFieldProvider{}) })
	seedUsers(t)

	viewer := dataisolate.WithPrincipal(context.Background(), &dataisolate.Principal{TenantID: "acme", UserID: 1, Roles: []string{"viewer"}})
	rows, err := db.WithCtx(viewer).RawSql("SELECT * FROM users").Find()
	if err != nil {
		t.Fatalf("viewer find: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 user, got %d", len(rows))
	}
	if pw, present := rows[0].Get("password").(string); present && pw != "" {
		t.Fatalf("viewer password should be NULL/masked, got %q", pw)
	}
	if rows[0].GetStr("name") != "alice" {
		t.Fatalf("non-sensitive column should still be visible, got name=%q", rows[0].GetStr("name"))
	}

	admin := dataisolate.WithPrincipal(context.Background(), &dataisolate.Principal{TenantID: "acme", UserID: 1, Roles: []string{"admin"}})
	rows, err = db.WithCtx(admin).RawSql("SELECT * FROM users").Find()
	if err != nil {
		t.Fatalf("admin find: %v", err)
	}
	if rows[0].GetStr("password") != "secret" {
		t.Fatalf("admin password should be visible, got %q", rows[0].GetStr("password"))
	}
}

// TestFieldMaskRemove drops the denied column entirely.
func TestFieldMaskRemove(t *testing.T) {
	startPlugin(t, `
dataisolate:
  policies: [tenant, field]
  tenant: { strategy: shared, column: tenant_id, scope: { mode: auto } }
  field: { default_mask: remove }
  configs: [main]
`, func(p *dataisolate.Plugin) { p.SetFieldProvider(stubFieldProvider{}) })
	seedUsers(t)

	viewer := dataisolate.WithPrincipal(context.Background(), &dataisolate.Principal{TenantID: "acme", UserID: 1, Roles: []string{"viewer"}})
	rows, err := db.WithCtx(viewer).RawSql("SELECT * FROM users").Find()
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if rows[0].Has("password") {
		t.Fatalf("password column should be removed, present in row")
	}
}
