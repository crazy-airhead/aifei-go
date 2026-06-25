package nami

import "sync"

// UpstreamFixed returns servers in round-robin order (a single server is always
// returned). It is safe for concurrent use.
type UpstreamFixed struct {
	servers []string
	mu      sync.Mutex
	index   int
}

// NewUpstreamFixed creates a fixed upstream from a list of server URLs.
// A single server is wrapped as a stateless closure (no locking needed);
// two or more servers are served round-robin via Get.
func NewUpstreamFixed(servers []string) Upstream {
	uf := &UpstreamFixed{
		servers: servers,
	}
	if len(servers) == 1 {
		return func() string { return servers[0] }
	}
	return uf.Get
}

// Get returns the next server URL in round-robin order.
func (uf *UpstreamFixed) Get() string {
	uf.mu.Lock()
	defer uf.mu.Unlock()

	if len(uf.servers) == 0 {
		return ""
	}
	s := uf.servers[uf.index]
	uf.index = (uf.index + 1) % len(uf.servers)
	return s
}
