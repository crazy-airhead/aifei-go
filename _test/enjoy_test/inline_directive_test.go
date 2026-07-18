package enjoy_test

import (
	"testing"

	"github.com/crazy-airhead/aifei-go/enjoy"
)

// 既有词法缺陷回归：scanDirective 的无参分支原本会把指令名后到行尾/# 的文本当作 para 吞掉，
// 导致「同行」的 #else<文本> / #end<文本> / #default<文本> 把后续文本吃进指令 token，
// 使 #if(c)A#else B#end 在条件为假时输出空而非 " B"。修复后无参指令（#else/#end/#break/
// #continue/#default）不消费尾部文本，行内文本作为独立 TokText 输出；多行写法行为不变。
func TestInlineElseNotSwallowed(t *testing.T) {
	engine := enjoy.NewEngine("inline-dir-1")
	cases := []struct {
		tpl  string
		data map[string]interface{}
		want string
	}{
		// 行内 #else：条件为假走 else 分支，尾部文本（含前导空格）保留
		{`#if(1>2)A#else B#end`, nil, " B"},
		{`#if(2>1)A#else B#end`, nil, "A"},
		// 中文紧跟 #else（非标识符字符，#else 仍被正确识别）
		{`#if(false)A#else非空#end`, nil, "非空"},
		{`#if(true)A#else非空#end`, nil, "A"},
		// #end 后行内文本保留（此前被吞）
		{`#if(true)X#end tail`, nil, "X tail"},
		// 行内 #default
		{`#switch(9)#case(1)one#default two#end`, nil, " two"},
		{`#switch(1)#case(1)one#default two#end`, nil, "one"},
		// 结合 0008 的 notEmpty：行内 #if/#else 现在可用
		{`#if(notEmpty(x))Y#else N#end`, map[string]interface{}{"x": []interface{}{1}}, "Y"},
		{`#if(notEmpty(x))Y#else N#end`, map[string]interface{}{"x": []interface{}{}}, " N"},
	}
	for _, c := range cases {
		got := renderToString(t, engine.GetTemplateByString(c.tpl), c.data)
		if got != c.want {
			t.Errorf("%-34s => %q, want %q", c.tpl, got, c.want)
		}
	}
}

// 多行写法（#else/#end 独占一行）行为不受影响：行首指令仍吃掉尾随换行，避免空行。
func TestMultilineElseUnchanged(t *testing.T) {
	engine := enjoy.NewEngine("inline-dir-2")
	tpl := "#if(notEmpty(x))\nhave\n#else\nempty\n#end"
	if got := renderToString(t, engine.GetTemplateByString(tpl), map[string]interface{}{"x": []interface{}{}}); got != "empty\n" {
		t.Errorf("multiline false-branch: got %q, want %q", got, "empty\n")
	}
	if got := renderToString(t, engine.GetTemplateByString(tpl), map[string]interface{}{"x": []interface{}{1}}); got != "have\n" {
		t.Errorf("multiline true-branch: got %q, want %q", got, "have\n")
	}
}

// #break 行内文本不再被吞（此前 #break<文本> 会吃掉文本）。这里用 #continue 验证：
// #continue 后的同一块内文本不再丢失，循环继续。#break/#continue 的循环语义本身不变。
func TestInlineBreakContinueText(t *testing.T) {
	engine := enjoy.NewEngine("inline-dir-3")
	// #for 内 #if 命中后 #continue：跳过本次后续 #(i)，但「#end」后的文本在循环体每次都输出
	tpl := "#for(i : x)#if(i == 2)#continue skip#end#(i)#end"
	got := renderToString(t, engine.GetTemplateByString(tpl), map[string]interface{}{"x": []interface{}{1, 2, 3}})
	// i=1 → "1"；i=2 → continue（#(2) 被跳过）；i=3 → "3"
	if got != "13" {
		t.Errorf("inline #continue text: got %q, want %q", got, "13")
	}
}
