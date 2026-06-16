package aifei

import "net/url"

// Input defines the interface for reading request parameters.
// It abstracts the underlying server (net/http, fasthttp, etc.) so that
// action methods don't depend on a specific transport layer.
type Input interface {
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

	// Bean / raw body
	GetBean(obj interface{}) error
	Body() []byte

	// Request metadata
	Method() string
	Path() string
	RemoteIP() string
	Query() url.Values
}
