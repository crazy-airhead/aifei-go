package nami

// Discovery is the interface for service discovery providers.
// Implementations resolve a logical service name to a concrete server URL.
type Discovery interface {
	// GetServer returns a URL for the given service group and name.
	GetServer(group, name string) (string, error)
}

// NewDiscoveryUpstream creates an Upstream backed by a Discovery instance.
func NewDiscoveryUpstream(d Discovery, group, name string) Upstream {
	return func() string {
		server, err := d.GetServer(group, name)
		if err != nil {
			return ""
		}
		return server
	}
}
