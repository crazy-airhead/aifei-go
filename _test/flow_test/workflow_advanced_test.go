package flow_test

import (
	"testing"

	flow "github.com/crazy-airhead/aifei-go/flow"
	"github.com/crazy-airhead/aifei-go/flow/workflow"
)

// start -> A -> X(exclusive) -> B(when v>0) / C(when v<=0) -> end
func buildGatewayGraph(t *testing.T) *flow.Graph {
	t.Helper()
	g, err := flow.Create("gwf", func(s *flow.GraphSpec) {
		s.AddStart("s").LinkAdd("a")
		s.AddActivity("a").LinkAdd("x")
		s.AddExclusive("x").
			LinkAddConfig("b", func(l *flow.LinkSpec) { l.When("v > 0") }).
			LinkAddConfig("c", func(l *flow.LinkSpec) { l.When("v <= 0") })
		s.AddActivity("b").LinkAdd("e")
		s.AddActivity("c").LinkAdd("e")
		s.AddEnd("e")
	})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// TestWorkflow_ForwardThroughExclusiveGateway: forwarding A routes through the
// exclusive gateway to the matching branch (exercises forwardHandle's gateway branch).
func TestWorkflow_ForwardThroughExclusiveGateway(t *testing.T) {
	g := buildGatewayGraph(t)
	engine := flow.NewEngine(flow.NewSimpleDriver())
	exec := workflow.NewExecutor(engine, workflow.NewBlockStateController(), workflow.NewInMemoryStateRepository())

	// v > 0 -> B
	ctx := flow.NewContext("g1")
	ctx.Put("v", 5)
	if task, _ := exec.ClaimTask(g, ctx); task == nil || task.NodeID() != "a" {
		t.Fatal("expected to claim a")
	}
	if err := exec.SubmitTask(g, g.GetNode("a"), workflow.ActionForward, ctx); err != nil {
		t.Fatal(err)
	}
	if task, _ := exec.ClaimTask(g, ctx); task == nil || task.NodeID() != "b" {
		t.Errorf("v>0 routed to %v, want b", task)
	}

	// v <= 0 -> C
	ctx2 := flow.NewContext("g2")
	ctx2.Put("v", -1)
	if task, _ := exec.ClaimTask(g, ctx2); task == nil || task.NodeID() != "a" {
		t.Fatal("expected to claim a")
	}
	if err := exec.SubmitTask(g, g.GetNode("a"), workflow.ActionForward, ctx2); err != nil {
		t.Fatal(err)
	}
	if task, _ := exec.ClaimTask(g, ctx2); task == nil || task.NodeID() != "c" {
		t.Errorf("v<=0 routed to %v, want c", task)
	}
}

// TestWorkflow_ForwardJump: jump forward to a target, auto-completing intermediates.
// start -> A -> B -> C -> end. Claim A, FORWARD_JUMP to C: B is auto-completed, C parked.
func TestWorkflow_ForwardJump(t *testing.T) {
	g, err := flow.Create("fj", func(s *flow.GraphSpec) {
		s.AddStart("s").LinkAdd("a")
		s.AddActivity("a").LinkAdd("b")
		s.AddActivity("b").LinkAdd("c")
		s.AddActivity("c").LinkAdd("e")
		s.AddEnd("e")
	})
	if err != nil {
		t.Fatal(err)
	}
	engine := flow.NewEngine(flow.NewSimpleDriver())
	exec := workflow.NewExecutor(engine, workflow.NewBlockStateController(), workflow.NewInMemoryStateRepository())
	ctx := flow.NewContext("fj1")

	if task, _ := exec.ClaimTask(g, ctx); task == nil || task.NodeID() != "a" {
		t.Fatal("expected to claim a")
	}
	// jump forward to c (skipping/Completing b)
	if err := exec.SubmitTask(g, g.GetNode("c"), workflow.ActionForwardJump, ctx); err != nil {
		t.Fatal(err)
	}
	if got := exec.GetState(g.GetNode("b"), ctx); got != workflow.TaskStateCompleted {
		t.Errorf("after forward-jump, b = %v, want COMPLETED (auto-completed)", got)
	}
	if got := exec.GetState(g.GetNode("c"), ctx); got != workflow.TaskStateWaiting {
		t.Errorf("after forward-jump, c = %v, want WAITING (parked target)", got)
	}
	if task, _ := exec.ClaimTask(g, ctx); task == nil || task.NodeID() != "c" {
		t.Errorf("after forward-jump, claim = %v, want c", task)
	}
}

// TestWorkflow_BackJump: jump back to a target. start -> A -> B -> C; advance to C,
// then BACK_JUMP to A: B,C reverted, A parked.
func TestWorkflow_BackJump(t *testing.T) {
	g, err := flow.Create("bj", func(s *flow.GraphSpec) {
		s.AddStart("s").LinkAdd("a")
		s.AddActivity("a").LinkAdd("b")
		s.AddActivity("b").LinkAdd("c")
		s.AddActivity("c").LinkAdd("e")
		s.AddEnd("e")
	})
	if err != nil {
		t.Fatal(err)
	}
	engine := flow.NewEngine(flow.NewSimpleDriver())
	exec := workflow.NewExecutor(engine, workflow.NewBlockStateController(), workflow.NewInMemoryStateRepository())
	ctx := flow.NewContext("bj1")

	// advance to c
	for _, id := range []string{"a", "b"} {
		if task, _ := exec.ClaimTask(g, ctx); task == nil || task.NodeID() != id {
			t.Fatalf("expected to claim %s, got %v", id, task)
		}
		if err := exec.SubmitTask(g, g.GetNode(id), workflow.ActionForward, ctx); err != nil {
			t.Fatal(err)
		}
	}
	if task, _ := exec.ClaimTask(g, ctx); task == nil || task.NodeID() != "c" {
		t.Fatalf("expected to claim c, got %v", task)
	}

	// jump back to a
	if err := exec.SubmitTask(g, g.GetNode("a"), workflow.ActionBackJump, ctx); err != nil {
		t.Fatal(err)
	}
	if got := exec.GetState(g.GetNode("a"), ctx); got != workflow.TaskStateWaiting {
		t.Errorf("after back-jump, a = %v, want WAITING", got)
	}
	if got := exec.GetState(g.GetNode("c"), ctx); got != workflow.TaskStateUnknown {
		t.Errorf("after back-jump, c = %v, want UNKNOWN (reverted)", got)
	}
	if task, _ := exec.ClaimTask(g, ctx); task == nil || task.NodeID() != "a" {
		t.Errorf("after back-jump, claim = %v, want a", task)
	}
}
