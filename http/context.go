package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/crazy-airhead/aifei-go/aifei"
)

// bodyType describes what kind of request body was detected.
type bodyType int

const (
	bodyNone bodyType = iota // no body — parse from query string
	bodyJSON                 // raw body (JSON or other Content-Type)
	bodyForm                 // form-encoded — Request.Form populated by ParseForm (includes merged query params)
)

// HTTPMeta is the HTTP-specific request metadata that goes beyond the
// transport-agnostic aifei.Meta: the HTTP method verb, the client address,
// and cookies. HttpContext satisfies it; HTTP-aware code obtains it via a
// type assertion on aifei.Input (e.g. an HTTP logger), so the core aifei.Input
// stays free of HTTP concepts.
type HTTPMeta interface {
	Method() string
	RemoteIP() string
	Cookie(name string) string
}

// HttpContext implements aifei.Input by wrapping *http.Request.
type HttpContext struct {
	Request  *http.Request
	params   map[string]string
	body     []byte
	bodyRead bool
}

// Compile-time guarantees that *HttpContext satisfies the aifei contracts and
// the HTTP-specific HTTPMeta extension.
var (
	_ aifei.Param = (*HttpContext)(nil)
	_ aifei.Meta  = (*HttpContext)(nil)
	_ aifei.Input = (*HttpContext)(nil)
	_ HTTPMeta    = (*HttpContext)(nil)
)

// NewInput creates an HttpContext from an http.Request.
func NewInput(r *http.Request) *HttpContext {
	return &HttpContext{Request: r}
}

// SetParams sets the path parameters extracted by the router.
func (c *HttpContext) SetParams(params map[string]string) {
	c.params = params
}

// ---- Input interface ----

func (c *HttpContext) Method() string { return c.Request.Method }
func (c *HttpContext) Path() string   { return c.Request.URL.Path }

func (c *HttpContext) RemoteIP() string {
	if ip := c.Request.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	if ip := c.Request.Header.Get("X-Forwarded-For"); ip != "" {
		if idx := strings.Index(ip, ","); idx != -1 {
			return strings.TrimSpace(ip[:idx])
		}
		return ip
	}
	host, _, err := net.SplitHostPort(c.Request.RemoteAddr)
	if err != nil {
		return c.Request.RemoteAddr
	}
	return host
}

// Header returns the first value of the named request header (case-insensitive), or "" if absent.
func (c *HttpContext) Header(name string) string {
	return c.Request.Header.Get(name)
}

// Cookie returns the value of the named cookie, or "" if it is not present.
func (c *HttpContext) Cookie(name string) string {
	ck, err := c.Request.Cookie(name)
	if err != nil {
		return ""
	}
	return ck.Value
}

// Context returns the request's context. It is cancelled when the client connection closes
// or the request completes, so it is safe to pass to downstream calls (db, RPC) for cancellation.
func (c *HttpContext) Context() context.Context {
	return c.Request.Context()
}

func (c *HttpContext) Has(name string) bool {
	bt, _ := c.ensureBody()
	if bt == bodyForm && c.Request.Form != nil {
		if _, ok := c.Request.Form[name]; ok {
			return true
		}
	}
	return c.Request.URL.Query().Get(name) != ""
}

func (c *HttpContext) PathPara(index int) string {
	if c.params == nil {
		return ""
	}
	keys := make([]string, 0, len(c.params))
	for k := range c.params {
		keys = append(keys, k)
	}
	if index >= len(keys) {
		return ""
	}
	return c.params[keys[index]]
}

func (c *HttpContext) PathParaByName(name string) string {
	if c.params == nil {
		return ""
	}
	return c.params[name]
}

func (c *HttpContext) Param(name string) string { return c.PathParaByName(name) }

func (c *HttpContext) GetStr(key string, def ...string) string {
	v := c.getVal(key)
	if v == "" && len(def) > 0 {
		return def[0]
	}
	return v
}

func (c *HttpContext) GetInt(key string, def ...int) int {
	v := c.getVal(key)
	if v == "" {
		if len(def) > 0 {
			return def[0]
		}
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		if len(def) > 0 {
			return def[0]
		}
		return 0
	}
	return n
}

func (c *HttpContext) GetInt64(key string, def ...int64) int64 {
	v := c.getVal(key)
	if v == "" {
		if len(def) > 0 {
			return def[0]
		}
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		if len(def) > 0 {
			return def[0]
		}
		return 0
	}
	return n
}

func (c *HttpContext) GetFloat64(key string, def ...float64) float64 {
	v := c.getVal(key)
	if v == "" {
		if len(def) > 0 {
			return def[0]
		}
		return 0
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		if len(def) > 0 {
			return def[0]
		}
		return 0
	}
	return n
}

func (c *HttpContext) GetBool(key string, def ...bool) bool {
	v := c.getVal(key)
	if v == "" {
		if len(def) > 0 {
			return def[0]
		}
		return false
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		if len(def) > 0 {
			return def[0]
		}
		return false
	}
	return b
}

