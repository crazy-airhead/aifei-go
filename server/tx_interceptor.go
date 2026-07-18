package server

import (
	"context"

	"github.com/crazy-airhead/aifei-go/aifei"
	"github.com/crazy-airhead/aifei-go/db"
)

// ctxSetter is implemented by any Input whose request context can be replaced.
// *http.HttpContext (and *In, via embedding) satisfy it. Interceptors use it to
// propagate context-bound values — like the active transaction — into the
// service method without depending on a concrete Input type.
type ctxSetter interface {
	SetContext(ctx context.Context)
}

// TxInterceptor returns an Interceptor that wraps the method in a database
// transaction. The *sql.Tx is carried by the context injected into the request,
// so a service method joins the transaction by using db.Ctx(in.Context()) (or
// the ctx-aware db helpers such as db.InsertCtx) for its db calls. The method's
// Output drives rollback: if it implements db.RollbackDecision with
// ShouldRollback()==true (server.Out does, returning true when code != 0) the
// transaction is rolled back; otherwise it is committed.
func TxInterceptor() aifei.Interceptor {
	return aifei.InterceptorFunc(func(method string, in aifei.Input, invoke func() aifei.Output) aifei.Output {
		var out aifei.Output
		err := db.TransactionCtx(in.Context(), func(txCtx context.Context) error {
			setInContext(in, txCtx)
			out = invoke()
			if shouldRollbackOutput(out) {
				return &rollbackError{}
			}
			return nil
		})
		if err != nil {
			if _, ok := err.(*rollbackError); ok {
				return out
			}
			return Fail("transaction error: %s", err)
		}
		return out
	})
}

// shouldRollbackOutput reports whether out requests a transaction rollback. It
// prefers db.RollbackDecision.ShouldRollback() when available (so any Output can
// drive the decision, not just server.Out), and falls back to code != 0 for
// Outputs that only expose Code().
func shouldRollbackOutput(out aifei.Output) bool {
	if out == nil {
		return false
	}
	if rd, ok := out.(db.RollbackDecision); ok {
		return rd.ShouldRollback()
	}
	return out.Code() != 0
}

// setInContext injects ctx into in so that in.Context() returns it for the
// service method. Inputs that do not support SetContext (e.g. test fixtures) are
// left untouched — the transaction still commits/rolls back, the method just
// won't automatically join it unless it propagates the ctx some other way.
func setInContext(in aifei.Input, ctx context.Context) {
	if s, ok := in.(ctxSetter); ok {
		s.SetContext(ctx)
	}
}

type rollbackError struct{}

func (e *rollbackError) Error() string { return "rollback" }
