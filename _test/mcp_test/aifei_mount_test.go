// aifei_mount_test.go P3-c 验收项：aifei 挂载互操作——MCP 服务端经
// Middleware 嵌入 server.IoHandler 裸通道（server.WithHTTPHandler 同款缝），
// mcp 客户端 ↔ aifei 服务端全流程，aifei 自身路由不受影响。
package mcp_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/crazy-airhead/aifei-go/aifei"
	"github.com/crazy-airhead/aifei-go/mcp"
	"github.com/crazy-airhead/aifei-go/server"
)

func TestServerAifeiMount(t *testing.T) {
	app := aifei.New()
	app.GET("/hello", func(in aifei.Input) aifei.Output {
		return server.Of(map[string]any{"ok": true})
	})

	mcps := mcp.NewServer(mcp.Implementation{Name: "mounted", Version: "1.0"})
	mcps.Tool("echo", "", func(cc *mcp.CallCtx, args map[string]any) (*mcp.CallToolResult, error) {
		text, _ := args["text"].(string)
		return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent("echo:" + text)}}, nil
	})

	io := server.NewIoHandler(app)
	ts := httptest.NewServer(mcps.Middleware("/mcp")(io))
	defer ts.Close()

	// aifei 自身路由照常（JSON 信封）
	resp, err := ts.Client().Get(ts.URL + "/hello")
	if err != nil {
		t.Fatalf("GET /hello: %v", err)
	}
	defer resp.Body.Close()
	var envelope map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope["code"] != float64(server.CodeOK) {
		t.Fatalf("envelope = %+v", envelope)
	}

	// MCP 端点全流程
	c := mcp.DialHTTP(ts.URL + "/mcp")
	defer c.Close()
	init, err := c.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if init.ServerInfo.Name != "mounted" {
		t.Fatalf("serverInfo = %+v", init.ServerInfo)
	}
	if _, err := c.ListAllTools(context.Background()); err != nil {
		t.Fatalf("ListAllTools: %v", err)
	}
	res, err := c.CallTool(context.Background(), "echo", map[string]any{"text": "mount"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if tc, ok := res.Content[0].(*mcp.TextContent); !ok || !strings.Contains(tc.Text, "echo:mount") {
		t.Fatalf("content = %+v", res.Content[0])
	}
	if n := mcps.SessionCount(); n != 1 {
		t.Fatalf("session count = %d", n)
	}
}
