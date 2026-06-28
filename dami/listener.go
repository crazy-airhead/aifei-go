package dami

import (
	"cmp"
	"slices"
	"sync"
)

// Listener handles a typed event and returns an error. A non-nil error aborts
// the dispatch of remaining listeners and propagates out of Send (per the
// decided exception-conduction strategy: first error stops and returns).
type Listener[P any] func(*Event[P]) error

// holder is the type-erased listener record stored by routers. handle reasserts
// the event back to *Event[P] for the listener that created it; the assertion
// succeeds when sender and listener agree on P for the topic.
type holder struct {
	index  int
	handle func(ev any) error
}

func newHolder[P any](index int, listener Listener[P]) *holder {
	return &holder{
		index: index,
		handle: func(ev any) error {
			return listener(ev.(*Event[P]))
		},
	}
}

// pipeline is an index-ordered set of holders for one routing bucket. Add
// re-sorts by index ascending, mirroring Java's EventListenPipeline.
type pipeline struct {
	mu      sync.RWMutex
	holders []*holder
}

func newPipeline() *pipeline { return &pipeline{} }

func (p *pipeline) add(h *holder) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.holders = append(p.holders, h)
	slices.SortFunc(p.holders, func(a, b *holder) int { return cmp.Compare(a.index, b.index) })
}

func (p *pipeline) remove(h *holder) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.holders = slices.DeleteFunc(p.holders, func(x *holder) bool { return x == h })
}

func (p *pipeline) empty() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.holders) == 0
}

// snapshot returns a copy of the holders slice so callers iterate without holding
// the lock and without racing concurrent add/remove (the same concern Java's
// dispatcher addresses with an index-based for-loop).
func (p *pipeline) snapshot() []*holder {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]*holder, len(p.holders))
	copy(out, p.holders)
	return out
}
