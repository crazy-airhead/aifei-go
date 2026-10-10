// client_test.go P3-b 客户端门面黑盒测试：自二进制 stdio 回环（握手→分页
// 列表→调用→ping→变更通知重拉）、版本协商失败、请求超时、能力缺失门禁、
// 心跳指数退避（假传输注入故障，按工厂调用时间戳断言几何增长）。
package mcp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/crazy-airhead/aifei-go/mcp"
)

// ---- 自二进制 stdio 迷你服务（TestMain 分身）----

const stdioServerEnv = "MCP_TEST_STDIO_SERVER"

func TestMain(m *testing.M) {
	if os.Getenv(stdioServerEnv) == "1" {
		runMiniStdioServer()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runMiniStdioServer 行帧 JSON-RPC 迷你服务：initialize（tools 能力）→
// tools/list 两代——第 0 代 echo/add 分页（nextCursor=p2），ping 后升级第 1 代
// 并推送 tools/list_changed（echo/add/mul 单页）→ tools/call 回 "hi:<name>"。
func runMiniStdioServer() {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	gen := 0
	tool := func(name, desc string) map[string]any {
		return map[string]any{
			"name": name, "description": desc,
			"inputSchema": map[string]any{"type": "object"},
		}
	}
	respond := func(id json.RawMessage, result any) {
		b, _ := json.Marshal(result)
		fmt.Fprintf(out, `{"jsonrpc":"2.0","id":%s,"result":%s}`+"\n", id, b)
		out.Flush()
	}

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		switch req.Method {
		case "initialize":
			respond(req.ID, map[string]any{
				"protocolVersion": mcp.Version20250618,
				"capabilities":    map[string]any{"tools": map[string]any{"listChanged": true}},
				"serverInfo":      map[string]any{"name": "mini-mcp", "version": "0.1"},
			})
		case "notifications/initialized":
			// 通知无响应
		case "ping":
			respond(req.ID, map[string]any{})
			if gen == 0 { // ping 后推一次工具变更：升代 + 通知
				gen = 1
				fmt.Fprintln(out, `{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`)
				out.Flush()
			}
		case "tools/list":
			var p struct {
				Cursor string `json:"cursor"`
			}
			_ = json.Unmarshal(req.Params, &p)
			if gen == 0 && p.Cursor == "" {
				respond(req.ID, map[string]any{"tools": []any{tool("echo", "回声")}, "nextCursor": "p2"})
			} else if gen == 0 {
				respond(req.ID, map[string]any{"tools": []any{tool("add", "加法")}})
			} else {
				respond(req.ID, map[string]any{"tools": []any{
					tool("echo", "回声"), tool("add", "加法"), tool("mul", "乘法"),
				}})
			}
		case "tools/call":
			var p struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(req.Params, &p)
			respond(req.ID, map[string]any{
				"content": []any{map[string]any{"type": "text", "text": "hi:" + p.Name}},
			})
		}
	}
}

func TestStdioLoopback(t *testing.T) {
	toolsChanged := make(chan []mcp.Tool, 4)
	tr := mcp.NewStdioTransport(os.Args[0]).StdioEnv(map[string]string{stdioServerEnv: "1"})
	c := mcp.NewClient(tr.TransportFactory(),
		mcp.WithToolsChangeConsumer(func(tools []mcp.Tool) { toolsChanged <- tools }))
	defer c.Close()

	// 握手（懒初始化）：服务端回落 2025-06-18，须落在客户端支持表内
	init, err := c.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if init.ServerInfo.Name != "mini-mcp" {
		t.Fatalf("serverInfo.name = %q", init.ServerInfo.Name)
	}
	if v := c.NegotiatedVersion(); v != mcp.Version20250618 {
		t.Fatalf("negotiated = %q", v)
	}
	if c.ServerCapabilities().Tools == nil {
		t.Fatal("server tools capability missing")
	}

	// 全量列表：跟随 nextCursor 翻页（echo@p1 + add@p2）
	tools, err := c.ListAllTools(context.Background())
	if err != nil {
		t.Fatalf("ListAllTools: %v", err)
	}
	if len(tools) != 2 || tools[0].Name != "echo" || tools[1].Name != "add" {
		t.Fatalf("tools = %+v", tools)
	}

	// 调用：文本内容回显
	res, err := c.CallTool(context.Background(), "echo", map[string]any{"text": "x"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if tc, ok := res.Content[0].(*mcp.TextContent); !ok || tc.Text != "hi:echo" {
		t.Fatalf("content = %+v", res.Content[0])
	}

	// ping：变更通知驱动自动重拉 + 消费者回调（第 1 代 3 工具单页）
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	select {
	case got := <-toolsChanged:
		if len(got) != 3 || got[2].Name != "mul" {
			t.Fatalf("changed tools = %+v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("tools/list_changed consumer not fired")
	}

	// 会话死亡自愈：Reset 后下次操作重建（重新拉起进程）
	c.Reset()
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping after Reset: %v", err)
	}
}

// ---- 假传输上的门面行为 ----

// brokenTransport 发送恒失败（注入传输故障；心跳退避用）。
type brokenTransport struct {
	recv chan mcp.Message
	once sync.Once
}

func (b *brokenTransport) Send(_ context.Context, _ mcp.Message) error {
	return &mcp.TransportError{Err: errors.New("broken pipe")}
}
func (b *brokenTransport) Receive() <-chan mcp.Message { return b.recv }
func (b *brokenTransport) Close() error {
	b.once.Do(func() { close(b.recv) })
	return nil
}

func TestClientNegotiationFailure(t *testing.T) {
	ft := newFakeTransport()
	c := mcp.NewClient(func(context.Context) (mcp.Transport, error) { return ft, nil })
	defer c.Close()

	go func() { // 回落不支持的版本 → 协商失败
		req := (<-ft.sent).(*mcp.JSONRPCRequest)
		if req.Method != mcp.MethodInitialize {
			t.Errorf("首帧 = %s", req.Method)
		}
		feed(t, ft, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"1999-01-01","capabilities":{},"serverInfo":{"name":"x","version":"1"}}}`)
	}()
	_, err := c.Initialize(context.Background())
	if err == nil {
		t.Fatal("Initialize 应因版本协商失败报错")
	}
	var rpcErr *mcp.JSONRPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != mcp.CodeInvalidParams {
		t.Fatalf("err = %v（应 -32602）", err)
	}
}

func TestClientRequestTimeout(t *testing.T) {
	ft := newFakeTransport()
	c := mcp.NewClient(func(context.Context) (mcp.Transport, error) { return ft, nil },
		mcp.WithRequestTimeout(120*time.Millisecond))
	defer c.Close()

	go func() { // 握手正常应答
		<-ft.sent
		feed(t, ft, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"x","version":"1"}}}`)
	}()
	_, err := c.CallTool(context.Background(), "t", nil) // tools/call 永不响应 → 超时
	if err == nil {
		t.Fatal("CallTool 应超时")
	}
	if !mcp.IsTransportError(err) {
		t.Fatalf("err = %v（超时应判定为传输错误）", err)
	}
}

func TestClientCapabilityGate(t *testing.T) {
	ft := newFakeTransport()
	c := mcp.NewClient(func(context.Context) (mcp.Transport, error) { return ft, nil })
	defer c.Close()

	go func() {
		<-ft.sent
		feed(t, ft, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","capabilities":{},"serverInfo":{"name":"x","version":"1"}}}`)
	}()
	_, err := c.ListTools(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "tools capability") {
		t.Fatalf("err = %v（应报能力缺失）", err)
	}
}

func TestHeartbeatBackoff(t *testing.T) {
	var mu sync.Mutex
	var calls []time.Time
	factory := func(context.Context) (mcp.Transport, error) {
		mu.Lock()
		calls = append(calls, time.Now())
		mu.Unlock()
		return &brokenTransport{recv: make(chan mcp.Message)}, nil
	}
	c := mcp.NewClient(factory, mcp.WithHeartbeat(60*time.Millisecond),
		mcp.WithRequestTimeout(300*time.Millisecond))

	// 首次操作即失败（传输故障）→ 心跳接手：60ms → 120ms → 240ms（几何退避）
	if err := c.Ping(context.Background()); err == nil {
		t.Fatal("Ping 应失败")
	}
	deadline := time.After(4 * time.Second)
	for {
		mu.Lock()
		n := len(calls)
		mu.Unlock()
		if n >= 4 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("工厂仅调用 %d 次（退避未推进）", n)
		case <-time.After(10 * time.Millisecond):
		}
	}
	c.Close() // 停心跳

	mu.Lock()
	defer mu.Unlock()
	d1 := calls[1].Sub(calls[0])
	d2 := calls[2].Sub(calls[1])
	d3 := calls[3].Sub(calls[2])
	if d2 < d1*3/2 || d3 < d2*3/2 {
		t.Fatalf("退避未几何增长: d1=%v d2=%v d3=%v", d1, d2, d3)
	}
	if d3 > d1*8 {
		t.Fatalf("退避发散: d1=%v d3=%v", d1, d3)
	}
	// Close 后心跳停止：不再有新工厂调用
	time.Sleep(150 * time.Millisecond)
	if n := len(calls); n > 4 {
		t.Fatalf("Close 后仍有 %d 次工厂调用", n)
	}
}
