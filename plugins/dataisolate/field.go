package dataisolate

import (
	"sort"
	"strings"

	"github.com/ajitpratap0/GoSQLX/pkg/sql/ast"

	"github.com/crazy-airhead/aifei-go/db"
)

// fieldPolicy implements column isolation: it rewrites only the OUTERMOST SELECT's
// projection (never recursing into subqueries / UNION branches / CTEs — see §10.2),
// expanding `*`/`t.*` to explicit columns and masking or removing denied columns. Rules
// are resolved at query time by the application-supplied FieldRuleProvider.
type fieldPolicy struct {
	cfg      *Config
	provider FieldRuleProvider
}

// newFieldPolicy returns a field policy, or nil when no provider is registered (field
// enabled but unimplemented → no-op).
func newFieldPolicy(cfg *Config, mgr *Manager) Policy {
	if mgr == nil || mgr.FieldProvider() == nil {
		return nil
	}
	return &fieldPolicy{cfg: cfg, provider: mgr.FieldProvider()}
}

// Name implements Policy.
func (fp *fieldPolicy) Name() string { return PolicyField }

// Apply implements Policy. It acts on the outermost SELECT only.
func (fp *fieldPolicy) Apply(stmt ast.Statement, p *Principal, pc *ParamCollector) bool {
	if p == nil || fp.provider == nil {
		return false
	}
	sel := topSelect(stmt)
	if sel == nil {
		return false
	}
	refs := selectTableRefs(sel)
	if len(refs) == 0 {
		return false // SELECT without FROM: nothing to mask
	}
	out, changed := fp.rewriteProjection(sel.Columns, refs, p, pc)
	if !changed {
		return false
	}
	sel.Columns = out
	return true
}

// topSelect returns the outermost *SelectStatement of a statement. A top-level UNION is
// NOT masked (rewriting one branch breaks column alignment).
func topSelect(stmt ast.Statement) *ast.SelectStatement {
	if s, ok := stmt.(*ast.SelectStatement); ok {
		return s
	}
	return nil
}

func (fp *fieldPolicy) rewriteProjection(cols []ast.Expression, refs []tableRef, p *Principal, pc *ParamCollector) ([]ast.Expression, bool) {
	changed := false
	out := make([]ast.Expression, 0, len(cols))
	for _, e := range cols {
		switch x := e.(type) {
		case *ast.Identifier:
			if x.Name == "*" {
				exp, ch := fp.expandStar(x, refs, p, pc)
				out = append(out, exp...)
				changed = changed || ch
				continue
			}
			se, drop, ch := fp.rewriteIdentColumn(x, refs, p, pc)
			if !drop {
				out = append(out, se)
			}
			changed = changed || ch
		case *ast.AliasedExpression:
			se, drop, ch := fp.rewriteAliased(x, refs, p, pc)
			if !drop {
				out = append(out, se)
			}
			changed = changed || ch
		default:
			out = append(out, e) // FunctionCall / expression → unfilterable, keep
		}
	}
	return out, changed
}

// expandStar turns `*` / `t.*` into explicit columns (kept or masked). Unregistered
// tables cannot be expanded and are left as a star (best-effort).
func (fp *fieldPolicy) expandStar(star *ast.Identifier, refs []tableRef, p *Principal, pc *ParamCollector) ([]ast.Expression, bool) {
	targets := starTargets(star, refs)
	if len(targets) == 0 {
		return []ast.Expression{star}, false
	}
	out := make([]ast.Expression, 0, len(refs)*4)
	changed := false
	for _, t := range targets {
		tb := db.GetTableByName(t.table)
		cols := tableColumns(tb)
		if len(cols) == 0 {
			out = append(out, &ast.Identifier{Name: "*", Table: t.table}) // keep t.* as-is
			continue
		}
		qual := t.qualifier(len(refs))
		rule, hasRule := fp.provider.Rule(t.table, p)
		for _, col := range cols {
			se, drop, ch := emitColumn(qual, col, rule, hasRule, fp.cfg.Field.DefaultMask, pc)
			changed = changed || ch
			if !drop {
				out = append(out, se)
			}
		}
	}
	return out, changed
}

// rewriteIdentColumn handles a bare/qualified column projection.
func (fp *fieldPolicy) rewriteIdentColumn(id *ast.Identifier, refs []tableRef, p *Principal, pc *ParamCollector) (ast.Expression, bool, bool) {
	col := id.Name
	table := ownerOf(id, refs)
	if table == "" {
		return id, false, false // can't resolve owner → keep
	}
	rule, hasRule := fp.provider.Rule(table, p)
	if !hasRule || !isDenied(rule, col) {
		return id, false, false // allowed → keep as-is
	}
	se, drop := maskExpr(col, rule, fp.cfg.Field.DefaultMask, pc)
	return se, drop, true
}

