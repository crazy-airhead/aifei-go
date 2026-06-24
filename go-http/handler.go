package gohttp

import (
	"encoding/json"
	"net/http"

	"github.com/crazy-airhead/aifei-go/aifei"
)

// HttpHandler wraps an aifei.Aifei instance as an http.Handler.
type HttpHandler struct {
	App *aifei.Aifei
}

// NewHttpHandler creates an HttpHandler that bridges net/http to aifei.
func NewHttpHandler(app *aifei.Aifei) *HttpHandler {
	return &HttpHandler{App: app}
}

// ServeHTTP implements http.Handler.
func (h *HttpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	in := NewInput(r)

	handlers, params, found := h.App.Router().Lookup(r.Method, r.URL.Path)
	if !found {
		writeJSON(w, aifei.NewResult(-1, "Not Found", nil))
		return
	}

	in.SetParams(params)

	// Build the innermost handler: call all route handlers in sequence
	final := func(in aifei.Input) aifei.Output {
		var out aifei.Output
		for _, handler := range handlers {
			out = handler(in)
		}
		return out
	}

	// Apply global handlers (outermost applied first)
	wrapped := final
	for i := len(h.App.Handlers()) - 1; i >= 0; i-- {
		wrapped = h.App.Handlers()[i](wrapped)
	}

	out := wrapped(in)
	writeJSON(w, out)
}

// writeJSON serializes an Output to the HTTP response.
// HTTP status is always 200; business semantics are in the JSON code field.
func writeJSON(w http.ResponseWriter, out aifei.Output) {
	if out == nil {
		out = aifei.NewResult(0, "ok", nil)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(200)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"code": out.Code(),
		"msg":  out.Msg(),
		"data": out.Data(),
	})
}
