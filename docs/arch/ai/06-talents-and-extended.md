# Talents 与扩展能力设计（ai/talent、sandbox、harness、loop、ui、ACP）

> 本文是「Solon-AI 迁移到 Go」系列的第七篇（能力包与高级编排）。
> 前置：[`00-overview.md`](00-overview.md)、[`02-chat-core.md`](02-chat-core.md)（Talent SPI）、[`03-agent-design.md`](03-agent-design.md)（ReAct/HITL）。
> 覆盖：`solon-ai-talents`（18 能力包，48.8K 行）、`solon-ai-sandbox`（10K）、`solon-ai-harness`（7.8K）、`solon-ai-loop`（16.3K）、`solon-ai-ui`（6K）、ACP 取舍。
> 本篇多为 **P5 缓迁项的契约骨架**：目标是定边界、定优先级、定模块位，供审核裁剪——不承诺首批实现。

---

## 0. TL;DR

- **Talent = 工具包 + 系统指令注入 + 准入控制** 三合一（Java 核心接口仅 7 方法），Go 化零成本；18 个能力包按「价值 × 依赖」排优先级，首批只做零依赖的 5 个（file/sys/diff/web 基础面/text2sql）。
- **sandbox 独立价值最高**（Claude Code sandbox-runtime 的等价物、纯 Go 强项），单列 `./sandbox` 模块，P5 先 macOS（Seatbelt）+ Linux（bubblewrap）。
- **harness/loop 是「全家桶最后一层」**（依赖 talents 矩阵 + sandbox + agent + flow），契约先立、实现排 P5 末（或按需解锁）。
- **ui/aisdk 是 P1 就要的**（聊天流对前端的直接出口）；agui P5。**ACP/ANP 不迁/缓迁**维持 00 §2.2 结论。

---

## 1. Talent SPI（`ai/talent`，02 §契约展开）

```go
type Talent interface {
    IsEnabled() bool
    Name() string                      // 缺省 = 类型名（base 提供）
    Description() string
    Metadata() *Metadata               // 声明式元数据（mcp 桥消费，见 §8）
    IsSupported(p *ai.Prompt) bool     // 准入：按 Prompt 决定本次是否激活
    OnAttach(p *ai.Prompt)             // 激活副作用（写 attrs 等）
    Instruction(p *ai.Prompt) string   // 注入 system message 的说明文本
    Tools(p *ai.Prompt) []ai.FunctionTool
}
type TalentBase struct{}               // 默认实现（恒支持、无副作用、无指令），嵌入按需覆写
```

注入时机：`RequestDesc.prepare` 阶段统一收集（`Options.Talents` 与模型默认 talents 合并），指令按启用序拼进系统提示（02 §7.3 的 prepare 步骤）。

---

## 2. 能力包优先级矩阵（solon-ai-talents 18 子模块）

| Talent | Java 规模 | Go 依赖面 | 优先级 | 方案要点 |
|--------|-----------|-----------|--------|----------|
| **file** | 303 行 | 标准库 | **P5-1** | 读/写/zip 工具（`ReadFile/WriteFile/Zip/Unzip`）；路径边界校验（根目录逃逸防护） |
| **sys** | 1,048 行 | 标准库 | **P5-2** | Shell/Python/Nodejs 执行（`os/exec`；经 `ProcessExecutor` 等价物）+ SystemClock |
| **diff** | 525 行 | 标准库 | **P5-3** | apply-diff/apply-patch 工具（diff 语法解析纯字符串处理） |
| **web（基础面）** | 813 行 | 标准库 | **P5-4** | Webfetch（httpx 拉页抽正文）+ Websearch（走 rag/search 三家）；crawler/flexmark 转 md P5 后期 |
| **text2sql** | 876 行 | `./db` | **P5-5** | 方言感知的 SQL 生成工具；Java 用 solon-data-sqlutils，Go 直接复用 `db/dialect` 与表元数据 |
| **code** | 3,312 行 | 标准库 + 各工具链 | P5 后 | 语言 Provider（Go/Rust/Python/Node/...的构建/测试命令封装）；先 Go/Python 两语言 |
| **memory** | 5,373 行 | 可选 lucene | P5 后 | md 记忆 + 检索记忆（内存索引先行，lucene 等价不迁） |
| **cli** | 16,102 行 | pty（creack/pty）+ sandbox | P5 后 | 终端会话/PTY/Skill/Todo；**依赖 sandbox，随其后** |
| **gateway** | 4,626 行 | swagger-parser 等价 | P5 后 | OpenAPI→MCP 工具网关；Go 侧 kin-openapi |
| **lsp** | 7,135 行 | lsp4j 等价 | 缓 | LSP 客户端（jsonrpc over stdio，可复用 `./mcp` 的帧层思路） |
| **mount** | 4,569 行 | 标准库 | 缓 | MountSource/SkillCatalog 统一资源系统（harness 依赖） |
| lucene | 235 行 | — | 不迁 | 全文检索由 ES/内存索引覆盖 |
| mail | 95 行 | 三方 SMTP | 按需 | simple-java-mail → net/smtp 或三方 |
| social | 345 行 | 标准库 | 按需 | 钉钉/飞书/企微 webhook（纯 POST） |
| pdf | 353 行 | pdf 解析库 | 按需 | 随 ragloader 的 pdf 能力 |
| generation | 324 行 | — | 按需 | 图像/视频生成 talent（依赖 generate.Model，P5） |
| data | 122 行 | redis | 按需 | 经 cache 通道 |
| 聚合包 | — | — | 不迁 | 00 §2.2-B |

