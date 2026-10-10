// mcp_test MCP 协议层黑盒测试（P3-a）：UnmarshalMessage 判别表驱动（含畸形 id
// 拒绝）、pending 表并道（50 并发请求乱序回包）、版本协商矩阵，附会话分发/
// 超时/关闭、内容多态。
package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/crazy-airhead/aifei-go/mcp"
)

// ---- 测试缝：内存双通道假传输 ----

type fakeTransport struct {
	sent chan mcp.Message
	recv chan mcp.Message
	done chan struct{}
	once sync.Once
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{
		sent: make(chan mcp.Message, 64), // 缓冲：允许同 goroutine 先发后收
		recv: make(chan mcp.Message, 256),
		done: make(chan struct{}),
	}
}

func (t *fakeTransport) Send(_ context.Context, m mcp.Message) error {
	select {
	case t.sent <- m:
		return nil
	case <-t.done:
		return mcp.ErrSessionClosed
	}
}

func (t *fakeTransport) Receive() <-chan mcp.Message { return t.recv }

func (t *fakeTransport) Close() error {
	t.once.Do(func() { close(t.done); close(t.recv) })
	return nil
}

// feed 把原始 JSON 帧经 UnmarshalMessage 解码后注入传输（模拟真实传输的
// 入站路径——P3-b/c 的 stdio/HTTP 传输同样经 UnmarshalMessage 解码）。
func feed(t *testing.T, ft *fakeTransport, frame string) {
	t.Helper()
	msg, err := mcp.UnmarshalMessage([]byte(frame))
	if err != nil {
		t.Fatalf("feed: UnmarshalMessage(%s): %v", frame, err)
	}
	ft.recv <- msg
}

func recvSent(t *testing.T, ft *fakeTransport) mcp.Message {
	t.Helper()
	select {
	case m := <-ft.sent:
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for outbound frame")
		return nil
	}
}

// ---- 1. UnmarshalMessage 判别表驱动 ----

