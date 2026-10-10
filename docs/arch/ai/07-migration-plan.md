# 迁移步骤与分期路线（ai）

> 本文是「Solon-AI 迁移到 Go」系列的第八篇（TDD 路线图与分期）。
> 前置：[`00-overview.md`](00-overview.md)（范围与决策索引）、各设计篇（02–06）。
> 本文是**执行契约**：每期的交付物、测试用例清单、验收标准（Definition of Done）。供逐卡执行与进度核对；顺序可按审核意见调整（§3 的 P3/P4 即设计为可对调）。

---

## 0. TDD 思想如何贯穿本迁移

1. **测试先行于方言**：每个方言的请求 JSON / 响应 SSE 片段，先从 Java 侧测试资源（`solon-ai` 聚合模块的 `features/ai/*`）搬译成 Go 的 httptest 假服务回放用例——**不依赖真实厂商**，语义对照以 Java 行为为黄金样本。
2. **不变量即测试**：流式 6 条不变量（01 §2.1）、copy-before-mutate（03 §5.2）、MCP 帧判别表（04 §10.1）这类「契约级」行为逐条固化为命名测试。
3. **黑盒与自包含**：全部测试走 `_test/<area>_test` 外部测试包 + 嵌入式依赖（httptest、in-process 回环、sqlite），与仓库测试约定一致；真实厂商/真实向量库联调用环境变量开关（对照 `FLOW_MYSQL_DSN` 先例），默认跳过。
4. **每期收口跑全量**：分期验收 = `go test` 全绿 + CLAUDE.md 模块表/go.work 同步更新。

---

## 1. 测试模块布局（遵循 aifei-go 约定）

| 模块 | 覆盖 | 嵌入依赖 |
|------|------|----------|
| `_test/ai_test` | `./ai`：消息/事件/工具/schema/方言（假服务回放）/流式不变量/会话/router/embedding/rag SPI/talent/ui-aisdk | httptest（标准库） |
| `_test/mcp_test` | `./mcp`：帧判别/会话/三传输回环/桥 | httptest + 自身二进制 stdio 回环 |
| `_test/ai_agent_test` | `./ai/agent` + `./ai/flow`：Simple/ReAct/Team、协议拓扑、HITL 挂起恢复、YAML 组件链 | 假 ChatModel（stub dialect） |
| `_test/ai_plugin_test` | `./plugins/ai` 系列：装配、ragloader/ragrepo 后端 | sqlite（db）+ 可选真连开关 |

共享夹具：`stubmodel`（假方言：脚本化事件序列，模拟文本/工具调用/思考交错）放 `_test/ai_test/internal/stubmodel`，`ai_agent_test` 以 replace 引用同源副本或直接复制小份（仓库无跨测试模块共享惯例，倾向复制并注明来源）。

各模块 go.mod 依 C1/C3 惯例：module `…/_test/<area>_test`、显式 require 被测模块、相对 replace、登记 `go.work`。

---

## 2. 分期总览

| 期 | 主题 | 新增模块 | 预估行数（含测试） |
|----|------|----------|---------------------|
| **P0** | 聊天核心（call 路径） | `./ai` | 2,200–2,800 |
| **P1** | 流式 + 方言矩阵 + 周边 | `./ai`（续） | 2,500–3,200 |
| **P2** | 智能体 + AI 流编排 | `./ai/agent`、`./ai/flow`（+flow 一处增量） | 2,300–3,000 |
| **P3** | MCP 协议与双端 | `./mcp`（+ `ai/mcp` 桥） | 2,000–2,600 |
| **P4** | RAG + 配置装配 + ui 收尾 | `./ai/rag`（续）、`./plugins/ai` | 1,200–1,600 |
| **P5** | 按需解锁 | ragloader/ragrepo/talents/sandbox/harness/loop/agui/ACP | 视范围 |

