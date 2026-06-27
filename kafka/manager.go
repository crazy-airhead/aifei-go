package kafka

import (
	"fmt"
	"sync"

	"github.com/crazy-airhead/aifei-go/log"
)

// Manager is a multi-cluster facade: it builds one Client per configured cluster
// and routes lookups by name, with a designated default. It mirrors the shape of
// cache.Manager / storage.Manager.
type Manager struct {
	clients map[string]Client
	def     Client
	mu      sync.RWMutex
	log     log.Logger
}

// NewManager builds a Manager from Config, constructing one Client per cluster.
// The cluster named in cfg.Default becomes the default; if Default is empty, an
// arbitrary cluster is chosen (so setting Default is recommended for
// determinism). Returns an error if no cluster is configured.
func NewManager(cfg *Config, logger log.Logger) (*Manager, error) {
	if logger == nil {
		logger = log.Default()
	}
	if cfg == nil || len(cfg.Clusters) == 0 {
		return nil, fmt.Errorf("kafka: no clusters configured")
	}
	m := &Manager{clients: make(map[string]Client), log: logger}
	for name, cc := range cfg.Clusters {
		if len(cc.Brokers) == 0 {
			return nil, fmt.Errorf("kafka: cluster %q has no brokers", name)
		}
		c, err := newClient(name, cc, logger)
		if err != nil {
			// Clean up any clients built so far before failing.
			for _, c := range m.clients {
				_ = c.Close()
			}
			return nil, err
		}
		m.clients[name] = c
		if name == cfg.Default || m.def == nil {
			m.def = c
		}
	}
	return m, nil
}

// Default returns the default-cluster Client.
func (m *Manager) Default() Client {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.def
}

// Instance returns the Client for the named cluster, or nil if it is not
// configured. An empty name returns the default cluster. (Named Instance rather
// than Client because Go forbids a method and its return type sharing a name.)
func (m *Manager) Instance(name string) Client {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if name == "" {
		return m.def
	}
	return m.clients[name]
}

// Names returns the names of all configured clusters.
func (m *Manager) Names() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.clients))
	for n := range m.clients {
		names = append(names, n)
	}
	return names
}

// Close stops every Subscription and closes every Client (producer clients).
func (m *Manager) Close() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var firstErr error
	for _, c := range m.clients {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