> 落位：零依赖 talents → `./ai` 的 `ai/talent/<name>` 子包；带依赖（cli/lsp/gateway/mail…）→ `./plugins/ai/talent/<name>`。每个 = 1 个 `Talent` 实现 + N 个工具 + 1 段指令文本，独立可测。

---

## 3. Sandbox（`./sandbox`，P5）

Java 侧是 Claude Code `sandbox-runtime`（TypeScript）的忠实 Java 移植；Go 移植它的忠实度会**高于 Java 版**（Go 与原版同为系统编程语言）。

```go
package sandbox

// 管理器（对标 SandboxManager 全静态单例 → Go 实例化，便于测试与多配置）
type Manager struct{ /* config + ask callback + violation store + proxy */ }
func New(cfg RuntimeConfig, opts ...Option) (*Manager, error)   // WithAskCallback/WithViolationStore
func (m *Manager) IsSupportedPlatform() bool
func (m *Manager) CheckDependencies() (*DependencyCheck, error)
func (m *Manager) WrapWithSandbox(ctx context.Context, command string, opts ...string) (string, error)
func (m *Manager) WrapWithSandboxArgv(ctx context.Context, argv []string) ([]string, error)  // Windows 位预留
func (m *Manager) UpdateConfig(cfg RuntimeConfig) error         // 网络规则热更新
func (m *Manager) CleanupAfterCommand(ctx context.Context) error
func (m *Manager) Violations() []Violation

// 配置（对标 config 包全家）
type RuntimeConfig struct {
    Network    NetworkConfig     // allowed/denied 域名模式（*.example.com）、unix socket、本地绑定、代理端口、父代理、TLS 终止
    Filesystem FilesystemConfig  // 读=deny-then-allow-back；写=allow-only；git 配置放行
    Seccomp    SeccompConfig     // linux
    Ripgrep    RipgrepConfig
    Windows    WindowsConfig     // 预留（00 §2.2-H：不实现，仅结构位）
}
type AskCallback func(p NetworkHostPattern) (bool, error)   // 未匹配域名的交互裁决（fail-closed）
```

平台后端：`platform_darwin.go`（`sandbox-exec` Seatbelt profile 生成：move-blocking、符号链接防护）、`platform_linux.go`（`bwrap --unshare-net` + socat 桥接代理）、`platform_windows.go`（返回不支持的哨兵实现）。网络代理：`net/http` CONNECT 正向代理 + SOCKS5（标准库 + `golang.org/x/net/proxy`），域名规则引擎 + 违规记录。**零外部依赖**（除 x/net，可降级纯标准库只做 HTTP 代理）。

## 4. Harness 契约骨架（P5 末，缓迁）

```go
// ai/harness（模块位预留；依赖 ai/agent + talents + sandbox）
type Engine struct{ /* 主 agent（运行时可切）+ AgentManager + CommandRegistry + SkillCatalog + PermissionEngine */ }
func NewEngine(opts ...Option) *Engine     // 装配 talents/权限/HITL/压缩/子代理
type Extension interface {                 // 对标 HarnessExtension
    Initialize(e *Engine) error
    Configure(e *Engine, agentName string, b *agent.ReActAgentBuilder)
}
// AgentDefinition：YAML frontmatter（name/description/tools/permissions）→ ReActAgent
//   内置 markdown 代理定义（explore/general/plan/bash/git-summary）随包 embed
// CommandRegistry：斜杠命令 + markdown 自定义命令加载
// PermissionEngine：ToolPermission 规则 → Allow/Deny/Ask；与 react 的 HITLInterceptor 对接
// TaskTalent：task 串行 / multitask 并行子代理调度
```

