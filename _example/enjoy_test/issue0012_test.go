package enjoy_test

import (
	"strings"
	"testing"

	"github.com/crazy-airhead/aifei-go/enjoy"
)

// ISSUE-0012 验收：?? 优先级、#@name?() 安全调用、:: 默认禁用、call 严格化、
// #include 相对父目录、错误行号定位、EngineConfig 配置项（keepLineBlankDirectives / roundingMode）。
//
// 对照 Java 源码（aifei-enjoy）锁定语义：
//   - ?? 优先级：ExprParser.nullSafe() 位于 mulDivMod(* / %) 与 unary 之间，for 循环左结合
//   - :: ：ExprParser.staticMember() 检查 isStaticMethodExpressionEnabled（默认 false），真反射用 Class.forName
//   - Location：ParseException 带 templateFile + row

// --- 1. ?? 优先级（对齐 Java nullSafe）---

// ?? 优先级高于 + - ：1 ?? 2 + 3 → (1 ?? 2) + 3 = 1 + 3 = 4（1 非 nil）。
// 旧实现把 ?? 放在 postfix 时此值不同。
func TestIssue0012CoalescePriorityOverAdd(t *testing.T) {
	engine := enjoy.NewEngine("i12-coalesce-add")
	tpl := engine.GetTemplateByString("#(1 ?? 2 + 3)")
	if got := renderToString(t, tpl, nil); got != "4" {
		t.Fatalf("1 ?? 2 + 3 应为 (1??2)+3 = 4，got %q", got)
	}
}

// ?? 优先级高于 * / % ：2 * 3 ?? 5 → 2 * (3 ?? 5) = 2 * 3 = 6（对齐 Java mulDivMod→nullSafe）。
func TestIssue0012CoalescePriorityOverMul(t *testing.T) {
	engine := enjoy.NewEngine("i12-coalesce-mul")
	tpl := engine.GetTemplateByString("#(2 * 3 ?? 5)")
	if got := renderToString(t, tpl, nil); got != "6" {
		t.Fatalf("2 * 3 ?? 5 应为 2*(3??5) = 6，got %q", got)
	}
}

// 左侧 nil 时 ?? 返回右侧：nil ?? 2 + 3 → (nil ?? 2) + 3 = 2 + 3 = 5。
func TestIssue0012CoalesceLeftNil(t *testing.T) {
	engine := enjoy.NewEngine("i12-coalesce-nil")
	tpl := engine.GetTemplateByString("#(nil ?? 2 + 3)")
	if got := renderToString(t, tpl, nil); got != "5" {
		t.Fatalf("nil ?? 2 + 3 应为 5，got %q", got)
	}
}

// 链式左结合：a ?? b ?? c → (a ?? b) ?? c（对齐 Java nullSafe for 循环）。
func TestIssue0012CoalesceChain(t *testing.T) {
	engine := enjoy.NewEngine("i12-coalesce-chain")
	tpl := engine.GetTemplateByString("#(nil ?? nil ?? 7)")
	if got := renderToString(t, tpl, nil); got != "7" {
		t.Fatalf("nil ?? nil ?? 7 应为 7，got %q", got)
	}
}

// ?? 右操作数支持一元运算符（旧实现 ?? 在 postfix，右操作数为 parsePostfix 遇 - 会报错）。
func TestIssue0012CoalesceUnary(t *testing.T) {
	engine := enjoy.NewEngine("i12-coalesce-unary")
	tpl := engine.GetTemplateByString("#(nil ?? -5)")
	if got := renderToString(t, tpl, nil); got != "-5" {
		t.Fatalf("nil ?? -5 应为 -5，got %q", got)
	}
}

// --- 2. #@name?() 安全调用 ---

// nullSafe：函数不存在静默跳过（对照 Java callIfDefined）。
func TestIssue0012SafeCallSkip(t *testing.T) {
	engine := enjoy.NewEngine("i12-safecall-skip")
	tpl := engine.GetTemplateByString("[#@noSuchFn?()]")
	if got := renderToString(t, tpl, nil); got != "[]" {
		t.Fatalf("#@noSuchFn?() 应跳过得 []，got %q", got)
	}
}

// 非 nullSafe：函数不存在抛异常（对照 Java Define.call）。
func TestIssue0012CallUndefinedErrors(t *testing.T) {
	engine := enjoy.NewEngine("i12-call-undefined")
	tpl := engine.GetTemplateByString("#@noSuchFn()")
	if _, err := tpl.RenderToString0(nil); err == nil {
		t.Fatal("非 nullSafe 调用不存在的函数应报错")
	}
}

