package dataisolate

import (
	"fmt"

	"github.com/crazy-airhead/aifei-go/config"
)

// Policy names referenced by the `policies` config list.
const (
	PolicyTenant = "tenant"
	PolicyScope  = "scope"
	PolicyField  = "field"
)

// OnFailure values.
const (
	OnFailureError       = "error"       // default: fail-closed, abort the statement
	OnFailurePassthrough = "passthrough" // let unparseable statements through (migrations)
)

// TenantStrategy values.
const (
	StrategyDatabase = "database" // one DB per tenant (route by config; no SQL rewrite)
	StrategySchema   = "schema"   // shared DB, one schema per tenant (route by config)
	StrategyShared   = "shared"   // shared tables + discriminator column (SQL rewrite)
)

// Config is the resolved dataisolate configuration loaded from config.Props under a
// prefix (default "dataisolate"). Only stable, app.yml-worthy settings live here;
// per-(table,role) rules come from registered providers, not config.
type Config struct {
	Prefix      string
	Policies    []string // ordered: projection policies should precede WHERE policies
	Enforce     bool
	AllowBypass bool
	OnFailure   string // "error" (default, fail-closed) | "passthrough"
	Configs     []string
	Resolver    string // principal resolver name; default subdomain_header
	Tenant      TenantConfig
	Scope       ScopeConfig
	Field       FieldConfig
}

// TenantConfig holds the tenant-isolation settings.
type TenantConfig struct {
	Strategy     string
	Column       string // global default tenant column (default tenant_id); per-table via TableMeta
	Mode         string // auto | whitelist | all
	IgnoreTables []string
	Tables       []string
	Tenants      map[string]TenantRoute // tenant id -> config id (strategy ①/②)
}

// TenantRoute maps a tenant id to a db.Config id for strategy ①/②.
type TenantRoute struct {
	Config string `yaml:"config"`
}

// ScopeConfig holds the multi-role merge strategy for data scope.
type ScopeConfig struct {
	Merge string // broadest (default) | strict
}

// FieldConfig holds the global default mask strategy for field isolation.
type FieldConfig struct {
	DefaultMask string // null (default) | constant | remove
}

// LoadConfig reads the dataisolate.* subtree from the global props. prefix defaults to
// "dataisolate". Mirrors plugins/storage/config.go (config.GetStr + config.SubBind).
func LoadConfig(prefix string) (*Config, error) {
	if prefix == "" {
		prefix = "dataisolate"
	}
	cfg := &Config{
		Prefix:    prefix,
		Policies:  getStrList(prefix + ".policies"),
		OnFailure: defaultStr(config.GetStr(prefix+".on_failure"), "error"),
		Resolver:  defaultStr(config.GetStr(prefix+".principal.resolver"), "subdomain_header"),
		Tenant: TenantConfig{
			Strategy: defaultStr(config.GetStr(prefix+".tenant.strategy"), StrategyShared),
			Column:   defaultStr(config.GetStr(prefix+".tenant.column"), "tenant_id"),
			Mode:     defaultStr(config.GetStr(prefix+".tenant.scope.mode"), "auto"),
		},
		Scope: ScopeConfig{
			Merge: defaultStr(config.GetStr(prefix+".scope.merge"), "broadest"),
		},
		Field: FieldConfig{
			DefaultMask: defaultStr(config.GetStr(prefix+".field.default_mask"), "null"),
		},
	}
	cfg.Enforce = config.GetBool(prefix + ".enforce")
	cfg.AllowBypass = config.GetBool(prefix + ".allow_bypass")
	cfg.Configs = getStrList(prefix + ".configs")
	cfg.Tenant.IgnoreTables = getStrList(prefix + ".tenant.scope.ignore_tables")
	cfg.Tenant.Tables = getStrList(prefix + ".tenant.scope.tables")

	// Default policies: tenant only (the most common single-dimension setup).
	if len(cfg.Policies) == 0 {
		cfg.Policies = []string{PolicyTenant}
	}
	// Default configs to install hooks on: the default "main" config.
	if len(cfg.Configs) == 0 {
		cfg.Configs = []string{"main"}
	}
	if err := config.SubBind(prefix+".tenant.tenants", &cfg.Tenant.Tenants); err != nil {
		return nil, fmt.Errorf("dataisolate: bind tenant.tenants: %w", err)
	}
	return cfg, nil
}

// needsHookKit reports whether hooks must be installed: the shared-table tenant
// strategy rewrites SQL, and any scope/field policy rewrites SQL. Strategy ①/②
// (database/schema) route by config and need no hooks — unless scope/field is on.
func needsHookKit(cfg *Config) bool {
	if hasPolicy(cfg.Policies, PolicyScope) || hasPolicy(cfg.Policies, PolicyField) {
		return true
	}
	strategy := cfg.Tenant.Strategy
	if strategy == "" {
		strategy = StrategyShared
	}
	return strategy == StrategyShared && hasPolicy(cfg.Policies, PolicyTenant)
}

func hasPolicy(policies []string, name string) bool {
	for _, p := range policies {
		if p == name {
			return true
		}
	}
	return false
}

func defaultStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// getStrList reads a YAML list as []string. config has no string-slice getter, so the
// raw value (usually []interface{}) is converted here.
func getStrList(key string) []string {
	v := config.Get(key)
	if v == nil {
		return nil
	}
	switch s := v.(type) {
	case []interface{}:
		out := make([]string, 0, len(s))
		for _, e := range s {
			if str, ok := e.(string); ok {
				out = append(out, str)
			}
		}
		return out
	case []string:
		return s
	case string:
		if s == "" {
			return nil
		}
		return []string{s}
	}
	return nil
}
