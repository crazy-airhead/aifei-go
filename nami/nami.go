// Package nami is a lightweight HTTP RPC client framework ported from Java Solon Nami.
// It provides Channel abstraction for transport, Encoder/Decoder for serialization,
// and service discovery integration.
package nami

import (
	"reflect"
)

// Channel is the execution channel for RPC calls.
type Channel interface {
	Call(ctx *Context) (*Result, error)
}

// Encoder serializes objects for request body transmission.
type Encoder interface {
	// Enctype returns the content type (e.g. "application/json").
	Enctype() string
	// BodyRequired returns true if a body is mandatory for this encoder.
	BodyRequired() bool
	// Encode serializes the object to bytes.
	Encode(obj any) ([]byte, error)
	// Pretreatment allows the encoder to modify the request context before sending.
	Pretreatment(ctx *Context)
}

// Decoder deserializes response bodies.
type Decoder interface {
	// Enctype returns the content type (e.g. "application/json").
	Enctype() string
	// Decode deserializes the result into the given type.
	Decode(rst *Result, typ reflect.Type) (any, error)
	// Pretreatment allows the decoder to modify the request context before sending.
	Pretreatment(ctx *Context)
}

// Filter is a request interceptor for the invocation chain.
type Filter interface {
	DoFilter(inv *Invocation) (*Result, error)
}

// FilterFunc wraps a function as a Filter.
type FilterFunc func(inv *Invocation) (*Result, error)

func (f FilterFunc) DoFilter(inv *Invocation) (*Result, error) {
	return f(inv)
}

// Upstream is a function that returns a server URL for each call.
// It enables service discovery integration.
type Upstream func() string
