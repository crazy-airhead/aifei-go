package flow

import "github.com/crazy-airhead/aifei-go/dami"

// Context is the per-instance flow context: the variable domain, trace, stop/
// interrupt control, and the event bus. It is the Go equivalent of Java's
// FlowContext. Build one with NewContext; recover from a snapshot with
// ContextFromJSON (see context_impl.go).
type Context interface {
	// InstanceID returns the flow instance id (read from the "instanceId" var).
	InstanceID() string

	// Vars returns the shared variable domain (mutable map).
	Vars() map[string]any

	// Put sets a variable (no-op for nil values); returns ctx for chaining.
	Put(key string, value any) Context
	// PutIfAbsent sets a variable only if absent.
	PutIfAbsent(key string, value any) Context
	// Get reads a variable (nil if absent).
	Get(key string) any
	// GetAs reads a variable (untyped convenience; Java getAs<T>).
	GetAs(key string) any
	// GetOrDefault reads a variable, or def when absent.
	GetOrDefault(key string, def any) any
	// ContainsKey reports whether the key exists.
	ContainsKey(key string) bool
	// Remove removes a variable.
	Remove(key string)

	// With runs fn with key temporarily set to value, restoring the prior value
	// afterwards (removing it if it was absent).
	With(key string, value any, fn func() error) error

	// Stop stops the whole flow; IsStopped reports whether it was stopped.
	Stop()
	IsStopped() bool
	// Interrupt interrupts only the current branch.
	Interrupt()

	// Trace returns the trace; EnableTrace toggles tracing (on by default).
	Trace() *Trace
	EnableTrace(enable bool) Context
	// LastRecord / LastNodeID return the root graph's last-executed node info.
	LastRecord() *NodeRecord
	LastNodeID() string

	// EventBus returns the instance event bus (built on dami).
	EventBus() *dami.Bus

	// ToJSON serializes the context (vars + stopped + trace) for snapshot
	// persistence; ContextFromJSON reverses it.
	ToJSON() string
}
