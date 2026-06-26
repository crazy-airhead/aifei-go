// Package cache provides a two-level (local + Redis) cache abstraction built on
// jetcache-go, configurable per instance.
//
// A Manager holds one Cache per configured instance and a designated default.
// The top-level helpers (Get, Set, ...) operate on the default instance;
// Use(name) returns the cache for a named instance. Install a Manager as the
// default with SetDefault, or wire it automatically via the Plugin:
//
//	config.Init(os.Args)
//	props := config.NewProps()
//	p, _ := cache.NewPlugin(props, nil)
//	app := aifei.New(aifei.WithPlugin(p))
//	server.Run(app, ":8080")
//
// Alternatively build a Manager directly:
//
//	mgr, _ := cache.NewManager(cfg, nil)
//	cache.SetDefault(mgr)
//	cache.Set(ctx, "user:1", user)
package cache

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	jcache "github.com/mgtv-tech/jetcache-go"
	"github.com/mgtv-tech/jetcache-go/encoding"
)

// ErrNotFound is the sentinel for a missing key. It is passed to jetcache as
// the not-found error (enabling cache-penetration protection) and should be
// returned by GetOrStore loaders to record a "not found" result. Get treats it
// (and jetcache's native miss) as "not found" rather than an error.
var ErrNotFound = errors.New("cache: key not found")

// Loader loads the value for a key on a cache miss. Its signature matches
// jetcache's cache.DoFunc, so it converts without allocation. Return ErrNotFound
// to cache a "not found" placeholder (penetration protection).
type Loader func(ctx context.Context) (any, error)

// Cache is the cache abstraction over a single named instance. It wraps
// jetcache-go to expose ficus-style store/get/remove/getOrStore semantics while
// keeping the full power of jetcache available through JetCache().
type Cache interface {
	// Get fetches the value for key, deserializing into dest. A cache miss
	// (including a stored "not found" placeholder) returns (false, nil); dest is
	// left untouched. A real failure returns (false, err).
	Get(ctx context.Context, key string, dest any) (found bool, err error)

	// Set stores value under key. ttl overrides the instance default for this
	// call; omit it to use the configured TTL. (Redis uses SETEX, so remote
	// values always expire; a non-positive ttl uses the library default.)
	Set(ctx context.Context, key string, value any, ttl ...time.Duration) error

	// Delete removes key from both the local and remote levels (when present).
	// Removing a missing key is not an error.
	Delete(ctx context.Context, key string) error

	// Exists reports whether key is present (excluding "not found" placeholders).
	Exists(ctx context.Context, key string) bool

	// GetOrStore returns the cached value for key, or on a miss invokes loader,
	// caches and returns its result, with singleflight coalescing concurrent
	// loads for the same key. A non-positive ttl uses the instance default. If
	// loader returns ErrNotFound, a placeholder is cached and ErrNotFound is
	// returned; dest is left untouched.
	GetOrStore(ctx context.Context, key string, dest any, loader Loader, ttl ...time.Duration) error

	// CacheType returns "local", "remote", or "both".
	CacheType() string

	// Close releases resources held by this instance (e.g. refresh goroutines).
	Close() error

	// JetCache returns the underlying jetcache-go cache for advanced use
	// (SetNX/SetXX/SkipLocal/Refresh/SyncLocal/...). Callers that use it become
	// coupled to jetcache-go.
	JetCache() jcache.Cache
}

// JetCache wraps a jetcache-go cache.Cache and implements Cache. It applies a
// per-instance key prefix so instances sharing one Redis do not collide
// (jetcache's WithName is used only for logging/metrics, not key prefixing).
type JetCache struct {
	name   string
	prefix string
	inner  jcache.Cache
}

// NewJetCache wraps an existing jetcache-go cache under name with an optional
// key prefix (empty disables prefixing). The caller is responsible for the
// cache's options; in particular WithErrNotFound must be set for GetOrStore to
// report not-found results. NewCache handles all of this automatically.
func NewJetCache(name, prefix string, inner jcache.Cache) *JetCache {
	return &JetCache{name: name, prefix: prefix, inner: inner}
}

