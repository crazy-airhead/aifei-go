package enjoy

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// ---- Expression AST nodes ----

// IDExpr is a variable identifier.
type IDExpr struct {
	Name string
}

func (e *IDExpr) Eval(scope *Scope, ctrl *Ctrl) interface{} {
	return scope.Get(e.Name)
}

// ConstExpr is a constant literal.
type ConstExpr struct {
	Type  string
	Value interface{}
}

func (e *ConstExpr) Eval(scope *Scope, ctrl *Ctrl) interface{} {
	return e.Value
}

// ArithExpr is an arithmetic expression.
type ArithExpr struct {
	Op    string
	Left  Expr
	Right Expr
}

func (e *ArithExpr) Eval(scope *Scope, ctrl *Ctrl) interface{} {
	if e.Op == "neg" {
		return -toFloat64(e.Left.Eval(scope, ctrl))
	}
	l := toFloat64(e.Left.Eval(scope, ctrl))
	r := toFloat64(e.Right.Eval(scope, ctrl))
	switch e.Op {
	case "+":
		return l + r
	case "-":
		return l - r
	case "*":
		return l * r
	case "/":
		if r == 0 {
			return float64(0)
		}
		return l / r
	case "%":
		if r == 0 {
			return float64(0)
		}
		return math.Mod(l, r)
	}
	return float64(0)
}

// CompareExpr is a comparison expression.
type CompareExpr struct {
	Op    string
	Left  Expr
	Right Expr
}

func (e *CompareExpr) Eval(scope *Scope, ctrl *Ctrl) interface{} {
	l := e.Left.Eval(scope, ctrl)
	r := e.Right.Eval(scope, ctrl)
	switch e.Op {
	case "==":
		return valEquals(l, r)
	case "!=":
		return !valEquals(l, r)
	case "<":
		return toFloat64(l) < toFloat64(r)
	case "<=":
		return toFloat64(l) <= toFloat64(r)
	case ">":
		return toFloat64(l) > toFloat64(r)
	case ">=":
		return toFloat64(l) >= toFloat64(r)
	}
	return false
}

// LogicExpr is a logical expression.
type LogicExpr struct {
	Op    string
	Left  Expr
	Right Expr
}

func (e *LogicExpr) Eval(scope *Scope, ctrl *Ctrl) interface{} {
	switch e.Op {
	case "&&":
		return isTruthy(e.Left.Eval(scope, ctrl)) && isTruthy(e.Right.Eval(scope, ctrl))
	case "||":
		return isTruthy(e.Left.Eval(scope, ctrl)) || isTruthy(e.Right.Eval(scope, ctrl))
	case "!":
		return !isTruthy(e.Left.Eval(scope, ctrl))
	}
	return false
}

// TernaryExpr is a ternary conditional.
type TernaryExpr struct {
	Cond Expr
	Then Expr
	Else Expr
}

func (e *TernaryExpr) Eval(scope *Scope, ctrl *Ctrl) interface{} {
	if isTruthy(e.Cond.Eval(scope, ctrl)) {
		return e.Then.Eval(scope, ctrl)
	}
	return e.Else.Eval(scope, ctrl)
}

// NullCoalesceExpr is a ?? b.
type NullCoalesceExpr struct {
	Left  Expr
	Right Expr
}

func (e *NullCoalesceExpr) Eval(scope *Scope, ctrl *Ctrl) interface{} {
	v := e.Left.Eval(scope, ctrl)
	if v == nil {
		return e.Right.Eval(scope, ctrl)
	}
	return v
}

// NullSafeExpr is a ?.b.
type NullSafeExpr struct {
	Inner Expr
}

func (e *NullSafeExpr) Eval(scope *Scope, ctrl *Ctrl) interface{} {
	ctrl.NullSafe = true
	defer func() { ctrl.NullSafe = false }()
	return e.Inner.Eval(scope, ctrl)
}

// FieldExpr is obj.field.
type FieldExpr struct {
	Obj  Expr
	Name string
}

