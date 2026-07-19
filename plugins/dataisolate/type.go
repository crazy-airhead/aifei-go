package dataisolate

// Status is the outcome of a rewrite pass.
type Status int

const (
	// StatusRewritten: the statement was modified (a predicate injected and/or
	// projection masked); the rebuilt SQL and realigned args are returned.
	StatusRewritten Status = iota
	// StatusSkippedNoScoped: the statement parsed successfully but touched no
	// controlled table/column — no isolation needed, pass through unchanged.
	StatusSkippedNoScoped
	// StatusFailed: the statement could not be parsed or a controlled table/column
	// could not be safely rewritten — fail-closed: the hook must abort the statement
	// rather than execute it unisolated.
	StatusFailed
)

// ---- Row isolation: data scope ----

// DataScopeType is the preset scope category resolved per (table, Principal) by a
// ScopeRuleProvider. ScopeCustom defers the field/op/values to the rule.
type DataScopeType int

const (
	ScopeAll          DataScopeType = iota // all rows: no predicate
	ScopeSelf                              // creator = ?
	ScopeDept                              // dept = ?
	ScopeDeptAndBelow                      // dept IN (?)
	ScopeRegion                            // region = ?
	ScopeCustom                            // structured custom: Column Op Values
)

// ScopeOp is the operator for a ScopeCustom rule. It is an enum (not a string) to
// forbid SQL injection via the operator.
type ScopeOp int

const (
	OpEq      ScopeOp = iota // =
	OpNeq                    // <>
	OpIn                     // IN (...)
	OpNotIn                  // NOT IN (...)
	OpLike                   // LIKE
	OpLt                     // <
	OpLte                    // <=
	OpGt                     // >
	OpGte                    // >=
	OpBetween                // BETWEEN ? AND ?
)

// ScopeRule is the per-(table, Principal) scope decision returned by a
// ScopeRuleProvider. Preset types bind their column/value implicitly via TableMeta +
// Principal; ScopeCustom states field/op/values explicitly (values resolved by the
// provider against the Principal).
type ScopeRule struct {
	Type   DataScopeType
	Column string  // ScopeCustom: the field (must be a registered column)
	Op     ScopeOp // ScopeCustom: the operator
	Values []any   // single (= / <>), multi (IN), or pair (BETWEEN)
}

// ---- Column isolation: field mask ----

// FieldMode selects whether Fields is an allowlist or a denylist.
type FieldMode int

const (
	FieldAllowlist FieldMode = iota // Fields are the allowed columns (rest masked/removed)
	FieldDenylist                   // Fields are the denied columns
)

// MaskStrategy selects how a denied column is hidden in the projection.
type MaskStrategy int

const (
	MaskNull     MaskStrategy = iota // NULL AS col (default; preserves column shape)
	MaskConstant                     // <constant> AS col
	MaskRemove                       // drop the column entirely
)

// FieldRule is the per-(table, Principal) field rule returned by a FieldRuleProvider.
type FieldRule struct {
	Mode     FieldMode
	Fields   []string
	Mask     MaskStrategy // default MaskNull
	Constant any          // when Mask == MaskConstant
}
