package dataisolate

import "context"

// bypassKey marks a context as bypassing the entire Policy chain. Use it for one-shot
// operations that must see all rows (seeding, migrations, system tables, cross-tenant
// imports). It is honored at the very front of each hook (principalOf). Do NOT put it
// on a request-scoped middleware context — that would unhook the whole request.
type bypassKey struct{}

// Bypass returns a ctx that skips isolation for the single call it wraps. When
// allow_bypass is false (strict deployments), hooks treat this as a no-op.
func Bypass(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, bypassKey{}, true)
}

// IsBypass reports whether ctx is marked to bypass isolation.
func IsBypass(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(bypassKey{}).(bool)
	return v
}

// As returns a ctx that acts as p instead of the request's principal (e.g. an admin
// impersonation that should see all rows). It simply replaces the Principal.
func As(ctx context.Context, p *Principal) context.Context {
	return WithPrincipal(ctx, p)
}
