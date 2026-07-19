package dataisolate

// ScopeRuleProvider resolves the data-scope rule for a (table, Principal) pair at query
// time. The plugin only defines the interface; the implementation, its data source
// (rule table / external policy), and its caching/refresh are the application's
// responsibility. Register via Manager.SetScopeProvider. When `scope` is enabled but no
// provider is registered, the policy is a no-op (no scope predicate injected).
type ScopeRuleProvider interface {
	ScopeRule(table string, p *Principal) (ScopeRule, bool)
}

// FieldRuleProvider resolves the field rule for a (table, Principal) pair at query time.
// Same contract as ScopeRuleProvider: the plugin defines only the interface. Register
// via Manager.SetFieldProvider.
type FieldRuleProvider interface {
	Rule(table string, p *Principal) (FieldRule, bool)
}
