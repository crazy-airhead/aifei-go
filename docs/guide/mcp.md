# Aifei-Go mcp：MCP 协议双端 + ai 桥 + 配置装配

> **一个包同时是 MCP 客户端与服务端**：`DialStdio`/`DialHTTP` 三行接入外部工具服务器；`Server.Tool().Resource().Prompt()` 把自身能力暴露出去；`ai/mcp` 桥把远端工具直接变成 `ai.FunctionTool`。

## 1. 背景与定位

MCP（Model Context Protocol）是 Anthropic 发起的模型-工具互操作标准。`./mcp` 模块（独立 go.mod，纯标准库实现）覆盖协议双端：

- **客户端方向**：连接任意识别 MCP 的工具服务器（文件系统、数据库、浏览器……），把它的工具接入 [ai 聊天核心](ai.md)与[智能体](ai-agent.md)
- **服务端方向**：把 Go 函数发布为 MCP 工具/资源/提示，供 Claude Desktop、Cursor、其他 MCP 客户端调用
- **桥接层** `ai/mcp`：客户端工具表 → `ai.FunctionTool`；`ai.Talent` ↔ MCP 资源互导

支持版本：`Version20241105` / `Version20250326` / `Version20250618` / `Version20251125`（`mcp.SupportedVersions` 全列，握手时 `mcp.Negotiate` 自动协商，`mcp.Latest` 取最新）。

## 2. 传输与总体结构

```text
┌─ 你的 Go 程序 ──────────────────────────────────────────┐
│                                                          │
│  mcp.Client ─── Transport ──┬── stdio（子进程 JSON-RPC）│── 对端 MCP Server
│                             ├── streamable HTTP（单端点）│
│                             └── 旧式 SSE（双端点）      │
│                                    ▲                    │
│  mcp.Server.Handler() ─────────────┘ 反向：对外提供工具 │── MCP 客户端们
│                                                          │
│  ai/mcp.Provider: Client.Tools() → []ai.FunctionTool    │
│  plugins/ai:      app.yml 声明式装配全部以上            │
└──────────────────────────────────────────────────────────┘
```

## 3. 客户端：连接与调用

```go
// 形态一：stdio 子进程（懒连接：首次操作才拉起进程）
c, err := mcp.DialStdio("npx", "-y", "@modelcontextprotocol/server-filesystem", "/tmp")

// 形态二：streamable HTTP（2025-03 起标准，单端点 POST/GET/DELETE）
c := mcp.DialHTTP("http://localhost:8080/mcp")

// 形态三：旧式 SSE（2024-11-05 双端点）
c := mcp.DialSSE("http://legacy-host/sse")

defer c.Close()
```

需要精细控制时用工厂形态（重连时工厂重建传输）。`WithHeartbeat(interval)` 开启后引擎以 ping 探活、断线指数退避重连（上限 10 分钟），长驻进程建议开启：

```go
tr := mcp.NewStdioTransport(os.Args[0]).
    StdioEnv(map[string]string{"SERVER": "1"}).      // 子进程环境变量
    StdioStderr(func(line string) { log.Info(line) }) // stderr 钩子
c := mcp.NewClient(tr.TransportFactory(),
    mcp.WithRequestTimeout(20*time.Second),
    mcp.WithHeartbeat(30*time.Second),               // ping 心跳 + 指数退避重连
    mcp.WithToolsChangeConsumer(func(tools []mcp.Tool) { // 工具表变更回调
        log.Info("tools changed", "n", len(tools))
    }),
)
```

常用 `ClientOption`：`WithClientInfo(Implementation)`（握手身份）、`WithHeaders(http.Header)`（鉴权头）、`WithRequestTimeout` / `WithInitializationTimeout`（缺省各 20s）、`WithHeartbeat(interval)`、`WithRoots(listChanged, roots...)`（声明可访问根路径）、`WithClientCapabilities(cap, sampling, elicitation)`（反向能力）、`WithLoggingConsumer` / `WithProgressConsumer` / 各 `With*ChangeConsumer`。

操作面（全部懒初始化，无需手动 `Initialize`）：

```go
tools, _ := c.ListAllTools(ctx)          // 自动翻页聚合
res, _ := c.CallTool(ctx, "echo", map[string]any{"text": "x"})
// res.Content 为 []mcp.Content（*mcp.TextContent 等类型断言）；res.IsError 标记业务失败

c.ListAllResources(ctx) / c.ReadResource(ctx, "memo://notes")
c.SubscribeResource(ctx, "memo://notes") / c.UnsubscribeResource(ctx, "memo://notes")
c.ListAllPrompts(ctx) / c.GetPrompt(ctx, "greet", map[string]string{"name": "张三"})
c.Ping(ctx)
```

