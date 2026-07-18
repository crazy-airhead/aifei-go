package dami_test

import (
	"testing"

	"github.com/crazy-airhead/aifei-go/dami"
)

// BenchmarkSend measures single-listener broadcast throughput — the Go analog
// of dami's benchmark/SendTest, which targets the 50M/s range on the reference
// machine. Absolute numbers are machine-dependent; the goal is to stay in the
// same order of magnitude (no allocation-heavy dispatch path).

func BenchmarkSend(b *testing.B) {
	bus := dami.New()
	dami.ListenOn(bus, "bench", func(e *dami.Event[any]) error { return nil })
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = dami.SendOn[any](bus, "bench", nil)
	}
}

func BenchmarkSendNoListener(b *testing.B) {
	// No listener → exercises the fallback/not-handled path (no distribution).
	bus := dami.New()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = dami.SendOn[any](bus, "bench", nil)
	}
}

func BenchmarkSendParallel(b *testing.B) {
	bus := dami.New()
	dami.ListenOn(bus, "bench", func(e *dami.Event[any]) error { return nil })
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = dami.SendOn[any](bus, "bench", nil)
		}
	})
}
