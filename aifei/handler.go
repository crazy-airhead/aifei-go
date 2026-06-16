package aifei

// HandlerFunc is the handler function type. It receives input and returns output.
// The go-http module is responsible for writing the Output to the HTTP response.
type HandlerFunc func(in Input) Output

// Handler wraps a HandlerFunc, used to build handler chains (replaces Java's Interceptor AOP).
type Handler func(next HandlerFunc) HandlerFunc

// ChainHandlers builds a handler chain from outermost to innermost.
func ChainHandlers(handlers []Handler, final HandlerFunc) HandlerFunc {
	for i := len(handlers) - 1; i >= 0; i-- {
		final = handlers[i](final)
	}
	return final
}
