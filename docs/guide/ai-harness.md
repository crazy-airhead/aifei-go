# Aifei-Go ai/harness：编码代理容器 + 持久循环 + 沙箱

> **把 ReAct 智能体组装成"能干活的编码代理"**：harness 提供多模型注册、子智能体目录、权限审批、上下文压缩、自定义命令；loop 提供目标驱动的长时循环策略；sandbox 给每一次工具执行套上系统级隔离。

## 1. 背景与定位

[react 智能体](ai-agent.md)解决"怎么循环"，但离生产级编码代理还差四块：**多模型与子智能体管理、工具权限审批、长上下文压缩、命令扩展**。`ai/harness` 把这些组装成 `Engine`（对标 solon-ai 的 ChatHarness / codeharness 思路）；配套两兄弟模块：

- `ai/loop`：目标驱动循环引擎——Ralph（自省迭代）、Team（阶段管道）、UltraQA（质量门禁）三种策略
- `sandbox`：macOS Seatbelt / Linux bubblewrap / Windows WFP 的命令包装器，网络与文件系统白名单隔离

三个模块均为独立 go.mod，可单独使用，组合即是完整的自主编码代理。

## 2. harness Engine：组装与运行

```go
e, err := harness.NewEngine(
    harness.WithWorkspace("/project"),           // 工作目录
    harness.WithModel(mainModel, "main"),        // 注册模型；首个即缺省
    harness.WithModel(fastModel, "fast"),
    harness.WithTools(myTools...),               // 自定义工具
    harness.WithTalent(file.NewReadWrite("/project")),  // 挂能力包（见 ai-talent.md）
    harness.WithDefaultTools("read", "shell"),   // 工具白名单（缺省 ["*"] 全放行）
    harness.WithMaxTurns(20),                    // 回合保险丝（缺省 20）
    harness.WithPermissionRule(                  // 权限规则（见 §5）
        harness.AllowRule("read"),
        harness.DenyPattern("shell", "rm -rf*"),
    ),
)

answer, err := e.Call(ctx, "修复 main.go 里的空指针")
// 或走完整请求链：e.Prompt(input) 返回 *react.Request（含重试/窗口配置）
```

构造期默认装配：`TaskTalent`（子任务派发，Name `"task"`）+ `GenerateTalent`（Name `"generate"`）+ 压缩拦截器（见 §6）。`WithAgentCatalogDir` 指定子智能体目录，`WithCommandDir` 指定 markdown 命令目录，`WithExtension` / `AddExtension(ext)` 是总扩展缝。

其余选项：`WithSystemPrompt(sp)`（覆写主智能体系统提示）、`WithDefaultModel(name)` / `AddModel(name, m)` / `SetDefaultModel(name)`（运行期模型管理）、`WithDisallowedTools(...)`（黑名单，与白名单叠加）、`WithHitlEnabled(on)`（缺省关）、`WithSessionWindowSize(n)`（缺省 12）、`WithMultitaskMax(n)`（并发子任务上限，缺省 5）。

Engine 访问器：`AgentManager()` / `CommandRegistry()` / `PermissionInterceptor()` / `CompressionInterceptor()` / `TaskTalent()` / `GenerateTalent()` / `MainAgent()`（主 react.Agent） / `AllowTool(name)` / `DisallowTool(name)`；便捷调用 `e.Call(ctx, input)`（一问一答）与 `e.Prompt(input)`（完整请求链，内置 MaxRetries=3 + maxTurns + sessionWindowSize）。

## 3. 子智能体：定义、目录与内置五件

```go
// 方式一：markdown 定义（frontmatter 元信息 + 正文即系统提示）
def, err := harness.ParseAgentMarkdown(content)
defs, err := harness.BuiltinAgents()   // explore / general / plan / bash / git-summary

// 方式二：代码构建
def := harness.NewDefinitionBuilder("reviewer").
    Description("代码审查专家").
    SystemPrompt("你只输出问题清单……").
    ToolsAdd("read", "grep").
    Hidden(false).Primary(false).Build()

agent, err := e.GetAgentOrMain("reviewer")   // 未命中回退主智能体
builder := e.GetAgentBuilder(def, "main")    // 取 react.Builder 再精调
```

目录管理：`harness.NewAgentManager(catalogDir)` 支持 `AddAgentIfAbsent` / `Get` / `Agents` / `InvalidateCache`（文件变更后失效重载）；`e.AgentManager()` 取引擎内置管理器。子智能体由主智能体经 `task` 工具按名字调度——探索归 explore、规划归 plan，各用各的模型与工具集。