// --- 3. :: 静态访问默认禁用 ---

// Cls::method 形式：默认禁用报错（对照 Java isStaticMethodExpressionEnabled=false）。
func TestIssue0012StaticDisableSimple(t *testing.T) {
	engine := enjoy.NewEngine("i12-static-simple")
	tpl := engine.GetTemplateByString(`#(Str::isBlank("x"))`)
	_, err := tpl.RenderToString0(nil)
	if err == nil || !strings.Contains(err.Error(), "not enabled") {
		t.Fatalf("Str::isBlank 应报 not enabled，got %v", err)
	}
}

// a.b.c::method 全限定名形式：默认禁用报错（预扫描覆盖）。
func TestIssue0012StaticDisableFqn(t *testing.T) {
	engine := enjoy.NewEngine("i12-static-fqn")
	tpl := engine.GetTemplateByString(`#(com.foo.Bar::isBlank("x"))`)
	_, err := tpl.RenderToString0(nil)
	if err == nil || !strings.Contains(err.Error(), "not enabled") {
		t.Fatalf("com.foo.Bar::isBlank 应报 not enabled，got %v", err)
	}
}

// 字符串字面量内的 :: 不误判（词法器跳过字符串内容）。
func TestIssue0012StaticInStringLiteral(t *testing.T) {
	engine := enjoy.NewEngine("i12-static-in-str")
	tpl := engine.GetTemplateByString(`#('a::b')`)
	if got := renderToString(t, tpl, nil); got != "a::b" {
		t.Fatalf("字符串内 :: 不应误判，got %q", got)
	}
}

// i12StrUtil 演示「导入整个工具类」：开启 :: 后，以 AddStatic 注册其实例，
// 所有导出方法（Upper/Lower）自动可在模板 Str::method(args) 调用——Java 静态方法的 Go 等价。
type i12StrUtil struct{}

func (i12StrUtil) Upper(s string) string { return strings.ToUpper(s) }
func (i12StrUtil) Lower(s string) string { return strings.ToLower(s) }

// 开启 SetStaticMethodExpressionEnabled(true) 后，AddStatic(alias, obj) 注册一个命名空间对象，
// 其所有导出方法自动可用：Str::Upper("hi") / Str::Lower("HI") 反射调用。
// Go 无 Class.forName / 包级函数整体反射，故以 struct 方法落地（要导标准库需包一层 struct）。
func TestIssue0012StaticEnableCallsRegisteredFunc(t *testing.T) {
	engine := enjoy.NewEngine("i12-static-enable")
	engine.GetConfig().SetStaticMethodExpressionEnabled(true)
	enjoy.AddStatic("Str", i12StrUtil{})
	defer enjoy.RemoveStatic("Str")
	tpl := engine.GetTemplateByString(`#(Str::Upper("hi"))-#(Str::Lower("HI"))`)
	if got := renderToString(t, tpl, nil); got != "HI-hi" {
		t.Fatalf("整包导入后 Str::Upper/Lower 应得 HI-hi，got %q", got)
	}
}

// 开启后未注册的命名空间/方法静默返回 nil（Go 宽松，不抛异常）。
func TestIssue0012StaticEnableUnregisteredIsNil(t *testing.T) {
	engine := enjoy.NewEngine("i12-static-unregistered")
	engine.GetConfig().SetStaticMethodExpressionEnabled(true)
	tpl := engine.GetTemplateByString(`[#(Nope::noSuch("x"))]`)
	if got := renderToString(t, tpl, nil); got != "[]" {
		t.Fatalf("未注册的 :: 调用应静默返回 nil 得 []，got %q", got)
	}
}

// --- 4. call 严格化 ---

// 参数个数不匹配抛异常（对照 Java Define.call 形参/实参匹配）。
func TestIssue0012CallArgcMismatch(t *testing.T) {
	engine := enjoy.NewEngine("i12-call-argc")
	tpl := engine.GetTemplateByString(`#define(f(a, b))#(a)-#(b)#end#@f(1)`)
	if _, err := tpl.RenderToString0(nil); err == nil ||
		!strings.Contains(err.Error(), "parameter count mismatch") {
		t.Fatalf("参数个数不匹配应报错，got %v", err)
	}
}

