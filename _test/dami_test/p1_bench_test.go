package dami_test

import (
	"context"
	"testing"

	"github.com/crazy-airhead/aifei-go/dami"
)

// BenchmarkCall measures synchronous request-response throughput (one handler,
// single typed reply) over the call pipeline.

func BenchmarkCall(b *testing.B) {
	bus := dami.New()
	dami.ListenCallOn(bus, "bench", func(data int) (int, error) { return data, nil })
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = dami.CallOn[int, int](bus, "bench", 1).Get(ctx)
	}
}

func BenchmarkLpcCall(b *testing.B) {
	bus := dami.New()
	lpc := dami.NewLpc(bus)
	if err := lpc.RegisterProvider("bench", &benchSvc{}); err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = dami.Call1[int](bus, ctx, "bench.Double", 1)
	}
}

type benchSvc struct{}

func (s *benchSvc) Double(n int) int { return n * 2 }