内容块家族：`mcp.NewTextContent(text)` / `mcp.NewImageContent(data, mimeType)`，其余资源型内容按 `*mcp.TextContent`、`*mcp.ImageContent` 等类型断言取用。状态与控制：`c.Initialize(ctx)`（幂等，可省略）、`c.IsInitialized()`、`c.ServerCapabilities()` / `c.ServerInfo()` / `c.ServerInstructions()` / `c.NegotiatedVersion()`、`c.Reset()`（丢弃会话待重建）、`c.Close()`（幂等）。

错误处理：`mcp.IsTransportError(err)` 判传输层错误（应重连）；JSON-RPC 错误用 `errors.As` 取 `*mcp.JSONRPCError`：

| 码 | 含义 | 码 | 含义 |
|---|---|---|---|
| -32700 | 解析错误 | -32602 | 参数无效 |
| -32600 | 无效请求 | -32603 | 内部错误 |
| -32601 | 方法不存在 | -32002 | 会话无效 |

## 4. 服务端：发布工具、资源与提示

```go
s := mcp.NewServer(mcp.Implementation{Name: "my-tools", Version: "1.0.0"},
    mcp.WithInstructions("内部效率工具集"))

// 工具：inputSchema 为 JSON Schema 文本；同名重复注册 = 替换
s.Tool("echo", `{"type":"object","properties":{"text":{"type":"string"}}}`,
    func(cc *mcp.CallCtx, args map[string]any) (*mcp.CallToolResult, error) {
        return &mcp.CallToolResult{
            Content: []mcp.Content{mcp.NewTextContent("echo:" + args["text"].(string))},
        }, nil
    }).Title("回声").Desc("原样返回输入")          // ToolSpec 链式补充

// 资源：URI 含 '{' 自动按 RFC 6570 模板注册
s.Resource("memo://notes", "notes", func(cc *mcp.CallCtx) (any, error) {
    return "note text", nil
}).MimeType("text/x-note").Desc("备忘")
s.Resource("db://users/{id}/posts", "posts", func(cc *mcp.CallCtx) (any, error) {
    return "posts of " + cc.RequestVars()["id"], nil   // 模板变量
})
s.Resource("bin://logo", "logo", func(cc *mcp.CallCtx) (any, error) {
    return []byte{0x00, 0x01}, nil                     // 二进制（base64 自动）
})

// 提示
s.Prompt("greet", []mcp.PromptArgument{{Name: "name", Description: "姓名"}},
    func(cc *mcp.CallCtx, args map[string]string) (any, error) {
        return "hi " + args["name"], nil
    }).Desc("问候")
```

处理器返回值宽容：资源可返回 `string`/`[]byte`/`ResourceContents`，提示可返回 `string`/`PromptMessage`，框架自动包装。注册表可查询与摘除：`HasTool/RemoveTool/ToolNames`（资源/提示同款）；变更后 `NotifyToolsListChanged()` / `NotifyResourcesListChanged()` / `NotifyPromptsListChanged()` 广播给在线客户端，`NotifyResourceUpdated(uri)` 只推订阅了该资源的会话，`NotifyLogging(n)` 主动下发日志。会话治理：`SessionCount()` 在线数、`Close()` 后续请求 503（幂等）。

### 4.1 承载方式

```go
// 单端点 streamable HTTP（推荐）
ts := httptest.NewServer(s.Handler())

// 挂进已有 HTTP 服务（前缀命中走 MCP，其余穿透）
mux.Handle("/", s.Middleware("/mcp")(existingHandler))
// 或 aifei：io := server.NewIoHandler(app); mux.Handle("/", s.Middleware("/mcp")(io))

// 旧式 SSE 双端点
http.Handle("/sse", s.SSEHandler("/sse", "/message"))

// stdio（做 Claude Desktop 等的本地插件；stdout 归协议独占，日志自动切 stderr）
_ = s.RunStdio(ctx)
```

### 4.2 CallCtx：会话、进度与反向请求

`CallCtx` 是处理器上下文：`SessionID()` / `ClientInfo()` / `ClientCapabilities()` / `RequestURI()` / `RequestVars()` / `ProgressToken()`，以及两个反向能力：

```go
s.Tool("slow", "", func(cc *mcp.CallCtx, _ map[string]any) (*mcp.CallToolResult, error) {
    _ = cc.NotifyProgress(0.5, 1, "half way")        // 进度上报
    res, err := cc.Sample(&mcp.CreateMessageRequest{ // 反向采样：请客户端的 LLM 帮忙
        Messages: []mcp.SamplingMessage{{
            Role: mcp.RoleUser, Content: mcp.NewTextContent("translate: bonjour"),
        }},
        MaxTokens: 32,
    })
    ...
})
```

