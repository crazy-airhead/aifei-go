package sql

import (
	"github.com/crazy-airhead/aifei-go/enjoy"
)

// AndDirective implements #and(field, operator, para).
type AndDirective struct {
	condition *SqlCondition
}

func (d *AndDirective) SetExprList(exprList *enjoy.ExprList) {
	var err error
	d.condition, err = NewSqlCondition(exprList, "#and")
	if err != nil {
		panic(err.Error())
	}
}

func (d *AndDirective) SetStat(stat enjoy.Stat) {}

func (d *AndDirective) Exec(env *enjoy.Env, scope *enjoy.Scope, writer *enjoy.IOAdapter, ctrl *enjoy.Ctrl) {
	sqlPara, ok := scope.Get(SqlParaKey).(*SqlPara)
	if !ok || sqlPara == nil {
		panic("#and must be used with sql(String, Map) or sqlById(String, Map)")
	}

	firstCondition := firstConditionFromScope(scope)
	d.condition.Generate(scope, writer, firstCondition, sqlPara)
}

func (d *AndDirective) HasEnd() bool { return false }

func firstConditionFromScope(scope *enjoy.Scope) *bool {
	v := scope.Get(FirstConditionKey)
	if p, ok := v.(*bool); ok {
		return p
	}
	// Support using #and after a literal WHERE clause (no #where directive)
	fc := false
	scope.Set(FirstConditionKey, &fc)
	return &fc
}