func TestUnmarshalMessageDispatch(t *testing.T) {
	cases := []struct {
		name  string
		frame string
		check func(t *testing.T, m mcp.Message)
	}{
		{
			name:  "request_string_id",
			frame: `{"jsonrpc":"2.0","id":"abc","method":"tools/list","params":{"cursor":"x"}}`,
			check: func(t *testing.T, m mcp.Message) {
				req, ok := m.(*mcp.JSONRPCRequest)
				if !ok {
					t.Fatalf("want *JSONRPCRequest, got %T", m)
				}
				if req.ID != "abc" || req.Method != "tools/list" {
					t.Fatalf("id=%v method=%s", req.ID, req.Method)
				}
				if string(req.Params) != `{"cursor":"x"}` {
					t.Fatalf("params=%s", req.Params)
				}
			},
		},
		{
			name:  "request_int_id_normalized_to_int64",
			frame: `{"jsonrpc":"2.0","id":42,"method":"ping"}`,
			check: func(t *testing.T, m mcp.Message) {
				req := m.(*mcp.JSONRPCRequest)
				if req.ID != int64(42) {
					t.Fatalf("id=%v (%T), want int64(42)", req.ID, req.ID)
				}
			},
		},
		{
			name:  "request_large_int_id_no_float_precision_loss",
			frame: `{"jsonrpc":"2.0","id":9007199254740993,"method":"ping"}`,
			check: func(t *testing.T, m mcp.Message) {
				req := m.(*mcp.JSONRPCRequest)
				if req.ID != int64(9007199254740993) { // 2^53+1，float64 会丢 1
					t.Fatalf("id=%v, want 9007199254740993", req.ID)
				}
			},
		},
		{
			name:  "notification_no_id",
			frame: `{"jsonrpc":"2.0","method":"notifications/initialized"}`,
			check: func(t *testing.T, m mcp.Message) {
				n, ok := m.(*mcp.JSONRPCNotification)
				if !ok {
					t.Fatalf("want *JSONRPCNotification, got %T", m)
				}
				if n.Method != "notifications/initialized" {
					t.Fatalf("method=%s", n.Method)
				}
			},
		},
		{
			name:  "response_with_result",
			frame: `{"jsonrpc":"2.0","id":7,"result":{"tools":[]}}`,
			check: func(t *testing.T, m mcp.Message) {
				resp, ok := m.(*mcp.JSONRPCResponse)
				if !ok {
					t.Fatalf("want *JSONRPCResponse, got %T", m)
				}
				if resp.ID != int64(7) || resp.Error != nil || string(resp.Result) != `{"tools":[]}` {
					t.Fatalf("id=%v result=%s err=%v", resp.ID, resp.Result, resp.Error)
				}
			},
		},
		{
			name:  "response_with_error",
			frame: `{"jsonrpc":"2.0","id":7,"error":{"code":-32601,"message":"Method not found"}}`,
			check: func(t *testing.T, m mcp.Message) {
				resp := m.(*mcp.JSONRPCResponse)
				if resp.Error == nil || resp.Error.Code != mcp.CodeMethodNotFound {
					t.Fatalf("error=%v", resp.Error)
				}
			},
		},
		{
			name:  "response_null_id_allowed",
			frame: `{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"parse error"}}`,
			check: func(t *testing.T, m mcp.Message) {
				resp, ok := m.(*mcp.JSONRPCResponse)
				if !ok || resp.ID != nil {
					t.Fatalf("want response with nil id, got %T id=%v", m, resp.ID)
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, err := mcp.UnmarshalMessage([]byte(c.frame))
			if err != nil {
				t.Fatalf("UnmarshalMessage: %v", err)
			}
			c.check(t, m)
		})
	}
}

func TestUnmarshalMessageRejections(t *testing.T) {
	cases := []struct {
		name     string
		frame    string
		wantCode int // 0 = 非 JSONRPCError 的普通错误
	}{
		{name: "malformed_json", frame: `{"jsonrpc":"2.0","id":`, wantCode: mcp.CodeParseError},
		{name: "array_frame", frame: `[]`, wantCode: mcp.CodeParseError},
		{name: "null_frame", frame: `null`, wantCode: mcp.CodeParseError},
		{name: "id_float_fractional", frame: `{"jsonrpc":"2.0","id":1.5,"method":"ping"}`, wantCode: mcp.CodeInvalidRequest},
		{name: "id_null", frame: `{"jsonrpc":"2.0","id":null,"method":"ping"}`, wantCode: mcp.CodeInvalidRequest},
		{name: "id_bool", frame: `{"jsonrpc":"2.0","id":true,"method":"ping"}`, wantCode: mcp.CodeInvalidRequest},
		{name: "id_array", frame: `{"jsonrpc":"2.0","id":[1],"method":"ping"}`, wantCode: mcp.CodeInvalidRequest},
		{name: "id_object", frame: `{"jsonrpc":"2.0","id":{"x":1},"method":"ping"}`, wantCode: mcp.CodeInvalidRequest},
		{name: "neither_method_nor_result_error", frame: `{"jsonrpc":"2.0","id":1}`, wantCode: 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, err := mcp.UnmarshalMessage([]byte(c.frame))
			if err == nil {
				t.Fatalf("want error, got message %T", m)
			}
			if c.wantCode == 0 {
				if !strings.Contains(err.Error(), "cannot deserialize JSONRPCMessage") {
					t.Fatalf("want IAE-style error, got %v", err)
				}
				return
			}
			var rpcErr *mcp.JSONRPCError
			if !errors.As(err, &rpcErr) {
				t.Fatalf("want *JSONRPCError, got %T: %v", err, err)
			}
			if rpcErr.Code != c.wantCode {
				t.Fatalf("code=%d, want %d (%v)", rpcErr.Code, c.wantCode, err)
			}
		})
	}
}

// ---- 2. pending 表并道：50 并发请求乱序回包 ----

