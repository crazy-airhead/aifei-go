package server

import (
	"bytes"
	"fmt"
	"io"

	"github.com/crazy-airhead/aifei-go/aifei"
)

// OutCode values follow the Java aifei-vip-arch convention:
// 0 = success, 500 = error.
const (
	CodeOK   = 0
	CodeFail = 500
)

// Out is a transport-agnostic result builder that implements aifei.Output.
// It accumulates code, message, data, and rendering intent during action
// execution. The IoHandler inspects that intent to decide HOW to write the
// response: JSON (default), an enjoy HTML view, a file download, raw/inline
// bytes, a redirect, plus optional custom headers/cookies.
//
// Business code never touches net/http: file and header output is expressed via
// closures/builder objects (FileSender, Headers) that the IoHandler applies.
//
// Pattern from Java aifei-vip-arch:
//   - server.Ok() / server.Fail(msg) / server.Of(data) for creation
//   - Fluent setters for chaining: out.SetOk().SetMsg("done").SetData(result)
//   - server.OfFile(...) / server.OfRaw(...) / server.Redirect(...) / out.SetView(...)
type Out struct {
	code int
	msg  string
	data interface{}
	view string

	// Rendering intent, inspected by IoHandler.Handle. At most one of these
	// modes is "active"; see IoHandler.Handle for precedence.
	forwardPath    string            // forward to another action path
	headers        *Headers          // response headers/cookies
	fileSender     func(*FileSender) // file download / export
	rawContentType string            // inline raw bytes content type
	rawBody        io.Reader         // inline raw bytes body
	rawSize        int64             // inline raw Content-Length; 0 = unknown
	redirectURL    string            // redirect Location
	redirectStatus int               // redirect status (0 → 302 in IoHandler)
}

// ---- Static constructors ----

// Ok creates an Out with success status. An optional message can be passed.
func Ok(msg ...string) *Out {
	if len(msg) > 0 {
		return &Out{code: CodeOK, msg: msg[0]}
	}
	return &Out{code: CodeOK, msg: "ok"}
}

// Fail creates an Out with failure status. msg is used directly when no args
// are given; otherwise it is treated as a fmt.Sprintf format string.
func Fail(msg string, args ...interface{}) *Out {
	if len(args) == 0 {
		return &Out{code: CodeFail, msg: msg}
	}
	return &Out{code: CodeFail, msg: fmt.Sprintf(msg, args...)}
}

// FailWithCode creates an Out with a specific error code and message.
func FailWithCode(code int, msg string) *Out {
	return &Out{code: code, msg: msg}
}

// Of creates an Out with data. Code defaults to 0 (success).
func Of(data interface{}) *Out {
	return &Out{code: CodeOK, data: data}
}

// OfField creates an Out with a key-value pair in data.
func OfField(field string, value interface{}) *Out {
	return (&Out{code: CodeOK}).Set(field, value)
}

// Forward creates an Out that forwards to another action path. The HTTP adapter
// re-dispatches the forward path (it is not rendered to the client).
func Forward(path string) *Out {
	return (&Out{code: CodeOK}).SetForward(path)
}

// Redirect creates an Out that responds with an HTTP redirect. status defaults
// to 302 (Found) when omitted; pass 301/307/308 to change it. Unlike JSON
// responses, the HTTP status carries the semantics here.
func Redirect(url string, status ...int) *Out {
	o := &Out{redirectURL: url}
	if len(status) > 0 {
		o.redirectStatus = status[0]
	}
	return o
}

// OfFile creates an Out that downloads a file or streams generated content.
// The closure populates a FileSender (SetFileName/SetSaveAsName/SetData/...);
// the IoHandler performs the write.
func OfFile(fn func(*FileSender)) *Out {
	return &Out{code: CodeOK, fileSender: fn}
}

// OfRaw creates an Out that writes inline bytes with the given content type
// (images, PDFs, previews, SSE payloads). No Content-Disposition is set.
func OfRaw(contentType string, data []byte) *Out {
	return &Out{code: CodeOK, rawContentType: contentType, rawBody: bytes.NewReader(data), rawSize: int64(len(data))}
}

