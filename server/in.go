package server

import (
	"net/http"

	"github.com/crazy-airhead/aifei-go"
	gohttp "github.com/crazy-airhead/aifei-go/go-http"
)

// In implements aifei.Input by embedding go-http.HttpContext.
//
// It reuses HttpContext's proven request-reading implementation instead of
// duplicating it, keeping server a thin convenience layer over go-http. The
// core aifei.Input contract (Param: GetStr/GetBean/...; Meta: Path/Header/
// Context/Body), the HTTP-specific HttpContext methods (Method/RemoteIP/
// Cookie, via gohttp.HTTPMeta), and SetParams are all promoted from the
// embedded HttpContext.
type In struct {
	*gohttp.HttpContext
}

// Compile-time guarantee that *In satisfies aifei.Input.
var _ aifei.Input = (*In)(nil)

// NewIn creates an In from an http.Request.
func NewIn(r *http.Request) *In {
	return &In{HttpContext: gohttp.NewInput(r)}
}
