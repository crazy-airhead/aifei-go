package dami

import (
	"context"
	"fmt"
)

// Future[R] is the reply handle for a call, corresponding to Java's
// CompletableFuture<R>. The first reply wins; later ones are dropped (settling is
// idempotent). It is safe for single-producer/single-consumer use as driven by
// the bus pipeline.
type Future[R any] struct {
	done chan futureResult[R]
}

type futureResult[R any] struct {
	val R
	err error
}

// NewFuture builds an unanswered Future.
func NewFuture[R any]() *Future[R] {
	return &Future[R]{done: make(chan futureResult[R], 1)}
}

func (f *Future[R]) settle(r futureResult[R]) {
	select {
	case f.done <- r:
	default: // first reply wins
	}
}

// Resolve settles the future with a value and optional error. It is intended for
// fallback handlers (when no listener matched) and for testing; normal replies
// flow through the bus pipeline.
func (f *Future[R]) Resolve(val R, err error) {
	f.settle(futureResult[R]{val: val, err: err})
}

// Get blocks until the reply arrives or ctx is cancelled. A cancelled ctx
// returns the zero R and ctx.Err().
func (f *Future[R]) Get(ctx context.Context) (R, error) {
	select {
	case r := <-f.done:
		return r.val, r.err
	case <-ctx.Done():
		var zero R
		return zero, ctx.Err()
	}
}

// Done returns the result channel for select-based waiting. It receives exactly
// one futureResult then remains readable (buffered=1).
func (f *Future[R]) Done() <-chan futureResult[R] { return f.done }

// Then runs fn asynchronously once the reply arrives.
func (f *Future[R]) Then(fn func(R, error)) {
	go func() {
		r := <-f.done
		fn(r.val, r.err)
	}()
}

// futureSink adapts a *Future[R] to the Sink interface for call replies.
// Next asserts the value is R (a sender/listener type convention); a mismatch
// settles the future with an error rather than panicking.
type futureSink[R any] struct{ fut *Future[R] }

func (s *futureSink[R]) Next(v any) {
	if v == nil {
		// A nil reply (e.g. a void provider method, or a nil pointer return)
		// maps to R's zero value rather than a type mismatch.
		var zero R
		s.fut.settle(futureResult[R]{val: zero})
		return
	}
	r, ok := v.(R)
	if !ok {
		var zero R
		s.fut.settle(futureResult[R]{err: fmt.Errorf("dami: call reply type mismatch: got %T, want %T", v, zero)})
		return
	}
	s.fut.settle(futureResult[R]{val: r})
}

func (s *futureSink[R]) Complete(err error) {
	s.fut.settle(futureResult[R]{err: err})
}
