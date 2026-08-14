package flow

import (
	"fmt"
)

// Engine evaluates graphs. Load graphs, then Eval them with a Context. The driver
// controls task/condition execution; the default is a SimpleDriver (enjoy-based).
// Mirrors Java's FlowEngine / FlowEngineDefault.
type Engine struct {
	graphs       map[string]*Graph
	drivers      map[string]Driver
	driverDef    Driver
	interceptors []rankInterceptor
}

// NewEngine creates an Engine, optionally with a default driver (else SimpleDriver).
func NewEngine(driver ...Driver) *Engine {
	e := &Engine{
		graphs:  map[string]*Graph{},
		drivers: map[string]Driver{},
	}
	if len(driver) > 0 && driver[0] != nil {
		e.driverDef = driver[0]
	} else {
		e.driverDef = NewSimpleDriver()
	}
	return e
}

// Then runs fn against the engine (fluent).
func (e *Engine) Then(fn func(*Engine)) *Engine {
	if fn != nil {
		fn(e)
	}
	return e
}

// GetDriver returns the driver for graph (graph.driver name, else the default).
func (e *Engine) GetDriver(graph *Graph) Driver {
	if graph.GetDriver() == "" {
		return e.driverDef
	}
	d, ok := e.drivers[graph.GetDriver()]
	if !ok {
		panic(fmt.Sprintf("flow: no driver found for '%s'", graph.GetDriver()))
	}
	return d
}

// AddInterceptor adds an interceptor (lower index runs first).
func (e *Engine) AddInterceptor(ic Interceptor, index ...int) {
	idx := 0
	if len(index) > 0 {
		idx = index[0]
	}
	e.interceptors = append(e.interceptors, rankInterceptor{target: ic, index: idx})
	sortRankInterceptors(e.interceptors)
}

// RemoveInterceptor removes an interceptor.
func (e *Engine) RemoveInterceptor(ic Interceptor) {
	for i, r := range e.interceptors {
		if r.target == ic {
			e.interceptors = append(e.interceptors[:i], e.interceptors[i+1:]...)
			return
		}
	}
}

// Register registers a named driver (or the default when name is "").
func (e *Engine) Register(name string, driver Driver) {
	if driver == nil {
		return
	}
	if name == "" {
		e.driverDef = driver
	} else {
		e.drivers[name] = driver
	}
}

// Unregister removes a named driver.
func (e *Engine) Unregister(name string) { delete(e.drivers, name) }

// Load registers a graph by its id.
func (e *Engine) Load(graph *Graph) { e.graphs[graph.GetID()] = graph }

// Unload removes a graph by id.
func (e *Engine) Unload(graphID string) { delete(e.graphs, graphID) }

// GetGraphs returns all loaded graphs.
func (e *Engine) GetGraphs() []*Graph {
	out := make([]*Graph, 0, len(e.graphs))
	for _, g := range e.graphs {
		out = append(out, g)
	}
	return out
}

// GetGraph returns a graph by id (nil if absent).
func (e *Engine) GetGraph(graphID string) *Graph { return e.graphs[graphID] }

// GetGraphOrThrow returns a graph by id, erroring if absent.
func (e *Engine) GetGraphOrThrow(graphID string) (*Graph, error) {
	g := e.GetGraph(graphID)
	if g == nil {
		return nil, fmt.Errorf("%w: %s", ErrGraphNotFound, graphID)
	}
	return g, nil
}

// Eval evaluates graph with ctx (unlimited steps).
func (e *Engine) Eval(graph *Graph, ctx Context) error {
	return e.EvalWithSteps(graph, -1, ctx)
}

// EvalByID looks up a graph by id and evaluates it.
func (e *Engine) EvalByID(graphID string, ctx Context) error {
	g, err := e.GetGraphOrThrow(graphID)
	if err != nil {
		return err
	}
	return e.Eval(g, ctx)
}

// EvalWithSteps evaluates graph with a step budget (steps < 0 = unlimited).
func (e *Engine) EvalWithSteps(graph *Graph, steps int, ctx Context) error {
	driver := e.GetDriver(graph)
	ex := NewExchanger(graph, e, driver, ctx, steps)
	return e.eval(graph, ex, nil)
}

