package enjoy

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
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
		// 裸调用 `name(args)`：先查变量 / 共享对象，未命中再查共享方法库 isEmpty/notEmpty 等
		// （对照 Java：ID 取值 → SharedMethodKit.getSharedMethodInfo）。
		if fn := scope.Get(e.Name); fn != nil {
			return callFunc(fn, args)
		}
		if r, ok := sharedMethodKit.Call(e.Name, args); ok {
			return r
		}
		return nil
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

	// 扩展方法：string 的 length/trim/.../toInt、数值的 toInt/toLong/toBoolean/... 等
	// （对照 Java MethodKit extension methods，已由硬编码迁入可注册的 ExtensionMethodKit）。
	if r, ok := extensionMethodKit.Call(obj, e.Name, args); ok {
		return r
	}

	// 反射：任意对象自身的真实方法（如注入工具对象后 #(tool.Up(...))）。
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

// AssignExpr 是赋值表达式，对照 Java Assign 支持两种形态：
//  1. 普通赋值 ID = expr（Target 为 nil，Name 为左标识符）：由 scope.Set 自内向外改写。
//  2. 索引赋值 container[index] = expr（Target 为 *IndexExpr）：map[key] / list[i] / array[i]。
//
// 右结合递归解析支持无限连写：id = a[i=0] = a[1] = 123。
type AssignExpr struct {
	Name   string // 普通赋值左侧标识符（Target 为 nil 时生效）
	Target Expr   // 索引赋值左侧容器表达式（*IndexExpr），递归求址后写
	Value  Expr
}

func (e *AssignExpr) Eval(scope *Scope, ctrl *Ctrl) interface{} {
	// 普通赋值：ID = expr（对照 Java Assign.assignVariable，wisdom 走 scope.Set 向上改写）。
	if e.Target == nil {
		v := e.Value.Eval(scope, ctrl)
		scope.Set(e.Name, v)
		return v
	}
	// 索引赋值：container[index] = expr（对照 Java Assign.assignElement）。
	if ix, ok := e.Target.(*IndexExpr); ok {
		return assignElement(scope, ctrl, ix, e.Value)
	}
	// 解析期已拦截非 ID/Index 左侧，理论上不会到达。
	return e.Value.Eval(scope, ctrl)
}

// assignElement 执行 container[index] = valueExpr 赋值。
// 求值顺序对照 Java Assign.assignElement：先 container、再 index，最后 right value。
// Go 版本宽松：container / index 为 nil 或类型不匹配时静默跳过（不抛异常）。
func assignElement(scope *Scope, ctrl *Ctrl, ix *IndexExpr, valueExpr Expr) interface{} {
	container := ix.Obj.Eval(scope, ctrl)
	if container == nil {
		return nil
	}
	idx := ix.Index.Eval(scope, ctrl)
	if idx == nil {
		return nil
	}
	v := valueExpr.Eval(scope, ctrl)
	setIndex(container, idx, v)
	return v
}

