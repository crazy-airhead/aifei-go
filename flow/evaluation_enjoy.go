package flow

import (
	"strings"
	"sync"

	"github.com/crazy-airhead/aifei-go/enjoy"
)

// EnjoyEvaluation implements Evaluation using the enjoy expression engine (parse +
// eval), mirroring Solon-Flow's LiquorEvaluation (Snel for conditions, liquor
// Scripts for tasks). Parsed expressions are cached (enjoy ASTs evaluate purely).
type EnjoyEvaluation struct {
	cache sync.Map // code (string) -> enjoy.Expr
}

// NewEnjoyEvaluation creates an EnjoyEvaluation.
func NewEnjoyEvaluation() *EnjoyEvaluation { return &EnjoyEvaluation{} }

// RunCondition evaluates code, returning truthiness (nil → false, bool → as-is,
// else → true).
func (e *EnjoyEvaluation) RunCondition(ctx Context, code string) (bool, error) {
	expr, err := e.parse(code)
	if err != nil {
		return false, err
	}
	val := expr.Eval(enjoy.NewScope(ctx.Vars()), enjoy.NewCtrl())
	if val == nil {
		return false, nil
	}
	if b, ok := val.(bool); ok {
		return b, nil
	}
	return true, nil
}

// RunTask executes the task code, splitting on ';' so multiple statements are
// supported. Each statement is evaluated against the context vars (assignment
// statements mutate the shared vars map).
func (e *EnjoyEvaluation) RunTask(ctx Context, code string) error {
	for _, stmt := range strings.Split(code, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		expr, err := e.parse(stmt)
		if err != nil {
			return err
		}
		expr.Eval(enjoy.NewScope(ctx.Vars()), enjoy.NewCtrl())
	}
	return nil
}

func (e *EnjoyEvaluation) parse(code string) (enjoy.Expr, error) {
	if v, ok := e.cache.Load(code); ok {
		return v.(enjoy.Expr), nil
	}
	expr, err := enjoy.ParseExpr(code)
	if err != nil {
		return nil, err
	}
	e.cache.Store(code, expr)
	return expr, nil
}