内置五件（`harness.BuiltinAgents()`，挂载点常量 `harness.BuiltinAgentMount = "@harness-agents"`）：

| 子智能体 | 分工 |
|---|---|
| `explore` | 只读探索代码库，汇总事实 |
| `general` | 通用子任务执行 |
| `plan` | 制定实施计划（只规划不动手） |
| `bash` | 专注命令行操作 |
| `git-summary` | 变更梳理与提交信息生成 |

## 4. 自定义命令（CommandRegistry）

```go
r := harness.NewCommandRegistry()
r.Register(myCommand)             // 实现 Name/Description/Examples/Model/CliOnly/
                                  // RunnableWhenBusy/Execute(ctx CommandContext)
r.LoadDir("/project/.commands")   // markdown 命令：ParseMarkdownCommand(name, content)

cmd := r.Find("/review")
for _, name := range r.Names() { ... }
```

markdown 命令即"斜杠命令"：frontmatter 声明描述/示例/目标模型，正文为提示模板——团队约定以文件形式沉淀进仓库。

## 5. 权限与 HITL：工具执行的门禁

```go
ctx := harness.NewPermissionContext().
    WithWorkingDirectory("/project").
    AddRule(
        harness.AllowRule("read"),                    // 放行
        harness.AskRule("shell"),                     // 每次询问（HITL）
        harness.DenyPattern("shell", "curl http*"),   // 模式拒绝（glob）
        harness.AllowPattern("write", "src/**"),      // 只许写 src
    )
engine := harness.NewPermissionEngine()
decision := engine.Evaluate("shell", args, ctx, defaultDecision)
// decision.Behavior() ∈ {BehaviorAllow, BehaviorDeny, BehaviorAsk}
```

规则要素：`ToolName` + `Behavior` + `Pattern`（glob）+ `Matcher`（自定义匹配器）+ `Prompt`（询问文案）+ `Priority`（`WithPriority` 覆写），`WithPrompt` 定制审批提示。

接入 react 的拦截器形态：

```go
p := harness.NewPermissionInterceptor().
    OnApproved(func(toolName string, args map[string]any) bool {   // Ask 的裁决回调
        return ui.Confirm("允许执行 " + toolName + "?")
    }).
    OnTool("shell", harness.NewBashToolStrategy(func() *harness.PermissionContext { return ctx })).
    OnTool("write", harness.NewWriteToolStrategy("write", func() *harness.PermissionContext { return ctx })).
    OnTool("webfetch", harness.NewWebToolStrategy("webfetch", func() *harness.PermissionContext { return ctx }))
// 挂进 react.Builder.InterceptorAdd(p)；e.PermissionInterceptor() 取引擎内建实例
```

## 6. 上下文压缩：长会话的续命机制

```go
type CompressionStrategy interface {
    Compress(model *ai.ChatModel, maxRetries int, trace *react.Trace, msgs []*ai.Message) *ai.Message
}
```

内置三实现：`KeyInfoExtractionStrategy`（关键信息抽取）、`HierarchicalCompressionStrategy`（分层摘要）、`CompositeStrategy{Strategies: [...]}`（串联）。引擎默认装配 `Composite[KeyInfo, Hierarchical]`，阈值 `MaxMessages=100` / `MaxContextRatio=0.75`：

```go
c := e.CompressionInterceptor()
c.SetConfig(func(cfg *harness.CompressionInterceptorConfig) {
    cfg.MaxMessages = 60
    cfg.MaxContextRatio = 0.6
}).SetTokenCounter(myCounter)   // 精确 token 计数（缺省近似）
```

触发时机：prepare 阶段检测会话超限 → 策略生成压缩摘要消息替换历史窗口 → 原 Prompt 保留在 Trace。

## 7. ai/loop：目标驱动的长时循环

`ai/loop` 与 harness 正交：任何"反复执行直到达标"的任务都可套循环引擎。

```go
e := loop.NewEngine(nil)   // nil = 缺省配置（清理 300s、状态过期 1h）
s, err := e.Start(&loop.Config{
    TaskDescription: "让测试全绿",
    Strategy:        loop.NewUltraQAStrategy(),   // 必填
    Validator:       nil,                         // nil = AcceptAllValidator
    MaxIterations:   50,                          // 保险丝；0 = 首轮即停
    VerificationRequired: true,
})
result := s.WaitForCompletion()   // 阻塞至终态；也可轮询 s.State()/IterationCount()/SuccessRate()
e.Pause(sessionID); e.Resume(sessionID); e.Stop(sessionID)
```

会话状态机：`Idle → Planning → Executing → Verifying → Fixing → Completed/Failed`（另有 `Paused`）：

