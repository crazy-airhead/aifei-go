// plugin_test.go P4-d 配置装配插件测试：app.yml 样例装配出可用 ChatModel
// （demo 场景 config 驱动一问一答）、缺省/多模型/环境变量覆盖（对照
// storage/kafka 插件测试式样：全局 config.SetProps + LoadYAMLBytes/
// LoadYAML）、embedding 段、agent 段（系统提示词 + 未知模型引用错误面）、
// mcp 客户端段（真 MCP 服务端 httptest 回环 + 白名单 + 附加头）、可选
// server 段（Handler() 挂载互操作）、stub doer 注入缝、Stop 清场。
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
	"sync"
	"testing"

	"github.com/crazy-airhead/aifei-go/config"
	"github.com/crazy-airhead/aifei-go/mcp"

	ai "github.com/crazy-airhead/aifei-go/plugins/ai"
)

// ---- 基座 ----

// capturedReq 一次请求快照。
type capturedReq struct {
	method string
	path   string
	header http.Header
	body   map[string]any
}

// fakeLLM 假 OpenAI 服务：捕获请求并回放固定 chat completion。
type fakeLLM struct {
	mu       sync.Mutex
	requests []capturedReq
	srv      *httptest.Server
}

func newFakeLLM(t *testing.T, reply string) *fakeLLM {
	t.Helper()
	f := &fakeLLM{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.requests = append(f.requests, capturedReq{
			method: r.Method, path: r.URL.Path, header: r.Header.Clone(), body: body,
		})
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(
			`{"choices":[{"message":{"role":"assistant","content":%q}}],"usage":{"total_tokens":7}}`, reply)))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeLLM) last() capturedReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[len(f.requests)-1]
}

func (f *fakeLLM) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

// setGlobalYAML 注入全局配置并注册清场（storage_test saveGlobal 式样）。
func setGlobalYAML(t *testing.T, yaml string) {
	t.Helper()
	props := config.NewProps()
	if err := props.LoadYAMLBytes([]byte(yaml)); err != nil {
		t.Fatalf("LoadYAMLBytes: %v", err)
	}
	config.SetProps(props)
	t.Cleanup(func() { config.SetProps(config.NewProps()) })
}

// startPlugin 装配 + 注册 Stop 清场（Stop 清包级默认防跨测试污染）。
func startPlugin(t *testing.T) *ai.Plugin {
	t.Helper()
	p, err := ai.NewPlugin(nil)
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}
	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = p.Stop() })
	return p
}

// ---- demo 场景：app.yml 样例装配 + config 驱动一问一答 ----

func TestPluginDemoConfigDrivenQA(t *testing.T) {
	f := newFakeLLM(t, "你好，我是配置装配的模型。")

	// 真实 app.yml 样例文件（含全部五段；mcp/server 段留待各自测试）
	dir := t.TempDir()
	appYML := fmt.Sprintf(`
ai:
  default: gpt4o
  models:
    gpt4o:
      provider: openai
      apiUrl: %s
      apiKey: sk-test
      model: gpt-4o
      timeout: 5
      contextLength: 128000
  embedding:
    default: emb
    models:
      emb:
        provider: openai
        apiUrl: %s/v1/embeddings
        apiKey: sk-test
        model: text-embedding-3-small
  agents:
    helper:
      model: gpt4o
      role: 助手
      instruction: 有问必答
`, f.srv.URL, f.srv.URL)
	path := filepath.Join(dir, "app.yml")
	if err := os.WriteFile(path, []byte(appYML), 0o644); err != nil {
		t.Fatal(err)
	}
	props := config.NewProps()
	if err := props.LoadYAML(path); err != nil {
		t.Fatalf("LoadYAML app.yml: %v", err)
	}
	config.SetProps(props)
	t.Cleanup(func() { config.SetProps(config.NewProps()) })

	p, err := ai.NewPlugin(nil)
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}
	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = p.Stop() }() // 手动 defer：下文断言 Stop 清场

	// 一问一答走默认模型
	m := ai.Default()
	if m == nil {
		t.Fatal("Default() = nil")
	}
	res, err := m.Prompt("你好").Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if res.Text() != "你好，我是配置装配的模型。" {
		t.Fatalf("Text = %q", res.Text())
	}
	// 请求形状：POST {base}/v1/chat/completions + Bearer 鉴权 + model 名
	req := f.last()
	if req.method != "POST" || req.path != "/v1/chat/completions" {
		t.Errorf("request = %s %s", req.method, req.path)
	}
	if got := req.header.Get("Authorization"); got != "Bearer sk-test" {
		t.Errorf("Authorization = %q", got)
	}
	if req.body["model"] != "gpt-4o" {
		t.Errorf("model = %v", req.body["model"])
	}
	if f.count() != 1 {
		t.Errorf("requests = %d, want 1", f.count())
	}

	// Stop 清场：包级默认与访问器归零
	if err := p.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if ai.DefaultRegistry() != nil || ai.Default() != nil || ai.Agent("helper") != nil {
		t.Error("Stop must clear the package default")
	}
}