依赖链：P0→P1→P2 严格顺序；**P3 与 P4 可并行、可与 P2 后半互换**（MCP 与 RAG 互不依赖，都只依赖 P1 的 `./ai`）。P5 各项彼此独立。

---

## 3. P0：聊天核心（call 路径）

### P0-a 消息与内容块
- 交付：`ai/message.go`、`ai/content.go`（02 §2 全契约）；`ai.Prompt`。
- 测试：四角色工厂；多模态 Content 序列化判别（type 分发往返）；`OfUserAugment`；`Clone` 深拷贝断言；大 base64 降级占位（>64KiB）。
- 验收：表驱动往返 `Marshal→Unmarshal→相等` 全绿。

### P0-b 事件模型与聚合器
- 交付：`event.go`（30 常量一次定义齐 + Group/IsDelta）、`accumulator.go`。
- 测试：构造事件序列（手写）→ 聚合出 AssistantMessage/Usage/finishReason；工具调用 delta 拼接（args 增量合并）；thinking 与 text 并存；`BuildResponse` 快照完整性。
- 验收：聚合器对「无事件流」「仅 ERROR」的边界行为符合不变量 1/3。

### P0-c 工具家族与 schema 生成
- 交付：`tool.go`、`tool_schema.go`、`tool_reflect.go`。
- 测试：`NewTool` 链式构建 → InputSchema 生成（基础类型/嵌套/slice/map/time/enum）；`RegisterTools` 反射绑定（方法名 kebab 化、param tag 解析、默认值、三种返回形态）；`Call` 的 panic→recover→`ToolResultErr`。
- 验收：`_OrderTools` 样例（Java 测试同名类搬译）schema 与 Java 输出逐字段一致。

### P0-d httpx 与 openai 方言（call）
- 交付：`httpx`（Client/PostJSON/SSE 解析器）、`dialect.go` SPI、`ai/dialect/openai`（BuildRequestJSON + ParseResponseJSON 的 call 分支）。
- 测试：SSE 解析器独立单测（分帧/多行 data/事件名/[DONE]/超大行/CRLF/注释行）；httptest 假 OpenAI 回放固定 JSON（含 usage/tool_calls/finish_reason 各形态）→ 事件序列断言。
- 验收：**不真连**任何厂商；假服务覆盖 ≥ Java `features/ai/openai` 用例集。

### P0-e 模型执行管线（call + autoToolCall）
- 交付：`model.go`（Config/ChatModel/RequestDesc/Response）、`options.go`、`usage.go`、`session.go`（Memory）、`interceptor.go`。
- 测试：系统提示组装序（role+instruction+schema 指令+talent 指令）；autoToolCall 递归（假工具 + 假模型两轮）消息表追加正确（assistant(tool_calls)→tool）；`Response.Events()` 全轨迹；maxTurns 不在此层（agent 层职责）。
- 验收：`go test ./_test/ai_test` 全绿；`go.work`/CLAUDE.md 登记 `./ai` 与 `_test/ai_test`。

---

## 4. P1：流式与方言矩阵

### P1-a stream 主路径
- 交付：`RequestDesc.Stream/StreamChan/CallStream`、`stream_context.go`。
- 测试：不变量 1–5 逐条命名测试；消费方 sink 出错即中止（无 ERROR 事件、透传错误）；ctx cancel 在帧间隙生效；工具递归子流事件在同管线续播且 Step 递增；channel 形态背压（慢消费者不丢事件）。
- 验收：openai 假 SSE（chunked）全链路绿。

### P1-b 方言矩阵
- 交付：`anthropic`（签名/thinking/input_json_delta/cache 计量）、`gemini`（thought/candidate）、`ollama`、`dashscope`；openai-responses 后置到 P1 末。
- 测试：每方言 ≥ 3 组回放（纯文本流 / 工具流 / 多 choice 或 candidate 归一）；请求 JSON 快照比对（Java 侧 dump 为黄金样本）。
- 验收：`SelectDialect` 优先级表驱动（standard > provider > openai 补位）。

