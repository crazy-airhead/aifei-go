package sql

import (
	"fmt"
	"reflect"

	"github.com/crazy-airhead/aifei-go/enjoy"
)

// FirstConditionKey is the scope key for tracking first condition generation.
const FirstConditionKey = "_FIRST_CONDITION_"

// SqlCondition encapsulates #where / #and directive parameter parsing and generation.
type SqlCondition struct {
	field    string
	operator *SqlOperator
	para     enjoy.Expr
}

// NewSqlCondition parses the directive parameters.
func NewSqlCondition(exprList *enjoy.ExprList, directive string) (*SqlCondition, error) {
	length := exprList.Length()
	if length < 2 || length > 3 {
		return nil, fmt.Errorf("%s requires 2 to 3 arguments, got %d", directive, length)
	}

	// Parse field (identifier or string literal)
	var field string
	switch e := exprList.GetExpr(0).(type) {
	case *enjoy.IDExpr:
		field = e.Name
	case *enjoy.ConstExpr:
		if e.Type == "string" {
			field = e.Value.(string)
		} else {
			return nil, fmt.Errorf("%s first argument must be an identifier or string literal", directive)
		}
	default:
		return nil, fmt.Errorf("%s first argument must be an identifier or string literal", directive)
	}

	// Parse operator (must be string const)
	opExpr, ok := exprList.GetExpr(1).(*enjoy.ConstExpr)
	if !ok || opExpr.Type != "string" {
		return nil, fmt.Errorf("%s second argument must be a string literal", directive)
	}
	operator := SqlOperatorFrom(opExpr.Value.(string))
	if operator == nil {
		return nil, fmt.Errorf("%s invalid operator: %s", directive, opExpr.Value.(string))
	}

	paraCount := length - 2

	// Zero-para operators: IS NULL, IS NOT NULL
	if operator.IsNilOp() {
		if paraCount != 0 {
			return nil, fmt.Errorf("%s requires 0 arguments, got %d", operator.SQL(), paraCount)
		}
		return &SqlCondition{field: field, operator: operator}, nil
	}

	// Non-zero-para operators
	if paraCount != 1 {
		return nil, fmt.Errorf("%s requires 1 argument, got %d", operator.SQL(), paraCount)
	}
	return &SqlCondition{field: field, operator: operator, para: exprList.GetExpr(2)}, nil
}

// Generate evaluates the condition and writes SQL to the writer.
func (c *SqlCondition) Generate(scope *enjoy.Scope, writer *enjoy.IOAdapter, firstCondition *bool, sqlPara *SqlPara) error {
	// IS NULL / IS NOT NULL: generate if field exists in scope
	if c.operator.IsNilOp() {
		if scope.Exists(c.field) {
			c.writeConditionHead(writer, firstCondition)
		}
		return nil
	}

	// For all other operators, skip if value is nil or empty string
	value := c.para.Eval(scope, nil)
	if value == nil {
		return nil
	}
	if s, ok := value.(string); ok && s == "" {
		return nil
	}

	switch {
	case c.operator.IsInOp():
		return c.generateInOrNotIn(writer, firstCondition, sqlPara, value)
	case c.operator.IsBetweenOp():
		return c.generateBetweenOrNotBetween(writer, firstCondition, sqlPara, value)
	case c.operator.LikeOp():
		c.writeConditionHead(writer, firstCondition)
		writer.WriteString(" ?")
		sqlPara.AddPara(c.operator.ToLikeValue(value))
	default:
		c.writeConditionHead(writer, firstCondition)
		writer.WriteString(" ?")
		sqlPara.AddPara(value)
	}
	return nil
}

func (c *SqlCondition) writeConditionHead(writer *enjoy.IOAdapter, firstCondition *bool) {
	if *firstCondition {
		*firstCondition = false
		writer.WriteString("WHERE ")
	} else {
		writer.WriteString("AND ")
	}
	writer.WriteString(c.field)
	writer.WriteString(" ")
	writer.WriteString(c.operator.SQL())
}

func (c *SqlCondition) generateInOrNotIn(writer *enjoy.IOAdapter, firstCondition *bool, sqlPara *SqlPara, value interface{}) error {
	list := c.toValueList(value, false, true)
	if len(list) == 0 {
		return nil
	}
	c.writeConditionHead(writer, firstCondition)
	writer.WriteString(" (")
	for i, item := range list {
		if i > 0 {
			writer.WriteString(", ?")
		} else {
			writer.WriteString("?")
		}
		sqlPara.AddPara(item)
	}
	writer.WriteString(")")
	return nil
}

func (c *SqlCondition) generateBetweenOrNotBetween(writer *enjoy.IOAdapter, firstCondition *bool, sqlPara *SqlPara, value interface{}) error {
	list := c.toValueList(value, true, false)
	if len(list) != 2 {
		return fmt.Errorf("%s requires exactly 2 arguments", c.operator.SQL())
	}
	start := list[0]
	end := list[1]
	if start == nil || end == nil {
		return nil
	}
	c.writeConditionHead(writer, firstCondition)
	writer.WriteString(" ? AND ?")
	sqlPara.AddPara(start)
	sqlPara.AddPara(end)
	return nil
}

func (c *SqlCondition) toValueList(value interface{}, allowNull bool, allowSingleValue bool) []interface{} {
	rv := reflect.ValueOf(value)

	// Handle slice
	if rv.Kind() == reflect.Slice {
		if rv.Len() == 0 {
			return nil
		}
		var result []interface{}
		for i := 0; i < rv.Len(); i++ {
			item := rv.Index(i).Interface()
			if item != nil {
				result = append(result, item)
			} else if allowNull {
				result = append(result, nil)
			}
		}
		return result
	}

	// Handle array
	if rv.Kind() == reflect.Array {
		if rv.Len() == 0 {
			return nil
		}
		var result []interface{}
		for i := 0; i < rv.Len(); i++ {
			item := rv.Index(i).Interface()
			if item != nil {
				result = append(result, item)
			} else if allowNull {
				result = append(result, nil)
			}
		}
		return result
	}

	// Single value
	if allowSingleValue {
		return []interface{}{value}
	}

	return nil
}
