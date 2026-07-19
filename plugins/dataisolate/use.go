package dataisolate

import (
	"context"

	"github.com/crazy-airhead/aifei-go/db"
)

// Use returns a Dao for the current request's tenant under strategy ①/② (database or
// schema isolation): it resolves the tenant id from ctx, maps it to a db.Config id, and
// returns a Dao on that config — still ctx-bound so it participates in any transaction.
// When no principal/tenant or no route is configured, it falls back to the default
// config. This path rewrites NO SQL; isolation strength is maximal.
func Use(ctx context.Context) *db.Dao {
	m := DefaultManager()
	p, ok := PrincipalFrom(ctx)
	if !ok || p.TenantID == "" {
		return db.Use().Ctx(ctx)
	}
	if m != nil {
		if cfgID := m.ConfigID(p.TenantID); cfgID != "" {
			return db.UseWithID(cfgID).Ctx(ctx)
		}
	}
	return db.Use().Ctx(ctx)
}
