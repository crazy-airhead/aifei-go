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
		return negateNum(e.Left.Eval(scope, ctrl))
	}
	l := e.Left.Eval(scope, ctrl)
	r := e.Right.Eval(scope, ctrl)

	// 字符串加法：任一侧为 String 时拼接（对照 Java Arith: String.valueOf(l).concat(String.valueOf(r))）
	if e.Op == "+" {
		if _, ok := l.(string); ok {
			return toStr(l) + toStr(r)
		}
		if _, ok := r.(string); ok {
			return toStr(l) + toStr(r)
		}
	}

	// 数值运算：按类型分派，整数运算保留整型（对照 Java Arith: INT/LONG/FLOAT/DOUBLE）
	lk, li, lf := numInfo(l)
	rk, ri, rf := numInfo(r)
	if lk != numNone && rk != numNone {
		if lk == numFloat || rk == numFloat {
			return arithFloat(e.Op, lf, rf)
		}
		return arithInt(e.Op, li, ri)
	}

	// 兜底：宽松降级为 float64 运算（Go 版本不抛异常，保持与历史行为一致）
	return arithFloat(e.Op, toFloat64(l), toFloat64(r))
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

// 数值种类：整数 / 浮点 / 非数值。
const (
	numNone = iota
	numInt
	numFloat
)

// numInfo 返回 v 的数值种类及其 int64/float64 表示。非数值返回 numNone。
func numInfo(v interface{}) (kind int, i int64, f float64) {
	if v == nil {
		return numNone, 0, 0
	}
	switch n := v.(type) {
	case int:
		return numInt, int64(n), float64(n)
	case int8:
		return numInt, int64(n), float64(n)
	case int16:
		return numInt, int64(n), float64(n)
	case int32:
		return numInt, int64(n), float64(n)
	case int64:
		return numInt, n, float64(n)
	case uint:
		return numInt, int64(n), float64(n)
	case uint8:
		return numInt, int64(n), float64(n)
	case uint16:
		return numInt, int64(n), float64(n)
	case uint32:
		return numInt, int64(n), float64(n)
	case uint64:
		return numInt, int64(n), float64(n)
	case float32:
		return numFloat, int64(n), float64(n)
	case float64:
		return numFloat, int64(n), n
	}
	return numNone, 0, 0
}

// arithInt 对两个 int64 做整数运算，除零返回 0（对照 Java Arith 整型分支）。
func arithInt(op string, l, r int64) interface{} {
	switch op {
	case "+":
		return l + r
	case "-":
		return l - r
	case "*":
		return l * r
	case "/":
		if r == 0 {
			return int64(0)
		}
		return l / r
	case "%":
		if r == 0 {
			return int64(0)
		}
		return l % r
	}
	return int64(0)
}

// arithFloat 对两个 float64 做浮点运算，除零返回 0（对照 Java Arith 浮点分支）。
func arithFloat(op string, l, r float64) interface{} {
	switch op {
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

// negateNum 取负，保留原数值种类（整数保持整型）。
func negateNum(v interface{}) interface{} {
	k, i, f := numInfo(v)
	if k == numInt {
		return -i
	}
	if k == numFloat {
		return -f
	}
	return -toFloat64(v)
}

// toStr 将任意值转为字符串用于拼接；nil 转空串，避免输出 "<nil>"。
func toStr(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
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
