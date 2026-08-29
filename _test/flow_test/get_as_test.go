package flow_test

import (
	"testing"

	"github.com/crazy-airhead/aifei-go/flow"
)

func TestContextGetAs(t *testing.T) {
	ctx := flow.NewContext("inst-1")
	ctx.Put("count", 42)
	ctx.Put("name", "demo")

	if got, ok := flow.GetAs[int](ctx, "count"); !ok || got != 42 {
		t.Errorf("GetAs[int](count) = (%d, %v)", got, ok)
	}
	if got, ok := flow.GetAs[string](ctx, "name"); !ok || got != "demo" {
		t.Errorf("GetAs[string](name) = (%q, %v)", got, ok)
	}

	// Absent and type-mismatched variables: zero T with ok=false
	if got, ok := flow.GetAs[int](ctx, "absent"); ok || got != 0 {
		t.Errorf("GetAs[int](absent) = (%d, %v), want (0, false)", got, ok)
	}
	if got, ok := flow.GetAs[string](ctx, "count"); ok || got != "" {
		t.Errorf("GetAs[string](count) = (%q, %v), want (\"\", false)", got, ok)
	}
}

func TestMetaAs(t *testing.T) {
	spec := flow.NewGraphSpec("g1")
	spec.MetaPut("env", "prod")
	spec.AddStart("a").MetaPut("retries", 3)
	g, err := flow.NewGraph(spec)
	if err != nil {
		t.Fatal(err)
	}

	if got, ok := g.MetaAs[string]("env"); !ok || got != "prod" {
		t.Errorf("Graph.MetaAs[string](env) = (%q, %v)", got, ok)
	}
	node := g.GetNode("a")
	if node == nil {
		t.Fatal("node a missing")
	}
	if got, ok := node.MetaAs[int]("retries"); !ok || got != 3 {
		t.Errorf("Node.MetaAs[int](retries) = (%d, %v)", got, ok)
	}
	if _, ok := node.MetaAs[string]("retries"); ok {
		t.Error("Node.MetaAs[string](retries) should be !ok on type mismatch")
	}
}

func TestTemporaryStackAs(t *testing.T) {
	spec := flow.NewGraphSpec("g1")
	spec.AddStart("a")
	spec.AddEnd("z")
	g, err := flow.NewGraph(spec)
	if err != nil {
		t.Fatal(err)
	}
	tmp := flow.NewExchanger(g, nil, nil, flow.NewContext(), 0).Temporary()

	tmp.StackPush("g1", "join", 3)
	if got, ok := tmp.StackPeekAs[int]("g1", "join"); !ok || got != 3 {
		t.Errorf("StackPeekAs[int] = (%d, %v)", got, ok)
	}
	if got, ok := tmp.StackPopAs[int]("g1", "join"); !ok || got != 3 {
		t.Errorf("StackPopAs[int] = (%d, %v)", got, ok)
	}
	// Empty stack: zero T with ok=false, like the nil-returning originals
	if got, ok := tmp.StackPopAs[int]("g1", "join"); ok || got != 0 {
		t.Errorf("StackPopAs[int] on empty = (%d, %v), want (0, false)", got, ok)
	}
	if _, ok := tmp.StackPeekAs[string]("g1", "join"); ok {
		t.Error("StackPeekAs on empty should be !ok")
	}
}

func TestComponentAs(t *testing.T) {
	c := flow.NewMapContainer()
	type emailTask struct{ To string }
	task := &emailTask{To: "a@x"}
	c.PutComponent("email", task)

	got, ok := flow.ComponentAs[*emailTask](c, "email")
	if !ok || got.To != "a@x" {
		t.Errorf("ComponentAs = (%v, %v)", got, ok)
	}
	if _, ok := flow.ComponentAs[*emailTask](c, "absent"); ok {
		t.Error("ComponentAs(absent) should be !ok")
	}
}