// ---- 缺省 / 多模型 ----

func TestPluginMultiModelAndUse(t *testing.T) {
	f := newFakeLLM(t, "ok")
	setGlobalYAML(t, fmt.Sprintf(`
ai:
  default: gpt4o
  models:
    gpt4o: {provider: openai, apiUrl: %s, model: gpt-4o}
    mini:  {provider: openai, apiUrl: %s, model: gpt-4o-mini}
`, f.srv.URL, f.srv.URL))
	startPlugin(t)

	if names := ai.DefaultRegistry().ChatModelNames(); len(names) != 2 ||
		names[0] != "gpt4o" || names[1] != "mini" {
		t.Fatalf("ChatModelNames = %v", names)
	}
	mini := ai.Use("mini")
	if mini == nil || mini.Config().Model != "gpt-4o-mini" {
		t.Fatalf("Use(mini) = %+v", mini.Config())
	}
	if ai.Use("nope") != nil {
		t.Error("Use(unknown) must be nil")
	}
	if ai.Default() == mini {
		t.Error("Default must differ from mini")
	}
	// Use("") 与 Default 同一实例
	if ai.Use("") != ai.Default() {
		t.Error(`Use("") must equal Default()`)
	}
}

// ---- 环境变量覆盖（config.LoadEnv：AIFEI_<小写路径>）----

func TestPluginEnvOverride(t *testing.T) {
	f := newFakeLLM(t, "ok")
	yaml := fmt.Sprintf(`
ai:
  default: gpt4o
  models:
    gpt4o: {provider: openai, apiUrl: %s, model: gpt-4o}
    mini:  {provider: openai, apiUrl: %s, model: gpt-4o-mini}
`, f.srv.URL, f.srv.URL)
	t.Setenv("AIFEI_AI_DEFAULT", "mini")                       // 切默认模型
	t.Setenv("AIFEI_AI_MODELS_MINI_MODEL", "gpt-4o-mini-2026") // 覆盖模型名字段
	props := config.NewProps()
	if err := props.LoadYAMLBytes([]byte(yaml)); err != nil {
		t.Fatal(err)
	}
	props.LoadEnv("AIFEI")
	config.SetProps(props)
	t.Cleanup(func() { config.SetProps(config.NewProps()) })
	startPlugin(t)

	m := ai.Default()
	if m == nil || m.Config().Model != "gpt-4o-mini-2026" {
		t.Fatalf("Default model = %+v", m)
	}
	// 端到端：请求体携带被覆盖的模型名
	if _, err := m.Prompt("hi").Call(); err != nil {
		t.Fatal(err)
	}
	if f.last().body["model"] != "gpt-4o-mini-2026" {
		t.Errorf("request model = %v", f.last().body["model"])
	}
}

