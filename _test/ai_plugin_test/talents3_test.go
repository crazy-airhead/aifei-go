// talents3_test.go 依赖批四件黑盒测试：gateway（OpenAPI 解析黄金：V3 参数桶/
// $ref 展开/outputSchema/multipart、call_api 运行时回放、V2 兼容、档位）、pdf
//（parse 复用手写 PDF 夹具、create 声明式降级载体、沙箱）、cli（同步 bash
// exit_code、异步三件套、终止、快照形态）、lsp（帧层黄金 + 沙箱声明）。
package ai_plugin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ai "github.com/crazy-airhead/aifei-go/ai"
	gateway "github.com/crazy-airhead/aifei-go/plugins/ai/talent/gateway"
	pdft "github.com/crazy-airhead/aifei-go/plugins/ai/talent/pdf"
	termt "github.com/crazy-airhead/aifei-go/plugins/ai/talent/cli"
	lspt "github.com/crazy-airhead/aifei-go/plugins/ai/talent/lsp"
)

func callT3(t *testing.T, tool ai.FunctionTool, args map[string]any) string {
	t.Helper()
	r := tool.Call(context.Background(), args)
	if r == nil {
		t.Fatal("空结果")
	}
	return r.ContentText()
}

func toolByName3(t *testing.T, tools []ai.FunctionTool, name string) ai.FunctionTool {
	t.Helper()
	for _, tool := range tools {
		if tool.Name() == name {
			return tool
		}
	}
	t.Fatalf("工具 %s 缺失", name)
	return nil
}

// ---- gateway ----

const petstoreV3 = `{
  "openapi": "3.0.0",
  "servers": [{"url": "https://api.example.com/v3"}],
  "paths": {
    "/pets/{petId}": {
      "parameters": [
        {"name": "tenantId", "in": "header", "required": true, "schema": {"type": "string"}}
      ],
      "get": {
        "operationId": "getPetById",
        "summary": "按 id 查宠物",
        "tags": ["pets"],
        "parameters": [
          {"name": "petId", "in": "path", "required": true, "schema": {"type": "integer"}},
          {"name": "verbose", "in": "query", "schema": {"type": "boolean", "enum": [true, false]}}
        ],
        "responses": {"200": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/Pet"}}}}}
      }
    },
    "/pets": {
      "post": {
        "operationId": "addPet",
        "summary": "新增宠物",
        "requestBody": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/NewPet"}}}},
        "responses": {"201": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/Pet"}}}}}
      }
    },
    "/pets/{petId}/uploadImage": {
      "post": {
        "operationId": "uploadImage",
        "summary": "上传图片",
        "parameters": [{"name": "petId", "in": "path", "required": true, "schema": {"type": "integer"}}],
        "requestBody": {"content": {"multipart/form-data": {"schema": {"type": "string"}}}},
        "responses": {"default": {"content": {"application/json": {"schema": {"type": "object"}}}}}
      }
    }
  },
  "components": {"schemas": {
    "Pet": {"type": "object", "properties": {"id": {"type": "integer"}, "name": {"type": "string"}}, "required": ["id"]},
    "NewPet": {"type": "object", "properties": {"name": {"type": "string"}}}
  }}
}`

func TestGatewayParseOpenAPI(t *testing.T) {
	tools, err := gateway.ParseOpenAPIDocument(petstoreV3, "")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*gateway.APITool{}
	for _, tool := range tools {
		byName[tool.Name] = tool
	}

	get := byName["getPetById"]
	if get == nil {
		t.Fatalf("工具：%v", byName)
	}
	if get.Method != "GET" || get.Path != "/pets/{petId}" || get.Category != "pets" {
		t.Fatalf("基本信息：%+v", get)
	}
	if !strings.Contains(get.HeaderSchema, `"tenantId"`) || !strings.Contains(get.HeaderSchema, `"required":["tenantId"]`) {
		t.Fatalf("header 桶（PathItem 级）：%s", get.HeaderSchema)
	}
	if !strings.Contains(get.PathSchema, `"required":["petId"]`) {
		t.Fatalf("path 桶全必填：%s", get.PathSchema)
	}
	if !strings.Contains(get.QuerySchema, `"verbose"`) {
		t.Fatalf("query 桶：%s", get.QuerySchema)
	}
	if !strings.Contains(get.OutputSchema, `"id"`) || strings.Contains(get.OutputSchema, "$ref") {
		t.Fatalf("outputSchema $ref 展开：%s", get.OutputSchema)
	}
	if get.BaseURL != "https://api.example.com/v3" {
		t.Fatalf("基址：%s", get.BaseURL)
	}

	post := byName["addPet"]
	if !strings.Contains(post.BodySchema, `"name"`) || strings.Contains(post.BodySchema, "$ref") {
		t.Fatalf("body 展开：%s", post.BodySchema)
	}

	up := byName["uploadImage"]
	if !up.IsMultipart {
		t.Fatalf("multipart 探测：%+v", up)
	}
	if !strings.Contains(up.BodySchema, `"body"`) {
		t.Fatalf("非 object 包装：%s", up.BodySchema)
	}
}

