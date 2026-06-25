package nami

import (
	"context"
	"fmt"
	"reflect"
	"strings"
)

// Nami is the main RPC client, providing a fluent API for making calls.
type Nami struct {
	url    string
	action string
	target any
	method reflect.Method
	config *Config
	result *Result
	ctx    context.Context
}

// New creates a Nami client with a fresh Config.
func New() *Nami {
	return &Nami{
		config: NewConfig(),
		action: MethodPost,
	}
}

// NewWithConfig creates a Nami client with the given Config.
func NewWithConfig(config *Config) *Nami {
	config.init()
	return &Nami{
		config: config,
		action: MethodPost,
	}
}

// Action sets the HTTP method (GET, POST, etc.).
func (n *Nami) Action(action string) *Nami {
	if action != "" {
		n.action = action
	}
	return n
}

// Context sets the caller context, made available to filters via the invocation.
func (n *Nami) Context(ctx context.Context) *Nami {
	n.ctx = ctx
	return n
}

// URL sets the request URL.
func (n *Nami) URL(u string) *Nami {
	n.url = u
	return n
}

// URLWithPath sets the base URL and path, joining them appropriately.
func (n *Nami) URLWithPath(baseURL, path string) *Nami {
	if strings.Contains(baseURL, "{fun}") {
		n.url = strings.ReplaceAll(baseURL, "{fun}", path)
	} else if path == "" {
		n.url = baseURL
	} else {
		n.url = joinURI(baseURL, path)
	}
	return n
}

// Call executes the RPC call with headers, args, and optional body.
func (n *Nami) Call(headers, args map[string]string, body any) *Nami {
	n.result, _ = n.CallOrThrow(headers, args, body)
	return n
}

// CallOrThrow executes the RPC call and returns any error.
func (n *Nami) CallOrThrow(headers, args map[string]string, body any) (*Result, error) {
	// Build args as map[string]any
	argsAny := make(map[string]any, len(args))
	for k, v := range args {
		argsAny[k] = v
	}

	// Resolve URL from upstream if needed
	callURL := n.url
	if callURL == "" && n.config.Upstream() != nil {
		baseURL := n.config.Upstream()()
		if baseURL == "" {
			return nil, fmt.Errorf("nami: upstream not found server instance: %s", n.config.Name())
		}
		if !strings.Contains(baseURL, "://") {
			baseURL = "http://" + baseURL
		}
		path := n.config.Path()
		callURL = joinURI(baseURL, path)
	}

	inv := NewInvocation(n.config, n.target, n.method, n.action, callURL, body,
		FilterFunc(func(inv *Invocation) (*Result, error) {
			return n.callDo(inv)
		}))
	inv.Ctx = n.ctx

	if headers != nil {
		for k, v := range headers {
			inv.Headers[k] = v
		}
	}
	if args != nil {
		for k, v := range argsAny {
			inv.Args[k] = v
		}
	}

	return inv.Invoke()
}

func (n *Nami) callDo(inv *Invocation) (*Result, error) {
	channel := n.config.Channel()
	if channel == nil {
		// Resolve channel by scheme from URL
		if idx := strings.Index(inv.URL, "://"); idx > 0 {
			scheme := inv.URL[:idx]
			channel = GetChannel(scheme)
		}
	}
	if channel == nil {
		return nil, fmt.Errorf("nami: no channel available for request: %s", inv.URL)
	}
	return channel.Call(&inv.Context)
}

// Result returns the raw Result of the last Call.
func (n *Nami) Result() *Result {
	return n.result
}

// GetString returns the result body as a string.
func (n *Nami) GetString() (string, error) {
	if n.result == nil {
		return "", nil
	}
	if err := n.result.AssertSuccess(); err != nil {
		return "", err
	}
	return n.result.BodyAsString(), nil
}

// CallAndBind executes the RPC call and deserializes the result body as JSON
// into val (must be a non-nil pointer). Returns an error if the call fails or
// the HTTP status is non-2xx.
func (n *Nami) CallAndBind(headers, args map[string]string, body any, val any) error {
	result, err := n.CallOrThrow(headers, args, body)
	if err != nil {
		return err
	}
	n.result = result
	return result.Bind(val)
}

// CallAndGetBody executes the RPC call and returns the result body parsed as
// JSON (map[string]any for objects, []any for arrays, or primitives).
// Returns an error if the call fails or the HTTP status is non-2xx.
func (n *Nami) CallAndGetBody(headers, args map[string]string, body any) (any, error) {
	result, err := n.CallOrThrow(headers, args, body)
	if err != nil {
		return nil, err
	}
	n.result = result
	return result.AsAny()
}

// GetObject deserializes the result into the given value.
// val must be a non-nil pointer.
func (n *Nami) GetObject(val any) error {
	if n.result == nil {
		return nil
	}
	if err := n.result.AssertSuccess(); err != nil {
		return err
	}

	rv := reflect.ValueOf(val)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("nami: GetObject requires a non-nil pointer")
	}

	decoder := n.config.Decoder()
	if decoder == nil {
		decoder = GetDecoder(JSONValue)
	}

	result, err := decoder.Decode(n.result, rv.Type().Elem())
	if err != nil {
		return err
	}

	rv.Elem().Set(reflect.ValueOf(result))
	return nil
}

// Config returns the underlying Config.
func (n *Nami) Config() *Config {
	return n.config
}

// joinURI joins a base URL with a path segment.
func joinURI(base, path string) string {
	base = strings.TrimRight(base, "/")
	path = strings.TrimLeft(path, "/")
	// Extract query string from base
	baseURL, queryStr := base, ""
	if idx := strings.Index(base, "?"); idx > 0 {
		baseURL = base[:idx]
		queryStr = base[idx:]
	}
	// Handle sd: prefix for service discovery
	if strings.HasPrefix(baseURL, "sd:") {
		baseURL = baseURL[3:]
	}
	// Find path start after scheme://host:port
	schemeEnd := strings.Index(baseURL, "://")
	if schemeEnd > 0 {
		pathStart := strings.Index(baseURL[schemeEnd+3:], "/")
		if pathStart > 0 {
			baseURL = baseURL[:schemeEnd+3+pathStart]
		}
	}
	result := baseURL + "/" + path
	// Remove double slashes (except after scheme://)
	result = strings.ReplaceAll(result, "///", "/")
	if schemeEnd > 0 {
		result = result[:schemeEnd+3] + strings.ReplaceAll(result[schemeEnd+3:], "//", "/")
	}
	if queryStr != "" {
		result += queryStr
	}
	return result
}
