package enjoy_test

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/crazy-airhead/aifei-go/enjoy"
)

// ISSUE-0008：默认共享方法库 isEmpty/notEmpty —— 对照 Java SharedMethodLib。
// Go 取宽松：nil/空集合/空串为空，其余类型视为非空（Java 对非集合类型抛异常）。
// 行内 #if(...)#else...#end 现已可用（无参指令吞文本缺陷由 ISSUE-0019 修复）。
func TestSharedMethodIsEmpty(t *testing.T) {
	engine := enjoy.NewEngine("shared-method-1")
	cases := []struct {
		tpl  string
		data map[string]interface{}
		want string
	}{
		{`#(isEmpty(list))`, map[string]interface{}{"list": []interface{}{}}, "true"},
		{`#(isEmpty(list))`, map[string]interface{}{"list": []interface{}{"a"}}, "false"},
		{`#(isEmpty(m))`, map[string]interface{}{"m": map[string]interface{}{}}, "true"},
		{`#(isEmpty(m))`, map[string]interface{}{"m": map[string]interface{}{"k": "v"}}, "false"},
		{`#(isEmpty(s))`, map[string]interface{}{"s": ""}, "true"},
		{`#(isEmpty(s))`, map[string]interface{}{"s": "x"}, "false"},
		{`#(isEmpty(n))`, map[string]interface{}{"n": nil}, "true"},
		{`#(notEmpty(list))`, map[string]interface{}{"list": []interface{}{"a"}}, "true"},
		{`#(notEmpty(list))`, map[string]interface{}{"list": []interface{}{}}, "false"},
	}
	for _, c := range cases {
		got := engine.GetTemplateByString(c.tpl).RenderToString(c.data)
		if got != c.want {
			t.Errorf("%-22s => %q, want %q", c.tpl, got, c.want)
		}
	}

	// 行内 #if/#else：notEmpty 为真输出 have，否则 empty。
	ml := "#if(notEmpty(list))have#else empty#end"
	if got := engine.GetTemplateByString(ml).RenderToString(map[string]interface{}{"list": []interface{}{}}); got != " empty" {
		t.Errorf("inline if/else empty: got %q", got)
	}
	if got := engine.GetTemplateByString(ml).RenderToString(map[string]interface{}{"list": []interface{}{"a"}}); got != "have" {
		t.Errorf("inline if/else nonempty: got %q", got)
	}
}

// 数值扩展方法（对照 Java Integer/Long/Short/Byte/Float/Double Ext）：覆盖全部整型/浮点 kind。
// 关键：toInt 等通过 numInfo 实现，对 int32/uint 等 kind 同样正确（非 toInt64 的子集）。
func TestNumericExtensionMethod(t *testing.T) {
	engine := enjoy.NewEngine("ext-num-1")
	values := []interface{}{int(7), int8(7), int16(7), int32(7), int64(7),
		uint(7), uint8(7), uint16(7), uint32(7), uint64(7), float64(7.9)}
	for _, v := range values {
		got := engine.GetTemplateByString(`#(v.toInt())`).RenderToString(map[string]interface{}{"v": v})
		if got != "7" {
			t.Errorf("%T toInt => %q, want 7", v, got)
		}
	}
	// toLong / toDouble / toBoolean
	cases := []struct {
		tpl  string
		data map[string]interface{}
		want string
	}{
		{`#(age.toLong())`, map[string]interface{}{"age": 18}, "18"},
		{`#(price.toDouble())`, map[string]interface{}{"price": 9}, "9"},
		{`#(n.toBoolean())`, map[string]interface{}{"n": 0}, "false"},
		{`#(n.toBoolean())`, map[string]interface{}{"n": 5}, "true"},
		{`#(f.toDouble())`, map[string]interface{}{"f": float64(3.5)}, "3.5"},
		{`#(f.toInt())`, map[string]interface{}{"f": float64(3.9)}, "3"}, // float→int 截断
	}
	for _, c := range cases {
		got := engine.GetTemplateByString(c.tpl).RenderToString(c.data)
		if got != c.want {
			t.Errorf("%-22s => %q, want %q", c.tpl, got, c.want)
		}
	}
}

