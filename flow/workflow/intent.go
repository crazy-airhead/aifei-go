package workflow

import "github.com/crazy-airhead/aifei-go/flow"

// IntentKey is the context key under which the active WorkflowIntent is stored.
const IntentKey = "WorkflowIntent"

// intentType identifies the workflow operation in progress.
type intentType int

const (
	intentUnknown intentType = iota
	intentClaimTask
	intentFindTask
	intentFindNextTasks
	intentSubmitTask
	intentSubmitTaskIfWaiting
)

// WorkflowIntent carries the operation type and result slots (task / nextTasks)
// populated by WorkflowDriver.HandleTask during an eval. Internal.
// Mirrors Java's WorkflowIntent.
type WorkflowIntent struct {
	rootGraph *flow.Graph
	typ       intentType
	nextTasks []*Task
	task      *Task
}

// newIntent creates an intent.
func newIntent(rootGraph *flow.Graph, typ intentType) *WorkflowIntent {
	return &WorkflowIntent{rootGraph: rootGraph, typ: typ, nextTasks: []*Task{}}
}

// Task returns the single matched task (claim/find).
func (i *WorkflowIntent) Task() *Task { return i.task }

// NextTasks returns the candidate next tasks (findNextTasks).
func (i *WorkflowIntent) NextTasks() []*Task { return i.nextTasks }
