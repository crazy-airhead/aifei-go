package dataisolate

import (
	"strings"

	"github.com/crazy-airhead/aifei-go/aifei"
)

// PrincipalResolver derives a Principal from a request. The plugin ships a built-in
// SubdomainHeaderResolver (tenant-only); applications provide their own JWT/session
// resolver to fill the full Principal. The resolver does NOT authenticate — request
// trust is the application/gateway's responsibility.
type PrincipalResolver interface {
	Resolve(in aifei.Input) *Principal
}

// SubdomainHeaderResolver extracts the tenant id from an explicit request header
// (default X-Tenant-ID), falling back to the first label of the Host (subdomain).
// It populates ONLY TenantID; for row/column isolation supply a full resolver.
type SubdomainHeaderResolver struct {
	// TenantHeader is the request header carrying the tenant id (default X-Tenant-ID).
	TenantHeader string
	// SubdomainFallback, when true, derives the tenant from the first DNS label of the
	// Host header when TenantHeader is absent (default true).
	SubdomainFallback bool
}

// NewSubdomainHeaderResolver builds a resolver with defaults.
func NewSubdomainHeaderResolver() *SubdomainHeaderResolver {
	return &SubdomainHeaderResolver{
		TenantHeader:      "X-Tenant-ID",
		SubdomainFallback: true,
	}
}

// Resolve implements PrincipalResolver.
func (r *SubdomainHeaderResolver) Resolve(in aifei.Input) *Principal {
	if r == nil {
		return nil
	}
	hdr := r.TenantHeader
	if hdr == "" {
		hdr = "X-Tenant-ID"
	}
	if tid := strings.TrimSpace(in.Header(hdr)); tid != "" {
		return &Principal{TenantID: tid}
	}
	if r.SubdomainFallback {
		if host := strings.TrimSpace(in.Header("Host")); host != "" {
			// strip port
			if i := strings.LastIndexByte(host, ':'); i >= 0 && strings.IndexByte(host[i:], '.') < 0 {
				host = host[:i]
			}
			if label := strings.ToLower(strings.SplitN(host, ".", 2)[0]); label != "" && !isIpLabel(label) {
				return &Principal{TenantID: label}
			}
		}
	}
	return nil
}

// isIpLabel reports whether label looks like the start of an IP (all digits), in which
// case it is not a tenant subdomain.
func isIpLabel(label string) bool {
	if label == "" {
		return false
	}
	for _, c := range label {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
