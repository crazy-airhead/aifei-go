package dataisolate

import (
	"strconv"
	"strings"

	"github.com/ajitpratap0/GoSQLX/pkg/gosqlx"
	"github.com/ajitpratap0/GoSQLX/pkg/sql/ast"
)

// Rewrite parses sql, runs the PolicyChain over the AST, rebuilds the SQL and realigns
// args so placeholders and parameters stay in lockstep.
//
// GoSQLX parses PostgreSQL-style $N placeholders only (the MySQL/SQLite "?" that aifei
// renders is rejected by its tokenizer), so Rewrite first converts each "?" to a $N in
// source order (mapping $N → args[N-1]), parses, rewrites, renders, then converts every
// $N back to "?" with the aligned args. Injected predicates use the same $N scheme
// (continuing the counter), so original and injected placeholders are uniform.
//
// StatusSkippedNoScoped: parsed but no policy modified it — return original sql/args.
// StatusFailed: parse error (note: PostgreSQL-specific syntax now parses fine, so this
// is rarer than with vitess), a policy signaled an unsafe rewrite, or placeholder
// alignment failed — the hook must abort (fail-closed).
// StatusRewritten: a policy modified the statement; rebuilt sql + realigned args.
func Rewrite(sql string, args []interface{}, p *Principal, chain PolicyChain) (string, []interface{}, Status) {
	if len(chain) == 0 {
		return sql, args, StatusSkippedNoScoped
	}
	conv, vals, ok := dollarizePlaceholders(sql, args)
	if !ok {
		return "", nil, StatusFailed
	}
	tree, err := gosqlx.Parse(conv)
	if err != nil {
		return "", nil, StatusFailed
	}
	if len(tree.Statements) == 0 {
		return sql, args, StatusSkippedNoScoped
	}
	stmt := tree.Statements[0]
	pc := newParamCollector(vals)
	changed := false
	for _, pol := range chain {
		if pol.Apply(stmt, p, pc) {
			changed = true
		}
		if pc.Failed() != nil {
			return "", nil, StatusFailed
		}
	}
	if !changed {
		return sql, args, StatusSkippedNoScoped
	}
	rendered := tree.Format(ast.CompactStyle())
	outSQL, outArgs, ok := undollarize(rendered, pc.Values())
	if !ok {
		return "", nil, StatusFailed
	}
	return outSQL, outArgs, StatusRewritten
}

// dollarizePlaceholders converts each "?" in sql to "$N" (N from 1, source order) while
// skipping string literals and backtick identifiers, and pairs $N with args[N-1]. It
// fails when there are more "?" than args.
func dollarizePlaceholders(sql string, args []interface{}) (string, []interface{}, bool) {
	var b strings.Builder
	vals := make([]interface{}, 0, len(args))
	inStr, inIdent := false, false
	n := len(sql)
	count := 0
	for i := 0; i < n; i++ {
		c := sql[i]
		switch {
		case inStr:
			b.WriteByte(c)
			if c == '\\' && i+1 < n {
				b.WriteByte(sql[i+1])
				i++
			} else if c == '\'' {
				inStr = false
			}
		case inIdent:
			b.WriteByte(c)
			if c == '`' {
				inIdent = false
			}
		case c == '\'':
			inStr = true
			b.WriteByte(c)
		case c == '`':
			inIdent = true
			b.WriteByte(c)
		case c == '?':
			count++
			if count > len(args) {
				return "", nil, false
			}
			vals = append(vals, args[count-1])
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(count))
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), vals, true
}