func TestSessionPendingConcurrent(t *testing.T) {
	const n = 50
	ft := newFakeTransport()
	session := mcp.NewSession(ft, nil, nil)
	defer session.Close()

	type outcome struct {
		idx  int
		ans  int
		err  error
	}
	outcomes := make(chan outcome, n)
	var wg sync.WaitGroup
	ctx := context.Background()

	// 发起方：50 个并发请求，各带可辨识载荷
	for i := 1; i <= n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			var out struct {
				Answer int `json:"answer"`
			}
			err := session.SendRequest(ctx, "computation/double", map[string]any{"n": idx}, &out)
			outcomes <- outcome{idx: idx, ans: out.Answer, err: err}
		}(i)
	}

	// 服务端：收满 50 个请求 → 乱序回包（并夹带 unknown-id 与 nil-id 干扰帧）
	ids := make([]int64, 0, n)
	idToN := make(map[int64]int, n)
	for len(ids) < n {
		m := recvSent(t, ft)
		req, ok := m.(*mcp.JSONRPCRequest)
		if !ok {
			t.Fatalf("want request, got %T", m)
		}
		var p struct {
			N int `json:"n"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			t.Fatalf("params: %v", err)
		}
		ids = append(ids, req.ID.(int64))
		idToN[req.ID.(int64)] = p.N
	}
	rand.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
	for _, id := range ids {
		frame := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"answer":%d}}`, id, idToN[id]*2)
		feed(t, ft, frame)
	}
	// 干扰帧：未知 id（warn 丢弃）与 nil id（error 丢弃），均不影响正确回包
	feed(t, ft, `{"jsonrpc":"2.0","id":9999,"result":{}}`)
	feed(t, ft, `{"jsonrpc":"2.0","id":null,"result":{}}`)

	wg.Wait()
	close(outcomes)
	for o := range outcomes {
		if o.err != nil {
			t.Errorf("request %d: %v", o.idx, o.err)
			continue
		}
		if o.ans != o.idx*2 {
			t.Errorf("request %d got answer %d (乱序并道错配)", o.idx, o.ans)
		}
	}
}

// ---- 3. 版本协商矩阵 ----

func TestVersionNegotiateMatrix(t *testing.T) {
	if got := mcp.Latest(mcp.SupportedVersions); got != mcp.Version20251125 {
		t.Fatalf("Latest = %s, want %s", got, mcp.Version20251125)
	}
	if got := mcp.Latest(nil); got != "" {
		t.Fatalf("Latest(nil) = %s", got)
	}
	// 方阵：全量客户端表 × 每个服务端版本，对角线命中
	for _, sv := range mcp.SupportedVersions {
		v, err := mcp.Negotiate(mcp.SupportedVersions, sv)
		if err != nil || v != sv {
			t.Errorf("Negotiate(full, %s) = %s, %v; want hit", sv, v, err)
		}
	}
	// 服务端版本不在客户端表 → -32602 + 诊断 data
	for _, bad := range []string{"1999-01-01", "", "2025-06-19"} {
		_, err := mcp.Negotiate(mcp.SupportedVersions, bad)
		if err == nil {
			t.Errorf("Negotiate(full, %q) should fail", bad)
			continue
		}
		var rpcErr *mcp.JSONRPCError
		if !errors.As(err, &rpcErr) || rpcErr.Code != mcp.CodeInvalidParams {
			t.Errorf("Negotiate(full, %q) = %v; want -32602", bad, err)
		}
		if !strings.Contains(fmt.Sprint(rpcErr.Data), bad) {
			t.Errorf("data %v should echo server version %q", rpcErr.Data, bad)
		}
	}
	// 旧式 SSE 客户端（仅 2024-11-05）遇新服务端 → 拒绝
	if _, err := mcp.Negotiate([]string{mcp.Version20241105}, mcp.Version20250326); err == nil {
		t.Fatal("legacy client accepting newer server version")
	}
}

// ---- 4. 会话服务端分发 ----