// EvalEx evaluates graph using a pre-built exchanger (for the workflow package,
// which supplies its own driver). opts may be nil.
func (e *Engine) EvalEx(graph *Graph, ex *Exchanger, opts *Options) error {
	return e.eval(graph, ex, opts)
}

// eval is the core: sets up the interceptor invocation around evalDo.
func (e *Engine) eval(graph *Graph, ex *Exchanger, opts *Options) error {
	lastNode := ex.ctx.trace.LastNode(graph)
	bak := ex.ctx.exchanger
	if opts == nil {
		opts = NewOptions()
	}
	opts.interceptorAddAll(e.interceptors)

	ex.ctx.setExchanger(ex)
	ex.ctx.setStopped(false) // reset before each eval
	err := newInvocation(ex, opts, lastNode, e.evalDo).Invoke()
	ex.ctx.setExchanger(bak)
	return err
}

// evalDo runs from the graph start, reverting to lastNode.
func (e *Engine) evalDo(inv *Invocation, opts *Options) error {
	ex := inv.Exchanger()
	start := inv.StartNode()
	return e.nodeRun(ex, opts, start.Graph().GetStart(), start)
}

// onNodeStart (engine-level): fires interceptors + driver, checks stop/interrupt.
func (e *Engine) onNodeStart(ex *Exchanger, opts *Options, node *Node) bool {
	if ex.IsReverting() {
		return true
	}
	for _, r := range opts.list() {
		r.target.OnNodeStart(ex.Context(), node)
	}
	ex.Driver().OnNodeStart(ex, node)
	if ex.IsStopped() {
		return false
	}
	if ex.IsInterrupted() {
		ex.SetInterrupt(false)
		return false
	}
	return true
}

// onNodeEnd (engine-level): fires interceptors + driver, checks stop/interrupt.
func (e *Engine) onNodeEnd(ex *Exchanger, opts *Options, node *Node) bool {
	if ex.IsReverting() {
		return true
	}
	for _, r := range opts.list() {
		r.target.OnNodeEnd(ex.Context(), node)
	}
	ex.Driver().OnNodeEnd(ex, node)
	if ex.IsStopped() {
		return false
	}
	if ex.IsInterrupted() {
		ex.SetInterrupt(false)
		return false
	}
	return true
}

func (e *Engine) conditionTest(ex *Exchanger, cond ConditionDesc, def bool) (bool, error) {
	if cond.IsEmpty() {
		return def, nil
	}
	return ex.Driver().HandleCondition(ex, cond)
}

// taskExec runs a node's when+task, bracketed by onNodeStart/onNodeEnd.
func (e *Engine) taskExec(ex *Exchanger, opts *Options, node *Node) (bool, error) {
	if ex.IsReverting() {
		return true, nil
	}
	if ok := e.onNodeStart(ex, opts, node); !ok {
		return false, nil
	}
	if ok, err := e.conditionTest(ex, node.When(), true); err != nil {
		return false, err
	} else if ok {
		if err := ex.Driver().HandleTask(ex, node.Task()); err != nil {
			return false, err
		}
	}
	if ex.IsStopped() {
		return false, nil
	}
	if ex.IsInterrupted() {
		ex.SetInterrupt(false)
		return false, nil
	}
	return e.onNodeEnd(ex, opts, node), nil
}

