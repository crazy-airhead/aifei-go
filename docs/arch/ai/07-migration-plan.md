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
- **P5-s2 linux bwrap**：`bwrap.go`（外层 bwrap `--unshare-net`/文件系统 bind/tmpfs 限制/`--unshare-pid`+`--proc` + 内层 socat 3128/1080 监听回连桥 socket、apply-seccomp 两段式 unix socket 封锁 + 宿主侧 socat 桥 `InitializeLinuxNetworkBridge` + mount 点计数延迟清理 + 强制拒绝清单 rg 扫描 + 写 glob 过滤/读 glob 展开语义）。
- **P5-s3 代理服务器**：HTTP CONNECT 正向代理 + SOCKS5 + ParentProxyResolver + TLS 终止/mitm（首卡仅预留 NetworkConfig 结构位与 SetProxyPorts 注入口）。
- **P5-s4 windows**：WFP 后端（srt-win.exe CLI 薄包装：二进制定位/组与 WFP 状态/一次性安装/包装 spawn 描述/就绪检查）。

验收（首卡）：profile 生成黄金断言（读写规则次序/网络限制分支/pty/apple events/弱隔离开关）+ 真 sandbox-exec 回环（allowOnly 外写失败、denyRead 读失败、放行路径成功、无限制命令原样返回）+ 过滤矩阵与 glob/路径规范化表驱动；CLAUDE.md 模块表/测试矩阵同步。

**P5-s1 已落地（2026-10-10）**：`./sandbox` 模块 12 文件（platform/config/restriction/glob/path/shell/net/violation/sandbox/backend_darwin + linux/windows 结构位占位）+ `_test/sandbox_test` 黑盒 19 用例全绿（含真 sandbox-exec 回环）。与 Java 的三处对齐勘误（移植期修正）：glob 静态基目录先剥尾 `/` 再 canonicalize（否则基目录与 glob 段间的分隔符在符号链接解析后丢失——Java substring 语义照抄后以测试暴露）；`isValidHost` 是宽松 DNS-label 字符集门（`1.2.3.4.5` 过门，严格拒收在 `CanonicalizeHost`）；`removeTrailingGlobSuffix` 裸 `**` 原样返回（仅剥 `/**` 尾）。真机探针结论留档：读拒绝=进程 SIGABRT（exit 134），写拒绝=EPERM 报错返回——两类都可用 `err != nil` 断言。

**P5-s2 已落地（2026-10-10）**：`bwrap.go`（对标 `LinuxSandboxBackend.java` 1483 行逐函数移植）+ `sandbox.go` 接线（`wrapLinux`/`ensureLinuxBridge`/`buildWriteConfig` 平台分支）+ `_test/sandbox_test/bwrap_test.go` 黑盒 15 用例全绿。移植期发现与偏差（均已在代码注释注明）：
- **隐式 GOOS 构建约束陷阱（s1 遗留隐患，本次修复）**：`backend_linux.go`/`backend_windows.go` 文件名后缀使它们被其他平台的构建剔除——`GOOS=linux go build` 自 s1 起就坏（darwin 上看不见重复声明、linux 上缺 darwin 符号）。后端文件改名 `seatbelt.go`/`bwrap.go`（argv/profile 生成是纯字符串操作，全平台编译可测；真 sandbox-exec/bwrap 执行自然只在对应平台有意义）。
- **socat 桥懒建（有意偏离）**：Java 仅在 `initialize()` 且当时端口已设置才建桥——实际流程端口总是后到（代理服务器 P5-s3 启动后回填），桥永远建不起来；Go 在 Manager 首次受限包装时按需建桥（`ensureLinuxBridge`），失败非致命（网络走无桥全拒分支）。
- **读 glob 怪癖忠实保留**：Java `wrapWithSandbox` 的 `buildReadConfig` 不展开读 glob，bwrap 侧按不存在路径静默跳过；`getFsReadConfig` 是另一条展开路径 → Go 导出 `LinuxExpandReadPaths` 作对应物，wrap 路径行为不变。
- macOS 宿主测试要点：bwrap 参数序断言用 `\x00` 精确连接（避免 `--tmpfs /etc` 假命中 `--tmpfs /`）；不存在路径规范化原样返回 vs 已存在路径解析 `/private` 前缀——直接 API 测试的基目录须先 canonical 化（Linux 上两者天然相同）；外层 `ShellQuote` 会转义内层脚本的单引号，断言到首个内层引号为止。

