# Aifei-Go ai/talent：能力包（Talent）总览

> **Talent = 系统提示片段 + 一组工具 + 准入规则的打包**：挂进 `ChatOptions.Talents` 一行，模型即获得文件读写、Shell 执行、网页抓取、邮件通知等 26 件预置能力，无需手写工具声明。

## 1. 背景与定位

直接给模型挂工具（[ai.md §5](ai.md)）适合少量自定义函数；当能力成规模——一组工具 + 配套用法说明 + 安全约束——打包成 **Talent（能力包）** 才好复用与治理。`ai.Talent` SPI 定义于聊天核心，实现分两层：

- `ai/talent/*`（11 包 20 件）：零第三方依赖或仅标准库，随 [ai 模块](ai.md)引入
- `plugins/ai/talent/*`（6 件）：带较重依赖（LSP、Bleve、markitdown 等），独立子模块按需引入

对标 solon-ai 的 talents 体系（契约与黄金样本一致），另经 [mcp Talent 桥](mcp.md) 可跨进程互导。

## 2. 机制：Talent SPI 与激活流程

```go
type Talent interface {
    IsEnabled() bool                // 总开关
    Name() string                   // 指令块标题（如 "file"）
    Description() string            // 一句话能力描述
    IsSupported(p *Prompt) bool     // 准入：本次会话是否激活
    OnAttach(p *Prompt)             // 激活副作用（写 prompt attrs）
    Instruction(p *Prompt) string   // 用法/约束指令文本
    Tools(p *Prompt) []FunctionTool // 工具集
}
```

挂载与激活：

```go
res, err := m.Prompt("把这份名单写入 users.xlsx 并抄送主管").
    Options(func(o *ai.ChatOptions) {
        o.Talents = []ai.Talent{
            file.NewReadWrite("/data"),       // 示例：文件能力
            mail.NewMail(mailer, "/data"),
        }
    }).Call()
```

prepare 阶段 `ai.ActiveTalents(opts, prompt, system)` 统一处理：准入通过者的指令以

```text
---
# [Talent: file] - 文件读写
<file>……用法与约束……</file>
---
```

块注入系统提示；工具 meta 染色 `talent=<name>` 后追加进 `o.Tools`。自定义只需嵌入 `ai.TalentBase`（全默认直通实现）覆写所需方法——见 [ai.md §12](ai.md)。

一次挂多个 Talent 时按声明顺序依次注入、工具合并去重；`IsSupported` 返回 false 的 Talent 整包跳过（指令与工具都不出现），这是"按会话动态选能力"的官方缝——例如仅当 prompt attrs 含 `mode=coder` 时才激活 shell 类能力包。

注入后模型每次工具调用都可经工具 meta（`talent=<name>`）追溯来源能力包——审计日志与 [harness 权限规则](ai-harness.md) 可按 meta 精确匹配，"这个工具是谁给的"一目了然。

## 3. ai/talent：20 件核心能力包

### 3.1 文件与压缩（file）

| Talent | 构造 | 工具 |
|---|---|---|
| `file.NewReadWrite(workDir)` → Name `"file"` | workDir 为根的沙箱目录 | `file_write` `file_read` `file_list` `file_delete` |
| `file.NewZip(workDir)` → `"zip"` | | `zip` `unzip` |

```go
rw := file.NewReadWrite("/workspace")   // 所有路径解析都限制在 workDir 内
zp := file.NewZip("/workspace")         // zip/unzip 同样受 workDir 约束
```

`file_read` 支持文本与二进制（二进制返回 base64）；`file_write` 的目录不存在自动创建；`file_list` 返回带大小/时间的清单。

### 3.2 系统执行（sys）

| Talent | 构造 | 工具 |
|---|---|---|
| `sys.NewShell(workDir)` → `"shell"` | 自动探测 COMSPEC / bash→sh | `execute_shell` |
| `sys.NewPython(workDir)` → `"python"` | 依赖本地 python 可执行 | `execute_python` |
| `sys.NewNodejs(workDir)` → `"nodejs"` | 依赖本地 node | `execute_js` |
| `sys.NewClock()` → `"clock"` | 零依赖 | `get_current_time` |

```go
sh := sys.NewShell("/workspace")    // 工作目录固定；命令超时与输出截断内建
py := sys.NewPython("/workspace")   // 代码落临时文件执行， stdout/stderr 一并返回
```

