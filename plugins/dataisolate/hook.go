package dataisolate

import (
	"context"
	"errors"
	"fmt"

	"github.com/crazy-airhead/aifei-go/db"
	dbsql "github.com/crazy-airhead/aifei-go/db/sql"
	"github.com/crazy-airhead/aifei-go/log"
)

// errNoPrincipal is returned (via Dao.Fail) when a controlled statement is executed
// without a principal while enforce=true.
var errNoPrincipal = errors.New("dataisolate: controlled statement without principal (enforce=true)")

// lastRewriteKey marks a Dao's context with the SQL most recently produced by this hook
// so a second Before* callback on the same staged statement (raw-SQL Find fires both
// BeforeQuery and BeforeFind) does not rewrite twice. The value is the rewritten SQL.
type lastRewriteKey struct{}

// hookKit implements all six DbHookKit interfaces. It runs the PolicyChain over each
// staged statement (SELECT/UPDATE/DELETE) and stamps identity columns on inserted rows.
// It is installed per db.Config and is stateless apart from the per-statement
// last-rewrite marker it threads through context.
type hookKit struct {
	cfg       *Config
	chain     PolicyChain
	tenantCol string
	log       log.Logger
}

func newHookKit(cfg *Config, chain PolicyChain, logger log.Logger) *hookKit {
	col := cfg.Tenant.Column
	if col == "" {
		col = "tenant_id"
	}
	if logger == nil {
		logger = log.Default()
	}
	return &hookKit{cfg: cfg, chain: chain, tenantCol: col, log: logger}
}

// guardedPolicy is a policy whose WHERE injection is redundant when its column already
// appears in the SQL (double-injection guard for db.Sql #and templates). The hook skips
// such a policy for the current statement.
type guardedPolicy interface {
	RedundantFor(sql string) bool
}