**P5-s3 已落地（2026-10-10）**：`proxy_http.go`（HTTP CONNECT 正向代理 + 明文 HTTP 转发）+ `proxy_socks.go`（SOCKS5 无鉴权）+ `parent_proxy.go`（ParentProxyResolver：显式配置/环境变量解析链 + NO_PROXY CIDR/后缀匹配）+ `sandbox.go` 接线（`WithFilterRequest`/`StartProxyServers`/`StopProxyServers`/`ParentProxyResolver`；Reset 关代理）。TLS 终止/mitm 上游本就无实现（`MitmProxyConfig.socketPath` 指向外部进程），Go 保持结构位。`_test/sandbox_test/proxy_test.go` 黑盒 14 用例 + race×2 全绿。移植期发现与偏差（代码注释均已注明）：
- **三处上游缺陷修正**：Java `handleClient` finally 无条件关 client——异步 pipe 刚返回隧道即断（上游移植缺陷，代理实际不可用），Go `tunnel` 阻塞等双泵；Java BufferedReader 预读吞请求体（其注释自认 unreliable），Go 统一经同一 bufio.Reader；Java 父代理应答的预读字节在 pipe 时丢弃（早发数据丢失），Go `bufferedConn` 并回。
- **有意增量**：代理服务器 Java 由外部基础设施启动 + `setProxyPorts` 回填，Go 增 `Manager.StartProxyServers()` 便捷接线（端口仍可外部注入）；配置经 provider 每请求读取（兑现 `UpdateConfig` 网络面活切换的既有承诺）；Stop 显式断在飞连接（Java shutdownNow 只中断线程不关 socket）；直连分支 HttpURLConnection → net/http；SOCKS 父路由 `Socket(Proxy.Type.HTTP)` → 手工 `dialViaParent`（两代理共用）。
- **忠实怪癖保留**：父代理 CONNECT 无端口即失败（对照明文分支有 80/443 回填）；CONNECT 端口非数字静默断连；SOCKS cmd 字段忽略（BIND/UDP 当 CONNECT）；https 父代理走裸 TCP；chunked 请求体按原始字节（含分帧）搬运；SOCKS 直连拨号无超时；filterRequest panic → 拒（Java catch Exception）；逐跳头 = RFC 7230 固定集 ∪ Connection 点名集；响应头每名取首值。
- **Go 版本勘误（本卡修正库内既有代码）**：当前 Go 的 `net.ParseIP` 对点分十进制也返回 16 字节 v4 映射形——`len(addr)==16` 判家族恒真，令 `127.0.0.0/8` 前 8 位（全零）匹配一切 v4 主机、裸 IP 误配 /128 越界；NO_PROXY CIDR 家族改按字面量判（`parentAddrBytes`），v6/映射形走 16 字节、点分十进制收窄 4 字节。`isValidIPv6Literal` 补 RFC 4291 尾组点分十进制形（`::ffff:127.0.0.1` 此前被判非 IP，v4 映射回环恒直连失效）。