// rewriteAliased handles `expr AS alias`. Non-column exprs are kept unchanged.
func (fp *fieldPolicy) rewriteAliased(ae *ast.AliasedExpression, refs []tableRef, p *Principal, pc *ParamCollector) (ast.Expression, bool, bool) {
	id, ok := ae.Expr.(*ast.Identifier)
	if !ok || id.Name == "*" {
		return ae, false, false // expression / star-in-alias → unfilterable, keep
	}
	col := id.Name
	table := ownerOf(id, refs)
	if table == "" {
		return ae, false, false
	}
	rule, hasRule := fp.provider.Rule(table, p)
	if !hasRule || !isDenied(rule, col) {
		return ae, false, false
	}
	label := col
	if ae.Alias != "" {
		label = ae.Alias
	}
	se, drop := maskExpr(label, rule, fp.cfg.Field.DefaultMask, pc)
	return se, drop, true
}

// emitColumn builds the projection entry for one expanded column: kept as qual.col, or
// masked/removed when denied. Returns (expr, drop, changed).
func emitColumn(qual, col string, rule FieldRule, hasRule bool, defaultMask string, pc *ParamCollector) (ast.Expression, bool, bool) {
	if !hasRule || !isDenied(rule, col) {
		return &ast.Identifier{Name: col, Table: qual}, false, false
	}
	se, drop := maskExpr(col, rule, defaultMask, pc)
	return se, drop, true
}

// maskExpr produces the masked projection for a denied column, preserving its label.
func maskExpr(label string, rule FieldRule, defaultMask string, pc *ParamCollector) (ast.Expression, bool) {
	switch effectiveMask(rule, defaultMask) {
	case MaskRemove:
		return nil, true
	case MaskConstant:
		return &ast.AliasedExpression{Expr: bindVal(pc.Bind(rule.Constant)), Alias: label}, false
	default: // MaskNull
		return &ast.AliasedExpression{Expr: &ast.LiteralValue{Value: nil, Type: "null"}, Alias: label}, false
	}
}

// isDenied reports whether col is denied by rule.
func isDenied(rule FieldRule, col string) bool {
	if len(rule.Fields) == 0 {
		return false
	}
	for _, f := range rule.Fields {
		if f == col {
			return rule.Mode == FieldDenylist
		}
	}
	return rule.Mode == FieldAllowlist // not listed: denied under allowlist, allowed under denylist
}

// effectiveMask resolves the mask strategy: an explicit rule override wins; otherwise the
// global default applies (MaskNull rules defer to the configured default).
func effectiveMask(rule FieldRule, defaultMask string) MaskStrategy {
	switch rule.Mask {
	case MaskConstant, MaskRemove:
		return rule.Mask
	}
	switch defaultMask {
	case "constant":
		return MaskConstant
	case "remove":
		return MaskRemove
	default:
		return MaskNull
	}
}

// starTargets resolves which tables a `*` / `t.*` spans.
func starTargets(star *ast.Identifier, refs []tableRef) []tableRef {
	if star.Table == "" {
		return refs
	}
	for _, r := range refs {
		if r.alias == star.Table || r.table == star.Table {
			return []tableRef{r}
		}
	}
	return nil
}

// ownerOf resolves the owning registered table of a projected column: the qualifier's
// table when qualified; the unique owner (or primary table on ambiguity) when bare.
func ownerOf(id *ast.Identifier, refs []tableRef) string {
	col := id.Name
	if id.Table != "" {
		for _, r := range refs {
			if r.alias == id.Table || r.table == id.Table {
				return r.table
			}
		}
		return ""
	}
	var owners []string
	for _, r := range refs {
		if tableHasColumn(r.table, col) {
			owners = append(owners, r.table)
		}
	}
	switch len(owners) {
	case 1:
		return owners[0]
	case 0:
		return ""
	default:
		return owners[0] // ambiguous → primary table wins (refs[0] is primary)
	}
}

// tableColumns returns the registered column list of a table in declared order (from
// Table.Fields), falling back to sorted FieldTypes keys.
func tableColumns(t *db.Table) []string {
	if t == nil {
		return nil
	}
	if s := strings.TrimSpace(t.Fields); s != "" {
		parts := strings.Split(s, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if c := strings.TrimSpace(p); c != "" {
				out = append(out, c)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	if t.FieldTypes != nil {
		out := make([]string, 0, len(t.FieldTypes))
		for k := range t.FieldTypes {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	return nil
}
