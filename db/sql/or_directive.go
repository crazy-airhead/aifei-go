package sql

import (
	"github.com/crazy-airhead/aifei-go/enjoy"
)

// OrDirective implements #or(field, operator, para).
type OrDirective struct {
	condition *SqlCondition
}

func (d *OrDirective) SetExprList(exprList *enjoy.ExprList) {
	var err error
	d.condition, err = NewSqlConditionWithConjunction(exprList, "#or", "OR")
	if err != nil {
		panic(err.Error())
	}
}

func (d *OrDirective) SetStat(stat enjoy.Stat) {}

func (d *OrDirective) Exec(env *enjoy.Env, scope *enjoy.Scope, writer *enjoy.IOAdapter, ctrl *enjoy.Ctrl) {
	sqlPara, ok := scope.Get(SqlParaKey).(*SqlPara)
	if !ok || sqlPara == nil {
		panic("#or must be used with sql(String, Map) or sqlById(String, Map)")
	}

	firstCondition := firstConditionFromScope(scope)
	d.condition.Generate(scope, writer, firstCondition, sqlPara)
}

func (d *OrDirective) HasEnd() bool { return false }
