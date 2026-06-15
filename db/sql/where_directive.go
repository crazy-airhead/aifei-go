package sql

import (
	"github.com/crazy-airhead/aifei-go/enjoy"
)

// WhereDirective implements #where(field, operator, para).
type WhereDirective struct {
	condition *SqlCondition
}

func (d *WhereDirective) SetExprList(exprList *enjoy.ExprList) {
	var err error
	d.condition, err = NewSqlCondition(exprList, "#where")
	if err != nil {
		panic(err.Error())
	}
}

func (d *WhereDirective) SetStat(stat enjoy.Stat) {}

func (d *WhereDirective) Exec(env *enjoy.Env, scope *enjoy.Scope, writer *enjoy.IOAdapter, ctrl *enjoy.Ctrl) {
	sqlPara, ok := scope.Get(SqlParaKey).(*SqlPara)
	if !ok || sqlPara == nil {
		panic("#where must be used with sql(String, Map) or sqlById(String, Map)")
	}

	firstCondition := true
	scope.Set(FirstConditionKey, &firstCondition)
	d.condition.Generate(scope, writer, &firstCondition, sqlPara)
}

func (d *WhereDirective) HasEnd() bool { return false }
