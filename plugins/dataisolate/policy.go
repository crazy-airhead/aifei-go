package dataisolate

import (
	"strconv"

	"github.com/ajitpratap0/GoSQLX/pkg/sql/ast"
)

// Policy rewrites one facet of a parsed statement. Apply mutates stmt in place and
// registers any new parameters via pc (in traversal order), returning changed=true when
// it modified the statement. A policy that finds a controlled table/column but cannot
// safely rewrite must call pc.Fail(err) so the rewriter returns StatusFailed
// (fail-closed) instead of executing an unisolated statement.
type Policy interface {
	Name() string
	Apply(stmt ast.Statement, p *Principal, pc *ParamCollector) bool
}

// PolicyChain is an ordered list of policies. Convention: projection policies
// (FieldMask) run before WHERE-injection policies (Tenant, DataScope). Projection and
// WHERE are orthogonal; multiple WHERE predicates merge with AND.
type PolicyChain []Policy

// ParamCollector carries the placeholder value table (original args + injected values)
// and a fail signal. Bind appends a value and returns its $N placeholder name, continuing
// the numbering started by the rewriter's pre-scan (so original $1..$M and injected
// $M+1.. never collide). The rewriter's final render converts every $N back to "?" with
// these values, keeping placeholders and args aligned.
type ParamCollector struct {
	vals []interface{}
	fail error
}

func newParamCollector(vals []interface{}) *ParamCollector {
	return &ParamCollector{vals: vals}
}

// Bind registers a new injected value and returns its $N placeholder name to embed in the
// AST (use as &ast.LiteralValue{Value: name, Type: "placeholder"}).
func (pc *ParamCollector) Bind(v interface{}) string {
	pc.vals = append(pc.vals, v)
	return "$" + strconv.Itoa(len(pc.vals))
}

// Fail signals that a controlled item could not be safely rewritten; the rewriter aborts
// the statement (StatusFailed). Only the first error is kept.
func (pc *ParamCollector) Fail(err error) {
	if pc.fail == nil {
		pc.fail = err
	}
}

// Values returns the placeholder value table in $N order (original args then injected).
func (pc *ParamCollector) Values() []interface{} { return pc.vals }

// Failed returns the fail signal, if any.
func (pc *ParamCollector) Failed() error { return pc.fail }
