// server_test.go P3-c 服务端黑盒测试：streamable HTTP 全流程（会话建立/三注册表
// /变更广播/订阅更新/DELETE 注销/Last-Event-ID/安全钩子/会话失效自愈/反向请求
// 走 POST 流）+ stdio 回环（TestMain 分身跑真实 Server）+ RegisterTools 反射
// 注册 + aifei 挂载互操作。
package mcp_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crazy-airhead/aifei-go/mcp"
)

// waitFor 轮询直至条件成立（广播类通知存在监听流就绪竞态，以公开的 Notify*
// 重试收敛）。
func waitFor(t *testing.T, what string, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.After(timeout)
	for !fn() {
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for %s", what)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// drainCh 排空通道缓冲（重试循环遗留的重复投递；静默断言前必做）。
func drainCh[T any](ch chan T) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

// newTestServer 建带 echo/boom 工具的测试服务端。
func newTestServer(opts ...mcp.ServerOption) *mcp.Server {
	s := mcp.NewServer(mcp.Implementation{Name: "srv", Version: "2.0"},
		append([]mcp.ServerOption{mcp.WithInstructions("use me")}, opts...)...)
	s.Tool("echo", `{"type":"object","properties":{"text":{"type":"string"}}}`,
		func(cc *mcp.CallCtx, args map[string]any) (*mcp.CallToolResult, error) {
			text, _ := args["text"].(string)
			return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent("echo:" + text)}}, nil
		}).Desc("回声")
	s.Tool("boom", "", func(cc *mcp.CallCtx, _ map[string]any) (*mcp.CallToolResult, error) {
		return nil, errors.New("boom failed")
	})
	return s
}

