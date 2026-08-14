package flow_test

import (
	"testing"

	flow "github.com/crazy-airhead/aifei-go/flow"
	"github.com/crazy-airhead/aifei-go/flow/workflow"
)

// newWorkflow builds an engine (with a component container) + executor over a
// BlockStateController and an InMemoryStateRepository.
func newWorkflow(t *testing.T, controller workflow.StateController) (*flow.Engine, *workflow.Executor, *flow.MapContainer) {
	t.Helper()
	c := flow.NewMapContainer()
	engine := flow.NewEngine(flow.NewSimpleDriver(flow.WithContainer(c)))
	repo := workflow.NewInMemoryStateRepository()
	exec := workflow.NewExecutor(engine, controller, repo)
	return engine, exec, c
}

// TestWorkflow_ClaimForwardFlow: start -> A -> B -> end. Claim parks the current
// activity as WAITING; submit FORWARD runs its task and advances.
func TestWorkflow_ClaimForwardFlow(t *testing.T) {
	g, err := flow.Create("wf", func(s *flow.GraphSpec) {
		s.AddStart("s").LinkAdd("a")
		s.AddActivity("a").Task("@StepA").LinkAdd("b")
		s.AddActivity("b").Task("@StepB").LinkAdd("e")
		s.AddEnd("e")
	})
	if err != nil {
		t.Fatal(err)
	}
	_, exec, c := newWorkflow(t, workflow.NewBlockStateController())
	c.PutComponent("StepA", flow.TaskFunc(func(ctx flow.Context, _ *flow.Node) error { ctx.Put("a_done", true); return nil }))
	c.PutComponent("StepB", flow.TaskFunc(func(ctx flow.Context, _ *flow.Node) error { ctx.Put("b_done", true); return nil }))

	ctx := flow.NewContext("inst1")

	// 1. claim -> A (parked, task not yet run)
	task, err := exec.ClaimTask(g, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if task == nil || task.NodeID() != "a" || task.State() != workflow.TaskStateWaiting {
		t.Fatalf("claim = %+v, want a/WAITING", task)
	}
	if ctx.Get("a_done") != nil {
		t.Error("StepA should not run on claim")
	}

	// 2. submit A forward -> StepA runs, advance to B
	if err := exec.SubmitTask(g, g.GetNode("a"), workflow.ActionForward, ctx); err != nil {
		t.Fatal(err)
	}
	if ctx.Get("a_done") != true {
		t.Error("StepA should run on forward")
	}

	// 3. claim -> B
	task2, err := exec.ClaimTask(g, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if task2 == nil || task2.NodeID() != "b" {
		t.Fatalf("claim after A = %+v, want b", task2)
	}

	// 4. submit B forward -> StepB runs, flow ends
	if err := exec.SubmitTask(g, g.GetNode("b"), workflow.ActionForward, ctx); err != nil {
		t.Fatal(err)
	}
	if ctx.Get("b_done") != true {
		t.Error("StepB should run on forward")
	}

	// 5. claim -> nil (ended)
	task3, err := exec.ClaimTask(g, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if task3 != nil {
		t.Errorf("claim after end = %+v, want nil", task3)
	}
}

// TestWorkflow_ActorController: a node with actor meta is operatable only when the
// context actor matches.
func TestWorkflow_ActorController(t *testing.T) {
	g, err := flow.Create("actor", func(s *flow.GraphSpec) {
		s.AddStart("s").LinkAdd("a")
		s.AddActivity("a").MetaPut("actor", "alice").Task("@X").LinkAdd("e")
		s.AddEnd("e")
	})
	if err != nil {
		t.Fatal(err)
	}
	_, exec, _ := newWorkflow(t, workflow.NewActorStateController())

	// wrong actor -> no claimable task
	ctx := flow.NewContext("i1")
	ctx.Put("actor", "bob")
	task, _ := exec.ClaimTask(g, ctx)
	if task != nil {
		t.Errorf("bob should not claim alice's task, got %+v", task)
	}

	// correct actor -> claim
	ctx2 := flow.NewContext("i2")
	ctx2.Put("actor", "alice")
	task2, _ := exec.ClaimTask(g, ctx2)
	if task2 == nil || task2.NodeID() != "a" {
		t.Errorf("alice claim = %+v, want a", task2)
	}
}

// TestWorkflow_Terminate: submitting TERMINATE marks the node terminated and stops.
func TestWorkflow_Terminate(t *testing.T) {
	g, err := flow.Create("term", func(s *flow.GraphSpec) {
		s.AddStart("s").LinkAdd("a")
		s.AddActivity("a").LinkAdd("b")
		s.AddActivity("b").LinkAdd("e")
		s.AddEnd("e")
	})
	if err != nil {
		t.Fatal(err)
	}
	_, exec, _ := newWorkflow(t, workflow.NewBlockStateController())
	ctx := flow.NewContext("t1")

	task, _ := exec.ClaimTask(g, ctx) // a
	if task == nil {
		t.Fatal("expected to claim a")
	}
	if err := exec.SubmitTask(g, g.GetNode("a"), workflow.ActionTerminate, ctx); err != nil {
		t.Fatal(err)
	}
	if got := exec.GetState(g.GetNode("a"), ctx); got != workflow.TaskStateTerminated {
		t.Errorf("a state = %v, want TERMINATED", got)
	}
	// after terminate, no further claimable task
	if task2, _ := exec.ClaimTask(g, ctx); task2 != nil {
		t.Errorf("claim after terminate = %+v, want nil", task2)
	}
}

// TestWorkflow_FindNextTasks: lists candidate next task nodes.
func TestWorkflow_FindNextTasks(t *testing.T) {
	g, err := flow.Create("next", func(s *flow.GraphSpec) {
		s.AddStart("s").LinkAdd("a")
		s.AddActivity("a").LinkAdd("b")
		s.AddActivity("b").LinkAdd("e")
		s.AddEnd("e")
	})
	if err != nil {
		t.Fatal(err)
	}
	_, exec, _ := newWorkflow(t, workflow.NewBlockStateController())
	ctx := flow.NewContext("n1")
	tasks, err := exec.FindNextTasks(g, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) == 0 || tasks[0].NodeID() != "a" {
		t.Errorf("FindNextTasks = %v, want first a", nodeIDsWf(tasks))
	}
}

func nodeIDsWf(tasks []*workflow.Task) string {
	out := ""
	for i, tk := range tasks {
		if i > 0 {
			out += ","
		}
		out += tk.NodeID()
	}
	return out
}