### P1-c embedding + 拦截器 + router
- 交付：`ai/embedding`（+openai/ollama/dashscope 三方言）、`interceptor.go` 链、`ai/router`（RoundRobin/加权/规则/Smart 四策略）。
- 测试：embed 批量与维度；拦截器 onPrepare 改写系统提示、InterceptCall 包裹链序；router 显式 attr 路由 > 策略选择；加权分布（统计断言）。
- 验收：Smart 路由（LLM 决策）用假模型测。

### P1-d 会话扩展 + ui/aisdk
- 交付：`FileSession`（原子写）、`ai/ui/aisdk`（WriteStream/WrapResponse + 19 parts）。
- 测试：会话窗口截断/RemoveLatest；aisdk 事件→parts 映射表驱动；httptest 完整 SSE 输出比对。
- 验收：一个端到端 demo（httptest 假模型 + aisdk 输出）入 `_test/ai_test` 示例目录。

---

## 5. P2：智能体与 AI 流编排

### P2-a Simple + 基座
- 交付：`ai/agent` 模块（AgentBase/Session/Event/Trace/Metrics）+ `simple/`。
- 测试：Agent 放进 flow 图当节点（`AddActivityNamed`）端到端；`_SESSION_` 装载复用；retry 仅传输错误重试；`OutputKey` 写回 ctx；流式 SimpleDelta 转发 + 终态聚合。
- 验收：`go.work` 登记 `./ai/agent`、`_test/ai_agent_test`。

### P2-b ReAct
- 交付：`react/`（两 Task + options）+ StopLoop/ToolRetry 拦截器。
- 测试：reason→action→reason 两轮收敛（stubmodel 脚本）；maxTurns 保险丝文案；autoRethink 放宽；会话窗口；死循环指纹检测；工具失败重试次数。
- 验收：Java `ReActStreamTest` 场景搬译（事件序列断言 Reason*/ToolCall*/RunEnd）。

### P2-c Team + 协议
- 交付：`team/`（引擎接线 + ProtocolBase）+ Sequential → Hierarchical → Swarm 三协议（其余 5 个随后增量）；**flow 增量 D8**（已定稿方案 A：`Node.Attachment/SetAttachment`，向后兼容 + flow 全量回归，单独成提交）。
- 测试：Sequential 静态拓扑（零 LLM 决策）；Hierarchical 的 supervisor 路由（假模型回 agent 名 → 边 when 命中）；`PrepareAgentPrompt` copy-before-mutate 专项（跨 agent 不串写）；拦截器经 `engine.AddInterceptor` 生效；会话挂起（Pending→图 Stop→快照→恢复续跑）。
- 验收：`flow` 模块现有测试全绿（增量无回归）；8 协议至少 3 个绿。

### P2-d ai/flow 组件
- 交付：`ai/flow` 模块（12 组件 + `RegisterDefaults` + attrs 常量表）。
- 测试：`chat_case1.chain.yml` **逐字**搬译执行（VarInput→VarCopy→ChatModelCom→ConsoleOutput）；stream:true 时输出事件流句柄；chatConfig 从 meta 构建 + 附件缓存命中（第二次执行不重建，D8 验证）；节点事件 `flow.node.start/end` 经 `ctx.EventBus()` 可订阅；exclusive 网关 + enjoy 条件（react1.yml 场景）。
- 验收：YAML 与 solon-ai-flow 测试资源兼容（同 schema 无改动可跑）。

---

## 6. P3：MCP

### P3-a 协议层
- 交付：`schema.go/types.go/version.go/session.go`（`./mcp` 模块）。
- 测试：`UnmarshalMessage` 判别表驱动（含畸形 id 拒绝）；pending 表并道（并发 50 请求乱序回包）；版本协商矩阵。
- 验收：`_test/mcp_test` 建立；零外部依赖（go.mod 只 require 内部 json/log）。

