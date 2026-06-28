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
//
// For call/stream, P is *RequestPayload[D] and the reply channel lives on that
// payload (Payload.Sink), not on the event.
type Event[P any] struct {
	Topic   string
	Payload P
	Attach  map[string]any

	handled bool
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

// Sink is the reply channel carried by call/stream payloads. A listener pushes
// results via Next and signals completion (with an optional terminal error) via
// Complete. It corresponds to Java's CompletableFuture (call) / Subscriber
// (stream) sink, unified behind one interface.
type Sink interface {
	// Next pushes one result value. For a call the first value is the reply; for
	// a stream each value is one element.
	Next(v any)
	// Complete ends the exchange; a non-nil error marks it failed.
	Complete(err error)
}

// assertTopic panics on an empty topic, mirroring Java's AssertUtil.assertTopic.
// An empty topic is a programming error, not a runtime condition.
func assertTopic(topic string) {
	if topic == "" {
		panic("dami: topic must not be empty")
	}
}
