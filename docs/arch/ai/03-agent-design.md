# 智能体设计（ai/agent + ai/flow）

> 本文是「Solon-AI 迁移到 Go」系列的第四篇（智能体与 AI 流编排）。
> 前置：[`00-overview.md`](00-overview.md)、[`01-go-comparison.md`](01-go-comparison.md)、[`02-chat-core.md`](02-chat-core.md)。
> 覆盖：`solon-ai-agent`（Simple/ReAct/Team + 协议 + 会话 + 拦截器）→ **`./ai/agent` 模块**；`solon-ai-flow`（AI 流编排组件）→ **`./ai/flow` 模块**。
> 本篇核心论点：**aifei-go 的 `./flow` 与 solon-flow API 逐字对应，智能体层可以零妥协落地**；列出的全部接缝均已对照 Go flow 现有源码核实。

---

## 0. TL;DR

- `Agent` = `flow.NamedTaskComponent`（Go 侧 `flow/component.go` 已有：`TaskComponent + Name() + Title()`）——智能体天然是图节点，这是 Java 侧最优雅的设计，**原样保留**。
- ReAct 不建图（手写 reason/action 循环，`flow.Context` 作黑板）；Team = 协议构建的图 + `Engine.Eval`（无限步数）；Simple = ChatModel 直包。三形态的 Java 语义全部可等价移植。
- 唯一内部 API 依赖（`FlowContextInternal` 向下转型）在 Go 侧**自然消失**（D7）：`flow.Context` 本就是我方接口，pending 语义 = `Context.Stop()` + 会话标志。
- `solon-ai-flow` 的 12 个组件 = 一组 `flow.NamedTaskComponent` + `@名称` 注册 + 节点 meta 约定，全部落在既有 `flow.Container` 派发机制上；唯一增量需求是按节点缓存构建好的模型实例（D8，已定稿方案 A：flow 增量）。

---

## 1. 模块与包布局

```
ai/agent/                        # 模块 github.com/crazy-airhead/aifei-go/ai/agent
  ├ agent.go                     # Agent 接口 + 默认 Run 实现（flow 节点协议）
  ├ request.go                   # RequestBase + 各具体 Request/Response
  ├ session.go                   # Session（AgentSession 对应物）+ MemorySession/FileSession + 快照
  ├ profile.go                   # Profile / Metadata
  ├ event.go                     # Event（~20 类型常量）+ 事件转发
  ├ trace.go                     # Trace（route 状态）+ Metrics（用量/时长核算）
  ├ simple/                      # SimpleAgent（单轮包装 + 重试 + outputKey）
  ├ react/                       # ReActAgent（推理-行动循环）
  │   ├ options.go task.go       # ReasonTask/ActionTask（maxTurns 保险丝）
  │   └ intercept/               # HITL / ContextCompression / StopLoop / ToolRetry / ToolSanitizer
  ├ team/                        # TeamAgent（图 + 引擎）
  │   ├ protocol/                # 8 协议 + ProtocolBase（4 阶段默认实现）
  │   └ intercept/               # TeamInterceptor → flow.Interceptor 适配
  └ util/

ai/flow/                         # 模块 github.com/crazy-airhead/aifei-go/ai/flow
  ├ component.go                 # Component 接口 + AbsComponent（节点事件 + IO 约定）
  ├ attrs.go                     # 元数据键与上下文键的常量表（单一事实源）
  ├ models/                      # ChatModelCom / EmbeddingModelCom / GenerateModelCom
  ├ inputs/                      # VarInput / VarCopy / ConsoleInput / WebInput
  ├ outputs/                     # ConsoleOutput / VarOutput / WebOutput
  └ repositorys/                 # InMemoryRepositoryCom（Redis P5）
```

依赖：`ai/agent`、`ai/flow` → `ai` + `flow`（+ enjoy：角色模板渲染；dami：经 `flow.Context.EventBus()`）。两模块互不依赖。

---

## 2. 核心抽象

### 2.1 Agent 接口（与 flow 的第一接缝）

```go
package agent

type Agent interface {
    flow.NamedTaskComponent           // Name() + Title() + Run(ctx flow.Context, node *flow.Node) error
    Role() string
    RoleFor(ctx flow.Context) string  // enjoy 按 ctx.Vars() 渲染角色模板（对标 SnEL roleFor）
    Profile() *Profile
    Metadata(ctx flow.Context) map[string]any
}

// 默认 Run 实现（嵌 AgentBase，对标 Agent 接口的 default run）：
//  1. 从 ctx 取/建 Session（保留键 sessionKey = "_SESSION_"）
//  2. 协议拦截（shouldAgentContinue，team 篇）
//  3. 组装本轮 Prompt（协议注入后；保留键 "_PROTOCOL_"）
//  4. 调用具体 agent 的 call 逻辑（虚方法：Base.Call(ctx, prompt, session)）
//  5. Trace 记录 + 向 streamSink 转发 NodeStart/NodeEnd 事件
type AgentBase struct{ /* name/title/role/profile + Call func 注入 */ }
func (b *AgentBase) Run(ctx flow.Context, node *flow.Node) error
```