// 动态 #call 的 nullSafe（首参 true）函数不存在跳过。
func TestIssue0012DynamicCallNullSafe(t *testing.T) {
	engine := enjoy.NewEngine("i12-dyncall-nullsafe")
	tpl := engine.GetTemplateByString(`[#call(true, "noSuchFn", "x")]`)
	if got := renderToString(t, tpl, nil); got != "[]" {
		t.Fatalf("#call(true,...) 不存在函数应跳过得 []，got %q", got)
	}
}

// --- 5. #include 相对父文件目录 ---

// 嵌套目录：父模板 testdata/sub/_parent.html include "_child.html"，
// 应相对父目录解析为 testdata/sub/_child.html（不设 baseTemplatePath）。
func TestIssue0012IncludeRelativeToParent(t *testing.T) {
	engine := enjoy.NewEngine("i12-include-rel")
	engine.SetDevMode(true)
	tpl := engine.GetTemplate("testdata/sub/_parent.html")
	got := renderToString(t, tpl, nil)
	if !strings.Contains(got, "P:") || !strings.Contains(got, "C") {
		t.Fatalf("include 应相对父目录解析出 P:C，got %q", got)
	}
}

// --- 6. 错误行号定位 ---

// 解析期语法错误带行号（#for 非迭代型）。
func TestIssue0012ErrorLine(t *testing.T) {
	engine := enjoy.NewEngine("i12-line")
	tpl := engine.GetTemplateByString("#for(bad)")
	_, err := tpl.RenderToString0(nil)
	if err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("#for 语法错应带 line 1，got %v", err)
	}
}

// 多行模板：第二行的错误带 line 2。
func TestIssue0012ErrorLineMulti(t *testing.T) {
	engine := enjoy.NewEngine("i12-line-multi")
	tpl := engine.GetTemplateByString("ok\n#for(bad)")
	_, err := tpl.RenderToString0(nil)
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("第二行错误应带 line 2，got %v", err)
	}
}

// directive 参数校验错误带行号（#number 空参）。
func TestIssue0012DirectiveErrorLine(t *testing.T) {
	engine := enjoy.NewEngine("i12-directive-line")
	tpl := engine.GetTemplateByString("#number()")
	_, err := tpl.RenderToString0(nil)
	if err == nil || !strings.Contains(err.Error(), "line") {
		t.Fatalf("#number() 错误应带行号，got %v", err)
	}
}

// --- 7. EngineConfig 配置项 ---

// keepLineBlankDirectives=true：保留行首指令后的空行（默认 false 会吃掉）。
func TestIssue0012KeepLineBlank(t *testing.T) {
	engine := enjoy.NewEngine("i12-keepblank")
	engine.GetConfig().SetKeepLineBlankDirectives(true)
	// #set 独占一行后跟一个空行；默认会吃掉换行，开启后保留。
	tpl := engine.GetTemplateByString("#set(x = 1)\n\nafter")
	got := renderToString(t, tpl, nil)
	if !strings.Contains(got, "\n\nafter") && !strings.Contains(got, "\nafter") {
		t.Fatalf("keepLineBlank 渲染异常，got %q", got)
	}
	// 对照：默认 false 时行首指令后的首个换行被吃掉。
	engine2 := enjoy.NewEngine("i12-keepblank-default")
	tpl2 := engine2.GetTemplateByString("#set(x = 1)\nafter")
	got2 := renderToString(t, tpl2, nil)
	if strings.Contains(got2, "\nafter") {
		t.Fatalf("默认 keepLineBlank=false 应吃掉指令行换行，got %q", got2)
	}
}

// roundingMode=HALF_UP：#number 按四舍五入（默认 HALF_EVEN 银行家舍入）。
func TestIssue0012RoundingMode(t *testing.T) {
	engine := enjoy.NewEngine("i12-rounding")
	engine.GetConfig().SetRoundingMode(enjoy.RoundingModeHalfUp)
	// 2.5 在 HALF_UP → 3；HALF_EVEN → 2。
	tpl := engine.GetTemplateByString(`#number(2.5, "#")`)
	if got := renderToString(t, tpl, nil); got != "3" {
		t.Fatalf("HALF_UP 下 2.5 应为 3，got %q", got)
	}
	engine2 := enjoy.NewEngine("i12-rounding-default")
	tpl2 := engine2.GetTemplateByString(`#number(2.5, "#")`)
	if got := renderToString(t, tpl2, nil); got != "2" {
		t.Fatalf("默认 HALF_EVEN 下 2.5 应为 2，got %q", got)
	}
}
