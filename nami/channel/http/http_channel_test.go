package http

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/crazy-airhead/aifei-go/nami"
	jsoncoder "github.com/crazy-airhead/aifei-go/nami/coder/json"
)

// newClient builds a Nami client that uses the given channel directly on the
// config (bypassing the global registry), with JSON codec wired in.
func newClient(t *testing.T, ch nami.Channel) *nami.Nami {
	t.Helper()
	cfg := nami.NewConfig()
	cfg.SetChannel(ch)
	cfg.SetEncoder(jsoncoder.NewEncoder())
	cfg.SetDecoder(jsoncoder.NewDecoder())
	return nami.NewWithConfig(cfg)
}

func TestGetSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		w.Write([]byte("hello"))
	}))
	defer srv.Close()

	rst, err := newClient(t, New()).Action(nami.MethodGet).URL(srv.URL).CallOrThrow(nil, nil, nil)
	if err != nil {
		t.Fatalf("CallOrThrow: %v", err)
	}
	if err := rst.AssertSuccess(); err != nil {
		t.Fatalf("AssertSuccess: %v", err)
	}
	if rst.BodyAsString() != "hello" {
		t.Fatalf("body = %q, want hello", rst.BodyAsString())
	}
}

func TestPostJSONBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"key":"val"}` {
			t.Errorf("body = %q, want {\"key\":\"val\"}", body)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", r.Header.Get("Content-Type"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"new"}`))
	}))
	defer srv.Close()

	type payload struct {
		Key string `json:"key"`
	}
	type resp struct {
		ID string `json:"id"`
	}

	var out resp
	err := newClient(t, New()).Action(nami.MethodPost).URL(srv.URL).
		CallAndBind(nil, nil, payload{Key: "val"}, &out)
	if err != nil {
		t.Fatalf("CallAndBind: %v", err)
	}
	if out.ID != "new" {
		t.Fatalf("id = %q, want new", out.ID)
	}
}

func TestGetWithQueryAndHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") != "search" {
			t.Errorf("q = %q, want search", r.URL.Query().Get("q"))
		}
		if r.Header.Get("X-Token") != "s3cr3t" {
			t.Errorf("X-Token = %q, want s3cr3t", r.Header.Get("X-Token"))
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	rst, err := newClient(t, New()).Action(nami.MethodGet).URL(srv.URL).
		CallOrThrow(map[string]string{"X-Token": "s3cr3t"}, map[string]string{"q": "search"}, nil)
	if err != nil {
		t.Fatalf("CallOrThrow: %v", err)
	}
	if rst.BodyAsString() != "ok" {
		t.Fatalf("body = %q, want ok", rst.BodyAsString())
	}
}

func TestPostFormBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "name=x" {
			t.Errorf("form body = %q, want name=x", body)
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	// Force form encoding via Content-Type header.
	rst, err := newClient(t, New()).Action(nami.MethodPost).URL(srv.URL).
		CallOrThrow(map[string]string{nami.HeaderContentType: nami.FormURLEncodedValue},
			map[string]string{"name": "x"}, nil)
	if err != nil {
		t.Fatalf("CallOrThrow: %v", err)
	}
	if rst.Code() != http.StatusOK {
		t.Fatalf("status = %d", rst.Code())
	}
}

func TestNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("not found"))
	}))
	defer srv.Close()

	rst, err := newClient(t, New()).Action(nami.MethodGet).URL(srv.URL).CallOrThrow(nil, nil, nil)
	if err != nil {
		t.Fatalf("CallOrThrow: %v", err)
	}
	if err := rst.AssertSuccess(); err == nil {
		t.Fatal("expected error for 404")
	}
}

func TestNewWithClient(t *testing.T) {
	custom := &http.Client{}
	ch := NewWithClient(custom)
	if ch == nil {
		t.Fatal("channel is nil")
	}
	ch.SetClient(custom) // ensure SetClient does not panic
}

// The package init() registers http and https schemes.
func TestRegistersDefaultSchemes(t *testing.T) {
	if nami.GetChannel("http") == nil {
		t.Fatal("http channel not registered")
	}
	if nami.GetChannel("https") == nil {
		t.Fatal("https channel not registered")
	}
}
