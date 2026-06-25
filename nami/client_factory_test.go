package nami

import (
	"testing"
)

func TestClientFactory(t *testing.T) {
	f := NewClientFactory().
		ServiceName("test-svc").
		Timeout(10).
		FilterAdd(FilterFunc(func(inv *Invocation) (*Result, error) {
			return inv.Invoke()
		}))

	c1 := f.For("/api/v1/users")
	c2 := f.For("/api/v1/orders")

	if c1.Config().Name() != "test-svc" {
		t.Errorf("c1 name = %q, want test-svc", c1.Config().Name())
	}
	if c1.Config().Timeout() != 10 {
		t.Errorf("c1 timeout = %d, want 10", c1.Config().Timeout())
	}
	if c1.Config().Path() != "/api/v1/users" {
		t.Errorf("c1 path = %q, want /api/v1/users", c1.Config().Path())
	}
	if len(c1.Config().Filters()) != 1 {
		t.Errorf("c1 filters len = %d, want 1", len(c1.Config().Filters()))
	}

	if c2.Config().Name() != "test-svc" {
		t.Errorf("c2 name = %q, want test-svc", c2.Config().Name())
	}
	if c2.Config().Path() != "/api/v1/orders" {
		t.Errorf("c2 path = %q, want /api/v1/orders", c2.Config().Path())
	}

	// Mutating c1's path should not affect c2
	c1.Config().SetPath("/modified")
	if c2.Config().Path() != "/api/v1/orders" {
		t.Errorf("c2 path after c1 mutation = %q, want /api/v1/orders", c2.Config().Path())
	}
}

func TestClientFactoryServiceNameInherited(t *testing.T) {
	// For uses the factory's service name — no need to repeat it per call
	f1 := NewClientFactory().Name("svc-a")
	f2 := NewClientFactory().ServiceName("svc-b")

	c1 := f1.For("/a")
	c2 := f2.For("/b")

	if c1.Config().Name() != "svc-a" {
		t.Errorf("c1 name = %q, want svc-a", c1.Config().Name())
	}
	if c2.Config().Name() != "svc-b" {
		t.Errorf("c2 name = %q, want svc-b", c2.Config().Name())
	}
}

func TestClientFactoryWithUpstream(t *testing.T) {
	u := func() string { return "http://localhost:8080" }
	f := NewClientFactory().
		ServiceName("test-svc").
		Upstream(u).
		Timeout(5)

	c := f.For("/api/test")
	if c.Config().Upstream() == nil {
		t.Fatal("upstream should not be nil")
	}
	if c.Config().Upstream()() != "http://localhost:8080" {
		t.Errorf("upstream resolved to %q", c.Config().Upstream()())
	}
	if c.Config().Name() != "test-svc" {
		t.Errorf("name = %q, want test-svc", c.Config().Name())
	}
	if c.Config().Path() != "/api/test" {
		t.Errorf("path = %q, want /api/test", c.Config().Path())
	}
}

func TestClientFactoryHeaderInheritance(t *testing.T) {
	f := NewClientFactory().
		HeaderSet("X-Custom", "shared")

	c := f.For("/api/test")
	if c.Config().HeaderGet("X-Custom") != "shared" {
		t.Errorf("header X-Custom = %q, want shared", c.Config().HeaderGet("X-Custom"))
	}

	// Mutate the clone — factory stays unchanged
	c.Config().HeaderSet("X-Custom", "overridden")
	if f.Config().HeaderGet("X-Custom") != "shared" {
		t.Errorf("factory header after clone mutation = %q, want shared", f.Config().HeaderGet("X-Custom"))
	}
}

func TestClientFactoryMultipleFilters(t *testing.T) {
	f := NewClientFactory().
		FilterAdd(FilterFunc(func(inv *Invocation) (*Result, error) { return inv.Invoke() })).
		FilterAdd(FilterFunc(func(inv *Invocation) (*Result, error) { return inv.Invoke() }))

	c := f.For("/api/test")
	if len(c.Config().Filters()) != 2 {
		t.Errorf("expected 2 filters, got %d", len(c.Config().Filters()))
	}

	// Add a filter to the clone — factory stays unchanged
	c.Config().FilterAdd(FilterFunc(func(inv *Invocation) (*Result, error) { return inv.Invoke() }))
	if len(c.Config().Filters()) != 3 {
		t.Errorf("clone should have 3 filters, got %d", len(c.Config().Filters()))
	}
	if len(f.Config().Filters()) != 2 {
		t.Errorf("factory should still have 2 filters, got %d", len(f.Config().Filters()))
	}
}
