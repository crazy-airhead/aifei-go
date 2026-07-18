package cache_test

import (
	"context"
	"errors"
	"testing"

	"github.com/crazy-airhead/aifei-go/plugins/cache"
)

func TestLocalCacheRoundTrip(t *testing.T) {
	c, err := cache.NewCache("test-local", cache.InstanceConfig{
		Type:  string(cache.CacheLocal),
		TTL:   60,
		Local: &cache.LocalConfig{Driver: string(cache.LocalFreeCache), Size: 1 << 20, TTL: 60},
	})
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	if c.CacheType() != "local" {
		t.Errorf("CacheType = %q, want local", c.CacheType())
	}
	if err := c.Set(ctx, "k1", "v1"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	var got string
	found, err := c.Get(ctx, "k1", &got)
	if err != nil || !found {
		t.Fatalf("Get: found=%v err=%v", found, err)
	}
	if got != "v1" {
		t.Errorf("Get = %q, want v1", got)
	}
	if !c.Exists(ctx, "k1") {
		t.Error("Exists k1 = false, want true")
	}

	// miss is not an error and not found.
	found, err = c.Get(ctx, "missing", new(string))
	if err != nil {
		t.Errorf("Get missing err = %v, want nil", err)
	}
	if found {
		t.Error("missing should not be found")
	}
	if c.Exists(ctx, "missing") {
		t.Error("Exists missing = true, want false")
	}

	if err := c.Delete(ctx, "k1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if c.Exists(ctx, "k1") {
		t.Error("Exists after delete = true, want false")
	}
	// deleting a missing key is not an error.
	if err := c.Delete(ctx, "missing"); err != nil {
		t.Errorf("Delete missing: %v", err)
	}
}

func TestLocalGetOrStore(t *testing.T) {
	c, err := cache.NewCache("test-local-gos", cache.InstanceConfig{
		Type:  string(cache.CacheLocal),
		Local: &cache.LocalConfig{Driver: string(cache.LocalFreeCache), Size: 1 << 20, TTL: 60},
	})
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	defer c.Close()
	ctx := context.Background()

	// loader runs once; subsequent calls hit the cache.
	var calls int
	loader := func(ctx context.Context) (any, error) {
		calls++
		return "v", nil
	}
	var dest string
	if err := c.GetOrStore(ctx, "k", &dest, loader); err != nil {
		t.Fatalf("GetOrStore: %v", err)
	}
	if dest != "v" {
		t.Errorf("dest = %q, want v", dest)
	}
	if err := c.GetOrStore(ctx, "k", &dest, loader); err != nil {
		t.Fatalf("GetOrStore 2: %v", err)
	}
	if calls != 1 {
		t.Errorf("loader called %d times, want 1", calls)
	}

	// a not-found loader caches a placeholder (penetration protection).
	var missCalls int
	missLoader := func(ctx context.Context) (any, error) {
		missCalls++
		return nil, cache.ErrNotFound
	}
	if err := c.GetOrStore(ctx, "miss", new(string), missLoader); !errors.Is(err, cache.ErrNotFound) {
		t.Fatalf("miss err = %v, want ErrNotFound", err)
	}
	if err := c.GetOrStore(ctx, "miss", new(string), missLoader); !errors.Is(err, cache.ErrNotFound) {
		t.Fatalf("miss 2 err = %v, want ErrNotFound", err)
	}
	if missCalls != 1 {
		t.Errorf("missLoader called %d times, want 1", missCalls)
	}
}
