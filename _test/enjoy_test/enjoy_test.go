package enjoy_test

import (
	"strings"
	"testing"

	"github.com/crazy-airhead/aifei-go/enjoy"
)

// renderToString 渲染模板，出错即 t.Fatal。
// enjoy 的 RenderToString 现返回 (string, error)（见 ISSUE-0009 连带改造）；
// 绝大多数测试用例只关心正常渲染结果，统一在此封装 err 处理，保持用例简洁。
// 需要断言「渲染出错」的用例（如 TestForCStyleNotSupported）请直接调用 RenderToString。
func renderToString(t *testing.T, tpl *enjoy.Template, data map[string]interface{}) string {
	t.Helper()
	out, err := tpl.RenderToString0(data)
	if err != nil {
		t.Fatalf("render template: %v", err)
	}
	return out
}

func TestTextTemplate(t *testing.T) {
	engine := enjoy.NewEngine("test1")
	tpl := engine.GetTemplateByString("Hello, World!")
	result := renderToString(t, tpl, nil)
	if result != "Hello, World!" {
		t.Fatalf("expected 'Hello, World!', got '%s'", result)
	}
}

func TestOutputExpr(t *testing.T) {
	engine := enjoy.NewEngine("test2")
	tpl := engine.GetTemplateByString("Hello, #(name)!")
	result := renderToString(t, tpl, map[string]interface{}{"name": "Aifei"})
	if result != "Hello, Aifei!" {
		t.Fatalf("expected 'Hello, Aifei!', got '%s'", result)
	}
}

func TestIfStat(t *testing.T) {
	engine := enjoy.NewEngine("test3")
	tpl := engine.GetTemplateByString("#if (show)visible#end")
	result := renderToString(t, tpl, map[string]interface{}{"show": true})
	if result != "visible" {
		t.Fatalf("expected 'visible', got '%s'", result)
	}

	result = renderToString(t, tpl, map[string]interface{}{"show": false})
	if result != "" {
		t.Fatalf("expected '', got '%s'", result)
	}
}

func TestForStat(t *testing.T) {
	engine := enjoy.NewEngine("test4")
	tpl := engine.GetTemplateByString("#for(item : items)#(item) #end")
	result := renderToString(t, tpl, map[string]interface{}{
		"items": []interface{}{"a", "b", "c"},
	})
	if !strings.Contains(result, "a") || !strings.Contains(result, "c") {
		t.Fatalf("expected to contain a, b, c; got '%s'", result)
	}
}

func TestSetStat(t *testing.T) {
	engine := enjoy.NewEngine("test5")
	tpl := engine.GetTemplateByString("#set(x = 42)#(x)")
	result := renderToString(t, tpl, nil)
	if result != "42" {
		t.Fatalf("expected '42', got '%s'", result)
	}
}

func TestArithExpr(t *testing.T) {
	engine := enjoy.NewEngine("test6")
	tpl := engine.GetTemplateByString("#(1 + 2 * 3)")
	result := renderToString(t, tpl, nil)
	if result != "7" {
		t.Fatalf("expected '7', got '%s'", result)
	}
}

func TestCompareExpr(t *testing.T) {
	engine := enjoy.NewEngine("test7")
	tpl := engine.GetTemplateByString("#if(age > 18)adult#end")
	result := renderToString(t, tpl, map[string]interface{}{"age": 20})
	if result != "adult" {
		t.Fatalf("expected 'adult', got '%s'", result)
	}
	result = renderToString(t, tpl, map[string]interface{}{"age": 10})
	if result != "" {
		t.Fatalf("expected '', got '%s'", result)
	}
}

func TestLogicExpr(t *testing.T) {
	engine := enjoy.NewEngine("test8")
	tpl := engine.GetTemplateByString("#if(a && b)both#elseone#end")
	result := renderToString(t, tpl, map[string]interface{}{"a": true, "b": true})
	if result != "both" {
		t.Fatalf("expected 'both', got '%s'", result)
	}
}