```text
Idle ──► Planning ──► Executing ──► Verifying ──通过──► Completed
              ▲            │            │不通过
              └──────── Fixing ◄────────┘          Paused（人工暂停，可 Resume）
                                    │连续修复失败
                                    ▼
                                 Failed
```

`Session` 全貌：`ID()` / `State()` / `Context()` / `IsRunning()` / `IterationHistory()` / `IterationCount()` / `SuccessRate()` / `ShouldContinue()` / `ExecuteIteration()` / `Pause()` / `Resume()` / `Stop()` / `WaitForCompletion()`。

自定义 Validator 示例（ultra-qa 的门禁本质）：

```go
crit := loop.StrictCriteria("测试门禁",
    "所有测试通过", "无竞态警告", "覆盖率不下降")
res := crit.Validate(ctx, session)   // PassedResult / FailedResult / NeedsFixResult
```

引擎治理：`EngineConfig{StateManager, MonitoringEnabled, DebuggingEnabled, CleanupIntervalSecs, StateExpirationMs}`（缺省 300s 清理、1h 过期）；`SetProjectRootDirectory(dir)` 供策略解析项目路径。

### 7.1 三种内置策略

| 策略 | 节奏 | 要点 |
|---|---|---|
| `NewRalphStrategy()`（"ralph"，50 轮） | 拆故事→实现→自省→再迭代 | `SetImplementor(fn)` 注入执行体；`SetCriticMode(CriticArchitect/CriticPeer)` 自省角色；`InjectPrdComponents(prd, disk)` 喂 PRD |
| `NewTeamStrategy()`（"team-pipeline"，100 轮） | PLAN→PRD→EXEC→VERIFY→FIX 阶段管道 | maxFix=3 后升级失败 |
| `NewUltraQAStrategy()`（"ultra-qa"，100 轮） | 改→跑门禁→未过再改 | 内置 `BuildGate()+TestGate()`；`SetExecutor(e)` 自定义命令执行（缺省真实进程）；GoalType 可切 GoalBuild/GoalLint/GoalTypeCheck/GoalCustom |

Validator 工具箱：`AcceptAllValidator{}` / `SimpleCriteria(name, requirements...)` / `StrictCriteria(...)` / 结果构造 `PassedResult` / `FailedResult` / `NeedsFixResult` / 门禁 `BuildGate()` / `TestGate()` / `LintGate()`。

### 7.2 集成桥：循环 × agent/flow/harness

```go
ai_ := loop.NewAgentIntegration(loopEngine)
s, _ := ai_.StartAgentDrivenRalphLoop("重构支付模块", myReactAgent)  // agent 做实现体

fi := loop.NewFlowIntegration(loopEngine)
s, _ = fi.StartFlowDrivenPipeline("flow-1", strategy)   // flow 图做执行体

hi := loop.NewHarnessIntegration(loopEngine)
s, _ = hi.StartHarnessDrivenUltraQA("修 CI", caller)    // harness.Engine 做实现体
// caller 实现 HarnessCaller（ExecuteWithHarness(prompt) string）
```

三桥统一模式：注入实现体（`AgentBridge` / `FlowBridge` / `HarnessBridge`）+ 便捷启动方法，把循环策略嫁接到既有执行设施上。

## 8. sandbox：命令级系统隔离

`sandbox.Manager` 把任意 shell 命令包装成受控执行（macOS `sandbox-exec`、Linux `bubblewrap`、Windows WFP）：

```go
m, err := sandbox.New(sandbox.RuntimeConfig{
    Network: &sandbox.NetworkConfig{       // nil = 不启用网络隔离；空列表 = 全拒
        AllowedDomains: []string{"api.internal", "proxy.corp"},
    },
    Filesystem: &sandbox.FilesystemConfig{ // 读 = deny-then-allow-back、写 = allow-only
        // 按需配置读写路径
    },
})
if deps := m.CheckDependencies(); deps.HasErrors() { /* 缺系统依赖 */ }

wrapped, err := m.WrapWithSandbox("go test ./...", "", nil)   // 得到可执行的安全命令串
httpPort, socksPort, err := m.StartProxyServers()             // 域名过滤代理（HTTP+SOCKS）
m.UpdateConfig(newCfg)   // 热更新：网络规则活切换；文件系统需 Reset 重建
```

要点：

- **fail-closed**：`RuntimeConfig` 全零值即最严配置；未命中域名规则的请求走 `WithAskCallback` 交互裁决（未设则拒绝）
- **违规审计**：`WithViolationStore(s)` 记录越权访问；`WithFilterRequest(cb)` 过滤请求
- **路径工具**：`GlobToRegex` / `ExpandGlobPattern` / `NormalizePathForSandbox` / `ShellQuote` 等包级函数处理通配与转义

