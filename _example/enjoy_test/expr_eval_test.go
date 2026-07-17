package enjoy_test

import (
	"testing"

	"github.com/crazy-airhead/aifei-go/enjoy"
)

func intLit(v int64) *enjoy.ConstExpr     { return &enjoy.ConstExpr{Type: "int", Value: v} }
func floatLit(v float64) *enjoy.ConstExpr { return &enjoy.ConstExpr{Type: "float", Value: v} }
func strLit(v string) *enjoy.ConstExpr    { return &enjoy.ConstExpr{Type: "string", Value: v} }

func evalExpr(e enjoy.Expr) interface{} {
	return e.Eval(enjoy.NewScope(nil), enjoy.NewCtrl())
}

// 整数运算保留整型（对照 Java Arith: int+int→int）。
func TestArithIntPreserved(t *testing.T) {
	cases := []struct {
		op   string
		l, r int64
		want int64
	}{
		{"+", 1, 2, 3},
		{"-", 10, 3, 7},
		{"*", 4, 5, 20},
		{"/", 10, 3, 3}, // 整数除法：3，非 3.333
		{"%", 7, 3, 1},
	}
	for _, c := range cases {
		got := evalExpr(&enjoy.ArithExpr{Op: c.op, Left: intLit(c.l), Right: intLit(c.r)})
		if got != c.want {
			t.Errorf("%d %s %d = %v (%T), want %d", c.l, c.op, c.r, got, got, c.want)
		}
		if _, ok := got.(int64); !ok {
			t.Errorf("%d %s %d 返回类型 %T, 应为 int64", c.l, c.op, c.r, got)
		}
	}
}

// 任一侧为浮点 → 提升为 float64。
func TestArithFloatPromotion(t *testing.T) {
	got := evalExpr(&enjoy.ArithExpr{Op: "+", Left: intLit(1), Right: floatLit(1.5)})
	if got != 2.5 {
		t.Errorf("1 + 1.5 = %v, want 2.5", got)
	}
	if _, ok := got.(float64); !ok {
		t.Errorf("1 + 1.5 返回类型 %T, 应为 float64", got)
	}

	got = evalExpr(&enjoy.ArithExpr{Op: "/", Left: floatLit(10), Right: intLit(3)})
	if got != 10.0/3.0 {
		t.Errorf("10.0 / 3 = %v, want %v", got, 10.0/3.0)
	}
}

// 字符串拼接：任一侧为 string（对照 Java Arith: String.valueOf 拼接）。
func TestArithStringConcat(t *testing.T) {
	cases := []struct {
		name string
		expr enjoy.Expr
		want string
	}{
		{"str+str", &enjoy.ArithExpr{Op: "+", Left: strLit("a"), Right: strLit("b")}, "ab"},
		{"str+int", &enjoy.ArithExpr{Op: "+", Left: strLit("v"), Right: intLit(1)}, "v1"},
		{"int+str", &enjoy.ArithExpr{Op: "+", Left: intLit(1), Right: strLit("v")}, "1v"},
		{"str+float", &enjoy.ArithExpr{Op: "+", Left: strLit("x"), Right: floatLit(1.5)}, "x1.5"},
		{"str+nil", &enjoy.ArithExpr{Op: "+", Left: strLit("n"), Right: &enjoy.ConstExpr{Type: "null"}}, "n"},
	}
	for _, c := range cases {
		got := evalExpr(c.expr)
		if got != c.want {
			t.Errorf("%s = %v, want %q", c.name, got, c.want)
		}
	}
}

// 负号保留整型。
func TestArithNeg(t *testing.T) {
	got := evalExpr(&enjoy.ArithExpr{Op: "neg", Left: intLit(3)})
	if got != int64(-3) {
		t.Errorf("-(3) = %v (%T), want -3 (int64)", got, got)
	}
}

// 除零返回 0 而非 panic。
func TestArithDivZero(t *testing.T) {
	if got := evalExpr(&enjoy.ArithExpr{Op: "/", Left: intLit(5), Right: intLit(0)}); got != int64(0) {
		t.Errorf("5 / 0 = %v, want 0", got)
	}
	if got := evalExpr(&enjoy.ArithExpr{Op: "/", Left: floatLit(5), Right: floatLit(0)}); got != float64(0) {
		t.Errorf("5.0 / 0 = %v, want 0", got)
	}
}

// ISSUE-0002 端到端：模板渲染的三个复现场景。
func TestArithIssue0002(t *testing.T) {
	engine := enjoy.NewEngine("issue0002")
	cases := []struct {
		tpl  string
		want string
	}{
		{"#(1 + 2)", "3"},            // 整数加法：3（非 3.0）
		{"#(1 + 2 * 3)", "7"},        // 混合优先级
		{"#(10 / 3)", "3"},           // 整数除法：3（非 3.333）
		{"#(\"a\" + \"b\")", "ab"},   // 字符串拼接：ab（非 0）
		{"#(\"id=\" + 42)", "id=42"}, // 拼 URL/字符串场景
		{"#(1.5 + 1)", "2.5"},        // 浮点提升
	}
	for _, c := range cases {
		got := engine.GetTemplateByString(c.tpl).RenderToString(nil)
		if got != c.want {
			t.Errorf("%-20s => %q, want %q", c.tpl, got, c.want)
		}
	}
}
