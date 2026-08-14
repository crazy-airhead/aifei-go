package flow_test

import (
	"strings"
	"testing"

	flow "github.com/crazy-airhead/aifei-go/flow"
)

// newEngineWithContainer builds an engine whose default SimpleDriver uses a fresh
// MapContainer (for @component tests).
func newEngineWithContainer() (*flow.Engine, *flow.MapContainer) {
	c := flow.NewMapContainer()
	e := flow.NewEngine(flow.NewSimpleDriver(flow.WithContainer(c)))
	return e, c
}

// TestEngine_SequentialExpressionTasks: start -> a -> b -> end, expression tasks.
func TestEngine_SequentialExpressionTasks(t *testing.T) {
	g, err := flow.Create("seq", func(s *flow.GraphSpec) {
		s.AddStart("s").LinkAdd("a")
		s.AddActivity("a").Task("ran_a = 1").LinkAdd("b")
		s.AddActivity("b").Task("ran_b = 1").LinkAdd("e")
		s.AddEnd("e")
	})
	if err != nil {
		t.Fatal(err)
	}
	e := flow.NewEngine()
	ctx := flow.NewContext("seq")
	if err := e.Eval(g, ctx); err != nil {
		t.Fatal(err)
	}
	if fmtInt(ctx.Get("ran_a")) != 1 || fmtInt(ctx.Get("ran_b")) != 1 {
		t.Errorf("ran_a=%v ran_b=%v", ctx.Get("ran_a"), ctx.Get("ran_b"))
	}
}

// TestEngine_ComponentDispatch: @DoA resolves to a TaskComponent in the container.
func TestEngine_ComponentDispatch(t *testing.T) {
	g, _ := flow.Create("comp", func(s *flow.GraphSpec) {
		s.AddStart("s").LinkAdd("a")
		s.AddActivity("a").Task("@DoA").LinkAdd("e")
		s.AddEnd("e")
	})
	e, c := newEngineWithContainer()
	c.PutComponent("DoA", flow.TaskFunc(func(ctx flow.Context, _ *flow.Node) error {
		ctx.Put("a_done", true)
		return nil
	}))
	ctx := flow.NewContext()
	if err := e.Eval(g, ctx); err != nil {
		t.Fatal(err)
	}
	if ctx.Get("a_done") != true {
		t.Error("component task did not run")
	}
}

// TestEngine_StopHalts: a node calls ctx.Stop(); the next node must not run.
func TestEngine_StopHalts(t *testing.T) {
	g, _ := flow.Create("stop", func(s *flow.GraphSpec) {
		s.AddStart("s").LinkAdd("a")
		s.AddActivity("a").Task("@Stop").LinkAdd("b")
		s.AddActivity("b").Task("ran_b = 1").LinkAdd("e")
		s.AddEnd("e")
	})
	e, c := newEngineWithContainer()
	c.PutComponent("Stop", flow.TaskFunc(func(ctx flow.Context, _ *flow.Node) error {
		ctx.Stop()
		return nil
	}))
	ctx := flow.NewContext()
	if err := e.Eval(g, ctx); err != nil {
		t.Fatal(err)
	}
	if ctx.Get("ran_b") != nil {
		t.Error("b should not run after stop")
	}
	if !ctx.IsStopped() {
		t.Error("ctx should be stopped")
	}
}

// TestEngine_ExclusiveGateway: first matching branch wins, else default.
func TestEngine_ExclusiveGateway(t *testing.T) {
	g, _ := flow.Create("ex", func(s *flow.GraphSpec) {
		s.AddStart("s").LinkAdd("x")
		s.AddExclusive("x").
			LinkAddConfig("hi", func(l *flow.LinkSpec) { l.When("v > 10") }).
			LinkAddConfig("lo", func(l *flow.LinkSpec) { l.When("v > 0") }).
			LinkAdd("def") // empty-when default branch
		s.AddEnd("hi")
		s.AddEnd("lo")
		s.AddEnd("def")
	})
	e := flow.NewEngine()

	ctx := flow.NewContext()
	ctx.Put("v", 20)
	if err := e.Eval(g, ctx); err != nil {
		t.Fatal(err)
	}
	if got := ctx.Trace().LastNode(g).ID(); got != "hi" {
		t.Errorf("v=20 ended at %s, want hi", got)
	}

	ctx2 := flow.NewContext()
	ctx2.Put("v", 5)
	_ = e.Eval(g, ctx2)
	if got := ctx2.Trace().LastNode(g).ID(); got != "lo" {
		t.Errorf("v=5 ended at %s, want lo", got)
	}

	ctx3 := flow.NewContext() // v unset -> no branch -> default
	_ = e.Eval(g, ctx3)
	if got := ctx3.Trace().LastNode(g).ID(); got != "def" {
		t.Errorf("v unset ended at %s, want def", got)
	}
}

