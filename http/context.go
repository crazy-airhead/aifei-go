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
// "Host" is special-cased to return Request.Host, since net/http keeps the host there for
// server requests rather than in the Header map (so middleware — e.g. tenant subdomain
// resolution — can read it via the Input interface).
func (c *HttpContext) Header(name string) string {
	if strings.EqualFold(name, "Host") {
		return c.Request.Host
	}
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

// SetContext replaces the request's context. The new context takes effect for
// subsequent Context() calls, so anything propagated on it — a transaction's
// *sql.Tx via db.WithTx, a request id, a deadline — becomes visible to the
// handler and to interceptors that run after this call. server.TxInterceptor
// uses it to inject the active transaction into the request so service methods
// can join it with db.Ctx(in.Context()).
func (c *HttpContext) SetContext(ctx context.Context) {
	c.Request = c.Request.WithContext(ctx)
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
// empty, so callers can pass id via query, form, or JSON body transparently.
//
// JSON bodies go through encoding/json directly. Form/query sources are
// string-typed, which encoding/json cannot coerce into numeric/bool fields
// (a "48" form value would fail to unmarshal into an int64). So for plain struct
// targets GetBean binds field-by-field with per-field type coercion — the same
// strconv path the typed getters use. A numeric-looking value still binds as a
// string into a string field. Targets implementing json.Unmarshaler (e.g.
// *db.Row-backed models) keep using the JSON path, where their custom unmarshaler
// accepts strings natively.
//
// With no keys the entire parameter set is used; each key walks one level deeper
// (JSON path only — form/query are flat):
//
//	GetBean(&user)                  → whole body
//	GetBean(&user, "data")          → body["data"]
//	GetBean(&city, "data", "addr")  → body["data"]["addr"]
func (c *HttpContext) GetBean(obj interface{}, keys ...string) error {
	bt, err := c.ensureBody()
	if err != nil {
		return err
	}

	// Form/query are string-typed: bind plain structs field-by-field with type
	// coercion. Non-struct targets and custom unmarshalers fall through to the
	// JSON path below.
	if (bt == bodyForm || bt == bodyNone) && len(keys) == 0 {
		if handled, err := c.bindFormStruct(obj); handled {
			return err
		}
	}

	data := c.marshalBody(bt)
	if data == nil || len(data) == 0 {
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

// jsonUnmarshalerType is the reflect.Type for json.Unmarshaler, used to detect
// targets (like *db.Row-backed models) that own their JSON/string parsing.
var jsonUnmarshalerType = reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()

// bindFormStruct binds form/query parameters to a plain struct obj using
// per-field type coercion. It reports handled=true when obj is a non-nil pointer
// to a struct that does not implement json.Unmarshaler — the case the JSON path
// can't handle for string-typed sources. For Row-backed models, slices, maps,
// scalars, or custom unmarshalers it reports handled=false so GetBean falls back
// to the JSON path, where the custom UnmarshalJSON accepts strings natively.
func (c *HttpContext) bindFormStruct(obj interface{}) (handled bool, err error) {
	rv := reflect.ValueOf(obj)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return false, nil
	}
	if rv.Type().Implements(jsonUnmarshalerType) {
		return false, nil
	}
	elem := rv.Elem()
	if elem.Kind() != reflect.Struct {
		return false, nil // slice/map/scalar → JSON path
	}

	params := c.formValues()
	if len(params) == 0 {
		return true, io.EOF
	}
	initEmbeddedPointers(rv)
	bindStructFields(elem, params)
	return true, nil
}

// formValues merges form and query parameters. Query overrides form on key
// conflicts, matching getVal's precedence (query is consulted first).
func (c *HttpContext) formValues() url.Values {
	out := make(url.Values, len(c.Request.Form)+len(c.Request.URL.Query()))
	for k, vs := range c.Request.Form {
		if len(vs) > 0 {
			out[k] = append(out[k], vs...)
		}
	}
	for k, vs := range c.Request.URL.Query() {
		if len(vs) > 0 {
			out[k] = vs // query wins
		}
	}
	return out
}

// bindStructFields walks rv's exported fields and assigns coerced values from
// params by JSON tag, recursing into embedded/anonymous structs.
func bindStructFields(rv reflect.Value, params url.Values) {
	rt := rv.Type()
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		fv := rv.Field(i)
		if f.Anonymous {
			if fv.Kind() == reflect.Ptr {
				if fv.IsNil() {
					continue
				}
				fv = fv.Elem()
			}
			if fv.Kind() == reflect.Struct {
				bindStructFields(fv, params)
			}
			continue
		}
		name := jsonFieldName(f)
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		values, ok := params[name]
		if !ok || len(values) == 0 {
			continue
		}
		setField(fv, values)
	}
}

// setField assigns coerced string value(s) to a struct field. Slices consume all
// values; scalars consume the first (dereferencing an initialized pointer).
func setField(fv reflect.Value, values []string) {
	if fv.Kind() == reflect.Slice {
		elem := fv.Type().Elem()
		slice := reflect.MakeSlice(fv.Type(), 0, len(values))
		for _, v := range values {
			if cv, ok := coerceString(elem, v); ok {
				slice = reflect.Append(slice, cv)
			}
		}
		fv.Set(slice)
		return
	}
	for fv.Kind() == reflect.Ptr {
		if fv.IsNil() {
			fv.Set(reflect.New(fv.Type().Elem()))
		}
		fv = fv.Elem()
	}
	if cv, ok := coerceString(fv.Type(), values[0]); ok {
		fv.Set(cv)
	}
}

// coerceString converts a form/query string to a reflect.Value of type t,
// reporting whether the coercion is valid for t's kind. Numeric-looking strings
// are NOT coerced for string kinds, so a value like "007" stays a string.
func coerceString(t reflect.Type, s string) (reflect.Value, bool) {
	switch t.Kind() {
	case reflect.String:
		return reflect.ValueOf(s).Convert(t), true
	case reflect.Bool:
		switch s {
		case "true", "True", "TRUE", "1":
			return reflect.ValueOf(true).Convert(t), true
		case "false", "False", "FALSE", "0":
			return reflect.ValueOf(false).Convert(t), true
		}
		return reflect.Value{}, false
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return reflect.Value{}, false
		}
		return reflect.ValueOf(n).Convert(t), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return reflect.Value{}, false
		}
		return reflect.ValueOf(n).Convert(t), true
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return reflect.Value{}, false
		}
		return reflect.ValueOf(f).Convert(t), true
	case reflect.Interface:
		return reflect.ValueOf(s), true
	}
	return reflect.Value{}, false
}