func (e *FieldExpr) Eval(scope *Scope, ctrl *Ctrl) interface{} {
	obj := e.Obj.Eval(scope, ctrl)
	if obj == nil {
		return nil
	}
	return getField(obj, e.Name)
}

// MethodExpr is obj.method(args).
type MethodExpr struct {
	Obj  Expr
	Name string
	Args []Expr
}

func (e *MethodExpr) Eval(scope *Scope, ctrl *Ctrl) interface{} {
	args := make([]interface{}, len(e.Args))
	for i, a := range e.Args {
		args[i] = a.Eval(scope, ctrl)
	}

	if e.Obj == nil {
		fn := scope.Get(e.Name)
		if fn == nil {
			return nil
		}
		return callFunc(fn, args)
	}

	obj := e.Obj.Eval(scope, ctrl)
	if obj == nil {
		return nil
	}

	if m, ok := obj.(map[string]interface{}); ok {
		if fn, exists := m[e.Name]; exists {
			return callFunc(fn, args)
		}
	}

	if s, ok := obj.(string); ok {
		return stringMethod(s, e.Name, args)
	}

	v := reflect.ValueOf(obj)
	method := v.MethodByName(e.Name)
	if !method.IsValid() {
		return nil
	}
	return callReflect(method, args)
}

// IndexExpr is arr[i].
type IndexExpr struct {
	Obj   Expr
	Index Expr
}

func (e *IndexExpr) Eval(scope *Scope, ctrl *Ctrl) interface{} {
	obj := e.Obj.Eval(scope, ctrl)
	idx := e.Index.Eval(scope, ctrl)
	if obj == nil {
		return nil
	}
	v := reflect.ValueOf(obj)
	switch v.Kind() {
	case reflect.Map:
		key := reflect.ValueOf(idx)
		val := v.MapIndex(key)
		if !val.IsValid() {
			return nil
		}
		return val.Interface()
	case reflect.Slice, reflect.Array, reflect.String:
		i := toInt(idx)
		if i < 0 || i >= v.Len() {
			return nil
		}
		return v.Index(i).Interface()
	}
	return nil
}

// AssignExpr is name = value.
type AssignExpr struct {
	Name  string
	Value Expr
}

func (e *AssignExpr) Eval(scope *Scope, ctrl *Ctrl) interface{} {
	v := e.Value.Eval(scope, ctrl)
	scope.Set(e.Name, v)
	return v
}

// IncDecExpr is ++/--.
type IncDecExpr struct {
	Name string
	Op   string
}

func (e *IncDecExpr) Eval(scope *Scope, ctrl *Ctrl) interface{} {
	n := toInt64(scope.Get(e.Name))
	if e.Op == "++" {
		n++
	} else {
		n--
	}
	scope.Set(e.Name, n)
	return n
}

// ArrayExpr is [1, 2, 3].
type ArrayExpr struct {
	Elements []Expr
}

func (e *ArrayExpr) Eval(scope *Scope, ctrl *Ctrl) interface{} {
	arr := make([]interface{}, len(e.Elements))
	for i, el := range e.Elements {
		arr[i] = el.Eval(scope, ctrl)
	}
	return arr
}

// MapPair is a key-value pair.
type MapPair struct {
	Key   Expr
	Value Expr
}

// MapExpr is {"k": "v"}.
type MapExpr struct {
	Pairs []MapPair
}

func (e *MapExpr) Eval(scope *Scope, ctrl *Ctrl) interface{} {
	m := make(map[string]interface{})
	for _, p := range e.Pairs {
		key := fmt.Sprintf("%v", p.Key.Eval(scope, ctrl))
		m[key] = p.Value.Eval(scope, ctrl)
	}
	return m
}

// RangeExpr is [0..10].
type RangeExpr struct {
	Start Expr
	End   Expr
}

func (e *RangeExpr) Eval(scope *Scope, ctrl *Ctrl) interface{} {
	start := toInt(e.Start.Eval(scope, ctrl))
	end := toInt(e.End.Eval(scope, ctrl))
	arr := make([]interface{}, 0)
	if start <= end {
		for i := start; i <= end; i++ {
			arr = append(arr, i)
		}
	} else {
		for i := start; i >= end; i-- {
			arr = append(arr, i)
		}
	}
	return arr
}