// setIndex 向 map / slice / array 的指定位置写入值（对照 Java Map.put / List.set / Array.set）。
// 类型不匹配或不可寻址（值数组）时静默跳过，避免 reflect 写入 panic。
func setIndex(container, idx, value interface{}) {
	v := reflect.ValueOf(container)
	switch v.Kind() {
	case reflect.Map:
		key := reflect.ValueOf(idx)
		if !key.Type().AssignableTo(v.Type().Key()) {
			return
		}
		elemType := v.Type().Elem()
		var elem reflect.Value
		if value == nil {
			elem = reflect.Zero(elemType)
		} else {
			rv := reflect.ValueOf(value)
			if !rv.Type().AssignableTo(elemType) {
				return
			}
			elem = rv
		}
		v.SetMapIndex(key, elem)
	case reflect.Slice, reflect.Array:
		i := toInt(idx)
		if i < 0 || i >= v.Len() {
			return
		}
		elem := v.Index(i)
		if !elem.CanSet() { // 值数组不可寻址，无法写入
			return
		}
		if value == nil {
			elem.Set(reflect.Zero(elem.Type()))
			return
		}
		rv := reflect.ValueOf(value)
		if !rv.Type().AssignableTo(elem.Type()) {
			return
		}
		elem.Set(rv)
	}
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

// getField 取对象字段，优先级对照 Java Field：
//  1. getter 方法 GetXxx()（首字母大写，零参；值接收者与指针接收者均可）。
//  2. public 字段（导出 struct 字段，按名匹配）。
//  3. map.get(name)（对照 Java Map / Record / Model.get）。
//
// 方法查找在解引用前的原值上进行，以同时命中值接收者方法（struct 值）与
// 指针接收者方法（struct 指针）；未命中再解引用取字段 / map key。
func getField(obj interface{}, name string) interface{} {
	v := reflect.ValueOf(obj)
	// 1: getter 方法 GetXxx()（对照 Java user.getName() 优先）。
	getterName := "Get" + firstCharToUpperCase(name)
	if m := v.MethodByName(getterName); m.IsValid() && m.Type().NumIn() == 0 {
		return callReflect(m, nil)
	}

	rv := v
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Struct:
		// 2: public 字段（对照 Java public field）。
		if f := rv.FieldByName(name); f.IsValid() {
			return f.Interface()
		}
	case reflect.Map:
		// 3: map.get(name)（对照 Java Map / Record / Model.get）。
		if val := rv.MapIndex(reflect.ValueOf(name)); val.IsValid() {
			return val.Interface()
		}
	}
	return nil
}

// firstCharToUpperCase 将首字母大写，其余不变（对照 Java StrUtil.firstCharToUpperCase），
// 用于由字段名推导 getter 方法名：name → GetName。
func firstCharToUpperCase(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	if r[0] >= 'a' && r[0] <= 'z' {
		r[0] -= 'a' - 'A'
	}
	return string(r)
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

// forEntry 封装 map 迭代产生的 key/value 项，模板中通过 entry.key / entry.value 取值
// （对照 Java ForEntry.getKey()/getValue()）。用 map 暴露 key/value，getField 会按
// map key 命中（亦支持 struct + GetKey()/GetValue() getter，见 getField）。
func forEntry(k, v interface{}) map[string]interface{} {
	return map[string]interface{}{"key": k, "value": v}
}

// forIteratorStatus 构造迭代型 #for(id : expr) 的循环状态对象，作为作用域内的 `for` 变量，
// 模板通过 for.index/count/first/last/odd/even/size/outer 对象式访问
// （对照 Java ForIteratorStatus）。odd/even 按 count 计数：index 偶数 → 第奇数个 → odd=true，
// 与 Java getOdd()=index%2==0 / getEven()=index%2!=0 一致。outer 为外层循环状态（for.outer）。
func forIteratorStatus(outer interface{}, index, size int) map[string]interface{} {
	return map[string]interface{}{
		"index": index,
		"count": index + 1,
		"first": index == 0,
		"last":  index == size-1,
		"odd":   index%2 == 0,
		"even":  index%2 != 0,
		"size":  size,
		"outer": outer,
	}
}

// toSlice 将迭代源规整为 []interface{}，支持 slice/array/map 与指针解引用；
// 非集合单对象自动包成单元素列表（对照 Java ForIteratorStatus.init）。
// map 转为 key/value entry 列表，使 #for(entry : myMap) 可遍历 #(entry.key)/#(entry.value)。
func toSlice(v interface{}) []interface{} {
	if v == nil {
		return nil
	}
	rv := reflect.ValueOf(v)
	// 解引用指针（含 ptr-to-slice / ptr-to-array / ptr-to-map）
	for rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		result := make([]interface{}, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			result[i] = rv.Index(i).Interface()
		}
		return result
	case reflect.Map:
		result := make([]interface{}, 0, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			result = append(result, forEntry(iter.Key().Interface(), iter.Value().Interface()))
		}
		return result
	}
	// 非集合单对象自动包成单元素列表（对照 Java SingleObjectIterator）
	return []interface{}{v}
}