// ---- embedding 段 ----

func TestPluginEmbedding(t *testing.T) {
	f := newFakeLLM(t, "unused")
	emb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.requests = append(f.requests, capturedReq{
			method: r.Method, path: r.URL.Path, header: r.Header.Clone(), body: body,
		})
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1,0.2]}],"model":"text-embedding-3-small"}`))
	}))
	t.Cleanup(emb.Close)

	setGlobalYAML(t, fmt.Sprintf(`
ai:
  embedding:
    default: emb
    models:
      emb: {provider: openai, apiUrl: %s/v1/embeddings, apiKey: sk-e, model: text-embedding-3-small}
`, emb.URL))
	startPlugin(t)

	m := ai.Embedding("")
	if m == nil {
		t.Fatal(`Embedding("") = nil`)
	}
	if names := ai.DefaultRegistry().EmbeddingNames(); len(names) != 1 || names[0] != "emb" {
		t.Fatalf("EmbeddingNames = %v", names)
	}
	vec, err := m.Embed(context.Background(), "你好")
	if err != nil {
		t.Fatal(err)
	}
	if len(vec) != 2 || vec[0] != 0.1 || vec[1] != 0.2 {
		t.Fatalf("vec = %v", vec)
	}
	req := f.last()
	if req.path != "/v1/embeddings" || req.body["model"] != "text-embedding-3-small" {
		t.Errorf("embedding request = %s %v", req.path, req.body)
	}
	if input, _ := req.body["input"].([]any); len(input) != 1 || input[0] != "你好" {
		t.Errorf("input = %v", req.body["input"])
	}
	if ai.Embedding("nope") != nil {
		t.Error("Embedding(unknown) must be nil")
	}
}

// ---- agent 段 ----

func TestPluginAgent(t *testing.T) {
	f := newFakeLLM(t, "好的。")
	setGlobalYAML(t, fmt.Sprintf(`
ai:
  default: m1
  models:
    m1: {provider: openai, apiUrl: %s, model: gpt-4o}
  agents:
    helper:
      model: m1
      role: 翻译官
      instruction: 忠实翻译
      maxRetries: 2
      retryDelayMs: 500
      sessionWindowSize: 3
`, f.srv.URL))
	startPlugin(t)

	ag := ai.Agent("helper")
	if ag == nil {
		t.Fatal("Agent(helper) = nil")
	}
	resp, err := ag.Prompt("hi").Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.Content() != "好的。" {
		t.Fatalf("Content = %q", resp.Content())
	}
	// 系统提示词：角色/指令进 system 消息（simple.SystemPrompt 布局）
	req := f.last()
	msgs, _ := req.body["messages"].([]any)
	if len(msgs) == 0 {
		t.Fatalf("messages = %v", req.body["messages"])
	}
	sys, _ := msgs[0].(map[string]any)
	if sys["role"] != "system" {
		t.Fatalf("messages[0].role = %v", sys["role"])
	}
	if content, _ := sys["content"].(string); !strings.Contains(content, "## 你的角色") ||
		!strings.Contains(content, "翻译官") || !strings.Contains(content, "## 执行指令") ||
		!strings.Contains(content, "忠实翻译") {
		t.Errorf("system prompt = %q", content)
	}
	if ai.Agent("nope") != nil {
		t.Error("Agent(unknown) must be nil")
	}
}

// agent 引用未定义模型 → 启动期 fail-fast。
func TestPluginAgentUnknownModelFails(t *testing.T) {
	f := newFakeLLM(t, "ok")
	setGlobalYAML(t, fmt.Sprintf(`
ai:
  models:
    m1: {provider: openai, apiUrl: %s, model: gpt-4o}
  agents:
    bad: {model: missing}
