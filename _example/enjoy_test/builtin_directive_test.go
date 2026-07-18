package enjoy_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/crazy-airhead/aifei-go/enjoy"
)

func TestDateDirective(t *testing.T) {
	engine := enjoy.NewEngine("dir-date")
	ts := time.Date(2026, 7, 18, 14, 30, 45, 0, time.UTC)

	tpl := engine.GetTemplateByString(`#date(ts, "yyyy-MM-dd HH:mm:ss")`)
	if got := renderToString(t, tpl, map[string]interface{}{"ts": ts}); got != "2026-07-18 14:30:45" {
		t.Fatalf("expected '2026-07-18 14:30:45', got %q", got)
	}

	// 默认 pattern（yyyy-MM-dd HH:mm），传变量
	tpl2 := engine.GetTemplateByString(`#date(ts)`)
	if got := renderToString(t, tpl2, map[string]interface{}{"ts": ts}); got != "2026-07-18 14:30" {
		t.Fatalf("expected '2026-07-18 14:30', got %q", got)
	}

	// 无参：输出当前时间，仅校验非空且长度合理
	tpl3 := engine.GetTemplateByString(`#date()`)
	got := renderToString(t, tpl3, nil)
	if len(got) < 10 {
		t.Fatalf("expected current date string, got %q", got)
	}
}

func TestDateDirectiveCustomDefault(t *testing.T) {
	engine := enjoy.NewEngine("dir-date-cfg")
	engine.GetConfig().SetDatePattern("yyyy/MM/dd")
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	tpl := engine.GetTemplateByString(`#date(ts)`)
	if got := renderToString(t, tpl, map[string]interface{}{"ts": ts}); got != "2026/01/02" {
		t.Fatalf("expected '2026/01/02', got %q", got)
	}
}

func TestEscapeDirective(t *testing.T) {
	engine := enjoy.NewEngine("dir-escape")
	tpl := engine.GetTemplateByString(`#escape(html)`)
	data := map[string]interface{}{"html": `<a href="x" class='c'>A & B</a>`}
	want := `&lt;a href=&quot;x&quot; class=&#39;c&#39;&gt;A &amp; B&lt;/a&gt;`
	if got := renderToString(t, tpl, data); got != want {
		t.Fatalf("expected escaped html, got %q", got)
	}

	// 数字原样输出（不转义）
	tpl2 := engine.GetTemplateByString(`#escape(n)`)
	if got := renderToString(t, tpl2, map[string]interface{}{"n": 42}); got != "42" {
		t.Fatalf("expected '42', got %q", got)
	}
}

func TestNumberDirective(t *testing.T) {
	engine := enjoy.NewEngine("dir-number")
	cases := []struct {
		tpl  string
		data map[string]interface{}
		want string
	}{
		{`#number(3.1415926, "#.##")`, nil, "3.14"},
		{`#number(0.9518, "#.##%")`, nil, "95.18%"},
		{`#number(1299792458, ",###")`, nil, "1,299,792,458"},
		{`#number(3, "0.00")`, nil, "3.00"},
		{`#number(300000, "光速为每秒 ,### 公里。")`, nil, "光速为每秒 300,000 公里。"},
		{`#number(n, "#.##")`, map[string]interface{}{"n": 1.5}, "1.5"},
		{`#number(n)`, map[string]interface{}{"n": 1234}, "1234"},
	}
	for i, c := range cases {
		tpl := engine.GetTemplateByString(c.tpl)
		got := renderToString(t, tpl, c.data)
		if got != c.want {
			t.Fatalf("case %d: %#v -> want %q, got %q", i, c.tpl, c.want, got)
		}
	}
}

func TestRandomDirective(t *testing.T) {
	engine := enjoy.NewEngine("dir-random")
	tpl := engine.GetTemplateByString(`#random`)
	got := renderToString(t, tpl, nil)
	if _, err := strconv.Atoi(got); err != nil {
		t.Fatalf("expected an int from #random, got %q (err: %v)", got, err)
	}
}

