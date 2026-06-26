package server

import (
	"net/http"
)

// Cookie is a transport-agnostic description of an HTTP response cookie. It is
// the Go analogue of Java aifei-vip-arch's cookie handling on Headers, kept free
// of net/http coupling so Out can carry it without importing the server.
type Cookie struct {
	Name     string
	Value    string
	MaxAge   int    // seconds; 0 means "until browser close", <0 deletes the cookie
	Path     string // defaults to "/" when empty
	Domain   string
	HttpOnly bool
	Secure   bool
	SameSite http.SameSite
}

// Headers is a transport-agnostic builder for HTTP response headers and cookies.
// Business code records header/cookie ops here without touching the HTTP layer;
// the IoHandler translates them onto the real http.ResponseWriter at write time.
//
// Mirrors Java aifei-vip-arch cn.aifei.vip.arch.http.Headers.
type Headers struct {
	setHeaders []headerOp // applied in record order
	cookies    []*Cookie
}

// headerOp records a single header mutation. add=true appends (Set-Cookie style),
// otherwise the value replaces any existing header.
type headerOp struct {
	name  string
	value string
	add   bool
}

// SetHeader sets (replaces) a response header. Returns h for chaining.
func (h *Headers) SetHeader(name, value string) *Headers {
	if h == nil {
		return nil
	}
	h.setHeaders = append(h.setHeaders, headerOp{name: name, value: value})
	return h
}

// AddHeader appends a response header, allowing multiple values for the same
// name (e.g. Set-Cookie, Cache-Control). Returns h for chaining.
func (h *Headers) AddHeader(name, value string) *Headers {
	if h == nil {
		return nil
	}
	h.setHeaders = append(h.setHeaders, headerOp{name: name, value: value, add: true})
	return h
}

// AddCookie adds a response cookie. Returns h for chaining.
func (h *Headers) AddCookie(c Cookie) *Headers {
	if h == nil {
		return nil
	}
	if c.Path == "" {
		c.Path = "/"
	}
	h.cookies = append(h.cookies, &c)
	return h
}

// RemoveCookie removes a cookie by setting it expired with no value. Returns h
// for chaining.
func (h *Headers) RemoveCookie(name string) *Headers {
	return h.AddCookie(Cookie{Name: name, Value: "", MaxAge: -1})
}

// apply writes the recorded headers and cookies onto w. Called by the IoHandler
// before the response body is written.
func (h *Headers) apply(w http.ResponseWriter) {
	if h == nil {
		return
	}
	hdr := w.Header()
	for _, op := range h.setHeaders {
		if op.add {
			hdr.Add(op.name, op.value)
		} else {
			hdr.Set(op.name, op.value)
		}
	}
	for _, c := range h.cookies {
		http.SetCookie(w, cookieToHTTP(c))
	}
}

// cookieToHTTP translates a transport-agnostic Cookie into a net/http cookie.
func cookieToHTTP(c *Cookie) *http.Cookie {
	hc := &http.Cookie{
		Name:     c.Name,
		Value:    c.Value,
		MaxAge:   c.MaxAge,
		Path:     c.Path,
		Domain:   c.Domain,
		HttpOnly: c.HttpOnly,
		Secure:   c.Secure,
		SameSite: c.SameSite,
	}
	return hc
}
