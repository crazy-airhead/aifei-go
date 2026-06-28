package dami

// CallOn sends a request-response event on b and returns a Future for the reply.
// Dispatch is synchronous: a handler registered with ListenCall runs (and should
// reply) before CallOn returns, so for a synchronous handler the Future is
// already settled. Collect the reply with Future.Get(ctx); ctx bounds the wait
// when a handler replies asynchronously. Mirrors Java bus.call(topic, data).
//
// fallback runs only when no handler matched (Handled==false), mirroring Java's
// call fallback.
func CallOn[D, R any](b *Bus, topic string, data D, fallback ...func(*Future[R])) *Future[R] {
	assertTopic(topic)
	fut := NewFuture[R]()
	ev := &Event[*RequestPayload[D]]{
		Topic:   topic,
		Payload: &RequestPayload[D]{Data: data, Sink: &futureSink[R]{fut: fut}},
	}
	_ = b.dispatcher.Dispatch(ev, b.router)
	if !ev.handled {
		for _, fb := range fallback {
			if fb != nil {
				fb(fut)
			}
		}
	}
	return fut
}

// Call sends a request-response event on the default bus.
func Call[D, R any](topic string, data D, fallback ...func(*Future[R])) *Future[R] {
	return CallOn[D, R](defaultBus, topic, data, fallback...)
}

// ListenCallOn registers a typed request-response handler on b. The handler
// returns (R, error): on success the result is pushed to the call's Future, on
// error the error is propagated through it. Mirrors Java's CallEventListener /
// bus.listen(topic, (event, data, sink) -> sink.complete(r)).
//
// R is a runtime convention shared with the caller's Call[D,R]: both sides must
// agree on R for the topic (a mismatch surfaces as a reply-type error in Get).
func ListenCallOn[D, R any](b *Bus, topic string, handler func(D) (R, error), index ...int) (unlisten func()) {
	return ListenOn(b, topic, func(ev *Event[*RequestPayload[D]]) error {
		r, err := handler(ev.Payload.Data)
		if err != nil {
			ev.Payload.Sink.Complete(err)
			return nil
		}
		ev.Payload.Sink.Next(r)
		return nil
	}, index...)
}

// ListenCall registers a typed request-response handler on the default bus.
func ListenCall[D, R any](topic string, handler func(D) (R, error), index ...int) (unlisten func()) {
	return ListenCallOn[D, R](defaultBus, topic, handler, index...)
}