func TestStringDirective(t *testing.T) {
	engine := enjoy.NewEngine("dir-string")
	// #end 后的内容需另起一行：词法器 #end 会吞掉同行紧跟的文本作为其 para。
	tpl := engine.GetTemplateByString("#string(msg)Hello #(name)!#end\n[#(msg)]")
	got := renderToString(t, tpl, map[string]interface{}{"name": "Aifei"})
	// 指令体被捕获为 msg 变量（不直接输出），随后 #(msg) 输出。
	if !strings.Contains(got, "[Hello Aifei!]") {
		t.Fatalf("expected body captured into msg then output, got %q", got)
	}
}

func TestRenderDirective(t *testing.T) {
	engine := enjoy.NewEngine("dir-render")
	engine.SetBaseTemplatePath("testdata")
	engine.SetDevMode(true)

	// 带赋值参数
	tpl := engine.GetTemplateByString(`#render("_hot.html", title = "news")`)
	if got := renderToString(t, tpl, nil); got != "Hot news!" {
		t.Fatalf("expected 'Hot news!', got %q", got)
	}

	// 动态文件名（表达式）
	tpl2 := engine.GetTemplateByString(`#set(f = "_hot.html")#render(f, title = item)`)
	if got := renderToString(t, tpl2, map[string]interface{}{"item": "prj"}); got != "Hot prj!" {
		t.Fatalf("expected 'Hot prj!', got %q", got)
	}
}

func TestCallDynamicDirective(t *testing.T) {
	engine := enjoy.NewEngine("dir-call")

	// Java CallDirective 动态形态（注册指令）：函数名为表达式 #call(fn, "Sam")
	tpl := engine.GetTemplateByString(`#define(greet(name))Hi #(name)#end#set(fn = "greet")#call(fn, "Sam")`)
	if got := renderToString(t, tpl, nil); !strings.Contains(got, "Hi Sam") {
		t.Fatalf("expected dynamic #call to greet 'Sam', got %q", got)
	}

	// nullSafe 形态：函数不存在时跳过（首个参数为 true）
	tpl2 := engine.GetTemplateByString(`[#call(true, missing, "x")]`)
	if got := renderToString(t, tpl2, nil); got != "[]" {
		t.Fatalf("expected nullSafe call to skip silently -> '[]', got %q", got)
	}
}

func TestCallAtSugarDirective(t *testing.T) {
	engine := enjoy.NewEngine("dir-atcall")

	// #@name(args) 静态糖（函数名为字面量，函数名可含数字）
	tpl := engine.GetTemplateByString(`#define(g2(name))[#(name)]#end#@g2("A")`)
	if got := renderToString(t, tpl, nil); got != "[A]" {
		t.Fatalf("expected #@g2 -> '[A]', got %q", got)
	}

	// #@ 后的内容不被重扫（旧词法器缺陷的回归测试）
	tpl2 := engine.GetTemplateByString(`#define(f())OK#end#@f()|tail`)
	if got := renderToString(t, tpl2, nil); got != "OK|tail" {
		t.Fatalf("expected #@f() not to rescan, got %q", got)
	}

	// #@name?(args) nullSafe：函数不存在时跳过（对照 Java callIfDefined）
	tpl3 := engine.GetTemplateByString(`[#@missing?("x")]`)
	if got := renderToString(t, tpl3, nil); got != "[]" {
		t.Fatalf("expected #@missing? to skip silently -> '[]', got %q", got)
	}
}

// directive 参数错误（#date 传 3 个参数）在解析期 panic，现经 compileSource 的 recover
// 转为 error（RenderToString 返回 error，不再打崩 GetTemplateByString 调用方）。
func TestDirectiveParsePanicBecomesError(t *testing.T) {
	engine := enjoy.NewEngine("dir-parse-err")
	tpl := engine.GetTemplateByString(`#date(ts, "yyyy-MM-dd", "extra")`)
	if _, err := tpl.RenderToString(map[string]interface{}{"ts": time.Now()}); err == nil {
		t.Fatal("#date 参数过多应解析报错并返回 error，而非渲染成功或打崩")
	}
}
