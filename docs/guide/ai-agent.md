# Aifei-Go ai/agent：智能体（Simple / ReAct / Team）+ ai/flow

> **三种递进的智能体形态**：Simple 一问一答、ReAct 推理-行动循环、Team 图驱动多智能体协作（8 种协议），全部实现统一的 `agent.Agent` 接口，可直接接入 flow 图编排。

## 1. 背景与定位

`./ai/agent` 系列模块（独立 go.mod）在 [ai 聊天核心](ai.md)之上构建自主智能体：模型不再只回答一次，而是**多轮推理、调用工具、自我修正、协同分工**。

| 模块 | 定位 | 对标 solon-ai |
|------|------|------|
| `ai/agent`（基座） | `Agent` 接口、Session、Event、重试 | `Agent`/`Agentic` 基类 |
| `ai/agent/simple` | 单步智能体（模型调用 + 提示组装） | `SimpleAgent` |
| `ai/agent/react` | 推理-行动循环智能体 | `ReActAgent` |
| `ai/agent/team` | 多智能体协作（flow 图驱动） | `TeamAgent` |
| `ai/flow` | LLM 流程组件（12 件，YAML 编排） | `flow` AI 扩展 |

## 2. agent 基座：接口、会话与事件

```go
type Agent interface {
    flow.NamedTaskComponent   // Name()/Title()/Run(ctx, node) —— 可直接作 flow 图节点
    Role() string
    RoleFor(ctx flow.Context) string
    Profile() *agent.Profile
    Metadata(ctx flow.Context) map[string]any
    Call(prompt *ai.Prompt, session agent.Session) (*ai.AssistantMessage, error) // prompt==nil 表示续跑
}
```

**Session**（会话）在 `ai.Session` 之上扩展了挂起语义（HITL 人工介入的基石）：

```go
s := agent.NewMemorySession("s1")            // 容量 50
s = agent.NewMemorySessionMax(200, "s1")
s = agent.NewMemorySessionOfContext(ctx)     // 共享 flow 图上下文，自动装进 KeySession

s.Pending(true, "等待人工审批")   // 挂起：内部 ctx.Stop()，图暂停
s.IsPending()                     // true
s.PendingReason()                 // "等待人工审批"
s.Pending(false, "")              // 恢复
```

**Event**（事件流）：`EventKind` 覆盖全形态——`KindSimpleStart/Delta/End`、`KindRunStart/End`、`KindReasonStart/Delta/End`、`KindToolCallStart/End`、`KindActionStart/End`、`KindNodeStart/End`、`KindTeamStart/End`、`KindPlan`、`KindSupervisorDelta`、`KindHITLPending/Decided` 等；消费接口 `agent.EventSink`（`OnEvent(*Event) error`）或函数适配 `agent.EventSinkFunc`。事件可经 `agent.AGUIOf(ev)` 适配为 AG-UI 前端协议（见 [ai.md §12](ai.md)）。

`Event` 载荷字段：`Kind` / `RunID` / `AgentName` / `Text` / `Session` / `Trace` / `ChatEvent`（细粒度增量）、`Message`（终态聚合）、`Meta`（节点事件带 `nodeId`/`nodeTitle`，工具事件带 `toolName`/`callId`）。

**Profile**（能力档案）与 **Metrics**（用量）：

```go
p := agent.NewProfile().
    CapabilityAdd("代码审查", "重构").   // 能力清单
    ConstraintAdd("只读，不修改文件").   // 约束
    ModeAdd("text", "stream").          // 交互模式
    SetStyle("严谨简洁").MetaPut("team", "qa")
p.FormatString("zh")                    // 档案摘要文本（注入协作提示）

m := resp.Metrics()                     // 跨智能体聚合的 token 用量
m.TotalTokens(); m.CacheRate()
m.DeltaSince(marker)                    // 增量统计
```

网络不稳时的统一重试设施（simple 内部即用它，指数退避只对传输类错误：5xx/429/net.Error/EOF）：

```go
task := agent.NewRetryTask()
res, err := task.Run(ctx, func() (T, error) { /* 调用远端 */ return v, nil })
```

## 3. Simple：单步智能体

