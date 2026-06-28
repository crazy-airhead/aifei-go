package dami

// Bus is the DamiBus Go counterpart. It is a concrete struct (not an interface)
// matching aifei's style.
//
// The payload-typed Send/Listen APIs are generic TOP-LEVEL functions (SendOn /
// ListenOn), not Bus methods: Go forbids type parameters on methods. Non-generic
// operations (Router / Intercept / UnlistenAll) remain ordinary methods. Use New
// to create a Bus; the zero value is not usable.
type Bus struct {
	router     Router
	dispatcher Dispatcher
}

// Option configures a Bus.
type Option func(*Bus)

// WithRouter sets the topic router (default HashRouter).
func WithRouter(r Router) Option {
	return func(b *Bus) {
		if r != nil {
			b.router = r
		}
	}
}

// WithDispatcher sets the dispatcher (default NewDispatcher()).
func WithDispatcher(d Dispatcher) Option {
	return func(b *Bus) {
		if d != nil {
			b.dispatcher = d
		}
	}
}

// New builds a Bus with the given options applied.
func New(opts ...Option) *Bus {
	b := &Bus{
		router:     NewHashRouter(),
		dispatcher: NewDispatcher(),
	}
	for _, o := range opts {
		o(b)
	}
	return b
}

// Router returns the bus's topic router.
func (b *Bus) Router() Router { return b.router }

// Intercept registers an event interceptor at an ordering position, mirroring
// Java DamiBus.intercept(index, interceptor).
func (b *Bus) Intercept(index int, it Interceptor) {
	b.dispatcher.AddInterceptor(index, it)
}

// UnlistenAll removes every listener under a topic. Mirrors Java
// DamiBus.unlisten(topic).
func (b *Bus) UnlistenAll(topic string) { b.router.RemoveAll(topic) }

// SendOn broadcasts an event on b to every matching listener in index order.
// (A top-level generic function — Go methods cannot carry type parameters.) It
// returns the event (Handled set when a listener matched) and the first
// propagated listener error. fallback runs only when no listener matched
// (Handled==false), mirroring Java DamiBus.send(topic, payload, fallback).
func SendOn[P any](b *Bus, topic string, payload P, fallback ...func(P)) (*Event[P], error) {
	assertTopic(topic)
	ev := &Event[P]{Topic: topic, Payload: payload}
	err := b.dispatcher.Dispatch(ev, b.router)
	if !ev.handled {
		for _, fb := range fallback {
			if fb != nil {
				fb(ev.Payload)
			}
		}
	}
	return ev, err
}

// ListenOn registers a typed listener on b; index (optional) orders listeners
// ascending. It returns an unlisten function. (Top-level generic function.)
func ListenOn[P any](b *Bus, topic string, listener Listener[P], index ...int) (unlisten func()) {
	idx := 0
	if len(index) > 0 {
		idx = index[0]
	}
	h := newHolder(idx, listener)
	b.router.Add(topic, h)
	return func() { b.router.Remove(topic, h) }
}
