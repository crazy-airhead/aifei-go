// transport_sse.go 旧式 SSE 客户端传输（2024-11-05 协议，Java WebFluxSseClient
// TransportProvider）：GET 流收全部服务端帧，首帧 endpoint 事件告知报文端点；
// 消息经 POST 该端点发送（会话以查询参数携带）。传输终结 = GET 流断开，客户端
// 门面自愈重连。
package mcp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// sseTransport 旧式 SSE 传输（postURL 在 endpoint 事件后落位）。cancel 绑定
// GET 请求——Close 时取消请求即断开连接（旧协议的会话生命周期 = GET 流生命
// 周期，服务端以流断开注销会话）。
type sseTransport struct {
	frames  *frameChan
	postURL string
	client  *Client
	cancel  context.CancelFunc

	mu sync.Mutex
}

func newSSETransport(ctx context.Context, baseURL string, c *Client) (Transport, error) {
	t := &sseTransport{frames: newFrameChan(), client: c}

	ctx, t.cancel = context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL, nil)
	if err != nil {
		t.cancel()
		return nil, &TransportError{Err: err}
	}
	req.Header.Set("Accept", "text/event-stream")
	for k, vs := range c.extraHeaders {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	hc := httpDefaultClient
	if c.httpClient != nil {
		hc = c.httpClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		t.cancel()
		return nil, &TransportError{Err: err}
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.cancel()
		return nil, &TransportError{Err: fmt.Errorf("sse connect: unexpected status %s", resp.Status)}
	}

	endpoint := make(chan string, 1)
	failed := make(chan error, 1)
	go func() {
		defer resp.Body.Close()
		err := scanSSE(resp.Body, func(event, data string) error {
			if event == "endpoint" {
				select {
				case endpoint <- data:
				default:
				}
				return nil
			}
			msg, err := UnmarshalMessage([]byte(data))
			if err != nil {
				return err
			}
			t.frames.deliver(msg)
			return nil
		})
		if err != nil {
			failed <- err
		}
		t.frames.terminate() // GET 流断开 = 会话终结
	}()

	select {
	case u := <-endpoint:
		t.postURL = resolveEndpoint(baseURL, u)
		return t, nil
	case err := <-failed:
		return nil, &TransportError{Err: err}
	case <-ctx.Done():
		return nil, &TransportError{Err: ctx.Err()}
	}
}

// resolveEndpoint endpoint 事件数据绝对化（相对路径按 baseURL 的 scheme://host
// 拼接）。
func resolveEndpoint(baseURL, endpoint string) string {
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		return endpoint
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return endpoint
	}
	path := endpoint
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return u.Scheme + "://" + u.Host + path
}

func (t *sseTransport) Receive() <-chan Message { return t.frames.recv }

// Send 消息 POST 到报文端点（202 确认）。
func (t *sseTransport) Send(ctx context.Context, msg Message) error {
	t.mu.Lock()
	postURL := t.postURL
	t.mu.Unlock()
	if postURL == "" {
		return &TransportError{Err: fmt.Errorf("sse transport: message endpoint not ready")}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, bytes.NewReader(mustMarshal(msg)))
	if err != nil {
		return &TransportError{Err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for k, vs := range t.client.extraHeaders {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	hc := httpDefaultClient
	if t.client.httpClient != nil {
		hc = t.client.httpClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return &TransportError{Err: err}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusAccepted, resp.StatusCode == http.StatusOK:
		return nil
	case resp.StatusCode == http.StatusNotFound:
		t.frames.terminate()
		return &TransportError{Err: fmt.Errorf("session not found: %s", resp.Status)}
	default:
		return &TransportError{Err: fmt.Errorf("unexpected status %s", resp.Status)}
	}
}

// Close 旧协议无注销端点：取消 GET 请求断开连接（服务端以流断开注销会话），
// 再终结传输。读协程随 body 读取失败退出。
func (t *sseTransport) Close() error {
	if t.cancel != nil {
		t.cancel()
	}
	t.frames.terminate()
	return nil
}

// DialSSE 旧式 SSE 便捷建连（baseURL 即 SSE 端点；连接在首个操作时建立）。
func DialSSE(baseURL string, opts ...ClientOption) *Client {
	var c *Client
	c = NewClient(func(ctx context.Context) (Transport, error) {
		return newSSETransport(ctx, baseURL, c)
	}, opts...)
	return c
}
