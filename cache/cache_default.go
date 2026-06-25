package cache

import (
	"context"
	"errors"
	"sync"
	"time"

	jcache "github.com/mgtv-tech/jetcache-go"
)

var (
	defaultMgr *Manager
	defaultMu  sync.RWMutex
)

// ErrNoDefault is returned by the top-level helpers when no default Manager has
// been installed.
var ErrNoDefault = errors.New("cache: no default manager configured")

// SetDefault installs mgr as the package-level default used by the top-level
// helpers.
func SetDefault(mgr *Manager) {
	defaultMu.Lock()
	defaultMgr = mgr
	defaultMu.Unlock()
}

// DefaultManager returns the installed package-level default Manager.
func DefaultManager() *Manager {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultMgr
}

// Use returns the cache for the named instance of the default Manager. An empty
// name returns the default-instance cache. It returns nil if no default Manager
// is installed or the instance is unknown.
func Use(name string) Cache {
	m := DefaultManager()
	if m == nil {
		return nil
	}
	return m.Instance(name)
}

// ---- Top-level convenience helpers (default instance) ----

// Get fetches a value from the default instance.
func Get(ctx context.Context, key string, dest any) (bool, error) {
	return defaultCache().Get(ctx, key, dest)
}

// Set stores a value in the default instance.
func Set(ctx context.Context, key string, value any, ttl ...time.Duration) error {
	return defaultCache().Set(ctx, key, value, ttl...)
}

// Delete removes a value from the default instance.
func Delete(ctx context.Context, key string) error {
	return defaultCache().Delete(ctx, key)
}

// Exists reports whether a key exists in the default instance.
func Exists(ctx context.Context, key string) bool {
	return defaultCache().Exists(ctx, key)
}

// GetOrStore gets-or-loads a value via the default instance.
func GetOrStore(ctx context.Context, key string, dest any, loader Loader, ttl ...time.Duration) error {
	return defaultCache().GetOrStore(ctx, key, dest, loader, ttl...)
}

// defaultCache returns the default-instance cache, or an errCache that surfaces
// ErrNoDefault when nothing is configured.
func defaultCache() Cache {
	m := DefaultManager()
	if m == nil {
		return errCache{ErrNoDefault}
	}
	c := m.Default()
	if c == nil {
		return errCache{ErrNoDefault}
	}
	return c
}

// errCache is a Cache whose data operations return err, so the top-level helpers
// surface a clear error instead of panicking when nothing is configured.
type errCache struct{ err error }

func (e errCache) Get(context.Context, string, any) (bool, error) { return false, e.err }
func (e errCache) Set(context.Context, string, any, ...time.Duration) error {
	return e.err
}
func (e errCache) Delete(context.Context, string) error { return e.err }
func (e errCache) Exists(context.Context, string) bool  { return false }
func (e errCache) GetOrStore(context.Context, string, any, Loader, ...time.Duration) error {
	return e.err
}
func (e errCache) CacheType() string      { return "" }
func (e errCache) Close() error           { return nil }
func (e errCache) JetCache() jcache.Cache { return nil }
