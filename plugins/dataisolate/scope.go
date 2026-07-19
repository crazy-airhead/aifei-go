package dataisolate

import (
	"errors"

	"github.com/ajitpratap0/GoSQLX/pkg/sql/ast"
)

// errMissingScopeValue is surfaced (via ParamCollector.Fail) when a scope rule needs a
// principal value that is absent while enforce=true.
var errMissingScopeValue = errors.New("dataisolate: scope rule needs a principal value that is absent (enforce=true)")

// scopePolicy implements row isolation: it injects a range predicate (self / dept /
// dept-tree / region / custom) per controlled table, resolved at query time by the
// application-supplied ScopeRuleProvider. The plugin never merges roles — the provider
// returns the already-merged rule (per cfg.Scope.Merge).
type scopePolicy struct {
	cfg      *Config
	provider ScopeRuleProvider
}

// newScopePolicy returns a scope policy, or nil when no provider is registered (scope
// enabled but unimplemented → the policy is a deliberate no-op).
func newScopePolicy(cfg *Config, mgr *Manager) Policy {
	if mgr == nil || mgr.ScopeProvider() == nil {
		return nil
	}
	return &scopePolicy{cfg: cfg, provider: mgr.ScopeProvider()}
}

// Name implements Policy.
func (sp *scopePolicy) Name() string { return PolicyScope }

// Apply implements Policy.
func (sp *scopePolicy) Apply(stmt ast.Statement, p *Principal, pc *ParamCollector) bool {
	if p == nil || sp.provider == nil {
		return false
	}
	return injectWherePredicates(stmt, func(refs []tableRef) []ast.Expression {
		var preds []ast.Expression
		for _, r := range refs {
			if r.table == "" {
				continue
			}
			rule, ok := sp.provider.ScopeRule(r.table, p)
			if !ok || rule.Type == ScopeAll {
				continue
			}
			pr := scopePredicate(r.table, r.qualifier(len(refs)), rule, p, sp.cfg.Enforce, pc)
			if pr != nil {
				preds = append(preds, pr)
			}
			if pc.Failed() != nil {
				return preds
			}
		}
		return preds
	})
}

// scopePredicate builds the WHERE predicate for one (table, rule, principal). It returns
// nil (no predicate) when the required column/value is absent; when enforce is true and a
// value is missing it fails the rewrite instead.
func scopePredicate(table, qual string, rule ScopeRule, p *Principal, enforce bool, pc *ParamCollector) ast.Expression {
	switch rule.Type {
	case ScopeSelf:
		col := TableMetaOf(table).CreatorCol
		return presetPredicate(table, qual, col, p.UserID, enforce, pc)
	case ScopeDept:
		col := TableMetaOf(table).DeptCol
		return presetPredicate(table, qual, col, p.DeptID, enforce, pc)
	case ScopeRegion:
		col := TableMetaOf(table).RegionCol
		return presetPredicate(table, qual, col, p.RegionID, enforce, pc)
	case ScopeDeptAndBelow:
		col := TableMetaOf(table).DeptCol
		if col == "" || !tableHasColumn(table, col) {
			return nil
		}
		vals := nonNilSlice(p.DeptTree)
		if len(vals) == 0 {
			if enforce {
				pc.Fail(errMissingScopeValue)
			}
			return nil
		}
		return inPredicate(qual, col, pc, vals)
	case ScopeCustom:
		if rule.Column == "" || !tableHasColumn(table, rule.Column) {
			return nil
		}
		return customPredicate(qual, rule, enforce, pc)
	}
	return nil
}

// presetPredicate handles the single-value preset types (self/dept/region).
func presetPredicate(table, qual, col string, val any, enforce bool, pc *ParamCollector) ast.Expression {
	if col == "" || !tableHasColumn(table, col) {
		return nil
	}
	if val == nil {
		if enforce {
			pc.Fail(errMissingScopeValue)
		}
		return nil
	}
	return eqCol(qual, col, pc.Bind(val))
}

// inPredicate builds "<qual>.<col> IN (?, ?, ...)".
func inPredicate(qual, col string, pc *ParamCollector, vals []any) ast.Expression {
	list := make([]ast.Expression, 0, len(vals))
	for _, v := range vals {
		list = append(list, bindVal(pc.Bind(v)))
	}
	return &ast.InExpression{Expr: qualifiedCol(qual, col), List: list}
}

// customPredicate builds a structured custom predicate from Column/Op/Values.
func customPredicate(qual string, rule ScopeRule, enforce bool, pc *ParamCollector) ast.Expression {
	left := qualifiedCol(qual, rule.Column)
	vals := nonNilSlice(rule.Values)
	need := func(n int) bool {
		if len(vals) < n {
			if enforce {
				pc.Fail(errMissingScopeValue)
			}
			return false
		}
		return true
	}
	switch rule.Op {
	case OpEq, OpNeq:
		if !need(1) {
			return nil
		}
		op := "="
		if rule.Op == OpNeq {
			op = "<>"
		}
		return &ast.BinaryExpression{Left: left, Operator: op, Right: bindVal(pc.Bind(vals[0]))}
	case OpLt, OpLte, OpGt, OpGte:
		if !need(1) {
			return nil
		}
		return &ast.BinaryExpression{Left: left, Operator: scopeOpStr(rule.Op), Right: bindVal(pc.Bind(vals[0]))}
	case OpLike:
		if !need(1) {
			return nil
		}
		return &ast.BinaryExpression{Left: left, Operator: "LIKE", Right: bindVal(pc.Bind(vals[0]))}
	case OpIn, OpNotIn:
		if !need(1) {
			return nil
		}
		expr := inPredicate(qual, rule.Column, pc, vals)
		if ie, ok := expr.(*ast.InExpression); ok && rule.Op == OpNotIn {
			ie.Not = true
		}
		return expr
	case OpBetween:
		if !need(2) {
			return nil
		}
		return &ast.BetweenExpression{
			Expr:  left,
			Lower: bindVal(pc.Bind(vals[0])),
			Upper: bindVal(pc.Bind(vals[1])),
		}
	}
	return nil
}

func scopeOpStr(op ScopeOp) string {
	switch op {
	case OpLt:
		return "<"
	case OpLte:
		return "<="
	case OpGt:
		return ">"
	case OpGte:
		return ">="
	}
	return "="
}

func nonNilSlice(in []any) []any {
	out := in[:0:0]
	for _, v := range in {
		if v != nil {
			out = append(out, v)
		}
	}
	return out
}
