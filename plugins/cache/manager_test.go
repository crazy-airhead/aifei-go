package cache

import "testing"

func TestNewManagerFallback(t *testing.T) {
	SetDefault(nil)
	mgr, err := NewManager(nil, nil)
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
	if mgr.Instance("") == nil || mgr.Instance(defaultCacheName) == nil {
		t.Error("Instance lookup failed")
	}
	if mgr.Instance("nope") != nil {
		t.Error("unknown instance should be nil")
	}
}

func TestNewManagerRemoteError(t *testing.T) {
	SetDefault(nil)
	cfg := &Config{
		Instances: map[string]InstanceConfig{
			"bad": {Type: string(CacheRemote)}, // no remote config
		},
	}
	if _, err := NewManager(cfg, nil); err == nil {
		t.Fatal("expected error for remote instance without remote config")
	}
}
