package dami

import (
	"cmp"
	"slices"
	"sync"
)

// Dispatcher runs the interceptor chain, then distributes the event to the
// matched listeners. Mirrors Java's EventDispatcher; the default implementation
// also owns the interceptor list.
type Dispatcher interface {
	// AddInterceptor registers an interceptor at an ordering position.
	AddInterceptor(index int, it Interceptor)
	// Dispatch matches targets via the router, runs interceptors, distributes,
	// and returns the first propagated listener error. It marks the event
	// handled when at least one target matched and distribution was reached.
	Dispatch(ev any, router Router) error
}

type dispatcher struct {
	mu       sync.RWMutex
	entities []*interceptorEntity
}

// NewDispatcher builds the default Dispatcher.
func NewDispatcher() Dispatcher { return &dispatcher{} }

// AddInterceptor implements Dispatcher.
func (d *dispatcher) AddInterceptor(index int, it Interceptor) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.entities = append(d.entities, &interceptorEntity{index: index, it: it})
	slices.SortFunc(d.entities, func(a, b *interceptorEntity) int {
		return cmp.Compare(a.index, b.index)
	})
}

func (d *dispatcher) snapshot() []*interceptorEntity {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]*interceptorEntity, len(d.entities))
	copy(out, d.entities)
	return out
}

// Dispatch corresponds to Java EventDispatcherDefault.dispatch: intercept →
// precheck → distribute. The event is marked handled inside the final step only
// when distribution is actually reached (mirroring Java's finally{setHandled}),
// so an interceptor that short-circuits leaves handled false.
func (d *dispatcher) Dispatch(ev any, router Router) error {
	view := ev.(eventView)
	targets := router.Match(view.viewTopic())
	if len(targets) == 0 {
		return nil
	}
	c := &chain{
		entities: d.snapshot(),
		final: func() error {
			err := distribute(ev, targets)
			view.markHandled()
			return err
		},
	}
	return c.proceed(view)
}

// distribute invokes each target in index order, stopping at the first error —
// the decided first-error-stops conduction strategy (transaction propagation).
func distribute(ev any, targets []*holder) error {
	for _, h := range targets {
		if err := h.handle(ev); err != nil {
			return err
		}
	}
	return nil
}
