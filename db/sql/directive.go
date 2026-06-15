package sql

import (
	"fmt"

	"github.com/crazy-airhead/aifei-go/enjoy"
)

// SqlDirective implements #sql(id) ... #end for named SQL templates.
type SqlDirective struct {
	id   string
	body enjoy.Stat
}

func (d *SqlDirective) SetExprList(exprList *enjoy.ExprList) {
	if exprList.Length() == 0 {
		panic("The parameter of #sql directive can not be blank")
	}
	if exprList.Length() > 1 {
		panic("Only one parameter allowed for #sql directive")
	}
	expr := exprList.GetExpr(0)
	c, ok := expr.(*enjoy.ConstExpr)
	if !ok || c.Type != "string" {
		panic("The parameter of #sql directive must be String")
	}
	d.id = c.Value.(string)
}

func (d *SqlDirective) SetStat(stat enjoy.Stat) {
	d.body = stat
}

func (d *SqlDirective) Exec(env *enjoy.Env, scope *enjoy.Scope, writer *enjoy.IOAdapter, ctrl *enjoy.Ctrl) {
	sqlCache, ok := scope.Get(SqlCacheKey).(map[string]*enjoy.Template)
	if !ok {
		panic(fmt.Sprintf("SqlCache not found in scope for #sql(%s)", d.id))
	}
	if _, exists := sqlCache[d.id]; exists {
		panic(fmt.Sprintf("Sql already exists with id: %s", d.id))
	}
	sqlCache[d.id] = enjoy.NewTemplate(env, d.body)
}

func (d *SqlDirective) HasEnd() bool { return true }
