package enjoy_test

import (
	"strings"
	"testing"

	"github.com/crazy-airhead/aifei-go/enjoy"
)

// upperUtil 是一个挂载方法的共享对象，验证 sharedObject 的主要用途：
// 注入工具对象后模板里能 `#(tool.Method(args))` 反射调方法。
type upperUtil struct{}

func (upperUtil) Up(s string) string { return strings.ToUpper(s) }

// AddSharedObject 注册的共享对象在模板里能被取到（对照 ISSUE-0005 复现步骤）。
func TestSharedObjectFallback(t *testing.T) {
	engine := enjoy.NewEngine("shared-obj-1")
	engine.AddSharedObject("greeting", "hello")
	engine.AddSharedObject("count", 42)

	if got := engine.GetTemplateByString("#(greeting)").RenderToString(nil); got != "hello" {
		t.Fatalf("shared string: expected 'hello', got '%s'", got)
	}
	if got := engine.GetTemplateByString("#(count)").RenderToString(nil); got != "42" {
		t.Fatalf("shared int: expected '42', got '%s'", got)
	}
}

// 未注册的标识符仍取不到（回退命中后才返回，不凭空造值）。
func TestSharedObjectMissStillEmpty(t *testing.T) {
	engine := enjoy.NewEngine("shared-obj-2")
	engine.AddSharedObject("greeting", "hello")

	if got := engine.GetTemplateByString("[#(missing)]").RenderToString(nil); got != "[]" {
		t.Fatalf("unregistered ident: expected '[]', got '%s'", got)
	}
}

// 局部 data 同名 key 优先于共享对象（data 链先于 sharedObjectMap 命中）。
func TestSharedObjectShadowedByData(t *testing.T) {
	engine := enjoy.NewEngine("shared-obj-3")
	engine.AddSharedObject("greeting", "hello")

	got := engine.GetTemplateByString("#(greeting)").RenderToString(map[string]interface{}{"greeting": "hi"})
	if got != "hi" {
		t.Fatalf("data should shadow shared: expected 'hi', got '%s'", got)
	}
}

// for 循环体（NewChild 子作用域）也能访问共享对象。
func TestSharedObjectInForBody(t *testing.T) {
	engine := enjoy.NewEngine("shared-obj-4")
	engine.AddSharedObject("sep", "|")

	tpl := engine.GetTemplateByString("#for(i : items)#(i)#(sep)#end")
	got := tpl.RenderToString(map[string]interface{}{"items": []interface{}{"a", "b", "c"}})
	if got != "a|b|c|" {
		t.Fatalf("shared object in for body: expected 'a|b|c|', got '%s'", got)
	}
}

// NewScopeWithShared + Get 直接回退共享对象，GetSharedObject 也能取（对照 Java Scope.getSharedObject）。
func TestSharedObjectScopeAPI(t *testing.T) {
	shared := map[string]interface{}{"now": "2026-07-18"}
	root := enjoy.NewScopeWithShared(map[string]interface{}{"local": "L"}, shared)

	if got := root.Get("now"); got != "2026-07-18" {
		t.Fatalf("Get fallback to shared: expected '2026-07-18', got '%v'", got)
	}
	if got := root.Get("local"); got != "L" {
		t.Fatalf("Get local data: expected 'L', got '%v'", got)
	}
	if got := root.GetSharedObject("now"); got != "2026-07-18" {
		t.Fatalf("GetSharedObject: expected '2026-07-18', got '%v'", got)
	}

	child := root.NewChild()
	if got := child.Get("now"); got != "2026-07-18" {
		t.Fatalf("child Get shared: expected '2026-07-18', got '%v'", got)
	}
	if got := child.GetSharedObject("now"); got != "2026-07-18" {
		t.Fatalf("child GetSharedObject: expected '2026-07-18', got '%v'", got)
	}

	// 普通顶层 scope（无共享对象）不应回退出值。
	plain := enjoy.NewScope(map[string]interface{}{"local": "L"})
	if got := plain.Get("now"); got != nil {
		t.Fatalf("plain scope Get unregistered: expected nil, got '%v'", got)
	}
}

// sharedObject 的主要用途：注入挂方法的工具对象，模板里 `#(tool.Method(args))`
// 反射调方法。修复前 Get 回退缺失 → obj 取到 nil → 方法调用直接返回 nil。
func TestSharedObjectMethodCall(t *testing.T) {
	tool := upperUtil{}

	// 通过 sharedObject 注入
	engine := enjoy.NewEngine("shared-obj-5")
	engine.AddSharedObject("tool", tool)
	got := engine.GetTemplateByString(`#(tool.Up("hi"))`).RenderToString(nil)
	if got != "HI" {
		t.Fatalf("shared object method call: expected 'HI', got '%s'", got)
	}

	// 对照：同一对象通过 data 注入，方法调用同样可达（取值能力上 data 与 sharedObject 等价）
	engine2 := enjoy.NewEngine("shared-obj-6")
	got2 := engine2.GetTemplateByString(`#(tool.Up("hi"))`).RenderToString(map[string]interface{}{"tool": tool})
	if got2 != "HI" {
		t.Fatalf("data object method call: expected 'HI', got '%s'", got2)
	}
}