// ---- helpers ----

func getField(obj interface{}, name string) interface{} {
	v := reflect.ValueOf(obj)
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() == reflect.Map {
		key := reflect.ValueOf(name)
		val := v.MapIndex(key)
		if !val.IsValid() {
			return nil
		}
		return val.Interface()
	}
	if v.Kind() == reflect.Struct {
		f := v.FieldByName(name)
		if !f.IsValid() {
			return nil
		}
		return f.Interface()
	}
	return nil
}

func callFunc(fn interface{}, args []interface{}) interface{} {
	v := reflect.ValueOf(fn)
	if v.Kind() != reflect.Func {
		return nil
	}
	return callReflect(v, args)
}

func callReflect(method reflect.Value, args []interface{}) interface{} {
	in := make([]reflect.Value, len(args))
	for i, arg := range args {
		if arg == nil {
			in[i] = reflect.Zero(reflect.TypeOf((*interface{})(nil)).Elem())
		} else {
			in[i] = reflect.ValueOf(arg)
		}
	}
	result := method.Call(in)
	if len(result) == 0 {
		return nil
	}
	return result[0].Interface()
}

func stringMethod(s string, name string, args []interface{}) interface{} {
	switch name {
	case "length", "len", "size":
		return len(s)
	case "trim":
		return strings.TrimSpace(s)
	case "upper", "toUpperCase":
		return strings.ToUpper(s)
	case "lower", "toLowerCase":
		return strings.ToLower(s)
	case "contains":
		if len(args) > 0 {
			return strings.Contains(s, fmt.Sprintf("%v", args[0]))
		}
	case "startsWith":
		if len(args) > 0 {
			return strings.HasPrefix(s, fmt.Sprintf("%v", args[0]))
		}
	case "endsWith":
		if len(args) > 0 {
			return strings.HasSuffix(s, fmt.Sprintf("%v", args[0]))
		}
	case "indexOf":
		if len(args) > 0 {
			return strings.Index(s, fmt.Sprintf("%v", args[0]))
		}
	case "substring", "sub":
		if len(args) >= 2 {
			return s[toInt(args[0]):toInt(args[1])]
		}
		if len(args) == 1 {
			return s[toInt(args[0]):]
		}
	case "replace":
		if len(args) >= 2 {
			return strings.ReplaceAll(s, fmt.Sprintf("%v", args[0]), fmt.Sprintf("%v", args[1]))
		}
	case "split":
		if len(args) > 0 {
			parts := strings.Split(s, fmt.Sprintf("%v", args[0]))
			result := make([]interface{}, len(parts))
			for i, p := range parts {
				result[i] = p
			}
			return result
		}
	case "isEmpty":
		return s == ""
	}
	return nil
}

func toFloat64(v interface{}) float64 {
	if v == nil {
		return 0
	}
	switch n := v.(type) {
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case float64:
		return n
	case float32:
		return float64(n)
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return f
	case bool:
		if n {
			return 1
		}
		return 0
	case time.Time:
		return float64(n.Unix())
	}
	return 0
}

func toInt64(v interface{}) int64 {
	if v == nil {
		return 0
	}
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	case float32:
		return int64(n)
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	case bool:
		if n {
			return 1
		}
		return 0
	}
	return 0
}

func toInt(v interface{}) int { return int(toInt64(v)) }

func isTruthy(v interface{}) bool {
	if v == nil {
		return false
	}
	switch val := v.(type) {
	case bool:
		return val
	case int:
		return val != 0
	case int64:
		return val != 0
	case float64:
		return val != 0
	case string:
		return val != "" && val != "false" && val != "0"
	}
	return true
}

func valEquals(a, b interface{}) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

func toSlice(v interface{}) []interface{} {
	if v == nil {
		return nil
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return []interface{}{v}
	}
	result := make([]interface{}, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		result[i] = rv.Index(i).Interface()
	}
	return result
}