### P3-b 客户端
- 交付：stdio 传输 + `Client` 门面 + 心跳重连。
- 测试：自二进制 stdio 回环（initialize→list→call→ping→变更通知）；超时与重连退避（假延迟注入）。
- 验收：`ai/mcp` 桥（Provider.Tools 映射 + CallTool 透传 + IsError 前缀 + 允许/拒绝清单 + 游标缓存）接入假函数调用循环绿。

### P3-c 服务端
- 交付：`Server` 门面 + `Handler()`（streamable HTTP 全语义）+ `RunStdio` + 旧式 SSE 兼容 + 安全钩子。
- 测试：httptest 回环全流程（会话建立/工具注册注销变更/资源订阅更新/提示/DELETE 注销/会话失效重连/Last-Event-ID）；`aifei` 挂载（`WithHTTPHandler` 裸通道）互操作（mcp 客户端 ↔ aifei 服务端）。
- 验收：对标准 MCP 客户端脚本（若可得）冒烟；P3 整体收口更新 CLAUDE.md。

---

## 7. P4：RAG 与装配（可与 P3 并行）

### P4-a SPI 与内置实现
- 交付：`ai/rag`（Document/Loader/Splitter/Repository/Storer/Lifecycle/Query + TextLoader + InMemory + 相似度工具）。
- 测试：全参数矩阵（limit/阈值/新鲜度/过滤/禁重滤）；enjoy 过滤表达式表驱动（SnEL 迁移表逐条）。

### P4-b 切分器与检索工具
- 交付：4 切分器 + Pipeline；`PromptAugment`、`RepositoryTool`（rerank 接口位）。
- 测试：TokenSize 近似计数（注入假 Tokenizer 对照）；SemanticSplitter 假 embedding 切点；RepositoryTool 在假 agent 循环中被选中并回注结果。

### P4-c 搜索即仓储
- 交付：`rag/search`（tavily/bocha/baidu；APIKey 经环境变量）。
- 测试：httptest 假回放（三家响应形状）；`Repository` 只读断言（不可断言为 Storer）。

### P4-d 配置装配（plugins/ai）
- 交付：`./plugins/ai`：读 `ai.*`（模型注册表/默认模型/embedding/agent 段/mcp 客户端段/可选 server 段）→ 包级默认（`ai.Use(name)` 语义经 registry 提供）。
- 测试：`app.yml` 样例装配出可用 ChatModel（stub doer）；缺省/多模型/环境变量覆盖（对照 storage/kafka 插件测试式样）。
- 验收：`_test/ai_plugin_test` 建立；demo 场景（config 驱动的一问一答）跑通。

---

## 8. P5：按需解锁（每项独立立项）

| 项 | 触发建议 | 首卡 |
|----|----------|------|
| ragloader（markdown/html/ddl） | RAG 真实语料接入时 | markdown 标题切分 loader |
| ragrepo（es/pgvector） | 向量库选型定时 | ES 后端（复用 plugins/elasticsearch，knn 查询） |
| talents 五件套 | 编码/系统代理场景 | file talent 竖切 |
| sandbox | cli talent 或独立安全需求 | darwin Seatbelt 后端 |
| harness / loop | talents+sandbox 就绪后 | AgentDefinition frontmatter 解析 |
| agui / 其余方言扩展 / ACP | 前端协议或编辑器需求 | agui 事件族 |

每项立项时在本篇追加小节（对照 flow 系列的 `06-mysql-repository.md` 追加式收尾）。

### P5-T talents 五件套（2026-10-10 立项，用户解锁；契约 `06-talents-and-extended.md` §1/§2）

依赖：P0/P1 已落地的 Talent SPI（`ai/talent.go`：接口 7 方法 + `TalentBase` + `ActiveTalents` 管线接线 + wikiTalent 竖切测试）。落位：file/sys/diff/web 基础面 → `./ai` 子包 `ai/talent/<name>`（零新增依赖；web 走 `ai/httpx` + `ai/rag/search`）；text2sql → `./plugins/ai/talent/text2sql`（带 `./db` 依赖，按 06 §2 落位规则出 ai 模块）。测试：前四件入 `_test/ai_test`（`talent_<name>_test.go`），text2sql 入 `_test/ai_plugin_test`。

