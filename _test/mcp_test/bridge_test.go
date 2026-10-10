// bridge_test.go P3-b ai/mcp 桥黑盒测试：工具映射（schema/returnDirect/meta）、
// 白/黑名单、游标缓存 TTL、IsError 前缀与 Content 块映射、传输错误重连重试、
// 假 OpenAI 函数调用循环（验收）。
package mcp_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	ai "github.com/crazy-airhead/aifei-go/ai"
	_ "github.com/crazy-airhead/aifei-go/ai/dialect/openai" // 注册默认方言
	aimcp "github.com/crazy-airhead/aifei-go/ai/mcp"
	"github.com/crazy-airhead/aifei-go/mcp"
)

// ---- 脚本化 MCP 服务（fakeTransport 之上的自动应答器）----

// scriptedMCP 按方法自动应答（toolsList/callTool 可换脚本；callTool 返回空串
// = 吞帧不答，模拟无响应超时）。每次重连换新 fakeTransport，共用同一应答器
// 与计数器。
type scriptedMCP struct {
	respFn func(ft *fakeTransport)

	mu        sync.Mutex
	counts    map[string]int
	factories int
	toolsList string
}

func newScriptedMCP(t *testing.T, toolsList string, callTool func(name string, call int) string) *scriptedMCP {
	t.Helper()
	s := &scriptedMCP{
		counts:    map[string]int{},
		toolsList: toolsList,
	}
	s.respFn = func(ft *fakeTransport) {
		for m := range ft.sent {
			switch msg := m.(type) {
			case *mcp.JSONRPCRequest:
				s.mu.Lock()
				s.counts[msg.Method]++
				call := s.counts[msg.Method]
				toolsList := s.toolsList
				s.mu.Unlock()

				id, _ := json.Marshal(msg.ID)
				var result string
				switch msg.Method {
				case "initialize":
					result = `{"protocolVersion":"2025-06-18","capabilities":{"tools":{"listChanged":true}},"serverInfo":{"name":"s","version":"1"}}`
				case "tools/list":
					result = toolsList
				case "tools/call":
					var p struct {
						Name string `json:"name"`
					}
					_ = json.Unmarshal(msg.Params, &p)
					result = callTool(p.Name, call)
				case "ping":
					result = `{}`
				default:
					continue
				}
				if result == "" {
					continue // 吞帧：模拟无响应（超时路径）
				}
				feed(t, ft, fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":%s}`, id, result))
			case *mcp.JSONRPCNotification:
				s.mu.Lock()
				s.counts[msg.Method]++
				s.mu.Unlock()
			}
		}
	}
	return s
}

// client 基于本应答器建 mcp.Client（每次初始化/重连换新传输）。
func (s *scriptedMCP) client(opts ...mcp.ClientOption) *mcp.Client {
	return mcp.NewClient(func(context.Context) (mcp.Transport, error) {
		s.mu.Lock()
		s.factories++
		s.mu.Unlock()
		ft := newFakeTransport()
		go s.respFn(ft)
		return ft, nil
	}, opts...)
}

func (s *scriptedMCP) count(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts[method]
}

// ---- 工具映射与白/黑名单 ----

const bridgeToolsList = `{"tools":[
	{"name":"echo","title":"回声","description":"回显文本",
	 "inputSchema":{"type":"object","properties":{"text":{"type":"string","description":"文本"}},"required":["text"]},
	 "outputSchema":{"type":"object"},
	 "annotations":{"returnDirect":true},
	 "_meta":{"src":"mcp"}},
	{"name":"secret","description":"机密","inputSchema":{"type":"object"}},
	{"name":"hidden","description":"隐藏","inputSchema":{"type":"object"}}
]}`

func TestBridgeToolsMapping(t *testing.T) {
	s := newScriptedMCP(t, bridgeToolsList, func(string, int) string { return `{}` })
	c := s.client()
	defer c.Close()
	p := aimcp.NewProvider(c, aimcp.WithAllowed("echo"))

	tools := p.Tools()
	if len(tools) != 1 {
		t.Fatalf("tools = %d（白名单应只剩 echo）", len(tools))
	}
	tt := tools[0]
	if tt.Name() != "echo" || tt.Title() != "回声" || tt.Description() != "回显文本" {
		t.Fatalf("映射: name=%q title=%q desc=%q", tt.Name(), tt.Title(), tt.Description())
	}
	if !tt.ReturnDirect() {
		t.Fatal("returnDirect 注记未映射")
	}
	schema := tt.InputSchema()
	for _, want := range []string{`"type":"object"`, `"required":["text"]`, `"text"`} {
		if !strings.Contains(schema, want) {
			t.Fatalf("InputSchema = %s（缺 %s）", schema, want)
		}
	}
	if got := tt.OutputSchema(); got != `{"type":"object"}` {
		t.Fatalf("OutputSchema = %s", got)
	}
	if tt.Meta()["src"] != "mcp" {
		t.Fatalf("meta = %v", tt.Meta())
	}
}

func TestBridgeAllowedDisallowed(t *testing.T) {
	s := newScriptedMCP(t, bridgeToolsList, func(string, int) string { return `{}` })

	names := func(p *aimcp.Provider) []string {
		var out []string
		for _, tt := range p.Tools() {
			out = append(out, tt.Name())
		}
		return out
	}

	// 空白/黑名单 = 全量
	c1 := s.client()
	defer c1.Close()
	if got := names(aimcp.NewProvider(c1)); len(got) != 3 {
		t.Fatalf("全量 = %v", got)
	}

	// 黑名单剔除
	c2 := s.client()
	defer c2.Close()
	got := names(aimcp.NewProvider(c2, aimcp.WithDisallowed("secret")))
	if len(got) != 2 || got[0] != "echo" || got[1] != "hidden" {
		t.Fatalf("黑名单 = %v", got)
	}

	// 白名单收窄 + 黑名单再剔除
	c3 := s.client()
	defer c3.Close()
	got = names(aimcp.NewProvider(c3, aimcp.WithAllowed("echo", "hidden"), aimcp.WithDisallowed("hidden")))
	if len(got) != 1 || got[0] != "echo" {
		t.Fatalf("白+黑 = %v", got)
	}
}

// ---- 游标缓存 ----

func TestBridgeCacheTTL(t *testing.T) {
	s := newScriptedMCP(t, bridgeToolsList, func(string, int) string { return `{}` })
	c := s.client()
	defer c.Close()
	p := aimcp.NewProvider(c, aimcp.WithAllowed("echo"), aimcp.WithCacheTTL(150*time.Millisecond))

	if got := p.Tools(); len(got) != 1 {
		t.Fatalf("首次 Tools = %d", len(got))
	}
	if got := p.Tools(); len(got) != 1 {
		t.Fatalf("缓存命中 Tools = %d", len(got))
	}
	if n := s.count("tools/list"); n != 1 {
		t.Fatalf("TTL 内 tools/list = %d（应 1）", n)
	}

	time.Sleep(200 * time.Millisecond) // 过期
	if got := p.Tools(); len(got) != 1 {
		t.Fatalf("过期后 Tools = %d", len(got))
	}
	if n := s.count("tools/list"); n != 2 {
		t.Fatalf("过期后 tools/list = %d（应 2）", n)
	}
}

// ---- CallTool 映射 ----

func TestBridgeCallToolIsErrorPrefix(t *testing.T) {
	s := newScriptedMCP(t, bridgeToolsList, func(name string, call int) string {
		switch call {
		case 1:
			return `{"content":[{"type":"text","text":"boom"}],"isError":true}`
		case 2:
			return `{"content":[{"type":"text","text":"Error: already"}],"isError":true}`
		default:
			return `{"content":[{"type":"text","text":"ok"}]}`
		}
	})
	c := s.client()
	defer c.Close()
	p := aimcp.NewProvider(c)

	res, err := p.CallTool(context.Background(), "echo", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError || res.ContentText() != "Error: boom" {
		t.Fatalf("res = %+v（应加 Error: 前缀）", res)
	}

	res, err = p.CallTool(context.Background(), "echo", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.ContentText() != "Error: already" {
		t.Fatalf("res = %q（已有前缀不得重复）", res.ContentText())
	}
}

func TestBridgeContentMapping(t *testing.T) {
	imgB64 := base64.StdEncoding.EncodeToString([]byte{1, 2, 3})
	s := newScriptedMCP(t, bridgeToolsList, func(string, int) string {
		return fmt.Sprintf(`{"content":[
			{"type":"text","text":"说明"},
			{"type":"image","data":%[1]q,"mimeType":"image/png"},
			{"type":"image","data":"https://x/img.png"},
			{"type":"audio","data":"https://x/a.mp3"},
			{"type":"resource_link","name":"cfg","uri":"file:///etc/cfg"},
			{"type":"resource","resource":{"uri":"file:///a.txt","mimeType":"text/plain","text":"内文"}}
		]}`, imgB64)
	})
	c := s.client()
	defer c.Close()
	p := aimcp.NewProvider(c)

	res, err := p.CallTool(context.Background(), "echo", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(res.Blocks) != 6 {
		t.Fatalf("blocks = %d", len(res.Blocks))
	}
	if tb, ok := res.Blocks[0].(ai.TextBlock); !ok || tb.Text != "说明" {
		t.Fatalf("b0 = %#v", res.Blocks[0])
	}
	ib, ok := res.Blocks[1].(ai.ImageBlock)
	if !ok || len(ib.Data) != 3 || ib.Mime != "image/png" || ib.URL != "" {
		t.Fatalf("b1 = %#v（base64 应解码）", res.Blocks[1])
	}
	ib, ok = res.Blocks[2].(ai.ImageBlock)
	if !ok || ib.URL != "https://x/img.png" || len(ib.Data) != 0 {
		t.Fatalf("b2 = %#v（URL 形态）", res.Blocks[2])
	}
	if ab, ok := res.Blocks[3].(ai.AudioBlock); !ok || ab.URL != "https://x/a.mp3" {
		t.Fatalf("b3 = %#v", res.Blocks[3])
	}
	if rl, ok := res.Blocks[4].(*ai.ResourceLink); !ok || rl.Name != "cfg" || rl.URI != "file:///etc/cfg" {
		t.Fatalf("b4 = %#v", res.Blocks[4])
	}
	if rl, ok := res.Blocks[5].(*ai.ResourceLink); !ok || rl.URI != "file:///a.txt" || rl.Mime != "text/plain" {
		t.Fatalf("b5 = %#v（EmbeddedResource → ResourceLink）", res.Blocks[5])
	}
}

// ---- 传输错误重连重试 ----

func TestBridgeRetryOnTransportError(t *testing.T) {
	s := newScriptedMCP(t, bridgeToolsList, func(name string, call int) string {
		if call == 1 {
			return "" // 首次吞帧 → 超时（传输错误）
		}
		return `{"content":[{"type":"text","text":"recovered"}]}`
	})
	c := s.client(mcp.WithRequestTimeout(150 * time.Millisecond))
	defer c.Close()
	p := aimcp.NewProvider(c)

	res, err := p.CallTool(context.Background(), "echo", nil)
	if err != nil {
		t.Fatalf("CallTool: %v（应重连重试成功）", err)
	}
	if res.ContentText() != "recovered" {
		t.Fatalf("res = %q", res.ContentText())
	}
	if n := s.count("tools/call"); n != 2 {
		t.Fatalf("tools/call = %d（应重试 1 次共 2）", n)
	}
}

// ---- 验收：假函数调用循环 ----

func TestBridgeFunctionCallingLoop(t *testing.T) {
	weatherTool := `{"tools":[{"name":"weather","description":"查天气","inputSchema":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}]}`
	s := newScriptedMCP(t, weatherTool, func(string, int) string {
		return `{"content":[{"type":"text","text":"晴，25度"}]}`
	})
	c := s.client()
	defer c.Close()
	p := aimcp.NewProvider(c)

	// 假 OpenAI：第 1 轮回 tool_calls，第 2 轮收 tool 结果后回终答
	var requests []map[string]any
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		requests = append(requests, body)
		i := len(requests) - 1
		mu.Unlock()

		if i == 0 {
			fmt.Fprint(w, `{"id":"c1","model":"gpt-4o","choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","index":0,"function":{"name":"weather","arguments":"{\"city\":\"北京\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
			return
		}
		fmt.Fprint(w, `{"id":"c2","model":"gpt-4o","choices":[{"message":{"role":"assistant","content":"北京：晴，25度"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":4,"total_tokens":9}}`)
	}))
	defer srv.Close()

	m, err := ai.NewChatModel(ai.Config{APIURL: srv.URL, Model: "gpt-4o", APIKey: "sk-test"})
	if err != nil {
		t.Fatalf("NewChatModel: %v", err)
	}
	auto := true
	resp, err := m.Prompt("北京天气？").Options(func(o *ai.ChatOptions) {
		o.AutoToolCall = &auto
		o.ToolAdd(p)
	}).Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.Text() != "北京：晴，25度" {
		t.Fatalf("text = %q", resp.Text())
	}

	// 第 2 轮请求须携带 role=tool 的 MCP 工具结果
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("chat 请求数 = %d", len(requests))
	}
	raw, _ := json.Marshal(requests[1]["messages"])
	if !strings.Contains(string(raw), `"晴，25度"`) || !strings.Contains(string(raw), `"weather"`) {
		t.Fatalf("第 2 轮消息缺工具结果: %s", raw)
	}
	// 首轮请求须注入 MCP 工具 schema
	raw0, _ := json.Marshal(requests[0]["tools"])
	if !strings.Contains(string(raw0), `"weather"`) {
		t.Fatalf("首轮 tools 缺 weather: %s", raw0)
	}
}