```go
a, err := simple.NewModelBuilder(model).        // model 为 *ai.ChatModel
    Name("hello").Role("值班助手").
    Instruction("回答要简短").
    Build()

resp, err := a.Prompt("你好").Call()
fmt.Println(resp.Content())

// 轨迹存于 flow 上下文，键为 "__" + name
trace, _ := resp.Context().Get("__hello").(*simple.Trace)
```

Builder 全链式：`Name/Role/Profile/Instruction/SystemPrompt/Model/Handler/InstructionFn/Retry(maxRetries, retryDelayMs)/SessionWindow(n)/OutputKey/OutputSchema/ModelOptions(fn)/Attr/ToolAdd(tools...)/TalentAdd(talents...)/InterceptorAdd(ics...)/ToolContextPut(k, v)/Build`。注意 `Retry` 为**两参必填**（max>=1、delayMs>=500），与 react 的可变参形态不同。

系统提示可用 `PromptBuilder` 组装（默认实现为 `simple.DefaultSystemPrompt()`）：

```go
sp := simple.NewPromptBuilder().
    Role("数据分析师").
    Instruction(func(trace *simple.Trace) string {   // 运行期动态指令
        return "当前任务是：" + trace.OriginalPrompt().LastMessage().Content
    }).
    Build()
a, _ := simple.NewModelBuilder(m).SystemPrompt(sp).Build()
```

simple 拦截器（嵌 `simple.InterceptorBase`）：在 [ai 拦截器](ai.md)之上增加 `OnSimpleStart(trace)` / `OnSimpleEnd(trace)` 两钩子；`Request.Options(fn)` 里经 `o.SimpleInterceptors` 或 `o.SetStreamSink(sink)` 注入。

三类进阶用法：

```go
// 1) 结构化输出 + 泛型回绑
a, _ = simple.NewModelBuilder(m).OutputSchema(simple.OutputSchemaOf[WeatherOut]()).Build()
out, _ := simple.Bind[WeatherOut](resp)

// 2) 无模型的本地 Handler（纯函数智能体，可做路由/校验节点）
a, _ = simple.NewBuilder().Name("local").Instruction("本地处理").
    Handler(func(p *ai.Prompt, s agent.Session) (*ai.AssistantMessage, error) {
        return &ai.AssistantMessage{Text: "本地处理结果"}, nil
    }).Build()

// 3) 接入 flow 图（Agent 实现了 NamedTaskComponent）
g, _ := flow.Create("f", func(s *flow.GraphSpec) {
    s.AddStart("s").LinkAdd("agentNode")
    s.AddActivityNamed(a).LinkAdd("e")   // 节点名取 Agent.Name()
    s.AddEnd("e")
})
flow.NewEngine().Eval(g, flow.NewContext("c1"))
```

流式：`a.Prompt("嗨").Stream(ctx, sink)` / `.StreamChan(ctx)`，事件序列 `KindSimpleStart → KindSimpleDelta(携带 ChatEvent) → KindSimpleEnd(携带聚合消息)`。

## 4. ReAct：推理-行动循环

```go
a, err := react.NewBuilder(model).        // react.NewConfig(nil) 会 panic，模型必填
    Name("weather").
    ToolAdd(ai.NewTool("get-weather").Desc("查天气").
        StringParam("city", "城市", true).
        Handler(func(ctx context.Context, args map[string]any) (any, error) {
            return "晴 26℃", nil
        }).Build()).
    Build()

resp, err := a.Prompt("北京天气怎么样？").Call()
resp.Trace().Route()          // react.IDEnd —— 循环走到终点
resp.Trace().TurnCount()      // 2（推理→行动→终答）
resp.Trace().ToolCallCount()  // 1
```

循环骨架：`reason`（IDReason）→ 模型思考并决定工具 → `action`（IDAction）→ 执行工具回填观测 → 再 reason → 直至给出终答（IDEnd）。两种风格：`StyleNativeTool`（原生 tool_calls，缺省）与 `StyleStructuredText`（`Thought:/Action:` 文本标签，适配不支持函数调用的模型）。

`Options` 缺省值与运行期调整（`Request.Options(fn)` 可多次调用、按序生效）：

