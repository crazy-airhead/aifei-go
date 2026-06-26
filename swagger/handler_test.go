package swagger

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeSpec is a minimal OpenAPI 2.0 document for testing the spec endpoint.
const fakeSpec = `{"swagger":"2.0","info":{"title":"Test","version":"1.0.0"},"paths":{}}`

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	cfg := &Config{Enabled: true, BasePath: "/swagger", GroupName: "Test API"}
	return buildHandler(cfg, func(...string) (string, error) {
		return fakeSpec, nil
	})
}

func do(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestServicesJSON(t *testing.T) {
	h := newTestHandler(t)
	rr := do(t, h, "/services.json")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"name":"Test API"`) {
		t.Errorf("body missing group name: %s", body)
	}
	// services.json must point the spec URL at the configured base path.
	if !strings.Contains(body, `"/swagger/swagger.json"`) {
		t.Errorf("body missing spec url: %s", body)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type = %q, want application/json", ct)
	}
}

func TestSpec(t *testing.T) {
	h := newTestHandler(t)
	rr := do(t, h, "/swagger/swagger.json")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if rr.Body.String() != fakeSpec {
		t.Errorf("body = %s, want %s", rr.Body.String(), fakeSpec)
	}
}

func TestDocHTML(t *testing.T) {
	h := newTestHandler(t)
	rr := do(t, h, "/swagger/doc.html")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Errorf("body not html: %s", body)
	}
	// doc.html references its webjars assets via relative paths.
	if !strings.Contains(body, "webjars/js/") {
		t.Errorf("body missing webjars reference: %s", body)
	}
}

func TestWebjarsAsset(t *testing.T) {
	h := newTestHandler(t)
	rr := do(t, h, "/swagger/robots.txt")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if rr.Body.Len() == 0 {
		t.Error("robots.txt body empty")
	}
}

func TestBasePathRedirect(t *testing.T) {
	h := newTestHandler(t)
	for _, target := range []string{"/swagger", "/swagger/"} {
		rr := do(t, h, target)
		if rr.Code != http.StatusFound {
			t.Errorf("%s: status = %d, want %d", target, rr.Code, http.StatusFound)
		}
		if loc := rr.Header().Get("Location"); loc != "/swagger/doc.html" {
			t.Errorf("%s: location = %q, want /swagger/doc.html", target, loc)
		}
	}
}

func TestPluginHandlerMiddlewarePassthrough(t *testing.T) {
	p := &Plugin{
		cfg:     &Config{Enabled: true, BasePath: "/swagger", GroupName: "Test API"},
		handler: newTestHandler(t),
	}
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	mw := p.Handler()(next)

	// A non-swagger request falls through to the next handler.
	rr := do(t, mw, "/api/users")
	if !called {
		t.Error("expected next handler to be called for /api/users")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("passthrough status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestPluginHandlerMiddlewareIntercepts(t *testing.T) {
	p := &Plugin{
		cfg:     &Config{Enabled: true, BasePath: "/swagger", GroupName: "Test API"},
		handler: newTestHandler(t),
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called for swagger path")
	})
	mw := p.Handler()(next)

	for _, target := range []string{"/services.json", "/swagger/doc.html", "/swagger/swagger.json", "/swagger"} {
		rr := do(t, mw, target)
		body, _ := io.ReadAll(rr.Body)
		if rr.Code != http.StatusOK && rr.Code != http.StatusFound {
			t.Errorf("%s: status = %d, body=%s", target, rr.Code, body)
		}
	}
}
