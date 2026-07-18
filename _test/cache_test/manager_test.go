package cache_test

import (
	"testing"

	"github.com/crazy-airhead/aifei-go/plugins/cache"
)

func TestNewManagerFallback(t *testing.T) {
	cache.SetDefault(nil)
	mgr, err := cache.NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager(nil): %v", err)
	}
	defer mgr.Close()

	if len(mgr.Names()) != 1 {
		t.Errorf("Names = %v, want 1 entry", mgr.Names())
	}
	if mgr.Default() == nil {
		t.Error("Default nil")
	}
	// defaultCacheName ("cache") is the instance name used when none is
	// configured; it is unexported, so use the literal here.
	if mgr.Instance("") == nil || mgr.Instance("cache") == nil {
		t.Error("Instance lookup failed")
	}
	if mgr.Instance("nope") != nil {
		t.Error("unknown instance should be nil")
	}
}

func TestNewManagerRemoteError(t *testing.T) {
	cache.SetDefault(nil)
	cfg := &cache.Config{
		Instances: map[string]cache.InstanceConfig{
			"bad": {Type: string(cache.CacheRemote)}, // no remote config
		},
	}
	if _, err := cache.NewManager(cfg, nil); err == nil {
		t.Fatal("expected error for remote instance without remote config")
	}
}
