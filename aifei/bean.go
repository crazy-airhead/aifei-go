package aifei

// Bean binds the request's structured parameters into a fresh T and returns
// it — the value-returning counterpart of Input.GetBean's pointer-out form.
//
// Java Aifei exposes getBean as a generic method (<T> T getBean()); Go could
// not put type parameters on methods when this port was made, so the
// interface carries GetBean(obj) instead. Go 1.27 still forbids type
// parameters on interface methods, so the generic entry is this package-level
// helper — service signatures take aifei.Input, and every Input works here:
//
//	func(in aifei.Input) aifei.Output {
//		user, err := aifei.Bean[CreateUserReq](in)
//		...
//	}
//
// Binding semantics (sources, coercion, nested keys) are exactly GetBean's;
// only the shape differs. Concrete *http.HttpContext also offers the method
// form Bean[T], promoted to *server.In.
func Bean[T any](in Input, keys ...string) (T, error) {
	var t T
	err := in.GetBean(&t, keys...)
	return t, err
}