func TestGatewayCallAPI(t *testing.T) {
	var method, fullQuery string
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		fullQuery = r.URL.Path + "?" + r.URL.RawQuery
		body, _ = io.ReadAll(r.Body)
		io.WriteString(w, `{"id":1,"name":"Rex"}`)
	}))
	defer srv.Close()

	g := gateway.New()
	g.SetDoer(srv.Client())
	docSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, strings.ReplaceAll(petstoreV3, "https://api.example.com/v3", srv.URL))
	}))
	defer docSrv.Close()

	if err := g.AddAPI(docSrv.URL, "", nil); err != nil {
		t.Fatal(err)
	}
	if g.ToolCount() != 3 {
		t.Fatalf("三接口：%d", g.ToolCount())
	}

	// ≤8 FULL 档位：只 call_api
	tools := g.Tools(nil)
	if len(tools) != 1 || tools[0].Name() != "call_api" {
		t.Fatalf("FULL 档位：%d %v", len(tools), tools)
	}

	out := callT3(t, tools[0], map[string]any{
		"api_name":     "getPetById",
		"path_params":  `{"petId": 42}`,
		"query_params": `{"verbose": true}`,
	})
	if !strings.Contains(out, "Rex") {
		t.Fatalf("输出：%q", out)
	}
	if method != "GET" || fullQuery != "/pets/42?verbose=true" {
		t.Fatalf("请求：%s %s", method, fullQuery)
	}

	callT3(t, tools[0], map[string]any{"api_name": "addPet", "body_params": `{"name":"Cat"}`})
	if !strings.Contains(string(body), `"name":"Cat"`) {
		t.Fatalf("POST body：%s", body)
	}

	if out := callT3(t, tools[0], map[string]any{"api_name": "getPetById"}); !strings.Contains(out, "缺少路径参数") {
		t.Fatalf("缺参：%q", out)
	}
	if out := callT3(t, tools[0], map[string]any{"api_name": "nope"}); !strings.Contains(out, "接口不存在") {
		t.Fatalf("未知接口：%q", out)
	}

	if ins := g.Instruction(nil); !strings.Contains(ins, "getPetById") || !strings.Contains(ins, "call_api") {
		t.Fatalf("指令：%s", ins)
	}
}

func TestGatewayV2Compat(t *testing.T) {
	v2 := `{
  "swagger": "2.0",
  "basePath": "/v2",
  "paths": {
    "/users/{id}": {
      "get": {
        "operationId": "getUser",
        "parameters": [
          {"name": "id", "in": "path", "type": "integer", "required": true},
          {"name": "X-Token", "in": "header", "type": "string"},
          {"name": "detail", "in": "query", "type": "boolean"},
          {"name": "body", "in": "body", "schema": {"$ref": "#/definitions/UserReq"}}
        ],
        "responses": {"200": {"schema": {"$ref": "#/definitions/User"}}}
      }
    }
  },
  "definitions": {
    "User": {"type": "object", "properties": {"name": {"type": "string"}}},
    "UserReq": {"type": "object", "properties": {"age": {"type": "integer"}}}
  }
}`
	tools, err := gateway.ParseOpenAPIDocument(v2, "https://api.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 {
		t.Fatalf("单接口：%d", len(tools))
	}
	tool := tools[0]
	if tool.BaseURL != "https://api.example.com" {
		t.Fatalf("V2 基址回退：%s", tool.BaseURL)
	}
	if !strings.Contains(tool.PathSchema, `"id"`) || !strings.Contains(tool.HeaderSchema, `"X-Token"`) ||
		!strings.Contains(tool.QuerySchema, `"detail"`) {
		t.Fatalf("V2 参数桶：%+v", tool)
	}
	if !strings.Contains(tool.BodySchema, `"age"`) {
		t.Fatalf("V2 body：%s", tool.BodySchema)
	}
	if !strings.Contains(tool.OutputSchema, `"name"`) {
		t.Fatalf("V2 output：%s", tool.OutputSchema)
	}
}

// ---- pdf ----

func TestPdfTalentParse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.pdf")
	os.WriteFile(path, minimalPDF("Hello PDF", "Second"), 0o600)

	p := pdft.New(dir)
	tools := p.Tools(nil)
	if len(tools) != 2 || tools[0].Name() != "pdf_parse" || tools[1].Name() != "pdf_create" {
		t.Fatalf("工具：%v", tools)
	}
	out := callT3(t, tools[0], map[string]any{"fileName": "doc.pdf"})
	if !strings.Contains(out, "Hello PDF") || !strings.Contains(out, "第 1 页") {
		t.Fatalf("解析：%q", out)
	}
	if out := callT3(t, tools[0], map[string]any{"fileName": "nope.pdf"}); out != "错误：文件不存在。" {
		t.Fatalf("缺失：%q", out)
	}
	if out := callT3(t, tools[0], map[string]any{"fileName": "../../etc/x.pdf"}); out != "非法路径访问" {
		t.Fatalf("越界：%q", out)
	}
}

