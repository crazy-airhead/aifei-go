// Package nacos integrates Nacos (service registration, config center, and
// discovery) with the aifei framework, built on the official nacos-sdk-go/v2.
//
// The Plugin implements aifei.Plugin and wires three concerns:
//   - service registration: this process registers itself on Start and
//     deregisters on Stop (ephemeral instances; heartbeats are automatic).
//   - config center: watches a DataID and pushes changes to a callback.
//   - discovery: NewNamiUpstream exposes registered services as nami.Upstream
//     so nami RPC clients resolve them via Nacos.
//
// Usage:
//
//	if err := config.Init(os.Args); err != nil { ... }
//	p := nacos.NewPlugin(nil)
//	app := aifei.New(aifei.WithPlugin(p))
//	server.Run(app, ":8080")
//
// The plugin reads its configuration from the global config under the "nacos"
// prefix. See Config for the supported keys.
package nacos

import (
	"fmt"

	"github.com/crazy-airhead/aifei-go/aifei"
	"github.com/crazy-airhead/aifei-go/log"

	"github.com/nacos-group/nacos-sdk-go/v2/clients/config_client"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/naming_client"
)

// Compile-time assertion that Plugin satisfies aifei.Plugin.
var _ aifei.Plugin = (*Plugin)(nil)

// Plugin implements the Nacos integration — service registration, config
// management, and discovery — on top of nacos-sdk-go/v2.
type Plugin struct {
	cfg    *Config
	logger log.Logger

	naming naming_client.INamingClient
	config config_client.IConfigClient

	registered bool
	listening  bool

	// ConfigChangeCallback is invoked with the current content whenever the
	// watched config changes (and once at startup with the initial value).
	ConfigChangeCallback func(dataID, group, content string)
}

// NewPlugin creates a Nacos Plugin that reads its configuration from the
// global config under the "nacos" prefix at Start time. Call SetDefaultConfig
// directly if you need to build a Config programmatically before Start.
// A nil logger falls back to log.Default().
func NewPlugin(logger log.Logger) *Plugin {
	if logger == nil {
		logger = log.Default()
	}
	return &Plugin{logger: logger}
}

// Start loads the nacos config from the global config, connects to Nacos,
// registers this service instance (the SDK sends heartbeats automatically for
// ephemeral instances), and begins watching the configured DataID for changes.
// When nacos.enabled is false, Start is a no-op.
func (p *Plugin) Start() error {
	if p.cfg == nil {
		cfg, err := LoadConfig()
		if err != nil {
			return err
		}
		p.cfg = cfg
		SetDefaultConfig(cfg)
	}

	if !p.cfg.Enabled {
		p.logger.Info("nacos disabled, plugin start skipped")
		return nil
	}

	if p.naming == nil {
		e, err := getClients(p.cfg)
		if err != nil {
			return err
		}
		p.naming = e.naming
		p.config = e.config
	}

	if err := p.registerInstance(); err != nil {
		return fmt.Errorf("register instance: %w", err)
	}
	p.registered = true

	if p.cfg.DataID != "" {
		if err := p.startConfigListen(); err != nil {
			return fmt.Errorf("listen config: %w", err)
		}
		p.listening = true
	}

	p.logger.Info("nacos plugin started, server=%s, service=%s/%s",
		p.cfg.ServerAddr, p.cfg.Group, p.cfg.ServiceName)
	return nil
}

// Stop stops config watching and deregisters this service instance.
// The underlying SDK clients are process-wide (they expose no Close) and
// remain alive for the remainder of the process. When nacos.enabled is false
// this is a no-op.
func (p *Plugin) Stop() error {
	if p.cfg == nil || !p.cfg.Enabled {
		return nil
	}
	if p.listening {
		if err := p.config.CancelListenConfig(listenParam(p.cfg)); err != nil {
			p.logger.Warn("nacos cancel listen: %v", err)
		}
		p.listening = false
	}
	if p.registered {
		if err := p.deregisterInstance(); err != nil {
			p.logger.Warn("nacos deregister: %v", err)
		} else {
			p.registered = false
		}
	}
	p.logger.Info("nacos plugin stopped")
	return nil
}
