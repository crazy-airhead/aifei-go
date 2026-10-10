// transport_http.go streamable HTTP 客户端传输（对标 WebFluxStreamableClient
// TransportProvider）：单端点 POST 收发（initialize JSON / 其余请求可 SSE 回包 /
// 通知与响应 202），GET 监听流收服务端推送，DELETE 注销。每次 (re)initialize
// 由工厂新建实例——会话 ID 与监听流随实例生命周期。
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/crazy-airhead/aifei-go/log"
)

// httpDefaultClient 共享零配置实例（可经 WithHTTPClient 覆盖）。
var httpDefaultClient = &http.Client{}

// frameChan 入站帧通道：deliver 与 terminate 互斥，杜绝 send-on-closed。
type frameChan struct {
	recv   chan Message
	mu     sync.Mutex
	closed bool
}

func newFrameChan() *frameChan { return &frameChan{recv: make(chan Message, 64)} }

func (f *frameChan) deliver(msg Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	f.recv <- msg // pump 持续消费；持锁阻塞直至腾位（terminate 会等本投递完成）
}

func (f *frameChan) terminate() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	f.closed = true
	close(f.recv)
}

// scanSSE 解析 text/event-stream：空行分发帧（data 多行以 \n 连接；":" 注释行
// 忽略——兼作心跳）。回调出错即中止。
func scanSSE(body io.Reader, fn func(event, data string) error) error {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 64*1024), 16<<20)
	event, data := "", ""
	has := false
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if has {
				if err := fn(event, data); err != nil {
					return err
				}
				event, data, has = "", "", false
			}
		case strings.HasPrefix(line, ":"):
			// comment / keep-alive
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			has = true
		case strings.HasPrefix(line, "data:"):
			d := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" {
				data = d
			} else {
				data += "\n" + d
			}
			has = true
		case strings.HasPrefix(line, "id:"):
			// Last-Event-ID 不使用（无服务端事件 id）
		}
	}
	if has {
		if err := fn(event, data); err != nil {
			return err
		}
	}
	return sc.Err()
}

// httpTransport streamable HTTP 传输。
type httpTransport struct {
	frames  *frameChan
	baseURL string
	client  *Client

	mu        sync.Mutex
	sessionID string
	getArmed  bool
}

func newHTTPTransport(baseURL string, c *Client) *httpTransport {
	return &httpTransport{frames: newFrameChan(), baseURL: baseURL, client: c}
}

func (t *httpTransport) Receive() <-chan Message { return t.frames.recv }

func (t *httpTransport) httpClient() *http.Client {
	if t.client != nil && t.client.httpClient != nil {
		return t.client.httpClient
	}
	return httpDefaultClient
}

// Send 帧出口：请求 POST（收响应）；通知/响应 POST（202 确认）。
func (t *httpTransport) Send(ctx context.Context, msg Message) error {
	if req, ok := msg.(*JSONRPCRequest); ok {
		return t.postRequest(ctx, req)
	}
	return t.postFrame(ctx, msg, "application/json, text/event-stream")
}

// postRequest 请求 POST：200 JSON → 单帧投递；200 SSE → 异步消费至收流
// （Java Flux 异步语义，Send 不等 EOF）；202 容忍；404（带会话）→ 传输终止
// （客户端门面自愈重连）；其余 → TransportError。
func (t *httpTransport) postRequest(ctx context.Context, req *JSONRPCRequest) error {
	isInit := req.Method == MethodInitialize
	resp, err := t.do(ctx, http.MethodPost, "application/json, text/event-stream",
		mustMarshal(req), !isInit)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusAccepted {
		resp.Body.Close()
		return nil
	}
	if resp.StatusCode == http.StatusNotFound && !isInit {
		resp.Body.Close()
		t.frames.terminate() // 会话失效：终结传输
		return &TransportError{Err: fmt.Errorf("session not found: %s", resp.Status)}
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return &TransportError{Err: fmt.Errorf("unexpected status %s", resp.Status)}
	}
	if isInit {
		if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
			t.mu.Lock()
			t.sessionID = sid
			t.mu.Unlock()
		}
	}
	ct := resp.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(ct, "application/json"):
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return &TransportError{Err: err}
		}
		msg, err := UnmarshalMessage(body)
		if err != nil {
			return &TransportError{Err: err}
		}
		if r, ok := msg.(*JSONRPCResponse); ok {
			t.frames.deliver(r)
		}
		t.armGet()
		return nil
	case strings.HasPrefix(ct, "text/event-stream"):
		t.armGet()
		go func() {
			defer resp.Body.Close()
			if err := scanSSE(resp.Body, func(_, data string) error {
				return t.deliverData(data)
			}); err != nil {
				log.Warn("mcp: http response stream ended with error: %v", err)
			}
		}()
		return nil
	default:
		resp.Body.Close()
		return &TransportError{Err: fmt.Errorf("unexpected content type %q", ct)}
	}
}

