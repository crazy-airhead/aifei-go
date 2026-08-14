package flow

import "sync"

// Container looks up task/condition components by name (the target of a `@name`
// reference in a node's task/when). The default MapContainer is an in-process map;
// applications may provide their own (e.g. backed by a DI container).
// Mirrors Java's Container.
type Container interface {
	// GetComponent returns the component registered under name (nil if absent).
	GetComponent(name string) any
}

// MapContainer is a thread-safe in-memory Container. Components may be TaskComponent,
// ConditionComponent, or any value the driver knows how to invoke.
// Mirrors Java's MapContainer.
type MapContainer struct {
	mu    sync.RWMutex
	comps map[string]any
}

// NewMapContainer creates an empty MapContainer.
func NewMapContainer() *MapContainer {
	return &MapContainer{comps: make(map[string]any)}
}

// PutComponent registers (or replaces) a component under key.
func (c *MapContainer) PutComponent(key string, component any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.comps[key] = component
}

// RemoveComponent removes a component by key.
func (c *MapContainer) RemoveComponent(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.comps, key)
}

// GetComponent returns a component by key (nil if absent).
func (c *MapContainer) GetComponent(key string) any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.comps[key]
}
