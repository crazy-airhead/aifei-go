# Aifei-Go ai：LLM 聊天核心（消息 / 方言 / 工具 / 流式）

> **一套 `ChatModel` 抽象直连 7 种厂商方言**：OpenAI 兼容 / Anthropic / Gemini / Ollama / DashScope 等，工具调用、流式事件、多模态内容、会话记忆开箱即用，零框架绑定。

## 1. 背景与定位

`./ai` 模块是 aifei-go AI 能力栈的地基（从 Java solon-ai 移植），只解决一件事：**用统一的 Go API 调用任意 LLM 聊天服务**。它不依赖 aifei 框架核心，可独立引入任何 Go 项目。

- **模块路径**：`github.com/crazy-airhead/aifei-go/ai`（独立 go.mod；`agent`/`flow`/`harness`/`loop` 是其兄弟模块）
- **零外部依赖**：核心包只用标准库 `net/http`；厂商适配以"方言（Dialect）"形式内置
- 上层建筑：[ai-agent](ai-agent.md)（智能体）、[mcp](mcp.md)（工具协议）、[ai-rag](ai-rag.md)（检索增强）、[ai-talent](ai-talent.md)（能力包）都构建在 `ChatModel` 之上

与 solon-ai 的对应关系：`ChatModel` ≈ `ChatModel`，`Prompt` ≈ `ChatPrompt`，`FunctionTool` ≈ `@ToolMapping` 方法，方言 ≈ `ChatDialect` 实现。

## 2. 核心概念与调用链路

| 概念 | 类型 | 说明 |
|------|------|------|
| 消息 | `Message` + `Role` | system/user/assistant/tool 四角色，`Content` 为 `any`（字符串 / 内容块 / 混合容器） |
| 多模态 | `ContentBlock` 家族 | text/image/audio/video/blob/resource 六类，URL 或 Base64 构造 |
| 提示 | `Prompt` | 有序消息列表 + `Attrs` 附加属性；`Copy()` 深拷贝（copy-before-mutate） |
| 模型 | `ChatModel` | 由 `Config` 构造，`Prompt(...)` 返回请求链 |
| 请求链 | `RequestDesc` | `.Session().Role().Options().Call()` 链式描述一次调用 |
| 选项 | `ChatOptions` | 温度/工具/Schema/拦截器/Talent，经 `Options(fn)` 回调写入 |
| 响应 | `Response` | 文本/思考/工具调用/用量/事件轨迹的统一读取面 |
| 方言 | `Dialect` | 请求/响应的厂商编解码器，import 即注册 |

```text
用户代码                ai 包                        LLM 服务
--------                -----                        --------
Prompt(p) ──────► RequestDesc ──► prepare（会话/系统提示/Talent/拦截器）
                        │
                   SelectDialect(cfg) ──► 方言 BuildRequest ──► HTTP
                        │                                      │
Call() ◄──────── Response ◄── 方言 ParseResponse ◄─────────────┘
Stream() ────────► ChatEvent 流（16 种生命周期事件）
```

## 3. 快速开始：最小调用

```go
import ai "github.com/crazy-airhead/aifei-go/ai"

// 形态一：Config 结构体直给（apiURL/model 必填）
m, err := ai.NewChatModel(ai.Config{
    APIURL: "https://api.openai.com/v1/chat/completions",
    APIKey: "sk-xxx", Model: "gpt-4o",
})
res, err := m.Prompt("你好").Call()
fmt.Println(res.Text())

// 形态二：Builder 链
m, err := ai.NewChatModelURL("http://localhost:11434/api/chat").
    APIKey("ollama").Model("qwen3").Provider("ollama").
    Name("本地模型").Timeout(30).Build()
```

`Prompt(p any)` 接受 `string` / `*Message` / `[]*Message` / `*Prompt` / `nil`（续跑会话）。请求链上还可挂角色与指令：

```go
res, err := m.Prompt("一共几单").
    Role("订单客服").            // 注入 "# Role: 订单客服"
    Instruction("只回答订单相关问题"). // 追加行为指令
    Call()
```

