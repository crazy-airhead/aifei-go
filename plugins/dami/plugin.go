// Package dami integrates the in-process event bus (github.com/crazy-airhead/
// aifei-go/dami) with the aifei framework as an aifei.Plugin. It owns a Bus and
// an Lpc, installs the Bus as dami's package-level default on Start, and clears
// every listener on Stop.
//
// Unlike the kafka/cache plugins, dami is purely in-process, so this plugin
// reads no external configuration — pass dami.Option values (e.g. a custom
// router) to NewPlugin instead.
//
// Usage:
//
//	p, err := dami.NewPlugin(nil)
//	app := aifei.New(aifei.WithPlugin(p))
//	server.Run(app, ":8080")
//
// Services then register providers on the plugin's Lpc (p.Lpc()) and any module
// may publish/consume via the dami.Send/Listen package helpers (which target the
// plugin's bus).
package dami

import (
	"github.com/crazy-airhead/aifei-go/aifei"
	adami "github.com/crazy-airhead/aifei-go/dami"
	"github.com/crazy-airhead/aifei-go/log"
)

// Compile-time assertion that Plugin satisfies aifei.Plugin.
var _ aifei.Plugin = (*Plugin)(nil)

// Plugin integrates the dami bus/lpc with the aifei lifecycle.
type Plugin struct {
	log log.Logger
	bus *adami.Bus
	lpc *adami.Lpc
}

// NewPlugin builds a Plugin that owns a fresh Bus (configured with opts) and an
// Lpc over it. A nil logger falls back to log.Default().
func NewPlugin(logger log.Logger, opts ...adami.Option) (*Plugin, error) {
	if logger == nil {
		logger = log.Default()
	}
	bus := adami.New(opts...)
	return &Plugin{log: logger, bus: bus, lpc: adami.NewLpc(bus)}, nil
}

// Start installs the plugin's bus as dami's package-level default, so that
// dami.Send/Listen/Call/Stream target it.
func (p *Plugin) Start() error {
	adami.SetDefaultBus(p.bus)
	p.log.Info("dami plugin started")
	return nil
}

// Stop removes every listener from the bus (clears all topics), releasing it for
// shutdown.
func (p *Plugin) Stop() error {
	p.bus.Stop()
	p.log.Info("dami plugin stopped")
	return nil
}

// Bus returns the owned bus (nil before NewPlugin).
func (p *Plugin) Bus() *adami.Bus { return p.bus }

// Lpc returns the owned Lpc for registering providers (nil before NewPlugin).
func (p *Plugin) Lpc() *adami.Lpc { return p.lpc }
