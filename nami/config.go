package nami

// Config holds per-client configuration for Nami calls.
type Config struct {
	timeout   int // seconds
	heartbeat int // seconds

	encoder Encoder
	decoder Decoder
	channel Channel

	upstream Upstream
	url      string
	name     string
	path     string
	group    string

	filters []Filter
	headers map[string]string
}

// NewConfig creates a Config with defaults and initializes decoder/encoder.
func NewConfig() *Config {
	c := &Config{
		headers: make(map[string]string),
	}
	c.init()
	return c
}

func (c *Config) init() {
	if c.decoder == nil {
		if at := c.headers[HeaderAccept]; at != "" {
			c.decoder = GetDecoder(at)
		}
		if c.decoder == nil {
			c.decoder = GetDecoder(JSONValue)
		}
	}

	if c.encoder == nil {
		if ct := c.headers[HeaderContentType]; ct != "" {
			c.encoder = GetEncoder(ct)
		}
	}
}

// Timeout returns the request timeout in seconds.
func (c *Config) Timeout() int { return c.timeout }

// SetTimeout sets the request timeout in seconds.
func (c *Config) SetTimeout(t int) { c.timeout = t }

// Heartbeat returns the heartbeat interval in seconds.
func (c *Config) Heartbeat() int { return c.heartbeat }

// SetHeartbeat sets the heartbeat interval in seconds.
func (c *Config) SetHeartbeat(h int) { c.heartbeat = h }

// Encoder returns the configured encoder (may be nil).
func (c *Config) Encoder() Encoder { return c.encoder }

// SetEncoder sets the encoder.
func (c *Config) SetEncoder(e Encoder) {
	if e != nil {
		c.encoder = e
	}
}

// EncoderOrDefault returns the configured encoder, or the first registered encoder.
func (c *Config) EncoderOrDefault() Encoder {
	if c.encoder != nil {
		return c.encoder
	}
	return GetEncoderFirst()
}

// Decoder returns the configured decoder.
func (c *Config) Decoder() Decoder { return c.decoder }

// SetDecoder sets the decoder.
func (c *Config) SetDecoder(d Decoder) {
	if d != nil {
		c.decoder = d
	}
}

// Channel returns the configured channel.
func (c *Config) Channel() Channel { return c.channel }

// SetChannel sets the channel.
func (c *Config) SetChannel(ch Channel) { c.channel = ch }

// Upstream returns the service discovery upstream function.
func (c *Config) Upstream() Upstream { return c.upstream }

// SetUpstream sets the service discovery upstream function.
func (c *Config) SetUpstream(u Upstream) { c.upstream = u }

// URL returns the base URL.
func (c *Config) URL() string { return c.url }

// SetURL sets the base URL.
func (c *Config) SetURL(u string) { c.url = u }

// Name returns the service name (for service discovery).
func (c *Config) Name() string { return c.name }

// SetName sets the service name.
func (c *Config) SetName(n string) { c.name = n }

// Path returns the base path.
func (c *Config) Path() string { return c.path }

// SetPath sets the base path.
func (c *Config) SetPath(p string) { c.path = p }

// Group returns the service group (for service discovery).
func (c *Config) Group() string { return c.group }

// SetGroup sets the service group.
func (c *Config) SetGroup(g string) { c.group = g }

// Filters returns the configured filters.
func (c *Config) Filters() []Filter { return c.filters }

// FilterAdd adds a filter.
func (c *Config) FilterAdd(f Filter) { c.filters = append(c.filters, f) }

// Headers returns the configured headers (read-only).
func (c *Config) Headers() map[string]string {
	m := make(map[string]string, len(c.headers))
	for k, v := range c.headers {
		m[k] = v
	}
	return m
}

// HeaderSet sets a header.
func (c *Config) HeaderSet(name, val string) { c.headers[name] = val }

// HeaderGet gets a header value.
func (c *Config) HeaderGet(name string) string { return c.headers[name] }

// clone returns a deep copy of the Config so that mutating the copy (e.g.
// setting a different path) does not affect the original or other copies.
// The clone shares encoder, decoder, channel, and upstream references —
// those are meant to be long-lived and stateless.
func (c *Config) clone() *Config {
	cl := &Config{
		timeout:   c.timeout,
		heartbeat: c.heartbeat,
		encoder:   c.encoder,
		decoder:   c.decoder,
		channel:   c.channel,
		upstream:  c.upstream,
		url:       c.url,
		name:      c.name,
		path:      c.path,
		group:     c.group,
	}
	if len(c.filters) > 0 {
		cl.filters = make([]Filter, len(c.filters))
		copy(cl.filters, c.filters)
	}
	if len(c.headers) > 0 {
		cl.headers = make(map[string]string, len(c.headers))
		for k, v := range c.headers {
			cl.headers[k] = v
		}
	} else {
		cl.headers = make(map[string]string)
	}
	return cl
}
