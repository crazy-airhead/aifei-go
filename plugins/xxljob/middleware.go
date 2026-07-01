package xxljob

// Middleware wraps a TaskFunc.
type Middleware func(TaskFunc) TaskFunc

// chain composes the middleware chain in FIFO order.
func (e *executor) chain(next TaskFunc) TaskFunc {
	for i := range e.middlewares {
		next = e.middlewares[len(e.middlewares)-1-i](next)
	}
	return next
}