func TestServerHTTPFullLoop(t *testing.T) {
	s := newTestServer()
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	c := mcp.DialHTTP(ts.URL)
	defer c.Close()

	init, err := c.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if init.ServerInfo.Name != "srv" || c.ServerInstructions() != "use me" {
		t.Fatalf("init = %+v instructions=%q", init.ServerInfo, c.ServerInstructions())
	}
	if v := c.NegotiatedVersion(); v != mcp.Version20251125 {
		t.Fatalf("negotiated = %q（应回显客户端最新）", v)
	}

	tools, err := c.ListAllTools(context.Background())
	if err != nil {
		t.Fatalf("ListAllTools: %v", err)
	}
	if len(tools) != 2 || tools[0].Name != "boom" || tools[1].Name != "echo" || tools[1].Description != "回声" {
		t.Fatalf("tools = %+v", tools)
	}

	res, err := c.CallTool(context.Background(), "echo", map[string]any{"text": "hi"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if tc, ok := res.Content[0].(*mcp.TextContent); !ok || tc.Text != "echo:hi" {
		t.Fatalf("content = %+v", res.Content[0])
	}

	// 处理器错误 → 协议层成功 + isError（Java McpResultResponder 语义）
	res, err = c.CallTool(context.Background(), "boom", nil)
	if err != nil {
		t.Fatalf("boom 应为 isError 结果而非协议错误: %v", err)
	}
	if !res.IsError {
		t.Fatal("boom 结果应 isError")
	}
	if tc, ok := res.Content[0].(*mcp.TextContent); !ok || !strings.Contains(tc.Text, "boom failed") {
		t.Fatalf("boom content = %+v", res.Content[0])
	}

	// 未知工具 → -32602
	_, err = c.CallTool(context.Background(), "nope", nil)
	var rpcErr *mcp.JSONRPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != mcp.CodeInvalidParams ||
		!strings.Contains(rpcErr.Message, "Tool not found: nope") {
		t.Fatalf("err = %v（应 -32602 Tool not found）", err)
	}

	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if n := s.SessionCount(); n != 1 {
		t.Fatalf("session count = %d", n)
	}

	// Close → DELETE 注销
	c.Close()
	waitFor(t, "session deleted", 3*time.Second, func() bool { return s.SessionCount() == 0 })
}

func TestServerHTTPRegistryChange(t *testing.T) {
	s := newTestServer()
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	changed := make(chan []mcp.Tool, 4)
	c := mcp.DialHTTP(ts.URL, mcp.WithToolsChangeConsumer(func(tools []mcp.Tool) {
		changed <- tools
	}))
	defer c.Close()
	if _, err := c.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// 监听流就绪存在竞态：以公开 Notify* 广播重试收敛；收敛后排空在途重复投递
	waitFor(t, "tools list_changed", 5*time.Second, func() bool {
		s.NotifyToolsListChanged()
		select {
		case <-changed:
			return true
		default:
			return false
		}
	})
	time.Sleep(150 * time.Millisecond)
	drainCh(changed)

	// RemoveTool → 自动广播（消费者重拉后只剩 boom）
	if !s.RemoveTool("echo") || s.RemoveTool("echo") {
		t.Fatal("RemoveTool 幂等性破坏")
	}
	select {
	case tools := <-changed:
		if len(tools) != 1 || tools[0].Name != "boom" {
			t.Fatalf("changed tools = %+v", tools)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("remove 未触发变更通知")
	}
	time.Sleep(150 * time.Millisecond)
	drainCh(changed)

	// 替换注册 = 覆盖 + 广播
	s.Tool("echo", "", func(*mcp.CallCtx, map[string]any) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent("v2")}}, nil
	})
	select {
	case tools := <-changed:
		if len(tools) != 2 {
			t.Fatalf("re-add tools = %+v", tools)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("re-add 未触发变更通知")
	}
}

func TestServerHTTPResources(t *testing.T) {
	s := mcp.NewServer(mcp.Implementation{Name: "res", Version: "1"})
	s.Resource("memo://notes", "notes", func(cc *mcp.CallCtx) (any, error) {
		return "note text", nil
	}).MimeType("text/x-note").Desc("备忘")
	s.Resource("db://users/{id}/posts", "posts", func(cc *mcp.CallCtx) (any, error) {
		return "posts of " + cc.RequestVars()["id"], nil
	})
	s.Resource("bin://logo", "logo", func(cc *mcp.CallCtx) (any, error) {
		return []byte{0x00, 0x01}, nil
	})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	updates := make(chan []mcp.ResourceContents, 4)
	c := mcp.DialHTTP(ts.URL, mcp.WithResourcesUpdateConsumer(func(rc []mcp.ResourceContents) {
		updates <- rc
	}))
	defer c.Close()
	if _, err := c.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// 静态资源：string → 文本内容（注册链式 MIME）
	res, err := c.ReadResource(context.Background(), "memo://notes")
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	tc, ok := res.Contents[0].(*mcp.TextResourceContents)
	if !ok || tc.Text != "note text" || tc.MimeType != "text/x-note" || tc.URI != "memo://notes" {
		t.Fatalf("contents = %+v", res.Contents[0])
	}

	// 模板资源：{id} 变量注入
	res, err = c.ReadResource(context.Background(), "db://users/42/posts")
	if err != nil {
		t.Fatalf("ReadResource template: %v", err)
	}
	tc, ok = res.Contents[0].(*mcp.TextResourceContents)
	if !ok || tc.Text != "posts of 42" {
		t.Fatalf("template contents = %+v", res.Contents[0])
	}

	// []byte → base64 blob
	res, err = c.ReadResource(context.Background(), "bin://logo")
	if err != nil {
		t.Fatalf("ReadResource blob: %v", err)
	}
	bc, ok := res.Contents[0].(*mcp.BlobResourceContents)
	if !ok || bc.Blob != "AAE=" {
		t.Fatalf("blob contents = %+v", res.Contents[0])
	}

	// 列表：静态 + 模板分开两接口
	rr, err := c.ListAllResources(context.Background())
	if err != nil || len(rr) != 2 {
		t.Fatalf("resources = %+v err=%v", rr, err)
	}
	tt, err := c.ListResourceTemplates(context.Background(), "")
	if err != nil || len(tt.ResourceTemplates) != 1 || tt.ResourceTemplates[0].URITemplate != "db://users/{id}/posts" {
		t.Fatalf("templates = %+v err=%v", tt, err)
	}

	// 未知资源 → -32002
	_, err = c.ReadResource(context.Background(), "nope://x")
	var rpcErr *mcp.JSONRPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != mcp.CodeResourceNotFound {
		t.Fatalf("err = %v（应 -32002）", err)
	}

	// 订阅 → NotifyResourceUpdated → 消费者收到自动重读
	if err := c.SubscribeResource(context.Background(), "memo://notes"); err != nil {
		t.Fatalf("SubscribeResource: %v", err)
	}
	waitFor(t, "resource updated", 5*time.Second, func() bool {
		s.NotifyResourceUpdated("memo://notes")
		select {
		case rc := <-updates:
			if len(rc) == 1 {
				if t2, ok := rc[0].(*mcp.TextResourceContents); ok && t2.Text == "note text" {
					return true
				}
			}
			return false
		default:
			return false
		}
	})

	// 退订后不再推送（先等在途投递落定并排空重试遗留）
	if err := c.UnsubscribeResource(context.Background(), "memo://notes"); err != nil {
		t.Fatalf("UnsubscribeResource: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	drainCh(updates)
	s.NotifyResourceUpdated("memo://notes")
	select {
	case rc := <-updates:
		t.Fatalf("退订后仍收到推送: %+v", rc)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestServerHTTPPromptsAndLogging(t *testing.T) {
	s := mcp.NewServer(mcp.Implementation{Name: "p", Version: "1"})
	s.Prompt("greet", []mcp.PromptArgument{{Name: "name", Description: "姓名"}},
		func(cc *mcp.CallCtx, args map[string]string) (any, error) {
			return "hi " + args["name"], nil
		}).Desc("问候")
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	logs := make(chan mcp.LoggingMessageNotification, 4)
	c := mcp.DialHTTP(ts.URL, mcp.WithLoggingConsumer(func(n mcp.LoggingMessageNotification) {
		logs <- n
	}))
	defer c.Close()
	if _, err := c.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	prompts, err := c.ListAllPrompts(context.Background())
	if err != nil || len(prompts) != 1 || prompts[0].Description != "问候" {
		t.Fatalf("prompts = %+v err=%v", prompts, err)
	}
	pr, err := c.GetPrompt(context.Background(), "greet", map[string]string{"name": "go"})
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}
	if len(pr.Messages) != 1 || pr.Messages[0].Role != mcp.RoleUser {
		t.Fatalf("prompt result = %+v", pr)
	}
	if tc, ok := pr.Messages[0].Content.(*mcp.TextContent); !ok || tc.Text != "hi go" {
		t.Fatalf("prompt content = %+v", pr.Messages[0].Content)
	}

	_, err = c.GetPrompt(context.Background(), "nope", nil)
	var rpcErr *mcp.JSONRPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != mcp.CodeInvalidParams ||
		rpcErr.Message != "Invalid prompt name" || fmt.Sprint(rpcErr.Data) != "Prompt not found: nope" {
		t.Fatalf("err = %v（应 -32602 Invalid prompt name）", err)
	}

	// 日志级别过滤：setLevel(warning) 后 info 被滤、error 放行
	if err := c.SetLoggingLevel(context.Background(), mcp.LogLevelWarning); err != nil {
		t.Fatalf("SetLoggingLevel: %v", err)
	}
	waitFor(t, "error-level log", 5*time.Second, func() bool {
		s.NotifyLogging(mcp.LoggingMessageNotification{Level: mcp.LogLevelError, Data: "bad"})
		select {
		case <-logs:
			return true
		default:
			return false
		}
	})
	time.Sleep(150 * time.Millisecond)
	drainCh(logs)
	s.NotifyLogging(mcp.LoggingMessageNotification{Level: mcp.LogLevelInfo, Data: "quiet"})
	select {
	case n := <-logs:
		t.Fatalf("info 级别不应送达: %+v", n)
	case <-time.After(300 * time.Millisecond):
	}
}

// ---- 裸 HTTP 语义测试（不经客户端门面）----

// rawClient 裸 streamable HTTP 客户端（帧级断言用）。
type rawClient struct {
	base string
	http *http.Client
	sess string
}

func (rc *rawClient) initialize(t *testing.T) {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18",` +
		`"capabilities":{"sampling":{}},"clientInfo":{"name":"raw","version":"1"}}}`
	req, _ := http.NewRequest(http.MethodPost, rc.base, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := rc.http.Do(req)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("initialize status = %d", resp.StatusCode)
	}
	rc.sess = resp.Header.Get("Mcp-Session-Id")
	if rc.sess == "" {
		t.Fatal("initialize 未返回 Mcp-Session-Id")
	}
	var frame struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
			ServerInfo      mcp.Implementation
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&frame); err != nil {
		t.Fatalf("decode initialize: %v", err)
	}
	if frame.Result.ProtocolVersion != mcp.Version20250618 {
		t.Fatalf("negotiated = %q（应回显请求版本）", frame.Result.ProtocolVersion)
	}
}

// post POST 单帧，返回响应。
func (rc *rawClient) post(t *testing.T, frame string, session bool) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, rc.base, strings.NewReader(frame))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if session {
		req.Header.Set("Mcp-Session-Id", rc.sess)
	}
	resp, err := rc.http.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	return resp
}

// readSSEFrames 从 SSE 响应体读全部帧（阻塞至收流；服务端响应后即收流）。
func readSSEFrames(t *testing.T, body io.Reader) []json.RawMessage {
	t.Helper()
	var out []json.RawMessage
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var data string
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if data != "" {
				out = append(out, json.RawMessage(data))
				data = ""
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read sse: %v", err)
	}
	return out
}

func TestServerHTTPTransportSemantics(t *testing.T) {
	s := newTestServer()
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	rc := &rawClient{base: ts.URL, http: ts.Client()}

	// 缺 Accept 双类型 → 400
	req, _ := http.NewRequest(http.MethodPost, ts.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	req.Header.Set("Accept", "application/json")
	if resp, err := ts.Client().Do(req); err != nil || resp.StatusCode != 400 {
		t.Fatalf("bad accept: resp=%v err=%v", resp, err)
	}

	// 畸形帧 → 400
	if resp := rc.post(t, `{bad json`, false); resp.StatusCode != 400 {
		t.Fatalf("bad json status = %d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}

	// 未初始化先发请求：缺会话头 → 400
	if resp := rc.post(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, false); resp.StatusCode != 400 {
		t.Fatalf("missing session status = %d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}

	rc.initialize(t)

	// 未知会话 → 404
	req, _ = http.NewRequest(http.MethodPost, ts.URL, strings.NewReader(`{"jsonrpc":"2.0","id":3,"method":"ping"}`))
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Session-Id", "not-a-session")
	if resp, err := ts.Client().Do(req); err != nil || resp.StatusCode != 404 {
		t.Fatalf("unknown session: resp=%v err=%v", resp, err)
	}

	// GET 缺 Accept → 400；缺会话 → 400；未知 → 404
	get := func(accept, sess string) *http.Response {
		req, _ := http.NewRequest(http.MethodGet, ts.URL, nil)
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		if sess != "" {
			req.Header.Set("Mcp-Session-Id", sess)
		}
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		return resp
	}
	if resp := get("application/json", rc.sess); resp.StatusCode != 400 {
		t.Fatalf("get bad accept = %d", resp.StatusCode)
	}
	if resp := get("text/event-stream", ""); resp.StatusCode != 400 {
		t.Fatalf("get missing session = %d", resp.StatusCode)
	}
	if resp := get("text/event-stream", "not-a-session"); resp.StatusCode != 404 {
		t.Fatalf("get unknown session = %d", resp.StatusCode)
	}

	// Last-Event-ID：无回放存储 → 空流即收
	req, _ = http.NewRequest(http.MethodGet, ts.URL, nil)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Mcp-Session-Id", rc.sess)
	req.Header.Set("Last-Event-ID", "42")
	resp, err := ts.Client().Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("last-event-id get: %v %v", resp, err)
	}
	frames := readSSEFrames(t, resp.Body)
	if len(frames) != 0 {
		t.Fatalf("last-event-id 应空回放: %v", frames)
	}

	// 监听流：DELETE 注销后流断、stale POST → 404
	resp2, err := ts.Client().Do(req.Clone(context.Background()))
	if err != nil || resp2.StatusCode != 200 {
		t.Fatalf("listening get: %v %v", resp2, err)
	}
	streamDone := make(chan struct{})
	go func() { readSSEFrames(t, resp2.Body); close(streamDone) }()
	time.Sleep(100 * time.Millisecond) // 等监听流登记

	dreq, _ := http.NewRequest(http.MethodDelete, ts.URL, nil)
	dreq.Header.Set("Mcp-Session-Id", rc.sess)
	dresp, err := ts.Client().Do(dreq)
	if err != nil || dresp.StatusCode != 200 {
		t.Fatalf("delete: %v %v", dresp, err)
	}
	select {
	case <-streamDone:
	case <-time.After(3 * time.Second):
		t.Fatal("DELETE 后监听流未断")
	}
	if resp := rc.post(t, `{"jsonrpc":"2.0","id":9,"method":"ping"}`, true); resp.StatusCode != 404 {
		t.Fatalf("stale session = %d（应 404）", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
}

func TestServerSecurityValidator(t *testing.T) {
	s := newTestServer(mcp.WithSecurityValidator(func(r *http.Request) error {
		if r.Header.Get("Authorization") == "" {
			return errors.New("missing token")
		}
		return nil
	}))
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL, strings.NewReader(`{}`))
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if resp.StatusCode != 403 {
		t.Fatalf("status = %d（应 403）", resp.StatusCode)
	}
	resp.Body.Close()

	c := mcp.DialHTTP(ts.URL, mcp.WithHeaders(http.Header{"Authorization": []string{"Bearer x"}}))
	defer c.Close()
	if _, err := c.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize with token: %v", err)
	}
}

func TestServerHTTPReverseRequests(t *testing.T) {
	s := mcp.NewServer(mcp.Implementation{Name: "rev", Version: "1"})
	s.Tool("slow", "", func(cc *mcp.CallCtx, _ map[string]any) (*mcp.CallToolResult, error) {
		if err := cc.NotifyProgress(0.5, 1, "half way"); err != nil {
			return nil, err
		}
		res, err := cc.Sample(&mcp.CreateMessageRequest{
			Messages: []mcp.SamplingMessage{{
				Role:    mcp.RoleUser,
				Content: mcp.NewTextContent("translate: bonjour"),
			}},
			MaxTokens: 32,
		})
		if err != nil {
			return nil, err
		}
		tc, _ := res.Content.(*mcp.TextContent)
		return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent("model said: " + tc.Text)}}, nil
	})
	// 反向采样请求的观测：包装 handler 前记录（CallCtx 无请求拦截缝，用通道对齐即可）
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	rc := &rawClient{base: ts.URL, http: ts.Client()}
	rc.initialize(t) // capabilities 带 sampling

	resp := rc.post(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call",`+
		`"params":{"name":"slow","arguments":{}},"_meta":{"progressToken":"tok-1"}}`, true)
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("请求应回 SSE 流: %q", ct)
	}
	body := resp.Body
	defer body.Close()

	// 逐帧读：progress 通知 → sampling 反向请求（回执后）→ 最终响应
	rd := bufio.NewReader(body)
	next := func() json.RawMessage {
		var data string
		for {
			line, err := rd.ReadString('\n')
			if err != nil {
				t.Fatalf("read frame: %v", err)
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				if data != "" {
					return json.RawMessage(data)
				}
				continue
			}
			if strings.HasPrefix(line, "data:") {
				data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			}
		}
	}

	var frame struct {
		ID     *int64          `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		Result json.RawMessage `json:"result"`
	}

	if err := json.Unmarshal(next(), &frame); err != nil {
		t.Fatalf("frame1: %v", err)
	}
	if frame.Method != mcp.MethodNotificationProgress {
		t.Fatalf("frame1 = %s（应 progress 通知）", frame.Method)
	}
	var prog mcp.ProgressNotification
	_ = json.Unmarshal(frame.Params, &prog)
	if fmt.Sprint(prog.ProgressToken) != "tok-1" || prog.Progress != 0.5 {
		t.Fatalf("progress = %+v", prog)
	}

	if err := json.Unmarshal(next(), &frame); err != nil {
		t.Fatalf("frame2: %v", err)
	}
	if frame.Method != mcp.MethodSamplingCreateMessage || frame.ID == nil || *frame.ID != 1 {
		t.Fatalf("frame2 = %+v（应 sampling/createMessage 反向请求）", frame)
	}

	// 回执采样响应（POST Response 帧 → 202）
	respRsp := rc.post(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"role":"assistant",`+
		`"content":{"type":"text","text":"hello"},"model":"m"}}`, *frame.ID), true)
	if respRsp.StatusCode != 202 {
		t.Fatalf("sampling response status = %d（应 202）", respRsp.StatusCode)
	}
	respRsp.Body.Close()

	if err := json.Unmarshal(next(), &frame); err != nil {
		t.Fatalf("frame3: %v", err)
	}
	if frame.ID == nil || *frame.ID != 2 {
		t.Fatalf("frame3 = %+v（应 tools/call 响应）", frame)
	}
	var result mcp.CallToolResult
	if err := json.Unmarshal(frame.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if tc, ok := result.Content[0].(*mcp.TextContent); !ok || tc.Text != "model said: hello" {
		t.Fatalf("content = %+v", result.Content[0])
	}
}

// flakyGETRoundTripper 首个 GET 请求失败（注入监听流故障 → 会话失效 → 客户端
// 自愈重连）。
type flakyGETRoundTripper struct {
	mu       sync.Mutex
	failed   bool
	getFails int
	base     http.RoundTripper
}

func (f *flakyGETRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodGet {
		f.mu.Lock()
		if !f.failed {
			f.failed = true
			f.getFails++
			f.mu.Unlock()
			return nil, errors.New("get dropped")
		}
		f.mu.Unlock()
	}
	return f.base.RoundTrip(req)
}

func TestServerHTTPSelfHeal(t *testing.T) {
	var inits atomic.Int32
	s := newTestServer()
	h := s.Handler()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var probe struct {
				Method string `json:"method"`
			}
			body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			_ = json.Unmarshal(body, &probe)
			if probe.Method == mcp.MethodInitialize {
				inits.Add(1)
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		h.ServeHTTP(w, r)
	}))
	defer ts.Close()

	flaky := &flakyGETRoundTripper{base: http.DefaultTransport}
	c := mcp.DialHTTP(ts.URL, mcp.WithHTTPClient(&http.Client{Transport: flaky}))
	defer c.Close()

	if _, err := c.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	// 首个 GET 失败 → 传输终结（会话 Done）
	waitFor(t, "first GET failure", 3*time.Second, func() bool {
		flaky.mu.Lock()
		defer flaky.mu.Unlock()
		return flaky.getFails >= 1
	})
	// 下次操作自愈：重建传输 + 重新握手（第 2 次 initialize；其 GET 正常）
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping after stream loss: %v", err)
	}
	waitFor(t, "second initialize", 3*time.Second, func() bool { return inits.Load() >= 2 })
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping on healed session: %v", err)
	}
}

