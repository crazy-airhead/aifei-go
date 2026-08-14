package flow_plugin_test

import (
	"testing"

	flow "github.com/crazy-airhead/aifei-go/flow"
	"github.com/crazy-airhead/aifei-go/flow/workflow"
	flowplugin "github.com/crazy-airhead/aifei-go/plugins/flow"
)

// TestWorkflow_WithMysqlRepo wires the MysqlStateRepository (fake-backed) into a real
// workflow.Executor and verifies the state machine persists transitions through it.
func TestWorkflow_WithMysqlRepo(t *testing.T) {
	g, err := flow.Create("wf", func(s *flow.GraphSpec) {
		s.AddStart("s").LinkAdd("a")
		s.AddActivity("a").LinkAdd("b")
		s.AddActivity("b").LinkAdd("e")
		s.AddEnd("e")
	})
	if err != nil {
		t.Fatal(err)
	}
	store := newMemStore()
	repo := flowplugin.NewMysqlStateRepository(nil)
	repo.SetStatePersisters(store.persist, store.load)

	engine := flow.NewEngine(flow.NewSimpleDriver())
	exec := workflow.NewExecutor(engine, workflow.NewBlockStateController(), repo)
	ctx := flow.NewContext("inst")

	// claim parks a as WAITING -> persisted
	task, err := exec.ClaimTask(g, ctx)
	if err != nil || task == nil || task.NodeID() != "a" {
		t.Fatalf("claim = %v err=%v", task, err)
	}
	if code := store.data["inst"]["wf:a"]; code != workflow.TaskStateWaiting.Code() {
		t.Errorf("after claim, persisted a = %d, want WAITING(%d)", code, workflow.TaskStateWaiting.Code())
	}

	// forward a -> COMPLETED persisted
	if err := exec.SubmitTask(g, g.GetNode("a"), workflow.ActionForward, ctx); err != nil {
		t.Fatal(err)
	}
	if code := store.data["inst"]["wf:a"]; code != workflow.TaskStateCompleted.Code() {
		t.Errorf("after forward, persisted a = %d, want COMPLETED(%d)", code, workflow.TaskStateCompleted.Code())
	}

	// a fresh executor over the SAME store must see the persisted state and resume to b.
	exec2 := workflow.NewExecutor(engine, workflow.NewBlockStateController(), repo)
	task2, err := exec2.ClaimTask(g, ctx)
	if err != nil || task2 == nil || task2.NodeID() != "b" {
		t.Fatalf("resume claim = %v err=%v (want b)", task2, err)
	}
}
