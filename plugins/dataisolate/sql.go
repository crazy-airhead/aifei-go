package dataisolate

import (
	"context"

	"github.com/crazy-airhead/aifei-go/db"
)

// VarTenant is the data-map variable name under which Sql/SqlById expose the current
// tenant id, for use with Enjoy SQL #and directives (e.g. #and(tenant_id, "=", tenantId)).
const VarTenant = "tenantId"

// Sql runs an Enjoy SQL template with the principal's identity variables injected into
// data (tenantId / userId / deptId), so static #and directives can filter on them. Under
// bypass the variables are set to nil so #and omits the predicate. The hook path still
// applies isolation to the rendered SQL (the double-injection guard prevents conflict
// when a #and already references the column).
func Sql(ctx context.Context, sqlStr string, data map[string]interface{}) *db.Dao {
	applySqlVars(ctx, data)
	return db.WithCtx(ctx).Sql(sqlStr, data)
}

// SqlById is the cached-template variant of Sql.
func SqlById(ctx context.Context, sqlID string, data map[string]interface{}) *db.Dao {
	applySqlVars(ctx, data)
	return db.WithCtx(ctx).SqlById(sqlID, data)
}

// SqlWithArgs runs an Enjoy SQL template with positional args. The #and directive path
// does not apply (it reads the named data map), so this only injects isolation via the
// hook.
func SqlWithArgs(ctx context.Context, sqlStr string, args ...interface{}) *db.Dao {
	return db.WithCtx(ctx).SqlWithArgs(sqlStr, args...)
}

// applySqlVars seeds data with the principal's identity for #and directives. It mutates
// data in place (creating it when nil).
func applySqlVars(ctx context.Context, data map[string]interface{}) map[string]interface{} {
	if data == nil {
		data = map[string]interface{}{}
	}
	if IsBypass(ctx) {
		data[VarTenant] = nil // #and omits the predicate on nil
		return data
	}
	p, ok := PrincipalFrom(ctx)
	if !ok || p == nil {
		return data
	}
	if p.TenantID != "" {
		data[VarTenant] = p.TenantID
	}
	if p.UserID != nil {
		data["userId"] = p.UserID
	}
	if p.DeptID != nil {
		data["deptId"] = p.DeptID
	}
	return data
}
