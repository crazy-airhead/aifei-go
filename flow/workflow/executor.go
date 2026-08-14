package workflow

import (
	"fmt"
	"sync"

	"github.com/crazy-airhead/aifei-go/flow"
)

// Executor is the workflow execution framework: claim/find/submit tasks over a graph.
// Mirrors Java's WorkflowExecutor.
type Executor struct {
	engine     *flow.Engine
	controller StateController
	repo       StateRepository
	mu         sync.Mutex
}

// NewExecutor builds an Executor over engine with the given controller and repository.
func NewExecutor(engine *flow.Engine, controller StateController, repo StateRepository) *Executor {
	return &Executor{engine: engine, controller: controller, repo: repo}
}

// Engine returns the flow engine.
func (e *Executor) Engine() *flow.Engine { return e.engine }

// StateController returns the state controller.
func (e *Executor) StateController() StateController { return e.controller }

// StateRepository returns the state repository.
func (e *Executor) StateRepository() StateRepository { return e.repo }

// driverFor wraps the engine's driver for graph with workflow semantics.
func (e *Executor) driverFor(graph *flow.Graph) flow.Driver {
	return NewWorkflowDriver(e.engine.GetDriver(graph), e.controller, e.repo)
}

// runWith builds a fresh exchanger (recordClear) and evaluates graph, with intent
// installed in the context for the duration.
func (e *Executor) runWith(graph *flow.Graph, ctx flow.Context, intent *WorkflowIntent) error {
	return ctx.With(IntentKey, intent, func() error {
		ex := flow.NewExchanger(graph, e.engine, e.driverFor(graph), ctx, -1)
		ex.RecordClear()
		return e.engine.EvalEx(graph, ex, nil)
	})
}

// ClaimTask returns the current activity task the user may operate (locks it WAITING),
// or nil if none. Mirrors Java's claimTask.
func (e *Executor) ClaimTask(graph *flow.Graph, ctx flow.Context) (*Task, error) {
	intent := newIntent(graph, intentClaimTask)
	if err := e.runWith(graph, ctx, intent); err != nil {
		return nil, err
	}
	return intent.task, nil
}

// ClaimTaskByID is ClaimTask by graph id.
func (e *Executor) ClaimTaskByID(graphID string, ctx flow.Context) (*Task, error) {
	g, err := e.engine.GetGraphOrThrow(graphID)
	if err != nil {
		return nil, err
	}
	return e.ClaimTask(g, ctx)
}

// FindTask returns the current determined task (logic probe), or nil.
func (e *Executor) FindTask(graph *flow.Graph, ctx flow.Context) (*Task, error) {
	intent := newIntent(graph, intentFindTask)
	if err := e.runWith(graph, ctx, intent); err != nil {
		return nil, err
	}
	return intent.task, nil
}

// FindNextTasks returns candidate next task nodes (logic probe; no actor config needed).
func (e *Executor) FindNextTasks(graph *flow.Graph, ctx flow.Context) ([]*Task, error) {
	intent := newIntent(graph, intentFindNextTasks)
	if err := e.runWith(graph, ctx, intent); err != nil {
		return nil, err
	}
	return intent.nextTasks, nil
}

// GetState returns the node's task state.
func (e *Executor) GetState(node *flow.Node, ctx flow.Context) TaskState {
	return e.repo.StateGet(ctx, node)
}

