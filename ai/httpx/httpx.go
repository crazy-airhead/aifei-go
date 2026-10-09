// Package httpx 是 ai 内部的 HTTP/SSE 客户端缝（决策 D2）：net/http 的薄封装，
// Doer 接口即测试注入缝。流式只设拨号级超时（总时长由调用侧 ctx 控制），
// 避免 http.Client.Timeout 覆盖整个请求生命周期而腰斩长流。
package httpx

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Doer 单请求执行缝（*http.Client 的最小面）；测试注入假实现。
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client 带默认头与可注入 Doer 的 HTTP 客户端。
type Client struct {
	doer    Doer
	headers map[string]string

	// 传输级配置（Doer 注入时忽略；选项顺序无关）
	timeout time.Duration
	proxy   string
}

// Option Client 构建选项。
type Option func(*Client)

// WithDoer 注入自定义 Doer（测试缝；否则按其余选项构建 *http.Client）。
func WithDoer(d Doer) Option { return func(c *Client) { c.doer = d } }

// WithTimeout 总超时（仅适合非流式调用；流式请用 ctx 控制时长）。
func WithTimeout(d time.Duration) Option { return func(c *Client) { c.timeout = d } }

// WithProxy HTTP(S) 代理地址。
func WithProxy(raw string) Option { return func(c *Client) { c.proxy = raw } }

// WithHeader 追加默认请求头。
func WithHeader(k, v string) Option {
	return func(c *Client) { c.headers[k] = v }
}

// WithUserAgent 默认 User-Agent。
func WithUserAgent(ua string) Option { return WithHeader("User-Agent", ua) }

func New(opts ...Option) *Client {
	c := &Client{headers: map[string]string{}}
	for _, opt := range opts {
		opt(c)
	}
	if c.doer == nil {
		c.doer = c.buildHTTPClient()
	}
	return c
}

func (c *Client) buildHTTPClient() *http.Client {
	cl := &http.Client{}
	if c.timeout > 0 {
		cl.Timeout = c.timeout
	}
	if c.proxy != "" {
		if u, err := url.Parse(c.proxy); err == nil {
			cl.Transport = &http.Transport{Proxy: http.ProxyURL(u)}
		}
	}
	return cl
}

// Doer 底层传输缝（请求级包装基座，如 Options.HTTPCustomize 的 per-request 定制）。
func (c *Client) Doer() Doer { return c.doer }

// HTTPError 非 2xx 响应；Body 全量读出供上层解析厂商 error JSON。
type HTTPError struct {
	Status int
	Body   []byte
}

func (e *HTTPError) Error() string {
	body := string(e.Body)
	if len(body) > 200 {
		body = body[:200] + "..."
	}
	return "ai: unexpected http status " + strconv.Itoa(e.Status) + ": " + body
}

func (c *Client) newRequest(ctx context.Context, method, rawURL string, body []byte, header map[string]string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	return req, nil
}

// Get 非流式 GET，读全文（无请求体）。非 2xx 返回 *HTTPError（Body 仍带出）。
func (c *Client) Get(ctx context.Context, rawURL string, header map[string]string) ([]byte, int, error) {
	req, err := c.newRequest(ctx, http.MethodGet, rawURL, nil, header)
	if err != nil {
		return nil, 0, err
	}
	resp, err := c.doer.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return data, resp.StatusCode, &HTTPError{Status: resp.StatusCode, Body: data}
	}
	return data, resp.StatusCode, nil
}

// PostJSON 非流式 POST JSON，读全文。非 2xx 返回 *HTTPError（Body 仍带出）。
func (c *Client) PostJSON(ctx context.Context, rawURL string, body []byte, header map[string]string) ([]byte, int, error) {
	req, err := c.newRequest(ctx, http.MethodPost, rawURL, body, header)
	if err != nil {
		return nil, 0, err
	}
	resp, err := c.doer.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return data, resp.StatusCode, &HTTPError{Status: resp.StatusCode, Body: data}
	}
	return data, resp.StatusCode, nil
}

// PostJSONStream 流式 POST JSON：SSE 帧逐事件回调（event 归一化小写，data 多行
// 已按 \n 拼接；[DONE] 作为普通 data 交付，方言自行识别）。非 2xx 读全文返回
// *HTTPError；onEvent 返回 error 立即中止并透传。
func (c *Client) PostJSONStream(ctx context.Context, rawURL string, body []byte, header map[string]string,
	onEvent func(event, data string) error) error {
	if header == nil {
		header = map[string]string{}
	}
	if _, ok := header["Accept"]; !ok {
		header["Accept"] = "text/event-stream"
	}
	req, err := c.newRequest(ctx, http.MethodPost, rawURL, body, header)
	if err != nil {
		return err
	}
	resp, err := c.doer.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return &HTTPError{Status: resp.StatusCode, Body: data}
	}
	return ParseSSE(ctx, resp.Body, onEvent)
}