// nodeRun is the recursive traversal core.
func (e *Engine) nodeRun(ex *Exchanger, opts *Options, node, startNode *Node) error {
	if node == nil {
		return nil
	}
	if ex.IsStopped() {
		return nil
	}
	if ex.IsInterrupted() {
		ex.SetInterrupt(false)
		return nil
	}

	if ex.IsReverting() {
		if node.ID() == startNode.ID() && node.Graph().GetID() == startNode.Graph().GetID() {
			ex.SetReverting(false)
		}
	} else {
		ex.RecordNode(node.Graph(), node)
	}

	if !ex.IsReverting() {
		if !ex.nextStep(node) {
			ex.Stop()
			return nil
		}
	}

	switch node.Type() {
	case NodeTypeStart:
		return e.startRun(ex, opts, node, startNode)
	case NodeTypeEnd:
		return e.endRun(ex, opts, node, startNode)
	case NodeTypeActivity:
		return e.activityRun(ex, opts, node, startNode)
	case NodeTypeInclusive:
		return e.inclusiveRun(ex, opts, node, startNode)
	case NodeTypeExclusive:
		return e.exclusiveRun(ex, opts, node, startNode)
	case NodeTypeParallel:
		return e.parallelRun(ex, opts, node, startNode)
	case NodeTypeLoop:
		return e.loopRun(ex, opts, node, startNode)
	}
	return nil
}