**P5-s4 已落地（2026-10-10）**：`wfp.go`（对标 `WindowsSandboxBackend.java` 955 行 + windows/ 六 DTO——srt-win.exe CLI 薄包装）+ `sandbox.go` 接线（`wrapWindows` 分发/`CheckDependencies` windows 并入/`hasNetworkRestriction` 三处内联收敛为 helper）+ `_test/sandbox_test/wfp_test.go` 黑盒 8 用例全平台可跑（假 srt-win 脚本回放）。移植期发现与偏差（代码注释均已注明）：
- **网络隔离本体在 srt-win.exe（Rust 助手，非本库产物）**：本地甄别组 + 按组 SID 键控的机器级 WFP 过滤器 + exec 子命令（受限令牌：组翻 deny-only + 加固 job）。真执行前置：一次性 install（单自提权进程=一次 UAC）+ 注销重登（组 SID 进 TokenGroups）。本库只负责 CLI 编排与状态解析。
- **Java 手写 indexOf 式 JSON 解析 → encoding/json**（对良好形成的 srt-win 输出行为等价，畸形输入错误面更干净）；port_range 缺省/null/单元素宽容为 nil 的 Java 语义保留。
- **两处上游缺陷修正**：① `SandboxManager.checkWindowsDependencies` 计算了 sublayerGuid 却调用未传入的旧版重载（死变量——配了自定义 sublayer 时依赖检查会查错 sublayer），Go 的 `Manager.CheckDependencies` 传入配置值；② wrap 的组引用分支与 `groupRefArgs` 几乎重复且判空语义不同（只判 null 不判 empty），Go 统一到 `groupRefArgs` 单入口。
- **runSrtWin 死锁规避**：Java 先读尽 stdout 再读 stderr——stderr 管道写满时互锁；Go exec 两侧并发缓冲。15 秒超时强杀 exit=-1（Java destroyForcibly 同款）。
- **有意适配**：安装指引文案去掉 `npx sandbox-runtime windows-install` 行（Node 生态专属），API 提示改 Go 名；`ErrWindowsBackendPending` 占位哨兵随卡删除（其文档声明的生命周期即「P5-s4 落地」）。
- Windows 后端纯字符串/进程操作全平台编译全平台可测（`wfp.go` 中性文件名，三平台交叉构建门禁绿）；真 WFP 执行只在 windows 有意义，Manager 的 windows 分发分支在其余平台仅编译验证。

### P5-M 模型补全与其余方言扩展（2026-10-10 立项并落地；契约 `02-chat-core.md` §11 与 §2.1 #5/#23）

- **ai/rerank**（02 §11 "rerank P4（RepositoryTool 依赖）" 的补欠）：Model/Builder/RequestDesc + openai/dashscope 两方言；`Rerank` 写回 score 后降序返回，满足 P4-b 预留的 `rag.Reranker` 接口位；条数不等显式报错（Java 直接越界的收敛）。usage 不进 Go 契约（与 embedding 对齐）。
- **ai/generate**（02 §11 "generate P5"）：Config 带 TaskURL（裸拼接语义，`TaskURLAndId` 不走 path.Join）+ openai/ollama/dashscope 三方言；Content{Text,Data,URL,MimeType} + Value 优先级 url>data>text；模型级默认选项配置形态字符串强类型转换（对标 GenerateOptions.putAll：bool/整数/浮点）。忠实怪癖：dashscope task_id 分支无 taskUrl 守卫（空 taskUrl 时 url=裸拼接结果，对照 openai 有双非空守卫）、ollama error 键存在即错、ollama matched 恒 false 仅兜底。
- **ai/ui/agui**（06 §6.2）：31 事件型（含 5 个 Deprecated THINKING_*）+ Wrapper 事件映射状态机。忠实语义：TEXT_START 落 default 分支降级 CUSTOM("text_start")（正文开界由 TEXT_DELTA 懒开）、ABORT/ERROR terminal 后 finish 静默、SSE 帧无 [DONE] 终标（与 aisdk 的协议差异）。agent 事件流映射（Java toAgUiAgentStream 的反射版）不在首卡——依赖 ai/agent 事件型，后续需要前端协议时补。
- **ai/dialect/gemini Interactions API**（00 §2.1 #5 "gemini(+interactions)"）：`InteractionsDialect`（google-interactions）+ URL 归一矩阵 + input[] step 数组请求 + event_type 帧协议解析（step 累积器挂 StreamContext attrs 并发隔离、服务端工具三态、搜索结果逐项投影去重、step.stop 签名绑定、TOOL_CALL_END 由 core 收尾）+ 非流式 steps[] 终态与六终态映射 + usage（prompt=input+tool_use、webSearch=Σsearch 类 grounding 计数）。
- **MCP×Talent 桥**（06 §8）：`aimcp.NewTalent`（远端 talent://metadataMcp 元数据 + 四 hide 管理工具跨进程回环）+ `RegisterTalent`（本地 talent 导出）。Go 的 Talent 接口无 error 位：IsSupported fail-closed、Instruction/Tools 空值回退、OnAttach 记日志。

**P5-M 已落地（2026-10-10，四提交 06772ea/c2b5f8b/ee619a9）**：黄金样本搬译 Java 两 rerank 测试、三 generate 测试、AgUiStreamWrapperTest 七用例、interactions 五测试文件；httptest 端到端（rerank 回环、generate 强类型转换、agui 假 OpenAI、interactions alt=sse）。

