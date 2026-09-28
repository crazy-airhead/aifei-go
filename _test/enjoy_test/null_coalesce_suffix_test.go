package enjoy_test

import (
	"strings"
	"testing"

	"github.com/crazy-airhead/aifei-go/enjoy"
)

// `??` 右操作数省略（后缀）写法，对照 Java ExprParser.atom() 对终结符 token
// 返回 null 的机制与 NullSafe 允许 right 为 null 的求值（#(name??) / #(name ?? )）。

// 后缀基本形态：取值非 null 原样输出，为 null 输出空。
func TestNullCoalesceSuffix(t *testing.T) {
	cases := []struct {
		src  string
		data map[string]interface{}
		want string
	}{
		// 紧贴与带空格两种写法等价
		{"[#(name??)]", map[string]interface{}{"name": "aifei"}, "[aifei]"},
		{"[#(name ?? )]", map[string]interface{}{"name": "aifei"}, "[aifei]"},
		{"[#(name??)]", nil, "[]"},
		{"[#(name ?? )]", nil, "[]"},
		// 级联取值中缀省略：#(a.b ??)
		{"[#(a.b ??)]", map[string]interface{}{}, "[]"},
		// 链式 + 末尾省略：a ?? b ?? → b 非 null 取 b
		{"[#(a ?? b ?? )]", map[string]interface{}{"a": nil, "b": "B"}, "[B]"},
		{"[#(a ?? b ?? )]", nil, "[]"},
		// 与三目混排（对照 Java atom() 的 QUESTION/COLON 注释用例）
		{"[#(c ?? ? 't' : 'f')]", nil, "[f]"},          // c ?? 后跟 ?
		{"[#(c ?? ? 't' : 'f')]", map[string]interface{}{"c": true}, "[t]"},
		{"[#(c ? a ?? : 'f')]", nil, "[f]"},            // a ?? 后跟 :
		{"[#(c ? a ?? : 'f')]", map[string]interface{}{"c": true}, "[]"},
		// 与逻辑/比较运算符混排（对照 Java atom() 的 AND/OR/EQUAL 注释用例）
		{"[#((x ?? ) && y)]", map[string]interface{}{"y": true}, "[false]"},
		{"[#((x ?? ) == y)]", map[string]interface{}{"y": nil}, "[true]"},
	}
	for _, c := range cases {
		engine := enjoy.NewEngine("nc-suffix")
		tpl := engine.GetTemplateByString(c.src)
		if got := renderToString(t, tpl, c.data); got != c.want {
			t.Errorf("%q with %v = %q, want %q", c.src, c.data, got, c.want)
		}
	}
}

// 后缀写法在数组/Map/区间/赋值等复合上下文中同样成立
// （对照 Java atom() 的 COMMA/RANGE/RBRACK/RBRACE 注释用例）。
func TestNullCoalesceSuffixInComposite(t *testing.T) {
	cases := []struct {
		src  string
		data map[string]interface{}
		want string
	}{
		// 数组元素以 ?? 结尾，逗号终止右操作数
		{"#set(arr = [a ??, 'x'])#(arr[0] ?? 'zero')#(arr[1])", nil, "zerox"},
		// Map 值以 ?? 结尾，} 终止
		{"#set(m = {k: v ?? })#(m.k ?? 'd')", nil, "d"},
		// 区间起点以 ?? 结尾，.. 终止（区间须在 [] 内：对照 Java "[start ?? .. end]"）
		{"#set(r = [1 ?? .. 3])#for(i : r)#(i)#end", nil, "123"},
		// 赋值右值以 ?? 结尾
		{"#set(x = a ?? )#(x ?? 'd')", nil, "d"},
		// 括号包裹的后缀：#( (a.b ??) )
		{"[#((a.b ??))]", nil, "[]"},
	}
	for _, c := range cases {
		engine := enjoy.NewEngine("nc-composite")
		tpl := engine.GetTemplateByString(c.src)
		if got := renderToString(t, tpl, c.data); got != c.want {
			t.Errorf("%q with %v = %q, want %q", c.src, c.data, got, c.want)
		}
	}
}

// 表达式首尾空白不再越界：旧 NewExprLexer 修剪 input 却用未修剪长度，
// `#(name ?? )` / `#( name )` 会 index out of range 被 recover 成模板错误。
func TestExprLeadingTrailingSpace(t *testing.T) {
	cases := []struct {
		src  string
		data map[string]interface{}
		want string
	}{
		{"[#( name )]", map[string]interface{}{"name": "aifei"}, "[aifei]"},
		{"[#( name ?? )]", nil, "[]"},
		{"[#(name ?? )]", nil, "[]"},
		{"[#( 1 + 2 )]", nil, "[3]"},
	}
	for _, c := range cases {
		engine := enjoy.NewEngine("nc-space")
		tpl := engine.GetTemplateByString(c.src)
		if got := renderToString(t, tpl, c.data); got != c.want {
			t.Errorf("%q = %q, want %q", c.src, got, c.want)
		}
	}
}

// 空操作数给出干净解析错误（不再 unexpected token 越界/panic），
// 对照 Java Arith/Logic/Ternary/NullSafe/Output 构造器的 null 校验。
func TestBlankOperandErrors(t *testing.T) {
	cases := []struct {
		src       string
		wantError string
	}{
		{"#()", "the expression can not be blank"},
		{"#( a + )", `the target of "+" operator on the right side can not be blank`},
		{"#( && b )", `the target of "&&" operator on the left side can not be blank`},
		{"#( ! )", `the target of "!" operator on the right side can not be blank`},
		{"#( ? a : b )", "the parameter of ternary expression can not be blank"},
		{"#( c ? a : )", "the parameter of ternary expression can not be blank"},
		{"#set(x = )", "the right side of assignment can not be blank"},
		{"#set(arr = [a, ])", "array element can not be blank"},
		{"#set(m = {k: })", "map value can not be blank"},
	}
	for _, c := range cases {
		engine := enjoy.NewEngine("nc-blank")
		tpl := engine.GetTemplateByString(c.src)
		_, err := tpl.RenderToString0(nil)
		if err == nil {
			t.Errorf("%q: expected error containing %q, got nil", c.src, c.wantError)
			continue
		}
		if !strings.Contains(err.Error(), c.wantError) {
			t.Errorf("%q: error %q does not contain %q", c.src, err.Error(), c.wantError)
		}
		if strings.Contains(err.Error(), "runtime error") {
			t.Errorf("%q: error should be a clean parse error, got %q", c.src, err.Error())
		}
	}
}
