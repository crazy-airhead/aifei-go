package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crazy-airhead/aifei-go/aifei"
	"github.com/crazy-airhead/aifei-go/server"
)

// decodeBody unmarshals the recorder's JSON envelope.
func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	return m
}

func TestHandleJSON(t *testing.T) {
	h := server.NewIoHandler(nil)
	rec := httptest.NewRecorder()
	h.Handle(rec, nil, server.Of(map[string]interface{}{"k": "v"}))

	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type: %q", ct)
	}
	m := decodeBody(t, rec)
	if m["code"] != float64(server.CodeOK) {
		t.Fatalf("code: %v", m["code"])
	}
	d, _ := m["data"].(map[string]interface{})
	if d["k"] != "v" {
		t.Fatalf("data: %v", m["data"])
	}
}

func TestHandleNilAndPlainOutput(t *testing.T) {
	h := server.NewIoHandler(nil)

	rec := httptest.NewRecorder()
	h.Handle(rec, nil, nil)
	if decodeBody(t, rec)["code"] != float64(server.CodeOK) {
		t.Fatal("nil out should default to ok")
	}

	rec2 := httptest.NewRecorder()
	h.Handle(rec2, nil, aifei.NewResult(404, "Not Found", nil))
	m := decodeBody(t, rec2)
	if m["code"] != float64(404) || m["msg"] != "Not Found" {
		t.Fatalf("plain output wrap: %v", m)
	}
}

func TestHandleRedirect(t *testing.T) {
	h := server.NewIoHandler(nil)
	rec := httptest.NewRecorder()
	h.Handle(rec, nil, server.Redirect("/elsewhere", http.StatusMovedPermanently))

	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status: %d", rec.Code)
	}
	if rec.Header().Get("Location") != "/elsewhere" {
		t.Fatalf("location: %q", rec.Header().Get("Location"))
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("redirect should have no body, got %q", rec.Body.String())
	}
}

func TestHandleHeaders(t *testing.T) {
	hdr := (&server.Headers{}).
		SetHeader("X-Test", "yes").
		AddCookie(server.Cookie{Name: "c", Value: "v"})
	h := server.NewIoHandler(nil)
	rec := httptest.NewRecorder()
	h.Handle(rec, nil, server.Ok().SetHeaders(hdr))

	if rec.Header().Get("X-Test") != "yes" {
		t.Fatalf("X-Test: %q", rec.Header().Get("X-Test"))
	}
	found := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == "c" && c.Value == "v" {
			found = true
		}
	}
	if !found {
		t.Fatal("cookie c=v not set")
	}
}

func TestHandleRaw(t *testing.T) {
	h := server.NewIoHandler(nil)
	rec := httptest.NewRecorder()
	body := []byte{0x89, 0x50, 0x4e, 0x47}
	h.Handle(rec, nil, server.OfRaw("image/png", body))

	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("content-type: %q", ct)
	}
	if rec.Header().Get("Content-Length") != "4" {
		t.Fatalf("content-length: %q", rec.Header().Get("Content-Length"))
	}
	if rec.Body.Bytes()[0] != 0x89 {
		t.Fatalf("body: %v", rec.Body.Bytes())
	}
}

func TestHandleRawReaderSize(t *testing.T) {
	h := server.NewIoHandler(nil)
	rec := httptest.NewRecorder()
	h.Handle(rec, nil, server.OfRawReader("application/pdf", strings.NewReader("pdf-bytes")).SetRawSize(9))
	if rec.Header().Get("Content-Length") != "9" {
		t.Fatalf("content-length from SetRawSize: %q", rec.Header().Get("Content-Length"))
	}
}

func TestHandleFileData(t *testing.T) {
	h := server.NewIoHandler(nil)
	rec := httptest.NewRecorder()
	h.Handle(rec, nil, server.OfFile(func(s *server.FileSender) {
		s.SetData([]byte("excel-bytes"))
		s.SetSaveAsName("report.xlsx")
	}))

	if !strings.Contains(rec.Header().Get("Content-Type"), "spreadsheet") {
		t.Fatalf("content-type guess: %q", rec.Header().Get("Content-Type"))
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, "attachment;") || !strings.Contains(cd, "report.xlsx") {
		t.Fatalf("disposition: %q", cd)
	}
	if rec.Body.String() != "excel-bytes" {
		t.Fatalf("body: %q", rec.Body.String())
	}
}

func TestHandleFileDisk(t *testing.T) {
	dir := t.TempDir()
	content := []byte("from-disk")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	h := server.NewIoHandler(nil, server.WithDownloadBase(dir))
	rec := httptest.NewRecorder()
	h.Handle(rec, nil, server.OfFile(func(s *server.FileSender) { s.SetFileName("f.txt") }))

	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("content-type: %q", rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(rec.Header().Get("Content-Disposition"), "f.txt") {
		t.Fatalf("disposition: %q", rec.Header().Get("Content-Disposition"))
	}
	if rec.Body.String() != "from-disk" {
		t.Fatalf("body: %q", rec.Body.String())
	}
}

func TestHandleFileMissingSourceFallsBackToJSON(t *testing.T) {
	h := server.NewIoHandler(nil)
	rec := httptest.NewRecorder()
	h.Handle(rec, nil, server.OfFile(func(s *server.FileSender) { /* no source set */ }))

	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("expected JSON fallback, content-type: %q", rec.Header().Get("Content-Type"))
	}
	if m := decodeBody(t, rec); m["code"] != float64(server.CodeFail) {
		t.Fatalf("expected fail code, got %v", m)
	}
}

func TestHandleView(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "t.html"), []byte("hello #(msg)"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := server.NewIoHandler(nil, server.WithBaseTemplatePath(dir))
	rec := httptest.NewRecorder()
	h.Handle(rec, nil, server.Of("ignored").SetView("t.html").SetData(map[string]interface{}{}).Set("msg", "world"))

	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type: %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "hello") || !strings.Contains(rec.Body.String(), "world") {
		t.Fatalf("view body: %q", rec.Body.String())
	}
}

func TestServeHTTPForwardAndNotFound(t *testing.T) {
	app := aifei.New()
	app.Router().Handle("GET", "/json", func(in aifei.Input) aifei.Output { return server.Of("ok") })
	app.Router().Handle("GET", "/a", func(in aifei.Input) aifei.Output { return server.Forward("/b") })
	app.Router().Handle("GET", "/b", func(in aifei.Input) aifei.Output { return server.Of("done") })
	app.Router().Handle("GET", "/loop", func(in aifei.Input) aifei.Output { return server.Forward("/loop") })

	h := server.NewIoHandler(app)

	// Direct JSON.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/json", nil))
	if decodeBody(t, rec)["data"] != "ok" {
		t.Fatalf("json data: %v", rec.Body.String())
	}

	// Forward /a -> /b.
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest("GET", "/a", nil))
	if decodeBody(t, rec2)["data"] != "done" {
		t.Fatalf("forwarded body: %v", rec2.Body.String())
	}

	// Self-forward is rejected.
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, httptest.NewRequest("GET", "/loop", nil))
	if m := decodeBody(t, rec3); m["code"] != float64(server.CodeFail) {
		t.Fatalf("self-forward should fail, got %v", m)
	}

	// Not found.
	rec4 := httptest.NewRecorder()
	h.ServeHTTP(rec4, httptest.NewRequest("GET", "/nope", nil))
	if m := decodeBody(t, rec4); m["code"] != float64(-1) {
		t.Fatalf("not found code: %v", m)
	}
}
