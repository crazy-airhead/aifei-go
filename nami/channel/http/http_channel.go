// Package http provides the HTTP channel implementation for Nami RPC calls.
package http

import (
	"fmt"
	"io"
	"net/http"
	nurl "net/url"
	"strings"
	"time"

	"github.com/crazy-airhead/aifei-go/nami"
)

// HttpChannel is the HTTP transport channel for Nami RPC calls.
// It implements nami.Channel using net/http.
type HttpChannel struct {
	nami.ChannelBase
	client *http.Client
}

// New creates a new HttpChannel with the default HTTP client (30s timeout).
func New() *HttpChannel {
	return &HttpChannel{
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// NewWithClient creates a new HttpChannel with a custom HTTP client.
func NewWithClient(client *http.Client) *HttpChannel {
	return &HttpChannel{client: client}
}

// SetClient sets the HTTP client used by this channel.
func (hc *HttpChannel) SetClient(client *http.Client) {
	hc.client = client
}

func init() {
	ch := New()
	nami.RegChannel("http", ch)
	nami.RegChannel("https", ch)
}

// Call executes the RPC call over HTTP and returns the Result.
func (hc *HttpChannel) Call(ctx *nami.Context) (*nami.Result, error) {
	hc.Pretreatment(ctx)

	isGet := nami.MethodGet == ctx.Action
	callURL := ctx.URL

	// Build query string for GET or when body+args coexist
	if (isGet && len(ctx.Args) > 0) || (ctx.Body != nil && len(ctx.Args) > 0) {
		callURL = appendQueryString(callURL, ctx.Args)
	}

	if ctx.Config.Decoder() == nil {
		return nil, fmt.Errorf("nami http channel: no matching decoder")
	}

	// Let decoder pretreatment set headers (e.g. Accept)
	ctx.Config.Decoder().Pretreatment(ctx)

	var httpReq *http.Request
	var err error

	if isGet {
		httpReq, err = http.NewRequest(nami.MethodGet, callURL, nil)
		if err != nil {
			return nil, fmt.Errorf("nami http channel: %w", err)
		}
	} else {
		contentType := ""
		if ct, ok := ctx.Headers[nami.HeaderContentType]; ok {
			contentType = ct
		}

		if strings.HasPrefix(contentType, nami.FormDataValue) ||
			strings.HasPrefix(contentType, nami.FormURLEncodedValue) {
			httpReq, err = hc.buildFormRequest(ctx, callURL)
		} else {
			httpReq, err = hc.buildBodyRequest(ctx, callURL, contentType)
		}
	}

	if err != nil {
		return nil, err
	}

	// Apply headers
	for k, v := range ctx.Headers {
		httpReq.Header.Set(k, v)
	}

	// Apply timeout from config
	client := hc.client
	if ctx.Config.Timeout() > 0 {
		client = &http.Client{Timeout: time.Duration(ctx.Config.Timeout()) * time.Second}
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("nami http channel: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("nami http channel: %w", err)
	}

	result := nami.NewResult(resp.StatusCode, body)

	// Copy response headers
	for k, vals := range resp.Header {
		for _, v := range vals {
			result.HeaderAdd(k, v)
		}
	}

	// Detect charset from Content-Type
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		if idx := strings.Index(ct, "charset="); idx > 0 {
			result.SetCharset(strings.TrimSpace(ct[idx+8:]))
		}
	}

	return result, nil
}

func (hc *HttpChannel) buildBodyRequest(ctx *nami.Context, callURL, contentType string) (*http.Request, error) {
	encoder := ctx.Config.Encoder()
	if encoder == nil && contentType != "" {
		encoder = nami.GetEncoder(contentType)
	}

	// If body or encoder exists, use body mode
	if ctx.Body != nil || encoder != nil {
		if encoder == nil {
			encoder = ctx.Config.EncoderOrDefault()
		}
		if encoder == nil {
			return nil, fmt.Errorf("nami http channel: missing suitable encoder")
		}
		if encoder.BodyRequired() && ctx.Body == nil {
			return nil, fmt.Errorf("nami http channel: encoder requires a body")
		}

		bodyBytes, err := encoder.Encode(ctx.BodyOrArgs())
		if err != nil {
			return nil, fmt.Errorf("nami http channel encode: %w", err)
		}

		req, err := http.NewRequest(ctx.Action, callURL, strings.NewReader(string(bodyBytes)))
		if err != nil {
			return nil, err
		}
		req.Header.Set(nami.HeaderContentType, encoder.Enctype())
		return req, nil
	}

	// Fallback to form request
	return hc.buildFormRequest(ctx, callURL)
}

func (hc *HttpChannel) buildFormRequest(ctx *nami.Context, callURL string) (*http.Request, error) {
	form := nurl.Values{}
	for k, v := range ctx.Args {
		form.Set(k, fmt.Sprintf("%v", v))
	}

	body := form.Encode()
	req, err := http.NewRequest(ctx.Action, callURL, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set(nami.HeaderContentType, nami.FormURLEncodedValue)
	return req, nil
}

func appendQueryString(baseURL string, args map[string]any) string {
	if len(args) == 0 {
		return baseURL
	}

	u, err := nurl.Parse(baseURL)
	if err != nil {
		return baseURL
	}

	q := u.Query()
	for k, v := range args {
		q.Set(k, fmt.Sprintf("%v", v))
	}
	u.RawQuery = q.Encode()
	return u.String()
}