// jsonFieldName extracts the name from a struct field's `json` tag. It returns
// "" when the tag is absent or has no name (caller falls back to the field
// name), and "-" to skip the field.
func jsonFieldName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "" {
		return ""
	}
	if comma := strings.IndexByte(tag, ','); comma >= 0 {
		tag = tag[:comma]
	}
	return tag
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

// marshalBody converts the request body (form/query/raw JSON) to JSON bytes.
// For bodyForm and bodyNone it serializes the parameter map to JSON; for
// bodyJSON it returns the raw body directly. Returns nil for empty bodies.
func (c *HttpContext) marshalBody(bt bodyType) []byte {
	switch bt {
	case bodyForm:
		if c.Request.Form != nil && len(c.Request.Form) > 0 {
			formMap := make(map[string]interface{}, len(c.Request.Form))
			for key, values := range c.Request.Form {
				if len(values) > 0 {
					formMap[key] = values[0]
				}
			}
			data, _ := json.Marshal(formMap) // error is impossible for map[string]interface{}
			return data
		}
	case bodyJSON:
		return c.Body()
	case bodyNone:
		query := c.Request.URL.Query()
		if len(query) > 0 {
			queryMap := make(map[string]interface{}, len(query))
			for key, values := range query {
				if len(values) > 0 {
					queryMap[key] = values[0]
				}
			}
			data, _ := json.Marshal(queryMap)
			return data
		}
	}
	return nil
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
