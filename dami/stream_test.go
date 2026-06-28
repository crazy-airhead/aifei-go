package dami

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestStreamItems(t *testing.T) {
	b := New()
	ListenStreamOn(b, "gen", func(data string, sink StreamSink[int]) {
		for i := 1; i <= 3; i++ {
			sink.Next(i)
		}
		sink.Complete(nil)
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var got []int
	for it := range StreamOn[string, int](b, ctx, "gen", "go") {
		if it.Err != nil {
			t.Fatal(it.Err)
		}
		got = append(got, it.Val)
	}
	if len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Fatalf("got=%v", got)
	}
}

func TestStreamError(t *testing.T) {
	b := New()
	ListenStreamOn(b, "gen", func(data string, sink StreamSink[int]) {
		sink.Next(1)
		sink.Complete(errors.New("stream-fail"))
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var got []int
	var streamErr error
	for it := range StreamOn[string, int](b, ctx, "gen", "go") {
		if it.Err != nil {
			streamErr = it.Err
			break
		}
		got = append(got, it.Val)
	}
	if len(got) != 1 {
		t.Fatalf("got=%v want 1 item before error", got)
	}
	if streamErr == nil || streamErr.Error() != "stream-fail" {
		t.Fatalf("streamErr=%v", streamErr)
	}
}

func TestStreamCancel(t *testing.T) {
	b := New()
	// Handler pushes two items but never completes; ctx cancellation closes the
	// channel.
	ListenStreamOn(b, "gen", func(data string, sink StreamSink[int]) {
		sink.Next(1)
		sink.Next(2)
		// intentionally no Complete
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	var n int
	for range StreamOn[string, int](b, ctx, "gen", "go") {
		n++
	}
	if n != 2 {
		t.Fatalf("n=%d want 2 (items pushed before cancel closes)", n)
	}
}

func TestStreamDefaultBus(t *testing.T) {
	topic := "test.stream.default.bus"
	un := ListenStream(topic, func(data string, sink StreamSink[string]) {
		sink.Next(data + "!")
		sink.Complete(nil)
	})
	defer un()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var got []string
	for it := range Stream[string, string](ctx, topic, "hi") {
		if it.Err != nil {
			t.Fatal(it.Err)
		}
		got = append(got, it.Val)
	}
	if len(got) != 1 || got[0] != "hi!" {
		t.Fatalf("got=%v", got)
	}
}
