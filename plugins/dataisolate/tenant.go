package dataisolate

import (
	"github.com/ajitpratap0/GoSQLX/pkg/sql/ast"
)

// TenantPolicy implements the shared-table tenant strategy (③): it injects
// "<alias>.<tenantCol> = ?" for every controlled table referenced in each DML node's
// FROM/JOIN/USING (recursively into subqueries / UNION branches / CTEs). INSERT
// row-stamping is handled by the hook (BeforeRowInsert), not here.
type TenantPolicy struct {
	// Column is the global default tenant column (default "tenant_id").
	Column string
	// Mode: "auto" (default, controlled = has the column), "whitelist" (only Tables),
	// "all" (every table).
	Mode string
	// IgnoreTables are exempted (global/cross-tenant tables).
	IgnoreTables []string
	// Tables are force-included regardless of mode.
	Tables []string
}

// Name implements Policy.
func (tp *TenantPolicy) Name() string { return PolicyTenant }

// RedundantFor implements guardedPolicy: when the SQL already references the tenant
// column (e.g. a db.Sql template using #and(tenant_id, ...)), the hook skips this
// policy to avoid double-injection. Coarse and conservative — a hit only skips.
func (tp *TenantPolicy) RedundantFor(sql string) bool {
	col := tp.Column
	if col == "" {
		col = "tenant_id"
	}
	return containsColumnWord(sql, col)
}

// Apply implements Policy. It is a no-op (returns false) when there is no tenant id.
func (tp *TenantPolicy) Apply(stmt ast.Statement, p *Principal, pc *ParamCollector) bool {
	if p == nil || p.TenantID == "" {
		return false
	}
	col := tp.Column
	if col == "" {
		col = "tenant_id"
	}
	ignore := toSet(tp.IgnoreTables)
	force := toSet(tp.Tables)
	return injectWherePredicates(stmt, func(refs []tableRef) []ast.Expression {
		var preds []ast.Expression
		for _, r := range refs {
			if r.table == "" {
				continue
			}
			if !tenantControlled(r.table, tp.Mode, ignore, force, col) {
				continue
			}
			preds = append(preds, eqCol(r.qualifier(len(refs)), col, pc.Bind(p.TenantID)))
		}
		return preds
	})
}

// tenantControlled decides whether a table is subject to tenant filtering. Precedence:
// ignore_tables → tables (force) → mode → metadata (has the column).
func tenantControlled(table, mode string, ignore, force map[string]bool, col string) bool {
	if ignore[table] {
		return false
	}
	if force[table] {
		return true
	}
	switch mode {
	case "all":
		return true
	case "whitelist":
		return false
	default: // "auto"
		return tableHasColumn(table, col)
	}
}

func toSet(items []string) map[string]bool {
	if len(items) == 0 {
		return nil
	}
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}
