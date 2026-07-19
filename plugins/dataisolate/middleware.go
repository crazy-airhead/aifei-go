package dataisolate

import (
	"context"

	"github.com/crazy-airhead/aifei-go/aifei"
	"github.com/crazy-airhead/aifei-go/server"
)

// ctxSetter is implemented by Input adapters that can replace their context (e.g.
// http.HttpContext.SetContext). The server package defines the same concept internally;
// it is duplicated here to avoid depending on an unexported symbol. §21 notes exporting
// ctxSetter from server as a future cleanup.
type ctxSetter interface {
	SetContext(context.Context)
}

// middlewareConfig holds the resolved middleware options.
type middlewareConfig struct {
	Resolver PrincipalResolver
	Enforce  bool
}

// Option configures Middleware.
type Option func(*middlewareConfig)

// WithResolver sets the Principal resolver (default SubdomainHeaderResolver).
func WithResolver(r PrincipalResolver) Option {
	return func(c *middlewareConfig) { c.Resolver = r }
}

// WithEnforce makes the middleware reject requests that resolve no principal instead of
// passing them through. Per-request it is OR-ed with the started manager's enforce flag.
func WithEnforce(enforce bool) Option {
	return func(c *middlewareConfig) { c.Enforce = enforce }
}

func resolveCfg(opts []Option) *middlewareConfig {
	c := &middlewareConfig{Resolver: NewSubdomainHeaderResolver()}
	for _, o := range opts {
		o(c)
	}
	if c.Resolver == nil {
		c.Resolver = NewSubdomainHeaderResolver()
	}
	return c
}

// Middleware resolves the Principal from the request and writes it into the request
// context so downstream ctx-aware db calls (db.WithCtx(in.Context())) pick it up. Place
// it before business/transaction handlers. Returns an aifei.Handler to match the
// server.Logger()/server.Recover() shape.
func Middleware(opts ...Option) aifei.Handler {
	cfg := resolveCfg(opts)
	return func(next aifei.HandlerFunc) aifei.HandlerFunc {
		return func(in aifei.Input) aifei.Output {
			p := cfg.Resolver.Resolve(in)
			if p == nil {
				enforce := cfg.Enforce
				if m := DefaultManager(); m != nil && m.cfg != nil {
					enforce = enforce || m.cfg.Enforce
				}
				if enforce {
					return server.Fail("missing principal")
				}
				return next(in)
			}
			ctx := WithPrincipal(in.Context(), p)
			if s, ok := in.(ctxSetter); ok {
				s.SetContext(ctx)
			}
			return next(in)
		}
	}
}
