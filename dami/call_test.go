package dami

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCallRequestResponse(t *testing.T) {
	b := New()
	ListenCallOn(b, "demo.hello", func(data string) (string, error) {
		return "hi:" + data, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r, err := CallOn[string, string](b, "demo.hello", "world").Get(ctx)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if r != "hi:world" {
		t.Fatalf("r=%q", r)
	}
}

func TestCallError(t *testing.T) {
	b := New()
	ListenCallOn(b, "op", func(data int) (int, error) {
		return 0, errors.New("boom")
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r, err := CallOn[int, int](b, "op", 1).Get(ctx)
	if err == nil || err.Error() != "boom" {
		t.Fatalf("err=%v r=%v", err, r)
	}
	if r != 0 {
		t.Fatalf("r=%v want 0", r)
	}
}

func TestCallFallback(t *testing.T) {
	b := New()
	// No handler → fallback supplies a default reply.
	fut := CallOn[string, string](b, "nope", "x", func(f *Future[string]) {
		f.Resolve("default", nil)
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r, err := fut.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if r != "default" {
		t.Fatalf("r=%q want default", r)
	}
}

func TestCallCompute(t *testing.T) {
	b := New()
	ListenCallOn(b, "op", func(data int) (int, error) { return data * 2, nil })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r, err := CallOn[int, int](b, "op", 21).Get(ctx)
	if err != nil || r != 42 {
		t.Fatalf("r=%d err=%v", r, err)
	}
}

func TestCallTimeout(t *testing.T) {
	b := New()
	// Handler never replies (synchronous dispatch runs it, but it does not call
	// the sink — emulated by a handler that returns without replying via a
	// custom sink). Easiest: no handler at all → Future stays unsettled.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	r, err := CallOn[int, int](b, "nobody", 1).Get(ctx)
	if err != context.DeadlineExceeded {
		t.Fatalf("err=%v want DeadlineExceeded", err)
	}
	if r != 0 {
		t.Fatalf("r=%v want 0", r)
	}
}

func TestCallDefaultBus(t *testing.T) {
	topic := "test.call.default.bus"
	un := ListenCall(topic, func(data int) (int, error) { return data + 1, nil })
	defer un()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r, err := Call[int, int](topic, 10).Get(ctx)
	if err != nil || r != 11 {
		t.Fatalf("r=%d err=%v", r, err)
	}
}
