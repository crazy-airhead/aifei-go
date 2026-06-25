// Package util provides the simplest possible URL-based HTTP requests using
// the nami RPC framework. No setup required — just import and call.
//
//	body, err := util.Get("https://www.example.com")
//	result, err := util.Post("https://api.example.com/data", payload)
//
// Base URL support — set once, then use relative paths:
//
//	util.SetBaseURL("http://api.example.com")
//	body, err := util.Get("/users")               // GET http://api.example.com/users
//	result, _ := util.Post("/orders", order)       // POST http://api.example.com/orders
package util

import (
	"strings"
	"sync"

	"github.com/crazy-airhead/aifei-go/nami"
	httpchannel "github.com/crazy-airhead/aifei-go/nami/channel/http"
	jsoncoder "github.com/crazy-airhead/aifei-go/nami/coder/json"
)

var (
	defaultChannel nami.Channel
	defaultDecoder nami.Decoder
	defaultEncoder nami.Encoder

	// mu guards baseURL, upstreamFunc, and timeout, which are read on every
	// request and mutated by SetBaseURL / SetTimeout.
	mu           sync.RWMutex
	baseURL      string
	upstreamFunc nami.Upstream
	timeout      int // seconds
)

func init() {
	defaultChannel = httpchannel.New()
	defaultDecoder = jsoncoder.NewDecoder()
	defaultEncoder = jsoncoder.NewEncoder()
}

// SetBaseURL sets the base URL used to resolve relative paths via nami's
// Upstream mechanism. When set, any URL starting with "/" is treated as
// a path and resolved against the base URL. Pass an empty string to clear.
//
//	util.SetBaseURL("http://api.example.com")
//	body, _ := util.Get("/users") // GET http://api.example.com/users
func SetBaseURL(url string) {
	url = strings.TrimRight(url, "/")
	var upstream nami.Upstream
	if url != "" {
		upstream = nami.NewUpstreamFixed([]string{url})
	}
	mu.Lock()
	baseURL = url
	upstreamFunc = upstream
	mu.Unlock()
}

// BaseURL returns the currently configured base URL.
func BaseURL() string {
	mu.RLock()
	defer mu.RUnlock()
	return baseURL
}

// SetTimeout sets the per-request timeout (in seconds) applied to every request
// made through this package. A value of 0 (the default) applies no explicit
// timeout, so the underlying HTTP client's default (30s) is used.
//
//	util.SetTimeout(10)
func SetTimeout(seconds int) {
	mu.Lock()
	timeout = seconds
	mu.Unlock()
}

// Timeout returns the currently configured per-request timeout in seconds.
func Timeout() int {
	mu.RLock()
	defer mu.RUnlock()
	return timeout
}

// newNamiBy creates a fully configured Nami client and applies the URL.
// If u starts with "/" and a base URL is set, it uses nami's upstream + path
// mechanism. Otherwise it sets the URL directly.
func newNamiBy(u string) *nami.Nami {
	n := newNami()
	mu.RLock()
	upstream := upstreamFunc
	mu.RUnlock()
	if upstream != nil && strings.HasPrefix(u, "/") {
		n.Config().SetUpstream(upstream)
		n.Config().SetPath(u)
		return n
	}
	return n.URL(u)
}

// newNami creates a fully configured Nami client that bypasses the global
// registry entirely. Channel, decoder, and encoder are set directly on the
// config so that registry mutations by other code (e.g. tests) have no effect.
func newNami() *nami.Nami {
	cfg := nami.NewConfig()
	cfg.SetChannel(defaultChannel)
	cfg.SetDecoder(defaultDecoder)
	cfg.SetEncoder(defaultEncoder)
	mu.RLock()
	t := timeout
	mu.RUnlock()
	if t > 0 {
		cfg.SetTimeout(t)
	}
	return nami.NewWithConfig(cfg)
}

// Get performs a GET request and returns the response body as a string.
// Returns an error if the request fails or the HTTP status is not 2xx.
func Get(url string) (string, error) {
	result, err := newNamiBy(url).Action(nami.MethodGet).CallOrThrow(nil, nil, nil)
	if err != nil {
		return "", err
	}
	if err := result.AssertSuccess(); err != nil {
		return "", err
	}
	return result.BodyAsString(), nil
}

// GetResult performs a GET request and returns the raw *nami.Result.
// The caller is responsible for checking the HTTP status code.
func GetResult(url string) (*nami.Result, error) {
	return newNamiBy(url).Action(nami.MethodGet).CallOrThrow(nil, nil, nil)
}

// GetResultWith is like GetResult but with query params and headers.
func GetResultWith(url string, params, headers map[string]string) (*nami.Result, error) {
	return newNamiBy(url).Action(nami.MethodGet).CallOrThrow(headers, params, nil)
}

// GetJSON performs a GET request and returns the JSON response body unmarshalled
// into T. Returns an error if the request fails or the HTTP status is not 2xx.
//
//	resp, err := util.GetJSON[MyResponse](url)
//	users, err := util.GetJSON[[]User](url)
func GetJSON[T any](url string) (T, error) {
	var val T
	err := newNamiBy(url).Action(nami.MethodGet).CallAndBind(nil, nil, nil, &val)
	return val, err
}

