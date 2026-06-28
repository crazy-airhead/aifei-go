// Package cache_redis_test exercises the cache module's Redis (L2 / "both")
// code paths against an embedded miniredis, so the cache module itself stays
// free of any Redis test dependency.
package cache_redis_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/crazy-airhead/aifei-go/plugins/cache"
	"github.com/crazy-airhead/aifei-go/config"
)

type cacheSample struct {
	V int
}

// newRemoteCache builds a remote (Redis) cache backed by a fresh miniredis.
func newRemoteCache(t *testing.T, name string) *cache.JetCache {
	t.Helper()
	mr := miniredis.RunT(t)
	c, err := cache.NewCache(name, cache.InstanceConfig{
		Type:   string(cache.CacheRemote),
		Remote: &cache.RemoteConfig{Redis: cache.RedisConfig{Addr: mr.Addr()}},
	})
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestRemoteCacheRoundTrip(t *testing.T) {
	c := newRemoteCache(t, "test-remote")
	ctx := context.Background()
	if c.CacheType() != "remote" {
		t.Errorf("CacheType = %q, want remote", c.CacheType())
	}
	type user struct {
		Name string
		Age  int
	}
	if err := c.Set(ctx, "u1", &user{Name: "bob", Age: 30}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	var got user
	found, err := c.Get(ctx, "u1", &got)
	if err != nil || !found {
		t.Fatalf("Get: found=%v err=%v", found, err)
	}
	if got.Name != "bob" || got.Age != 30 {
		t.Errorf("Get = %+v", got)
	}
	if !c.Exists(ctx, "u1") {
		t.Error("Exists u1 = false")
	}
	if err := c.Delete(ctx, "u1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if c.Exists(ctx, "u1") {
		t.Error("Exists after delete = true")
	}
}

func TestRemoteLiveRoundTrip(t *testing.T) {
	addr := os.Getenv("CACHE_REDIS_ADDR")
	if addr == "" {
		t.Skip("CACHE_REDIS_ADDR not set; skipping live redis round-trip")
	}
	c, err := cache.NewCache("live", cache.InstanceConfig{
		Type:   string(cache.CacheRemote),
		Remote: &cache.RemoteConfig{TTL: 60, Redis: cache.RedisConfig{Addr: addr}},
	})
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	key := "cache-live-test"
	defer c.Delete(ctx, key)
	if err := c.Set(ctx, key, "hello", 10*time.Second); err != nil {
		t.Fatalf("Set: %v", err)
	}
	var s string
	found, err := c.Get(ctx, key, &s)
	if err != nil || !found || s != "hello" {
		t.Fatalf("Get: found=%v err=%v s=%q", found, err, s)
	}
}

func TestGetNotFoundIsNotError(t *testing.T) {
	c := newRemoteCache(t, "nf")
	ctx := context.Background()

	found, err := c.Get(ctx, "absent", new(string))
	if err != nil {
		t.Errorf("Get absent err = %v, want nil", err)
	}
	if found {
		t.Error("Get absent found = true, want false")
	}
	if c.Exists(ctx, "absent") {
		t.Error("Exists absent = true")
	}
}

func TestGetOrStoreLoadsAndCaches(t *testing.T) {
	c := newRemoteCache(t, "gos")
	ctx := context.Background()

	var calls int32
	loader := func(ctx context.Context) (any, error) {
		atomic.AddInt32(&calls, 1)
		return &cacheSample{V: 42}, nil
	}
	var dest cacheSample
	if err := c.GetOrStore(ctx, "k", &dest, loader); err != nil {
		t.Fatalf("GetOrStore: %v", err)
	}
	if dest.V != 42 {
		t.Errorf("dest.V = %d, want 42", dest.V)
	}
	var dest2 cacheSample
	if err := c.GetOrStore(ctx, "k", &dest2, loader); err != nil {
		t.Fatalf("GetOrStore 2: %v", err)
	}
	if dest2.V != 42 {
		t.Errorf("dest2.V = %d, want 42", dest2.V)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("loader called %d times, want 1", got)
	}
}

func TestGetOrStoreSingleflight(t *testing.T) {
	c := newRemoteCache(t, "sf")
	ctx := context.Background()

	var calls int32
	loader := func(ctx context.Context) (any, error) {
		atomic.AddInt32(&calls, 1)
		// a slow loader maximizes concurrent coalescing under singleflight.
		select {
		case <-time.After(50 * time.Millisecond):
		case <-ctx.Done():
		}
		return "result", nil
	}
	const n = 20
	var wg sync.WaitGroup
	errs := make([]error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = c.GetOrStore(ctx, "shared", new(string), loader)
		}(i)
	}
	close(start)
	wg.Wait()
	for i, e := range errs {
		if e != nil {
			t.Errorf("goroutine %d err = %v", i, e)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("loader called %d times, want 1 (singleflight)", got)
	}
}

func TestGetOrStoreNotFoundPenetration(t *testing.T) {
	c := newRemoteCache(t, "pen")
	ctx := context.Background()

	var calls int32
	loader := func(ctx context.Context) (any, error) {
		atomic.AddInt32(&calls, 1)
		return nil, cache.ErrNotFound
	}
	if err := c.GetOrStore(ctx, "missing", new(string), loader); !errors.Is(err, cache.ErrNotFound) {
		t.Fatalf("first GetOrStore err = %v, want ErrNotFound", err)
	}
	if err := c.GetOrStore(ctx, "missing", new(string), loader); !errors.Is(err, cache.ErrNotFound) {
		t.Fatalf("second GetOrStore err = %v, want ErrNotFound", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("loader called %d times, want 1 (placeholder cached)", got)
	}
}

func TestNewManagerMultiple(t *testing.T) {
	cache.SetDefault(nil)
	mr := miniredis.RunT(t)

	cfg := &cache.Config{
		Default: "user",
		Instances: map[string]cache.InstanceConfig{
			"user": {
				Type:   string(cache.CacheBoth),
				TTL:    60,
				Local:  &cache.LocalConfig{Driver: string(cache.LocalFreeCache), Size: 1 << 20},
				Remote: &cache.RemoteConfig{Redis: cache.RedisConfig{Addr: mr.Addr()}},
			},
			"sess": {
				Type:  string(cache.CacheLocal),
				Local: &cache.LocalConfig{Driver: string(cache.LocalTinyLFU), Size: 1000},
			},
		},
	}
	mgr, err := cache.NewManager(cfg, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	def := mgr.Default()
	if def == nil || def.CacheType() != "both" {
		t.Errorf("Default = %v", def)
	}
	sess := mgr.Instance("sess")
	if sess == nil || sess.CacheType() != "local" {
		t.Errorf("sess = %v", sess)
	}
	if len(mgr.Names()) != 2 {
		t.Errorf("Names = %v, want 2", mgr.Names())
	}
}

func TestManagerClose(t *testing.T) {
	cache.SetDefault(nil)
	mr := miniredis.RunT(t)
	mgr, err := cache.NewManager(&cache.Config{
		Default: "a",
		Instances: map[string]cache.InstanceConfig{
			"a": {Type: string(cache.CacheRemote), TTL: 60, Remote: &cache.RemoteConfig{Redis: cache.RedisConfig{Addr: mr.Addr()}}},
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestPluginLifecycle(t *testing.T) {
	cache.SetDefault(nil)
	mr := miniredis.RunT(t)
	defer cache.SetDefault(nil)

	props := config.NewProps()
	yaml := []byte(`
cache:
  default: user
  instances:
    user:
      type: remote
      ttl: 60
      remote:
        redis:
          addr: "` + mr.Addr() + `"
`)
	if err := props.LoadYAMLBytes(yaml); err != nil {
		t.Fatalf("LoadYAMLBytes: %v", err)
	}
	config.SetProps(props)
	p, err := cache.NewPlugin(nil)
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}
	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop()

	if p.Manager() == nil {
		t.Fatal("Manager nil after Start")
	}
	if cache.DefaultManager() == nil {
		t.Fatal("default not installed")
	}

	// top-level helpers operate on the default instance.
	ctx := context.Background()
	if err := cache.Set(ctx, "top", "v"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	var s string
	found, err := cache.Get(ctx, "top", &s)
	if err != nil || !found || s != "v" {
		t.Fatalf("Get: found=%v err=%v s=%q", found, err, s)
	}
}

func TestUseAndTopLevelWithManager(t *testing.T) {
	cache.SetDefault(nil)
	mr := miniredis.RunT(t)
	defer cache.SetDefault(nil)

	mgr, err := cache.NewManager(&cache.Config{
		Default: "d",
		Instances: map[string]cache.InstanceConfig{
			"d": {Type: string(cache.CacheRemote), TTL: 60, Remote: &cache.RemoteConfig{Redis: cache.RedisConfig{Addr: mr.Addr()}}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()
	cache.SetDefault(mgr)

	ctx := context.Background()
	if err := cache.Set(ctx, "k", "v"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	var s string
	found, err := cache.Get(ctx, "k", &s)
	if err != nil || !found || s != "v" {
		t.Fatalf("Get: found=%v err=%v s=%q", found, err, s)
	}
	if cache.Use("") == nil || cache.Use("d") == nil {
		t.Error("Use lookup failed")
	}
	if cache.Use("missing") != nil {
		t.Error("Use missing should be nil")
	}
}