// keepPara 场景（对照 Java StringExt 注释）：表单回传后 age 变成 String，
// `age.toInt() > 18` 无论 age 是 String 还是 int 都成立（行内 #if/#else）。
func TestStringExtensionConversion(t *testing.T) {
	engine := enjoy.NewEngine("ext-str-1")
	cond := "#if(age.toInt() > 18)成年#else未成年#end"
	for _, age := range []interface{}{"20", 20} {
		got := engine.GetTemplateByString(cond).RenderToString(map[string]interface{}{"age": age})
		if got != "成年" {
			t.Fatalf("age=%T(%v) toInt>18: expected '成年', got %q", age, age, got)
		}
	}

	cases := []struct {
		tpl  string
		data map[string]interface{}
		want string
	}{
		{`#(s.toBoolean())`, map[string]interface{}{"s": "true"}, "true"},
		{`#(s.toBoolean())`, map[string]interface{}{"s": "0"}, "false"},
		{`#(s.toLong())`, map[string]interface{}{"s": "123"}, "123"},
		{`#(s.toDouble())`, map[string]interface{}{"s": "3.5"}, "3.5"},
	}
	for _, c := range cases {
		got := engine.GetTemplateByString(c.tpl).RenderToString(c.data)
		if got != c.want {
			t.Errorf("%-20s => %q, want %q", c.tpl, got, c.want)
		}
	}
}

// 原硬编码 string 方法迁入注册体系后仍可用（回归保护）。
func TestStringExtensionLegacyMethods(t *testing.T) {
	engine := enjoy.NewEngine("ext-str-2")
	cases := []struct {
		tpl  string
		data map[string]interface{}
		want string
	}{
		{`#(s.length())`, map[string]interface{}{"s": "hello"}, "5"},
		{`#(s.upper())`, map[string]interface{}{"s": "hi"}, "HI"},
		{`#(s.trim())`, map[string]interface{}{"s": "  x  "}, "x"},
		{`#(s.substring(0, 2))`, map[string]interface{}{"s": "hello"}, "he"},
		{`#(s.isEmpty())`, map[string]interface{}{"s": ""}, "true"},
	}
	for _, c := range cases {
		got := engine.GetTemplateByString(c.tpl).RenderToString(c.data)
		if got != c.want {
			t.Errorf("%-22s => %q, want %q", c.tpl, got, c.want)
		}
	}
}

// 注册自定义共享方法（注册机制验收）。
func TestRegisterCustomSharedMethod(t *testing.T) {
	enjoy.AddSharedMethod("greet", func(args []interface{}) interface{} {
		if len(args) == 0 {
			return "hi"
		}
		return "hi " + fmt.Sprintf("%v", args[0])
	})
	defer enjoy.RemoveSharedMethod("greet")

	engine := enjoy.NewEngine("shared-method-custom")
	got := engine.GetTemplateByString(`#(greet(name))`).
		RenderToString(map[string]interface{}{"name": "Aifei"})
	if got != "hi Aifei" {
		t.Fatalf("custom shared method: expected 'hi Aifei', got %q", got)
	}
}

// 注册自定义扩展方法（注册机制验收）：给 bool 加一个 toggle 扩展。
func TestRegisterCustomExtensionMethod(t *testing.T) {
	enjoy.AddExtensionMethod(reflect.Bool, "toggle", func(target interface{}, _ []interface{}) interface{} {
		if b, ok := target.(bool); ok {
			return !b
		}
		return nil
	})

	engine := enjoy.NewEngine("ext-custom")
	got := engine.GetTemplateByString(`#(flag.toggle())`).
		RenderToString(map[string]interface{}{"flag": false})
	if got != "true" {
		t.Fatalf("custom extension method: expected 'true', got %q", got)
	}
}
