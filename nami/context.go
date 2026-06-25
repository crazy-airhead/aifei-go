package nami

import (
	"context"
	"net/url"
	"reflect"
)

// Context holds the request context for a single RPC call.
type Context struct {
	Config  *Config
	Target  any
	Method  reflect.Method
	Action  string
	URL     string
	URI     *url.URL
	Headers map[string]string
	Args    map[string]any
	Body    any
	// Ctx is the caller's context, made available to filters (e.g. one that
	// injects request-scoped values such as an Authorization header).
	Ctx context.Context
}

// NewContext creates a new Context.
func NewContext(config *Config, target any, method reflect.Method, action, rawURL string, body any) *Context {
	uri, _ := url.Parse(rawURL)
	return &Context{
		Config:  config,
		Target:  target,
		Method:  method,
		Action:  action,
		URL:     rawURL,
		URI:     uri,
		Headers: make(map[string]string),
		Args:    make(map[string]any),
		Body:    body,
	}
}

// BodyOrArgs returns the body if set, otherwise the args map.
func (c *Context) BodyOrArgs() any {
	if c.Body != nil {
		return c.Body
	}
	return c.Args
}