## 5. Loop 契约骨架（P5 末，缓迁）

```go
// ai/loop（模块位预留；依赖 ai/agent + ai/harness + flow）
type Engine interface {                    // 对标 LoopEngine
    Start(cfg LoopConfig) (*Session, error)
    Pause(id string) / Resume(id string) / Stop(id string) error
    GetState(id string) (LoopState, error) / GetSession(id string) (*Session, error)
    ActiveSessions() []*Session
}
// 状态机：IDLE→PLANNING→EXECUTING→VERIFYING→FIXING→COMPLETED/FAILED（+PAUSED）
// 持久化：DiskStateStore（JSON 原子写：临时文件+rename，互斥锁防双启）
// 策略 SPI：Strategy{ ShouldContinue(ctx) bool; ExecuteIteration(ctx) IterationResult; MaxIterations() int }
//   内置：Ralph（PRD/用户故事驱动，对标 oh-my-claudecode）/ TeamPipeline（flow 图驱动）/ UltraQA
// 质量门禁：Validator/QualityGate（Architect/Critic 两个内置验证器）
// Autopilot：EXECUTION→QA→VALIDATION→COMPLETED 多阶段管道（goroutine 链，对标 supplyAsync）
```

> loop 对 solon-flow 的依赖仅为 `FlowContext`（grep 已核实单点），Go 侧 `flow.Context` 直用。

## 6. UI 流协议适配（`ai/ui`）

### 6.1 aisdk（P1-e，Vercel AI SDK UI Message Stream v1）

```go
package aisdk
// ChatEvent 流 → SSE parts 流（data: {...} 帧）
func WriteStream(ctx context.Context, w io.Writer, req ai.RequestDesc, meta map[string]any) error
func WrapResponse(resp *ai.Response, w io.Writer) error            // 非流式一次性 parts
// Parts（对标 19 个 AiSdkStreamPart，Go struct + json tag）：
//   start/start-step/text-start/text-delta/text-end/
//   reasoning-start|delta|end/tool-input-start|delta|tool-available/tool-output-available/
//   finish-step/finish/error/data/file/metadata/abort/source-url/source-document
// 事件映射：TEXT_*→text-*；THINKING_*→reasoning-*；TOOL_CALL_*→tool-*；RESPONSE_END→finish；
//          ERROR→error。ID 生成器可注入（AiSdkIdGenerator 对应物）
```

### 6.2 agui（P5）

同型：`agui.WriteStream(ctx, w, events, threadID, runID)`；事件族 `RunStarted/Finished/Error、Step*、TextMessage*、Thinking*、ToolCall*、MessagesSnapshot、StateSnapshot、StateDelta(JSON-Patch RFC-6902)、Custom、Raw`。与 aisdk 共享 SSE 写出基建。

## 7. ACP / ANP（维持 00 §2.2 结论）

- **ANP**：空占位，不迁。
- **ACP**：P5 之后按需。真启动时的复用面：JSON-RPC 帧层与 `./mcp` 同构（stdio 传输可直接复用 `mcp/transport_stdio.go` 的行帧泵）；Zed 生态需求出现时以 `./acp` 模块落地（client/agent 双端 + WebSocket 传输一层薄壳）。`@UnstableAcpApi` 标注面（Form/Url 等）不做承诺。

## 8. MCP×Talent 桥（P4/P5 衔接点）

Java 侧 `McpTalentClient/McpTalentServer` 把 MCP 资源包装成 Talent（或反向把本服务能力声明为 Talent 元数据）。Go 侧在 `ai/mcp` 桥稳定后加：

```go
// aimcp 包内（P4）
func NewTalent(p *Provider, opts ...) ai.Talent      // MCP 工具集 → Talent（isSupported 按 prompt 关键词/配置）
```

## 9. 实现顺序建议

1. （P1-e）ui/aisdk（chat 流出.text/plain 场景立即可用）→ 2. （P4）Talent SPI 全量测试 + 首个内示例 talent（file，作竖切验证）→ 3. （P5-1…5）file→sys→diff→web→text2sql 五件套 → 4. sandbox（darwin→linux）→ 5. cli/code/memory（依赖前者）→ 6. harness → 7. loop → 8. agui / 其余 talents / ACP（按需）。
