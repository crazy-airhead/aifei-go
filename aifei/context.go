package aifei

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
)

// Context combines Java's Input and Output into a unified request/response context.
type Context struct {
	Request  *http.Request
	Writer   http.ResponseWriter
	params   map[string]string // path parameters
	handlers []HandlerFunc     // middleware + handler chain
	index    int               // current handler index (-1 = not started)
	status   int
	written  bool
	body     []byte
	bodyRead bool
}

// newContext creates a Context for the given request/response pair.
func newContext(w http.ResponseWriter, r *http.Request) *Context {
	return &Context{
		Request: r,
		Writer:  w,
		index:   -1,
	}
}

// ---- Request (Input) ----

// Method returns the HTTP method.
func (c *Context) Method() string {
	return c.Request.Method
}

// Path returns the request path.
func (c *Context) Path() string {
	return c.Request.URL.Path
}

// RemoteIP returns the client IP address.
func (c *Context) RemoteIP() string {
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

// Has checks if a query or form parameter exists.
func (c *Context) Has(name string) bool {
	_ = c.ensureBody()
	if c.Request.Form != nil {
		if _, ok := c.Request.Form[name]; ok {
			return true
		}
	}
	return c.Request.URL.Query().Get(name) != ""
}

// HasPara checks if a path parameter exists at the given index.
func (c *Context) HasPara(index int) bool {
	if c.params == nil {
		return false
	}
	return index < len(c.params)
}

// PathPara returns the path parameter by index. Returns empty string if not found.
func (c *Context) PathPara(index int) string {
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

// PathParaByName returns the path parameter by name.
func (c *Context) PathParaByName(name string) string {
	if c.params == nil {
		return ""
	}
	return c.params[name]
}

// Param returns the path parameter by name.
func (c *Context) Param(name string) string {
	return c.PathParaByName(name)
}

// GetStr returns a query/form parameter as string.
func (c *Context) GetStr(key string) string {
	return c.getVal(key)
}

// GetStrDefault returns a query/form parameter as string with default.
func (c *Context) GetStrDefault(key, def string) string {
	v := c.getVal(key)
	if v == "" {
		return def
	}
	return v
}

// GetInt returns a query/form parameter as int.
func (c *Context) GetInt(key string) int {
	return c.GetIntDefault(key, 0)
}

// GetIntDefault returns a query/form parameter as int with default.
func (c *Context) GetIntDefault(key string, def int) int {
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

// GetInt64 returns a query/form parameter as int64.
func (c *Context) GetInt64(key string) int64 {
	return c.GetInt64Default(key, 0)
}

// GetInt64Default returns a query/form parameter as int64 with default.
func (c *Context) GetInt64Default(key string, def int64) int64 {
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

// GetFloat64 returns a query/form parameter as float64.
func (c *Context) GetFloat64(key string) float64 {
	return c.GetFloat64Default(key, 0)
}

// GetFloat64Default returns a query/form parameter as float64 with default.
func (c *Context) GetFloat64Default(key string, def float64) float64 {
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

// GetBool returns a query/form parameter as bool.
func (c *Context) GetBool(key string) bool {
	return c.GetBoolDefault(key, false)
}

// GetBoolDefault returns a query/form parameter as bool with default.
func (c *Context) GetBoolDefault(key string, def bool) bool {
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

// GetBean parses JSON body into a struct.
// It initializes nil embedded pointer fields so that promoted
// json.Unmarshaler methods work correctly.
func (c *Context) GetBean(obj interface{}) error {
	body := c.Body()
	if len(body) == 0 {
		return fmt.Errorf("empty body")
	}
	initEmbeddedPointers(reflect.ValueOf(obj))
	return json.Unmarshal(body, obj)
}

// initEmbeddedPointers recursively initializes nil embedded pointer fields
// so that json.Unmarshal can safely call promoted methods through them.
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

// Body returns the raw request body (lazy read).
func (c *Context) Body() []byte {
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

// Query returns query parameters as url.Values.
func (c *Context) Query() url.Values {
	return c.Request.URL.Query()
}

// getVal gets a value from query params, then form params.
func (c *Context) getVal(key string) string {
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

// ensureBody parses the form body if needed.
func (c *Context) ensureBody() error {
	if c.Request.Form == nil {
		contentType := c.Request.Header.Get("Content-Type")
		if strings.HasPrefix(contentType, "application/x-www-form-urlencoded") ||
			strings.HasPrefix(contentType, "multipart/form-data") {
			return c.Request.ParseForm()
		}
	}
	return nil
}

// ---- Response (Output) ----

// Status sets the HTTP status code.
func (c *Context) Status(code int) *Context {
	c.status = code
	return c
}

// Header sets a response header.
func (c *Context) Header(key, value string) {
	c.Writer.Header().Set(key, value)
}

// write ensures headers are written before body.
func (c *Context) write(code int, contentType string) {
	if c.written {
		return
	}
	if c.status == 0 {
		c.status = code
	}
	c.Writer.Header().Set("Content-Type", contentType)
	c.Writer.WriteHeader(c.status)
	c.written = true
}

// Json writes a JSON response.
func (c *Context) Json(data interface{}) {
	body, err := json.Marshal(data)
	if err != nil {
		c.Status(500).Text(`{"code":500,"msg":"json marshal error"}`)
		return
	}
	c.write(200, "application/json; charset=utf-8")
	c.Writer.Write(body)
}

// JsonOK writes a success JSON response.
func (c *Context) JsonOK(data interface{}) {
	c.Json(map[string]interface{}{
		"code": 0,
		"msg":  "ok",
		"data": data,
	})
}

// JsonFail writes an error JSON response.
func (c *Context) JsonFail(msg string) {
	c.Json(map[string]interface{}{
		"code": -1,
		"msg":  msg,
	})
}

// Text writes a plain text response.
func (c *Context) Text(format string, args ...interface{}) {
	c.write(200, "text/plain; charset=utf-8")
	c.Writer.Write([]byte(fmt.Sprintf(format, args...)))
}

// Html writes an HTML response.
func (c *Context) Html(html string) {
	c.write(200, "text/html; charset=utf-8")
	c.Writer.Write([]byte(html))
}

// Redirect sends an HTTP redirect.
func (c *Context) Redirect(url string) {
	c.Writer.Header().Set("Location", url)
	c.Writer.WriteHeader(http.StatusFound)
	c.written = true
}

// SetCookie adds a Set-Cookie header.
func (c *Context) SetCookie(name, value string, maxAge int, path, domain string, secure, httpOnly bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    value,
		MaxAge:   maxAge,
		Path:     path,
		Domain:   domain,
		Secure:   secure,
		HttpOnly: httpOnly,
	})
}

// ---- Chain control ----

// Next calls the next handler in the chain.
func (c *Context) Next() {
	c.index++
	for c.index < len(c.handlers) {
		c.handlers[c.index](c)
		c.index++
	}
}

// Abort stops the handler chain.
func (c *Context) Abort() {
	c.index = len(c.handlers)
}
