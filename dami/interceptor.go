package dami

// Interceptor is event-level AOP, mirroring aifei's Handler wrapper style.
//
//	ev  is the non-generic event view, shared with the listeners that run later;
//	next continues the chain — calling it runs the next interceptor, or the final
//	distribution when the chain is exhausted. Returning a non-nil error, or
//	simply not calling next, short-circuits dispatch.
type Interceptor func(ev eventView, next func() error) error

type interceptorEntity struct {
	index int
	it    Interceptor
}

// chain drives the ordered interceptor list, terminating at final when every
// interceptor has called next. Mirrors Java's InterceptorChain.
type chain struct {
	entities []*interceptorEntity
	final    func() error
	i        int
}

func (c *chain) proceed(ev eventView) error {
	if c.i < len(c.entities) {
		e := c.entities[c.i]
		c.i++
		return e.it(ev, func() error { return c.proceed(ev) })
	}
	return c.final()
}
