package aifei

// Output defines the result of a handler invocation.
// Implementations provide code, message, and data for serialization.
// HTTP handlers always return HTTP 200; business semantics are in the code field.
type Output interface {
	Code() int
	Msg() string
	Data() interface{}
}

// result is the minimal Output implementation used by the framework internally.
type result struct {
	code int
	msg  string
	data interface{}
}

func (r *result) Code() int         { return r.code }
func (r *result) Msg() string       { return r.msg }
func (r *result) Data() interface{} { return r.data }

// NewResult creates a basic Output with the given code, message, and data.
func NewResult(code int, msg string, data interface{}) Output {
	return &result{code: code, msg: msg, data: data}
}
