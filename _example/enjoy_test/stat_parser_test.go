package enjoy_test

import (
	"strings"
	"testing"

	"github.com/crazy-airhead/aifei-go/enjoy"
)

// ISSUE-0003：#returnIf(cond) 的 cond 是「返回条件」，仅当求值为真才提前返回；
// 为假时继续渲染后续内容（此前实现把它当无条件 return，恒返回）。
func TestReturnIfConditional(t *testing.T) {
	engine := enjoy.NewEngine("issue0003")
	tpl := engine.GetTemplateByString("head#returnIf(count > 0)tail")

	cases := []struct {
		name string
		data map[string]interface{}
		want string
	}{
		{"cond-false-continue", map[string]interface{}{"count": 0}, "headtail"},
		{"cond-negative-continue", map[string]interface{}{"count": -1}, "headtail"},
		{"cond-true-return", map[string]interface{}{"count": 3}, "head"},
	}
	for _, c := range cases {
		got := renderToString(t, tpl, c.data)
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// 条件为真时，returnIf 之后的 #(expr) / 文本都不应再渲染。
func TestReturnIfSkipsFollowing(t *testing.T) {
	engine := enjoy.NewEngine("issue0003-follow")
	tpl := engine.GetTemplateByString("A#returnIf(ok)B#(value)C")

	if got := renderToString(t, tpl, map[string]interface{}{"ok": true, "value": "X"}); got != "A" {
		t.Errorf("ok=true: got %q, want %q (应提前返回)", got, "A")
	}
	if got := renderToString(t, tpl, map[string]interface{}{"ok": false, "value": "X"}); got != "ABXC" {
		t.Errorf("ok=false: got %q, want %q (应继续渲染)", got, "ABXC")
	}
}

// 空参数应解析失败（对照 Java ReturnIf.java 抛 ParseException）。
// 经公开 API 验证：#returnIf() 解析失败，RenderToString 返回 error（不再烘进输出）。
func TestReturnIfEmptyParam(t *testing.T) {
	engine := enjoy.NewEngine("issue0003-empty")
	for _, src := range []string{"#returnIf()", "#returnIf(   )"} {
		out, err := engine.GetTemplateByString(src).RenderToString(nil)
		if err == nil {
			t.Errorf("%q 空参数应解析报错，实际渲染成功输出 %q", src, out)
		}
	}
}

// ISSUE-0004：#for(x : collection) 此前只接受 slice/array，map 被当成单个元素，
// 循环体只执行一次且取到 map 本身。修复后 map 迭代产生 key/value entry 项。
func TestForIterateMap(t *testing.T) {
	engine := enjoy.NewEngine("issue0004-map")
	tpl := engine.GetTemplateByString("#for(entry : m)#(entry.key)=#(entry.value);#end")

	got := renderToString(t, tpl, map[string]interface{}{
		"m": map[string]interface{}{"a": "1", "b": "2"},
	})
	// map 迭代顺序不确定，逐项校验
	if !containsAll(got, "a=1;", "b=2;") {
		t.Fatalf("map 迭代应输出 a=1 与 b=2，got %q", got)
	}
	if n := count(got, "="); n != 2 {
		t.Fatalf("应迭代 2 个 entry，实际 %d 个，got %q", n, got)
	}
}

// map 迭代产生的项同时可取 .key 与 .value，且数量等于 map 大小（覆盖 ForEntry 语义）。
func TestForIterateMapEntryCount(t *testing.T) {
	engine := enjoy.NewEngine("issue0004-count")
	tpl := engine.GetTemplateByString("#for(entry : m)[#(entry.key):#(entry.value)]#end")
	got := renderToString(t, tpl, map[string]interface{}{
		"m": map[string]int{"x": 10, "y": 20, "z": 30},
	})
	if n := count(got, ":"); n != 3 {
		t.Fatalf("应迭代 3 个 entry，实际 %d 个，got %q", n, got)
	}
	if !containsAll(got, "x:10", "y:20", "z:30") {
		t.Fatalf("应包含每个 entry 的 key:value，got %q", got)
	}
}

// 指针切片与空 map 边界：ptr-to-slice 应正常解引用；空 map 不应进入循环体。
func TestForIteratePointerSliceAndEmptyMap(t *testing.T) {
	engine := enjoy.NewEngine("issue0004-ptr")

	// ptr-to-slice 应解引用后迭代
	slice := []interface{}{"p", "q"}
	tpl := engine.GetTemplateByString("#for(x : ps)#(x)#end")
	if got := renderToString(t, tpl, map[string]interface{}{"ps": &slice}); got != "pq" {
		t.Fatalf("ptr-to-slice 应迭代为 pq，got %q", got)
	}

	// 空 map：循环体不应执行（迭代 0 次）。模板以 #end 收尾，避免「#end 后文本被吃一字」的解析细节干扰。
	tpl2 := engine.GetTemplateByString(">#for(entry : em)X#end")
	if got := renderToString(t, tpl2, map[string]interface{}{"em": map[string]interface{}{}}); got != ">" {
		t.Fatalf("空 map 应迭代 0 次（仅保留前导文本），got %q", got)
	}
}

// 非集合单对象应自动包成单元素列表（对照 Java SingleObjectIterator）。
// 循环状态经 ISSUE-0009 改为对象式访问，故用 for.index（index 从 0 开始）。
func TestForIterateSingleObject(t *testing.T) {
	engine := enjoy.NewEngine("issue0004-single")
	tpl := engine.GetTemplateByString("#for(x : n)[#(x)/#(for.index)]#end")
	got := renderToString(t, tpl, map[string]interface{}{"n": 42})
	if got != "[42/0]" {
		t.Fatalf("单对象应包成单元素列表迭代一次，got %q", got)
	}
}

// containsAll 报告 s 是否包含全部子串。
func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

// count 统计 substr 在 s 中非重叠出现的次数。
func count(s, substr string) int {
	if substr == "" {
		return 0
	}
	n := 0
	for {
		i := strings.Index(s, substr)
		if i < 0 {
			break
		}
		n++
		s = s[i+len(substr):]
	}
	return n
}

// ISSUE-0009：#for 的 #else 分支 —— 集合为空（循环体一次未执行）时走 else 体。
// 用 #else#(fallback) 表达式体，规避「ASCII 紧贴 #else 被并入指令名」的词法细节。
func TestForElseBranch(t *testing.T) {
	engine := enjoy.NewEngine("issue0009-else")
	tpl := engine.GetTemplateByString("#for(x : list)#(x)#else#(fallback)#end")

	if got := renderToString(t, tpl, map[string]interface{}{"list": []interface{}{}, "fallback": "EMPTY"}); got != "EMPTY" {
		t.Fatalf("空集合应走 #else，got %q", got)
	}
	if got := renderToString(t, tpl, map[string]interface{}{"list": []interface{}{"a", "b"}, "fallback": "EMPTY"}); got != "ab" {
		t.Fatalf("非空集合应迭代且不触发 #else，got %q", got)
	}
}

// ISSUE-0009：迭代型循环状态对象 for.index/count/first/last/odd/even/size
// （对照 Java ForIteratorStatus；odd/even 按 count 计数：index 偶数为第奇数个）。
func TestForIteratorStatus(t *testing.T) {
	engine := enjoy.NewEngine("issue0009-status")
	tpl := engine.GetTemplateByString(
		"#for(x : list)[#(for.index)/#(for.count)/#(for.first)/#(for.last)/#(for.odd)/#(for.even)/#(for.size)]#end")
	got := renderToString(t, tpl, map[string]interface{}{"list": []interface{}{"a", "b", "c"}})
	want := "[0/1/true/false/true/false/3]" +
		"[1/2/false/false/false/true/3]" +
		"[2/3/false/true/true/false/3]"
	if got != want {
		t.Fatalf("迭代型循环状态不符，got %q want %q", got, want)
	}
}

// ISSUE-0009：嵌套循环的 for.outer 指向外层循环状态，可取 for.outer.index。
func TestForOuterNesting(t *testing.T) {
	engine := enjoy.NewEngine("issue0009-outer")
	tpl := engine.GetTemplateByString(
		"#for(r : rows)#for(c : cols)[#(for.index)/#(for.outer.index)]#end#end")
	got := renderToString(t, tpl, map[string]interface{}{
		"rows": []interface{}{0, 1},
		"cols": []interface{}{0, 1},
	})
	want := "[0/0][1/0][0/1][1/1]"
	if got != want {
		t.Fatalf("嵌套 for.outer 不符，got %q want %q", got, want)
	}
}

// ISSUE-0009：Go 版本不支持 C 风格 for(init; cond; update)（收敛 for 语义，仅迭代型）。
// header 不匹配迭代型语法时报语法错误，RenderToString 返回 error（不再烘进输出）。
func TestForCStyleNotSupported(t *testing.T) {
	engine := enjoy.NewEngine("issue0009-no-cstyle")
	tpl := engine.GetTemplateByString("#for(i=0; i<3; i++)X#end")
	out, err := tpl.RenderToString(nil)
	if err == nil {
		t.Fatalf("C 风格 for 应报语法错误，实际渲染成功输出 %q", out)
	}
}

// ISSUE-0009：#for 内 #break/#continue/#return 的跳转语义（对照 Java For）。
// #break 跳出循环；#continue 跳过本次后续；#return 透传出整个模板。
func TestForBreakContinueReturn(t *testing.T) {
	engine := enjoy.NewEngine("issue0009-jump")
	list := map[string]interface{}{"list": []interface{}{1, 2, 3}}

	if got := renderToString(t, engine.GetTemplateByString("#for(x : list)#if(x == 2)#break#end#(x)#end"), list); got != "1" {
		t.Fatalf("#break 应在 x==2 时跳出，got %q want %q", got, "1")
	}
	if got := renderToString(t, engine.GetTemplateByString("#for(x : list)#if(x == 2)#continue#end#(x)#end"), list); got != "13" {
		t.Fatalf("#continue 应跳过 x==2，got %q want %q", got, "13")
	}
	// #return 透传：x==2 时返回，后续循环与 #end 后的 -TAIL 均不输出。
	if got := renderToString(t, engine.GetTemplateByString("#for(x : list)#if(x == 2)#return#end#(x)#end-TAIL"), list); got != "1" {
		t.Fatalf("#return 应透传终止模板，got %q want %q", got, "1")
	}
}

// ISSUE-0009：即使首次即 #break，循环体已执行一次，#else 不应触发（对照 Java index!=0 判定）。
func TestForElseNotRunWhenBreakOnFirst(t *testing.T) {
	engine := enjoy.NewEngine("issue0009-break-else")
	tpl := engine.GetTemplateByString("#for(x : list)#break#else#(fallback)#end")

	if got := renderToString(t, tpl, map[string]interface{}{"list": []interface{}{1, 2}, "fallback": "EMPTY"}); got != "" {
		t.Fatalf("首次即 #break 时 #else 不应触发，got %q", got)
	}
	if got := renderToString(t, tpl, map[string]interface{}{"list": []interface{}{}, "fallback": "EMPTY"}); got != "EMPTY" {
		t.Fatalf("空集合应触发 #else，got %q", got)
	}
}

// ISSUE-0010：#call 调用时以 caller scope 为 parent 构造子作用域，函数体内可见外层变量
// （对照 Java Define.call 的 new Scope(scope)）。此前用 NewScope(empty) 无 parent，
// 函数体读不到外层变量。
func TestCallSeesOuterScope(t *testing.T) {
	engine := enjoy.NewEngine("issue0010-scope")
	tpl := engine.GetTemplateByString(`#define(greet())Hello #(user)#end#@greet()`)

	got := renderToString(t, tpl, map[string]interface{}{"user": "Aifei"})
	if got != "Hello Aifei" {
		t.Fatalf("define 函数体应可见外层变量 user，期望 'Hello Aifei'，got %q", got)
	}
}

// 同一作用域内：define 函数体修改局部变量不影响外层（参数与局部赋值落在子作用域）。
func TestCallLocalDoesNotLeak(t *testing.T) {
	engine := enjoy.NewEngine("issue0010-local")
	tpl := engine.GetTemplateByString(`#define(bump())#set(user = "inner")#end#@bump()[#(user)]`)

	got := renderToString(t, tpl, map[string]interface{}{"user": "outer"})
	if got != "[outer]" {
		t.Fatalf("define 内 #set 应落在子作用域、不污染外层 user，期望 '[outer]'，got %q", got)
	}
}

// ISSUE-0010：#define 在 parse 阶段注册，支持前向引用——文档顺序靠后的 define 也能被
// 前面的 call 调用（对照 Java Parser.statList: env.addFunction）。
func TestCallForwardReference(t *testing.T) {
	engine := enjoy.NewEngine("issue0010-fwdref")
	tpl := engine.GetTemplateByString(`[#@greet("Sam")]|#define(greet(name))Hi #(name)#end`)

	if got := renderToString(t, tpl, nil); got != "[Hi Sam]|" {
		t.Fatalf("call 在 define 之前应能前向引用，期望 '[Hi Sam]|'，got %q", got)
	}
}

// 前向引用同样适用于动态 #call(...) 指令。
func TestCallDynamicForwardReference(t *testing.T) {
	engine := enjoy.NewEngine("issue0010-fwdref-dyn")
	tpl := engine.GetTemplateByString(`[#call("greet", "Sam")]|#define(greet(name))Hi #(name)#end`)

	if got := renderToString(t, tpl, nil); got != "[Hi Sam]|" {
		t.Fatalf("动态 #call 在 define 之前应能前向引用，期望 '[Hi Sam]|'，got %q", got)
	}
}

// 嵌套 define：外层 define 体里的内层 define 也能被注册并调用。
func TestCallNestedDefine(t *testing.T) {
	engine := enjoy.NewEngine("issue0010-nested")
	tpl := engine.GetTemplateByString(`#define(outer())#define(inner())I#end#@inner()#end#@outer()`)

	if got := renderToString(t, tpl, nil); got != "I" {
		t.Fatalf("嵌套 define 应可注册并调用，期望 'I'，got %q", got)
	}
}

// ISSUE-0010：函数体内的 #return/#break/#continue 在 define 边界消化，不外泄到调用方
// （对照 Java Define.call 末尾 setJumpNone）。#return 在函数体内只结束本次调用。
func TestCallReturnConsumedInDefine(t *testing.T) {
	engine := enjoy.NewEngine("issue0010-return")
	tpl := engine.GetTemplateByString(`#define(f())A#return#end#@f()|TAIL`)

	if got := renderToString(t, tpl, nil); got != "A|TAIL" {
		t.Fatalf("define 内 #return 不应外泄，期望 'A|TAIL'，got %q", got)
	}
}

// #break 在函数体内消化：不应跳出外层 #for 循环。
func TestCallBreakConsumedInDefine(t *testing.T) {
	engine := enjoy.NewEngine("issue0010-break")
	tpl := engine.GetTemplateByString(`#define(f())X#break#end#for(i : list)#@f()#end`)

	got := renderToString(t, tpl, map[string]interface{}{"list": []interface{}{1, 2, 3}})
	if got != "XXX" {
		t.Fatalf("define 内 #break 不应跳出外层循环，期望 'XXX'，got %q", got)
	}
}
