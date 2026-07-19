package dataisolate

import "context"

// Principal is the complete identity of the current actor, carried through context.
// It is the single source of truth for all isolation policies. Fields are read-only
// after construction (goroutine-safe to share).
type Principal struct {
	TenantID string // tenant identifier (TenantPolicy / strategy ③)
	UserID   any    // user id (int/string); ScopeSelf
	UserName string
	DeptID   any      // department id; ScopeDept
	DeptTree []any    // this dept + descendants, pre-resolved (ScopeDeptAndBelow)
	RegionID any      // region; ScopeRegion
	Roles    []string // roles (rule matching / multi-role merge)
	Perms    []string // permission points (fine-grained rules)
}

type principalKey struct{}

// WithPrincipal returns a ctx carrying p. A nil ctx is treated as context.Background.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the Principal carried by ctx, or (nil, false) when absent.
func PrincipalFrom(ctx context.Context) (*Principal, bool) {
	if ctx == nil {
		return nil, false
	}
	p, ok := ctx.Value(principalKey{}).(*Principal)
	return p, ok && p != nil
}
