package storage

import (
	"github.com/crazy-airhead/aifei-go/aifei"
	"github.com/crazy-airhead/aifei-go/config"
	"github.com/crazy-airhead/aifei-go/log"
)

// Compile-time assertion that Plugin satisfies aifei.Plugin.
var _ aifei.Plugin = (*Plugin)(nil)

// Plugin integrates storage with the aifei framework. On Start it reads the
// "storage" subtree from props, builds a Manager, and installs it as the
// package-level default so that the top-level storage.Put/Get/... helpers work.
//
// Usage:
//
//	if err := config.Init(os.Args); err != nil { ... }
//	props := config.NewProps()
//	// ... populate props ...
//	p, err := storage.NewPlugin(props, nil)
//	app := aifei.New(aifei.WithPlugin(p))
//	server.Run(app, ":8080")
type Plugin struct {
	props  *config.Props
	prefix string
	log    log.Logger
	mgr    *Manager
}

// NewPlugin creates a storage Plugin that reads its configuration from props
// under the given prefix (empty defaults to "storage"). A nil logger falls back
// to log.Default().
func NewPlugin(props *config.Props, logger log.Logger, prefix ...string) (*Plugin, error) {
	p := "storage"
	if len(prefix) > 0 && prefix[0] != "" {
		p = prefix[0]
	}
	if logger == nil {
		logger = log.Default()
	}
	return &Plugin{props: props, prefix: p, log: logger}, nil
}

// Start loads the storage config, builds the Manager, and registers it as the
// package default. If no props were provided it is a no-op (callers can still
// call SetDefault with a Manager built directly).
func (p *Plugin) Start() error {
	if p.props == nil {
		p.log.Info("storage plugin: no config props, skipping auto-setup")
		return nil
	}
	cfg, err := LoadConfig(p.props, p.prefix)
	if err != nil {
		return err
	}
	mgr, err := NewManager(cfg, p.log)
	if err != nil {
		return err
	}
	p.mgr = mgr
	SetDefault(mgr)
	p.log.Info("storage plugin started, buckets=%v", mgr.Buckets())
	return nil
}

// Stop is a no-op; storage clients hold no connections to tear down.
func (p *Plugin) Stop() error { return nil }

// Manager returns the Manager built on Start, or nil if Start has not run.
func (p *Plugin) Manager() *Manager { return p.mgr }
