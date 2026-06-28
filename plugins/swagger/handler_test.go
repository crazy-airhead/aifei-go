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

// multiPathSpec is an OpenAPI doc with one admin and one app path, used to
// verify path-filter grouping. Its top-level tags mirror the operations' tags
// so tag-trimming can be checked too.
const multiPathSpec = `{
  "swagger": "2.0",
  "info": {"title": "T", "version": "1"},
  "tags": [{"name": "文件存储"}, {"name": "应用版本"}],
  "paths": {
    "/oa/admin-api/file/storage/get": {"get": {"tags": ["文件存储"]}},
    "/oa/app-api/version": {"get": {"tags": ["应用版本"]}}
  }
}`

func TestMultiGroupServicesJSON(t *testing.T) {
	cfg := &Config{
		Enabled:  true,
		BasePath: "/swagger",
		Groups: []Group{
			{Name: "AdminApi", Path: "admin", Filter: `^/oa/admin-api`},
			{Name: "AppApi", Path: "app", Filter: `^/oa/app-api`},
		},
	}
	h := buildHandler(cfg, func(...string) (string, error) { return multiPathSpec, nil })

	rr := do(t, h, "/services.json")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`"name":"AdminApi"`, `"name":"AppApi"`,
		`"/swagger/admin/swagger.json"`, `"/swagger/app/swagger.json"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("services.json missing %q: %s", want, body)
		}
	}
}

func TestMultiGroupSwaggerConfig(t *testing.T) {
	cfg := &Config{
		Enabled:  true,
		BasePath: "/swagger",
		Groups: []Group{
			{Name: "AdminApi", Path: "admin", Filter: `^/oa/admin-api`},
			{Name: "AppApi", Path: "app", Filter: `^/oa/app-api`},
		},
	}
	h := buildHandler(cfg, func(...string) (string, error) { return multiPathSpec, nil })

	rr := do(t, h, "/swagger/v3/api-docs/swagger-config")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	for _, want := range []string{`"configUrl":""`, `"name":"AdminApi"`, `"name":"AppApi"`} {
		if !strings.Contains(body, want) {
			t.Errorf("swagger-config missing %q: %s", want, body)
		}
	}
}

func TestMultiGroupFiltersByPath(t *testing.T) {
	cfg := &Config{
		Enabled:  true,
		BasePath: "/swagger",
		Groups: []Group{
			{Name: "AdminApi", Path: "admin", Filter: `^/oa/admin-api`},
			{Name: "AppApi", Path: "app", Filter: `^/oa/app-api`},
		},
	}
	h := buildHandler(cfg, func(...string) (string, error) { return multiPathSpec, nil })

	// Admin group keeps only the admin path + tag; drops the app ones.
	admin := do(t, h, "/swagger/admin/swagger.json").Body.String()
	if !strings.Contains(admin, "/oa/admin-api/file/storage/get") {
		t.Errorf("admin spec missing admin path: %s", admin)
	}
	if strings.Contains(admin, "/oa/app-api/version") {
		t.Errorf("admin spec leaked app path: %s", admin)
	}
	if strings.Contains(admin, `"应用版本"`) {
		t.Errorf("admin spec leaked app tag: %s", admin)
	}

	// App group keeps only the app path + tag; drops the admin ones.
	app := do(t, h, "/swagger/app/swagger.json").Body.String()
	if !strings.Contains(app, "/oa/app-api/version") {
		t.Errorf("app spec missing app path: %s", app)
	}
	if strings.Contains(app, "/oa/admin-api/file/storage/get") {
		t.Errorf("app spec leaked admin path: %s", app)
	}
	if strings.Contains(app, `"文件存储"`) {
		t.Errorf("app spec leaked admin tag: %s", app)
	}
}

func TestFilterNilServesVerbatim(t *testing.T) {
	// No filter (legacy single group) → bytes returned exactly as read.
	cfg := &Config{Enabled: true, BasePath: "/swagger", GroupName: "Legacy"}
	h := buildHandler(cfg, func(...string) (string, error) { return multiPathSpec, nil })
	if got := do(t, h, "/swagger/swagger.json").Body.String(); got != multiPathSpec {
		t.Errorf("legacy spec not verbatim:\ngot:  %s\nwant: %s", got, multiPathSpec)
	}
}
