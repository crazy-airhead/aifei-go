package sql

import (
	"fmt"
	"reflect"

	"github.com/crazy-airhead/aifei-go/enjoy"
)

// ParaDirective implements #para(name) and #p(name) for parameter placeholders.
type ParaDirective struct {
	index    int
	paraName string
	exprList *enjoy.ExprList

	// like/in support
	paraType int
}

const (
	paraTypeNormal    = 0
	paraTypeLike      = 1
	paraTypeLikeLeft  = 2
	paraTypeLikeRight = 3
	paraTypeIn        = 4
)

func (d *ParaDirective) SetExprList(exprList *enjoy.ExprList) {
	d.exprList = exprList

	if exprList.Length() == 0 {
		panic("The parameter of #para directive can not be blank")
	}

	d.index = -1

	expr := exprList.GetExpr(0)
	if c, ok := expr.(*enjoy.ConstExpr); ok && c.Type == "int" {
		d.index = int(reflect.ValueOf(c.Value).Int())
		if d.index < 0 {
			panic("The index of para array must be greater than -1")
		}
	}

	if exprList.Length() > 1 {
		expr = exprList.GetExpr(1)
		if c, ok := expr.(*enjoy.ConstExpr); ok && c.Type == "string" {
			typeStr := c.Value.(string)
			switch typeStr {
			case "like", "%like%":
				d.paraType = paraTypeLike
			case "%like":
				d.paraType = paraTypeLikeLeft
			case "like%":
				d.paraType = paraTypeLikeRight
			case "in":
				d.paraType = paraTypeIn
			default:
				panic(fmt.Sprintf("The type of para must be: like, %%like, like%%, in. Not support: %s", typeStr))
			}
		}
	}

	if d.index == -1 {
		if id, ok := exprList.GetExpr(0).(*enjoy.IDExpr); ok {
			d.paraName = id.Name
		}
	}
}

func (d *ParaDirective) SetStat(stat enjoy.Stat) {}

func (d *ParaDirective) Exec(env *enjoy.Env, scope *enjoy.Scope, writer *enjoy.IOAdapter, ctrl *enjoy.Ctrl) {
	sqlPara, ok := scope.Get(SqlParaKey).(*SqlPara)
	if !ok || sqlPara == nil {
		panic("#para directive invoked by sql(...)、sqlById(...) method only")
	}

	sqlPara.SetEnjoySql(true)

	if d.index == -1 {
		if d.paraName != "" && !scope.Exists(d.paraName) {
			panic(fmt.Sprintf("The parameter %q must be assigned", d.paraName))
		}
		d.handleSqlPara(writer, sqlPara, d.exprList.GetExpr(0).Eval(scope, ctrl))
	} else {
		paras, ok := scope.Get(ParaArrayKey).([]interface{})
		if !ok || paras == nil {
			panic(fmt.Sprintf("The #para(%d) directive must invoked by sql(String, Object...) or sqlById(String, Object...) method", d.index))
		}
		if d.index >= len(paras) {
			panic(fmt.Sprintf("The index of #para directive is out of bounds: %d", d.index))
		}
		d.handleSqlPara(writer, sqlPara, paras[d.index])
	}
}

func (d *ParaDirective) HasEnd() bool { return false }

func (d *ParaDirective) handleSqlPara(writer *enjoy.IOAdapter, sqlPara *SqlPara, value interface{}) {
	switch d.paraType {
	case paraTypeNormal:
		writer.WriteString("?")
		sqlPara.AddPara(value)
	case paraTypeLike:
		writer.WriteString("?")
		sqlPara.AddPara(fmt.Sprintf("%%%v%%", value))
	case paraTypeLikeLeft:
		writer.WriteString("?")
		sqlPara.AddPara(fmt.Sprintf("%%%v", value))
	case paraTypeLikeRight:
		writer.WriteString("?")
		sqlPara.AddPara(fmt.Sprintf("%v%%", value))
	case paraTypeIn:
		d.handleIn(writer, sqlPara, value)
	}
}

func (d *ParaDirective) handleIn(writer *enjoy.IOAdapter, sqlPara *SqlPara, value interface{}) {
	rv := reflect.ValueOf(value)
	if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
		writer.WriteString("(")
		for i := 0; i < rv.Len(); i++ {
			if i == 0 {
				writer.WriteString("?")
			} else {
				writer.WriteString(", ?")
			}
			sqlPara.AddPara(rv.Index(i).Interface())
		}
		writer.WriteString(")")
	} else {
		writer.WriteString("(?)")
		sqlPara.AddPara(value)
	}
}