`cc.Elicit(&mcp.ElicitRequest{...})` 则向用户征询表单输入（需客户端声明 elicitation 能力）。

### 4.3 安全与批量注册

```go
// 安全钩子：拒绝时整个请求 403（放在 NewServer 选项）
s := mcp.NewServer(info, mcp.WithSecurityValidator(func(r *http.Request) error {
    if r.Header.Get("Authorization") == "" {
        return errors.New("missing token")
    }
    return nil
}))

// 反射批量注册：导出方法 func(*CallCtx, map[string]any)(any, error) 即成工具
type calcService struct{}
func (c *calcService) Add(cc *mcp.CallCtx, args map[string]any) (any, error) { ... }
mcp.RegisterTools(s, &calcService{})   // @ToolMapping 扫描的对应物
```

其余服务端选项：`WithInstructions` / `WithCapabilities` / `WithStrictToolName(true)`（非法工具名 panic，缺省开）/ `WithServerRequestTimeout`（反向请求超时 20s）/ `WithKeepAlive`（GET 流保活）/ `WithServerProtocolVersions`。

## 5. ai/mcp 桥：工具表接入与 Talent 互导

### 5.1 Provider（远端工具 → ai.FunctionTool）

```go
p := aimcp.NewProvider(c,                    // c 为 *mcp.Client
    aimcp.WithAllowed("echo", "read_file"),  // 白名单（空 = 全部）
    aimcp.WithCacheTTL(time.Minute),         // 工具表缓存（缺省 30s；<=0 关）
)

// 实现 ai.ToolProvider —— 两行接入聊天/智能体：
//   Options(func(o *ai.ChatOptions){ o.ToolAdd(p) })
tools := p.Tools()
res, _ := p.CallTool(ctx, "echo", map[string]any{"text": "x"})
```

内建行为：自动订阅 `tools/list_changed` 失效缓存；工具属性（Title/Desc/Schema/Annotations）完整映射；`CallTool` 遇传输错误自动 `Reset()` 重连重试一次；`IsError` 文本自动补 `"Error: "` 前缀；Content 块映射 Text/Image/Audio/EmbeddedResource→`ai.ResourceLink`。

### 5.2 Talent 桥（能力包跨进程互导）

```go
// 方向一：远端 MCP 上的 talent 端点包装为本地 ai.Talent
tl, err := aimcp.NewTalent(p, aimcp.WithTalentTimeout(30*time.Second))
tl.Name(); tl.Instruction(prompt); tl.Tools(prompt)   // 背后是 4 个管理工具的远程调用

// 方向二：本地 ai.Talent 导出为 MCP 资源 + 管理工具
aimcp.RegisterTalent(mcpServer, myTalent)
// 导出资源 talent://metadataMcp（元信息）+ isSupportedMcp/onAttachMcp/
// getInstructionMcp/getToolsMcp 四个工具（入参统一 {"promptJson": ...}）

// getToolsMcp 名单裁剪缝：nil=全量；返回 []string 按名过滤
aimcp.SetToolsNameProvider(func(p *ai.Prompt) []string { return []string{"wiki-search"} })
```

降级策略：`IsSupported` 远程失败按 false 处理（fail-closed）；Instruction/Tools 失败回退空值/全量；OnAttach 失败仅记日志。

## 6. plugins/ai：声明式装配

`./plugins/ai`（导入名 `ai`，常配别名 `aiai`）把模型、智能体、MCP 全部收进一份 `app.yml`：

```yaml
ai:
  default: gpt4o                       # 引用 models 键；引用未定义启动期报错
  models:
    gpt4o: {provider: openai, apiUrl: https://api.openai.com/v1/chat/completions,
            apiKey: sk-xxx, model: gpt-4o, timeout: 30, contextLength: 128000}
    local: {provider: ollama, apiUrl: http://localhost:11434/api/chat, model: qwen3}
  embedding:
    default: emb
    models:
      emb: {provider: openai, apiUrl: https://api.openai.com/v1/embeddings,
            apiKey: sk-xxx, model: text-embedding-3-small}
  agents:
    helper: {model: gpt4o, role: 助手, instruction: 有问必答}
  mcp:
    servers:                           # command 与 url 二选一
      fs:     {command: [npx, -y, "@modelcontextprotocol/server-filesystem", /tmp]}
      remote: {url: http://localhost:8080/mcp,
               headers: {Authorization: "Bearer x"},
               requestTimeout: 10, allow: [echo], deny: [dangerous], cacheTtl: 60}
    server: {name: my-server, version: 1.0.0, basePath: /mcp}   # 对外暴露段（可选）
```