func (e *Engine) startRun(ex *Exchanger, opts *Options, node, startNode *Node) error {
	if ok := e.onNodeStart(ex, opts, node); !ok {
		return nil
	}
	if ok := e.onNodeEnd(ex, opts, node); !ok {
		return nil
	}
	for _, l := range node.NextLinks() {
		if ok, err := e.conditionTest(ex, l.When(), true); err != nil {
			return err
		} else if ok {
			if err := e.nodeRun(ex, opts, l.NextNode(), startNode); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Engine) endRun(ex *Exchanger, opts *Options, node, startNode *Node) error {
	if ok := e.onNodeStart(ex, opts, node); !ok {
		return nil
	}
	if ok := e.onNodeEnd(ex, opts, node); !ok {
		return nil
	}
	return nil
}

func (e *Engine) activityRun(ex *Exchanger, opts *Options, node, startNode *Node) error {
	ok, err := e.taskExec(ex, opts, node)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	return e.activityRunOut(ex, opts, node, startNode)
}

func (e *Engine) activityRunOut(ex *Exchanger, opts *Options, node, startNode *Node) error {
	for _, l := range node.NextLinks() {
		if ok, err := e.conditionTest(ex, l.When(), true); err != nil {
			return err
		} else if ok {
			if err := e.nodeRun(ex, opts, l.NextNode(), startNode); err != nil {
				return err
			}
		}
	}
	return nil
}

// exclusive gateway: first true branch wins; else the default (empty-when) branch.
func (e *Engine) exclusiveRun(ex *Exchanger, opts *Options, node, startNode *Node) error {
	ok, err := e.taskExec(ex, opts, node)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	var defLine *Link
	for _, l := range node.NextLinks() {
		if l.When().IsEmpty() {
			defLine = l
			continue
		}
		if match, err := e.conditionTest(ex, l.When(), false); err != nil {
			return err
		} else if match {
			return e.nodeRun(ex, opts, l.NextNode(), startNode)
		}
	}
	if defLine != nil {
		return e.nodeRun(ex, opts, defLine.NextNode(), startNode)
	}
	return nil
}

// inclusive gateway: all true branches (with join on incoming).
func (e *Engine) inclusiveRun(ex *Exchanger, opts *Options, node, startNode *Node) error {
	gid := node.Graph().GetID()
	stackKey := "inclusive_run"
	// join: wait for all incoming branches
	if len(node.PrevLinks()) > 1 {
		if ex.Temporary().StackSize(gid, stackKey) > 0 {
			startSize := ex.Temporary().StackPeek(gid, stackKey).(int)
			inSize := ex.Temporary().CountIncr(gid, node.ID())
			if startSize > inSize {
				return nil // wait for all branches
			}
			ex.Temporary().StackPop(gid, stackKey)
		}
	}
	ok, err := e.taskExec(ex, opts, node)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	// out: collect matched, push count, run all
	var matched []*Link
	for _, l := range node.NextLinks() {
		if m, err := e.conditionTest(ex, l.When(), true); err != nil {
			return err
		} else if m {
			matched = append(matched, l)
		}
	}
	if len(matched) > 0 {
		ex.Temporary().StackPush(gid, stackKey, len(matched))
		for _, l := range matched {
			if err := e.nodeRun(ex, opts, l.NextNode(), startNode); err != nil {
				return err
			}
		}
	}
	return nil
}

// parallel gateway: all branches (with count-based join).
func (e *Engine) parallelRun(ex *Exchanger, opts *Options, node, startNode *Node) error {
	gid := node.Graph().GetID()
	// join: wait for all incoming branches
	count := ex.Temporary().CountIncr(gid, node.ID())
	if len(node.PrevLinks()) > count {
		return nil
	}
	ok, err := e.taskExec(ex, opts, node)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	// reset count, then run all next nodes. Branches run SEQUENTIALLY (matching
	// Solon-Flow's no-executor default); the parallel gateway is about fork-join
	// semantics, not concurrent execution. True concurrency would require a
	// thread-safe Trace and atomic exchanger flags — deliberately not done.
	ex.Temporary().CountSet(gid, node.ID(), 0)
	for _, n := range node.NextNodes() {
		if err := e.nodeRun(ex, opts, n, startNode); err != nil {
			return err
		}
	}
	return nil
}

// loop gateway: iterate $in collection, binding each item to $for.
func (e *Engine) loopRun(ex *Exchanger, opts *Options, node, startNode *Node) error {
	gid := node.Graph().GetID()
	stackKey := "loop_run"
	forKey := node.MetaAsString("$for")

	if forKey == "" {
		// inflow (end of loop body): wait for iterator to exhaust
		if ex.Temporary().StackSize(gid, stackKey) > 0 {
			it := ex.Temporary().StackPeek(gid, stackKey).(iterator)
			if it.hasNext() {
				return nil // wait
			}
			ex.Temporary().StackPop(gid, stackKey)
		}
		ok, err := e.taskExec(ex, opts, node)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		return e.activityRunOut(ex, opts, node, startNode)
	}

	// outflow (start of loop): build iterator and drive body
	ok, err := e.taskExec(ex, opts, node)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	inKey := node.Meta("$in")
	it, err := buildIterator(inKey, ex)
	if err != nil {
		return err
	}
	ex.Temporary().StackPush(gid, stackKey, it)
	for it.hasNext() {
		item := it.next()
		ex.ctx.Put(forKey, item)
		if err := e.activityRunOut(ex, opts, node, startNode); err != nil {
			return err
		}
	}
	return nil
}

// iterator abstracts slice/stepper iteration for the loop gateway.
type iterator interface {
	hasNext() bool
	next() any
}

type sliceIterator struct {
	items []any
	i     int
}

func (s *sliceIterator) hasNext() bool { return s.i < len(s.items) }
func (s *sliceIterator) next() any     { v := s.items[s.i]; s.i++; return v }

type stepperIterator struct{ s *Stepper }

func (s *stepperIterator) hasNext() bool { return s.s.HasNext() }
func (s *stepperIterator) next() any     { return s.s.Next() }

// buildIterator builds an iterator from a $in value: a slice (constant), a stepper
// string ("a:b:step" or "a...b"), or a variable name holding a slice.
func buildIterator(inKey any, ex *Exchanger) (iterator, error) {
	switch v := inKey.(type) {
	case []any:
		return &sliceIterator{items: v}, nil
	case []int:
		items := make([]any, len(v))
		for i, x := range v {
			items[i] = x
		}
		return &sliceIterator{items: items}, nil
	case string:
		if !containsColonOrEllipsis(v) {
			// variable holding a collection
			col := ex.ctx.Get(v)
			if items, ok := col.([]any); ok {
				return &sliceIterator{items: items}, nil
			}
			return nil, fmt.Errorf("flow: $in variable '%s' is not a collection", v)
		}
		s, err := StepperFrom(v)
		if err != nil {
			return nil, err
		}
		return &stepperIterator{s: s}, nil
	}
	return nil, fmt.Errorf("flow: $in must be a list or a string")
}

func containsColonOrEllipsis(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			return true
		}
		if s[i] == '.' && i+2 < len(s) && s[i+1] == '.' && s[i+2] == '.' {
			return true
		}
	}
	return false
}

func sortRankInterceptors(s []rankInterceptor) {
	// stable by index
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j].index < s[j-1].index; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