// undollarize converts every "$N" token in the rendered SQL back to a positional "?" and
// produces the aligned args slice, in textual order, skipping string literals and
// backtick identifiers. A bare "$" (not followed by digits, e.g. a dollar-quote) is left
// untouched. An out-of-range $N yields ok=false (fail-closed).
func undollarize(rendered string, vals []interface{}) (string, []interface{}, bool) {
	var b strings.Builder
	var out []interface{}
	inStr, inIdent := false, false
	n := len(rendered)
	for i := 0; i < n; i++ {
		c := rendered[i]
		switch {
		case inStr:
			b.WriteByte(c)
			if c == '\\' && i+1 < n {
				b.WriteByte(rendered[i+1])
				i++
			} else if c == '\'' {
				inStr = false
			}
		case inIdent:
			b.WriteByte(c)
			if c == '`' {
				inIdent = false
			}
		case c == '\'':
			inStr = true
			b.WriteByte(c)
		case c == '`':
			inIdent = true
			b.WriteByte(c)
		case c == '$':
			j := i + 1
			for j < n && rendered[j] >= '0' && rendered[j] <= '9' {
				j++
			}
			if j == i+1 {
				b.WriteByte(c) // bare $ (not a placeholder)
				continue
			}
			idx, err := strconv.Atoi(rendered[i+1 : j])
			if err != nil || idx < 1 || idx > len(vals) {
				return "", nil, false
			}
			b.WriteByte('?')
			out = append(out, vals[idx-1])
			i = j - 1
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), out, true
}

// ---- WHERE injection (tenant / data scope) ----

// injectWherePredicates walks stmt and, for every SELECT/UPDATE/DELETE node, AND-merges
// the predicates that pred returns for that node's table references. Recursion is
// automatic (ast.Walk descends into subqueries / UNION branches / CTEs via Children),
// so every access to a controlled table is filtered. Returns true if any node changed.
func injectWherePredicates(stmt ast.Statement, pred func(refs []tableRef) []ast.Expression) bool {
	w := &whereVisitor{pred: pred}
	_ = ast.Walk(w, stmt)
	return w.changed
}

type whereVisitor struct {
	pred    func([]tableRef) []ast.Expression
	changed bool
}

func (w *whereVisitor) Visit(n ast.Node) (ast.Visitor, error) {
	if n == nil {
		return nil, nil
	}
	var refs []tableRef
	var where ast.Expression
	switch s := n.(type) {
	case *ast.SelectStatement:
		refs, where = selectTableRefs(s), s.Where
		preds := w.pred(refs)
		if len(preds) > 0 {
			s.Where = andMergeExpr(where, preds)
			w.changed = true
		}
	case *ast.UpdateStatement:
		refs, where = updateTableRefs(s), s.Where
		preds := w.pred(refs)
		if len(preds) > 0 {
			s.Where = andMergeExpr(where, preds)
			w.changed = true
		}
	case *ast.DeleteStatement:
		refs, where = deleteTableRefs(s), s.Where
		preds := w.pred(refs)
		if len(preds) > 0 {
			s.Where = andMergeExpr(where, preds)
			w.changed = true
		}
	}
	return w, nil
}

// andMergeExpr folds preds onto existing: when existing is nil the predicate tree is
// returned directly, otherwise it is ANDed on.
func andMergeExpr(existing ast.Expression, preds []ast.Expression) ast.Expression {
	expr := andExprs(preds)
	if existing == nil {
		return expr
	}
	return &ast.BinaryExpression{Left: existing, Operator: "AND", Right: expr}
}

// andExprs folds a non-empty predicate list into a left-leaning AND tree.
func andExprs(preds []ast.Expression) ast.Expression {
	var expr ast.Expression
	for _, pr := range preds {
		if expr == nil {
			expr = pr
		} else {
			expr = &ast.BinaryExpression{Left: expr, Operator: "AND", Right: pr}
		}
	}
	return expr
}

// qualifiedCol builds <alias>.col or bare col as an Identifier.
func qualifiedCol(alias, col string) *ast.Identifier {
	return &ast.Identifier{Name: col, Table: alias}
}

// bindVal builds a placeholder value node carrying the given $N name.
func bindVal(name string) *ast.LiteralValue {
	return &ast.LiteralValue{Value: name, Type: "placeholder"}
}

// eqCol builds <alias>.col = <bindName>.
func eqCol(alias, col, bindName string) ast.Expression {
	return &ast.BinaryExpression{Left: qualifiedCol(alias, col), Operator: "=", Right: bindVal(bindName)}
}

// tableRef is a concrete (alias, table) reference discovered in a FROM/JOIN/USING clause.
type tableRef struct{ alias, table string }

// qualifier picks the column qualifier for a reference: the alias when present; the table
// name when the clause names more than one table (to disambiguate); bare otherwise.
func (r tableRef) qualifier(total int) string {
	if r.alias != "" {
		return r.alias
	}
	if total > 1 {
		return r.table
	}
	return ""
}

// selectTableRefs collects concrete table references from a SELECT's FROM and JOINs.
// GoSQLX places the leftmost table in From and repeats it as Joins[0].Left, so only the
// Right side of each join is added (the left side is already in From) to avoid counting
// the base table twice.
func selectTableRefs(s *ast.SelectStatement) []tableRef {
	var refs []tableRef
	for i := range s.From {
		refs = append(refs, tableRefOf(s.From[i]))
	}
	if len(refs) == 0 && len(s.Joins) > 0 {
		// No FROM table recorded: the first join's left is the base table.
		refs = append(refs, tableRefOf(s.Joins[0].Left))
	}
	for i := range s.Joins {
		refs = append(refs, tableRefOf(s.Joins[i].Right))
	}
	return refs
}

func updateTableRefs(s *ast.UpdateStatement) []tableRef {
	var refs []tableRef
	if s.TableName != "" {
		refs = append(refs, tableRef{alias: s.Alias, table: s.TableName})
	}
	for i := range s.From {
		refs = append(refs, tableRefOf(s.From[i]))
	}
	return refs
}

func deleteTableRefs(s *ast.DeleteStatement) []tableRef {
	var refs []tableRef
	if s.TableName != "" {
		refs = append(refs, tableRef{alias: s.Alias, table: s.TableName})
	}
	for i := range s.Using {
		refs = append(refs, tableRefOf(s.Using[i]))
	}
	return refs
}

// tableRefOf maps a TableReference to a tableRef, skipping subqueries (their inner tables
// are visited separately as the subquery's own SELECT node by Walk).
func tableRefOf(tr ast.TableReference) tableRef {
	if tr.Subquery != nil || tr.Name == "" {
		return tableRef{}
	}
	return tableRef{alias: tr.Alias, table: tr.Name}
}

// containsColumnWord reports whether sql references col as a word (case-insensitive,
// word-delimited), used by the hook to skip double-injection when a db.Sql template
// already filters on the column (e.g. #and(tenant_id, ...)). Coarse and conservative — a
// hit only skips injection, never injects.
func containsColumnWord(sql, col string) bool {
	if col == "" {
		return false
	}
	needle := strings.ToLower(col)
	s := strings.ToLower(sql)
	idx := 0
	for {
		j := strings.Index(s[idx:], needle)
		if j < 0 {
			return false
		}
		pos := idx + j
		before := pos == 0 || !isIdentByte(s[pos-1])
		after := pos+len(needle) == len(s) || !isIdentByte(s[pos+len(needle)])
		if before && after {
			return true
		}
		idx = pos + len(needle)
	}
}

func isIdentByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}