- **P5-t1 file**：读/写/zip 工具（ReadFile/WriteFile/Zip/Unzip）+ 路径边界校验（根目录逃逸防护）；兼作 SPI 全链路竖切验证（准入/指令块/工具 `talent` meta 染色端到端）。
- **P5-t2 sys**：Shell/Python/Nodejs 执行工具（`os/exec` 的 ProcessExecutor 等价物：超时/输出捕获/exit code 语义）+ SystemClock。
- **P5-t3 diff**：apply-diff/apply-patch 工具（diff 语法解析，纯字符串处理）。
- **P5-t4 web（基础面）**：Webfetch（httpx 拉页抽正文）+ Websearch（走 rag/search 三家）；crawler/flexmark 转 md 留 P5 后期。
- **P5-t5 text2sql**：方言感知的 SQL 生成工具；复用 `db/dialect` 与 `db.Table` 表元数据注册表。

验收：五件各自黑盒测试全绿（TempDir 假文件系统、真 exec 安全命令、httptest 假回放）；`ChatOptions.Talents` 挂入后指令块 + 工具部署端到端断言；CLAUDE.md 模块表/架构段同步；收口随下一舰队发版。

### P5-S sandbox（2026-10-10 立项，用户解锁；契约 `06-talents-and-extended.md` §3）

依赖：仅内部 `log`，零外部依赖。落位 `./sandbox` 新模块（cli/code/memory talents 与 harness/loop 的前置，06 §9 依赖链）。Java 源 `solon-ai-sandbox`（Claude Code sandbox-runtime 的 Java 移植，6.4K 行）；Go 移植按平台分卡，Manager 实例化（非 Java 静态单例——06 契约决策，便于测试与多配置）。

- **P5-s1 darwin Seatbelt（首卡）**：配置层全家（RuntimeConfig/Network/Filesystem/Seccomp/Ripgrep/Windows 结构位）+ 读写限制结构（读=deny-then-allow-back、写=allow-only）+ 平台探测与依赖检查 + macOS Seatbelt profile 生成（move-blocking 防绕过/符号链接边界防护/强制拒绝清单：shell 启动文件与 .git）+ Manager 门面（WrapWithSandbox/Argv、UpdateConfig 热更新、CleanupAfterCommand、违规存储）+ 域名过滤（DomainPatternMatcher/HostUtils/NetworkFilter，fail-closed：无配置/畸形主机/回调异常一律拒绝）+ 工具层（glob→regex、路径规范化 `~`/相对路径/符号链接、shell 引用、代理环境变量注入）。测试 `_test/sandbox_test`（黑盒；真 `sandbox-exec` 读写拒绝/放行回环）。
- **P5-s2 linux bwrap**：`backend_linux.go`（bwrap --unshare-net + socat 桥接 + mount 点清理 + ripgrep glob 展开）。
- **P5-s3 代理服务器**：HTTP CONNECT 正向代理 + SOCKS5 + ParentProxyResolver + TLS 终止/mitm（首卡仅预留 NetworkConfig 结构位与 SetProxyPorts 注入口）。
- **P5-s4 windows**：WFP 驱动后端（WindowsConfig 结构位已留；srt-win.exe 形态）。

验收（首卡）：profile 生成黄金断言（读写规则次序/网络限制分支/pty/apple events/弱隔离开关）+ 真 sandbox-exec 回环（allowOnly 外写失败、denyRead 读失败、放行路径成功、无限制命令原样返回）+ 过滤矩阵与 glob/路径规范化表驱动；CLAUDE.md 模块表/测试矩阵同步。

