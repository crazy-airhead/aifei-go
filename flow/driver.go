package flow

// Driver controls execution semantics, like a JDBC driver. The engine calls
// OnNodeStart/OnNodeEnd around task execution, and HandleCondition/HandleTask to
// evaluate conditions and run tasks. Mirrors Java's FlowDriver.
type Driver interface {
	// Executor returns an optional async submit function for parallel-gateway
	// branches. nil means run sequentially (the default).
	Executor() func(fn func())

	// OnNodeStart is fired by the engine when a node is entered (before its task).
	OnNodeStart(ex *Exchanger, node *Node)

	// OnNodeEnd is fired by the engine when a node is left (after its task).
	OnNodeEnd(ex *Exchanger, node *Node)

	// HandleCondition evaluates a condition descriptor.
	HandleCondition(ex *Exchanger, cond ConditionDesc) (bool, error)

	// HandleTask runs a task descriptor (default delegates to PostHandleTask).
	HandleTask(ex *Exchanger, task TaskDesc) error

	// PostHandleTask actually runs the task (the default task execution).
	PostHandleTask(ex *Exchanger, task TaskDesc) error
}