// TestEngine_ParallelForkJoin: split into 2, both run, join once, end once.
func TestEngine_ParallelForkJoin(t *testing.T) {
	var ran []string
	g, _ := flow.Create("par", func(s *flow.GraphSpec) {
		s.AddStart("s").LinkAdd("split")
		s.AddParallel("split").LinkAdd("a").LinkAdd("b")
		s.AddActivity("a").Task("@A").LinkAdd("join")
		s.AddActivity("b").Task("@B").LinkAdd("join")
		s.AddParallel("join").LinkAdd("e")
		s.AddEnd("e")
	})
	e, c := newEngineWithContainer()
	c.PutComponent("A", flow.TaskFunc(func(flow.Context, *flow.Node) error { ran = append(ran, "a"); return nil }))
	c.PutComponent("B", flow.TaskFunc(func(flow.Context, *flow.Node) error { ran = append(ran, "b"); return nil }))
	if err := e.Eval(g, flow.NewContext()); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(ran, ",")
	if joined != "a,b" && joined != "b,a" {
		t.Errorf("branches ran %v, want a,b (both once)", ran)
	}
	if got := g.GetID(); got != "par" {
		_ = got
	}
}

// TestEngine_InclusiveGateway: multiple true branches run, then join.
func TestEngine_InclusiveGateway(t *testing.T) {
	var ran []string
	g, _ := flow.Create("inc", func(s *flow.GraphSpec) {
		s.AddStart("s").LinkAdd("x")
		s.AddInclusive("x").
			LinkAddConfig("a", func(l *flow.LinkSpec) { l.When("v > 0") }).
			LinkAddConfig("b", func(l *flow.LinkSpec) { l.When("v > 10") })
		s.AddActivity("a").Task("@A").LinkAdd("join")
		s.AddActivity("b").Task("@B").LinkAdd("join")
		s.AddInclusive("join").LinkAdd("e")
		s.AddEnd("e")
	})
	e, c := newEngineWithContainer()
	c.PutComponent("A", flow.TaskFunc(func(flow.Context, *flow.Node) error { ran = append(ran, "a"); return nil }))
	c.PutComponent("B", flow.TaskFunc(func(flow.Context, *flow.Node) error { ran = append(ran, "b"); return nil }))
	ctx := flow.NewContext()
	ctx.Put("v", 20) // both branches true
	if err := e.Eval(g, ctx); err != nil {
		t.Fatal(err)
	}
	if !contains(ran, "a") || !contains(ran, "b") {
		t.Errorf("inclusive branches ran %v, want both a and b", ran)
	}
	if got := ctx.Trace().LastNode(g).ID(); got != "e" {
		t.Errorf("ended at %s, want e (join passed)", got)
	}
}

// TestEngine_LoopGateway: $for/$in iterates a slice, body runs N times.
func TestEngine_LoopGateway(t *testing.T) {
	// The loop node drives iteration internally (while-loop over $in); its single
	// outgoing link is the body, which flows on to the end.
	g, err := flow.Create("loop", func(s *flow.GraphSpec) {
		s.AddStart("s").LinkAdd("l")
		s.AddLoop("l").MetaPut("$for", "item").MetaPut("$in", "items").LinkAdd("body")
		s.AddActivity("body").Task("ran = ran + 1").LinkAdd("e")
		s.AddEnd("e")
	})
	if err != nil {
		t.Fatal(err)
	}
	e := flow.NewEngine()
	ctx := flow.NewContext()
	ctx.Put("items", []any{1, 2, 3})
	ctx.Put("ran", 0)
	if err := e.Eval(g, ctx); err != nil {
		t.Fatal(err)
	}
	if fmtInt(ctx.Get("ran")) != 3 {
		t.Errorf("loop body ran %v times, want 3", ctx.Get("ran"))
	}
}

// TestEngine_Interceptor: interceptFlow + onNodeStart/onNodeEnd fire.
func TestEngine_Interceptor(t *testing.T) {
	var log []string
	ic := flow.InterceptorFunc(func(inv *flow.Invocation) error {
		log = append(log, "flow:start")
		err := inv.Invoke()
		log = append(log, "flow:end")
		return err
	})
	// wrap to add per-node callbacks
	ic2 := nodeInterceptor{
		start: func(_ flow.Context, n *flow.Node) { log = append(log, "start:"+n.ID()) },
		end:   func(_ flow.Context, n *flow.Node) { log = append(log, "end:"+n.ID()) },
		wrap:  ic,
	}
	g, _ := flow.Create("int", func(s *flow.GraphSpec) {
		s.AddStart("s").LinkAdd("a")
		s.AddActivity("a").LinkAdd("e")
		s.AddEnd("e")
	})
	e := flow.NewEngine()
	e.AddInterceptor(ic2)
	if err := e.Eval(g, flow.NewContext()); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(log, ",")
	if !strings.Contains(joined, "flow:start") || !strings.Contains(joined, "start:s") || !strings.Contains(joined, "end:a") {
		t.Errorf("interceptor log = %s", joined)
	}
}

