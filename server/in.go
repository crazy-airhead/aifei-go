package server

import (
	"net/http"

	"github.com/crazy-airhead/aifei-go"
	gohttp "github.com/crazy-airhead/aifei-go/go-http"
)

// In implements aifei.Input by embedding go-http.HttpContext.
//
// It reuses HttpContext's proven request-reading implementation instead of
// duplicating it, keeping server a thin convenience layer over go-http. All
// aifei.Input methods (Method, Path, GetStr, GetBean, Header, Cookie,
// Context, ...) and SetParams are promoted from the embedded HttpContext.
type In struct {
	*gohttp.HttpContext
}

// Compile-time guarantee that *In satisfies aifei.Input.
var _ aifei.Input = (*In)(nil)

// NewIn creates an In from an http.Request.
func NewIn(r *http.Request) *In {
	return &In{HttpContext: gohttp.NewInput(r)}
}