`Response` 读取面：`Text()` / `Thinking()`（思考链）/ `Content()` / `ToolCalls()` / `Blocks()` / `FinishReason()` / `Usage()` / `Events()` / `Raw()` / `IsTerminal()` / `HasContent()`；结构化输出用 `ai.Bind[T]`：

```go
type WeatherOut struct{ City string `json:"city"`; Temp int `json:"temp"` }
schema := ai.OutputSchemaOf[WeatherOut]()   // 由泛型生成 JSON Schema
// Options 中挂 o.OutputSchema = schema 后：
out, err := ai.Bind[WeatherOut](res.Message())
```

## 4. 消息与多模态

```go
// 四角色工厂
msg := ai.OfUser("查天气")                  // Content 为 string
msg := ai.OfSystem("你是助手")
msg := ai.OfSystemRole("翻译官", "只做中英互译") // "# Role: 翻译官\n\n只做中英互译"
msg := ai.OfAssistant("好的")
msg := ai.OfTool(result, "query-order", callID, false) // 工具结果回填

// 多模态混合容器
contents := ai.NewContents("这张图里是什么？").
    AddBlock(ai.ImageBlockOfURL("https://x/a.jpg")).
    AddBlock(ai.AudioBlockOfBase64(bytes))
msg := ai.OfUser(contents)   // contents.IsMultiModal() == true
```

内容块构造族（均带可变 `mime ...string` 参数）：`TextBlockOf`、`ImageBlockOfURL/ImageBlockOfBase64`（缺省 `image/jpeg`）、`AudioBlockOf*`（`audio/mpeg`）、`VideoBlockOf*`（`video/mpeg`）、`BlobBlockOf*`（`application/octet-stream`）；`ResourceLink` 用字面量 `&ai.ResourceLink{Name, URI, Mime, Meta}` 引用外部资源。

`Prompt` 侧：`NewPrompt(msgs...)` / `PromptOf("单条")` / `AddMessage` / `LastMessage` / `SetAttr` / `Copy()`（方言与拦截器都拿拷贝，不改原件）。

## 5. 工具调用（Function Calling）

```go
tool := ai.NewTool("query-order").
    Desc("按订单号查询订单状态").
    StringParam("orderId", "订单号", true).
    Handler(func(ctx context.Context, args map[string]any) (any, error) {
        tc := ai.ToolContextOf(ctx)   // 取回注入的会话级上下文
        _ = tc["sessionId"]
        return "A1 已发货", nil
    }).Build()

on := true
res, err := m.Prompt("查一下订单 A1").
    Options(func(o *ai.ChatOptions) {
        o.AutoToolCall = &on      // 开启自动执行；nil = 关闭
        o.Tools = []ai.FunctionTool{tool}
    }).
    Call()
// 模型返回 tool_calls → ai 自动执行 → 结果回填 → 递归下一轮 → 终答
```

`NewTool(name)` 链式全集：`Title` / `Desc` / `Strict` / `ReturnDirect(bool)` / `Param(ParamSpec)`（声明式）/ `StringParam` / `IntParam` / `FloatParam` / `BoolParam` / `DateParam` / `DateTimeParam` / `InputSchemaJSON(s)`（直给 schema 覆盖生成）/ `OutputSchemaJSON` / `MetaSet` / `ResultConverter(fn)` / `Handler(fn)` / `Build()`。

机制要点：

- **自动执行开关**：`o.AutoToolCall != nil` 时取其值，否则回退 `Config.DefaultAutoToolCall`；开启后模型发起的 tool_calls 顺序执行，非 `returnDirect` 工具执行后递归下一轮（Step+1），全部 returnDirect 时由方言合成终答
- **ReturnDirect**：工具结果直接作为最终答案返回（如"当前时间"类工具）
- **错误兜底**：`Call()` 内置 panic recover，错误文本自动补 `"Error: "` 前缀返回给模型
- **批量注册**：`o.ToolAdd(p)` 接受任意 `ToolProvider`（`Tools() []FunctionTool`），[mcp 桥](mcp.md)即从此缝接入