// SubmitTaskIfWaiting submits task only if it is WAITING and operatable (double-check
// under lock). Returns false if not permitted.
func (e *Executor) SubmitTaskIfWaiting(task *Task, action TaskAction, ctx flow.Context) (bool, error) {
	if task == nil || task.State() != TaskStateWaiting {
		return false, nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.repo.StateGet(ctx, task.Node()) != TaskStateWaiting || !e.controller.IsOperatable(ctx, task.Node()) {
		return false, nil
	}
	intent := newIntent(task.RootGraph(), intentSubmitTask)
	err := ctx.With(IntentKey, intent, func() error {
		return e.submitTaskDo(task.RootGraph(), task.Node(), action, ctx)
	})
	return true, err
}

// SubmitTask submits a node action (FORWARD/BACK/...).
func (e *Executor) SubmitTask(graph *flow.Graph, node *flow.Node, action TaskAction, ctx flow.Context) error {
	if action == ActionUnknown {
		return fmt.Errorf("workflow: action is UNKNOWN")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	intent := newIntent(graph, intentSubmitTask)
	return ctx.With(IntentKey, intent, func() error {
		return e.submitTaskDo(graph, node, action, ctx)
	})
}

// SubmitTaskByID submits by graph id + node id.
func (e *Executor) SubmitTaskByID(graphID, nodeID string, action TaskAction, ctx flow.Context) error {
	g, err := e.engine.GetGraphOrThrow(graphID)
	if err != nil {
		return err
	}
	node, err := g.GetNodeOrThrow(nodeID)
	if err != nil {
		return err
	}
	return e.SubmitTask(g, node, action, ctx)
}

// SubmitTaskFor submits the action for a claimed task.
func (e *Executor) SubmitTaskFor(task *Task, action TaskAction, ctx flow.Context) error {
	return e.SubmitTask(task.RootGraph(), task.Node(), action, ctx)
}

// submitTaskDo applies the action (faithful port of Java's submitTaskDo).
func (e *Executor) submitTaskDo(graph *flow.Graph, node *flow.Node, action TaskAction, ctx flow.Context) error {
	driver := e.driverFor(graph)
	ex := flow.NewExchanger(graph, e.engine, driver, ctx, -1)
	newState := action.TargetState()

	switch action {
	case ActionBack:
		e.backHandle(graph, node, ex)
	case ActionBackJump:
		var last *Task
		for {
			task, err := e.findTaskEx(graph, ex)
			if err != nil {
				return err
			}
			if task == nil {
				break
			}
			if last != nil && last.NodeID() == task.NodeID() {
				break
			}
			last = task
			if task.NodeID() == node.ID() {
				e.repo.StatePut(ctx, task.Node(), TaskStateWaiting) // park target as todo
				break
			}
			e.backHandle(graph, task.Node(), ex)
		}
	case ActionRestart:
		e.repo.StateClear(ctx)
	case ActionForward:
		if err := e.forwardHandle(graph, node, newState, ex); err != nil {
			return err
		}
	case ActionForwardJump:
		var last *Task
		for {
			task, err := e.findTaskEx(graph, ex)
			if err != nil {
				return err
			}
			if task == nil {
				break
			}
			if last != nil && last.NodeID() == task.NodeID() {
				break
			}
			last = task
			if task.NodeID() == node.ID() {
				e.repo.StatePut(ctx, task.Node(), TaskStateWaiting)
				break
			}
			if err := e.forwardHandle(graph, task.Node(), newState, ex); err != nil {
				return err
			}
		}
	default:
		e.repo.StatePut(ctx, node, newState)
	}
	return nil
}

// findTaskEx runs a findTask using the given exchanger's context.
func (e *Executor) findTaskEx(graph *flow.Graph, ex *flow.Exchanger) (*Task, error) {
	intent := newIntent(graph, intentFindTask)
	err := ex.Context().With(IntentKey, intent, func() error {
		ex2 := flow.NewExchanger(graph, e.engine, e.driverFor(graph), ex.Context(), -1)
		ex2.RecordClear()
		return e.engine.EvalEx(graph, ex2, nil)
	})
	return intent.task, err
}

// forwardHandle runs the node task, marks state, and auto-advances next nodes.
func (e *Executor) forwardHandle(graph *flow.Graph, node *flow.Node, newState TaskState, ex *flow.Exchanger) error {
	ex.SetReverting(false)
	if err := ex.Driver().PostHandleTask(ex, node.Task()); err != nil {
		return fmt.Errorf("workflow: task handle failed: %s / %s: %w", graph.GetID(), node.ID(), err)
	}
	e.repo.StatePut(ex.Context(), node, newState)

	for _, next := range node.NextNodes() {
		nn := next
		if nn.Type().IsGateway() {
			// resolve through the engine to the next activity
			task, err := e.findTaskEx(graph, ex)
			if err != nil {
				return err
			}
			if task != nil {
				if task.State() == TaskStateTerminated {
					return nil // terminated: stop forwarding
				}
				nn = task.Node()
			} else {
				nn = nil
			}
		}
		if nn != nil && e.controller.IsAutoForward(ex.Context(), nn) {
			ex.RecordClear()
			cex := ex.CopyFor(nn.Graph())
			cex.SetReverting(false)
			if err := e.engine.EvalEx(nn.Graph(), cex, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// backHandle reverts node and its predecessors' states (requires redo).
func (e *Executor) backHandle(graph *flow.Graph, node *flow.Node, ex *flow.Exchanger) {
	e.backHandleDo(graph, node, ex, map[string]bool{})
}

func (e *Executor) backHandleDo(_ *flow.Graph, node *flow.Node, ex *flow.Exchanger, visited map[string]bool) {
	k := node.Graph().GetID() + ":" + node.ID()
	if visited[k] {
		return
	}
	visited[k] = true

	e.repo.StateRemove(ex.Context(), node)
	for _, prev := range node.PrevNodes() {
		switch prev.Type() {
		case flow.NodeTypeActivity:
			e.repo.StateRemove(ex.Context(), prev)
		default:
			if prev.Type().IsGateway() {
				for _, child := range prev.NextNodes() {
					if child.Type() == flow.NodeTypeActivity {
						e.repo.StateRemove(ex.Context(), child)
					}
				}
				e.backHandleDo(prev.Graph(), prev, ex, visited)
			}
		}
	}
}
