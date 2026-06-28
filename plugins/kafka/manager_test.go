package kafka

import (
	"testing"
)

// These tests build real franz-go clients but never connect: kgo.NewClient dials
// lazily, so pointing brokers at closed ports still constructs successfully and
// Close() cleans up without network errors.

func TestNewManagerRouting(t *testing.T) {
	cfg := &Config{
		Default: "a",
		Clusters: map[string]ClusterConfig{
			"a": {Brokers: []string{"127.0.0.1:65531"}},
			"b": {Brokers: []string{"127.0.0.1:65532"}},
		},
	}
	m, err := NewManager(cfg, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Close()

	if got := m.Default(); got == nil || got.Name() != "a" {
		t.Errorf("default: want a, got %v", got)
	}
	if got := m.Instance("b"); got == nil || got.Name() != "b" {
		t.Errorf("instance b: want b, got %v", got)
	}
	if got := m.Instance(""); got == nil || got.Name() != "a" {
		t.Errorf("empty name should fall back to default, got %v", got)
	}
	if got := m.Instance("missing"); got != nil {
		t.Errorf("unknown instance should be nil, got %v", got)
	}
	if n := len(m.Names()); n != 2 {
		t.Errorf("names: want 2, got %d", n)
	}
}

func TestNewManagerDefaultFallback(t *testing.T) {
	// No Default set: an arbitrary cluster becomes default.
	m, err := NewManager(&Config{
		Clusters: map[string]ClusterConfig{
			"only": {Brokers: []string{"127.0.0.1:65533"}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Close()
	if got := m.Default(); got == nil || got.Name() != "only" {
		t.Errorf("default fallback wrong: %v", got)
	}
}

func TestNewManagerErrors(t *testing.T) {
	// No clusters.
	if _, err := NewManager(&Config{}, nil); err == nil {
		t.Error("want error for no clusters")
	}
	// No brokers.
	if _, err := NewManager(&Config{Clusters: map[string]ClusterConfig{"a": {}}}, nil); err == nil {
		t.Error("want error for cluster without brokers")
	}
	// Bad SASL surfaces at construction (no dial needed).
	if _, err := NewManager(&Config{Clusters: map[string]ClusterConfig{
		"a": {Brokers: []string{"127.0.0.1:1"}, SASL: &SASLConfig{Mechanism: "bogus"}},
	}}, nil); err == nil {
		t.Error("want error for unsupported sasl mechanism")
	}
}

func TestDefaultClientErrNoDefault(t *testing.T) {
	SetDefault(nil) // ensure no package default
	defer SetDefault(nil)

	c := DefaultClient()
	if _, ok := c.(errClient); !ok {
		t.Fatalf("want errClient when no default, got %T", c)
	}
	if err := c.ProduceSync(nil, NewMessage("t", nil)); err == nil || err != ErrNoDefault {
		t.Errorf("want ErrNoDefault, got %v", err)
	}
	if _, err := c.Subscribe(nil, []string{"t"}, nil); err == nil || err != ErrNoDefault {
		t.Errorf("want ErrNoDefault from Subscribe, got %v", err)
	}
	// Async Produce surfaces the error through the promise, matching franz-go.
	var got error
	c.Produce(nil, NewMessage("t", nil), func(_ *Message, err error) { got = err })
	if got != ErrNoDefault {
		t.Errorf("want ErrNoDefault via promise, got %v", got)
	}
}
