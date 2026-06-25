package cache

import (
	"fmt"
	"sync"

	"github.com/crazy-airhead/aifei-go/log"
)

// Manager is a multi-instance facade: it builds one Cache per configured
// instance and routes lookups by name, with a designated default. It mirrors
// the shape of storage.Manager.
type Manager struct {
	caches map[string]Cache
	def    Cache
	mu     sync.RWMutex
	log    log.Logger
}

// NewManager builds a Manager from Config, constructing one Cache per instance.
// The instance named in cfg.Default becomes the default; if Default is empty, an
// arbitrary instance is chosen (so setting Default is recommended for
// determinism). With no instances configured a single local instance is created.
func NewManager(cfg *Config, logger log.Logger) (*Manager, error) {
	if logger == nil {
		logger = log.Default()
	}
	m := &Manager{caches: make(map[string]Cache), log: logger}

	if cfg == nil || len(cfg.Instances) == 0 {
		// No instances configured: fall back to a single local instance.
		c, err := NewCache(defaultCacheName, InstanceConfig{
			Type:  string(CacheLocal),
			Local: &LocalConfig{Driver: string(LocalFreeCache)},
		})
		if err != nil {
			return nil, err
		}
		m.caches[defaultCacheName] = c
		m.def = c
		return m, nil
	}

	for name, ic := range cfg.Instances {
		c, err := NewCache(name, ic)
		if err != nil {
			return nil, fmt.Errorf("cache: instance %s: %w", name, err)
		}
		m.caches[name] = c
		if name == cfg.Default || m.def == nil {
			m.def = c
		}
	}
	return m, nil
}

// Default returns the default-instance cache.
func (m *Manager) Default() Cache {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.def
}

// Instance returns the cache for the named instance, or nil if it is not
// configured. An empty name returns the default cache. (Named Instance rather
// than Cache because Go forbids a method and its return type sharing a name.)
func (m *Manager) Instance(name string) Cache {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if name == "" {
		return m.def
	}
	return m.caches[name]
}

// Names returns the names of all configured instances.
func (m *Manager) Names() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.caches))
	for n := range m.caches {
		names = append(names, n)
	}
	return names
}

// Close releases resources held by every instance (e.g. refresh goroutines).
func (m *Manager) Close() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var firstErr error
	for _, c := range m.caches {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
