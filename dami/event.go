package dami

// Event is the in-process event carrier: a topic, a typed payload, a shared
// attachment map, and a handled flag. It mirrors Java DamiBus's Event<P>.
//
// All listeners matched for a topic receive the SAME *Event pointer, so they can
// cooperate via Attach and the bus records Handled once. The generic parameter P
// gives a single send/listen site compile-time convenience; matching the same P
// across independent Send/Listen calls is a runtime convention (as in Java),
// enforced by a type assertion on dispatch — a mismatch panics, just as Java
// throws a ClassCastException.
type Event[P any] struct {
	Topic   string
	Payload P
	Attach  map[string]any

	handled bool
	sink    sink // reserved for P1 call/stream; nil in P0
}

// AttachMap returns the attachment map, lazily allocating it. Use it when a
// listener needs to read attachments written by an earlier-ordered listener.
func (e *Event[P]) AttachMap() map[string]any {
	if e.Attach == nil {
		e.Attach = make(map[string]any)
	}
	return e.Attach
}

// Handled reports whether at least one listener matched and processed the event.
// When false, Send invokes its fallback (if any).
func (e *Event[P]) Handled() bool { return e.handled }

func (e *Event[P]) markHandled() { e.handled = true }

// eventView is the non-generic, package-private view of an event used by the
// interceptor chain, which must read any event regardless of its payload type P.
// Every *Event[P] satisfies it; the view shares the underlying event, so an
// interceptor mutates the very object listeners later see.
type eventView interface {
	viewTopic() string
	viewPayload() any
	viewAttach() map[string]any
	markHandled()
}

func (e *Event[P]) viewTopic() string { return e.Topic }
func (e *Event[P]) viewPayload() any  { return any(e.Payload) }
func (e *Event[P]) viewAttach() map[string]any {
	if e.Attach == nil {
		e.Attach = make(map[string]any)
	}
	return e.Attach
}

// sink is the result receiver reserved for P1 call/stream. It is unused in P0
// (Event.sink stays nil) but defined here so the Event shape is stable for later
// phases without an API break.
type sink interface {
	next(v any)
	complete(err error)
}

// assertTopic panics on an empty topic, mirroring Java's AssertUtil.assertTopic.
// An empty topic is a programming error, not a runtime condition.
func assertTopic(topic string) {
	if topic == "" {
		panic("dami: topic must not be empty")
	}
}
