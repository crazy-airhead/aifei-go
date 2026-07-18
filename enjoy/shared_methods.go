package enjoy

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
)

// ISSUE-0008：补齐 Java enjoy 的「共享方法库（SharedMethodKit）」与「扩展方法库
// （MethodKit extension methods）」两套体系。
//
// 与 Java 的对照：
//   - 共享方法：Java 挂在 EngineConfig.sharedMethodKit（per-engine），默认 SharedMethodLib
//     提供 isEmpty/notEmpty；模板里可裸调用 `isEmpty(x)`。
//   - 扩展方法：Java 在 MethodKit 的 static 块注册（进程级），把 Integer/Long/.../String
//     的 `toBoolean/toInt/.../toBigDecimal` 等挂到对应类型上；模板里 `age.toInt()`。
//
// Go 取舍：表达式 Eval 的入参只有 (scope, ctrl)，且单元测试直接用 NewScope(nil) 构造
// scope、并不持有 EngineConfig。为让两套方法在「模板体 / for 子作用域 / 裸 NewScope 单测」
// 中一致可用，这里采用与 Java 扩展方法一致的「进程级 kit」存放（Java 扩展方法本就是 static
// 全局；共享方法默认值亦全局），免于把 kit 逐层穿透 Scope。用户自定义通过 AddSharedMethod /
// AddExtensionMethod（或 Engine 同名方法）注册，对整个进程生效——已在解决记录中注明该取舍。

// SharedMethod 是裸调用 `name(args)` 的共享方法签名（对照 Java SharedMethodLib.isEmpty）。
// args 为实参列表；返回值即模板里该调用产生的值。
type SharedMethod func(args []interface{}) interface{}

// ExtensionMethod 是挂在对端值上的扩展方法签名（对照 Java IntegerExt.toInt(self)）。
// target 为调用主体（`.method()` 左侧的值），args 为其余实参。
type ExtensionMethod func(target interface{}, args []interface{}) interface{}

// SharedMethodKit 管理按名注册的共享方法（对照 Java SharedMethodKit）。
// Go 简化为「单名单实现」（模板场景无需按参数类型重载）。
type SharedMethodKit struct {
	mu      sync.RWMutex
	methods map[string]SharedMethod
}

// NewSharedMethodKit 创建一个空的共享方法库。
func NewSharedMethodKit() *SharedMethodKit {
	return &SharedMethodKit{methods: make(map[string]SharedMethod)}
}

// Add 注册或覆盖一个共享方法（对照 Java addSharedMethod）。
func (k *SharedMethodKit) Add(name string, fn SharedMethod) {
	if k == nil || fn == nil {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.methods[name] = fn
}

// Remove 按名移除一个共享方法（对照 Java removeSharedMethod）。
func (k *SharedMethodKit) Remove(name string) {
	if k == nil {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.methods, name)
}

// Call 按名查找并调用共享方法；未注册返回 (nil, false)（对照 Java getSharedMethodInfo）。
func (k *SharedMethodKit) Call(name string, args []interface{}) (interface{}, bool) {
	if k == nil {
		return nil, false
	}
	k.mu.RLock()
	fn, ok := k.methods[name]
	k.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return fn(args), true
}

// ExtensionMethodKit 按 reflect.Kind + 方法名注册的扩展方法集合（对照 Java MethodKit
// 的 extension method 部分，Java 按 Class 注册，Go 按 Kind 归并：所有整型/浮点 kind 共享
// 同一组数值转换方法，等价覆盖 Java 的 Integer/Long/Short/Byte/Float/Double Ext）。
type ExtensionMethodKit struct {
	mu      sync.RWMutex
	methods map[reflect.Kind]map[string]ExtensionMethod
}

// NewExtensionMethodKit 创建一个空的扩展方法库。
func NewExtensionMethodKit() *ExtensionMethodKit {
	return &ExtensionMethodKit{methods: make(map[reflect.Kind]map[string]ExtensionMethod)}
}

// Add 在指定 Kind 上注册一个扩展方法（对照 Java addExtensionMethod(targetClass, extObject)）。
func (k *ExtensionMethodKit) Add(kind reflect.Kind, name string, fn ExtensionMethod) {
	if k == nil || fn == nil {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	byName, ok := k.methods[kind]
	if !ok {
		byName = make(map[string]ExtensionMethod)
		k.methods[kind] = byName
	}
	byName[name] = fn
}

// Call 查找 target 对应 Kind 上的扩展方法并调用；解引用指针后按 Kind 分派。
// 未注册返回 (nil, false)（对照 Java MethodKit.getMethod → MethodInfo.notNull()）。
func (k *ExtensionMethodKit) Call(target interface{}, name string, args []interface{}) (interface{}, bool) {
	if k == nil || target == nil {
		return nil, false
	}
	rv := reflect.ValueOf(target)
	for rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return nil, false
		}
		rv = rv.Elem()
	}
	kind := rv.Kind()
	k.mu.RLock()
	byName, ok := k.methods[kind]
	k.mu.RUnlock()
	if !ok {
		return nil, false
	}
	fn, ok := byName[name]
	if !ok {
		return nil, false
	}
	// 传解引用后的具体值，使 *int / *string 等也能命中转换（toInt64/toFloat64 只认具体类型）。
	return fn(rv.Interface(), args), true
}