## 6. 流式（SSE）

```go
// 形态一：channel（缓冲 64，背压不丢事件）
ch, err := ai.StreamChan(ctx, m.Prompt("写一首长诗"))
for ev := range ch {
    switch ev.Type {
    case ai.EventTextDelta:      // 文本增量 ev.Text
    case ai.EventThinkingDelta:  // 思考链增量
    case ai.EventUsage:          // 用量
    case ai.EventResponseEnd:    // 收尾
    }
}

// 形态二：自定义 sink（OnEvent 返回 error 即中止）
err = m.Prompt("你好").Stream(ctx, ai.ChatSinkFunc(func(ev *ai.ChatEvent) error {
    fmt.Print(ev.Text)
    return nil
}))

// 形态三：流式转聚合（保留全事件轨迹）
resp, err := ai.CallStream(ctx, m.Prompt("你好"))
```

事件常量（`event.go`）：`EventResponseStart/End`、`EventStepStart/End`、`EventTextStart/Delta/End`、`EventThinkingDelta`、`EventToolCallStart/ArgsDelta/End`、`EventToolResult`、`EventUsage`、`EventError`、`EventRaw`、`EventCustom`。

## 7. 方言（Dialect）：一码多厂商

方言随包 import 自注册（`RegisterDialect(d, priority)`），`SelectDialect(cfg)` 按 ① `Config.Standard` 精确匹配 → ② 优先级序第一个 `Matched(cfg)` → ③ `IsDefault()` 补位 选择：

| import（`ai/dialect/...`） | standard 名 | 优先级 | 命中特征 |
|---|---|---|---|
| `dashscope` | `dashscope` | 10 | provider/standard=dashscope，或 URL 前缀 `dashscope.aliyuncs.com/api/v1/services/` |
| `ollama` | `ollama` | 20 | standard=ollama，或 URL 以 `/api/chat` 结尾 |
| `gemini`（interactions.go） | `google-interactions` | 29 | 或 URL 以 `/interactions` 结尾 |
| `gemini` | `gemini` | 30 | provider=google 等，或 URL 含 `/v1|v1beta` models 路径 |
| `anthropic` | `anthropic` | 40 | provider=claude/anthropic，或 URL 以 `/messages` 结尾 |
| `openai`（openai.go） | `openai-completions` | 100 | **默认补位**：任何未命中的配置都落到它 |
| `openai`（responses.go） | `openai-responses` | 100 | 显式指定，或 URL 以 `/responses` 结尾 |

实践建议：**URL 能唯一确定时无需写 provider**（如 Ollama 的 `/api/chat` 自动命中）；网关代理地址建议显式 `Provider("openai")`。`ai.Dialects()` 可列出全部注册名。

## 8. 会话（Session）

会话挂在**请求链**上而非 Prompt：`m.Prompt("...").Session(sess)`（内部写 attrs `sessionId` 并注入 ToolContext）。

```go
sess := ai.NewMemorySession("s1")          // 内存会话，容量上限 50
sess = ai.NewMemorySessionMax(200, "s1")   // 自定义容量；<=0 不限

fs := ai.NewFileSession("/data/sessions", "user-42") // NDJSON 持久化
fs.AddMessage(...)                        // system 角色不落盘
if err := fs.LastError(); err != nil {     // 装载/落盘错误逃生舱
    log.Warn("session io", "err", err)
}
reload := ai.NewFileSession("/data/sessions", "user-42") // 同 id 重启恢复
```

未挂会话时每次调用自动建临时 `NewMemorySession()`，多轮工具递归仍共享。

## 9. 三个模型子包：embedding / rerank / generate

### embedding（向量化，喂给 [RAG](ai-rag.md)）