> 保留键统一收进 `agent/keys.go` 常量表（`_SESSION_`、`_PROTOCOL_`、`_TEAM_TRACE_`、`_PENDING_REASON`、`start`、`end`、`supervisor`、`reason`、`action`），杜绝 Java 侧字符串散落的隐患。

### 2.2 Request / Response（CRTP 消解后的形态）

```go
type RequestBase struct{ /* session + 拦截器 + ctx */ }
func (r *RequestBase) Session(s Session)

// 具体类型上的富 API（调用方静态可见，无接口丢失）：
type SimpleRequest struct{ RequestBase; ag *SimpleAgent }
func (r *SimpleRequest) Call() (*SimpleResponse, error)
func (r *SimpleRequest) CallAsync(ctx context.Context) *dami.Future[*SimpleResponse]   // 形状复用 dami.Future
func (r *SimpleRequest) Stream(ctx context.Context, sink EventSink) error
func (r *SimpleRequest) StreamChan(ctx context.Context) (<-chan *Event, error)

type SimpleResponse struct{ /* trace/session/ctx/metrics/message */ }
func (r *SimpleResponse) Message() *ai.AssistantMessage
func (r *SimpleResponse) Content() string
func Bind[T any](r *SimpleResponse) (T, error)      // ReAct/Team Response 同款包级泛型
```

### 2.3 Session（D7：无需 flow 内部 API）

```go
type Session interface {
    ai.Session                                     // 消息窗口（继承 chat 会话）
    UpdateSnapshot()                               // 把 flow 上下文钉进快照（对标 updateSnapshot）
    Context() flow.Context                         // 快照的 flow 上下文（默认 NewContext(id)）
    Pending(pending bool, reason string)           // true → Context().Stop()（挂起整个图执行）
    IsPending() bool
    PendingReason() string
}
func NewMemorySession(id ...string) Session
func NewMemorySessionOfContext(ctx flow.Context) Session   // 对标 of(FlowContext)：把 _SESSION_ 放进 ctx
```

Java 侧 `AgentSession` 调 `FlowContextInternal.stop()`；Go 侧 `flow.Context.Stop()` 是公开方法（已核实），**语义等价且零内部依赖**。快照序列化 = `flow.Context.ToJSON()` + 会话消息（文件会话落盘）。

---

## 3. SimpleAgent（单轮智能体）

```go
func New(model *ai.ChatModel) *SimpleAgentBuilder
// Builder：Name/Role/Profile/Instruction/SystemPrompt/Retry(maxRetries, delayMs)/
//          SessionWindow(n)/OutputKey(k)/OutputSchema(schema|Bind[T])/TalentAdd/ToolAdd/InterceptorAdd
func (b *SimpleAgentBuilder) Build() *SimpleAgent
func (a *SimpleAgent) Prompt(p any) *SimpleRequest        // string | *ai.Prompt
```

执行语义（对标 `SimpleAgent.call`）：

1. 取会话窗口 `LatestMessages(windowSize)` 拼 Prompt；
2. 重试壳（`callWithRetry`：仅对传输类 error 重试，业务 ERROR 不重试）；
3. `OutputSchema` → 方言 `PrepareOutputSchemaInstruction` 注入系统提示；
4. `OutputKey` → 结果写 `ctx.Put(outputKey, content)`；
5. **流式路径**：`Stream` 存在时，`ai.CallStream` 逐事件转发为 `Event{SimpleDelta}`，终态 `RESPONSE_END` 聚合成 Response（对标 `takeUntil + reduce + block` 的 Go 直译——同步聚合，回调转发）。

---

## 4. ReActAgent（推理-行动循环）

```go
func New(model *ai.ChatModel) *ReActAgentBuilder
// Builder：Style(NativeTool)/MaxTurns(8)/AutoRethink(false)/FinishMarker/
//          OutputSchema/PlanningMode/FeedbackMode/MaxRetries/RetryDelay/SessionWindow/
//          TalentAdd/ToolAdd/InterceptorAdd
func (a *ReActAgent) Prompt(p any) *ReActRequest
```

**循环引擎**（对标 `evalDo`，无图）：