func TestTernaryExpr(t *testing.T) {
	engine := enjoy.NewEngine("test9")
	tpl := engine.GetTemplateByString("#(ok ? 'yes' : 'no')")
	result := renderToString(t, tpl, map[string]interface{}{"ok": true})
	if result != "yes" {
		t.Fatalf("expected 'yes', got '%s'", result)
	}
}

func TestFieldAccess(t *testing.T) {
	engine := enjoy.NewEngine("test10")
	tpl := engine.GetTemplateByString("#(user.name)")
	result := renderToString(t, tpl, map[string]interface{}{
		"user": map[string]interface{}{"name": "james"},
	})
	if result != "james" {
		t.Fatalf("expected 'james', got '%s'", result)
	}
}

func TestArrayAccess(t *testing.T) {
	engine := enjoy.NewEngine("test11")
	tpl := engine.GetTemplateByString("#(items[0])")
	result := renderToString(t, tpl, map[string]interface{}{
		"items": []interface{}{"first", "second"},
	})
	if result != "first" {
		t.Fatalf("expected 'first', got '%s'", result)
	}
}

func TestComment(t *testing.T) {
	engine := enjoy.NewEngine("test12")
	tpl := engine.GetTemplateByString("before### this is a comment\nafter")
	result := renderToString(t, tpl, nil)
	if !strings.Contains(result, "before") || !strings.Contains(result, "after") {
		t.Fatalf("expected before/after, got '%s'", result)
	}
	if strings.Contains(result, "comment") {
		t.Fatalf("comment should be stripped, got '%s'", result)
	}
}

func TestRawBlock(t *testing.T) {
	engine := enjoy.NewEngine("test13")
	tpl := engine.GetTemplateByString("#[[#(not_parsed)]]#")
	result := renderToString(t, tpl, nil)
	if result != "#(not_parsed)" {
		t.Fatalf("expected raw block, got '%s'", result)
	}
}

func TestDefineAndCall(t *testing.T) {
	engine := enjoy.NewEngine("test14")
	tpl := engine.GetTemplateByString("#define(greet(name))Hello #(name)#end#@greet('Aifei')")
	result := renderToString(t, tpl, nil)
	if !strings.Contains(result, "Hello Aifei") {
		t.Fatalf("expected 'Hello Aifei', got '%s'", result)
	}
}

func TestNestedForInIf(t *testing.T) {
	engine := enjoy.NewEngine("test-nested")
	data := map[string]interface{}{
		"show": true,
		"items": []map[string]interface{}{
			{"name": "a"},
			{"name": "b"},
		},
	}
	tpl := engine.GetTemplateByString("#if (show)#for (it : items)#(it.name)#end#end")
	result := renderToString(t, tpl, data)
	if result != "ab" {
		t.Fatalf("expected 'ab', got '%s'", result)
	}
}

func TestSwitchBasic(t *testing.T) {
	engine := enjoy.NewEngine("test-switch")
	tpl := engine.GetTemplateByString(`#switch(x)
#case(1)
one
#case(2)
two
#default
other
#end`)
	if result := renderToString(t, tpl, map[string]interface{}{"x": 1}); result != "one\n" {
		t.Fatalf("expected '\\none\\n', got '%s'", result)
	}
	if result := renderToString(t, tpl, map[string]interface{}{"x": 2}); result != "two\n" {
		t.Fatalf("expected '\\ntwo\\n', got '%s'", result)
	}
	if result := renderToString(t, tpl, map[string]interface{}{"x": 99}); result != "other\n" {
		t.Fatalf("expected '\\nother\\n', got '%s'", result)
	}
}