// GetBean binds request parameters to obj. It reads from the request body
// (form-encoded or raw JSON), falling back to query parameters when the body is
// empty. All three sources go through the same JSON unmarshal path so callers
// can pass id via query, form, or JSON body transparently.
//
// With no keys the entire parameter set is used; each key walks one level deeper:
//
//	GetBean(&user)                  → whole body
//	GetBean(&user, "data")          → body["data"]
//	GetBean(&city, "data", "addr")  → body["data"]["addr"]
func (c *HttpContext) GetBean(obj interface{}, keys ...string) error {
	bt, err := c.ensureBody()
	if err != nil {
		return err
	}

	var data []byte
	switch bt {
	case bodyForm:
		if c.Request.Form != nil && len(c.Request.Form) > 0 {
			formMap := make(map[string]interface{}, len(c.Request.Form))
			for key, values := range c.Request.Form {
				if len(values) > 0 {
					formMap[key] = values[0]
				}
			}
			data, err = json.Marshal(formMap)
			if err != nil {
				return err
			}
		}
	case bodyJSON:
		data = c.Body()
	case bodyNone:
		query := c.Request.URL.Query()
		if len(query) > 0 {
			queryMap := make(map[string]interface{}, len(query))
			for key, values := range query {
				if len(values) > 0 {
					queryMap[key] = values[0]
				}
			}
			data, err = json.Marshal(queryMap)
			if err != nil {
				return err
			}
		}
	}

	if len(data) == 0 {
		return io.EOF
	}

	for _, key := range keys {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(data, &m); err != nil {
			return fmt.Errorf("parse body at %q: %w", key, err)
		}
		raw, ok := m[key]
		if !ok {
			return io.EOF
		}
		data = raw
	}

	initEmbeddedPointers(reflect.ValueOf(obj))
	return json.Unmarshal(data, obj)
}

func (c *HttpContext) Body() []byte {
	if !c.bodyRead {
		c.bodyRead = true
		if c.Request.Body != nil {
			body, err := io.ReadAll(c.Request.Body)
			if err == nil {
				c.body = body
			}
		}
	}
	return c.body
}

func (c *HttpContext) Query() url.Values { return c.Request.URL.Query() }

// GetMap returns request parameters as a map. It reads from the request body
// (form-encoded or raw JSON), falling back to query parameters when the body is
// empty — the same resolution order as GetBean.
//
// Pass keys to filter by a dot-joined prefix:
//
//	GetMap()                  → all params
//	GetMap("user")            → "user.*" params, prefix stripped
//	GetMap("user", "addr")    → "user.addr.*" params
func (c *HttpContext) GetMap(keys ...string) map[string]interface{} {
	bt, err := c.ensureBody()
	if err != nil {
		bt = bodyNone
	}

	m := make(map[string]interface{})
	switch bt {
	case bodyForm:
		if c.Request.Form != nil {
			for k, v := range c.Request.Form {
				if len(v) > 0 {
					m[k] = v[0]
				}
			}
		}
	case bodyJSON:
		if data := c.Body(); len(data) > 0 {
			// Unmarshal top-level JSON object into flat map.
			var raw map[string]interface{}
			if err := json.Unmarshal(data, &raw); err == nil {
				for k, v := range raw {
					m[k] = v
				}
			}
		}
	case bodyNone:
		for k, v := range c.Request.URL.Query() {
			if len(v) > 0 {
				m[k] = v[0]
			}
		}
	}

	// Apply prefix filter and strip.
	prefix := ""
	if len(keys) > 0 {
		prefix = strings.Join(keys, ".") + "."
	}
	if prefix == "" {
		return m
	}

	filtered := make(map[string]interface{})
	for k, v := range m {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		filtered[strings.TrimPrefix(k, prefix)] = v
	}
	return filtered
}

func (c *HttpContext) getVal(key string) string {
	if v := c.Request.URL.Query().Get(key); v != "" {
		return v
	}
	bt, _ := c.ensureBody()
	if bt == bodyForm && c.Request.Form != nil {
		if v := c.Request.Form.Get(key); v != "" {
			return v
		}
	}
	return ""
}

// ensureBody detects the request body type and parses form-encoded bodies.
// It returns the bodyType so callers can branch on the data source directly
// instead of re-inspecting Content-Type or c.Request.Form.
func (c *HttpContext) ensureBody() (bodyType, error) {
	if c.Request.Form != nil {
		return bodyForm, nil
	}
	contentType := c.Request.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/x-www-form-urlencoded") ||
		strings.HasPrefix(contentType, "multipart/form-data") {
		if err := c.Request.ParseForm(); err != nil {
			return bodyNone, err
		}
		return bodyForm, nil
	}
	if contentType != "" || c.Request.ContentLength > 0 {
		return bodyJSON, nil
	}
	return bodyNone, nil
}

func initEmbeddedPointers(v reflect.Value) {
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return
	}
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() || !f.Anonymous {
			continue
		}
		fv := v.Field(i)
		if fv.Kind() == reflect.Ptr && fv.IsNil() && fv.CanSet() {
			fv.Set(reflect.New(fv.Type().Elem()))
		}
		if fv.Kind() == reflect.Ptr && !fv.IsNil() {
			initEmbeddedPointers(fv)
		}
	}
}
