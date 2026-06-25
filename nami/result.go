package nami

import (
	"encoding/json"
	"fmt"
)

// Result wraps an RPC response.
type Result struct {
	code    int
	headers map[string][]string
	charset string
	body    []byte

	bodyString string
}

// NewResult creates a Result with the given status code and body bytes.
func NewResult(code int, body []byte) *Result {
	return &Result{
		code:    code,
		body:    body,
		charset: "utf-8",
		headers: make(map[string][]string),
	}
}

// Code returns the HTTP status code.
func (r *Result) Code() int { return r.code }

// Body returns the raw response body.
func (r *Result) Body() []byte { return r.body }

// Charset returns the response charset.
func (r *Result) Charset() string { return r.charset }

// SetCharset sets the response charset.
func (r *Result) SetCharset(cs string) { r.charset = cs }

// Headers returns all response headers.
func (r *Result) Headers() map[string][]string { return r.headers }

// HeaderGet returns the first value for a header name.
func (r *Result) HeaderGet(name string) string {
	if vals, ok := r.headers[name]; ok && len(vals) > 0 {
		return vals[0]
	}
	return ""
}

// HeaderAdd adds a header value.
func (r *Result) HeaderAdd(name, value string) {
	r.headers[name] = append(r.headers[name], value)
}

// BodyAsString returns the response body as a string, cached after first call.
func (r *Result) BodyAsString() string {
	if r.bodyString == "" && r.body != nil {
		r.bodyString = string(r.body)
		r.body = nil // free the bytes after converting
	}
	return r.bodyString
}

// AssertSuccess checks if the status code indicates success (2xx).
func (r *Result) AssertSuccess() error {
	if r.code >= 400 {
		body := r.BodyAsString()
		if body != "" {
			return fmt.Errorf("nami call failure, code: %d, message: %s", r.code, body)
		}
		return fmt.Errorf("nami call failure, code: %d", r.code)
	}
	return nil
}

// Bind deserializes the result body as JSON into val, which must be a non-nil
// pointer. The HTTP status is checked first; a non-2xx response returns an error.
//
//	var item MyStruct
//	if err := result.Bind(&item); err != nil { ... }
func (r *Result) Bind(val any) error {
	if err := r.AssertSuccess(); err != nil {
		return err
	}
	str := r.BodyAsString()
	if str == "" || str == "null" {
		return nil
	}
	return json.Unmarshal([]byte(str), val)
}

// AsAny deserializes the result body as JSON into an interface{} value. JSON
// objects become map[string]any, arrays become []any, and primitives are
// returned as their Go equivalents. The HTTP status is checked first.
func (r *Result) AsAny() (any, error) {
	if err := r.AssertSuccess(); err != nil {
		return nil, err
	}
	str := r.BodyAsString()
	if str == "" || str == "null" {
		return nil, nil
	}
	var v any
	if err := json.Unmarshal([]byte(str), &v); err != nil {
		return nil, fmt.Errorf("nami json decode: %w", err)
	}
	return v, nil
}