| 项 | 缺省 | 调整方法 |
|---|---|---|
| 最大回合 | 8 | `o.SetMaxTurns(n)` / `o.AddMaxTurns(v)` |
| 重试 | 3 次 / 1000ms | `o.SetRetryConfig(max, delayMs)` |
| 会话窗口 | 8 条 | `o.SetSessionWindowSize(n)` |
| 终答重想 | 关 | `o.SetAutoRethink(b)` |
| 结构化输出 | 无 | `o.SetOutputSchema(schema)` |
| 规划模式 | 关 | `o.SetPlanningMode(b)` + `SetPlanningInstructionProvider(fn)` |

`Trace` 常用读取：`RunID()` / `OriginalPrompt()` / `WorkingMemory()` / `Route()`（当前所处节点）/ `FinalAnswer()` / `TurnCount()` / `ToolCallCount()` / `LastReasonMessage()` / `IsAbnormal()` / `Extras()`。

防失控三件与规划模式：

```go
a, _ = react.NewBuilder(m).
    MaxTurns(8).                 // 回合保险丝（缺省 8）；触顶即停
    AutoRethink(true).           // 触顶后追加"重新思考"提示再试一轮
    InterceptorAdd(              // 内置拦截器
        react.NewStopLoopInterceptorOf(4, 6),     // 重复输出熔断
        react.NewToolRetryInterceptorOf(3, 500),  // 工具失败重试
    ).
    PlanningMode(true).          // 行动前先出计划（OnPlan 拦截钩子）
    FeedbackMode(true).          // 人工介入：注入 __ask_for_feedback 工具
    Build()
```

react 拦截器接口提供全生命周期钩子：`OnAgentStart/OnReasonStart/OnReasonRetry/OnReasonEnd/OnPlan/OnActionStart/OnToolCallStart/OnToolCallEnd/OnActionEnd/OnAgentEnd`（嵌入 `react.InterceptorBase` 按需覆写）。

流式事件序（一次带工具的运行）：`RunStart → ReasonStart → ReasonEnd → ActionStart → ToolCallStart(Meta["toolName"]/Meta["callId"]) → ToolCallEnd → ActionEnd → ReasonDelta(细粒度) → RunEnd`。

## 5. Team：多智能体协作（8 种协议）

Team 用 [flow](flow.md) 图承载协作拓扑：**supervisor 模型**（决策者）+ 若干**成员 Agent**（simple/react 实例均可），协议决定图的形状与交接规则。

```go
writer, _ := simple.NewModelBuilder(writerModel).Name("w1").Role("撰写初稿").Build()
reviewer, _ := simple.NewModelBuilder(reviewerModel).Name("w2").Role("复核成稿").Build()

tm, err := team.NewBuilder(supervisorModel).   // 首参即 supervisor 的 *ai.ChatModel
    Name("seq").
    AgentAdd(writer, reviewer).
    Protocol(func(c *team.Config) team.Protocol {
        return team.NewSequentialProtocol(c)
    }).
    Build()

resp, err := tm.Prompt("写一份两章节的报告").Call()
// resp.Trace().Records() 依次来自 w1、w2；Route() 终点为 agent.IDEnd
```

### 5.1 八种协作协议

| 协议工厂 | 拓扑 | 适用 |
|---|---|---|
| `NewSequentialProtocol` | 流水线：成员按注册顺序接力 | 固定工序（起草→复核→润色） |
| `NewHierarchicalProtocol` | supervisor 中心调度（`NewConfig` 缺省） | 动态分派、结果汇总 |
| `NewSwarmProtocol` | 成员自组织 + `__emerge_tasks__` 涌现工具（信息素 `SwarmState`） | 开放式探索 |
| `NewA2AProtocol` | 点对点接力：成员可 `SetTransferRequest(target, payload, memo)` 直接移交 | 专家转接 |
| `NewContractNetProtocol` | 合同网招投标：发布→竞标→中标执行 | 任务竞价分配 |
| `NewBlackboardProtocol` | 黑板共享区：各成员读写同一 `BoardState` | 知识聚合 |
| `NewMarketBasedProtocol` | 市场 EMA 定价（`MarketState`） | 资源调度模拟 |
| `NewNoneProtocol` | 空协议：仅建图不协作 | 测试/自定义图基座 |

