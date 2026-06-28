package dami

import (
	"context"
	"fmt"
	"sync"
)

// StreamItem is one element of a stream, or a terminal error. An item with a
// non-nil Err is the last item before the channel closes.
type StreamItem[R any] struct {
	Val R
	Err error
}

// streamSink adapts a buffered channel to the Sink interface for stream replies.
// Finite buffering is the backpressure mechanism (a slow consumer blocks the
// producer, or the producer drops on ctx cancellation). The sink terminates
// exactly once: a non-nil completion error is delivered as a final item, then the
// channel closes.
type streamSink[R any] struct {
	ch   chan StreamItem[R]
	ctx  context.Context
	done chan struct{}
	once sync.Once
}

func (s *streamSink[R]) Next(v any) {
	r, ok := v.(R)
	if !ok {
		var zero R
		s.Complete(fmt.Errorf("dami: stream item type mismatch: got %T, want %T", v, zero))
		return
	}
	s.push(StreamItem[R]{Val: r})
}

// Complete ends the stream. A non-nil err is delivered as a final item before the
// channel closes; nil just closes. Idempotent via once.
func (s *streamSink[R]) Complete(err error) {
	s.once.Do(func() {
		if err != nil {
			select {
			case s.ch <- StreamItem[R]{Err: err}:
			case <-s.ctx.Done():
			}
		}
		close(s.done)
		close(s.ch)
	})
}

// cancel ends the stream without a final error item — used when the consumer's
// context expired; the consumer learns of cancellation via ctx.Err() directly,
// not via a stream item.
func (s *streamSink[R]) cancel() {
	s.once.Do(func() {
		close(s.done)
		close(s.ch)
	})
}

// push delivers one item, yielding to ctx cancellation or stream termination.
func (s *streamSink[R]) push(it StreamItem[R]) {
	select {
	case s.ch <- it:
	case <-s.done:
	case <-s.ctx.Done():
	}
}

// StreamOn sends a streaming event on b and returns a read-only channel of
// items. The handler (registered via ListenStream) pushes items through the
// payload's Sink; the channel closes when the handler completes. ctx cancels the
// stream (closes the channel with ctx.Err()). Mirrors Java bus.stream(topic,
// data) over a bounded channel instead of the Reactive Streams protocol.
//
// Handlers are expected to push synchronously within dispatch (like Java's
// onStream); a handler that returns without calling Complete leaves the channel
// open until ctx cancellation.
func StreamOn[D, R any](b *Bus, ctx context.Context, topic string, data D, fallback ...func(<-chan StreamItem[R])) <-chan StreamItem[R] {
	assertTopic(topic)
	ch := make(chan StreamItem[R], 16)
	sink := &streamSink[R]{ch: ch, ctx: ctx, done: make(chan struct{})}
	ev := &Event[*RequestPayload[D]]{
		Topic:   topic,
		Payload: &RequestPayload[D]{Data: data, Sink: sink},
	}
	_ = b.dispatcher.Dispatch(ev, b.router)
	if !ev.handled {
		for _, fb := range fallback {
			if fb != nil {
				fb(ch)
			}
		}
	}
	go func() {
		<-ctx.Done()
		sink.cancel()
	}()
	return ch
}

// Stream sends a streaming event on the default bus.
func Stream[D, R any](ctx context.Context, topic string, data D, fallback ...func(<-chan StreamItem[R])) <-chan StreamItem[R] {
	return StreamOn[D, R](defaultBus, ctx, topic, data, fallback...)
}

// StreamSink is the typed handle a stream handler pushes results through. It
// wraps the event's Sink so the handler works with concrete R values.
type StreamSink[R any] struct {
	s Sink
}

// Next pushes one stream element.
func (ss StreamSink[R]) Next(v R) { ss.s.Next(v) }

// Complete ends the stream; a non-nil error fails it (the consumer sees a final
// StreamItem with that Err before the channel closes).
func (ss StreamSink[R]) Complete(err error) { ss.s.Complete(err) }

// ListenStreamOn registers a streaming handler on b. The handler receives the
// request data and a StreamSink[R] to push items; it must call Complete when
// done. Mirrors Java's StreamEventListener /
// bus.listen(topic, (event, att, data, sink) -> ...).
func ListenStreamOn[D, R any](b *Bus, topic string, handler func(D, StreamSink[R]), index ...int) (unlisten func()) {
	return ListenOn(b, topic, func(ev *Event[*RequestPayload[D]]) error {
		handler(ev.Payload.Data, StreamSink[R]{s: ev.Payload.Sink})
		return nil
	}, index...)
}

// ListenStream registers a streaming handler on the default bus.
func ListenStream[D, R any](topic string, handler func(D, StreamSink[R]), index ...int) (unlisten func()) {
	return ListenStreamOn[D, R](defaultBus, topic, handler, index...)
}
