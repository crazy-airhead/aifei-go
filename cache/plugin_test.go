package cache

import "testing"

func TestPluginNoProps(t *testing.T) {
	SetDefault(nil)
	p, err := NewPlugin(nil, nil)
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}
	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if p.Manager() != nil {
		t.Error("Manager should be nil without props")
	}
	if DefaultManager() != nil {
		t.Error("no default should be installed")
	}
	if err := p.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}