// TestEngine_StepLimit: with a step budget, execution stops early.
func TestEngine_StepLimit(t *testing.T) {
	g, _ := flow.Create("steps", func(s *flow.GraphSpec) {
		s.AddStart("s").LinkAdd("a")
		s.AddActivity("a").LinkAdd("b")
		s.AddActivity("b").LinkAdd("c")
		s.AddActivity("c").LinkAdd("e")
		s.AddEnd("e")
	})
	e := flow.NewEngine()
	ctx := flow.NewContext()
	// 2 steps: s(1), a(2) -> stop before b
	if err := e.EvalWithSteps(g, 2, ctx); err != nil {
		t.Fatal(err)
	}
	if !ctx.IsStopped() {
		t.Error("should be stopped by step limit")
	}
	last := ctx.Trace().LastNode(g).ID()
	// recordNode fires before the step check, so b (the next node) is recorded as
	// the stop point after 2 executed steps (s, a).
	if last != "b" {
		t.Errorf("stopped at %s, want b (after 2 steps)", last)
	}
}

// TestEngine_SnapshotResume: run to a conditional stop, snapshot, restore, resume.
// The stop must be data-dependent so it does not re-fire when the node re-runs on
// resume (the resume point's task executes again).
func TestEngine_SnapshotResume(t *testing.T) {
	g, _ := flow.Create("snap", func(s *flow.GraphSpec) {
		s.AddStart("s").LinkAdd("a")
		s.AddActivity("a").Task("@GateA").LinkAdd("b")
		s.AddActivity("b").Task("@B").LinkAdd("e")
		s.AddEnd("e")
	})
	e, c := newEngineWithContainer()
	c.PutComponent("GateA", flow.TaskFunc(func(ctx flow.Context, _ *flow.Node) error {
		if ctx.Get("ready") != true { // wait until externally signalled
			ctx.Stop()
		}
		return nil
	}))
	c.PutComponent("B", flow.TaskFunc(func(ctx flow.Context, _ *flow.Node) error { ctx.Put("b_done", true); return nil }))

	// first run: stops at a (ready unset)
	ctx := flow.NewContext("snap1")
	if err := e.Eval(g, ctx); err != nil {
		t.Fatal(err)
	}
	snapshot := ctx.ToJSON()
	if ctx.Get("b_done") != nil {
		t.Fatal("b should not have run yet")
	}

	// restore, signal ready, and resume
	ctx2, err := flow.ContextFromJSON(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	ctx2.Put("ready", true)
	if err := e.Eval(g, ctx2); err != nil {
		t.Fatal(err)
	}
	if ctx2.Get("b_done") != true {
		t.Error("b should run after resume")
	}
	if got := ctx2.Trace().LastNode(g).ID(); got != "e" {
		t.Errorf("ended at %s, want e", got)
	}
}

// nodeInterceptor is a test Interceptor combining flow-wrap + per-node callbacks.
type nodeInterceptor struct {
	start, end func(flow.Context, *flow.Node)
	wrap       flow.Interceptor
}

func (n nodeInterceptor) InterceptFlow(inv *flow.Invocation) error { return n.wrap.InterceptFlow(inv) }
func (n nodeInterceptor) OnNodeStart(c flow.Context, node *flow.Node) { n.start(c, node) }
func (n nodeInterceptor) OnNodeEnd(c flow.Context, node *flow.Node)   { n.end(c, node) }

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// TestEngine_Subgraph: a "#sub" task runs another loaded graph, then the main flow
// continues.
func TestEngine_Subgraph(t *testing.T) {
	sub, err := flow.Create("sub", func(s *flow.GraphSpec) {
		s.AddStart("ss").LinkAdd("sa")
		s.AddActivity("sa").Task("sub_ran = 1").LinkAdd("se")
		s.AddEnd("se")
	})
	if err != nil {
		t.Fatal(err)
	}
	main, err := flow.Create("main", func(s *flow.GraphSpec) {
		s.AddStart("s").LinkAdd("a")
		s.AddActivity("a").Task("#sub").LinkAdd("e")
		s.AddEnd("e")
	})
	if err != nil {
		t.Fatal(err)
	}
	e := flow.NewEngine()
	e.Load(sub)
	e.Load(main)
	ctx := flow.NewContext()
	if err := e.EvalByID("main", ctx); err != nil {
		t.Fatal(err)
	}
	if fmtInt(ctx.Get("sub_ran")) != 1 {
		t.Error("subgraph did not run")
	}
	if got := ctx.Trace().LastNode(main).ID(); got != "e" {
		t.Errorf("main ended at %s, want e", got)
	}
}

// TestEngine_MetaScript: a "$key" task resolves its script from graph meta.
func TestEngine_MetaScript(t *testing.T) {
	g, err := flow.Create("meta", func(s *flow.GraphSpec) {
		s.MetaPut("mytask", "x = 42")
		s.AddStart("s").LinkAdd("a")
		s.AddActivity("a").Task("$mytask").LinkAdd("e")
		s.AddEnd("e")
	})
	if err != nil {
		t.Fatal(err)
	}
	e := flow.NewEngine()
	ctx := flow.NewContext()
	if err := e.Eval(g, ctx); err != nil {
		t.Fatal(err)
	}
	if fmtInt(ctx.Get("x")) != 42 {
		t.Errorf("x = %v, want 42 (resolved from $mytask meta)", ctx.Get("x"))
	}
}

func fmtInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return -1
}