装配与使用：

```go
if err := config.Init(os.Args); err != nil { panic(err) }
p, _ := aiai.NewPlugin(nil)             // (logger, prefix...)；prefix 缺省 "ai"
app := aifei.New(aifei.WithPlugin(p))
// p.Handler() 返回 func(http.Handler) http.Handler（配置了 ai.mcp.server 时），
// 挂进 HTTP 服务即对外提供 MCP 端点

aiai.Use("gpt4o").Prompt("你好").Call()     // 包级快捷：具名/默认 chat 模型
aiai.Embedding("emb")                       // 嵌入模型
aiai.Agent("helper")                        // simple.Agent
prov := aiai.MCP("remote")                  // *aimcp.Provider（懒连接：Tools() 才握手）
srv := aiai.Server()                        // 对外 MCP 服务端（未配置为 nil）
```

- `Start()` 只装配不触网（MCP 懒连接）；`Stop()` 关闭全部 MCP 客户端/服务端
- 安全钩子公共字段：`p.SecurityValidator = func(*http.Request) error{...}`（须 Start 前设置）
- 测试注入缝：`aiai.RegisterDoer("stub", doer)` + 模型配置 `doer: stub`
- 环境变量覆盖：`AIFEI_AI_DEFAULT=mini`、`AIFEI_AI_MODELS_MINI_MODEL=gpt-4o-mini`（规则 `AIFEI_<小写路径>`）
- 方言在插件内全量注册，yml 只写 `provider`/`standard` 即命中（见 [ai.md §7](ai.md)）

`Registry` 全访问器（包级函数等价转发到默认注册表）：

| 访问器 | 返回 | 说明 |
|---|---|---|
| `ChatModel(name)` / `Use(name)` / `Default()` | `*ai.ChatModel` | Use 空名取默认 |
| `Embedding(name)` | `*embedding.Model` | 空名取 embedding.default |
| `Agent(name)` | `*simple.Agent` | agents 段装配产物 |
| `MCP(name)` | `*aimcp.Provider` | mcp.servers 段装配产物 |
| `Server()` / `ServerPath()` | `*mcp.Server` / string | 未配置 server 段为 nil |
| `ChatModelNames` 等 `*Names()` | `[]string` | 各注册表键清单 |
| `SetDefault(r)` / `DefaultRegistry()` | — | 包级默认注册表替换/读取 |

## 7. 模块结构

| 路径 | 职责 |
|---|---|
| `mcp/client.go` `transport_stdio.go` `transport_http.go` `transport_sse.go` | 客户端、三传输、选项与消费者 |
| `mcp/server.go` `server_http.go` `server_session.go` | 服务端注册表、HTTP/SSE/stdio 承载、会话 |
| `mcp/schema.go` `version.go` `session.go` | 协议类型、版本协商、传输错误判定 |
| `ai/mcp/provider.go` | Provider 桥（白黑名单/缓存/重连） |
| `ai/mcp/talent.go` | Talent 双向桥 |
| `plugins/ai/config.go` `plugin.go` `registry.go` `dialects.go` | 配置键、生命周期、注册表、方言全量注册 |
| `_test/mcp_test/` | 回环测试（stdio 回放、HTTP、桥、aifei 挂载） |

## 8. 总结

- **协议自足**：纯标准库实现 MCP 双端，无 SDK 依赖，stdio/HTTP/SSE 三传输齐备
- **懒连接 + 自愈**：握手、心跳、断线重连、缓存失效全部内建，业务代码零感知
- **桥只做映射**：Provider 不发明新概念，远端工具就是 `ai.FunctionTool`，一行 `ToolAdd` 进聊天链
- **安全默认从严**：StrictToolName、SecurityValidator 403、Talent fail-closed
- **配置收口**：plugins/ai 让"模型 + 智能体 + MCP 客户端 + MCP 服务端"一份 yml 声明完毕

### 延伸阅读

- [ai — 聊天核心（ToolProvider 接入缝）](ai.md)
- [ai-agent — 给智能体挂 MCP 工具](ai-agent.md)
- [ai-talent — 能力包（Talent 桥的本体）](ai-talent.md)
- [config — 分层配置（SubBind 装配原理）](config.md)
- 架构设计：[../arch/ai/04-mcp-design.md](../arch/ai/04-mcp-design.md)