// GetJSONWith performs a GET request with query params and headers, returning
// the JSON response body unmarshalled into T.
func GetJSONWith[T any](url string, params, headers map[string]string) (T, error) {
	var val T
	err := newNamiBy(url).Action(nami.MethodGet).CallAndBind(headers, params, nil, &val)
	return val, err
}

// Post performs a POST request with an optional JSON body and returns the
// response body as a string. Pass nil for body to send a POST with no body.
// Returns an error if the request fails or the HTTP status is not 2xx.
func Post(url string, body any) (string, error) {
	result, err := newNamiBy(url).Action(nami.MethodPost).CallOrThrow(nil, nil, body)
	if err != nil {
		return "", err
	}
	if err := result.AssertSuccess(); err != nil {
		return "", err
	}
	return result.BodyAsString(), nil
}

// PostResult is like Post but returns the raw *nami.Result without checking
// the HTTP status code. The caller is responsible for error handling.
func PostResult(url string, body any) (*nami.Result, error) {
	return newNamiBy(url).Action(nami.MethodPost).CallOrThrow(nil, nil, body)
}

// PostResultWith is like PostResult but with query params and headers.
func PostResultWith(url string, body any, params, headers map[string]string) (*nami.Result, error) {
	return newNamiBy(url).Action(nami.MethodPost).CallOrThrow(headers, params, body)
}

// PostJSON performs a POST request with body and returns the JSON response
// body unmarshalled into T.
func PostJSON[T any](url string, body any) (T, error) {
	var val T
	err := newNamiBy(url).Action(nami.MethodPost).CallAndBind(nil, nil, body, &val)
	return val, err
}

// PostJSONWith performs a POST request with body, query params, and headers,
// returning the JSON response body unmarshalled into T.
func PostJSONWith[T any](url string, body any, params, headers map[string]string) (T, error) {
	var val T
	err := newNamiBy(url).Action(nami.MethodPost).CallAndBind(headers, params, body, &val)
	return val, err
}

// GetWith performs a GET request with query params and headers, returning the
// response body as a string. Both params and headers may be nil.
func GetWith(url string, params, headers map[string]string) (string, error) {
	result, err := newNamiBy(url).Action(nami.MethodGet).CallOrThrow(headers, params, nil)
	if err != nil {
		return "", err
	}
	if err := result.AssertSuccess(); err != nil {
		return "", err
	}
	return result.BodyAsString(), nil
}

// GetBind performs a GET request with body and unmarshals the JSON response
// into val. val must be a non-nil pointer. Returns an error if the request
// fails or the HTTP status is not 2xx.
func GetBind(url string, body any, val any) error {
	return newNamiBy(url).Action(nami.MethodGet).CallAndBind(nil, nil, body, val)
}

// GetBindWith performs a GET request with body, query params, and headers,
// then unmarshals the JSON response into val. val must be a non-nil pointer.
func GetBindWith(url string, body any, params, headers map[string]string, val any) error {
	return newNamiBy(url).Action(nami.MethodGet).CallAndBind(headers, params, body, val)
}

// PostWith performs a POST request with body, query params, and headers.
// body, params, and headers may all be nil. Returns the raw *nami.Result.
func PostWith(url string, body any, params, headers map[string]string) (*nami.Result, error) {
	return newNamiBy(url).Action(nami.MethodPost).CallOrThrow(headers, params, body)
}

// PostBind performs a POST request with body and unmarshals the JSON response
// into val. val must be a non-nil pointer. Returns an error if the request
// fails or the HTTP status is not 2xx.
func PostBind(url string, body any, val any) error {
	return newNamiBy(url).Action(nami.MethodPost).CallAndBind(nil, nil, body, val)
}

// PostBindWith performs a POST request with body, query params, and headers,
// then unmarshals the JSON response into val. val must be a non-nil pointer.
func PostBindWith(url string, body any, params, headers map[string]string, val any) error {
	return newNamiBy(url).Action(nami.MethodPost).CallAndBind(headers, params, body, val)
}

// Request performs a request and returns the response body as a string.
func Request(method, url string, body any, params, headers map[string]string) (string, error) {
	result, err := newNamiBy(url).Action(method).CallOrThrow(headers, params, body)
	if err != nil {
		return "", err
	}
	if err := result.AssertSuccess(); err != nil {
		return "", err
	}
	return result.BodyAsString(), nil
}

// RequestResult performs a request with query params and returns the raw result.
func RequestResult(method, url string, body any, params, headers map[string]string) (*nami.Result, error) {
	return newNamiBy(url).Action(method).CallOrThrow(headers, params, body)
}

// RequestJSON performs a generic HTTP request with the given method, body,
// query params, and headers, returning the JSON response body unmarshalled
// into T.
func RequestJSON[T any](method, url string, body any, params, headers map[string]string) (T, error) {
	var val T
	err := newNamiBy(url).Action(method).CallAndBind(headers, params, body, &val)
	return val, err
}

// RequestBind performs a request with body, query params, and headers,
// then unmarshals the JSON response into val. val must be a non-nil pointer.
func RequestBind(method, url string, body any, params, headers map[string]string, val any) error {
	return newNamiBy(url).Action(method).CallAndBind(headers, params, body, val)
}