// ---- 进程级默认 kit（模板求值实际查询的实例） ----

var (
	// sharedMethodKit 是模板求值时裸调用 `name(args)` 在变量/共享对象未命中后查询的共享方法库
	// （对照 Java EngineConfig.sharedMethodKit，默认含 isEmpty/notEmpty）。
	sharedMethodKit = newDefaultSharedMethodKit()
	// extensionMethodKit 是模板求值时 `obj.method(args)` 的扩展方法库
	// （对照 Java MethodKit static 块，默认含 String 与全部数值 kind 的转换方法）。
	extensionMethodKit = newDefaultExtensionMethodKit()
)

// AddSharedMethod 向进程级共享方法库注册一个共享方法（对照 Java addSharedMethod）。
func AddSharedMethod(name string, fn SharedMethod) { sharedMethodKit.Add(name, fn) }

// RemoveSharedMethod 按名移除进程级共享方法库中的一个方法（对照 Java removeSharedMethod）。
func RemoveSharedMethod(name string) { sharedMethodKit.Remove(name) }

// AddExtensionMethod 向进程级扩展方法库在指定 Kind 上注册一个扩展方法
// （对照 Java MethodKit.addExtensionMethod）。
func AddExtensionMethod(kind reflect.Kind, name string, fn ExtensionMethod) {
	extensionMethodKit.Add(kind, name, fn)
}

// ---- 默认共享方法（SharedMethodLib） ----

func newDefaultSharedMethodKit() *SharedMethodKit {
	k := NewSharedMethodKit()
	k.Add("isEmpty", func(args []interface{}) interface{} {
		var v interface{}
		if len(args) > 0 {
			v = args[0]
		}
		return isEmptyValue(v)
	})
	k.Add("notEmpty", func(args []interface{}) interface{} {
		var v interface{}
		if len(args) > 0 {
			v = args[0]
		}
		return !isEmptyValue(v)
	})
	return k
}

// isEmptyValue 判断 Collection/Map/数组/String 是否为空（对照 Java SharedMethodLib.isEmpty）。
//
// 与 Java 的差异（Go 取「宽松」一致风格）：Java 仅接受 Collection/Map/数组/Iterator/Iterable，
// 其余类型抛 IllegalArgumentException；Go 对 nil→true、slice/array/map/string 看 len、其余
// （数值/bool/struct）一律视为非空（返回 false），不抛异常。Go 无 Iterator/Iterable 概念。
func isEmptyValue(v interface{}) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return true
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map, reflect.String:
		return rv.Len() == 0
	default:
		return false
	}
}

// ---- 默认扩展方法：String + 数值 ----

func newDefaultExtensionMethodKit() *ExtensionMethodKit {
	k := NewExtensionMethodKit()
	registerStringExtensions(k)
	registerNumericExtensions(k)
	return k
}

// registerStringExtensions 把字符串扩展方法（既含原 expr_eval 硬编码的 length/trim/upper/...，
// 也含对照 Java StringExt 的 toBoolean/toInt/toLong/...）注册到 reflect.String（对照 Java
// MethodKit.addExtensionMethod(String.class, new StringExt())）。
func registerStringExtensions(k *ExtensionMethodKit) {
	names := []string{
		"length", "len", "size", "trim",
		"upper", "toUpperCase", "lower", "toLowerCase",
		"contains", "startsWith", "endsWith", "indexOf",
		"substring", "sub", "replace", "split",
		"isEmpty",
		"toBoolean", "toInt", "toLong", "toFloat", "toDouble",
		"toShort", "toByte", "toBigInteger", "toBigDecimal",
	}
	for _, name := range names {
		n := name // 捕获给闭包
		k.Add(reflect.String, n, func(target interface{}, args []interface{}) interface{} {
			return stringExt(target.(string), n, args)
		})
	}
}