// postFrame 通知/响应帧 POST（202 确认）。
func (t *httpTransport) postFrame(ctx context.Context, msg Message, accept string) error {
	resp, err := t.do(ctx, http.MethodPost, accept, mustMarshal(msg), true)
	if err != nil {
		return err
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

func (t *httpTransport) deliverData(data string) error {
	msg, err := UnmarshalMessage([]byte(data))
	if err != nil {
		return err
	}
	t.frames.deliver(msg)
	return nil
}

// armGet 首个会话 ID 落位后拉起 GET 监听流（一次）。
func (t *httpTransport) armGet() {
	t.mu.Lock()
	if t.getArmed || t.sessionID == "" {
		t.mu.Unlock()
		return
	}
	t.getArmed = true
	sid := t.sessionID
	t.mu.Unlock()
	go t.getStream(sid)
}

// getStream GET 监听流：全部服务端主动帧（通知/反向请求）由此抵达；流断即
// 传输终结（门面自愈）。
func (t *httpTransport) getStream(sid string) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, t.baseURL, nil)
	if err != nil {
		t.frames.terminate()
		return
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Mcp-Session-Id", sid)
	t.applyExtraHeaders(req)
	resp, err := t.httpClient().Do(req)
	if err != nil {
		t.frames.terminate()
		return
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.frames.terminate()
		return
	}
	err = scanSSE(resp.Body, func(_, data string) error {
		return t.deliverData(data)
	})
	resp.Body.Close()
	if err != nil {
		log.Debug("mcp: listening stream error: %v", err)
	}
	t.frames.terminate() // 监听流结束 = 会话断连
}

func (t *httpTransport) applyExtraHeaders(req *http.Request) {
	if t.client == nil {
		return
	}
	for k, vs := range t.client.extraHeaders {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
}

func (t *httpTransport) do(ctx context.Context, method, accept string, body []byte, withSession bool) (*http.Response, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, t.baseURL, rdr)
	if err != nil {
		return nil, &TransportError{Err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", accept)
	t.applyExtraHeaders(req)
	if withSession {
		t.mu.Lock()
		sid := t.sessionID
		t.mu.Unlock()
		if sid != "" {
			req.Header.Set("Mcp-Session-Id", sid)
		}
	}
	resp, err := t.httpClient().Do(req)
	if err != nil {
		return nil, &TransportError{Err: err}
	}
	return resp, nil
}

// Close 注销会话（DELETE）并终结传输。
func (t *httpTransport) Close() error {
	t.mu.Lock()
	sid := t.sessionID
	t.mu.Unlock()
	if sid != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if resp, err := t.do(ctx, http.MethodDelete, "application/json", nil, true); err == nil {
			resp.Body.Close()
		}
	}
	t.frames.terminate()
	return nil
}

func mustMarshal(msg Message) []byte {
	b, err := json.Marshal(msg)
	if err != nil {
		panic("mcp: marshal frame: " + err.Error()) // 帧结构体皆可序列化，不可达
	}
	return b
}

// DialHTTP streamable HTTP 便捷建连（懒连接：首个操作时握手；重连 = 新传输 +
// 重新握手；工厂闭包捕获 *Client 以读取 headers/httpClient 选项）。
func DialHTTP(baseURL string, opts ...ClientOption) *Client {
	var c *Client
	c = NewClient(func(ctx context.Context) (Transport, error) {
		return newHTTPTransport(baseURL, c), nil
	}, opts...)
	return c
}
