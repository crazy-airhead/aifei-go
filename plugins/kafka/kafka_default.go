package kafka

import (
	"context"
	"errors"
	"sync"

	"github.com/twmb/franz-go/pkg/kgo"
)

var (
	defaultMgr *Manager
	defaultMu  sync.RWMutex
)

// ErrNoDefault is returned by the top-level helpers when no default Manager has
// been installed.
var ErrNoDefault = errors.New("kafka: no default manager configured")

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

// ProduceSync produces one or more messages on the default cluster.
func ProduceSync(ctx context.Context, msgs ...*Message) error {
	return DefaultClient().ProduceSync(ctx, msgs...)
}

// Produce produces a single message asynchronously on the default cluster.
func Produce(ctx context.Context, msg *Message, promise Promise) {
	DefaultClient().Produce(ctx, msg, promise)
}

// Flush blocks until all buffered records on the default cluster are flushed.
func Flush(ctx context.Context) error {
	return DefaultClient().Flush(ctx)
}

// Subscribe starts a background consumer on the default cluster.
func Subscribe(ctx context.Context, topics []string, handler Handler) (*Subscription, error) {
	return DefaultClient().Subscribe(ctx, topics, handler)
}

// errClient is a Client whose operations surface err, so the top-level helpers
// return a clear error instead of panicking when nothing is configured. Produce
// routes the error through the promise (matching franz-go, where async produce
// errors arrive via the promise); a nil promise is a silent no-op.
type errClient struct{ err error }

func (e errClient) Name() string                                   { return "" }
func (e errClient) ProduceSync(context.Context, ...*Message) error { return e.err }
func (e errClient) Produce(_ context.Context, msg *Message, promise Promise) {
	if promise != nil {
		promise(msg, e.err)
	}
}
func (e errClient) Flush(context.Context) error { return e.err }
func (e errClient) Subscribe(context.Context, []string, Handler) (*Subscription, error) {
	return nil, e.err
}
func (e errClient) Close() error           { return nil }
func (e errClient) KgoClient() *kgo.Client { return nil }
