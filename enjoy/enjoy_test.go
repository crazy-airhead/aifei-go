package enjoy

import (
	"strings"
	"testing"
)

func TestTextTemplate(t *testing.T) {
	engine := NewEngine("test1")
	tpl := engine.GetTemplateByString("Hello, World!")
	result := tpl.RenderToString(nil)
	if result != "Hello, World!" {
		t.Fatalf("expected 'Hello, World!', got '%s'", result)
	}
}

func TestOutputExpr(t *testing.T) {
	engine := NewEngine("test2")
	tpl := engine.GetTemplateByString("Hello, #(name)!")
	result := tpl.RenderToString(map[string]interface{}{"name": "Aifei"})
	if result != "Hello, Aifei!" {
		t.Fatalf("expected 'Hello, Aifei!', got '%s'", result)
	}
}

func TestIfStat(t *testing.T) {
	engine := NewEngine("test3")
	tpl := engine.GetTemplateByString("#if(show)visible#end")
	result := tpl.RenderToString(map[string]interface{}{"show": true})
	if result != "visible" {
		t.Fatalf("expected 'visible', got '%s'", result)
	}

	result = tpl.RenderToString(map[string]interface{}{"show": false})
	if result != "" {
		t.Fatalf("expected '', got '%s'", result)
	}
}

func TestForStat(t *testing.T) {
	engine := NewEngine("test4")
	tpl := engine.GetTemplateByString("#for(item : items)#(item) #end")
	result := tpl.RenderToString(map[string]interface{}{
		"items": []interface{}{"a", "b", "c"},
	})
	if !strings.Contains(result, "a") || !strings.Contains(result, "c") {
		t.Fatalf("expected to contain a, b, c; got '%s'", result)
	}
}

func TestSetStat(t *testing.T) {
	engine := NewEngine("test5")
	tpl := engine.GetTemplateByString("#set(x = 42)#(x)")
	result := tpl.RenderToString(nil)
	if result != "42" {
		t.Fatalf("expected '42', got '%s'", result)
	}
}

func TestArithExpr(t *testing.T) {
	engine := NewEngine("test6")
	tpl := engine.GetTemplateByString("#(1 + 2 * 3)")
	result := tpl.RenderToString(nil)
	if result != "7" {
		t.Fatalf("expected '7', got '%s'", result)
	}
}

func TestCompareExpr(t *testing.T) {
	engine := NewEngine("test7")
	tpl := engine.GetTemplateByString("#if(age > 18)adult#end")
	result := tpl.RenderToString(map[string]interface{}{"age": 20})
	if result != "adult" {
		t.Fatalf("expected 'adult', got '%s'", result)
	}
	result = tpl.RenderToString(map[string]interface{}{"age": 10})
	if result != "" {
		t.Fatalf("expected '', got '%s'", result)
	}
}

func TestLogicExpr(t *testing.T) {
	engine := NewEngine("test8")
	tpl := engine.GetTemplateByString("#if(a && b)both#elseone#end")
	result := tpl.RenderToString(map[string]interface{}{"a": true, "b": true})
	if result != "both" {
		t.Fatalf("expected 'both', got '%s'", result)
	}
}

func TestTernaryExpr(t *testing.T) {
	engine := NewEngine("test9")
	tpl := engine.GetTemplateByString("#(ok ? 'yes' : 'no')")
	result := tpl.RenderToString(map[string]interface{}{"ok": true})
	if result != "yes" {
		t.Fatalf("expected 'yes', got '%s'", result)
	}
}

func TestFieldAccess(t *testing.T) {
	engine := NewEngine("test10")
	tpl := engine.GetTemplateByString("#(user.name)")
	result := tpl.RenderToString(map[string]interface{}{
		"user": map[string]interface{}{"name": "james"},
	})
	if result != "james" {
		t.Fatalf("expected 'james', got '%s'", result)
	}
}

func TestArrayAccess(t *testing.T) {
	engine := NewEngine("test11")
	tpl := engine.GetTemplateByString("#(items[0])")
	result := tpl.RenderToString(map[string]interface{}{
		"items": []interface{}{"first", "second"},
	})
	if result != "first" {
		t.Fatalf("expected 'first', got '%s'", result)
	}
}

func TestComment(t *testing.T) {
	engine := NewEngine("test12")
	tpl := engine.GetTemplateByString("before### this is a comment\nafter")
	result := tpl.RenderToString(nil)
	if !strings.Contains(result, "before") || !strings.Contains(result, "after") {
		t.Fatalf("expected before/after, got '%s'", result)
	}
	if strings.Contains(result, "comment") {
		t.Fatalf("comment should be stripped, got '%s'", result)
	}
}

func TestRawBlock(t *testing.T) {
	engine := NewEngine("test13")
	tpl := engine.GetTemplateByString("#[[#(not_parsed)]]#")
	result := tpl.RenderToString(nil)
	if result != "#(not_parsed)" {
		t.Fatalf("expected raw block, got '%s'", result)
	}
}

func TestDefineAndCall(t *testing.T) {
	engine := NewEngine("test14")
	tpl := engine.GetTemplateByString("#define(greet(name))Hello #(name)#end#call greet('Aifei')")
	result := tpl.RenderToString(nil)
	if !strings.Contains(result, "Hello Aifei") {
		t.Fatalf("expected 'Hello Aifei', got '%s'", result)
	}
}
