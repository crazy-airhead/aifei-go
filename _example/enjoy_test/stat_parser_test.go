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
		got := tpl.RenderToString(c.data)
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// 条件为真时，returnIf 之后的 #(expr) / 文本都不应再渲染。
func TestReturnIfSkipsFollowing(t *testing.T) {
	engine := enjoy.NewEngine("issue0003-follow")
	tpl := engine.GetTemplateByString("A#returnIf(ok)B#(value)C")

	if got := tpl.RenderToString(map[string]interface{}{"ok": true, "value": "X"}); got != "A" {
		t.Errorf("ok=true: got %q, want %q (应提前返回)", got, "A")
	}
	if got := tpl.RenderToString(map[string]interface{}{"ok": false, "value": "X"}); got != "ABXC" {
		t.Errorf("ok=false: got %q, want %q (应继续渲染)", got, "ABXC")
	}
}

// 空参数应解析失败（对照 Java ReturnIf.java 抛 ParseException）。
// 经公开 API 验证：#returnIf() 解析为 errorStat，渲染输出错误标记而非静默返回。
func TestReturnIfEmptyParam(t *testing.T) {
	engine := enjoy.NewEngine("issue0003-empty")
	for _, src := range []string{"#returnIf()", "#returnIf(   )"} {
		got := engine.GetTemplateByString(src).RenderToString(nil)
		if !strings.Contains(got, "template error") {
			t.Errorf("%q 空参数应解析报错，实际输出 %q", src, got)
		}
	}
}

// ISSUE-0004：#for(x : collection) 此前只接受 slice/array，map 被当成单个元素，
// 循环体只执行一次且取到 map 本身。修复后 map 迭代产生 key/value entry 项。
func TestForIterateMap(t *testing.T) {
	engine := enjoy.NewEngine("issue0004-map")
	tpl := engine.GetTemplateByString("#for(entry : m)#(entry.key)=#(entry.value);#end")

	got := tpl.RenderToString(map[string]interface{}{
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
	got := tpl.RenderToString(map[string]interface{}{
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
	if got := tpl.RenderToString(map[string]interface{}{"ps": &slice}); got != "pq" {
		t.Fatalf("ptr-to-slice 应迭代为 pq，got %q", got)
	}

	// 空 map：循环体不应执行（迭代 0 次）。模板以 #end 收尾，避免「#end 后文本被吃一字」的解析细节干扰。
	tpl2 := engine.GetTemplateByString(">#for(entry : em)X#end")
	if got := tpl2.RenderToString(map[string]interface{}{"em": map[string]interface{}{}}); got != ">" {
		t.Fatalf("空 map 应迭代 0 次（仅保留前导文本），got %q", got)
	}
}

// 非集合单对象应自动包成单元素列表（对照 Java SingleObjectIterator）。
func TestForIterateSingleObject(t *testing.T) {
	engine := enjoy.NewEngine("issue0004-single")
	tpl := engine.GetTemplateByString("#for(x : n)[#(x)/#(index)]#end")
	got := tpl.RenderToString(map[string]interface{}{"n": 42})
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