```go
for {
    if session.IsPending() { break }                 // HITL 挂起
    switch trace.Route() {
    case routeReason: reasonTask.Run(trace, ctx)     // 调模型；有 ToolCall → route=action
    case routeAction: actionTask.Run(trace, ctx)     // 执行工具；完成 → route=reason
    default: return                                  // 终态
    }
}
```

- `ReasonTask`：组装系统提示（角色+工具说明+输出 schema）→ `ai.RequestDesc.Stream` → 把 ChatEvent 流转发为 agent Event（`ReasonStart/Delta/End`、`ToolCallStart/End`）；终态若有 ToolCall 写 `trace.SetRoute(action)`。**maxTurns 保险丝**：`turn > maxTurns` → `SetFinalAnswer("Agent error: Maximum turns reached")`；`autoRethink` 在 80% 阈值放宽上限（Java 同款）。
- `ActionTask`：顺序执行 ToolCall（`FunctionTool.Call(ctx,...)`），构造 tool 消息入会话，`SetRoute(reason)`。
- 会话窗口：每轮 reason 前 `LatestMessages(sessionWindowSize)` 截断。
- **拦截器**（react/intercept，接口 + base 默认实现）：

| 拦截器 | 职责 | Go 契约要点 |
|--------|------|-------------|
| HITLInterceptor | 高危工具人工审批 → `session.Pending(true, reason)` 挂起图 | `Strategy` 接口：`Decide(call) (Decision, error)`；`Allow/Deny/Pending` 三态 |
| ContextCompressionInterceptor | 窗口压缩（层级/关键信息/LLM/向量库/组合 5 策略） | `CompressionStrategy` 接口；P0 仅层级（旧消息摘要折叠），LLM 策略 P1 |
| StopLoopInterceptor | 相似度检测死循环 | 轮末内容指纹比对 |
| ToolRetryInterceptor | 工具失败重试 | 次数 + 退避 |
| ToolSanitizerInterceptor | 工具入参/出参清洗 | 函数对（`SanitizeIn/SanitizeOut`） |

---

## 5. TeamAgent（多智能体 = flow 图）

### 5.1 构建与执行（与 Go flow 的逐点对照）

| Java（solon-ai-agent） | Go（./flow 已核实存在） |
|------------------------|--------------------------|
| `FlowEngine.newInstance(true)`（简化驱动） | `flow.NewEngine()`（默认 SimpleDriver：enjoy 求值 + MapContainer） |
| `Graph.create(traceKey, spec -> { protocol.buildGraph(spec); adjuster.accept(spec); })` | `flow.Create(traceKey, func(spec *flow.GraphSpec){ protocol.BuildGraph(spec); adjuster(spec) })` |
| `spec.addStart("start").linkAdd("supervisor")` | `spec.AddStart("start").LinkAdd("supervisor")` |
| `spec.addActivity(agent)`（Agent 即 NamedTaskComponent） | `spec.AddActivityNamed(agent)`（接口已核实逐字对应） |
| `spec.addExclusive(new SupervisorTask(...))` | `spec.AddExclusiveNamed(supervisorTask)` |
| `ns.linkAdd(agentName, l -> l.when(ctx -> ...))` | `ns.LinkAddConfig(agentName, func(l *flow.LinkSpec){ l.WhenCond(flow.ConditionFunc(func(ctx flow.Context)(bool,error){ ... })) })` |
| `flowEngine.eval(graph, -1, context, flowOptions)` | `engine.Eval(graph, ctx)`（默认无限步；或 `EvalWithSteps`） |
| TeamInterceptor → `FlowOptions.interceptorAdd` | `engine.AddInterceptor(ic flow.Interceptor, idx...)` |
| `context.trace().recordNode(graph, null)` | flow 引擎自动维护 `Trace`/`NodeRecord`（已有） |

### 5.2 协议 SPI（4 阶段生命周期，base struct 提供默认）

