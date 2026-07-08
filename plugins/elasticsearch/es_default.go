package elasticsearch

import (
	"context"
	"errors"
	"io"
	"sync"

	elasticsearch8 "github.com/elastic/go-elasticsearch/v8"
)

var (
	defaultMgr *Manager
	defaultMu  sync.RWMutex
)

// ErrNoDefault is returned by the top-level helpers when no default Manager has
// been installed.
var ErrNoDefault = errors.New("elasticsearch: no default manager configured")

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

// Use returns the Client for the named cluster of the default Manager. An empty
// name returns the default-cluster Client. It returns nil if no default Manager
// is installed or the cluster is unknown.
func Use(name string) Client {
	m := DefaultManager()
	if m == nil {
		return nil
	}
	return m.Instance(name)
}

// DefaultClient returns the default-cluster Client, or an errClient that surfaces
// ErrNoDefault when nothing is configured.
func DefaultClient() Client {
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

// ---- Top-level convenience helpers (default cluster) ----

// Search executes a search query on the default cluster.
func Search(ctx context.Context, index string, query map[string]any) (*SearchResult, error) {
	return DefaultClient().Search(ctx, index, query)
}

// SearchRaw executes a search with a raw JSON body on the default cluster.
func SearchRaw(ctx context.Context, index string, body io.Reader) (*SearchResult, error) {
	return DefaultClient().SearchRaw(ctx, index, body)
}

// Index creates or replaces a document on the default cluster.
func Index(ctx context.Context, index, id string, doc any) (*IndexResult, error) {
	return DefaultClient().Index(ctx, index, id, doc)
}

// Get retrieves a document by its _id from the default cluster.
func Get(ctx context.Context, index, id string) (*GetResult, error) {
	return DefaultClient().Get(ctx, index, id)
}

// Delete removes a document by its _id from the default cluster.
func Delete(ctx context.Context, index, id string) (*DeleteResult, error) {
	return DefaultClient().Delete(ctx, index, id)
}

// BulkIndex indexes a batch of documents on the default cluster.
func BulkIndex(ctx context.Context, index string, docs []BulkDoc) (*BulkResult, error) {
	return DefaultClient().BulkIndex(ctx, index, docs)
}

// IndicesExists reports whether an index exists on the default cluster.
func IndicesExists(ctx context.Context, index string) (bool, error) {
	return DefaultClient().IndicesExists(ctx, index)
}

// IndicesCreate creates an index on the default cluster.
func IndicesCreate(ctx context.Context, index string, mappings map[string]any) error {
	return DefaultClient().IndicesCreate(ctx, index, mappings)
}

// IndicesDelete deletes an index from the default cluster.
func IndicesDelete(ctx context.Context, index string) error {
	return DefaultClient().IndicesDelete(ctx, index)
}

// Ping checks whether the default cluster is reachable.
func Ping(ctx context.Context) error {
	return DefaultClient().Ping(ctx)
}

// ---- errClient ----

// errClient is a Client whose every call returns err, so the top-level helpers
// surface a clear error instead of panicking when nothing is configured.
type errClient struct{ err error }

func (e errClient) Name() string { return "" }
func (e errClient) Search(context.Context, string, map[string]any) (*SearchResult, error) {
	return nil, e.err
}
func (e errClient) SearchRaw(context.Context, string, io.Reader) (*SearchResult, error) {
	return nil, e.err
}
func (e errClient) Index(context.Context, string, string, any) (*IndexResult, error) {
	return nil, e.err
}
func (e errClient) Get(context.Context, string, string) (*GetResult, error) {
	return nil, e.err
}
func (e errClient) Delete(context.Context, string, string) (*DeleteResult, error) {
	return nil, e.err
}
func (e errClient) BulkIndex(context.Context, string, []BulkDoc) (*BulkResult, error) {
	return nil, e.err
}
func (e errClient) IndicesExists(context.Context, string) (bool, error) { return false, e.err }
func (e errClient) IndicesCreate(context.Context, string, map[string]any) error {
	return e.err
}
func (e errClient) IndicesDelete(context.Context, string) error { return e.err }
func (e errClient) Ping(context.Context) error                  { return e.err }
func (e errClient) Close() error                                { return nil }
func (e errClient) ESClient() *elasticsearch8.Client            { return nil }
