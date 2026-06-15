package sql

import (
	"fmt"
	"reflect"

	"github.com/crazy-airhead/aifei-go/enjoy"
)

const defaultOrderByPara = "orderBy"

var orderWhitelist = map[string]string{
	"asc": "ASC", "ASC": "ASC",
	"desc": "DESC", "DESC": "DESC",
}

// OrderByDirective implements #orderBy(field1, field2, ...).
type OrderByDirective struct {
	paraName      string
	fieldWhiteMap map[string]string
}

func (d *OrderByDirective) SetExprList(exprList *enjoy.ExprList) {
	length := exprList.Length()
	if length == 0 {
		panic("#orderBy() requires at least 1 argument")
	}

	d.paraName = defaultOrderByPara
	d.fieldWhiteMap = make(map[string]string)

	for i := 0; i < length; i++ {
		var str string
		expr := exprList.GetExpr(i)
		switch e := expr.(type) {
		case *enjoy.IDExpr:
			str = e.Name
		case *enjoy.ConstExpr:
			if e.Type == "string" {
				str = e.Value.(string)
			} else {
				panic("#orderBy() arguments must be identifiers or string literals")
			}
		default:
			panic("#orderBy() arguments must be identifiers or string literals")
		}

		// Custom parameter name with '$' prefix
		if len(str) > 0 && str[0] == '$' {
			if i != 0 {
				panic(fmt.Sprintf("#orderBy() parameter starting with '$' must be the first argument, but found at position: %d", i+1))
			}
			d.paraName = str[1:]
			if d.paraName == "" {
				panic("#orderBy() parameter name after '$' must not be empty")
			}
			continue
		}

		// Check for sqlField:clientField mapping
		idx := -1
		for j, c := range str {
			if c == ':' {
				idx = j
				break
			}
		}
		if idx == -1 {
			d.fieldWhiteMap[str] = str
		} else {
			sqlField := str[:idx]
			clientField := str[idx+1:]
			if sqlField == "" || clientField == "" {
				panic(fmt.Sprintf("#orderBy() invalid whitelist format, expected sqlField:clientField: %s", str))
			}
			d.fieldWhiteMap[clientField] = sqlField
		}
	}

	if len(d.fieldWhiteMap) == 0 {
		panic("#orderBy() requires at least one sortable field in whitelist")
	}
}

func (d *OrderByDirective) SetStat(stat enjoy.Stat) {}

func (d *OrderByDirective) Exec(env *enjoy.Env, scope *enjoy.Scope, writer *enjoy.IOAdapter, ctrl *enjoy.Ctrl) {
	sqlPara, ok := scope.Get(SqlParaKey).(*SqlPara)
	if !ok || sqlPara == nil {
		panic("#orderBy() must be used with sql(String, Map) or sqlById(String, Map)")
	}

	orderBy := scope.Get(d.paraName)
	if orderBy == nil {
		return
	}

	// Single-field: map
	if m, ok := orderBy.(map[string]interface{}); ok {
		if len(m) > 0 {
			d.generateOrderByItem(m, writer, true)
		}
		return
	}

	// Multi-field: slice or array
	list := d.toOrderByItemList(orderBy)
	if len(list) > 0 {
		for i, item := range list {
			d.generateOrderByItem(item, writer, i == 0)
		}
	}
}

func (d *OrderByDirective) HasEnd() bool { return false }

func (d *OrderByDirective) toOrderByItemList(orderBy interface{}) []map[string]interface{} {
	rv := reflect.ValueOf(orderBy)

	if rv.Kind() == reflect.Slice {
		if rv.Len() == 0 {
			return nil
		}
		ret := make([]map[string]interface{}, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			item := rv.Index(i)
			if !item.IsNil() {
				if m, ok := item.Interface().(map[string]interface{}); ok {
					ret = append(ret, m)
				}
			}
		}
		return ret
	}

	if rv.Kind() == reflect.Array {
		if rv.Len() == 0 {
			return nil
		}
		ret := make([]map[string]interface{}, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			item := rv.Index(i)
			if !item.IsNil() {
				if m, ok := item.Interface().(map[string]interface{}); ok {
					ret = append(ret, m)
				}
			}
		}
		return ret
	}

	return nil
}

func (d *OrderByDirective) generateOrderByItem(item map[string]interface{}, writer *enjoy.IOAdapter, first bool) {
	fieldFromClient, ok := item["field"]
	if !ok || fieldFromClient == nil {
		panic("orderBy field must not be null")
	}
	orderFromClient, ok := item["order"]
	if !ok || orderFromClient == nil {
		panic("orderBy order must not be null")
	}

	// Whitelist check
	clientField := fmt.Sprintf("%v", fieldFromClient)
	field, ok := d.fieldWhiteMap[clientField]
	if !ok {
		panic(fmt.Sprintf("orderBy field not in whitelist: %s", clientField))
	}
	order, ok := orderWhitelist[fmt.Sprintf("%v", orderFromClient)]
	if !ok {
		panic(fmt.Sprintf("orderBy order must be asc or desc, but got: %v", orderFromClient))
	}

	if first {
		writer.WriteString("ORDER BY ")
	} else {
		writer.WriteString(", ")
	}
	writer.WriteString(field)
	writer.WriteString(" ")
	writer.WriteString(order)
}
