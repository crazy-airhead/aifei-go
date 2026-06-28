package dami

// RequestPayload carries request data D and the reply Sink, used by both call
// and stream events. Java splits CallPayload/StreamPayload by sink type
// (CompletableFuture vs Subscriber); the unified Sink interface lets us use one
// payload type. For a call the payload's topic listener replies once; for a
// stream it pushes many items then completes.
type RequestPayload[D any] struct {
	Data D
	Sink Sink
}
