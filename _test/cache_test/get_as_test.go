package cache_test

import (
	"context"
	"testing"

	"github.com/crazy-airhead/aifei-go/plugins/cache"
)

type asUser struct {
	Name string
	Age  int
}

func setupAsDefault(t *testing.T) {
	t.Helper()
	cache.SetDefault(nil)
	mgr, err := cache.NewManager(&cache.Config{
		Default: "as",
		Instances: map[string]cache.InstanceConfig{
			"as": {
				Type:  string(cache.CacheLocal),
				Local: &cache.LocalConfig{Driver: string(cache.LocalFreeCache), Size: 1 << 20, TTL: 60},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	cache.SetDefault(mgr)
	t.Cleanup(func() { mgr.Close() })
	t.Cleanup(func() { cache.SetDefault(nil) })
}

func TestGetAs(t *testing.T) {
	setupAsDefault(t)
	ctx := context.Background()

	if err := cache.Set(ctx, "u1", asUser{Name: "james", Age: 18}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	u, found, err := cache.GetAs[asUser](ctx, "u1")
	if err != nil || !found {
		t.Fatalf("GetAs = (found %v, err %v)", found, err)
	}
	if u.Name != "james" || u.Age != 18 {
		t.Errorf("GetAs = %+v, want {james 18}", u)
	}

	// Miss: zero T, found=false, nil error (same contract as Get)
	miss, found, err := cache.GetAs[asUser](ctx, "absent")
	if err != nil || found || miss.Name != "" {
		t.Errorf("GetAs miss = (%+v, found %v, err %v)", miss, found, err)
	}
}

func TestGetOrStoreAs(t *testing.T) {
	setupAsDefault(t)
	ctx := context.Background()

	loads := 0
	u, err := cache.GetOrStoreAs[asUser](ctx, "u2", func(ctx context.Context) (asUser, error) {
		loads++
		return asUser{Name: "bond", Age: 42}, nil
	})
	if err != nil {
		t.Fatalf("GetOrStoreAs: %v", err)
	}
	if u.Name != "bond" || u.Age != 42 {
		t.Errorf("GetOrStoreAs = %+v, want {bond 42}", u)
	}

	// Second call is served from cache: loader not re-run
	cached, err := cache.GetOrStoreAs[asUser](ctx, "u2", func(ctx context.Context) (asUser, error) {
		loads++
		return asUser{}, nil
	})
	if err != nil || cached.Name != "bond" {
		t.Errorf("GetOrStoreAs cached = (%+v, %v)", cached, err)
	}
	if loads != 1 {
		t.Errorf("loader ran %d times, want 1", loads)
	}
}