func TestSwitchMultiValue(t *testing.T) {
	engine := enjoy.NewEngine("test-switch-multi")
	tpl := engine.GetTemplateByString(`#switch(x)
#case(1, 3, 5)
odd
#case(2, 4, 6)
even
#default
other
#end`)
	if result := renderToString(t, tpl, map[string]interface{}{"x": 3}); result != "odd\n" {
		t.Fatalf("expected '\\nodd\\n', got '%s'", result)
	}
	if result := renderToString(t, tpl, map[string]interface{}{"x": 4}); result != "even\n" {
		t.Fatalf("expected '\\neven\\n', got '%s'", result)
	}
	if result := renderToString(t, tpl, map[string]interface{}{"x": 7}); result != "other\n" {
		t.Fatalf("expected '\\nother\\n', got '%s'", result)
	}
}

func TestSwitchString(t *testing.T) {
	engine := enjoy.NewEngine("test-switch-str")
	tpl := engine.GetTemplateByString(`#switch(x)
#case('a')
alpha
#case('b')
beta
#default
other
#end`)
	if result := renderToString(t, tpl, map[string]interface{}{"x": "a"}); result != "alpha\n" {
		t.Fatalf("expected '\\nalpha\\n', got '%s'", result)
	}
	if result := renderToString(t, tpl, map[string]interface{}{"x": "b"}); result != "beta\n" {
		t.Fatalf("expected '\\nbeta\\n', got '%s'", result)
	}
	if result := renderToString(t, tpl, map[string]interface{}{"x": "z"}); result != "other\n" {
		t.Fatalf("expected '\\nother\\n', got '%s'", result)
	}
}

func TestSwitchNoDefault(t *testing.T) {
	engine := enjoy.NewEngine("test-switch-nodef")
	tpl := engine.GetTemplateByString(`#switch(x)
#case(1)
one
#end`)
	if result := renderToString(t, tpl, map[string]interface{}{"x": 1}); result != "one\n" {
		t.Fatalf("expected '\\none\\n', got '%s'", result)
	}
	if result := renderToString(t, tpl, map[string]interface{}{"x": 2}); result != "" {
		t.Fatalf("expected '', got '%s'", result)
	}
}

func TestInclude(t *testing.T) {
	engine := enjoy.NewEngine("test-include")
	engine.SetBaseTemplatePath("testdata")
	engine.SetDevMode(true)

	subTpl := engine.GetTemplate("testdata/_sub.html")
	// testdata 文件缺失时解析报错（现经 RenderToString 的 error 返回），跳过本用例。
	if _, err := subTpl.RenderToString0(nil); err != nil {
		t.Skip("testdata/_sub.html not found, skipping include test")
	}

	tpl := engine.GetTemplate("testdata/_parent.html")
	result := renderToString(t, tpl, map[string]interface{}{"name": "World"})
	if !strings.Contains(result, "Hello") || !strings.Contains(result, "World") {
		t.Fatalf("expected 'Hello World' from include, got '%s'", result)
	}
}

func TestNestedIfInFor(t *testing.T) {
	engine := enjoy.NewEngine("test-nested2")
	data := map[string]interface{}{
		"items": []map[string]interface{}{
			{"val": 1},
			{"val": 0},
			{"val": 2},
		},
	}
	tpl := engine.GetTemplateByString("#for (it : items)#if (it.val > 0)#(it.val)#end#end")
	result := renderToString(t, tpl, data)
	if result != "12" {
		t.Fatalf("expected '12', got '%s'", result)
	}
}

// RenderToString0 是 RenderToString 的 panic-on-error 便捷版本（内部调用 RenderToString）。
func TestRenderToString0(t *testing.T) {
	engine := enjoy.NewEngine("render0")

	// 正常渲染：返回字符串、无 error 概念。
	tpl := engine.GetTemplateByString("Hello, #(name)!")
	if got := tpl.RenderToString(map[string]interface{}{"name": "Aifei"}); got != "Hello, Aifei!" {
		t.Fatalf("RenderToString0 正常渲染: got %q", got)
	}

	// 渲染错误（C 风格 for 语法错）应 panic，而非静默返回。
	badTpl := engine.GetTemplateByString("#for(i=0; i<3; i++)X#end")
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("RenderToString0 渲染错误时应 panic")
		}
	}()
	badTpl.RenderToString(nil)
}
