package flow_test

import (
	"testing"

	flow "github.com/crazy-airhead/aifei-go/flow"
	"github.com/crazy-airhead/aifei-go/flow/workflow"
)

// TestWorkflow_Back: forward to b, then BACK at b reverts b and re-parks a as WAITING.
func TestWorkflow_Back(t *testing.T) {
	g, err := flow.Create("back", func(s *flow.GraphSpec) {
		s.AddStart("s").LinkAdd("a")
		s.AddActivity("a").LinkAdd("b")
		s.AddActivity("b").LinkAdd("e")
		s.AddEnd("e")
	})
	if err != nil {
		t.Fatal(err)
	}
	engine := flow.NewEngine(flow.NewSimpleDriver())
	exec := workflow.NewExecutor(engine, workflow.NewBlockStateController(), workflow.NewInMemoryStateRepository())
	ctx := flow.NewContext("bk")

	// advance to b
	if task, _ := exec.ClaimTask(g, ctx); task == nil || task.NodeID() != "a" {
		t.Fatal("expected to claim a")
	}
	if err := exec.SubmitTask(g, g.GetNode("a"), workflow.ActionForward, ctx); err != nil {
		t.Fatal(err)
	}
	if task, _ := exec.ClaimTask(g, ctx); task == nil || task.NodeID() != "b" {
		t.Fatal("expected to claim b after forwarding a")
	}

	// back at b reverts b and its predecessor a (states removed -> UNKNOWN).
	if err := exec.SubmitTask(g, g.GetNode("b"), workflow.ActionBack, ctx); err != nil {
		t.Fatal(err)
	}
	if got := exec.GetState(g.GetNode("b"), ctx); got != workflow.TaskStateUnknown {
		t.Errorf("after back, b state = %v, want UNKNOWN", got)
	}
	if got := exec.GetState(g.GetNode("a"), ctx); got != workflow.TaskStateUnknown {
		t.Errorf("after back, a state = %v, want UNKNOWN (reverted)", got)
	}

	// claim again -> a (re-parked as WAITING)
	if task, _ := exec.ClaimTask(g, ctx); task == nil || task.NodeID() != "a" || task.State() != workflow.TaskStateWaiting {
		t.Errorf("after back, claim = %v, want a/WAITING", task)
	}
}

// TestWorkflow_Restart: after restart, all states clear and the first task is claimable again.
func TestWorkflow_Restart(t *testing.T) {
	g, err := flow.Create("restart", func(s *flow.GraphSpec) {
		s.AddStart("s").LinkAdd("a")
		s.AddActivity("a").LinkAdd("b")
		s.AddActivity("b").LinkAdd("e")
		s.AddEnd("e")
	})
	if err != nil {
		t.Fatal(err)
	}
	engine := flow.NewEngine(flow.NewSimpleDriver())
	exec := workflow.NewExecutor(engine, workflow.NewBlockStateController(), workflow.NewInMemoryStateRepository())
	ctx := flow.NewContext("rs")

	if err := exec.SubmitTask(g, g.GetNode("a"), workflow.ActionForward, ctx); err != nil {
		t.Fatal(err)
	}
	if exec.GetState(g.GetNode("a"), ctx) != workflow.TaskStateCompleted {
		t.Fatal("a should be COMPLETED before restart")
	}

	if err := exec.SubmitTask(g, g.GetNode("a"), workflow.ActionRestart, ctx); err != nil {
		t.Fatal(err)
	}
	if got := exec.GetState(g.GetNode("a"), ctx); got != workflow.TaskStateUnknown {
		t.Errorf("after restart, a = %v, want UNKNOWN", got)
	}
	if task, _ := exec.ClaimTask(g, ctx); task == nil || task.NodeID() != "a" {
		t.Errorf("after restart, claim = %v, want a", task)
	}
}
