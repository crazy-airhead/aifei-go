package enjoy

// ExprList holds a list of expressions.
type ExprList struct {
	Exprs []Expr
}

// NewExprList creates an ExprList.
func NewExprList(exprs ...Expr) *ExprList {
	return &ExprList{Exprs: exprs}
}

// EvalAll evaluates all expressions and returns the results.
func (el *ExprList) EvalAll(scope *Scope, ctrl *Ctrl) []interface{} {
	if el == nil || len(el.Exprs) == 0 {
		return nil
	}
	result := make([]interface{}, len(el.Exprs))
	for i, e := range el.Exprs {
		result[i] = e.Eval(scope, ctrl)
	}
	return result
}

// First returns the first expression, or nil.
func (el *ExprList) First() Expr {
	if el == nil || len(el.Exprs) == 0 {
		return nil
	}
	return el.Exprs[0]
}

// Length returns the number of expressions.
func (el *ExprList) Length() int {
	if el == nil {
		return 0
	}
	return len(el.Exprs)
}

// GetExpr returns the expression at the given index.
func (el *ExprList) GetExpr(index int) Expr {
	if index >= len(el.Exprs) {
		return nil
	}
	return el.Exprs[index]
}