```go
type Protocol interface {
    Name() string
    BuildGraph(spec *flow.GraphSpec)                       // 阶段1：静态拓扑

    // 阶段2：成员 agent 运行时
    InjectAgentTools(ctx flow.Context, ag Agent, recv func(ai.FunctionTool))
    InjectAgentInstruction(ctx flow.Context, ag Agent, sb *strings.Builder)
    PrepareAgentPrompt(trace *Trace, ag Agent, p *ai.Prompt) *ai.Prompt  // 必须 p.Copy() 后再改
    ResolveAgentOutput(trace *Trace, ag Agent, content string) string
    OnAgentEnd(trace *Trace, ag Agent)

    // 阶段3：supervisor 治理
    InjectSupervisorTools(ctx flow.Context, recv func(ai.FunctionTool))
    PrepareSupervisorInstruction(ctx flow.Context, trace *Trace, sb *strings.Builder)
    ShouldSupervisorExecute(ctx flow.Context, trace *Trace) bool   // false=跳过 LLM 决策（固定流）
    ResolveSupervisorRoute(ctx flow.Context, trace *Trace, decision string) string // ""=默认解析
    OnSupervisorRouting(ctx flow.Context, trace *Trace, next string)

    // 阶段4：收尾
    OnFinish(ctx flow.Context, trace *Trace)
}
type ProtocolBase struct{}                                  // 全部默认实现，嵌入者按需覆写
```

**8 协议迁移优先级**（07 分期）：P2-c 先 `Hierarchical`（主管派发，最有代表性）+ `Sequential`（固定顺序，验证静态拓扑）+ `Swarm`（对等移交）；`A2A/ContractNet/Blackboard/MarketBased/None` P2-c 尾或 P5（同构性高，增量补）。

> **copy-before-mutate 铁律**（Java javadoc 明示，Go 更易踩）：`PrepareAgentPrompt` 收到的 Prompt 必须先 `Copy()`——Go 的 slice/map 是引用类型，直接追加会跨 agent 串写。测试固化该用例。

### 5.3 Supervisor 路由

- 默认实现：supervisor 是一个 `AddExclusiveNamed` 的决策节点（内部调 ChatModel，输出下一个 agent 名）→ 边 `when` 按 `trace.Route()` 比对（大小写不敏感，Java 同款）。
- `ShouldSupervisorExecute() == false`（如 Sequential）→ 直接静态边，零 LLM 开销。

---

## 6. 事件模型（agent.Event）

Java ~20 个事件类（Simple/Run/Reason/ToolCall/Action/Node/Team/Plan/ContextSize/HITL/Supervisor 家族）→ **单结构体 + 类型常量**（与 ChatEvent 同风格）：

```go
type EventKind uint8
const (
    KindSimpleStart EventKind = iota + 1; KindSimpleDelta; KindSimpleEnd
    KindRunStart; KindRunEnd
    KindReasonStart; KindReasonDelta; KindReasonEnd
    KindToolCallStart; KindToolCallEnd
    KindActionStart; KindActionEnd
    KindNodeStart; KindNodeEnd
    KindTeamStart; KindTeamEnd
    KindPlan; KindContextSize
    KindHITLPending; KindHITLDecided
    KindSupervisorDelta
)
type Event struct {
    Kind      EventKind
    RunID     string
    AgentName string
    Text      string           // Reason/Supervisor delta 文本
    Session   Session
    Meta      map[string]any   // ToolCall 明细、Plan 结构、HITL 决策等
}
type EventSink interface{ OnEvent(*Event) error }
```

**ChatEvent → agent.Event 的映射集中在每类 agent 的一处**（ReasonTask/SimpleAgent 转发器、AgentBase 的 Node 事件），不散落（Java 同构，D10）。

---

## 7. D8：flow 的 per-node 附件缓存（定稿：方案 A）

solon-ai-flow 的 `ChatModelCom` 把按节点 meta 构建好的 `ChatModel` 缓存在公有字段 `node.attachment`，避免每次执行重复构建（解析 chatConfig、建 httpx 客户端）。Go 侧 `flow.Node` 目前只有只读 metas。曾列两案：

| 方案 | 做法 | 代价 |
|------|------|------|
| **A（已定稿）：flow 增量** | `flow.Node` 增 `Attachment(key string) any` + `SetAttachment(key string, v any)`（内部 `sync.RWMutex` 保护的 map，序列化忽略） | 动 flow 一处（向后兼容，dataisolate 给 db 加 5 项增量的同款先例）；语义与 Java 对齐度最高 |
| B：组件内缓存 | 组件 struct 持 `sync.Map`，键 = `graphID + ":" + nodeID` | flow 零改动；但图卸载后缓存泄漏需自行清理，且多引擎实例间不共享语义 |

**2026-10-08 审核定稿方案 A**：`flow.Node` 增加 `Attachment/SetAttachment`。实施约束照 07 §5 P2-c——单独成提交、flow 全量回归；方案 B 保留为文档备选（契约不变，仅实现位置），不再作为并行选项。

---

## 8. ai/flow：AI 流编排组件（solon-ai-flow 对应物）

### 8.1 组件模型

