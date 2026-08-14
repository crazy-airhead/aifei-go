package flow

import "sort"

// Interceptor wraps flow execution. interceptFlow wraps the whole eval (chain);
// OnNodeStart/OnNodeEnd are per-node callbacks fired by the engine around task
// execution. Mirrors Java's FlowInterceptor.
type Interceptor interface {
	// InterceptFlow wraps one engine eval. Must call inv.Invoke() to proceed.
	InterceptFlow(inv *Invocation) error
	// OnNodeStart is fired before a node's task runs (after inflow, before outflow).
	OnNodeStart(ctx Context, node *Node)
	// OnNodeEnd is fired after a node's task runs (before outflow).
	OnNodeEnd(ctx Context, node *Node)
}

// InterceptorFunc adapts a function as an Interceptor (per-node callbacks are no-ops).
type InterceptorFunc func(inv *Invocation) error

func (f InterceptorFunc) InterceptFlow(inv *Invocation) error { return f(inv) }
func (f InterceptorFunc) OnNodeStart(Context, *Node)          {}
func (f InterceptorFunc) OnNodeEnd(Context, *Node)            {}

// rankInterceptor pairs an interceptor with an ordering index (lower runs first).
type rankInterceptor struct {
	target Interceptor
	index  int
}

// Options carries per-eval interceptor configuration. Mirrors Java's FlowOptions.
type Options struct {
	interceptors []rankInterceptor
}

// NewOptions creates empty Options.
func NewOptions() *Options { return &Options{} }

// InterceptorAdd adds an interceptor at index (lower first), keeping the list sorted.
func (o *Options) InterceptorAdd(ic Interceptor, index ...int) *Options {
	idx := 0
	if len(index) > 0 {
		idx = index[0]
	}
	o.interceptors = append(o.interceptors, rankInterceptor{target: ic, index: idx})
	sort.SliceStable(o.interceptors, func(i, j int) bool {
		return o.interceptors[i].index < o.interceptors[j].index
	})
	return o
}

// interceptorAddAll merges more interceptors (engine-level) into these options.
func (o *Options) interceptorAddAll(more []rankInterceptor) {
	o.interceptors = append(o.interceptors, more...)
	sort.SliceStable(o.interceptors, func(i, j int) bool {
		return o.interceptors[i].index < o.interceptors[j].index
	})
}

// list returns the sorted interceptors (for engine per-node callbacks).
func (o *Options) list() []rankInterceptor { return o.interceptors }

// Invocation is the interceptor call chain for one eval. invoke() walks interceptors
// in order, then runs the engine's evalDo. Mirrors Java's FlowInvocation.
type Invocation struct {
	exchanger    *Exchanger
	options      *Options
	startNode    *Node
	interceptors []rankInterceptor
	lastHandler  func(*Invocation, *Options) error
	index        int
}

func newInvocation(ex *Exchanger, opts *Options, startNode *Node, last func(*Invocation, *Options) error) *Invocation {
	return &Invocation{
		exchanger:    ex,
		options:      opts,
		startNode:    startNode,
		interceptors: opts.list(),
		lastHandler:  last,
	}
}

// Exchanger returns the evaluation exchanger.
func (inv *Invocation) Exchanger() *Exchanger { return inv.exchanger }

// Context returns the flow context.
func (inv *Invocation) Context() Context { return inv.exchanger.Context() }

// Graph returns the graph being evaluated.
func (inv *Invocation) Graph() *Graph { return inv.startNode.Graph() }

// StartNode returns the recovery start node.
func (inv *Invocation) StartNode() *Node { return inv.startNode }

// Invoke advances the chain; calls the engine handler once interceptors are exhausted.
func (inv *Invocation) Invoke() error {
	if inv.index < len(inv.interceptors) {
		ic := inv.interceptors[inv.index].target
		inv.index++
		return ic.InterceptFlow(inv)
	}
	return inv.lastHandler(inv, inv.options)
}