// effectiveChain drops policies that are redundant for this statement.
func (h *hookKit) effectiveChain(sql string) PolicyChain {
	if len(h.chain) == 0 {
		return nil
	}
	out := make(PolicyChain, 0, len(h.chain))
	for _, p := range h.chain {
		if g, ok := p.(guardedPolicy); ok && g.RedundantFor(sql) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// principalOf returns the principal and whether isolation is bypassed for this Dao.
func (h *hookKit) principalOf(dao *db.Dao) (*Principal, bool) {
	ctx := dao.Context()
	if ctx == nil {
		return nil, false
	}
	if IsBypass(ctx) {
		return nil, true
	}
	p, _ := PrincipalFrom(ctx)
	return p, false
}

// apply runs the policy chain over the Dao's staged statement and writes the rebuilt SQL
// back. It is idempotent within one staged statement: the last rewritten SQL is threaded
// through context so a second Before* on the same statement is a no-op. Paginate fires
// Before* on two different statements (count, then data), both of which rewrite because
// their SQLs differ.
func (h *hookKit) apply(dao *db.Dao) {
	p, bypass := h.principalOf(dao)
	if bypass {
		return
	}
	if p == nil {
		if h.cfg.Enforce {
			dao.Fail(errNoPrincipal)
		}
		return
	}
	sql, args := dao.SqlAndArgs()
	ctx := dao.Context()
	if ctx != nil {
		if last, _ := ctx.Value(lastRewriteKey{}).(string); last == sql {
			return
		}
	}
	chain := h.effectiveChain(sql)
	if len(chain) == 0 {
		return
	}
	out, outArgs, st := Rewrite(sql, args, p, chain)
	switch st {
	case StatusRewritten:
		dao.SqlPara(&dbsql.SqlPara{Sql: out, Paras: outArgs})
		if ctx != nil {
			dao.Ctx(context.WithValue(ctx, lastRewriteKey{}, out))
		}
	case StatusSkippedNoScoped:
		// no controlled table/column touched — pass through unchanged
	case StatusFailed:
		if h.cfg.OnFailure == OnFailurePassthrough {
			h.log.Warn("dataisolate: rewrite failed, passthrough: %s", sql)
		} else {
			dao.Fail(fmt.Errorf("dataisolate: cannot safely rewrite: %s", sql))
		}
	}
}

// stamp fills identity columns on a row about to be inserted — tenant always, creator/
// dept when those columns exist and the principal carries the values — so later
// SELF/DEPT scope queries can find the row. It uses Put (no change tracking) so a row
// later reused for an UPDATE does not try to write the stamped columns.
func (h *hookKit) stamp(dao *db.Dao, row *db.Row) {
	p, bypass := h.principalOf(dao)
	if bypass || p == nil {
		return
	}
	table := row.Table()
	if h.tenantCol != "" && tableHasColumn(table, h.tenantCol) && !row.Has(h.tenantCol) && p.TenantID != "" {
		row.Put(h.tenantCol, p.TenantID)
	}
	tm := TableMetaOf(table)
	if tm.CreatorCol != "" && tableHasColumn(table, tm.CreatorCol) && !row.Has(tm.CreatorCol) && p.UserID != nil {
		row.Put(tm.CreatorCol, p.UserID)
	}
	if tm.DeptCol != "" && tableHasColumn(table, tm.DeptCol) && !row.Has(tm.DeptCol) && p.DeptID != nil {
		row.Put(tm.DeptCol, p.DeptID)
	}
}

// ---- InsertHook ----

// BeforeRowInsert implements InsertHook: stamp identity columns onto the row.
func (h *hookKit) BeforeRowInsert(dao *db.Dao, row *db.Row) interface{} {
	h.stamp(dao, row)
	return nil
}

// AfterRowInsert implements InsertHook (no-op).
func (h *hookKit) AfterRowInsert(dao *db.Dao, row *db.Row, fromBefore interface{}) {}

// ---- DeleteHook ----

func (h *hookKit) BeforeSqlDelete(dao *db.Dao) interface{}                         { h.apply(dao); return nil }
func (h *hookKit) AfterSqlDelete(dao *db.Dao, ret int64, fromBefore interface{})   {}
func (h *hookKit) BeforeRowDelete(dao *db.Dao, row *db.Row) interface{}            { return nil }
func (h *hookKit) AfterRowDelete(dao *db.Dao, row *db.Row, fromBefore interface{}) {}

// ---- UpdateHook ----

func (h *hookKit) BeforeSqlUpdate(dao *db.Dao) interface{}                         { h.apply(dao); return nil }
func (h *hookKit) AfterSqlUpdate(dao *db.Dao, ret int64, fromBefore interface{})   {}
func (h *hookKit) BeforeRowUpdate(dao *db.Dao, row *db.Row) interface{}            { return nil }
func (h *hookKit) AfterRowUpdate(dao *db.Dao, row *db.Row, fromBefore interface{}) {}

// ---- FindHook ----

func (h *hookKit) BeforeFind(dao *db.Dao) interface{}                            { h.apply(dao); return nil }
func (h *hookKit) AfterFind(dao *db.Dao, rows []*db.Row, fromBefore interface{}) {}

// ---- QueryHook ----

func (h *hookKit) BeforeQuery(dao *db.Dao) interface{}                                { h.apply(dao); return nil }
func (h *hookKit) AfterQuery(dao *db.Dao, result interface{}, fromBefore interface{}) {}

// ---- PaginateHook ----

func (h *hookKit) BeforeQueryTotalRows(dao *db.Dao, sp *dbsql.SqlPara) interface{} {
	h.apply(dao)
	return nil
}
func (h *hookKit) AfterQueryTotalRows(dao *db.Dao, ret int64, fromBefore interface{}) {}
func (h *hookKit) BeforePaginate(dao *db.Dao, pageNum, pageSize int, totalRows int64, sp *dbsql.SqlPara) interface{} {
	h.apply(dao)
	return nil
}
func (h *hookKit) AfterPaginate(dao *db.Dao, page *db.Page, fromBefore interface{}) {}

// ---- installation ----

// buildChain assembles the PolicyChain in canonical order (projection before WHERE):
// field → tenant → scope, including only enabled policies.
func buildChain(cfg *Config, mgr *Manager) PolicyChain {
	var chain PolicyChain
	if hasPolicy(cfg.Policies, PolicyField) {
		if fp := newFieldPolicy(cfg, mgr); fp != nil {
			chain = append(chain, fp)
		}
	}
	if hasPolicy(cfg.Policies, PolicyTenant) {
		chain = append(chain, &TenantPolicy{
			Column:       cfg.Tenant.Column,
			Mode:         cfg.Tenant.Mode,
			IgnoreTables: cfg.Tenant.IgnoreTables,
			Tables:       cfg.Tenant.Tables,
		})
	}
	if hasPolicy(cfg.Policies, PolicyScope) {
		if sp := newScopePolicy(cfg, mgr); sp != nil {
			chain = append(chain, sp)
		}
	}
	return chain
}

// installHookKit builds the PolicyChain and installs a hookKit (composed with any
// pre-existing HookKit) onto each configured db.Config. Shared-table strategy and any
// scope/field policy require this.
func installHookKit(mgr *Manager, cfg *Config, logger log.Logger) error {
	chain := buildChain(cfg, mgr)
	if len(chain) == 0 {
		return nil
	}
	for _, id := range cfg.Configs {
		c := db.GetConfig(id)
		if c == nil {
			return fmt.Errorf("dataisolate: db config %q not found", id)
		}
		hk := newHookKit(cfg, chain, logger)
		c.HookKit = mergeHookKits(c.GetDbHookKit(), hk)
	}
	if logger != nil {
		logger.Info("dataisolate hook installed on configs=%v chain=%d", cfg.Configs, len(chain))
	}
	return nil
}