### P5-R ragloader 三件（2026-10-10 立项并落地；契约 `05-rag-design.md` §7）

落位 `plugins/ai/ragloader`（goldmark + goquery + db）：**MarkdownLoader**（Heading 恒切分 + 水平线/代码块/块引用可选切分——块引用切的是块首不切块尾、代码块后恒切分的 commonmark SplitVisitor 语义逐分支对标；标题上 metadata category=header_N+title、行内码/围栏码染色、软硬换行与列表项归一空格）；**HtmlSimpleLoader**（goquery body 归一化正文 + title/description/language 元数据）；**DdlLoader**（SQL 模板全参数化、缺省 MySQL information_schema 形态、`#{key}` 占位符替换、完整 DDL 重组截断保留表体；Java 源码 shcema 笔误在 Go 修正）。pdf/word/excel/ppt 维持 00 §2.2-I 的分批降级决策（解析库按格式独立立项）。测试入 `_test/ai_plugin_test`（markdown 切分矩阵、html 元数据、ddl sqlite 回环三形态）。

### P5-V ragrepo 样板后端（2026-10-10 立项并落地；契约 `05-rag-design.md` §6）

落位 `plugins/ai/ragrepo`：共享 **过滤表达式解析器**（enjoy 判定子集：==/!=/超小于等于/&&/||/!/裸字段/裸布尔/括号/单双引号/裸数值 → 节点树 → ES bool query 与 SQL WHERE 双翻译；方法调用等不可译形态 ok=false 回退 rag.Refilter 内存过滤；SQL 值一律参数化——Java pgvector FilterTransformer 内联字符串的注入面收口）。**es.Repository**（复用 plugins/elasticsearch Client）：检索按 ES8 现代语义重写——VECTOR 走顶层 knn（Java 把 knn 子句误包进 bool.must）、EXACT_KNN script_score、HYBRID 恒 bool should [match+script_score]；批量写入 metadata 平铺 + 分批进度。**pgvector.Repository**（database/sql）：VECTOR(dims)+JSONB+元数据列建表、ivfflat cosine 索引、upsert ON CONFLICT、1−(embedding<=>vec) 相似度 + 过滤 WHERE（声明列直引、其余 metadata->>'k'）+ 占位符随过滤参数偏移。测试面（05 §9.5）：SQL 骨架经导出的 Build* 函数黄金断言 + stubESClient 全链路；真连开关 RAG_PGVECTOR_DSN 留位（FLOW_MYSQL_DSN 先例）。其余后端（milvus/qdrant/redis…）维持 00 §2.1 #22 的按需逐个决策。

### P5-H harness 编排容器（2026-10-10 立项并落地；契约 `06-talents-and-extended.md` §4）

落位 `ai/harness` 新模块（依赖 ai/agent + flow + yaml.v3）：**AgentDefinition**（frontmatter 全字段 + 序列化往返 + 五内置 markdown 代理 embed：explore/general/plan/bash/git-summary——hidden 与无写权限语义照搬）；**PermissionEngine**（priority 降序 + 同序 DENY>ALLOW>ASK + 首条匹配 + glob→正则）与 **HITL 三策略**（Bash 六规则/Write 路径回溯与敏感文件/Web 高风险域名）+ PermissionInterceptor（`ai.Interceptor.InterceptTool` 缝否决——为此给 `ai.ToolRequest` 增量补 Name 位；ASK 经审批回调放行并记忆 allow 规则）；**BashCommandClassifier**（只读白名单含 git 子命令细分、sed -i/重定向/git branch -d 排除、&&/||/; 分段）；**CommandRegistry + MarkdownCommand**（Claude Code 自定义命令兼容：$1..N/$ARGUMENTS 模板、子目录冒号命名）；**AgentManager**（注册表 + 目录回源 + hidden 过滤）与 **AgentFactory**（definition → react.Builder）；**TaskTalent/GenerateTalent**（task 串行与 multitask 并行 XML 聚合、generate 复制 general 覆写 + .soloncode 持久化）；**Engine**（模型注册表/工具池/主代理懒加载热重建/扩展 initialize 生命周期/Prompt 入口）。