`, f.srv.URL))

	p, _ := ai.NewPlugin(nil)
	err := p.Start()
	if err == nil || !strings.Contains(err.Error(), `unknown model "missing"`) {
		t.Fatalf("Start err = %v, want unknown model", err)
	}
}

// ai.default 引用未定义模型 → 启动期 fail-fast。
func TestPluginUnknownDefaultFails(t *testing.T) {
	setGlobalYAML(t, `
ai:
  default: ghost
  models:
    m1: {provider: openai, apiUrl: https://x.example, model: gpt-4o}
`)
	p, _ := ai.NewPlugin(nil)
	if err := p.Start(); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("Start err = %v, want default model error", err)
	}
}

// ---- mcp 客户端段 ----

// newFakeMCPServer 真 MCP 服务端（一个 echo 工具 + 一个 other 工具，供
// 白名单过滤断言），外加捕获请求头。
func newFakeMCPServer(t *testing.T) (*httptest.Server, *capturedHeader) {
	t.Helper()
	srv := mcp.NewServer(mcp.Implementation{Name: "fake-mcp", Version: "1.0.0"})
	srv.Tool("echo", `{"type":"object","properties":{"text":{"type":"string"}}}`,
		func(cc *mcp.CallCtx, args map[string]any) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent(args["text"].(string))}}, nil
		})
	srv.Tool("other", "", func(*mcp.CallCtx, map[string]any) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent("other")}}, nil
	})
	cap := &capturedHeader{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.mu.Lock()
		cap.header = r.Header.Clone()
		cap.mu.Unlock()
		srv.Handler().ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts, cap
}

type capturedHeader struct {
	mu     sync.Mutex
	header http.Header
}

func (c *capturedHeader) get(key string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.header.Get(key)
}

func TestPluginMCPClient(t *testing.T) {
	ts, cap := newFakeMCPServer(t)
	setGlobalYAML(t, fmt.Sprintf(`
ai:
  mcp:
    servers:
      demo:
        url: %s
        headers: {X-Token: abc}
        requestTimeout: 5
        allow: [echo]
`, ts.URL))
	startPlugin(t)

	prov := ai.MCP("demo")
	if prov == nil {
		t.Fatal(`MCP("demo") = nil`)
	}
	// 懒连接：装配期 0 请求；Tools() 才握手 + tools/list
	tools := prov.Tools()
	if len(tools) != 1 || tools[0].Name() != "echo" {
		t.Fatalf("tools = %v (want only echo via allow-list)", tools)
	}
	if got := cap.get("X-Token"); got != "abc" {
		t.Errorf("X-Token = %q, want forwarded header", got)
	}
	if ai.MCP("nope") != nil {
		t.Error("MCP(unknown) must be nil")
	}
}

// mcp 段错误面：command/url 二选一。
func TestPluginMCPConfigErrors(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{
			"both",
			"ai:\n  mcp:\n    servers:\n      s:\n        command: [cat]\n        url: http://x\n",
			"mutually exclusive",
		},
		{
			"neither",
			"ai:\n  mcp:\n    servers:\n      s: {}\n",
			"either command",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setGlobalYAML(t, tc.yaml)
			p, _ := ai.NewPlugin(nil)
			err := p.Start()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Start err = %v, want %q", err, tc.want)
			}
		})
	}
}

// ---- 可选 server 段（Handler() 挂载互操作）----

func TestPluginMCPServerSection(t *testing.T) {
	setGlobalYAML(t, `
ai:
  mcp:
    server:
      name: demo-srv
      version: 2.0.0
      basePath: /mcpx
`)
	p := startPlugin(t)

	srv := ai.Server()
	if srv == nil {
		t.Fatal("Server() = nil")
	}
	if ai.DefaultRegistry().ServerPath() != "/mcpx" {
		t.Fatalf("ServerPath = %q", ai.DefaultRegistry().ServerPath())
	}
	srv.Tool("ping", "", func(*mcp.CallCtx, map[string]any) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent("pong")}}, nil
	})

	// Handler() 挂载：前缀命中走 MCP，其余穿透
	h := p.Handler()
	if h == nil {
		t.Fatal("Handler() = nil with server section configured")
	}
	root := h(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("fallback"))
	}))
	ts := httptest.NewServer(root)
	t.Cleanup(ts.Close)

	// 穿透：非前缀路径
	resp, err := http.Get(ts.URL + "/other")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if b, _ := io.ReadAll(resp.Body); string(b) != "fallback" {
		t.Fatalf("passthrough = %q", b)
	}
	// MCP：initialize POST → JSON 单帧响应 + 会话头 + 服务端名
	ver := mcp.Latest([]string{"2024-11-05", "2025-03-26", "2025-06-18"})
	init := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":%q,"capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`, ver)
	req2, err := http.NewRequest(http.MethodPost, ts.URL+"/mcpx", strings.NewReader(init))
	if err != nil {
		t.Fatal(err)
	}
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Accept", "application/json, text/event-stream") // streamable 双类型要求
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("initialize status = %d", resp2.StatusCode)
	}
	if resp2.Header.Get("Mcp-Session-Id") == "" {
		t.Error("Mcp-Session-Id header missing")
	}
	body, _ := io.ReadAll(resp2.Body)
	if !strings.Contains(string(body), "demo-srv") {
		t.Errorf("initialize body = %s", body)
	}
}

