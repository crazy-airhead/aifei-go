// Package storage provides a unified file-storage abstraction with local
// filesystem and S3-compatible backends, configurable per bucket.
//
// A Manager holds one Client per configured bucket and a designated default.
// The top-level helpers (Put, Get, ...) operate on the default bucket; Use(name)
// returns the client for a named bucket. Install a Manager as the default with
// SetDefault, or wire it automatically via the Plugin:
//
//	if err := config.Init(os.Args); err != nil { ... }
//	props := config.NewProps()
//	// ... populate props ...
//	p, _ := storage.NewPlugin(props, nil)
//	app := aifei.New(aifei.WithPlugin(p))
//	server.Run(app, ":8080")
//
// Alternatively build a Manager directly:
//
//	mgr, _ := storage.NewManager(cfg, nil)
//	storage.SetDefault(mgr)
//	storage.Put("a/b.txt", media)
package storage

import (
	"errors"
	"sync"
	"time"
)

var (
	defaultMgr *Manager
	defaultMu  sync.RWMutex
)

// ErrNoDefault is returned by the top-level helpers when no default Manager has
// been installed.
var ErrNoDefault = errors.New("storage: no default manager configured")

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

// Use returns the client for the named bucket of the default Manager. An empty
// name returns the default-bucket client. It returns nil if no default Manager
// is installed or the bucket is unknown.
func Use(bucket string) Client {
	m := DefaultManager()
	if m == nil {
		return nil
	}
	return m.Bucket(bucket)
}

// ---- Top-level convenience helpers (default bucket) ----

// Exists reports whether an object exists in the default bucket.
func Exists(key string) (bool, error) { return defaultClient().Exists(key) }

// TempURL returns a presigned URL for an object in the default bucket.
func TempURL(key string, ttl time.Duration) (string, error) {
	return defaultClient().TempURL(key, ttl)
}

// Get fetches an object as a Media from the default bucket.
func Get(key string) (*Media, error) { return defaultClient().Get(key) }

// Put stores media in the default bucket.
func Put(key string, media *Media) (*PutResult, error) {
	return defaultClient().Put(key, media)
}

// Delete removes an object from the default bucket.
func Delete(key string) error { return defaultClient().Delete(key) }

// DeleteBatch removes multiple objects from the default bucket.
func DeleteBatch(keys []string) (*BatchResult, error) {
	return defaultClient().DeleteBatch(keys)
}

// defaultClient returns the default-bucket client, or an errClient that surfaces
// ErrNoDefault when nothing is configured.
func defaultClient() Client {
	m := DefaultManager()
	if m == nil {
		return errClient{ErrNoDefault}
	}
	c := m.Default()
	if c == nil {
		return errClient{ErrNoDefault}
	}
	return c
}

// errClient is a Client whose every call returns err, so the top-level helpers
// surface a clear error instead of panicking when nothing is configured.
type errClient struct{ err error }

func (e errClient) Exists(string) (bool, error)                   { return false, e.err }
func (e errClient) TempURL(string, time.Duration) (string, error) { return "", e.err }
func (e errClient) Get(string) (*Media, error)                    { return nil, e.err }
func (e errClient) Put(string, *Media) (*PutResult, error)        { return nil, e.err }
func (e errClient) Delete(string) error                           { return e.err }
func (e errClient) DeleteBatch([]string) (*BatchResult, error)    { return nil, e.err }
