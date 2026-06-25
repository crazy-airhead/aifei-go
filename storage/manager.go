package storage

import (
	"fmt"
	"sync"

	"github.com/crazy-airhead/aifei-go/log"
)

// Manager is a multi-bucket facade: it builds one Client per configured bucket
// and routes lookups by bucket name, with a designated default. It merges the
// responsibilities of the Java StorageClientImpl (config-driven facade) and
// StorageClientUtils (bucket registry).
type Manager struct {
	clients map[string]Client
	def     Client
	mu      sync.RWMutex
	log     log.Logger
}

// NewManager builds a Manager from Config, constructing one Client per bucket.
// The bucket named in cfg.Default becomes the default; if Default is empty, an
// arbitrary bucket is chosen (so setting Default is recommended for
// determinism). With no buckets configured a single local bucket is created.
func NewManager(cfg *Config, logger log.Logger) (*Manager, error) {
	if logger == nil {
		logger = log.Default()
	}
	m := &Manager{clients: make(map[string]Client), log: logger}

	if cfg == nil || len(cfg.Buckets) == 0 {
		// No buckets configured: fall back to a single local bucket.
		lc, err := NewLocalClient(defaultBucketName, defaultBucketName, logger)
		if err != nil {
			return nil, err
		}
		m.clients[defaultBucketName] = lc
		m.def = lc
		return m, nil
	}

	for name, bc := range cfg.Buckets {
		client, err := newClient(name, bc, logger)
		if err != nil {
			return nil, fmt.Errorf("storage: bucket %s: %w", name, err)
		}
		m.clients[name] = client
		if name == cfg.Default || m.def == nil {
			m.def = client
		}
	}
	return m, nil
}

// Default returns the default-bucket client.
func (m *Manager) Default() Client {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.def
}

// Bucket returns the client for the named bucket, or nil if it is not
// configured. An empty name returns the default client.
func (m *Manager) Bucket(name string) Client {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if name == "" {
		return m.def
	}
	return m.clients[name]
}

// Buckets returns the names of all configured buckets.
func (m *Manager) Buckets() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.clients))
	for n := range m.clients {
		names = append(names, n)
	}
	return names
}

// newClient builds a Client for a single bucket according to its driver/endpoint.
func newClient(name string, bc BucketConfig, logger log.Logger) (Client, error) {
	bucket := bc.Bucket
	if bucket == "" {
		bucket = name
	}
	switch storageTypeOf(bc.Driver, bc.Endpoint) {
	case StorageS3:
		return NewS3Client(bucket, S3Options{
			Endpoint:         bc.Endpoint,
			Region:           bc.resolvedRegion(),
			AccessKey:        bc.AccessKey,
			SecretKey:        bc.SecretKey,
			AutoCreateBucket: bc.AutoCreateBucket,
		}, logger)
	default:
		return NewLocalClient(bucket, bc.Endpoint, logger)
	}
}
