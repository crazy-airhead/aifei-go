package workflow

import "github.com/crazy-airhead/aifei-go/flow"

// Task is a workflow task: a node, its state, and the root graph of the instance.
// Mirrors Java's Task.
type Task struct {
	ex        *flow.Exchanger
	rootGraph *flow.Graph
	node      *flow.Node
	state     TaskState
}

// newTask builds a Task (used by the driver/intent).
func newTask(ex *flow.Exchanger, rootGraph *flow.Graph, node *flow.Node, state TaskState) *Task {
	return &Task{ex: ex, rootGraph: rootGraph, node: node, state: state}
}

// Run runs the node's task against ctx (re-running the driver's task handler with
// reverting off).
func (t *Task) Run(ctx flow.Context) error {
	if t.node == nil {
		return flow.ErrNodeNotFound
	}
	ex := t.ex.CopyForContext(t.node.Graph(), ctx)
	ex.SetReverting(false)
	return t.ex.Driver().HandleTask(ex, t.node.Task())
}

// RootGraph returns the instance root graph.
func (t *Task) RootGraph() *flow.Graph { return t.rootGraph }

// Node returns the task node.
func (t *Task) Node() *flow.Node { return t.node }

// NodeID returns the task node id.
func (t *Task) NodeID() string {
	if t.node == nil {
		return ""
	}
	return t.node.ID()
}

// State returns the task state.
func (t *Task) State() TaskState { return t.state }

// IsEnd reports whether the instance's last record is an end node.
func (t *Task) IsEnd() bool {
	r := t.ex.Context().LastRecord()
	if r == nil {
		return false
	}
	return r.IsEnd()
}
