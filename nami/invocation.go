package nami

import "reflect"

// Invocation extends Context to support a filter chain.
// It implements the Filter chain pattern: user filters execute first,
// then the final actuator (channel call) at the end.
type Invocation struct {
	Context
	filters  []Filter
	index    int
	actuator Filter
}

// NewInvocation creates an Invocation with config-level filters plus the actuator.
func NewInvocation(config *Config, target any, method reflect.Method, action, rawURL string, body any, actuator Filter) *Invocation {
	ctx := NewContext(config, target, method, action, rawURL, body)

	// Merge config-level headers into the context headers
	for k, v := range config.Headers() {
		ctx.Headers[k] = v
	}

	filters := make([]Filter, 0, len(config.Filters())+1)
	filters = append(filters, config.Filters()...)
	filters = append(filters, actuator)

	return &Invocation{
		Context:  *ctx,
		filters:  filters,
		index:    0,
		actuator: actuator,
	}
}

// Invoke executes the filter chain and returns the result.
func (inv *Invocation) Invoke() (*Result, error) {
	if inv.index >= len(inv.filters) {
		return nil, nil
	}
	f := inv.filters[inv.index]
	inv.index++
	return f.DoFilter(inv)
}