`RuntimeConfig` 主要字段：

| 字段 | 作用 | 缺省 |
|---|---|---|
| `Network.AllowedDomains` / `DeniedDomains` | 域名白/黑名单（nil=不启用隔离、空=全拒） | 全拒 |
| `Filesystem` | 读（deny-then-allow-back）/写（allow-only）路径集 | 最严 |
| `MandatoryDenySearchDepth` | 强制拒绝规则的目录扫描深度 | 3 |
| `Ripgrep` | rg 检索是否放行及参数 | 严格 |
| `Seccomp`（Linux）/ `Windows`（WFP） | 平台专属强化 | 关 |
| `BwrapPath` / `SocatPath` | Linux bubblewrap 与 socat 路径覆盖 | 自动探测 |

平台矩阵：macOS（`sandbox-exec` + 代理）／Linux（bubblewrap + seccomp）／Windows（WFP 过滤驱动）；`WrapWithSandboxArgv(command, binShell, customConfig)` 返回 `*SpawnSpec`（argv 形态，免 shell 注入），`WrapWithSandbox` 返回命令串形态。

与 harness 的配合：把 `execute_shell`/`bash` 类工具的 Handler 内改为 `manager.WrapWithSandbox(cmd)` 后执行，模型的所有命令天然受网络/文件系统白名单约束。

## 9. 组合：一个自主编码代理的最小骨架

```go
// 模型
mainModel, _ := ai.NewChatModel(cfgMain)

// 沙箱
sb, _ := sandbox.New(sandbox.RuntimeConfig{Network: &sandbox.NetworkConfig{
    AllowedDomains: []string{"proxy.golang.org"},
}})

// 循环 × 质量门禁
le := loop.NewEngine(nil)
hi := loop.NewHarnessIntegration(le)

// harness 主体
e, _ := harness.NewEngine(
    harness.WithWorkspace(repo),
    harness.WithModel(mainModel, "main"),
    harness.WithTalent(file.NewReadWrite(repo)),
    harness.WithTalent(sys.NewShell(repo)),          // Handler 内经 sb 包装
    harness.WithPermissionRule(harness.AskRule("execute_shell")),
)
sess, _ := hi.StartHarnessDrivenUltraQA("修复 failing_test.go 导致的 CI 失败",
    callerOf(e))
_ = sess.WaitForCompletion()
```

## 10. 模块结构

| 路径 | 职责 |
|---|---|
| `ai/harness/engine.go` `factory.go` | Engine 装配、react.Builder 工厂 |
| `ai/harness/definition.go` `manager.go` `agents/*.md` | 子智能体定义/目录/内置五件 |
| `ai/harness/command.go` | CommandRegistry 与 markdown 命令 |
| `ai/harness/permission.go` `hitl.go` | 规则引擎、Ask 审批、三大工具策略 |
| `ai/harness/compress.go` | 三压缩策略与拦截器 |
| `ai/harness/talent.go` | TaskTalent / GenerateTalent |
| `ai/loop/{engine,state,strategy}.go` | 循环引擎、8 态状态机、策略 SPI |
| `ai/loop/{ralph,team,ultraqa}.go` `validator.go` | 三策略与门禁 |
| `ai/loop/integration.go` `autopilot.go` | 三集成桥 |
| `sandbox/{sandbox,config,platform}.go` | 包装器、RuntimeConfig、平台探测（含 Windows WFP） |

## 11. 总结

- **Engine 即产品**：harness 把"多模型 + 子智能体 + 命令 + 权限 + 压缩"装成一个可对话的编码代理容器
- **权限三态**：Allow / Deny / Ask（HITL），Pattern 级白黑名单 + 优先级，fail-safe 默认
- **循环与执行解耦**：loop 只定义"何时停、何为过"（策略+门禁），执行体经三桥注入 agent/flow/harness
- **压缩保长命**：KeyInfo+Hierarchical 复合策略在 100 条/75% 上下文阈值自动触发
- **纵深防御**：应用层权限（harness）之下还有系统层隔离（sandbox 网络/文件白名单）

### 延伸阅读

- [ai-agent — ReAct 智能体（harness 的内核）](ai-agent.md)
- [ai — 聊天核心（拦截器四接口）](ai.md)
- [ai-talent — file/sys 等 26 件能力包](ai-talent.md)
- [flow — 流程编排（loop 的 FlowBridge 依赖）](flow.md)
- 架构设计：[../arch/ai/06-talents-and-extended.md](../arch/ai/06-talents-and-extended.md)