// OfRawReader is like OfRaw but streams from a reader (preferred for large or
// size-unknown content, e.g. a storage object body). Use SetRawSize to set
// Content-Length when the length is known.
func OfRawReader(contentType string, body io.Reader) *Out {
	return &Out{code: CodeOK, rawContentType: contentType, rawBody: body}
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

// SetMsg sets the message. If no args are given, msg is used directly.
// Otherwise msg is treated as a format string.
func (o *Out) SetMsg(msg string, args ...interface{}) *Out {
	if len(args) == 0 {
		o.msg = msg
	} else {
		o.msg = fmt.Sprintf(msg, args...)
	}
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

// ---- Rendering-intent setters (consumed by IoHandler.Handle) ----

// SetForward sets a forward path. The HTTP adapter re-dispatches this path
// instead of rendering the Out to the client.
func (o *Out) SetForward(path string) *Out {
	o.forwardPath = path
	return o
}

// SetRedirect sets a redirect. status defaults to 302 when 0.
func (o *Out) SetRedirect(url string, status ...int) *Out {
	o.redirectURL = url
	if len(status) > 0 {
		o.redirectStatus = status[0]
	}
	return o
}

// SetFile sets the file-download closure (see OfFile).
func (o *Out) SetFile(fn func(*FileSender)) *Out {
	o.fileSender = fn
	return o
}

// SetRaw sets inline raw bytes with the given content type (see OfRaw).
func (o *Out) SetRaw(contentType string, data []byte) *Out {
	o.rawContentType = contentType
	o.rawBody = bytes.NewReader(data)
	o.rawSize = int64(len(data))
	return o
}

// SetRawReader sets inline raw bytes from a reader (see OfRawReader).
func (o *Out) SetRawReader(contentType string, body io.Reader) *Out {
	o.rawContentType = contentType
	o.rawBody = body
	return o
}

// SetRawSize sets an explicit Content-Length for the inline raw body (useful
// when streaming from a reader of known length). Returns o for chaining.
func (o *Out) SetRawSize(size int64) *Out {
	o.rawSize = size
	return o
}

// SetHeaders attaches response headers/cookies, applied by the IoHandler before
// the response body is written.
func (o *Out) SetHeaders(h *Headers) *Out {
	o.headers = h
	return o
}

// Clear resets code, msg, data, view, and all rendering intent.
func (o *Out) Clear() *Out {
	o.code = CodeOK
	o.msg = ""
	o.data = nil
	o.view = ""
	o.forwardPath = ""
	o.headers = nil
	o.fileSender = nil
	o.rawContentType = ""
	o.rawBody = nil
	o.rawSize = 0
	o.redirectURL = ""
	o.redirectStatus = 0
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

// ---- Accessors consumed by IoHandler.Handle ----

// ForwardPath returns the forward target, or "" when not forwarding.
func (o *Out) ForwardPath() string { return o.forwardPath }

// RedirectURL returns the redirect Location, or "" when not redirecting.
func (o *Out) RedirectURL() string { return o.redirectURL }

// RedirectStatus returns the redirect status (0 means "use default" — the
// IoHandler treats it as 302).
func (o *Out) RedirectStatus() int { return o.redirectStatus }

// HeadersOut returns the attached headers/cookies builder, or nil.
func (o *Out) HeadersOut() *Headers { return o.headers }

// FileSenderOut returns the file-download closure, or nil.
func (o *Out) FileSenderOut() func(*FileSender) { return o.fileSender }

// RawBody returns the inline raw body reader, or nil.
func (o *Out) RawBody() io.Reader { return o.rawBody }

// RawContentType returns the inline raw content type, or "".
func (o *Out) RawContentType() string { return o.rawContentType }

// RawSize returns the inline raw Content-Length, or 0 when unknown.
func (o *Out) RawSize() int64 { return o.rawSize }