// 无 server 段时 Handler() 为 nil（挂载守卫契约）。
func TestPluginHandlerNilWithoutServerSection(t *testing.T) {
	setGlobalYAML(t, "ai:\n  models: {}\n")
	p := startPlugin(t)
	if p.Handler() != nil {
		t.Error("Handler() must be nil without ai.mcp.server")
	}
	if ai.Server() != nil {
		t.Error("Server() must be nil without ai.mcp.server")
	}
}

// ---- stub doer 注入缝 ----

// stubDoer 回放固定 chat completion 的假 Doer。
type stubDoer struct{}

func (stubDoer) Do(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"role":"assistant","content":"from stub doer"}}]}`)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Request:    req,
	}, nil
}

func TestPluginStubDoer(t *testing.T) {
	ai.RegisterDoer("stub-test", stubDoer{})
	setGlobalYAML(t, `
ai:
  default: m1
  models:
    m1: {provider: openai, apiUrl: https://stub.invalid, model: gpt-4o, doer: stub-test}
`)
	startPlugin(t)

	res, err := ai.Default().Prompt("hi").Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if res.Text() != "from stub doer" {
		t.Fatalf("Text = %q", res.Text())
	}
}

// doer 引用未注册名 → 启动期 fail-fast。
func TestPluginUnknownDoerFails(t *testing.T) {
	setGlobalYAML(t, `
ai:
  models:
    m1: {provider: openai, apiUrl: https://x.example, model: gpt-4o, doer: ghost}
`)
	p, _ := ai.NewPlugin(nil)
	err := p.Start()
	if err == nil || !strings.Contains(err.Error(), `unknown doer "ghost"`) {
		t.Fatalf("Start err = %v, want unknown doer", err)
	}
}

// ---- 安全钩子（server 段 + SecurityValidator）----

func TestPluginMCPSecurityValidator(t *testing.T) {
	setGlobalYAML(t, `
ai:
  mcp:
    server: {name: guarded}
`)
	p, err := ai.NewPlugin(nil)
	if err != nil {
		t.Fatal(err)
	}
	p.SecurityValidator = func(r *http.Request) error {
		if r.Header.Get("X-Secret") != "s3cret" {
			return fmt.Errorf("denied")
		}
		return nil
	}
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Stop() })

	ts := httptest.NewServer(p.Handler()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("must not fall through")
	})))
	t.Cleanup(ts.Close)

	// 无凭据 → 403
	resp, err := http.Post(ts.URL+"/mcp", "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}
