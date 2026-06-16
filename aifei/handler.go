package aifei

// HandlerFunc is the handler function type. It receives input and returns output.
// The go-http module is responsible for writing the Output to the HTTP response.
type HandlerFunc func(in Input) Output

// Middleware wraps a HandlerFunc. It replaces Java's Handler chain + Interceptor AOP.
type Middleware func(next HandlerFunc) HandlerFunc

// ChainMiddleware builds a middleware chain from outermost to innermost.
func ChainMiddleware(middlewares []Middleware, final HandlerFunc) HandlerFunc {
	for i := len(middlewares) - 1; i >= 0; i-- {
		final = middlewares[i](final)
	}
	return final
}