三个执行类 Talent 的指令块都会注入"先读后写、失败重试前先诊断"类约束，降低模型乱执行的概率；系统级隔离请再叠 [sandbox](ai-harness.md)。

### 3.3 代码变更（diff）

| Talent | 构造 | 工具 |
|---|---|---|
| `diff.NewApplyDiff(workDir)` → `"diff"` | | `apply_diff`（结构化差异块） |
| `diff.NewApplyPatch(workDir)` → `"patch"` | | `apply_patch`（unified patch） |

两者都是"模型产出补丁、工具负责落盘"：补丁应用失败（上下文不匹配）会把错误细节回给模型自修正，配合 [react 智能体](ai-agent.md)的循环形成自愈式改码。

### 3.4 网络访问（web）

```go
fetch := web.NewFetch().RetryConfig(2)                 // "webfetch" / 工具 webfetch
search := web.NewSearch(repo).RetryConfig(2)           // "websearch" / 工具 websearch；repo 为任意 rag.Repository
                                                       // 常配 search.NewTavily(...)（见 ai-rag.md §8）
crawler := web.NewCrawler(web.NewJinaCrawlerDriver(nil)).  // "Crawler" / 工具 crawl_url
    APIKey("jina-key")                                    // 或 NewFirecrawlCrawlerDriver(nil)
```

`web.Search` 是"搜索引擎 → 模型"的桥：检索走 [RAG Repository SPI](ai-rag.md)，因此博查/百度/Tavily 均可即插即用。

```go
// 联网检索 + 抓取正文，两件套覆盖"查资料"场景
o.Talents = []ai.Talent{
    web.NewSearch(search.NewTavily(search.WithAPIKey("tk"))).RetryConfig(2),
    web.NewFetch().RetryConfig(2),   // webfetch 工具：URL → 干净正文（自动去导航/广告）
}
```

`RetryConfig(n)` 为两个网络类 Talent 共有的重试配置；`web.Crawler` 的 Jina/Firecrawl 驱动做深度抓取（整站爬取入库再检索），`APIKey(k)` 与 `Driver(d)` 二选一。

### 3.5 多模态生成（generation）

```go
img := generation.NewImage(generation.NewModelImageDriver(imageModel), "/out")  // "ImageGeneration" / generate_image
vid := generation.NewVideo(driver, "/out")                                     // "VideoGeneration" / generate_video
```

`NewModelImageDriver` 适配 [ai/generate](ai.md) 的 `*generate.Model`；产物落盘 workDir 并返回路径。

### 3.6 消息通知（social + mail）

```go
social.NewDingTalk(webhookURL, secret)   // "DingTalk" / send_dingtalk
social.NewFeishu(webhookURL, secret)     // "Lark"    / send_lark
social.NewWeCom(webhookURL)              // "WeCom"   / send_wecom

mailer := mail.NewSMTPMailer("smtp.example.com", 465, "bot@example.com", "pass") // 465 走 TLS
mailT := mail.NewMail(mailer, "/attachments")                                    // "Mail" / send_email
```

### 3.7 记忆 / 代码上下文 / 键值（memory / code / data）

```go
store, _ := memory.NewMemoryStore("/mem")   // scope 缺省 [user, workspace]
mem := memory.NewMemory(store).             // "Memory"：五工具见下表
    SessionIsolation(true).                 // 链式：会话隔离
    RelevanceCount(5)                       // 召回条数

codeT := code.New(workDir)                  // "Code"：纯指令 talent（Tools 返回 nil），
                                            // 注入工程结构与语言规范；无 provider 时 BuiltinProviders()

kv := data.New(kvStore, "bot:")             // "redis_storage"：redis_set/redis_get
                                            // kvStore 自定义实现（Get/Set 接口）
```

`memory` 五工具的分工（长期记忆的完整生命周期）：

| 工具 | 时机 | 行为 |
|---|---|---|
| `memory_extract` | 对话结束后 | 从会话中抽取事实性记忆（按 scope 分层存储） |
| `memory_search` | 按需 | 关键词检索记忆条目 |
| `memory_recall` | 会话开始 | 按 `RelevanceCount` 召回最相关记忆注入上下文 |
| `memory_consolidate` | 定期 | 合并去重、冲突消解 |
| `memory_prune` | 定期 | 清理过期/低频记忆 |