协议接口四阶段：`Name()` / `BuildGraph(spec)`（建 flow 图）/ 成员运行期方法（`PrepareAgentPrompt`、`InjectAgentInstruction`、`InjectAgentTools`、`ResolveAgentOutput`、`OnAgentEnd`）/ supervisor 治理方法（`ResolveSupervisorRoute` 等 8 个）+ `OnTeamFinished`。`Builder.GraphAdjuster(fn)` 可在协议建图后微调图结构。内置图节点 id：`team.IDSystem = "system"`、`team.IDSupervisor = "supervisor"`，起点/终点为 `agent.IDStart/IDEnd`。

带状态的协议提供专属状态对象（经 `trace.ProtocolContext()` 存取）：

```go
// Swarm：任务池 + 信息素（蚁群协作轨迹）
swarmState.GetTaskPool(); swarmState.GetPheromones()   // map[string]int

// A2A：成员间点对点移交
a2aState.SetTransferRequest("reviewer", payload, "请复核")  // 当前成员完成后直接移交给 reviewer
a2aState.PendingTarget(); a2aState.LastPayload(); a2aState.TransferCount()

// Blackboard / Market：黑板共享区、市场 EMA 计价
board := team.NewBoardState(); market := team.NewMarketState()
```

Builder 其余旋钮：`SessionWindowSize`（supervisor 会话窗口）、`RecordWindowSize`（协作记录窗口）、`MaxTurns`（协作回合上限）、`RetryConfig(max, delay)`、`FinishMarker`（结构化文本风格时的结束标记）、`DefaultToolAdd`/`DefaultToolContextPut`/`DefaultInterceptorAdd`（给全体成员的默认配置）、`ModelOptions(fn)`（supervisor 模型选项）。

### 5.2 轨迹、事件与拦截器

```go
tr := resp.Trace()                  // *team.Trace
tr.Records()                        // 协作记录：Role/Source(成员名)/Content/Duration
tr.FormattedHistory()               // 格式化协作史（喂给 supervisor 下一轮）
tr.ProtocolContext().Load("swarm_state_obj")  // 协议自定义状态（*team.SwarmState 等）
tr.ProtocolDashboardSnapshot()      // 仪表盘快照文本

// 流式事件：TeamStart → NodeStart/NodeEnd（含 supervisor 节点）→ SupervisorDelta → TeamEnd
ch, _ := tm.Prompt("...").StreamChan(ctx)
```

团队拦截器（嵌 `team.InterceptorBase`）：`OnTeamStart/OnTeamEnd/ShouldSupervisorContinue/OnModelStart/OnModelEnd/OnSupervisorDecision/ShouldAgentContinue/OnAgentEnd`；内置 `team.NewLoopingTeamInterceptor()` 检测"单点复读"与"A-B-A-B 序列嵌套"两类死循环（内容演进与短回复免疫）。

## 6. HITL：挂起与恢复（人工介入）

`FeedbackMode(true)` 后自动注入 `__ask_for_feedback` 工具（ReturnDirect），模型需要人工输入时调用它，返回 `{"reason": ..., "status": "suspended"}`；运行时检出即挂起会话：

```go
tm, _ := team.NewBuilder(sup).Name("fb").FeedbackMode(true).
    AgentAdd(writer).Build()
sess := agent.NewMemorySession("fb-sess")

resp1, _ := tm.Prompt("安排一次出行").Session(sess).Call()
// 模型缺目的地 → resp1.Content() == "请提供目的地城市"（=挂起原因）
sess.IsPending()      // true

resp2, _ := tm.Prompt(nil).Session(sess).Call()   // 空 Prompt 续跑 = 恢复
// resp2.Content() == "交付完成"；sess.IsPending() == false
```

react 同款：`react.FeedbackTool(desc, reasonDesc)`、`react.IsSuspend(text)`、`react.AsSuspendDecision(reason)`。相关事件：`KindHITLPending` / `KindHITLDecided`。

## 7. ai/flow：LLM 流程组件（12 件）

`ai/flow`（包名 `aiflow`）不自带引擎——组件实现 `flow.NamedTaskComponent`，注册进容器后由 [flow 引擎](flow.md) 驱动，天然获得网关、快照、恢复能力。