// NewCache builds a Cache for one instance from its configuration. This is the
// constructor used by the Manager.
func NewCache(name string, cfg InstanceConfig) (*JetCache, error) {
	if name == "" {
		name = defaultCacheName
	}
	typ := cacheTypeOf(cfg.Type, cfg.Local != nil, cfg.Remote != nil)

	codec := strings.ToLower(strings.TrimSpace(cfg.Codec))
	if codec != "" && codec != "msgpack" {
		// msgpack is always available (jetcache imports it); other codecs must be
		// imported by the application to register themselves.
		if encoding.GetCodec(codec) == nil {
			return nil, fmt.Errorf("cache: codec %q is not registered; import github.com/mgtv-tech/jetcache-go/encoding/%s to register it", codec, cfg.Codec)
		}
	}

	opts := []jcache.Option{
		jcache.WithName(name),
		jcache.WithErrNotFound(ErrNotFound),
	}
	if codec != "" {
		opts = append(opts, jcache.WithCodec(codec))
	}

	switch typ {
	case CacheRemote, CacheBoth:
		if cfg.Remote == nil {
			return nil, fmt.Errorf("cache: instance %q of type %q requires a remote config", name, typ)
		}
		cli, err := buildRedisClient(cfg.Remote.Redis)
		if err != nil {
			return nil, fmt.Errorf("cache: instance %q: %w", name, err)
		}
		opts = append(opts, jcache.WithRemote(buildRemoteCache(cli)))
		if ttl := cfg.remoteTTL(); ttl > 0 {
			opts = append(opts, jcache.WithRemoteExpiry(ttl))
		}
	}
	if typ == CacheBoth || typ == CacheLocal {
		if cfg.Local == nil {
			return nil, fmt.Errorf("cache: instance %q of type %q requires a local config", name, typ)
		}
		opts = append(opts, jcache.WithLocal(buildLocalCache(cfg.Local)))
	}
	if cfg.Refresh != nil {
		if d := ttlSeconds(cfg.Refresh.Duration); d > 0 {
			opts = append(opts, jcache.WithRefreshDuration(d))
			if cfg.Refresh.Concurrency > 0 {
				opts = append(opts, jcache.WithRefreshConcurrency(cfg.Refresh.Concurrency))
			}
			if cfg.Refresh.StopAfter > 0 {
				opts = append(opts, jcache.WithStopRefreshAfterLastAccess(ttlSeconds(cfg.Refresh.StopAfter)))
			}
		}
	}
	if cfg.SyncLocal {
		opts = append(opts, jcache.WithSyncLocal(true))
	}

	return &JetCache{name: name, prefix: cfg.prefixedName(name), inner: jcache.New(opts...)}, nil
}

// Name returns the instance name.
func (c *JetCache) Name() string { return c.name }

// fullKey applies the instance prefix, isolating instances that share storage.
func (c *JetCache) fullKey(key string) string {
	if c.prefix == "" {
		return key
	}
	return c.prefix + keySeparator + key
}

// Get implements Cache.
func (c *JetCache) Get(ctx context.Context, key string, dest any) (bool, error) {
	err := c.inner.Get(ctx, c.fullKey(key), dest)
	if err == nil {
		return true, nil
	}
	// jetcache returns ErrCacheMiss for a real miss and our ErrNotFound for a
	// stored "not found" placeholder; both are "not found" to callers.
	if errors.Is(err, ErrNotFound) || errors.Is(err, jcache.ErrCacheMiss) {
		return false, nil
	}
	return false, fmt.Errorf("cache: get %q: %w", key, err)
}

// Set implements Cache.
func (c *JetCache) Set(ctx context.Context, key string, value any, ttl ...time.Duration) error {
	opts := []jcache.ItemOption{jcache.Value(value)}
	// jetcache treats a zero TTL as "use remoteExpiry", so only set it when the
	// caller asked for a positive one; otherwise the instance default applies.
	if len(ttl) > 0 && ttl[0] > 0 {
		opts = append(opts, jcache.TTL(ttl[0]))
	}
	if err := c.inner.Set(ctx, c.fullKey(key), opts...); err != nil {
		return fmt.Errorf("cache: set %q: %w", key, err)
	}
	return nil
}

// Delete implements Cache.
func (c *JetCache) Delete(ctx context.Context, key string) error {
	if err := c.inner.Delete(ctx, c.fullKey(key)); err != nil {
		return fmt.Errorf("cache: delete %q: %w", key, err)
	}
	return nil
}

// Exists implements Cache.
func (c *JetCache) Exists(ctx context.Context, key string) bool {
	return c.inner.Exists(ctx, c.fullKey(key))
}

// GetOrStore implements Cache.
func (c *JetCache) GetOrStore(ctx context.Context, key string, dest any, loader Loader, ttl ...time.Duration) error {
	opts := []jcache.ItemOption{jcache.Do(jcache.DoFunc(loader)), jcache.Value(dest)}
	if len(ttl) > 0 && ttl[0] > 0 {
		opts = append(opts, jcache.TTL(ttl[0]))
	}
	err := c.inner.Once(ctx, c.fullKey(key), opts...)
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	return fmt.Errorf("cache: getorstore %q: %w", key, err)
}

// CacheType implements Cache.
func (c *JetCache) CacheType() string { return c.inner.CacheType() }

// Close implements Cache.
func (c *JetCache) Close() error {
	c.inner.Close()
	return nil
}

// JetCache implements Cache.
func (c *JetCache) JetCache() jcache.Cache { return c.inner }