**P5-s1 已落地（2026-10-10）**：`./sandbox` 模块 12 文件（platform/config/restriction/glob/path/shell/net/violation/sandbox/backend_darwin + linux/windows 结构位占位）+ `_test/sandbox_test` 黑盒 19 用例全绿（含真 sandbox-exec 回环）。与 Java 的三处对齐勘误（移植期修正）：glob 静态基目录先剥尾 `/` 再 canonicalize（否则基目录与 glob 段间的分隔符在符号链接解析后丢失——Java substring 语义照抄后以测试暴露）；`isValidHost` 是宽松 DNS-label 字符集门（`1.2.3.4.5` 过门，严格拒收在 `CanonicalizeHost`）；`removeTrailingGlobSuffix` 裸 `**` 原样返回（仅剥 `/**` 尾）。真机探针结论留档：读拒绝=进程 SIGABRT（exit 134），写拒绝=EPERM 报错返回——两类都可用 `err != nil` 断言。

---

## 9. 全局验收清单（Definition of Done）

- [ ] `go test`（显式模块路径列表）全绿；被测模块全部零真实外网依赖。
- [ ] 流式 6 不变量、copy-before-mutate、MCP 判别/协商矩阵有命名测试固化。
- [ ] 方言请求 JSON 与关键 SSE 回放用例有黄金样本（来源标注 Java 测试文件）。
- [ ] `go.work` / CLAUDE.md（模块表 + 测试表）/ `docs/arch/index.md` / VitePress sidebar 同步更新。
- [ ] 每个 P 期一次多模块标签发布演练（`ai/vX.Y.Z`、`mcp/vX.Y.Z`…，对照打标签约定）。
- [ ] API 命名走查：无 `Get` 冗余前缀访问器、泛型受限处走包级 helper（与 01 §7 速查一致）。

## 10. 风险与对策（TDD 视角）

| 风险 | 对策 |
|------|------|
| 方言语义漂移（尤其 snapshot delta 归一） | 黄金样本 + 回放测试先行；Java 侧行为差异记录在案（issue 式） |
| 流式取消边界（SSE 半帧/工具执行中） | ctx 注入点表（01 §3）逐点测试；假延迟注入覆盖竞态 |
| flow 增量（D8，已定稿 A）引入回归 | 单独提交 + flow 全量回归；备选方案 B 留档可切（契约不变） |
| MCP 规范理解偏差 | 协议常量/错误码与 spec 章节对照注释；互操作冒烟（标准客户端/服务端脚本） |
| 范围蔓延（P5 长尾） | P5 项一律独立立项 + 触发条件；主线 DoD 不含 P5 |
| Go 解析生态缺口（pdf/ppt） | 能力声明式降级（返回明确不支持错误）+ 独立插件包隔离依赖 |

## 11. 提交粒度建议

- 一个 P-x 小节 ≈ 3–8 个提交；每提交「实现 + 对应测试 + 文档同步」三件套。
- 消息前缀按模块：`ai:`、`mcp:`、`ai/agent:`、`ai/flow:`、`plugins/ai:`、`flow:`（D8 增量单独成提交）。
- 黄金样本与回放夹具独立提交（便于 review 数据与逻辑分离）。
- 每期收口一个 `docs/arch/ai/` 状态注记（在 00 顶部加一行「P-x 已落地」式记录，对照 flow 系列落地后的记忆惯例）。

## 12. 起步（P0-a 第一张卡示例）

```
卡：ai/P0-a 消息与内容块
1. go work edit -use ./ai ./_test/ai_test（先建两模块骨架 go.mod，依 00 §4.3 模板）
2. _test/ai_test 先写失败测试：
   TestMessageRoundtrip（四角色 + 多模态块 type 判别往返）
   TestOfUserAugment / TestMessageCloneDeep / TestBase64Downgrade
3. 实现 ai/message.go + ai/content.go 至全绿
4. CLAUDE.md 模块表 + 测试表登记；提交 `ai: 消息与内容块（P0-a）`
```
