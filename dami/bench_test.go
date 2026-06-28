package dami

import "testing"

// BenchmarkSend measures single-listener broadcast throughput — the Go analog
// of dami's benchmark/SendTest, which targets the 50M/s range on the reference
// machine. Absolute numbers are machine-dependent; the goal is to stay in the
// same order of magnitude (no allocation-heavy dispatch path).

func BenchmarkSend(b *testing.B) {
	bus := New()
	ListenOn(bus, "bench", func(e *Event[any]) error { return nil })
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = SendOn[any](bus, "bench", nil)
	}
}

func BenchmarkSendNoListener(b *testing.B) {
	// No listener → exercises the fallback/not-handled path (no distribution).
	bus := New()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = SendOn[any](bus, "bench", nil)
	}
}

func BenchmarkSendParallel(b *testing.B) {
	bus := New()
	ListenOn(bus, "bench", func(e *Event[any]) error { return nil })
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = SendOn[any](bus, "bench", nil)
		}
	})
}

func BenchmarkHashRouterMatch(b *testing.B) {
	r := NewHashRouter()
	r.Add("bench.topic", noopHolder(0))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.Match("bench.topic")
	}
}

func BenchmarkPathRouterMatch(b *testing.B) {
	r := NewPathRouter()
	r.Add("event/*/created", noopHolder(0))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.Match("event/user/created")
	}
}
