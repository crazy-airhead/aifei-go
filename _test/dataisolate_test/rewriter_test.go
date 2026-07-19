package dataisolate_test

import (
	"reflect"
	"strings"
	"testing"

	dataisolate "github.com/crazy-airhead/aifei-go/plugins/dataisolate"
)

// norm collapses runs of whitespace and lowercases so SQL comparisons are independent of
// keyword case and layout (parsers canonicalize case differently).
func norm(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

func tenantChain(mode string, ignore, force []string) dataisolate.PolicyChain {
	return dataisolate.PolicyChain{&dataisolate.TenantPolicy{
		Column: "tenant_id", Mode: mode, IgnoreTables: ignore, Tables: force,
	}}
}

func principal(tenant string) *dataisolate.Principal {
	return &dataisolate.Principal{TenantID: tenant}
}

func rewriteOK(t *testing.T, sql string, args []interface{}, p *dataisolate.Principal, chain dataisolate.PolicyChain) (string, []interface{}) {
	t.Helper()
	out, outArgs, st := dataisolate.Rewrite(sql, args, p, chain)
	if st != dataisolate.StatusRewritten {
		t.Fatalf("expected StatusRewritten, got %d for %q", st, sql)
	}
	return out, outArgs
}

// TestTenantSelectInject checks WHERE injection into a SELECT that already has a WHERE.
func TestTenantSelectInject(t *testing.T) {
	out, args := rewriteOK(t,
		"SELECT id, status FROM orders WHERE id = ? AND status = ?",
		[]interface{}{10, "open"}, principal("acme"), tenantChain("auto", nil, nil))
	if want := "select id, status from orders where id = ? and status = ? and tenant_id = ?"; norm(out) != want {
		t.Fatalf("sql: got %q want %q", norm(out), want)
	}
	if !reflect.DeepEqual(args, []interface{}{10, "open", "acme"}) {
		t.Fatalf("args: got %v", args)
	}
}

// TestTenantSelectNoWhere creates a WHERE when none exists.
func TestTenantSelectNoWhere(t *testing.T) {
	out, args := rewriteOK(t,
		"SELECT * FROM orders", nil, principal("acme"), tenantChain("auto", nil, nil))
	if want := "select * from orders where tenant_id = ?"; norm(out) != want {
		t.Fatalf("got %q want %q", norm(out), want)
	}
	if !reflect.DeepEqual(args, []interface{}{"acme"}) {
		t.Fatalf("args: got %v", args)
	}
}

// TestTenantUpdateDelete verifies UPDATE/DELETE WHERE injection.
func TestTenantUpdateDelete(t *testing.T) {
	cases := []struct {
		sql  string
		args []interface{}
		want string
	}{
		{"UPDATE orders SET status = ? WHERE id = ?", []interface{}{"x", 1},
			"update orders set status = ? where id = ? and tenant_id = ?"},
		{"DELETE FROM orders WHERE id = ?", []interface{}{1},
			"delete from orders where id = ? and tenant_id = ?"},
	}
	for _, c := range cases {
		out, args := rewriteOK(t, c.sql, c.args, principal("acme"), tenantChain("auto", nil, nil))
		if norm(out) != c.want {
			t.Fatalf("got %q want %q", norm(out), c.want)
		}
		wantArgs := append(append([]interface{}{}, c.args...), "acme")
		if !reflect.DeepEqual(args, wantArgs) {
			t.Fatalf("args: got %v want %v", args, wantArgs)
		}
	}
}

// TestTenantRecursesIntoSubquery verifies injection reaches the inner subquery too.
func TestTenantRecursesIntoSubquery(t *testing.T) {
	out, args := rewriteOK(t,
		"SELECT * FROM orders WHERE id IN (SELECT id FROM orders WHERE x = ?) AND y = ?",
		[]interface{}{1, 2}, principal("acme"), tenantChain("auto", nil, nil))
	want := "select * from orders where id in (select id from orders where x = ? and tenant_id = ?) and y = ? and tenant_id = ?"
	if norm(out) != want {
		t.Fatalf("got %q want %q", norm(out), want)
	}
	if !reflect.DeepEqual(args, []interface{}{1, "acme", 2, "acme"}) {
		t.Fatalf("args: got %v", args)
	}
}

// TestTenantJoinAliases qualifies each controlled table by its alias.
func TestTenantJoinAliases(t *testing.T) {
	out, args := rewriteOK(t,
		"SELECT o.id FROM orders o JOIN users u ON o.id = u.id WHERE o.id = ?",
		[]interface{}{5}, principal("acme"), tenantChain("auto", nil, nil))
	want := "select o.id from orders o inner join users u on o.id = u.id where o.id = ? and o.tenant_id = ? and u.tenant_id = ?"
	if norm(out) != want {
		t.Fatalf("got %q want %q", norm(out), want)
	}
	if !reflect.DeepEqual(args, []interface{}{5, "acme", "acme"}) {
		t.Fatalf("args: got %v", args)
	}
}

// TestTenantIgnoresGlobalTable: a table without the tenant column is not filtered.
func TestTenantIgnoresGlobalTable(t *testing.T) {
	out, _, st := dataisolate.Rewrite("SELECT * FROM logs", nil, principal("acme"), tenantChain("auto", nil, nil))
	if st != dataisolate.StatusSkippedNoScoped {
		t.Fatalf("expected skipped, got %d (%q)", st, out)
	}
	if out != "SELECT * FROM logs" {
		t.Fatalf("expected unchanged, got %q", out)
	}
}

// TestTenantIgnoreTables exempts an otherwise-controlled table.
func TestTenantIgnoreTables(t *testing.T) {
	out, _, st := dataisolate.Rewrite("SELECT * FROM orders", nil, principal("acme"),
		tenantChain("auto", []string{"orders"}, nil))
	if st != dataisolate.StatusSkippedNoScoped {
		t.Fatalf("expected skipped, got %d (%q)", st, out)
	}
}

// TestTenantWhitelistMode only filters explicitly forced tables.
func TestTenantWhitelistMode(t *testing.T) {
	out, _, st := dataisolate.Rewrite("SELECT * FROM orders", nil, principal("acme"),
		tenantChain("whitelist", nil, nil))
	if st != dataisolate.StatusSkippedNoScoped {
		t.Fatalf("orders without force should be skipped in whitelist mode, got %d (%q)", st, out)
	}
	out2, _, st2 := dataisolate.Rewrite("SELECT * FROM orders", nil, principal("acme"),
		tenantChain("whitelist", nil, []string{"orders"}))
	if st2 != dataisolate.StatusRewritten {
		t.Fatalf("forced orders should rewrite in whitelist mode, got %d (%q)", st2, out2)
	}
}

// TestNoPrincipalSkips: without a tenant, nothing is injected (skip).
func TestNoPrincipalSkips(t *testing.T) {
	out, _, st := dataisolate.Rewrite("SELECT * FROM orders", nil, nil, tenantChain("auto", nil, nil))
	if st != dataisolate.StatusSkippedNoScoped {
		t.Fatalf("expected skipped, got %d (%q)", st, out)
	}
}

// TestPostgresSyntaxNowParses: GoSQLX parses PostgreSQL-specific syntax (e.g. the `::`
// cast) natively, so such statements are isolated rather than fail-closed — an improvement
// over the vitess-sqlparser backend, which rejected them. aifei renders `?` placeholders,
// so the input uses `?` (never PG `$N`).
func TestPostgresSyntaxNowParses(t *testing.T) {
	out, args, st := dataisolate.Rewrite(
		"SELECT * FROM orders WHERE id::int = ?", []interface{}{1},
		principal("acme"), tenantChain("auto", nil, nil))
	if st != dataisolate.StatusRewritten {
		t.Fatalf("expected StatusRewritten for PG cast syntax, got %d (%s)", st, out)
	}
	if !reflect.DeepEqual(args, []interface{}{1, "acme"}) {
		t.Fatalf("args: got %v", args)
	}
}

// TestMalformedFailsClosed: a genuinely unparseable statement still fails closed.
func TestMalformedFailsClosed(t *testing.T) {
	for _, bad := range []string{
		"THIS IS NOT SQL AT ALL",
		"SELECT * FROM orders WHERE",
	} {
		_, _, st := dataisolate.Rewrite(bad, nil, principal("acme"), tenantChain("auto", nil, nil))
		if st != dataisolate.StatusFailed {
			t.Fatalf("expected StatusFailed for malformed %q, got %d", bad, st)
		}
	}
}

// TestPlaceholderInStringLiteral: a colon inside a string literal is not a bind var.
func TestPlaceholderInStringLiteral(t *testing.T) {
	out, args := rewriteOK(t,
		"SELECT * FROM orders WHERE note = 'a:b' AND id = ?",
		[]interface{}{7}, principal("acme"), tenantChain("auto", nil, nil))
	want := "select * from orders where note = 'a:b' and id = ? and tenant_id = ?"
	if norm(out) != want {
		t.Fatalf("got %q want %q", norm(out), want)
	}
	if !reflect.DeepEqual(args, []interface{}{7, "acme"}) {
		t.Fatalf("args: got %v", args)
	}
}

// TestEmptyChainSkips: no policies → no work, skipped.
func TestEmptyChainSkips(t *testing.T) {
	out, _, st := dataisolate.Rewrite("SELECT * FROM orders", nil, principal("acme"), nil)
	if st != dataisolate.StatusSkippedNoScoped {
		t.Fatalf("expected skipped, got %d", st)
	}
	if out != "SELECT * FROM orders" {
		t.Fatalf("expected unchanged, got %q", out)
	}
}
