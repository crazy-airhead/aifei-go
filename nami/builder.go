package nami

// Builder provides a builder pattern for creating pre-configured Nami clients.
type Builder struct {
	config *Config
}

// NewBuilder creates a new Builder with a fresh Config.
func NewBuilder() *Builder {
	return &Builder{
		config: NewConfig(),
	}
}

// BuilderWithConfig creates a Builder using an existing Config.
func BuilderWithConfig(config *Config) *Builder {
	return &Builder{config: config}
}

// Timeout sets the request timeout in seconds.
func (b *Builder) Timeout(seconds int) *Builder {
	b.config.SetTimeout(seconds)
	return b
}

// Heartbeat sets the heartbeat interval in seconds.
func (b *Builder) Heartbeat(seconds int) *Builder {
	b.config.SetHeartbeat(seconds)
	return b
}

// Encoder sets the encoder.
func (b *Builder) Encoder(e Encoder) *Builder {
	b.config.SetEncoder(e)
	return b
}

// Decoder sets the decoder.
func (b *Builder) Decoder(d Decoder) *Builder {
	b.config.SetDecoder(d)
	return b
}

// Channel sets the channel.
func (b *Builder) Channel(ch Channel) *Builder {
	b.config.SetChannel(ch)
	return b
}

// Upstream sets the service discovery upstream function.
func (b *Builder) Upstream(u Upstream) *Builder {
	b.config.SetUpstream(u)
	return b
}

// URL sets the base URL.
func (b *Builder) URL(u string) *Builder {
	b.config.SetURL(u)
	return b
}

// Name sets the service name (for service discovery).
func (b *Builder) Name(name string) *Builder {
	b.config.SetName(name)
	return b
}

// Path sets the base path.
func (b *Builder) Path(path string) *Builder {
	b.config.SetPath(path)
	return b
}

// Group sets the service group (for service discovery).
func (b *Builder) Group(group string) *Builder {
	b.config.SetGroup(group)
	return b
}

// FilterAdd adds a filter.
func (b *Builder) FilterAdd(f Filter) *Builder {
	b.config.FilterAdd(f)
	return b
}

// HeaderSet sets a header.
func (b *Builder) HeaderSet(name, val string) *Builder {
	b.config.HeaderSet(name, val)
	return b
}

// Build creates a pre-configured Nami client.
func (b *Builder) Build() *Nami {
	return NewWithConfig(b.config)
}

// Config returns the underlying Config.
func (b *Builder) Config() *Config {
	return b.config
}
