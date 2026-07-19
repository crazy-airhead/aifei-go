package dataisolate

import (
	"github.com/crazy-airhead/aifei-go/log"
)

// Manager owns the resolved configuration, the tenant→config routing for strategy
// ①/②, and the application-supplied rule providers. It is the package default once the
// plugin starts.
type Manager struct {
	cfg *Config
	log log.Logger

	router *tenantRouter

	scopeProvider ScopeRuleProvider
	fieldProvider FieldRuleProvider
}

// NewManager builds a Manager from the resolved config. A nil logger falls back to the
// default logger.
func NewManager(cfg *Config, logger log.Logger) (*Manager, error) {
	if logger == nil {
		logger = log.Default()
	}
	return &Manager{
		cfg:    cfg,
		log:    logger,
		router: newTenantRouter(cfg.Tenant.Tenants),
	}, nil
}

// Cfg returns the resolved configuration.
func (m *Manager) Cfg() *Config { return m.cfg }

// Log returns the manager's logger.
func (m *Manager) Log() log.Logger { return m.log }

// ConfigID returns the db.Config id for a tenant id under strategy ①/②, or "" when no
// route is registered.
func (m *Manager) ConfigID(tid string) string { return m.router.configID(tid) }

// Names returns the registered tenant ids (strategy ①/②).
func (m *Manager) Names() []string { return m.router.names() }

// SetScopeProvider registers the application's data-scope rule provider.
func (m *Manager) SetScopeProvider(p ScopeRuleProvider) { m.scopeProvider = p }

// SetFieldProvider registers the application's field-rule provider.
func (m *Manager) SetFieldProvider(p FieldRuleProvider) { m.fieldProvider = p }

// ScopeProvider returns the registered scope provider (may be nil).
func (m *Manager) ScopeProvider() ScopeRuleProvider { return m.scopeProvider }

// FieldProvider returns the registered field provider (may be nil).
func (m *Manager) FieldProvider() FieldRuleProvider { return m.fieldProvider }

// tenantRouter maps tenant ids to db.Config ids for strategy ①/②.
type tenantRouter struct {
	tenants map[string]string
}

func newTenantRouter(routes map[string]TenantRoute) *tenantRouter {
	t := make(map[string]string, len(routes))
	for tid, r := range routes {
		if r.Config != "" {
			t[tid] = r.Config
		}
	}
	return &tenantRouter{tenants: t}
}

func (r *tenantRouter) configID(tid string) string {
	if r == nil || tid == "" {
		return ""
	}
	return r.tenants[tid]
}

func (r *tenantRouter) names() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.tenants))
	for k := range r.tenants {
		out = append(out, k)
	}
	return out
}
