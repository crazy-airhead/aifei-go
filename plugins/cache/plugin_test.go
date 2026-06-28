package cache

import "testing"

func TestPluginStartWithEmptyConfig(t *testing.T) {
	SetDefault(nil)
	p, err := NewPlugin(nil)
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}
	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Start with empty global config still creates a Manager.
	if p.Manager() == nil {
		t.Error("Manager should not be nil after Start")
	}
	if err := p.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}