// registerNumericExtensions 把数值扩展方法（对照 Java Integer/Long/Short/Byte/Float/Double Ext）
// 注册到全部整型与浮点 kind——各 kind 的转换语义一致，共用同一组实现。
//
// 注意：实现一律走 numInfo（覆盖 int8-64 / uint8-64 / float32-64 全部 kind），而非 toInt64/toFloat64
// ——后两者的类型 switch 只含 int/int64/float64/float32，对 int32/uint 等 kind 会落到默认 0。
func registerNumericExtensions(k *ExtensionMethodKit) {
	numericMethods := map[string]ExtensionMethod{
		// 对照 Java XxxExt.toBoolean：self != 0（用 float 表示统一覆盖整数与浮点）。
		"toBoolean": func(t interface{}, _ []interface{}) interface{} {
			_, _, f := numInfo(t)
			return f != 0
		},
		"toInt": func(t interface{}, _ []interface{}) interface{} {
			_, i, _ := numInfo(t)
			return int(i)
		},
		"toLong": func(t interface{}, _ []interface{}) interface{} {
			_, i, _ := numInfo(t)
			return i
		},
		// Go 无 float32/float64 之分的应用语义，toFloat/toDouble 统一返回 float64
		// （numInfo 将 float32/float64 同归 numFloat，算术/比较一致）。
		"toFloat": func(t interface{}, _ []interface{}) interface{} {
			_, _, f := numInfo(t)
			return f
		},
		"toDouble": func(t interface{}, _ []interface{}) interface{} {
			_, _, f := numInfo(t)
			return f
		},
		// Go 模板类型系统无 narrow int 类型（isTruthy 仅识别 int/int64/float64），
		// toShort/toByte 退化为 int，避免 int8/int16 在布尔判断时被当作「恒真」。
		"toShort": func(t interface{}, _ []interface{}) interface{} {
			_, i, _ := numInfo(t)
			return int(i)
		},
		"toByte": func(t interface{}, _ []interface{}) interface{} {
			_, i, _ := numInfo(t)
			return int(i)
		},
		// Go 无 BigDecimal/BigInteger：toBigInteger 取最近整型 int64、toBigDecimal 取 float64，
		// 保证结果仍可参与模板算术/比较（numInfo 可识别）。已注明有损近似。
		"toBigInteger": func(t interface{}, _ []interface{}) interface{} {
			_, i, _ := numInfo(t)
			return i
		},
		"toBigDecimal": func(t interface{}, _ []interface{}) interface{} {
			_, _, f := numInfo(t)
			return f
		},
	}
	for _, kind := range numericKinds {
		for name, fn := range numericMethods {
			k.Add(kind, name, fn)
		}
	}
}

// numericKinds 是全部整型与浮点 kind（uint 系列一并纳入，覆盖无符号整数）。
var numericKinds = []reflect.Kind{
	reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
	reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
	reflect.Float32, reflect.Float64,
}

// stringExt 是字符串扩展方法的具体分派（原 expr_eval.stringMethod 迁入并补齐 StringExt 转换）。
// 对照 Java StringExt：toBoolean/toInt/.../toBigDecimal；length/trim/... 为 Go 侧便利方法。
func stringExt(s string, name string, args []interface{}) interface{} {
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
	case "toBoolean":
		// 对照 Java StringExt.toBoolean：blank → null；"true"/"1" → true；"false"/"0" → false；
		// 其余 Java 抛异常，Go 取宽松 → 返回 nil（不抛）。
		if strings.TrimSpace(s) == "" {
			return nil
		}
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "true", "1":
			return true
		case "false", "0":
			return false
		}
		return nil
	case "toInt":
		return int(toInt64(s))
	case "toLong":
		return toInt64(s)
	case "toFloat", "toDouble":
		return toFloat64(s)
	case "toShort", "toByte":
		return int(toInt64(s))
	case "toBigInteger":
		return toInt64(s)
	case "toBigDecimal":
		return toFloat64(s)
	}
	return nil
}
