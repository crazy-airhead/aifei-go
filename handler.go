package aifei

// HandlerFunc is the handler function type for HTTP requests.
type HandlerFunc func(c *Context)

// Middleware is a function that wraps a HandlerFunc.
// It completely replaces Java's Interceptor + AOP proxy mechanism.
type Middleware func(next HandlerFunc) HandlerFunc

// ChainMiddleware builds a middleware chain. Middlewares are applied in order:
// the first middleware wraps the second, which wraps the third, etc.
// The final handler is the innermost function.
func ChainMiddleware(middlewares []Middleware, final HandlerFunc) HandlerFunc {
	for i := len(middlewares) - 1; i >= 0; i-- {
		final = middlewares[i](final)
	}
	return final
}
