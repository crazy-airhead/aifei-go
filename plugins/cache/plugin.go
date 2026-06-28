package cache

import (
	"github.com/crazy-airhead/aifei-go/aifei"
	"github.com/crazy-airhead/aifei-go/log"
)

// Compile-time assertion that Plugin satisfies aifei.Plugin.
var _ aifei.Plugin = (*Plugin)(nil)

// Plugin integrates cache with the aifei framework. On Start it reads the
// "cache" subtree from the global config, builds a Manager, and installs it
// as the package-level default so that the top-level cache.Get/Set/...
// helpers work.
//
// Unlike the storage plugin, Stop is not a no-op: caches may run background
// refresh goroutines, so Stop closes every instance.
//
// Usage:
//
//	if err := config.Init(os.Args); err != nil {
//	    log.Fatal(err)
//	}
//	p, err := cache.NewPlugin(nil)
//	app := aifei.New(aifei.WithPlugin(p))
//	server.Run(app, ":8080")
type Plugin struct {
	prefix string
	log    log.Logger
	mgr    *Manager
}

// NewPlugin creates a cache Plugin that reads its configuration from the
// global config under the given prefix (empty defaults to "cache").
// A nil logger falls back to log.Default().
func NewPlugin(logger log.Logger, prefix ...string) (*Plugin, error) {
	p := "cache"
	if len(prefix) > 0 && prefix[0] != "" {
		p = prefix[0]
	}
	if logger == nil {
		logger = log.Default()
	}
	return &Plugin{prefix: p, log: logger}, nil
}

// Start loads the cache config from the global config, builds the Manager,
// and registers it as the package default.
func (p *Plugin) Start() error {
	cfg, err := LoadConfig(p.prefix)
	if err != nil {
		return err
	}
	mgr, err := NewManager(cfg, p.log)
	if err != nil {
		return err
	}
	p.mgr = mgr
	SetDefault(mgr)
	p.log.Info("cache plugin started, instances=%v", mgr.Names())
	return nil
}

// Stop closes every cache instance to stop refresh timers and release
// resources. Unlike the storage plugin (a no-op), caches may hold background
// goroutines.
func (p *Plugin) Stop() error {
	if p.mgr == nil {
		return nil
	}
	for _, name := range p.mgr.Names() {
		if c := p.mgr.Instance(name); c != nil {
			if err := c.Close(); err != nil {
				p.log.Warn("cache plugin: close %s: %v", name, err)
			}
		}
	}
	return nil
}

// Manager returns the Manager built on Start, or nil if Start has not run.
func (p *Plugin) Manager() *Manager { return p.mgr }
