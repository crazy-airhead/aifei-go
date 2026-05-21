package enjoy

// DirectiveFactory creates a new Directive instance.
type DirectiveFactory func() Directive

// Directive is the interface for custom template directives.
type Directive interface {
	SetExprList(exprList *ExprList)
	SetStat(stat Stat)
	Exec(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl)
	HasEnd() bool
}