func TestSessionServerSideDispatch(t *testing.T) {
	notified := make(chan string, 4)
	ft := newFakeTransport()
	session := mcp.NewSession(ft, map[string]mcp.RequestHandlerFunc{
		"echo": func(_ context.Context, params json.RawMessage) (any, *mcp.JSONRPCError) {
			var p struct {
				X int `json:"x"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, &mcp.JSONRPCError{Code: mcp.CodeInvalidParams, Message: err.Error()}
			}
			return map[string]int{"echoed": p.X}, nil
		},
		"fail": func(_ context.Context, _ json.RawMessage) (any, *mcp.JSONRPCError) {
			return nil, &mcp.JSONRPCError{Code: mcp.CodeResourceNotFound, Message: "no such resource", Data: "file:///a.txt"}
		},
		"boom": func(_ context.Context, _ json.RawMessage) (any, *mcp.JSONRPCError) {
			panic("kaboom")
		},
	}, map[string]mcp.NotificationHandlerFunc{
		"notifications/message": func(_ json.RawMessage) error {
			notified <- "message"
			return nil
		},
	})
	defer session.Close()

	// 命中 handler → result 响应
	feed(t, ft, `{"jsonrpc":"2.0","id":7,"method":"echo","params":{"x":1}}`)
	resp := recvSent(t, ft).(*mcp.JSONRPCResponse)
	if resp.ID != int64(7) || resp.Error != nil || !strings.Contains(string(resp.Result), `"echoed":1`) {
		t.Fatalf("echo response: id=%v err=%v result=%s", resp.ID, resp.Error, resp.Result)
	}

	// handler 业务错误 → 透传 code/message/data
	feed(t, ft, `{"jsonrpc":"2.0","id":8,"method":"fail"}`)
	resp = recvSent(t, ft).(*mcp.JSONRPCResponse)
	if resp.Error == nil || resp.Error.Code != mcp.CodeResourceNotFound ||
		resp.Error.Message != "no such resource" || resp.Error.Data != "file:///a.txt" {
		t.Fatalf("fail response: %v", resp.Error)
	}

	// handler panic → INTERNAL_ERROR
	feed(t, ft, `{"jsonrpc":"2.0","id":9,"method":"boom"}`)
	resp = recvSent(t, ft).(*mcp.JSONRPCResponse)
	if resp.Error == nil || resp.Error.Code != mcp.CodeInternalError || resp.Error.Message != "kaboom" {
		t.Fatalf("boom response: %v", resp.Error)
	}

	// 未知方法 → MethodNotFound（Java getMethodNotFoundError 文案）
	feed(t, ft, `{"jsonrpc":"2.0","id":10,"method":"foo/bar"}`)
	resp = recvSent(t, ft).(*mcp.JSONRPCResponse)
	if resp.Error == nil || resp.Error.Code != mcp.CodeMethodNotFound ||
		resp.Error.Message != "Method not found: foo/bar" {
		t.Fatalf("unknown method response: %v", resp.Error)
	}

	// roots/list 特判文案与 data
	feed(t, ft, `{"jsonrpc":"2.0","id":11,"method":"roots/list"}`)
	resp = recvSent(t, ft).(*mcp.JSONRPCResponse)
	if resp.Error == nil || resp.Error.Code != mcp.CodeMethodNotFound ||
		resp.Error.Message != "Roots not supported" {
		t.Fatalf("roots/list response: %v", resp.Error)
	}
	data, _ := resp.Error.Data.(map[string]any)
	if data["reason"] != "Client does not have roots capability" {
		t.Fatalf("roots/list data: %v", resp.Error.Data)
	}

	// 通知分发（无响应帧）
	feed(t, ft, `{"jsonrpc":"2.0","method":"notifications/message","params":{"level":"info"}}`)
	select {
	case <-notified:
	case <-time.After(2 * time.Second):
		t.Fatal("notification handler not invoked")
	}

	// 出站通知：无 id 帧
	if err := session.SendNotification("notifications/cancelled", map[string]any{"id": 7}); err != nil {
		t.Fatalf("SendNotification: %v", err)
	}
	notif := recvSent(t, ft).(*mcp.JSONRPCNotification)
	if notif.Method != "notifications/cancelled" || string(notif.Params) != `{"id":7}` {
		t.Fatalf("notification frame: %s %s", notif.Method, notif.Params)
	}
}

// ---- 5. 超时 ----

func TestSessionRequestTimeout(t *testing.T) {
	ft := newFakeTransport()
	session := mcp.NewSession(ft, nil, nil)
	defer session.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := session.SendRequest(ctx, "ping", nil, nil)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline error, got %v", err)
	}
}

// ---- 6. 关闭清场：pending 请求以 ErrSessionClosed 终止 ----

func TestSessionCloseDismissesPending(t *testing.T) {
	ft := newFakeTransport()
	session := mcp.NewSession(ft, nil, nil)

	errCh := make(chan error, 1)
	go func() {
		errCh <- session.SendRequest(context.Background(), "ping", nil, nil)
	}()

	// 等请求真正挂到 pending 表后再关闭
	if m := recvSent(t, ft); m.(*mcp.JSONRPCRequest).Method != "ping" {
		t.Fatalf("want ping request")
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, mcp.ErrSessionClosed) {
			t.Fatalf("pending request terminated with %v, want ErrSessionClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending request not dismissed by Close")
	}
	// 关闭后再发请求立即失败
	if err := session.SendRequest(context.Background(), "ping", nil, nil); !errors.Is(err, mcp.ErrSessionClosed) {
		t.Fatalf("post-close SendRequest = %v, want ErrSessionClosed", err)
	}
	// Close 幂等
	if err := session.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// ---- 7. 内容多态 ----

func TestContentPolymorphism(t *testing.T) {
	// 五种内容块构造 → JSON 判别 → 回读同型
	cases := []struct {
		c    mcp.Content
		want string
	}{
		{mcp.NewTextContent("hi"), `"type":"text"`},
		{mcp.NewImageContent("aGk=", "image/png"), `"type":"image"`},
		{mcp.NewAudioContent("aGk=", "audio/wav"), `"type":"audio"`},
		{mcp.NewEmbeddedResource(&mcp.TextResourceContents{URI: "file:///a.txt", Text: "abc"}), `"type":"resource"`},
		{mcp.NewResourceLink("a", "file:///a.txt"), `"type":"resource_link"`},
	}
	for _, c := range cases {
		b, err := json.Marshal(c.c)
		if err != nil {
			t.Fatalf("marshal %T: %v", c.c, err)
		}
		if !strings.Contains(string(b), c.want) {
			t.Fatalf("%T json %s lacks %s", c.c, b, c.want)
		}
		back, err := mcp.UnmarshalContent(b)
		if err != nil {
			t.Fatalf("UnmarshalContent(%s): %v", b, err)
		}
		if back.ContentType() != c.c.ContentType() {
			t.Fatalf("round-trip type %s != %s", back.ContentType(), c.c.ContentType())
		}
	}
	// 未知内容类型拒绝
	if _, err := mcp.UnmarshalContent([]byte(`{"type":"video","text":"x"}`)); err == nil {
		t.Fatal("unknown content type accepted")
	}

	// CallToolResult 混合内容列表
	var ctr mcp.CallToolResult
	frame := `{"content":[{"type":"text","text":"ok"},{"type":"resource_link","name":"a","uri":"file:///a.txt"}],"isError":false}`
	if err := json.Unmarshal([]byte(frame), &ctr); err != nil {
		t.Fatalf("CallToolResult: %v", err)
	}
	if len(ctr.Content) != 2 || ctr.Content[0].ContentType() != "text" || ctr.Content[1].ContentType() != "resource_link" {
		t.Fatalf("CallToolResult content: %#v", ctr.Content)
	}

	// ResourceContents DEDUCTION：有 text/blob 键即判别
	rc, err := mcp.UnmarshalResourceContents([]byte(`{"uri":"file:///a.txt","text":"abc"}`))
	if err != nil {
		t.Fatalf("text resource: %v", err)
	}
	if _, ok := rc.(*mcp.TextResourceContents); !ok {
		t.Fatalf("want TextResourceContents, got %T", rc)
	}
	rc, err = mcp.UnmarshalResourceContents([]byte(`{"uri":"file:///a.bin","blob":"aGk="}`))
	if err != nil {
		t.Fatalf("blob resource: %v", err)
	}
	if _, ok := rc.(*mcp.BlobResourceContents); !ok {
		t.Fatalf("want BlobResourceContents, got %T", rc)
	}
	if _, err := mcp.UnmarshalResourceContents([]byte(`{"uri":"file:///x"}`)); err == nil {
		t.Fatal("resource without text/blob accepted")
	}

	// ReadResourceResult 列表判别
	var rrr mcp.ReadResourceResult
	if err := json.Unmarshal([]byte(`{"contents":[{"uri":"file:///a.txt","text":"a"},{"uri":"file:///b.bin","blob":"Yg=="}]}`), &rrr); err != nil {
		t.Fatalf("ReadResourceResult: %v", err)
	}
	if len(rrr.Contents) != 2 {
		t.Fatalf("contents: %#v", rrr.Contents)
	}
}
