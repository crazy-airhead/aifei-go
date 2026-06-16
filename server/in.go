package server

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
)

// In implements aifei.Input and wraps *http.Request for reading request parameters.
type In struct {
	Request  *http.Request
	params   map[string]string // path parameters
	body     []byte
	bodyRead bool
}

// NewIn creates an In from an http.Request.
func NewIn(r *http.Request) *In {
	return &In{Request: r}
}

// SetParams sets the path parameters (called by the router).
func (in *In) SetParams(params map[string]string) {
	in.params = params
}

// ---- Parameter existence ----

// Has checks if a query or form parameter exists.
func (in *In) Has(name string) bool {
	_ = in.ensureBody()
	if in.Request.Form != nil {
		if _, ok := in.Request.Form[name]; ok {
			return true
		}
	}
	return in.Request.URL.Query().Get(name) != ""
}

// ---- Path parameters ----

// PathPara returns the path parameter by index.
func (in *In) PathPara(index int) string {
	if in.params == nil {
		return ""
	}
	keys := make([]string, 0, len(in.params))
	for k := range in.params {
		keys = append(keys, k)
	}
	if index >= len(keys) {
		return ""
	}
	return in.params[keys[index]]
}

// PathParaByName returns the path parameter by name.
func (in *In) PathParaByName(name string) string {
	if in.params == nil {
		return ""
	}
	return in.params[name]
}

// Param returns the path parameter by name.
func (in *In) Param(name string) string {
	return in.PathParaByName(name)
}

// ---- Typed getters ----

// GetStr returns a query/form parameter as string.
func (in *In) GetStr(key string) string {
	return in.getVal(key)
}

// GetStrDefault returns a query/form parameter as string with default.
func (in *In) GetStrDefault(key, def string) string {
	v := in.getVal(key)
	if v == "" {
		return def
	}
	return v
}

// GetInt returns a query/form parameter as int.
func (in *In) GetInt(key string) int {
	return in.GetIntDefault(key, 0)
}

// GetIntDefault returns a query/form parameter as int with default.
func (in *In) GetIntDefault(key string, def int) int {
	v := in.getVal(key)
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
func (in *In) GetInt64(key string) int64 {
	return in.GetInt64Default(key, 0)
}

// GetInt64Default returns a query/form parameter as int64 with default.
func (in *In) GetInt64Default(key string, def int64) int64 {
	v := in.getVal(key)
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
func (in *In) GetFloat64(key string) float64 {
	return in.GetFloat64Default(key, 0)
}

// GetFloat64Default returns a query/form parameter as float64 with default.
func (in *In) GetFloat64Default(key string, def float64) float64 {
	v := in.getVal(key)
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
func (in *In) GetBool(key string) bool {
	return in.GetBoolDefault(key, false)
}

// GetBoolDefault returns a query/form parameter as bool with default.
func (in *In) GetBoolDefault(key string, def bool) bool {
	v := in.getVal(key)
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
func (in *In) GetBean(obj interface{}) error {
	body := in.Body()
	if len(body) == 0 {
		return io.EOF
	}
	initEmbeddedPointers(reflect.ValueOf(obj))
	return json.Unmarshal(body, obj)
}

// Body returns the raw request body (lazy read).
func (in *In) Body() []byte {
	if !in.bodyRead {
		in.bodyRead = true
		if in.Request.Body != nil {
			body, err := io.ReadAll(in.Request.Body)
			if err == nil {
				in.body = body
			}
		}
	}
	return in.body
}

// Method returns the HTTP method.
func (in *In) Method() string {
	return in.Request.Method
}

// Path returns the request path.
func (in *In) Path() string {
	return in.Request.URL.Path
}

// RemoteIP returns the client IP address.
func (in *In) RemoteIP() string {
	if ip := in.Request.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	if ip := in.Request.Header.Get("X-Forwarded-For"); ip != "" {
		if idx := strings.Index(ip, ","); idx != -1 {
			return strings.TrimSpace(ip[:idx])
		}
		return ip
	}
	host, _, err := net.SplitHostPort(in.Request.RemoteAddr)
	if err != nil {
		return in.Request.RemoteAddr
	}
	return host
}

// Query returns query parameters as url.Values.
func (in *In) Query() url.Values {
	return in.Request.URL.Query()
}

// GetMap returns query parameters as map[string]interface{} for Enjoy SQL templates.
func (in *In) GetMap() map[string]interface{} {
	q := in.Query()
	m := make(map[string]interface{}, len(q))
	for k, v := range q {
		if len(v) > 0 {
			m[k] = v[0]
		}
	}
	return m
}

// ---- Internal helpers ----

func (in *In) getVal(key string) string {
	if v := in.Request.URL.Query().Get(key); v != "" {
		return v
	}
	_ = in.ensureBody()
	if in.Request.Form != nil {
		if v := in.Request.Form.Get(key); v != "" {
			return v
		}
	}
	return ""
}

func (in *In) ensureBody() error {
	if in.Request.Form == nil {
		contentType := in.Request.Header.Get("Content-Type")
		if strings.HasPrefix(contentType, "application/x-www-form-urlencoded") ||
			strings.HasPrefix(contentType, "multipart/form-data") {
			return in.Request.ParseForm()
		}
	}
	return nil
}

// initEmbeddedPointers initializes nil embedded pointer fields so that
// promoted json.Unmarshaler methods work correctly.
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
