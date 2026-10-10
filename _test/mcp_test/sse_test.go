// sse_test.go 旧式 SSE（2024-11-05）双端回环：GET 流建会话 + endpoint 事件 →
// 报文端点 POST 收发 → 变更广播；未知会话 POST → 404。
package mcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/crazy-airhead/aifei-go/mcp"
)

func TestOldSSELoopback(t *testing.T) {
	s := newTestServer()
	ts := httptest.NewServer(s.SSEHandler("/sse", "/mcp"))
	defer ts.Close()

	changed := make(chan []mcp.Tool, 4)
	c := mcp.DialSSE(ts.URL+"/sse", mcp.WithToolsChangeConsumer(func(tools []mcp.Tool) {
		changed <- tools
	}))
	defer c.Close()

	init, err := c.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if init.ServerInfo.Name != "srv" || c.NegotiatedVersion() != mcp.Version20251125 {
		t.Fatalf("init = %+v negotiated=%q", init.ServerInfo, c.NegotiatedVersion())
	}

	tools, err := c.ListAllTools(context.Background())
	if err != nil {
		t.Fatalf("ListAllTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("tools = %+v", tools)
	}

	res, err := c.CallTool(context.Background(), "echo", map[string]any{"text": "old"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if tc, ok := res.Content[0].(*mcp.TextContent); !ok || tc.Text != "echo:old" {
		t.Fatalf("content = %+v", res.Content[0])
	}

	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if n := s.SessionCount(); n != 1 {
		t.Fatalf("session count = %d", n)
	}

	// 广播走 GET 流：以公开 Notify* 重试收敛
	waitFor(t, "tools list_changed over sse", 5*time.Second, func() bool {
		s.NotifyToolsListChanged()
		select {
		case <-changed:
			return true
		default:
			return false
		}
	})

	// Close → GET 流断 → 服务端注销会话
	c.Close()
	waitFor(t, "session removed", 3*time.Second, func() bool { return s.SessionCount() == 0 })
}

func TestOldSSEUnknownSession(t *testing.T) {
	s := mcp.NewServer(mcp.Implementation{Name: "sse404", Version: "1"})
	ts := httptest.NewServer(s.SSEHandler("/sse", "/mcp"))
	defer ts.Close()

	// 未经 GET 建会话直接 POST → 404
	resp, err := ts.Client().Post(ts.URL+"/mcp", "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d（应 404）", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(body["error"].(map[string]any)["message"].(string), "Session not found") {
		t.Fatalf("body = %+v", body)
	}
}
