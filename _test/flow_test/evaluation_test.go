package flow_test

import (
	"fmt"
	"testing"

	flow "github.com/crazy-airhead/aifei-go/flow"
)

// compStub is a comparable TaskComponent (no func fields) for container identity tests.
type compStub struct{ name string }

func (compStub) Run(flow.Context, *flow.Node) error { return nil }

func TestEnjoyEvaluationCondition(t *testing.T) {
	ev := flow.NewEnjoyEvaluation()
	ctx := flow.NewContext()
	ctx.Put("amount", 150)
	cases := []struct {
		expr string
		want bool
	}{
		{"amount >= 100", true},
		{"amount > 200", false},
		{"amount > 100 && amount <= 500", true},
		{"amount > 500", false},
	}
	for _, c := range cases {
		got, err := ev.RunCondition(ctx, c.expr)
		if err != nil {
			t.Errorf("RunCondition(%q) err: %v", c.expr, err)
			continue
		}
		if got != c.want {
			t.Errorf("RunCondition(%q) = %v, want %v", c.expr, got, c.want)
		}
	}
}

func TestEnjoyEvaluationConditionTruthiness(t *testing.T) {
	ev := flow.NewEnjoyEvaluation()
	ctx := flow.NewContext()
	if got, _ := ev.RunCondition(ctx, "nope"); got {
		t.Error("nil should be false")
	}
}

func TestEnjoyEvaluationTaskAssignment(t *testing.T) {
	ev := flow.NewEnjoyEvaluation()
	ctx := flow.NewContext()
	if err := ev.RunTask(ctx, "score = 100"); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(ctx.Get("score")); got != "100" {
		t.Errorf("score = %v, want 100", ctx.Get("score"))
	}
}

func TestEnjoyEvaluationTaskMultiStatement(t *testing.T) {
	ev := flow.NewEnjoyEvaluation()
	ctx := flow.NewContext()
	if err := ev.RunTask(ctx, "a = 1; b = 2;"); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(ctx.Get("a")) != "1" || fmt.Sprint(ctx.Get("b")) != "2" {
		t.Errorf("a=%v b=%v", ctx.Get("a"), ctx.Get("b"))
	}
}

func TestEnjoyEvaluationTaskMapFunctionCall(t *testing.T) {
	ev := flow.NewEnjoyEvaluation()
	called := false
	order := map[string]any{}
	order["setScore"] = func(v any) any { called = true; order["score"] = v; return nil }
	ctx := flow.NewContext()
	ctx.Put("order", order)
	if err := ev.RunTask(ctx, "order.setScore(100)"); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("method not called")
	}
	if got := fmt.Sprint(order["score"]); got != "100" {
		t.Errorf("method call side-effect: score=%v, want 100", order["score"])
	}
}

func TestEnjoyEvaluationCachesAndRepeats(t *testing.T) {
	ev := flow.NewEnjoyEvaluation()
	ctx := flow.NewContext()
	ctx.Put("x", 1)
	for i := 0; i < 3; i++ {
		if got, _ := ev.RunCondition(ctx, "x > 0"); !got {
			t.Fatal("x > 0 should hold each call")
		}
	}
}

func TestMapContainer(t *testing.T) {
	c := flow.NewMapContainer()
	if c.GetComponent("missing") != nil {
		t.Error("missing should be nil")
	}
	comp := compStub{"DoA"}
	c.PutComponent("DoA", comp)
	if got := c.GetComponent("DoA"); got != comp {
		t.Error("GetComponent mismatch")
	}
	c.RemoveComponent("DoA")
	if c.GetComponent("DoA") != nil {
		t.Error("not removed")
	}
}
