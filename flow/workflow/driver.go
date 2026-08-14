package workflow

import "github.com/crazy-airhead/aifei-go/flow"

// WorkflowDriver wraps a base flow.Driver and translates the engine's "run to
// completion" semantics into workflow "human-task pause / claim / submit" semantics,
// driven by a StateController (who/when) and a StateRepository (per-node state).
// Mirrors Java's WorkflowDriver.
type WorkflowDriver struct {
	wrapped    flow.Driver
	controller StateController
	repo       StateRepository
}

// NewWorkflowDriver wraps base with workflow semantics.
func NewWorkflowDriver(base flow.Driver, controller StateController, repo StateRepository) *WorkflowDriver {
	return &WorkflowDriver{wrapped: base, controller: controller, repo: repo}
}

// Executor returns the wrapped driver's executor.
func (d *WorkflowDriver) Executor() func(fn func()) { return d.wrapped.Executor() }

// OnNodeStart merges the repository's vars into the context, then delegates.
func (d *WorkflowDriver) OnNodeStart(ex *flow.Exchanger, node *flow.Node) {
	if m := d.repo.VarsGet(ex.Context(), node); len(m) > 0 {
		for k, v := range m {
			ex.Context().Put(k, v)
		}
	}
	d.wrapped.OnNodeStart(ex, node)
}

// OnNodeEnd delegates, then clears a CLAIM match if the graph ended (no task left).
func (d *WorkflowDriver) OnNodeEnd(ex *flow.Exchanger, node *flow.Node) {
	d.wrapped.OnNodeEnd(ex, node)
	if node.Type() == flow.NodeTypeEnd {
		intent := intentOf(ex)
		if intent == nil {
			return
		}
		if intent.typ == intentClaimTask {
			intent.task = nil // ended -> no claimable task
		}
	}
}

// HandleCondition delegates to the wrapped driver.
func (d *WorkflowDriver) HandleCondition(ex *flow.Exchanger, cond flow.ConditionDesc) (bool, error) {
	return d.wrapped.HandleCondition(ex, cond)
}

// PostHandleTask delegates to the wrapped driver (runs the actual task).
func (d *WorkflowDriver) PostHandleTask(ex *flow.Exchanger, task flow.TaskDesc) error {
	return d.wrapped.PostHandleTask(ex, task)
}

// intentOf reads the active intent from the context (nil if none).
func intentOf(ex *flow.Exchanger) *WorkflowIntent {
	v := ex.Context().Get(IntentKey)
	if v == nil {
		return nil
	}
	it, _ := v.(*WorkflowIntent)
	return it
}

// HandleTask is the workflow state machine: decides whether to run/stop/interrupt at
// a node based on its state, the controller, and the active intent.
func (d *WorkflowDriver) HandleTask(ex *flow.Exchanger, task flow.TaskDesc) error {
	intent := intentOf(ex)
	if intent == nil {
		intent = newIntent(ex.Graph(), intentUnknown)
	}
	ctx := ex.Context()
	node := task.Node()

	if d.controller.IsAutoForward(ctx, node) {
		// auto-advance (no permission filtering)
		state := d.repo.StateGet(ctx, node)
		switch state {
		case TaskStateUnknown, TaskStateWaiting: // UNKNOWN or WAITING
			if err := d.PostHandleTask(ex, task); err != nil {
				return err
			}
			if ex.IsStopped() || ex.IsInterrupted() {
				intent.task = newTask(ex, intent.rootGraph, node, TaskStateWaiting)
				if state == TaskStateUnknown {
					d.repo.StatePut(ctx, node, TaskStateWaiting)
				}
			} else {
				intent.task = newTask(ex, intent.rootGraph, node, TaskStateCompleted)
				d.repo.StatePut(ctx, node, TaskStateCompleted)
			}
		case TaskStateTerminated:
			if intent.typ == intentFindTask {
				intent.task = newTask(ex, intent.rootGraph, node, TaskStateTerminated)
			}
			ex.Stop()
		case TaskStateCompleted:
			if intent.typ == intentFindTask {
				intent.task = newTask(ex, intent.rootGraph, node, TaskStateCompleted)
			}
		}
		return nil
	}

	// controlled (human task)
	state := d.repo.StateGet(ctx, node)
	switch state {
	case TaskStateUnknown, TaskStateWaiting:
		if d.controller.IsOperatable(ctx, node) {
			taskObj := newTask(ex, intent.rootGraph, node, TaskStateWaiting)
			intent.task = taskObj
			intent.nextTasks = append(intent.nextTasks, taskObj)
			if state == TaskStateUnknown {
				d.repo.StatePut(ctx, node, TaskStateWaiting)
			}
			if intent.typ == intentFindNextTasks {
				ex.Interrupt()
			} else {
				ex.Stop()
			}
		} else {
			// not permitted; offer as unknown candidate
			taskObj := newTask(ex, intent.rootGraph, node, TaskStateUnknown)
			intent.nextTasks = append(intent.nextTasks, taskObj)
			if intent.typ == intentFindTask {
				intent.task = taskObj
				ex.Stop()
			} else {
				ex.Interrupt()
			}
		}
	case TaskStateTerminated:
		if intent.typ == intentFindTask {
			intent.task = newTask(ex, intent.rootGraph, node, TaskStateTerminated)
		}
		ex.Stop()
	case TaskStateCompleted:
		if intent.typ == intentFindTask {
			intent.task = newTask(ex, intent.rootGraph, node, TaskStateCompleted)
		}
	}
	return nil
}
