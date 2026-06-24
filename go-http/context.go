package gohttp

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/crazy-airhead/aifei-go"
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
	_ = c.ensureBody()
	if c.Request.Form != nil {
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

func (c *HttpContext) GetStr(key string) string { return c.getVal(key) }

func (c *HttpContext) GetStrDefault(key, def string) string {
	v := c.getVal(key)
	if v == "" {
		return def
	}
	return v
}

func (c *HttpContext) GetInt(key string) int         { return c.GetIntDefault(key, 0) }
func (c *HttpContext) GetInt64(key string) int64     { return c.GetInt64Default(key, 0) }
func (c *HttpContext) GetFloat64(key string) float64 { return c.GetFloat64Default(key, 0) }
func (c *HttpContext) GetBool(key string) bool       { return c.GetBoolDefault(key, false) }

func (c *HttpContext) GetIntDefault(key string, def int) int {
	v := c.getVal(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func (c *HttpContext) GetInt64Default(key string, def int64) int64 {
	v := c.getVal(key)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}

func (c *HttpContext) GetFloat64Default(key string, def float64) float64 {
	v := c.getVal(key)
	if v == "" {
		return def
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return n
}

func (c *HttpContext) GetBoolDefault(key string, def bool) bool {
	v := c.getVal(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func (c *HttpContext) GetBean(obj interface{}) error {
	body := c.Body()
	if len(body) == 0 {
		return io.EOF
	}
	initEmbeddedPointers(reflect.ValueOf(obj))
	return json.Unmarshal(body, obj)
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

func (c *HttpContext) GetMap() map[string]interface{} {
	q := c.Query()
	m := make(map[string]interface{}, len(q))
	for k, v := range q {
		if len(v) > 0 {
			m[k] = v[0]
		}
	}
	return m
}

func (c *HttpContext) getVal(key string) string {
	if v := c.Request.URL.Query().Get(key); v != "" {
		return v
	}
	_ = c.ensureBody()
	if c.Request.Form != nil {
		if v := c.Request.Form.Get(key); v != "" {
			return v
		}
	}
	return ""
}

func (c *HttpContext) ensureBody() error {
	if c.Request.Form == nil {
		contentType := c.Request.Header.Get("Content-Type")
		if strings.HasPrefix(contentType, "application/x-www-form-urlencoded") ||
			strings.HasPrefix(contentType, "multipart/form-data") {
			return c.Request.ParseForm()
		}
	}
	return nil
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
