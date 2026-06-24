package server

import (
	"fmt"

	"github.com/crazy-airhead/aifei-go/aifei"
)

// OutCode values follow the Java aifei-vip-arch convention:
// 0 = success, non-zero = error.
const (
	CodeOK   = 0
	CodeFail = 90000
)

// Out is a transport-agnostic result builder that implements aifei.Output.
// It accumulates code, message, data, and view information during action
// execution, and is serialized to JSON by the framework.
//
// Pattern from Java aifei-vip-arch:
//   - server.Ok() / server.Fail(msg) / server.Of(data) for creation
//   - Fluent setters for chaining: out.SetOk().SetMsg("done").SetData(result)
//   - View for server-side template rendering
type Out struct {
	code int
	msg  string
	data interface{}
	view string
}

// ---- Static constructors ----

// Ok creates an Out with success status.
func Ok() *Out {
	return &Out{code: CodeOK, msg: "ok"}
}

// OkMsg creates an Out with success status and a custom message.
func OkMsg(msg string) *Out {
	return &Out{code: CodeOK, msg: msg}
}

// OkMsgf creates an Out with success status and a formatted message.
func OkMsgf(format string, args ...interface{}) *Out {
	return &Out{code: CodeOK, msg: fmt.Sprintf(format, args...)}
}

// Fail creates an Out with failure status and a message.
func Fail(msg string) *Out {
	return &Out{code: CodeFail, msg: msg}
}

// FailMsgf creates an Out with failure status and a formatted message.
func FailMsgf(format string, args ...interface{}) *Out {
	return &Out{code: CodeFail, msg: fmt.Sprintf(format, args...)}
}

// FailWithCode creates an Out with a specific error code and message.
func FailWithCode(code int, msg string) *Out {
	return &Out{code: code, msg: msg}
}

// Of creates an Out with data. Code defaults to 0 (success).
func Of(data interface{}) *Out {
	return &Out{data: data}
}

// OfField creates an Out with a key-value pair in data.
func OfField(field string, value interface{}) *Out {
	return (&Out{}).Set(field, value)
}

// Forward creates an Out that forwards to another action path.
func Forward(path string) *Out {
	return &Out{view: "forward:" + path}
}

// Compile-time check: Out implements aifei.Output.
var _ aifei.Output = (*Out)(nil)

// ---- Output interface (aifei.Output) ----

// Code returns the business status code (0 = success).
func (o *Out) Code() int { return o.code }

// Msg returns the message.
func (o *Out) Msg() string { return o.msg }

// Data returns the response data.
func (o *Out) Data() interface{} { return o.data }

// ---- Fluent setters ----

// SetOk sets the status to success.
func (o *Out) SetOk() *Out {
	o.code = CodeOK
	if o.msg == "" {
		o.msg = "ok"
	}
	return o
}

// SetFail sets the status to failure.
func (o *Out) SetFail() *Out {
	o.code = CodeFail
	if o.msg == "" {
		o.msg = "fail"
	}
	return o
}

// SetMsg sets the message.
func (o *Out) SetMsg(msg string) *Out {
	o.msg = msg
	return o
}

// SetMsgf sets the message using a format string.
func (o *Out) SetMsgf(format string, args ...interface{}) *Out {
	o.msg = fmt.Sprintf(format, args...)
	return o
}

// SetData sets the data payload.
func (o *Out) SetData(data interface{}) *Out {
	o.data = data
	return o
}

// Set adds a key-value pair to data (data becomes a map if it isn't already).
func (o *Out) Set(field string, value interface{}) *Out {
	m, ok := o.data.(map[string]interface{})
	if !ok {
		if o.data != nil {
			panic("Out.Set: data is not a map and not nil")
		}
		m = make(map[string]interface{})
		o.data = m
	}
	m[field] = value
	return o
}

// Get retrieves a value from data (expects data to be a map).
func (o *Out) Get(field string) interface{} {
	m, ok := o.data.(map[string]interface{})
	if !ok {
		return nil
	}
	return m[field]
}

// SetView sets the template view path for server-side rendering.
func (o *Out) SetView(view string) *Out {
	o.view = view
	return o
}

// View returns the template view path.
func (o *Out) View() string {
	return o.view
}

// Clear resets code, msg, and data.
func (o *Out) Clear() *Out {
	o.code = 0
	o.msg = ""
	o.data = nil
	o.view = ""
	return o
}

// ---- Helpers ----

// IsOk returns true if the status is success.
func (o *Out) IsOk() bool {
	return o.code == CodeOK
}

// ShouldRollback returns true when the code is not OK, for transaction rollback.
func (o *Out) ShouldRollback() bool {
	return o.code != CodeOK
}
