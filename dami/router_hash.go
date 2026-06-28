package dami

import "sync"

// HashRouter matches by exact topic string via a map (the default, fastest).
// Mirrors Java's HashTopicEventRouter.
type HashRouter struct {
	mu        sync.RWMutex
	pipelines map[string]*pipeline
}

// NewHashRouter builds an empty HashRouter.
func NewHashRouter() Router {
	return &HashRouter{pipelines: make(map[string]*pipeline)}
}

// Add implements Router.
func (r *HashRouter) Add(topic string, h *holder) {
	r.mu.Lock()
	p, ok := r.pipelines[topic]
	if !ok {
		p = newPipeline()
		r.pipelines[topic] = p
	}
	r.mu.Unlock()
	p.add(h)
}

// Remove implements Router.
func (r *HashRouter) Remove(topic string, h *holder) {
	r.mu.RLock()
	p := r.pipelines[topic]
	r.mu.RUnlock()
	if p != nil {
		p.remove(h)
	}
}

// RemoveAll implements Router.
func (r *HashRouter) RemoveAll(topic string) {
	r.mu.Lock()
	delete(r.pipelines, topic)
	r.mu.Unlock()
}

// Match implements Router.
func (r *HashRouter) Match(topic string) []*holder {
	r.mu.RLock()
	p := r.pipelines[topic]
	r.mu.RUnlock()
	if p == nil {
		return nil
	}
	return p.snapshot()
}

// Count implements Router.
func (r *HashRouter) Count(topic string) int { return len(r.Match(topic)) }

// ClearAll implements Router.
func (r *HashRouter) ClearAll() {
	r.mu.Lock()
	clear(r.pipelines)
	r.mu.Unlock()
}
