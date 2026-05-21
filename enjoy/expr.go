package enjoy

// Expr is the interface for expression AST nodes.
type Expr interface {
	Eval(scope *Scope, ctrl *Ctrl) interface{}
}