`code` 的 `LanguageProvider` 决定注入哪些语言规范（`BuiltinProviders()` 覆盖常见语言）；`data` 的 `KVStore` 是两方法接口，Redis/内存 map/BoltDB 皆可实现，key 统一加 `keyPrefix` 防碰撞。

### 3.8 组合示例：一个"会查资料、能记事、可通知"的助手

```go
o.Talents = []ai.Talent{
    memory.NewMemory(store),                                   // 记住用户偏好
    web.NewSearch(searchRepo).RetryConfig(2),                  // 联网查证
    social.NewFeishu(hook, secret),                            // 重要结论推飞书
    sys.NewClock(),                                            // 感知时间（"明天"类问题）
}
// 模型可自主决定：先 recall 记忆 → 不够则 websearch → 结论 send_lark
```

四个能力包互不知晓，却经统一的 SPI 协同——这正是 Talent 模型的价值：能力正交、组合涌现。

## 4. plugins/ai/talent：6 件增强能力包

| Talent | 构造 | Name | 工具 |
|---|---|---|---|
| `gateway.New()`（`SetDoer(d)` 可换传输） | OpenAPI 网关 | `"Gateway"` | `call_api` `get_api_detail` `search_apis` |
| `pdf.New(rootPath)` | PDF 解析/生成 | `"pdf_tool"` | `pdf_parse` `pdf_create` |
| `cli.New(cli.NewManager())` | 后台终端 | `"Terminal"` | `bash` `bash_start` `bash_wait` `bash_stop` |
| `lsp.New(lsp.NewManager(ws), nil)` | 语言服务器 | `"Lsp"` | `lsp`（单工具按 operation 分发：goToDefinition/findReferences/hover/goToImplementation/documentSymbol/workspaceSymbol） |
| `bleve.New(workDir)` | 全文检索 | `"Bleve"` | `full_text_search` `refresh_search_index` |
| `text2sql.New(pool, driver, tables...)` | 数据库专家 | `"text2sql"` | `execute_sql` `get_table_schema` |

### 4.1 bleve：本地全文检索（Bleve + gse 中文分词）

```go
bt := bleve.New("/idx").
    IgnoreNames("node_modules", ".git").        // 跳过目录/文件名
    SearchableExtensions("go", "md", "java")    // 只索引这些扩展名；两者均可覆写缺省
// refresh_search_index 增量建索引；full_text_search(query) 走 gse CutSearch 分词的
// MatchQuery（索引与查询同源分词，中文无空格也可正确命中）
```

