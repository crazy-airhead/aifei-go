package kafka

import (
	"github.com/crazy-airhead/aifei-go/aifei"
	"github.com/crazy-airhead/aifei-go/log"
)

// Compile-time assertion that Plugin satisfies aifei.Plugin.
var _ aifei.Plugin = (*Plugin)(nil)

// Plugin integrates kafka with the aifei framework. On Start it reads the
// "kafka" subtree from the global config, builds a Manager, and installs it as
// the package-level default so that the top-level kafka.ProduceSync/Produce/
// Subscribe helpers work.
//
// Stop is not a no-op: it stops every running Subscription (committing marked
// offsets) and closes every producer client, releasing all resources and
// background goroutines.
//
// Usage:
//
//	if err := config.Init(os.Args); err != nil { ... }
//	p, err := kafka.NewPlugin(nil)
//	app := aifei.New(aifei.WithPlugin(p))
//	server.Run(app, ":8080")
type Plugin struct {
	prefix string
	log    log.Logger
	mgr    *Manager
}

// NewPlugin creates a kafka Plugin that reads its configuration from the global
// config under the given prefix (empty defaults to "kafka"). A nil logger falls
// back to log.Default().
func NewPlugin(logger log.Logger, prefix ...string) (*Plugin, error) {
	p := "kafka"
	if len(prefix) > 0 && prefix[0] != "" {
		p = prefix[0]
	}
	if logger == nil {
		logger = log.Default()
	}
	return &Plugin{prefix: p, log: logger}, nil
}

// Start loads the kafka config from the global config, builds the Manager, and
// registers it as the package default.
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
	p.log.Info("kafka plugin started, clusters=%v", mgr.Names())
	return nil
}

// Stop stops every Subscription and closes every producer Client to release
// resources and background goroutines.
func (p *Plugin) Stop() error {
	if p.mgr == nil {
		return nil
	}
	return p.mgr.Close()
}

// Manager returns the Manager built on Start, or nil if Start has not run.
func (p *Plugin) Manager() *Manager { return p.mgr }