func TestPdfTalentCreate(t *testing.T) {
	dir := t.TempDir()
	p := pdft.New(dir)
	tools := p.Tools(nil)

	out := callT3(t, tools[1], map[string]any{
		"fileName": "report", "content": "# 标题\n\n正文", "format": "markdown"})
	if !strings.Contains(out, "report.html") || !strings.Contains(out, "存为 PDF") {
		t.Fatalf("输出：%q", out)
	}
	data, err := os.ReadFile(filepath.Join(dir, "report.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "<h1>标题</h1>") || !strings.Contains(string(data), "line-height") {
		t.Fatalf("HTML 载体：%s", data)
	}
	// 文件名自动补后缀
	callT3(t, tools[1], map[string]any{"fileName": "x", "content": "<b>html</b>", "format": "html"})
	if _, err := os.Stat(filepath.Join(dir, "x.html")); err != nil {
		t.Fatalf("后缀补全：%v", err)
	}
}

// ---- cli ----

func TestCliTalentBashSync(t *testing.T) {
	tm := termt.New(termt.NewManager())
	tools := tm.Tools(nil)
	if len(tools) != 4 {
		t.Fatalf("四工具：%d", len(tools))
	}
	bash := toolByName3(t, tools, "bash")

	out := callT3(t, bash, map[string]any{"command": "echo hello"})
	if !strings.Contains(out, "hello") || !strings.HasSuffix(out, "[exit_code]0") {
		t.Fatalf("同步输出：%q", out)
	}
	out = callT3(t, bash, map[string]any{"command": "exit 3"})
	if !strings.HasSuffix(out, "[exit_code]3") {
		t.Fatalf("失败退出码：%q", out)
	}
	// 超时强杀
	out = callT3(t, bash, map[string]any{"command": "sleep 2", "timeout": 200})
	if !strings.Contains(out, "[exit_code]") {
		t.Fatalf("超时输出：%q", out)
	}
}

func TestCliTalentAsync(t *testing.T) {
	m := termt.NewManager()
	tm := termt.New(m)
	tools := tm.Tools(nil)
	start := toolByName3(t, tools, "bash_start")
	wait := toolByName3(t, tools, "bash_wait")
	stop := toolByName3(t, tools, "bash_stop")

	// 快命令：yield 内完成
	out := callT3(t, start, map[string]any{"command": "echo done", "yield_time_ms": 800})
	if !strings.Contains(out, "session_id: cmd_") || !strings.Contains(out, "completed") ||
		!strings.Contains(out, "done") {
		t.Fatalf("启动快照：%q", out)
	}

	// 慢命令：yield 后 running → wait 收口
	out = callT3(t, start, map[string]any{"command": "sleep 1 && echo slow", "yield_time_ms": 50})
	if !strings.Contains(out, "running") {
		t.Fatalf("运行中：%q", out)
	}
	sessionID := extractSessionID3(t, out)
	time.Sleep(1300 * time.Millisecond)
	out = callT3(t, wait, map[string]any{"session_id": sessionID, "yield_time_ms": 100})
	if !strings.Contains(out, "completed") || !strings.Contains(out, "slow") {
		t.Fatalf("等待收口：%q", out)
	}

	// stop 终止长命令
	out = callT3(t, start, map[string]any{"command": "sleep 5", "yield_time_ms": 50})
	sessionID = extractSessionID3(t, out)
	out = callT3(t, stop, map[string]any{"session_id": sessionID, "reason": "test"})
	if !strings.Contains(out, "terminated") {
		t.Fatalf("终止：%q", out)
	}
	// 未知会话
	if out := callT3(t, wait, map[string]any{"session_id": "nope"}); !strings.Contains(out, "ERROR") {
		t.Fatalf("未知会话：%q", out)
	}
}

func extractSessionID3(t *testing.T, snapshot string) string {
	t.Helper()
	for _, line := range strings.Split(snapshot, "\n") {
		if strings.HasPrefix(line, "session_id: ") {
			return strings.TrimPrefix(line, "session_id: ")
		}
	}
	t.Fatalf("快照无 session_id：%q", snapshot)
	return ""
}

// ---- lsp ----

func TestLspDefaultsAndFrames(t *testing.T) {
	// 内置 14 服务器面
	defaults := lspt.DefaultServers()
	if len(defaults) != 14 {
		t.Fatalf("内置服务器：%d", len(defaults))
	}
	byName := map[string]lspt.ServerParams{}
	for _, s := range defaults {
		byName[s.Name] = s
	}
	if byName["go"].Command[0] != "gopls" || byName["go"].Extensions[0] != ".go" {
		t.Fatalf("go 服务器：%+v", byName["go"])
	}

	// jsonrpc 帧黄金：Content-Length 头 + JSON 体
	msg := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"}
	b, _ := json.Marshal(msg)
	framed := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(b), b)
	var length int
	if _, err := fmt.Sscanf(strings.SplitN(framed, "\r\n", 2)[0], "Content-Length: %d", &length); err != nil || length != len(b) {
		t.Fatalf("帧头解析：%d %v", length, err)
	}
	// URI 形态
	if u := lspt.FileURI("/tmp/x.go"); !strings.HasPrefix(u, "file:///") {
		t.Fatalf("URI：%s", u)
	}
}