域收窄（06 §2 优先级矩阵的 talents 只有五件落地）：工具名映射按 Go 已有 talent 工具集（*/**/pi 展开收窄为 read/write/shell/webfetch 等 + clock 收尾）；terminal/code/memory/skill/lsp 族 talent 未迁移——经 `Engine.SetToolMapper` 缝对接，随后续 talents 解锁补齐。上下文压缩拦截器（Java ContextCompressionInterceptor）同理留缝。

### P5-L loop 持久循环引擎（2026-10-10 立项并落地；契约 `06-talents-and-extended.md` §5）

落位 `ai/loop` 新模块（依赖 ai + flow 单点）：**状态机**（IDLE→PLANNING→EXECUTING→VERIFYING→FIXING→COMPLETED/FAILED(+PAUSED) 转移表）+ 引擎主循环（迭代 → VERIFYING → 验证裁决：passed→续跑/COMPLETED、needsFix→FIXING 修复轮、否则 FAILED）；**Strategy SPI** 三内置：**Ralph**（PRD 文档/用户故事/完成度统计 + Architect→VerificationState→Critic 双层验证与上限强判 + 实现即完成声明语义）、**TeamPipeline**（PLAN→PRD→EXEC→VERIFY→FIX 全程 contextData 驱动、非法转移守卫、取消/保留恢复、fix 溢出 FAILED）、**UltraQA**（质量门禁 + CommandExecutor 缝 + 同失败三连指纹归一 + 轮次上限；ProcessCommandExecutor 走 sh -c/cmd /c）；**磁盘状态**（.loops/{state/{ralph,team,ultraqa,sessions},prd,progress} 布局、_meta 信封 + data 双层 JSON 旧格式兼容、原子写 tmp+rename 无残留）；**互斥守卫**（内存 activeModes + 磁盘同会话双检）；**Autopilot**（五阶段管道 + 适配器 SPI + 跟踪摘要 + 取消）。

Java 两处已知缺陷的 Go 修正（模块文档注明）：① iterationCount 每轮真实递增（保险丝真实生效——Java 恒 0 只对 MaxIterations=0 有效）；② pause 以条件变量阻塞执行 goroutine（resume 真续跑——Java pause 使线程退出、恢复不续跑）。stop→FAILED 的 Java 语义保留。agent/flow/harness 三集成桥（Java integration 包的反射版）不随首卡——loop 的 Strategy/StoryImplementor 已是函数位，宿主直接注入即可。

---

## 9. 全局验收清单（Definition of Done）

- [x] `go test`（显式模块路径列表）全绿（P0–P5 全量：27 测试模块 + 库模块编译检查）；被测模块全部零真实外网依赖（httptest 假服务回放/嵌入式 sqlite/真 sandbox-exec 平台内建）。
- [x] 流式 6 不变量、copy-before-mutate、MCP 判别/协商矩阵有命名测试固化（`_test/ai_test/stream_test.go`、`_test/ai_agent_test/team_test.go`、`_test/mcp_test/{mcp,client}_test.go`）。
- [x] 方言请求 JSON 与关键 SSE 回放用例有黄金样本（来源标注 Java 测试文件：rerank/generate/agui/interactions 各测试文件头注明搬译来源；P0–P4 既有黄金沿用）。
- [x] `go.work` / CLAUDE.md（模块表 + 测试表）/ `docs/arch/index.md` / VitePress sidebar 同步更新（P5 收口一并完成；index/sidebar 自 P3 起收录八篇未再变）。
- [x] 每个 P 期一次多模块标签发布演练（`ai/v1.0.3`、`mcp/v1.0.3`、`ai/agent/v1.0.3`、`ai/flow/v1.0.3`、`plugins/ai/v1.0.3`；P5 新模块 `sandbox/v1.0.3`、`ai/harness/v1.0.3`、`ai/loop/v1.0.3`、`plugins/ai/ragloader/v1.0.3`、`plugins/ai/ragrepo/v1.0.3` 随本期补打）。
- [x] API 命名走查：无 `Get` 冗余前缀访问器（P5 新模块 grep 复核零命中）、泛型受限处走包级 helper（与 01 §7 速查一致）。

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
