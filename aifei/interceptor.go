package aifei

// Interceptor wraps a single service method invocation.
// Unlike Middleware (which wraps the entire request pipeline), Interceptors
// are method-level and are declared by the service itself.
//
// Pattern from Java Aifei's Interceptor:
//
//	interceptor.Intercept(method, in, invoke)
//
// The invoke function calls the next interceptor or the actual method.
type Interceptor interface {
	Intercept(method string, in Input, invoke func() Output) Output
}

// InterceptorFunc is a function adapter for Interceptor.
type InterceptorFunc func(method string, in Input, invoke func() Output) Output

func (f InterceptorFunc) Intercept(method string, in Input, invoke func() Output) Output {
	return f(method, in, invoke)
}

// MethodInterceptors is implemented by services that declare per-method interceptors.
// The map key is the method name, the value is the list of interceptors
// applied in order (first interceptor wraps the second, etc.).
type MethodInterceptors interface {
	MethodInterceptors() map[string][]Interceptor
}
