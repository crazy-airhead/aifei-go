package flow

// This file hosts the Go 1.27 typed reads for flow (see
// docs/arch/generic-methods.md). Context and Container are interfaces, and Go
// still forbids type parameters on interface methods, so their generic
// entries are package-level helpers; the concrete structs (Node/Link/Graph,
// Temporary) take generic methods directly.

// GetAs reads a context variable as T. ok reports both presence and type
// match; absent or mismatched variables yield the zero T.
//
//	count, ok := flow.GetAs[int](ctx, "count")
func GetAs[T any](ctx Context, key string) (T, bool) {
	return as[T](ctx.Get(key))
}

// ComponentAs returns a container component as T (ok is false when absent or
// of another type).
//
//	task, ok := flow.ComponentAs[EmailTask](ctx.Container(), "emailTask")
func ComponentAs[T any](c Container, key string) (T, bool) {
	return as[T](c.GetComponent(key))
}

// MetaAs returns the node's meta value for key as T (ok is false when absent
// or of another type).
func (n *Node) MetaAs[T any](key string) (T, bool) {
	return as[T](n.Meta(key))
}

// MetaAs returns the link's meta value for key as T (ok is false when absent
// or of another type).
func (l *Link) MetaAs[T any](key string) (T, bool) {
	return as[T](l.Meta(key))
}

// MetaAs returns the graph's meta value for key as T (ok is false when absent
// or of another type).
func (g *Graph) MetaAs[T any](key string) (T, bool) {
	return as[T](g.Meta(key))
}

// StackPeekAs returns the top of the (graphID, key) stack as T without
// removing it (zero T with ok=false when the stack is empty).
func (t *Temporary) StackPeekAs[T any](graphID, key string) (T, bool) {
	return as[T](t.StackPeek(graphID, key))
}

// StackPopAs removes and returns the top of the (graphID, key) stack as T
// (zero T with ok=false when the stack is empty).
func (t *Temporary) StackPopAs[T any](graphID, key string) (T, bool) {
	return as[T](t.StackPop(graphID, key))
}

// as asserts v into T, reporting ok for both presence and type match.
func as[T any](v any) (T, bool) {
	t, ok := v.(T)
	return t, ok
}