```go
package aiflow

type Component interface {
    flow.NamedTaskComponent                  // @名称 注册 + Run(ctx, node)
    Description() string
}
type ComponentBase struct{}                  // 默认实现：节点事件 + IO 约定
// AbsAiComponent 对应物：Run 包一层
//   ctx.EventBus() 发送 "flow.node.start" / "flow.node.end"（话题与 Java 一致，payload NodeEvent{Ctx, Node}）
//   再调 DoRun(ctx, node)

// IO 约定（AiIoComponent 对应物，嵌入 ComponentBase）：
//   输入：node meta "input" 指定的变量名，缺省取上下文键 message
//   输出：node meta "output" 指定的变量名，缺省写回输入名
//   属性：上下文键 property_<name> 的列表累积（AiPropertyComponent 对应物）
```

### 8.2 组件清单（12 个，全量迁移）

| 组件 | 节点 meta 关键项 | 行为 |
|------|------------------|------|
| `inputs.VarInput` | `message`（或任意名） | 常量 → 上下文变量 |
| `inputs.VarCopy` | `var2: message` | 变量复制 |
| `inputs.ConsoleInput` | prompt 文案 | 阻塞 stdin 读一行 |
| `inputs.WebInput` | url/headers | httpx GET → message |
| `outputs.ConsoleOutput` | — | 打印输入变量 |
| `outputs.VarOutput` | var 名 | 输入写指定变量（透传） |
| `outputs.WebOutput` | stream/headers | 把结果作为 SSE/JSON 响应写出（经 `io.Writer` 缝注入） |
| `models.ChatModelCom` | `systemPrompt`（enjoy 渲染）/`stream`/`chatConfig`/`toolProviders`/`mcpServers`/`chatSession` | 构建或复用 ChatModel（D8 附件缓存）；输入 string/Message/Prompt；`stream:true` 时输出事件流句柄，否则 `*ai.Response` |
| `models.EmbeddingModelCom` | embedConfig | 同构 |
| `models.GenerateModelCom` | genConfig | 同构（P5） |
| `repositorys.InMemoryRepositoryCom` | docs | 内存仓储节点（RAG 链） |
| `repositorys.RedisRepositoryCom` | redisConfig | P5（cache 通道） |

`mcpServers` meta → `aimcp.Providers`（P3 之后启用；组件接口先留）。`toolProviders` → 类名实例化改为**注册表按名取**（`aiflow.RegisterToolProvider(name, provider)`；Go 无类反射，注册式即可）。

### 8.3 YAML 契约（与 solon-ai-flow 逐字兼容为目标）

```yaml
id: chat_case1
layout:
  - task: "@VarInput"
    meta: {message: "你好"}
  - task: "@VarCopy"
    meta: {var2: message}
  - task: "@ChatModel"
    meta:
      systemPrompt: "你是客服助手"
      stream: false
      chatConfig: {provider: ollama, model: "qwen2.5:1.5b", apiUrl: "http://localhost:11434"}
  - task: "@ConsoleOutput"
```

解析直接用 `flow.GraphFromText`（已有）；组件经 flow 的 `Container`（`@名称` 派发）解析——**零新解析器**。注册入口：`aiflow.RegisterDefaults(container)` 把 12 组件装进 `flow.MapContainer`。

---

## 9. 与 server 的集成（SSE 端点）

聊天/智能体流的对外暴露（对标 Solon `@Produces(TEXT_EVENT_STREAM)` 控制器）：

- 首选：`server` 侧加一个轻量 `SSE` 帮助类型（`http.Flusher` + `text/event-stream` 头 + `WriteEvent(name, data)`），普通 handler 内直接把 `StreamChan` 泵给客户端；
- MCP/MCP 式**原生语义端点**（POST+GET+DELETE、非 `{code,msg,data}` 信封）走 `server.WithHTTPHandler(...)` 裸通道（swagger 先例，04 展开）；
- `outputs.WebOutput` 经注入的 `http.ResponseWriter` 缝工作，不直接耦合 server 包。

---

## 10. 实现顺序建议（对齐 07 的 P2）

1. AgentBase + Session + 事件 + Trace/Metrics（纯类型 + 内存测试）→ 2. SimpleAgent（含 retry/outputKey/流转发）→ 3. ReAct（Reason/Action 两 Task + maxTurns）+ StopLoop/ToolRetry → 4. flow 增量 D8（方案 A）→ 5. Team + ProtocolBase + Sequential → 6. Hierarchical（supervisor 决策节点）+ Swarm → 7. HITL（挂起/恢复：快照持久化联测）+ ContextCompression（层级策略）→ 8. ai/flow 组件（Var 族 → ChatModelCom → 输出族 → 仓储族）+ YAML 兼容用例（chat_case1 逐字搬）。
