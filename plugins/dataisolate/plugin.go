package dataisolate

import (
	"github.com/crazy-airhead/aifei-go/aifei"
	"github.com/crazy-airhead/aifei-go/log"
)

var _ aifei.Plugin = (*Plugin)(nil)

// Plugin implements aifei.Plugin. Start loads the config, builds the Manager, registers
// it as the package default, and — when SQL-rewriting policies are active (strategy ③ or
// scope/field) — installs the DbHookKit onto the configured db.Config(s).
type Plugin struct {
	prefix string
	log    log.Logger
	mgr    *Manager

	scopeProvider ScopeRuleProvider
	fieldProvider FieldRuleProvider
}

// NewPlugin builds a Plugin. The optional prefix overrides the config subtree
// (default "dataisolate"); a nil logger falls back to the default logger.
func NewPlugin(logger log.Logger, prefix ...string) (*Plugin, error) {
	p := "dataisolate"
	if len(prefix) > 0 && prefix[0] != "" {
		p = prefix[0]
	}
	if logger == nil {
		logger = log.Default()
	}
	return &Plugin{prefix: p, log: logger}, nil
}

// Start loads configuration, builds the Manager, sets the package default, and installs
// the hook kit when SQL rewriting is required.
func (p *Plugin) Start() error {
	cfg, err := LoadConfig(p.prefix)
	if err != nil {
		return err
	}
	mgr, err := NewManager(cfg, p.log)
	if err != nil {
		return err
	}
	mgr.SetScopeProvider(p.scopeProvider)
	mgr.SetFieldProvider(p.fieldProvider)
	p.mgr = mgr
	SetDefault(mgr)
	if needsHookKit(cfg) {
		if err := installHookKit(mgr, cfg, p.log); err != nil {
			return err
		}
	}
	p.log.Info("dataisolate plugin started, strategy=%s policies=%v tenants=%v",
		cfg.Tenant.Strategy, cfg.Policies, mgr.Names())
	return nil
}

// SetScopeProvider registers the application's data-scope rule provider. Call before
// Start so the scope policy is included in the hook chain.
func (p *Plugin) SetScopeProvider(provider ScopeRuleProvider) { p.scopeProvider = provider }

// SetFieldProvider registers the application's field-rule provider. Call before Start so
// the field policy is included in the hook chain.
func (p *Plugin) SetFieldProvider(provider FieldRuleProvider) { p.fieldProvider = provider }

// Stop is a no-op (hooks are stateless; the db.Config outlives the plugin).
func (p *Plugin) Stop() error { return nil }

// Manager returns the started Manager (nil before Start).
func (p *Plugin) Manager() *Manager { return p.mgr }
