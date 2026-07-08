package elasticsearch

import (
	"github.com/crazy-airhead/aifei-go/aifei"
	"github.com/crazy-airhead/aifei-go/log"
)

// Compile-time assertion that Plugin satisfies aifei.Plugin.
var _ aifei.Plugin = (*Plugin)(nil)

// Plugin integrates elasticsearch with the aifei framework. On Start it reads the
// "elasticsearch" subtree from the global config, builds a Manager, and installs it
// as the package-level default so that the top-level elasticsearch.Search/Index/
// Get/... helpers work.
//
// Usage:
//
//	if err := config.Init(os.Args); err != nil { ... }
//	p, err := elasticsearch.NewPlugin(nil)
//	app := aifei.New(aifei.WithPlugin(p))
//	server.Run(app, ":8080")
type Plugin struct {
	prefix string
	log    log.Logger
	mgr    *Manager
}

// NewPlugin creates an elasticsearch Plugin that reads its configuration from the
// global config under the given prefix (empty defaults to "elasticsearch").
// A nil logger falls back to log.Default().
func NewPlugin(logger log.Logger, prefix ...string) (*Plugin, error) {
	p := "elasticsearch"
	if len(prefix) > 0 && prefix[0] != "" {
		p = prefix[0]
	}
	if logger == nil {
		logger = log.Default()
	}
	return &Plugin{prefix: p, log: logger}, nil
}

// Start loads the elasticsearch config from the global config, builds the Manager,
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
	p.log.Info("elasticsearch plugin started, clusters=%v", mgr.Names())
	return nil
}

// Stop releases resources held by every Client.
func (p *Plugin) Stop() error {
	if p.mgr == nil {
		return nil
	}
	return p.mgr.Close()
}

// Manager returns the Manager built on Start, or nil if Start has not run.
func (p *Plugin) Manager() *Manager { return p.mgr }
