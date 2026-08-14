package flow_test

import (
	"testing"

	flow "github.com/crazy-airhead/aifei-go/flow"
)

// TestEngine_SubgraphInterrupt: a "#sub" task runs a subgraph that does NOT end (no
// end node); RunGraph then interrupts the parent branch, so the node after the
// subgraph task is not reached.
func TestEngine_SubgraphInterrupt(t *testing.T) {
	// sub has no end node: start -> S (terminal activity)
	sub, err := flow.Create("sub", func(s *flow.GraphSpec) {
		s.AddStart("ss").LinkAdd("s")
		s.AddActivity("s").Task("@SubTask")
	})
	if err != nil {
		t.Fatal(err)
	}
	// main: start -> M(#sub) -> N(@N) -> end
	main, err := flow.Create("main", func(s *flow.GraphSpec) {
		s.AddStart("s").LinkAdd("m")
		s.AddActivity("m").Task("#sub").LinkAdd("n")
		s.AddActivity("n").Task("@N").LinkAdd("e")
		s.AddEnd("e")
	})
	if err != nil {
		t.Fatal(err)
	}
	c := flow.NewMapContainer()
	c.PutComponent("SubTask", flow.TaskFunc(func(ctx flow.Context, _ *flow.Node) error { ctx.Put("sub_ran", true); return nil }))
	c.PutComponent("N", flow.TaskFunc(func(ctx flow.Context, _ *flow.Node) error { ctx.Put("n_ran", true); return nil }))
	engine := flow.NewEngine(flow.NewSimpleDriver(flow.WithContainer(c)))
	engine.Load(sub)
	engine.Load(main)

	ctx := flow.NewContext("i")
	if err := engine.EvalByID("main", ctx); err != nil {
		t.Fatal(err)
	}
	if ctx.Get("sub_ran") != true {
		t.Error("subgraph task should have run")
	}
	if ctx.Get("n_ran") != nil {
		t.Error("node after subgraph task should NOT run (branch interrupted)")
	}
}

// TestEngine_LoopLiteralList: the loop-demo2 pattern — loop with a literal $in list
// and a terminal body, iterating once per item.
func TestEngine_LoopLiteralList(t *testing.T) {
	g, err := flow.Create("looplit", func(s *flow.GraphSpec) {
		s.AddStart("s").LinkAdd("l")
		s.AddLoop("l").MetaPut("$for", "item").MetaPut("$in", []any{"a", "b", "c"}).LinkAdd("body")
		s.AddActivity("body").Task("ran = ran + 1").LinkAdd("e")
		s.AddEnd("e")
	})
	if err != nil {
		t.Fatal(err)
	}
	engine := flow.NewEngine()
	ctx := flow.NewContext()
	ctx.Put("ran", 0)
	if err := engine.Eval(g, ctx); err != nil {
		t.Fatal(err)
	}
	if fmtInt(ctx.Get("ran")) != 3 {
		t.Errorf("loop body ran %v times, want 3", ctx.Get("ran"))
	}
}
