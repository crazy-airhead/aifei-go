package aifei

import (
	"context"
)

// Input is the interface for reading a request. It is the union of two
// contracts:
//
//   - Param: transport-agnostic parameter reading (the part any request
//     source — HTTP, a test fixture, a programmatic call — can satisfy).
//   - Meta:  transport-agnostic request metadata (context, header, path,
//     body).
//
// Splitting them lets code depend on the narrow Param when it only needs
// parameters, and keeps HTTP-specific notions (method verb, remote address,
// cookies) off this interface — those live on the HTTP adapter. This mirrors
// Java, where the core Input carries only parameter reading and the
// HTTP-bound In class adds the rest.

// Param is the transport-agnostic contract for reading request parameters.
type Param interface {
	// Parameter existence
	Has(name string) bool

	// Path parameters (extracted from route patterns like /:id)
	PathPara(index int) string
	PathParaByName(name string) string
	Param(name string) string // alias for PathParaByName

	// Typed getters by name
	GetStr(key string) string
	GetStrDefault(key, def string) string
	GetInt(key string) int
	GetIntDefault(key string, def int) int
	GetInt64(key string) int64
	GetInt64Default(key string, def int64) int64
	GetFloat64(key string) float64
	GetFloat64Default(key string, def float64) float64
	GetBool(key string) bool
	GetBoolDefault(key string, def bool) bool

	// Bean / structured parameters
	GetBean(obj interface{}) error
	GetMap() map[string]interface{}
}

// Meta is the transport-agnostic contract for request-level metadata. Only
// concepts every invocation has regardless of transport belong here. The
// cancellation Context, string metadata via Header, the invocation Path, and
// the raw Body all generalize beyond HTTP; method verb, remote address, and
// cookies do not, so they are kept on the HTTP adapter instead.
type Meta interface {
	Context() context.Context
	Header(name string) string
	Path() string
	Body() []byte
}

// Input is the full contract an action handler receives: Param composed with Meta.
type Input interface {
	Param
	Meta
}