// ---- stdio 回环（TestMain 分身跑真实 Server）----

const stdioMcpServerEnv = "MCP_TEST_STDIO_MCP_SERVER"

func runRealStdioServer() {
	s := mcp.NewServer(mcp.Implementation{Name: "stdio-mcp", Version: "1.0"})
	s.Tool("echo", "", func(cc *mcp.CallCtx, args map[string]any) (*mcp.CallToolResult, error) {
		text, _ := args["text"].(string)
		return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent("echo:" + text)}}, nil
	})
	s.Resource("memo://readme", "readme", func(cc *mcp.CallCtx) (any, error) {
		return "hello memo", nil
	})
	s.Prompt("greet", []mcp.PromptArgument{{Name: "name"}},
		func(cc *mcp.CallCtx, args map[string]string) (any, error) {
			return "hi " + args["name"], nil
		})
	_ = s.RunStdio(context.Background())
	os.Exit(0)
}

func TestServerStdioLoop(t *testing.T) {
	tr := mcp.NewStdioTransport(os.Args[0]).StdioEnv(map[string]string{stdioMcpServerEnv: "1"})
	c := mcp.NewClient(tr.TransportFactory())
	defer c.Close()

	init, err := c.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if init.ServerInfo.Name != "stdio-mcp" {
		t.Fatalf("serverInfo = %+v", init.ServerInfo)
	}
	res, err := c.CallTool(context.Background(), "echo", map[string]any{"text": "io"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if tc, ok := res.Content[0].(*mcp.TextContent); !ok || tc.Text != "echo:io" {
		t.Fatalf("content = %+v", res.Content[0])
	}
	rr, err := c.ReadResource(context.Background(), "memo://readme")
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if tc, ok := rr.Contents[0].(*mcp.TextResourceContents); !ok || tc.Text != "hello memo" {
		t.Fatalf("contents = %+v", rr.Contents[0])
	}
	pr, err := c.GetPrompt(context.Background(), "greet", map[string]string{"name": "stdio"})
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}
	if tc, ok := pr.Messages[0].Content.(*mcp.TextContent); !ok || tc.Text != "hi stdio" {
		t.Fatalf("prompt = %+v", pr)
	}
}

// ---- RegisterTools 反射注册 ----

type calcService struct{ hits atomic.Int32 }

func (cs *calcService) Add(cc *mcp.CallCtx, args map[string]any) (any, error) {
	cs.hits.Add(1)
	a, _ := args["a"].(float64)
	b, _ := args["b"].(float64)
	return map[string]any{"sum": a + b}, nil
}

type greetArgs struct {
	Name string `json:"name"`
}

func (cs *calcService) Greet(cc *mcp.CallCtx, args *greetArgs) (any, error) {
	return "hello " + args.Name, nil
}

func (cs *calcService) NotATool() string { return "skipped" } // 形态不符 → 不注册

func TestRegisterTools(t *testing.T) {
	cs := &calcService{}
	s := mcp.NewServer(mcp.Implementation{Name: "reg", Version: "1"})
	if err := mcp.RegisterTools(s, cs); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	names := s.ToolNames()
	if len(names) != 2 || names[0] != "Add" || names[1] != "Greet" {
		t.Fatalf("tool names = %v（NotATool 应被跳过）", names)
	}
	if !s.HasTool("Add") || s.HasTool("NotATool") {
		t.Fatal("HasTool 判定错误")
	}

	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	c := mcp.DialHTTP(ts.URL)
	defer c.Close()
	if _, err := c.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// map 参数方法 → 结果 JSON 文本化
	res, err := c.CallTool(context.Background(), "Add", map[string]any{"a": 1, "b": 2})
	if err != nil {
		t.Fatalf("CallTool Add: %v", err)
	}
	if tc, ok := res.Content[0].(*mcp.TextContent); !ok || !strings.Contains(tc.Text, `"sum":3`) {
		t.Fatalf("Add content = %+v", res.Content[0])
	}
	// 结构体参数方法 → args JSON 反序列化注入
	res, err = c.CallTool(context.Background(), "Greet", map[string]any{"name": "go"})
	if err != nil {
		t.Fatalf("CallTool Greet: %v", err)
	}
	if tc, ok := res.Content[0].(*mcp.TextContent); !ok || tc.Text != "hello go" {
		t.Fatalf("Greet content = %+v", res.Content[0])
	}
	if cs.hits.Load() != 1 {
		t.Fatalf("hits = %d", cs.hits.Load())
	}
}
