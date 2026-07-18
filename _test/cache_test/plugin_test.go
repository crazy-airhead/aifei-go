package cache_test

import (
	"testing"

	"github.com/crazy-airhead/aifei-go/plugins/cache"
)

func TestPluginStartWithEmptyConfig(t *testing.T) {
	cache.SetDefault(nil)
	p, err := cache.NewPlugin(nil)
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
