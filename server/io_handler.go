package server

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/crazy-airhead/aifei-go/aifei"
	"github.com/crazy-airhead/aifei-go/enjoy"
	"github.com/crazy-airhead/aifei-go/log"
)

// ContentType values follow the Java aifei-vip-arch ContentType enum.
const (
	ContentTypeHTML        = "text/html; charset=utf-8"
	ContentTypeJSON        = "application/json; charset=utf-8"
	ContentTypeText        = "text/plain; charset=utf-8"
	ContentTypeEventStream = "text/event-stream; charset=utf-8"
	ContentTypeXML         = "text/xml; charset=utf-8"
	ContentTypeJavaScript  = "application/javascript; charset=utf-8"
	ContentTypeOctetStream = "application/octet-stream"
)

// maxForwards bounds the forward chain to guard against accidental loops.
const maxForwards = 8

// IoHandler is the HTTP adapter that connects net/http to aifei, porting Java
// aifei-vip-arch's IoHandler in full: it looks up routes, invokes handlers,
// follows forward chains, and dispatches the resulting Out according to its
// rendering intent (redirect → view → file → raw → JSON). Unlike http.
// HttpHandler (which builds requests as *http.HttpContext), IoHandler builds
// *In so that *In methods (GetFile, GetFiles) are reachable from services.
//
// Dispatch precedence in Handle (mirrors Java handleOutput):
//  1. redirect  — Location + status (no body)
//  2. headers   — always applied first; mode-specific Content-Type overrides
//  3. view      — enjoy template → HTML
//  4. file      — FileSender → attachment download/export
//  5. raw       — inline bytes with custom content type
//  6. json      — {code,msg,data} (default)
type IoHandler struct {
	app *aifei.Aifei

	engine           *enjoy.Engine // lazy view engine; see viewEngine()
	engineName       string        // engine name, default "FICUS"
	baseTemplatePath string        // joined onto view paths
	downloadBase     string        // root for FileSender disk downloads
	devMode          bool          // enjoy dev mode (disables template cache)
}

// IoOption configures an IoHandler.
type IoOption func(*IoHandler)

// WithViewEngine supplies a pre-built enjoy engine for view rendering.
func WithViewEngine(e *enjoy.Engine) IoOption {
	return func(h *IoHandler) { h.engine = e }
}

// WithEngineName sets the enjoy engine name (default "FICUS").
func WithEngineName(name string) IoOption {
	return func(h *IoHandler) { h.engineName = name }
}

// WithBaseTemplatePath sets the base directory joined onto view paths.
func WithBaseTemplatePath(p string) IoOption {
	return func(h *IoHandler) { h.baseTemplatePath = p }
}

// WithDownloadBase sets the root directory for FileSender disk downloads.
func WithDownloadBase(p string) IoOption {
	return func(h *IoHandler) { h.downloadBase = p }
}

// WithDevMode enables enjoy dev mode (templates reloaded on change).
func WithDevMode(b bool) IoOption {
	return func(h *IoHandler) { h.devMode = b }
}