```go
m, _ := embedding.NewBuilder("https://api.openai.com/v1/embeddings").
    APIKey("sk-xxx").Model("text-embedding-3-small").Build()
vec, _ := m.Embed(ctx, "你好")                       // 单条
vecs, _ := m.EmbedTexts(ctx, []string{"a", "b", "c"}) // 自动分批（BatchSize）
dim, _ := m.Dimensions(ctx)                          // 真实探测维度
vecs, _ = m.EmbedTextsWith(ctx, texts, map[string]any{"text_type": "query"}) // 逐请求参数
```

方言：dashscope(10) / ollama(20) / openai(100 补位)。

### rerank（重排，满足 `rag.Reranker` 接口）

```go
m, _ := rerank.NewBuilder(url).APIKey("k").Model("bge-reranker-v2-m3").Build()
sorted, _ := m.Rerank(ctx, "query", docs)                       // 便捷面
res, _ := m.Input("query", docs).TopN(3).Call(ctx)              // 链式面
```

### generate（文生图 / 图生图）

```go
m, _ := generate.NewBuilder(url).APIKey("k").Model("gpt-image-1").
    TaskURL("https://api.openai.com/v1/images/tasks/"). // 异步任务查询地址
    Build()
resp, _ := m.Prompt("a cat").Size("512x512").Call(ctx)          // 文生图
resp, _ = m.PromptMap(map[string]any{                          // 图生图等多键
    "prompt": "换成夜景", "base_image_url": "https://x/a.png",
}).Call(ctx)
url := resp.Content().Value()  // 优先级 url > data > text
```

## 10. 多模型路由（router）

```go
big, _ := ai.NewChatModel(bigCfg)
fast, _ := ai.NewChatModel(fastCfg)
r1, _ := router.NewRoute("big", "长文档深度分析", 3, big)
r2, _ := router.NewRoute("fast", "快速问答", 1, fast)

r, _ := router.NewRouter([]router.Route{r1, r2}, &router.RoundRobinStrategy{})
desc, _ := r.Prompt("证明哥德巴赫猜想")   // 返回 ai.RequestDesc，后续与 ChatModel 完全一致
resp, _ := desc.Call()
```

四策略：`RoundRobinStrategy{}`（轮询）、`WeightedRoundRobinStrategy{}`（nginx 平滑加权）、`NewRuleBasedStrategy(rules)`（`router.NewRule(id, func(ctx) bool)` 规则链）、`NewSmartStrategy(classifierModel)`（用小模型分类选路）。显式标注优先于策略：`prompt.SetAttr(router.AttrRouteID, "big")`。

## 11. 拦截器（Interceptor）

挂载：`Options(func(o *ai.ChatOptions){ o.Interceptors = []ai.Interceptor{...} })`。

```go
type auditInterceptor struct{ ai.InterceptorBase } // 嵌 Base 得默认直通实现

func (a *auditInterceptor) OnPrepare(sess ai.Session, opts *ai.ChatOptions,
    p *ai.Prompt, system *strings.Builder) {
    system.WriteString("\n[审计模式] 所有回答需附依据。") // 追加系统提示段
}
```

可选能力按接口断言启用（不实现即跳过）：

| 接口 | 钩子 | 用途 |
|---|---|---|
| 基础 `Interceptor` | `OnPrepare` | 系统提示注入、选项改写 |
| `CallInterceptor` | `InterceptCall(req, chain)` | 包裹同步调用（重试/审计/熔断） |
| `StreamInterceptor` | `InterceptStream(ctx, req, chain)` | 包裹流式调用 |
| `ToolInterceptor` | `InterceptTool(req, chain)` | 包裹工具执行（权限/限流） |

链式传递：`chain.DoIntercept(...)` 依次穿过启用的拦截器，终点是真实调用——[ai-harness](ai-harness.md) 的权限与压缩拦截器均由此实现。

## 12. Talent SPI 与前端协议适配

