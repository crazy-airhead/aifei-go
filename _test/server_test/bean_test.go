package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/crazy-airhead/aifei-go/aifei"
	"github.com/crazy-airhead/aifei-go/server"
)

// newJSONRequest builds a request with a JSON body.
func newJSONRequest(t *testing.T, method, path, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req
}

// newFormRequest builds a urlencoded form request.
func newFormRequest(t *testing.T, body *strings.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequest("POST", "/x", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

// newQueryRequest builds a GET request with query params and no body.
func newQueryRequest(t *testing.T, query string) *http.Request {
	t.Helper()
	req, err := http.NewRequest("GET", "/x?"+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

type beanLoginReq struct {
	User string `json:"user"`
	Age  int    `json:"age"`
}

func TestAifeiBeanJSONBody(t *testing.T) {
	req := newJSONRequest(t, "POST", "/x", `{"user":"james","age":18}`)
	in := server.NewIn(req)

	// Package-level generic helper over the aifei.Input interface — the main
	// entry for service code whose signature is func(in aifei.Input).
	v, err := aifei.Bean[beanLoginReq](in)
	if err != nil {
		t.Fatalf("aifei.Bean failed: %v", err)
	}
	if v.User != "james" || v.Age != 18 {
		t.Errorf("Bean = %+v, want {james 18}", v)
	}

	// Nested subtree binding: GetBean(&v, "data") shape
	req2 := newJSONRequest(t, "POST", "/x", `{"data":{"user":"bond","age":42}}`)
	v2, err := aifei.Bean[beanLoginReq](server.NewIn(req2), "data")
	if err != nil {
		t.Fatalf("aifei.Bean with key failed: %v", err)
	}
	if v2.User != "bond" || v2.Age != 42 {
		t.Errorf("Bean(data) = %+v, want {bond 42}", v2)
	}
}

func TestAifeiBeanFormAndQuery(t *testing.T) {
	// Form body: string-typed sources bind with per-field coercion
	form := strings.NewReader("user=james&age=18")
	req := newFormRequest(t, form)
	in := server.NewIn(req)
	v, err := aifei.Bean[beanLoginReq](in)
	if err != nil {
		t.Fatalf("aifei.Bean(form) failed: %v", err)
	}
	if v.User != "james" || v.Age != 18 {
		t.Errorf("Bean(form) = %+v, want {james 18}", v)
	}

	// Query params with empty body
	req2 := newQueryRequest(t, "user=bond&age=42")
	v2, err := aifei.Bean[beanLoginReq](server.NewIn(req2))
	if err != nil {
		t.Fatalf("aifei.Bean(query) failed: %v", err)
	}
	if v2.User != "bond" || v2.Age != 42 {
		t.Errorf("Bean(query) = %+v, want {bond 42}", v2)
	}
}

func TestHttpContextBeanMethod(t *testing.T) {
	// The concrete-method form on *http.HttpContext, promoted to *server.In
	// through embedding (interface methods cannot take type parameters, so
	// this lives on the concrete type only).
	req := newJSONRequest(t, "POST", "/x", `{"user":"james","age":18}`)
	in := server.NewIn(req)

	v, err := in.Bean[beanLoginReq]()
	if err != nil {
		t.Fatalf("in.Bean failed: %v", err)
	}
	if v.User != "james" || v.Age != 18 {
		t.Errorf("in.Bean = %+v, want {james 18}", v)
	}

	// GetBean and Bean agree on the same request
	var w beanLoginReq
	if err := in.GetBean(&w); err != nil || w != v {
		t.Errorf("GetBean = (%+v, %v), want same as Bean %+v", w, err, v)
	}
}
