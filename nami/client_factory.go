package nami

// ClientFactory pre-configures shared settings for a specific upstream service
// and produces per-path Nami clients. A factory is bound to one service via
// ServiceName(); individual endpoints differ only by path.
//
// Typical usage:
//
//	factory := server.NewClientFactory("my-service", 15)
//
//	users  := factory.For("/api/v1/users")
//	orders := factory.For("/api/v1/orders")
//
// Each client returned by For is independent — calling For again does not
// affect previously returned clients.
type ClientFactory struct {
	config *Config
}

// NewClientFactory creates a ClientFactory with a fresh Config.
func NewClientFactory() *ClientFactory {
	return &ClientFactory{config: NewConfig()}
}

// ClientFactoryWithConfig creates a ClientFactory using an existing Config as
// the template for all produced clients.
func ClientFactoryWithConfig(config *Config) *ClientFactory {
	config.init()
	return &ClientFactory{config: config}
}

// Timeout sets the request timeout in seconds for all clients.
func (f *ClientFactory) Timeout(seconds int) *ClientFactory {
	f.config.SetTimeout(seconds)
	return f
}

// Heartbeat sets the heartbeat interval in seconds for all clients.
func (f *ClientFactory) Heartbeat(seconds int) *ClientFactory {
	f.config.SetHeartbeat(seconds)
	return f
}

// Upstream sets the service discovery upstream function.
func (f *ClientFactory) Upstream(u Upstream) *ClientFactory {
	f.config.SetUpstream(u)
	return f
}

// Name sets the service name (for service discovery).
func (f *ClientFactory) Name(name string) *ClientFactory {
	f.config.SetName(name)
	return f
}

// ServiceName is an alias for Name — sets the service name used for service
// discovery. Prefer ServiceName when the intent is specifically the target
// microservice identity.
func (f *ClientFactory) ServiceName(name string) *ClientFactory {
	f.config.SetName(name)
	return f
}

// URL sets the base URL for all clients.
func (f *ClientFactory) URL(u string) *ClientFactory {
	f.config.SetURL(u)
	return f
}

// Group sets the service group (for service discovery).
func (f *ClientFactory) Group(group string) *ClientFactory {
	f.config.SetGroup(group)
	return f
}

// FilterAdd adds a filter to all clients produced by this factory.
func (f *ClientFactory) FilterAdd(filter Filter) *ClientFactory {
	f.config.FilterAdd(filter)
	return f
}

// HeaderSet sets a default header for all clients.
func (f *ClientFactory) HeaderSet(name, val string) *ClientFactory {
	f.config.HeaderSet(name, val)
	return f
}

// Encoder sets the encoder for all clients.
func (f *ClientFactory) Encoder(e Encoder) *ClientFactory {
	f.config.SetEncoder(e)
	return f
}

// Decoder sets the decoder for all clients.
func (f *ClientFactory) Decoder(d Decoder) *ClientFactory {
	f.config.SetDecoder(d)
	return f
}

// Channel sets the channel for all clients.
func (f *ClientFactory) Channel(ch Channel) *ClientFactory {
	f.config.SetChannel(ch)
	return f
}

// For returns a pre-configured Nami client for the given path. The factory's
// service name, upstream, timeout, filters, and headers are deep-copied so
// each client is independent.
//
// The returned client can be further customized (e.g. Action, Context) before
// calling Call / CallOrThrow.
func (f *ClientFactory) For(path string) *Nami {
	clone := f.config.clone()
	clone.SetPath(path)
	return NewWithConfig(clone)
}

// Config returns the factory's underlying template Config (read-only — do not
// mutate; use the builder methods instead).
func (f *ClientFactory) Config() *Config {
	return f.config
}
