package flow

import "sync/atomic"

// Exchanger holds the mutable state of one graph evaluation: the current
// graph/engine/driver/context, the step budget, the Temporary (gateway join state),
// and the interrupted/stopped/reverting flags. Internal; not serialized.
// Mirrors Java's FlowExchanger.
type Exchanger struct {
	graph   *Graph
	engine  *Engine
	driver  Driver
	ctx     *flowContext
	steps   int
	stepCnt *int32 // shared atomic counter across subgraph evals
	temp    *Temporary

	interrupted bool
	stopped     bool
	reverting   bool
}

// newExchanger builds an Exchanger for one eval. stepCount is shared across
// subgraph evals (passed in from the parent).
func newExchanger(graph *Graph, engine *Engine, driver Driver, ctx *flowContext, steps int, stepCount *int32) *Exchanger {
	return &Exchanger{
		graph: graph, engine: engine, driver: driver, ctx: ctx,
		steps: steps, stepCnt: stepCount,
		temp:        newTemporary(),
		interrupted: false, stopped: false, reverting: true,
	}
}

// NewExchanger builds an Exchanger for a top-level eval with a fresh step counter.
// Exported for the workflow package (which evaluates with custom drivers).
func NewExchanger(graph *Graph, engine *Engine, driver Driver, ctx Context, steps int) *Exchanger {
	fc, _ := ctx.(*flowContext)
	var stepCnt int32 = 0
	return &Exchanger{
		graph: graph, engine: engine, driver: driver, ctx: fc,
		steps: steps, stepCnt: &stepCnt,
		temp:        newTemporary(),
		interrupted: false, stopped: false, reverting: true,
	}
}

// CopyFor returns a shallow copy bound to a new graph (for subgraph/forward eval),
// sharing the context, steps budget, step counter, and temporary state.
func (ex *Exchanger) CopyFor(graph *Graph) *Exchanger {
	return &Exchanger{
		graph: graph, engine: ex.engine, driver: ex.driver, ctx: ex.ctx,
		steps: ex.steps, stepCnt: ex.stepCnt,
		temp: ex.temp, reverting: true,
	}
}

// CopyForContext returns a shallow copy bound to a new graph and context.
func (ex *Exchanger) CopyForContext(graph *Graph, ctx Context) *Exchanger {
	fc, _ := ctx.(*flowContext)
	return &Exchanger{
		graph: graph, engine: ex.engine, driver: ex.driver, ctx: fc,
		steps: ex.steps, stepCnt: ex.stepCnt,
		temp: ex.temp, reverting: true,
	}
}

// Graph returns the current graph.
func (ex *Exchanger) Graph() *Graph { return ex.graph }

// Engine returns the current engine.
func (ex *Exchanger) Engine() *Engine { return ex.engine }

// Driver returns the current driver.
func (ex *Exchanger) Driver() Driver { return ex.driver }

// Context returns the flow context (as the public interface).
func (ex *Exchanger) Context() Context { return ex.ctx }

// Temporary returns the per-run temporary state.
func (ex *Exchanger) Temporary() *Temporary { return ex.temp }

// RecordNode records the node as the graph's last-executed (trace).
func (ex *Exchanger) RecordNode(graph *Graph, node *Node) {
	ex.ctx.trace.RecordNode(graph, node)
}

// RecordClear clears the trace.
func (ex *Exchanger) RecordClear() { ex.ctx.trace.Clear() }

// RunGraph evaluates a subgraph (for `#graphId` task), sharing the step budget.
// If the subgraph has not ended, the current branch is interrupted.
func (ex *Exchanger) RunGraph(graph *Graph) {
	ex.prevStep() // refund a step (subgraph counts separately)
	ex.engine.eval(graph, ex.CopyFor(graph), nil)
	if !ex.IsStopped() && !ex.ctx.trace.IsEnd(graph.GetID()) {
		ex.Interrupt() // subgraph not ended -> interrupt this branch
	}
}

// RunTask runs an ad-hoc task (node + description) via the driver.
func (ex *Exchanger) RunTask(node *Node, description string) error {
	if node == nil {
		return ErrNodeNotFound
	}
	if err := ex.engine.GetDriver(node.Graph()).HandleTask(ex, TaskDesc{node: node, description: description}); err != nil {
		return err
	}
	return nil
}

// GetSteps returns the step budget (-1 = unlimited).
func (ex *Exchanger) GetSteps() int { return ex.steps }

// prevStep refunds one step (used before subgraph eval).
func (ex *Exchanger) prevStep() {
	if ex.steps < 0 {
		return
	}
	atomic.AddInt32(ex.stepCnt, -1)
}

// nextStep consumes one step; returns false when the budget is exhausted.
func (ex *Exchanger) nextStep(node *Node) bool {
	if ex.steps < 0 {
		return true
	}
	return atomic.AddInt32(ex.stepCnt, 1) <= int32(ex.steps)
}

// IsStopped reports whether the whole flow has been stopped.
func (ex *Exchanger) IsStopped() bool { return ex.stopped || ex.ctx.IsStopped() }

// Stop stops the whole flow.
func (ex *Exchanger) Stop() {
	ex.stopped = true
	ex.ctx.setStopped(true)
}

// IsInterrupted reports whether the current branch is interrupted.
func (ex *Exchanger) IsInterrupted() bool { return ex.interrupted }

// Interrupt interrupts the current branch only.
func (ex *Exchanger) Interrupt() { ex.interrupted = true }

// SetInterrupt sets/clears the interrupt flag.
func (ex *Exchanger) SetInterrupt(b bool) { ex.interrupted = b }

// IsReverting reports whether the engine is still re-walking to the resume point.
func (ex *Exchanger) IsReverting() bool { return ex.reverting }

// SetReverting sets the reverting flag.
func (ex *Exchanger) SetReverting(b bool) { ex.reverting = b }