选型说明：对标 solon-ai 的 Lucene talent；Go 侧用 [Bleve](https://github.com/blevesearch/bleve)（内存索引 + BM25）+ [gse](https://github.com/go-ego/gse)（纯 Go 中文分词，内嵌词典）等价替代，无需部署 Elasticsearch。

### 4.2 text2sql：自然语言查库

```go
tt := text2sql.New(pool, "mysql", "employee", "department").
    MaxRows(50).                 // 分页保护（缺省 50）
    ReadOnly(true).              // 只读保护（缺省开：非 SELECT 拒绝）
    SchemaMode(text2sql.SchemaDynamic)  // >20 表时建议动态模式（按需 get_table_schema）
// 或经 db 模块配置构造：text2sql.NewFromConfig(db.GetConfig("main"), "employee")

result, err := tt.ExecuteSql(ctx, "SELECT count(*) FROM employee")
```

内置方言：`GenericDialect` / `H2Dialect` / `MySQLDialect` / `OracleDialect` / `PostgreDialect` / `SQLiteDialect`（按 driverName 自动识别，pgx/pq 归一 PostgreSQL）；`.Dialect(d)` 显式覆写。`Instruction` 注入方言知识与多表分析规范，`execute_sql` 结果以 JSON 行返回。

### 4.3 gateway：OpenAPI 网关

```go
gw := gateway.New()          // SetDoer(d) 可换传输（测试注入/内网代理）
// 挂入后模型即可三步走：
//   search_apis("订单")      → 语义匹配出相关接口
//   get_api_detail("GET /orders/{id}") → 参数/响应结构细节
//   call_api("GET", "/orders/A1", {...}) → 真实调用并返回结果
```

`gateway` 把 OpenAPI 文档变成模型的"API 字典"：无需为每个接口写工具包装，接口变更时文档即真相，是"让模型调内部 API"的最省事路径。

### 4.4 cli 与 lsp：终端会话与语言智能

```go
term := cli.New(cli.NewManager())   // "Terminal"
// bash        —— 前台执行（带超时）
// bash_start  —— 后台会话启动，返回会话 id
// bash_wait   —— 等待/收割后台会话输出
// bash_stop   —— 终止会话
// 长命令（dev server、watch）不再阻塞智能体循环

lspT := lsp.New(lsp.NewManager("/project"), nil)   // "Lsp"；nil readFile 走 os.ReadFile
// 单工具 lsp，按 operation 参数分发：
//   goToDefinition / findReferences / hover /
//   goToImplementation / documentSymbol / workspaceSymbol
```

`cli` 补齐"长任务不卡循环"，`lsp` 给模型编译级代码理解（比 grep 更准的重命名/引用链）——两者与 [ai-harness](ai-harness.md) 的编码代理天然搭配。

## 5. 选型速查

| 场景 | 推荐组合 |
|---|---|
| 办公文档问答助手 | `file.ReadWrite` + [ragloader](ai-rag.md) 入库 + `rag.NewRepositoryTool` |
| 运维值班机器人 | `sys.Shell` + `web.Fetch` + `social.DingTalk`（告警通知）+ [sandbox](ai-harness.md) 包 shell |
| 数据分析助手 | `text2sql`（查库）+ `generation.Image`（画图）+ `data`（会话暂存） |
| 编码代理 | `file.ReadWrite` + `diff.ApplyPatch` + `cli.Terminal` + `lsp.Lsp`（见 [ai-harness](ai-harness.md)） |
| 知识型客服 | `memory.Memory`（长期记忆）+ `bleve`（本地全文）+ `web.Search`（兜底联网） |
| API 集成代理 | `gateway`（OpenAPI 网关）+ [mcp](mcp.md)（外部工具协议） |

组合原则：能力包数量控制在 3~5 件——工具表过长会稀释模型选择准确度；同类能力只挂一件（如 shell 与 cli.Terminal 不同时挂）。

## 6. mount：资源挂载管理（非 Talent）

`ai/talent/mount` 管理 skills/agents 资源目录的挂载（[ai-harness](ai-harness.md) 的底座之一）：

```go
src, _ := mount.NewFileMountSource("/data/skills")
m, _ := mount.NewMountBuilder().
    Alias("skills").Type("skills").Writeable(false).Source(src).Build()

mgr, _ := mount.NewMountManager("/workspace")
_ = mgr.Register(m)             // 另有 Remove/Get/Mounts
path := mgr.ResolveResource("skills/search/SKILL.md")
mgr.Skills(); mgr.Agents()      // 聚合列举已挂载资源
```

## 7. 模块结构

| 路径 | 职责 |
|---|---|
| `ai/talent/{file,sys,diff,web,generation,social,mail,memory,code,data}/` | 20 件核心能力包 |
| `ai/talent/mount/` | 挂载构建器与资源管理器 |
| `plugins/ai/talent/{gateway,pdf,cli,lsp,bleve,text2sql}/` | 6 件增强能力包 |
| `ai/talent.go` | Talent SPI 与 `ActiveTalents` 激活器 |
| `_test/ai_test/` `_test/ai_plugin_test/` | 黑盒测试（bleve/text2sql 等含中文分词与 sqlite 回放专项） |

## 8. 总结

- **能力即插即用**：一行 `o.Talents = append(...)`，指令与工具成套注入，模型"会"用而非"能"用
- **安全内建**：workDir 沙箱（file/sys/diff/generation）、ReadOnly（text2sql）、白黑名单（gateway/mcp）默认从严
- **依赖分层**：核心 20 件零负担，增强 6 件独立模块按需引入，编译产物不膨胀
- **生态互通**：Talent 经 [mcp 桥](mcp.md) 可导出为 MCP 服务、也可消费远端 talent；web.Search 复用 [RAG SPI](ai-rag.md)
- **Go 原生选型**：全文检索用 Bleve+gse、文档解析用 markitdown，全部纯 Go，无外部服务部署

### 延伸阅读

- [ai — 聊天核心（Talent SPI 定义）](ai.md)
- [mcp — Talent 跨进程互导](mcp.md)
- [ai-rag — 检索 SPI（web.Search/bleve 的契约）](ai-rag.md)
- [ai-harness — 用 Talent 组装编码代理](ai-harness.md)
- 架构设计：[../arch/ai/06-talents-and-extended.md](../arch/ai/06-talents-and-extended.md)