| 类别 | 组件（`Name()`） | 说明 |
|---|---|---|
| 输入 | `VarInput` `VarCopy` `ConsoleInput` `WebInput` | 变量注入/复制、控制台/HTTP 文本输入 |
| 输出 | `VarOutput` `ConsoleOutput` `WebOutput` | 输出缝：`aiflow.ConsoleWriter`/`WebWriter` 可替换 |
| 模型 | `ChatModel` `EmbeddingModel` `GenerateModel` | 三类模型节点，meta 携带 chatConfig |
| 知识库 | `InMemoryRepository` `RedisRepository` | RAG 仓储节点 |

```go
c := aiflow.RegisterDefaults(flow.NewMapContainer())   // 一次注册 12 件
engine := flow.NewEngine(flow.NewSimpleDriver(flow.WithContainer(c)))

g, _ := flow.GraphFromText(yamlText)   // YAML/JSON 链定义
_ = engine.Eval(g, flow.NewContext("case1"))

// 工具集注册：YAML 中 task: "@工具集名" 引用
aiflow.RegisterToolProvider("search", searchTool, weatherTool)
```

链样例（meta 驱动配置）：

```yaml
- { id: in, type: activity, task: "@VarInput", meta: {message: "你好"}, link: cm }
- { id: cm, type: activity, task: "@ChatModel",
    meta: {systemPrompt: "你是个聊天助手", stream: false,
           chatConfig: {provider: openai, model: gpt-4o, apiUrl: "https://..."}}, link: e }
- { id: e, type: end }
```

`stream: true` 时节点输出 `*aiflow.StreamHandle`（冷启动：首次消费才发请求）：

```go
handle, _ := ctx.Get("message").(*aiflow.StreamHandle)
text, _ := aiflow.InputAsString(handle)   // 聚合流式文本
```

节点事件经 dami 总线广播（`EventFlowNodeStart`/`EventFlowNodeEnd`，载荷 `aiflow.NodeEvent`），可做执行观测。

## 8. 模块结构

| 路径 | 职责 |
|---|---|
| `ai/agent/agent.go` `profile.go` `keys.go` | Agent 接口、能力档案、上下文键 |
| `ai/agent/session.go` `event.go` `trace.go` `retry.go` | 会话与挂起、事件流、Trace/用量、指数退避重试 |
| `ai/agent/simple/` | 单步智能体（agent/request/options/prompt） |
| `ai/agent/react/` | 循环智能体（含 feedback.go HITL 工具、intercept.go 内置拦截器） |
| `ai/agent/team/` | 多智能体（agent/config/protocol + 8 协议文件） |
| `ai/agent/aguiadapt.go` | `AGUIOf`/`AGUIWriteStream` 事件→AG-UI 适配 |
| `ai/flow/register.go` 及组件文件 | 12 组件注册与实现、StreamHandle |
| `_test/ai_agent_test/` `_test/ai_flow_test/` | 黑盒测试（黄金样本搬译自 Java） |

## 9. 总结

- **一个接口三种形态**：`agent.Agent` 统一 Simple/ReAct/Team，且都是 flow 图节点——单体可平滑进化为团队
- **图驱动协作**：Team 协议的本质是"用代码建 flow 图"，拓扑即协议，可观测可恢复
- **保险丝内建**：MaxTurns、StopLoop、ToolRetry、LoopingTeam 四道防线对抗失控与死循环
- **HITL 是一等公民**：`__ask_for_feedback` 工具 + Session.Pending + `Prompt(nil)` 续跑，三行代码接入人工审批
- **配置即流程**：ai/flow 组件让 LLM 调用进入 YAML 编排世界，与业务流程同引擎运转

### 延伸阅读

- [ai — 聊天核心（ChatModel / 工具 / 流式）](ai.md)
- [flow — 流程编排引擎](flow.md)
- [ai-harness — 编码代理容器（ReAct 的工业级组装）](ai-harness.md)
- [mcp — 给智能体接外部工具](mcp.md)
- 架构设计：[../arch/ai/03-agent-design.md](../arch/ai/03-agent-design.md)
