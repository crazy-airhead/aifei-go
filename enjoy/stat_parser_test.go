package enjoy

import "testing"

// ISSUE-0003：#returnIf(cond) 的 cond 是「返回条件」，仅当求值为真才提前返回；
// 为假时继续渲染后续内容（此前实现把它当无条件 return，恒返回）。
func TestReturnIfConditional(t *testing.T) {
	engine := NewEngine("issue0003")
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
	engine := NewEngine("issue0003-follow")
	tpl := engine.GetTemplateByString("A#returnIf(ok)B#(value)C")

	if got := tpl.RenderToString(map[string]interface{}{"ok": true, "value": "X"}); got != "A" {
		t.Errorf("ok=true: got %q, want %q (应提前返回)", got, "A")
	}
	if got := tpl.RenderToString(map[string]interface{}{"ok": false, "value": "X"}); got != "ABXC" {
		t.Errorf("ok=false: got %q, want %q (应继续渲染)", got, "ABXC")
	}
}

// 空参数应报错（对照 Java ReturnIf.java 抛 ParseException）。
func TestReturnIfEmptyParam(t *testing.T) {
	if _, err := parseReturnIfStat(""); err == nil {
		t.Errorf(`parseReturnIfStat("") 应报错，实际 nil`)
	}
	if _, err := parseReturnIfStat("   "); err == nil {
		t.Errorf(`parseReturnIfStat("   ") 应报错，实际 nil`)
	}
}