**Talent**（能力包，详见 [ai-talent](ai-talent.md)）：`Options` 中挂 `o.Talents = []ai.Talent{...}`，prepare 阶段统一激活——指令以 `# [Talent: name]` 块注入系统提示，工具染色 `talent=<name>` 追加进 `o.Tools`。自定义只需嵌入 `ai.TalentBase` 覆写所需方法：

```go
type wikiTalent struct{ ai.TalentBase }
func (w *wikiTalent) Name() string { return "wiki" }
func (w *wikiTalent) Tools(*ai.Prompt) []ai.FunctionTool {
    return []ai.FunctionTool{
        ai.NewTool("wiki-search").Desc("搜内部维基").
            StringParam("q", "关键词", true).
            Handler(func(ctx context.Context, args map[string]any) (any, error) {
                return "……", nil
            }).Build(),
    }
}
```

**UI 协议输出**（把聊天流转成前端 SSE 协议）：

```go
// Vercel AI SDK parts 协议
http.HandleFunc("/chat", func(w http.ResponseWriter, r *http.Request) {
    aisdk.NewWrapper().WriteStream(r.Context(), w, m.Prompt("hi"),
        map[string]any{"sessionId": "sess-9"})
})
resp, _ := m.Prompt("hi").Call()
aisdk.NewWrapper().WrapResponse(resp, &buf)      // 非流式包装

// AG-UI 协议（智能体事件流见 ai-agent.md）
agui.NewWrapper("th-1", "run-1").WriteStream(ctx, &buf, m.Prompt("hi"))
```

注意两包 `IDGenerator` 签名不同（aisdk 带 prefix 参数）。

## 13. 模块结构

| 文件/目录 | 职责 |
|---|---|
| `message.go` / `content.go` / `prompt.go` | 消息四角色、内容块家族、混合容器、提示 |
| `model.go` / `config.go` / `options.go` / `response.go` | ChatModel 构造与调用链、选项、响应读取面 |
| `stream.go` / `event.go` / `stream_context.go` | 流式三形态与事件常量 |
| `tool.go` / `tool_schema.go` / `tool_reflect.go` | 工具链式声明、Schema 生成、反射工具 |
| `session.go` / `session_file.go` | 内存/NDJSON 文件会话 |
| `dialect.go` + `dialect/*` | 方言注册表与 5 厂商 7 方言实现 |
| `interceptor.go` / `accumulator.go` | 拦截器四接口、流聚积器 |
| `embedding/` `rerank/` `generate/` `router/` | 三个模型子包与多模型路由 |
| `rag/` | 检索增强（独立成篇：[ai-rag](ai-rag.md)） |
| `mcp/` | MCP 桥（独立成篇：[mcp](mcp.md)） |
| `talent/` | 能力包（独立成篇：[ai-talent](ai-talent.md)） |
| `ui/aisdk` `ui/agui` | 前端 SSE 协议包装器 |
| `agent/` `flow/` `harness/` `loop/` | 兄弟模块（[ai-agent](ai-agent.md) / [ai-harness](ai-harness.md)） |

## 14. 总结

- **方言先行**：厂商差异全部压进 Dialect，业务代码只面对 `ChatModel`/`RequestDesc`
- **copy-before-mutate**：prepare 阶段对 Prompt/Options 拷贝后加工，并发调用互不污染
- **工具自动闭环**：`AutoToolCall` + `ReturnDirect` 覆盖"执行-回填-递归-终答"全链路
- **事件即协议**：16 种 ChatEvent 是流式的唯一事实，聚合、桥接、UI 包装都消费它
- **正交扩展点**：Dialect（厂商）、Interceptor（横切）、Talent（能力包）、ToolProvider（外部工具）四条缝互不耦合

### 延伸阅读

- [ai-agent — 智能体（Simple / ReAct / Team）+ ai/flow](ai-agent.md)
- [mcp — MCP 协议双端与 ai 桥](mcp.md)
- [ai-rag — 检索增强生成](ai-rag.md)
- [ai-talent — 18 件能力包](ai-talent.md)
- 架构设计：[../arch/ai/02-chat-core.md](../arch/ai/02-chat-core.md)