// NewIoHandler creates an IoHandler that serves the given aifei app. Optional
// IoOptions configure view rendering, download paths, and dev mode.
// The returned *IoHandler implements http.Handler.
func NewIoHandler(app *aifei.Aifei, opts ...IoOption) *IoHandler {
	h := &IoHandler{app: app, engineName: "FICUS"}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// ServeHTTP implements http.Handler. It builds an *In from the request, looks
// up the route, invokes handlers (including global middleware), follows any
// forward chain, and dispatches the result via Handle.
func (h *IoHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	in := NewIn(r)
	path := r.URL.Path

	out, found := h.invoke(in, path)
	if !found {
		h.Handle(w, in, aifei.NewResult(-1, "Not Found", nil))
		return
	}

	// Follow the forward chain (bounded), mirroring Java IoHandler.handle.
	for depth := 0; depth < maxForwards; depth++ {
		o, ok := out.(*Out)
		if !ok || o.ForwardPath() == "" {
			break
		}
		target := o.ForwardPath()
		if target == path {
			h.Handle(w, in, Fail("forward target equals current path: %s", target))
			return
		}
		next, ok := h.invoke(in, target)
		if !ok {
			h.Handle(w, in, Fail("forward target not found: %s", target))
			return
		}
		path = target
		out = next
	}

	h.Handle(w, in, out)
}

// invoke looks up path on the request's method, sets its route params on in, and
// runs the route handlers wrapped in the app's global handler chain.
func (h *IoHandler) invoke(in *In, path string) (aifei.Output, bool) {
	handlers, params, found := h.app.Router().Lookup(in.Method(), path)
	if !found {
		return nil, false
	}
	in.SetParams(params)

	final := func(in aifei.Input) aifei.Output {
		var out aifei.Output
		for _, handler := range handlers {
			out = handler(in)
		}
		return out
	}

	wrapped := final
	for i := len(h.app.Handlers()) - 1; i >= 0; i-- {
		wrapped = h.app.Handlers()[i](wrapped)
	}

	return wrapped(in), true
}

// viewEngine lazily creates the enjoy engine on first view render.
func (h *IoHandler) viewEngine() *enjoy.Engine {
	if h.engine == nil {
		name := h.engineName
		if name == "" {
			name = "FICUS"
		}
		e := enjoy.NewEngine(name)
		if h.baseTemplatePath != "" {
			e.SetBaseTemplatePath(h.baseTemplatePath)
		}
		e.SetDevMode(h.devMode)
		h.engine = e
	}
	return h.engine
}

// Handle writes out to w according to its rendering intent. It is the Go port
// of Java IoHandler.handleOutput. ServeHTTP calls Handle after route lookup and
// invocation; Handle itself is public so it can be unit-tested with httptest.
func (h *IoHandler) Handle(w http.ResponseWriter, in aifei.Input, out aifei.Output) {
	o := normalizeOut(out)

	// Business headers/cookies first; a mode's Content-Type overrides them.
	o.HeadersOut().apply(w)

	// 1. Redirect.
	if url := o.RedirectURL(); url != "" {
		status := o.RedirectStatus()
		if status == 0 {
			status = http.StatusFound
		}
		w.Header().Set("Location", url)
		w.WriteHeader(status)
		return
	}

	// 2. View (enjoy → HTML).
	if view := o.View(); view != "" {
		w.Header().Set("Content-Type", ContentTypeHTML)
		path := view
		if h.baseTemplatePath != "" {
			path = filepath.Join(h.baseTemplatePath, view)
		}
		data, _ := o.Data().(map[string]interface{})
		if err := h.viewEngine().GetTemplate(path).Render(data, w); err != nil {
			log.Default().Warn("io: view render %q: %v", view, err)
		}
		return
	}

	// 3. File download / export.
	if fn := o.FileSenderOut(); fn != nil {
		wt := &writeTracker{ResponseWriter: w}
		s := &FileSender{}
		fn(s)
		if err := s.send(wt, h.downloadBase); err != nil {
			if !wt.wroteHeader {
				writeJSON(w, Fail("io: file: %s", err))
			} else {
				log.Default().Warn("io: file send: %v", err)
			}
		}
		return
	}

	// 4. Raw inline bytes.
	if body := o.RawBody(); body != nil {
		ct := o.RawContentType()
		if ct == "" {
			ct = ContentTypeOctetStream
		}
		hdr := w.Header()
		hdr.Set("Content-Type", ct)
		if size := o.RawSize(); size > 0 {
			hdr.Set("Content-Length", strconv.FormatInt(size, 10))
		}
		if closer, ok := body.(io.Closer); ok {
			defer closer.Close()
		}
		if _, err := io.Copy(w, body); err != nil {
			log.Default().Warn("io: raw write: %v", err)
		}
		return
	}

	// 5. JSON (default).
	writeJSON(w, o)
}

// writeJSON serializes an Output as {"code","msg","data"}, matching http's
// wire format. The HTTP status is derived from the business code: negative codes
// → 404, client-error codes (4xx) pass through, server-error codes (≥500) →
// 500, success codes (0–399) → 200.
func writeJSON(w http.ResponseWriter, out aifei.Output) {
	if out == nil {
		out = aifei.NewResult(CodeOK, "ok", nil)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	status := httpStatus(out.Code())
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"code": out.Code(),
		"msg":  out.Msg(),
		"data": out.Data(),
	})
}

// httpStatus maps a business code to a meaningful HTTP status:
//
//	< 0        → 404 (Not Found, e.g. route-miss)
//	400 – 499  → the code itself (client error)
//	≥ 500      → 500 (server error, unified to avoid leaking internals)
//	otherwise  → 200 (success)
func httpStatus(code int) int {
	switch {
	case code < 0:
		return http.StatusNotFound
	case code >= 500:
		return http.StatusInternalServerError
	case code >= 400:
		return code
	default:
		return http.StatusOK
	}
}

// normalizeOut coerces an aifei.Output into a *Out. nil becomes a success Out;
// a plain (non-*Out) Output is wrapped so its code/msg/data still render as JSON.
func normalizeOut(out aifei.Output) *Out {
	if out == nil {
		return Ok()
	}
	if o, ok := out.(*Out); ok {
		return o
	}
	return &Out{code: out.Code(), msg: out.Msg(), data: out.Data()}
}

// writeTracker wraps http.ResponseWriter to record whether the response header
// has been written, so Handle can decide between a JSON fallback and logging on
// a file-send error.
type writeTracker struct {
	http.ResponseWriter
	wroteHeader bool
}

func (t *writeTracker) WriteHeader(status int) {
	t.wroteHeader = true
	t.ResponseWriter.WriteHeader(status)
}

func (t *writeTracker) Write(b []byte) (int, error) {
	if !t.wroteHeader {
		t.wroteHeader = true
	}
	return t.ResponseWriter.Write(b)
}
