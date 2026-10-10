# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test

```bash
# Run all tests (workspace root is NOT a Go module, so "..." wildcards do not
# expand from the repo root — list module paths explicitly, as below).
# All _test.go files live under _test/; the library paths below (aifei, config,
# …) carry no tests and are listed only to compile-check them.
go test ./ai ./ai/agent ./ai/flow ./aifei ./config ./dami ./db ./enjoy ./flow ./http ./json ./log ./mcp ./nami ./server \
         ./tools/generator ./tools/damigen \
         ./plugins/cache ./plugins/dami ./plugins/dataisolate ./plugins/flow ./plugins/kafka ./plugins/nacos ./plugins/storage ./plugins/swagger ./plugins/elasticsearch ./plugins/xxljob \
         ./_test/json_test ./_test/log_test ./_test/config_test ./_test/dami_test \
         ./_test/server_test ./_test/nami_test ./_test/storage_test ./_test/swagger_test \
         ./_test/nacos_test ./_test/damigen_test ./_test/generator_test \
         ./_test/dataisolate_test ./_test/db_test ./_test/cache_test ./_test/kafka_test ./_test/enjoy_test ./_test/flow_test ./_test/flow_plugin_test ./_test/ai_test ./_test/ai_agent_test ./_test/ai_flow_test ./_test/mcp_test

# Run tests for a single area (tests live under _test/<area>_test)
go test ./_test/db_test
go test ./_test/enjoy_test
go test ./_test/json_test
go test ./_test/log_test
go test ./_test/nacos_test
go test ./_test/generator_test
go test ./_test/config_test
go test ./_test/dami_test
go test ./_test/flow_test

# Run db integration tests (requires sqlite)
go test ./_test/db_test

# Run cache redis integration tests (embedded miniredis; no external redis)
go test ./_test/cache_test

# Run kafka integration tests (embedded franz-go kfake broker; no external kafka)
go test ./_test/kafka_test

# Run a single test
go test ./enjoy -run TestOutputExpr

# Run the demo
go run ./_test/demo
```

## 文档站点（VitePress）

文档站源码即 `docs/` 目录（内容原地发布），配置在 `docs/.vitepress/config.ts`。文档站自成独立 pnpm 包：`package.json`/`pnpm-lock.yaml`/`pnpm-workspace.yaml`/`scripts/` 均在 `docs/` 下，仓库根目录没有 node_modules。所有命令在 `docs/` 目录下执行（根目录等价写法 `pnpm -C docs <script>`）。

```bash
cd docs
pnpm docs:dev       # 本地开发（http://localhost:5173/aifei-go/，注意 base 前缀）
pnpm docs:build     # 构建到 docs/.vitepress/dist（构建即死链检查，失败会报 file:line）
pnpm docs:preview   # 预览构建产物
pnpm docs:clean     # 清理 dist 与缓存
pnpm docs:mermaid   # 校验所有 ```mermaid 块语法（构建不查 mermaid，改图后必跑）
```

内容规则：

- 站点收录 `docs/guide/` 与 `docs/arch/`；`docs/issues/` 和 `**/_*.md`（如 `guide/_STYLE.md`）通过 `srcExclude` 排除，不发布。
- 不要添加指向 `issues/`、`_` 前缀文件或 `docs/` 之外的相对链接——死链会让构建直接失败（这是有意的质量门禁）。
- 架构/流程/时序**图一律用 ```mermaid 代码块**（`vitepress-plugin-mermaid` 渲染）：flowchart 表架构与数据流、sequenceDiagram 表交互时序；节点文本必须双引号包裹、标签内勿用裸 `"`（用 `#quot;` 或去引号）、id 用短英文。**文件树（├──/└──）保持 ASCII 不转图**。新写/修改 mermaid 后跑 `pnpm docs:mermaid`——VitePress 构建不校验 mermaid 语法，错误只会在浏览器渲染时才暴露。
- 侧边栏/导航为手工维护的 curated 配置（`config.ts` 里的 `sidebar`），新增文档需同步更新。
- 部署：push 到 GitHub 远端 `master` 触发 `.github/workflows/docs.yml`，构建产物以孤儿单提交推到 `gh-pages` 分支，由 GitHub Pages（base `/aifei-go/`）对外服务。

## Testing Conventions

**All test code lives under `_test/` — never co-locate `_test.go` files inside the library packages** (do not add tests to `db/`, `enjoy/`, `db/sql/`, etc.). When writing a new test, add it to the matching `_test/<area>_test/` module; if no module exists for that area, create one following the rules below. This is why the `db/sql` parser and `SqlKit` unit tests live in `_test/db_test`, imported as `dbsql "github.com/crazy-airhead/aifei-go/db/sql"` rather than as a `package sql` test inside `db/sql/`.

Each test area is its own Go module:

| Module | Covers | Embedded test dependency |
|--------|--------|--------------------------|
| `_test/db_test` | `db` + `db/sql` (CRUD, pagination, transactions, batch, Enjoy SQL directives, SQL parser) | `modernc.org/sqlite` |
| `_test/cache_test` | `plugins/cache` (local + Redis two-level cache) | `miniredis` |
| `_test/kafka_test` | `plugins/kafka` (producer/consumer, at-least-once) | franz-go `kfake` broker |
| `_test/enjoy_test` | `enjoy` template engine (black-box) | — |
| `_test/flow_test` | `flow` engine + `flow/workflow` (claim/submit, gateways, snapshot) (black-box) | — |
| `_test/flow_plugin_test` | `plugins/flow` MysqlStateRepository + TaskHistoryRecorder (black-box; MySQL dialect via `FLOW_MYSQL_DSN`) | — |
| `_test/json_test` | `json` marshal/unmarshal wrappers (black-box) | — |
| `_test/log_test` | `log` logger interface + levels (black-box) | — |
| `_test/mcp_test` | `mcp` 协议层 + 客户端 + `ai/mcp` 桥：`UnmarshalMessage` 判别表驱动（畸形 id 拒绝）、会话 pending 表 50 并发乱序回包、版本协商矩阵、服务端分发/超时/关闭清场、内容多态；自二进制 stdio 回环（握手→分页列表→调用→ping→变更通知重拉）、协商失败/请求超时/能力门禁、心跳指数退避；桥工具映射/白黑名单/缓存 TTL/IsError 前缀/块映射/传输错误重连重试/假 OpenAI 函数调用循环；P3-c 服务端：streamable HTTP 全流程（会话建立/三注册表/变更广播收敛/资源订阅更新/日志级别过滤/DELETE 注销/Last-Event-ID 空回放/裸 HTTP 帧级语义 400/404/405/403/202/503/GET 流断自愈重握手/反向采样+进度走 POST 回写流）、stdio 分身回环、`RegisterTools` 反射注册、旧式 SSE 双端回环（含未知会话 404）、aifei `Middleware` 挂载互操作 (black-box) | —（httptest 假 OpenAI 回放） |
| `_test/config_test` | `config` layered loading, `Props`/`Store`/`Sub`/`Bind` (black-box) | — |
| `_test/dami_test` | `dami` event bus (send/call/stream/lpc) + `plugins/dami` (black-box) | — |
| `_test/server_test` | `server` bootstrap: `In`/`Out`, `IoHandler`, middleware (black-box) | — |
| `_test/nami_test` | `nami` RPC client + `util`/`coder/json`/`channel/http` subpackages (black-box) | — |
| `_test/storage_test` | `plugins/storage` local + S3 clients, `Manager` (black-box) | `minio-go` |
| `_test/swagger_test` | `plugins/swagger` config loading (black-box) | — |
| `_test/dataisolate_test` | `plugins/dataisolate` tenant/row/column isolation (rewriter unit tests + sqlite integration) (black-box) | `modernc.org/sqlite`, `github.com/ajitpratap0/GoSQLX` |
| `_test/nacos_test` | `plugins/nacos` `NamiUpstream` (black-box) | — |
| `_test/damigen_test` | `tools/damigen` dami-provider codegen (black-box) | — |
| `_test/generator_test` | `tools/generator` schema→code (`MetaReader`, type mapping) (black-box) | `modernc.org/sqlite` |
| `_test/ai_agent_test` | `ai/agent` Agent base (`AgentBase` flow-node run, session/pending, team seam) + `ai/agent/simple` (call pipeline, retry-on-transport-error-only, session window, `OutputKey`, `OutputSchema`, stream forwarding) + `ai/agent/team` (Sequential 零 LLM 拓扑、Hierarchical 主管路由/错误阻断、Swarm 涌现池/信息素、copy-before-mutate、拦截器三层接线、feedback 挂起恢复、Looping 死循环检测) (black-box) | —（httptest 假 OpenAI 服务回放 JSON/SSE） |
| `_test/ai_flow_test` | `ai/flow` AI 流编排 12 组件：chat_case1 链逐字执行、stream:true 冷启动句柄、D8 附件缓存、节点事件订阅、exclusive 网关 enjoy 条件、注册表与 P4/P5 桩 (black-box) | —（httptest 假 OpenAI 服务回放 JSON/SSE） |
| `_test/demo` | full demo app (run with `go run`, not a test suite) | `modernc.org/sqlite` |

Rules for adding tests:

- **External test package:** declare `package <area>_test` (matching the directory name) and test the **exported API only**. Import the library normally; alias sub-packages when convenient (e.g. `dbsql "github.com/crazy-airhead/aifei-go/db/sql"`). If you need to exercise unexported behavior, expose a test hook or assert via observable output/SQLite — do not drop a `package <lib>` test file into the library package.
- **Own module per area:** `module github.com/crazy-airhead/aifei-go/_test/<area>_test`, with `replace` directives pointing at the local modules it needs (`../../db`, `../../enjoy`, …), and add `use ./_test/<area>_test` to `go.work`.
- **Self-contained:** prefer embedded/in-process dependencies so `go test` needs no external services — SQLite via `modernc.org/sqlite`, `miniredis` for Redis, franz-go `kfake` for Kafka.
- **Issue regressions:** name the file `issueNNNN_test.go` inside the relevant `_test/<area>_test/` module, matching the `docs/issues/NNNN-*.md` note number.

> **Running tests:** the workspace root is not itself a Go module, so `...` wildcard patterns (e.g. `./_test/...`, `./plugins/...`) do **not** expand from the repo root. Run tests by explicit module path — `go test ./_test/db_test` — or `cd` into the module and run `go test ./...`.

## Module Structure (Go Workspace)

This project uses a Go workspace (`go.work`) of independent modules, layered by role: **Core** (the `aifei` framework itself), **Core library** (zero-external-dep primitives usable standalone), **Runtime** (the default `net/http` adapter + production bootstrap for aifei), **Standalone framework** (sibling frameworks with no dependency on aifei), **Code generation** (`tools/`), **Plugin** (optional integrations that pull in third-party libraries), and **Example** (demos/integration tests under `_test/`, not published).

| Layer | Module | Path | Dependencies |
|-------|--------|------|--------------|
| Core | `github.com/crazy-airhead/aifei-go` | `./aifei` | — |
| Core library | `…/aifei-go/enjoy` | `./enjoy` | — |
| Core library | `…/aifei-go/db` | `./db` | enjoy, log |
| Core library | `…/aifei-go/json` | `./json` | — |
| Core library | `…/aifei-go/log` | `./log` | — |
| Core library | `…/aifei-go/mcp` | `./mcp` | log（solon-ai 迁移的 MCP 双端：协议层 JSON-RPC 帧判别/域类型/版本协商/会话 pending 表 + stdio/streamable-HTTP/旧式 SSE 三传输 + `Client` 门面与心跳重连（P3-b）+ `Server` 门面三注册表/反向采样征询/变更广播/`RunStdio`/安全钩子（P3-c，P3 收口）） |
| Core library | `…/aifei-go/config` | `./config` | `yaml.v3` |
| Core library | `…/aifei-go/ai` | `./ai` | mcp, log, enjoy（solon-ai 迁移的聊天模型核心：消息/事件/工具/方言/会话；依赖经 `ai/mcp` 桥引入——核心包本身零依赖；`ai/rag` 子包 P4 RAG SPI，enjoy 仅其过滤表达式用） |
| Core library | `…/aifei-go/ai/agent` | `./ai/agent` | ai, flow + `dami`/`enjoy`/`yaml.v3` 间接依赖（solon-ai agent 层：AgentBase/Session/Trace/Metrics/RetryTask + `simple/` 简单智能体 + `react/` ReAct 智能体 + `team/` 多智能体协作） |
| Core library | `…/aifei-go/ai/flow` | `./ai/flow` | ai, flow + `dami`/`enjoy`/`yaml.v3` 间接依赖（solon-ai-flow 迁移的 AI 流编排：12 组件 + `RegisterDefaults` + attrs 常量表，flat `aiflow` 包） |
| Runtime | `…/aifei-go/http` | `./http` | aifei |
| Runtime | `…/aifei-go/server` | `./server` | aifei, http, db, enjoy, log |
| Standalone framework | `…/aifei-go/nami` | `./nami` | — |
| Standalone framework | `…/aifei-go/dami` | `./dami` | — |
| Code generation | `…/aifei-go/tools/generator` | `./tools/generator` | db, enjoy |
| Code generation | `…/aifei-go/tools/damigen` | `./tools/damigen` | enjoy |
| Plugin | `…/aifei-go/plugins/cache` | `./plugins/cache` | aifei, config, log + `jetcache-go`, `go-redis` |
| Plugin | `…/aifei-go/plugins/dami` | `./plugins/dami` | aifei, dami, log |
| Plugin | `…/aifei-go/plugins/kafka` | `./plugins/kafka` | aifei, config, log + `franz-go` |
| Plugin | `…/aifei-go/plugins/nacos` | `./plugins/nacos` | aifei, nami, log + `nacos-sdk-go` |
| Plugin | `…/aifei-go/plugins/storage` | `./plugins/storage` | aifei, config, log + `minio-go` |
| Plugin | `…/aifei-go/plugins/swagger` | `./plugins/swagger` | aifei, config, log + `swaggo/swag` |
| Plugin | `…/aifei-go/plugins/dataisolate` | `./plugins/dataisolate` | aifei, config, db, http, log, server + `github.com/ajitpratap0/GoSQLX` |
| Example | `_test/demo` | `./_test/demo` | core + db + generator + `modernc.org/sqlite` |
| Example | `_test/db_test` | `./_test/db_test` | db + `modernc.org/sqlite` |
| Example | `_test/cache_test` | `./_test/cache_test` | `miniredis` |
| Example | `_test/kafka_test` | `./_test/kafka_test` | `franz-go/kfake` |
| Example | `_test/enjoy_test` | `./_test/enjoy_test` | enjoy |
| Example | `_test/json_test` | `./_test/json_test` | json |
| Example | `_test/log_test` | `./_test/log_test` | log |
| Example | `_test/mcp_test` | `./_test/mcp_test` | mcp, ai, aifei, server |
| Example | `_test/config_test` | `./_test/config_test` | config |
| Example | `_test/dami_test` | `./_test/dami_test` | dami, plugins/dami |
| Example | `_test/server_test` | `./_test/server_test` | server, aifei |
| Example | `_test/nami_test` | `./_test/nami_test` | nami |
| Example | `_test/storage_test` | `./_test/storage_test` | plugins/storage + `minio-go` |
| Example | `_test/swagger_test` | `./_test/swagger_test` | plugins/swagger |
| Example | `_test/dataisolate_test` | `./_test/dataisolate_test` | plugins/dataisolate + `modernc.org/sqlite` |
| Example | `_test/nacos_test` | `./_test/nacos_test` | plugins/nacos |
| Example | `_test/damigen_test` | `./_test/damigen_test` | tools/damigen |
| Example | `_test/generator_test` | `./_test/generator_test` | tools/generator + `modernc.org/sqlite` |
| Example | `_test/ai_test` | `./_test/ai_test` | ai（消息/事件/工具/方言/流式 + `ai/rag` P4-a/b：Document/TextLoader/相似度工具/InMemory 生命周期、检索全参数矩阵、SnEL→enjoy 过滤迁移表、4 切分器+Pipeline（假 Tokenizer 对照/假嵌入主题切点）、PromptAugment/RepositoryTool（假 agent 循环回注+Reranker 重排），黑盒；httptest 假服务回放 + 确定性假嵌入器） |
| Example | `_test/ai_agent_test` | `./_test/ai_agent_test` | ai/agent（Agent 基座 + Simple + ReAct + Team：flow 图节点、会话/挂起、重试、流式转发、reason/action 循环与拦截器、多智能体三协议协作，黑盒；httptest 假服务回放） |
| Example | `_test/ai_flow_test` | `./_test/ai_flow_test` | ai/flow（AI 流编排 12 组件：chat_case1 链逐字执行、冷启动流句柄、D8 附件缓存、节点事件、exclusive 网关 enjoy 条件，黑盒；httptest 假服务回放） |

`…` = `github.com/crazy-airhead`. In the Dependencies column, bare names are internal modules; backticked names are external libraries; `—` means no dependencies.

Users can import individual modules without pulling unwanted dependencies:
- `go get github.com/crazy-airhead/aifei-go/enjoy` — template engine only, zero external deps
- `go get github.com/crazy-airhead/aifei-go/db` — database access only, zero external deps (user provides their own driver)
- `go get github.com/crazy-airhead/aifei-go` — core web framework, zero external deps
- `go get github.com/crazy-airhead/aifei-go/nami` — HTTP RPC client framework, zero external deps
- `go get github.com/crazy-airhead/aifei-go/plugins/nacos` — Nacos plugin (service registry, config center, discovery)
- `go get github.com/crazy-airhead/aifei-go/plugins/storage` — storage plugin (local filesystem + S3-compatible backends)
- `go get github.com/crazy-airhead/aifei-go/plugins/cache` — cache plugin (local FreeCache/TinyLFU + Redis two-level cache)
- `go get github.com/crazy-airhead/aifei-go/plugins/swagger` — knife4j-vue3 OpenAPI docs plugin (embedded UI, serves spec via swaggo/swag)
- `go get github.com/crazy-airhead/aifei-go/plugins/kafka` — Kafka plugin (franz-go producer/consumer, multi-cluster, at-least-once Subscribe)
- `go get github.com/crazy-airhead/aifei-go/plugins/dataisolate` — data isolation plugin (tenant isolation + row/column isolation; AST SQL rewriting via GoSQLX)

Requires Go 1.27. All library code uses only the Go standard library.

## Architecture

Aifei-Go is a lightweight Go web framework ported from [Aifei Java](https://github.com/jfinal/aifei). It follows a "Just Service" philosophy — flat architecture, no Controller/Service/DAO layers.

### Core (`./aifei`)

- **`aifei.go`** — `Aifei` struct: entry point with `New()`, `Use()`, route methods (`GET`/`POST`/`PUT`/`DELETE`/`PATCH`/`Any`), `Handle()`, `Group()`. Transport-agnostic — does **not** itself implement `http.Handler`; that bridging lives in `http.HttpHandler` and `server.IoHandler`. (Struct-method → route registration is `server.Register()`, NOT a method on `Aifei`.)
- **`input.go`** — `Input` interface: request parameter abstraction. Methods: `Has()`, `PathPara()`, `GetStr()`, `GetInt()`, `GetInt64()`, `GetFloat64()`, `GetBool()`, `GetBean()`, `Body()`, `Method()`, `Path()`, `RemoteIP()`, `Query()`.
- **`output.go`** — `Output` interface: response abstraction. Methods: `Code()`, `Msg()`, `Data()`. The `server` package provides the `Out` struct implementing this.
- **`handler.go`** — `HandlerFunc func(in Input) Output`. `ChainHandlers()` composes handler chains from `Handler` wrappers.
- **`router.go`** — Radix tree router per HTTP method. Supports `:param` and `*catchAll`. `RouterGroup` for grouped routes with prefix + handler wrappers. (Reflection-based struct-method routing lives in `server/register.go` — see server bootstrap & Naming Conventions.)
- **`interceptor.go`** — `Interceptor` interface for method-level AOP. `InterceptorFunc` adapter. `MethodInterceptors` interface for services with per-method interceptors.
- **`config.go`** — `Config` with functional options pattern (`WithHandlers`, `WithPlugin`, `WithOnStart`, `WithOnStop`). `WithHandlers` accepts `Handler` wrappers.
- **`plugin.go`** — `Plugin` interface (`Start()`/`Stop()` lifecycle).

### HTTP Adapter (`./http`)

Bridges `net/http` to the aifei framework:
- **`context.go`** — `HttpContext` implements `aifei.Input` by wrapping `*http.Request`.
- **`handler.go`** — `HttpHandler` implements `http.Handler`, bridging to `aifei.Aifei`.
- **`server.go`** — `Server` interface and `DefaultServer` (net/http implementation).

### Server Bootstrap (`./server`)

Convenience layer for production use:
- **`in.go`** — `In` struct: full `aifei.Input` implementation (wraps `*http.Request`).
- **`out.go`** — `Out` struct: fluent `aifei.Output` builder (`Ok()`, `Fail()`, `Of()`, `OfField()`, `SetMsg()`, `SetData()`, `IsOk()`, `ShouldRollback()`).
- **`io_handler.go`** — `IoHandler`: implements `http.Handler`, bridging `net/http` to aifei (route lookup → builds `*In` → runs the handler chain → renders `Out`); wired via `WithHTTPHandler()`.
- **`middleware.go`** — Built-in `Handler` wrappers: `Logger()`, `Recover()`, `Timeout()`, and HTTP-level wrappers: `CORS()`, `BasicAuth()`, `RequestID()`, `StaticFile()`.
- **`run.go`** — `Run(app, addr, opts...)` — starts server with graceful shutdown, plugin lifecycle, signal handling.
- **`register.go`** — `Register(router, prefix, service, handlers...)`: reflects over a service struct's exported methods and maps each to a route via `resolveRoute()` (two rules — see Naming Conventions). This powers struct-based "Just Service" routing.
- **`service.go`** — `RegisterService()` + `AutoRegisterServices(app)`: generated `service.go` files self-register in `init()`; `AutoRegisterServices` calls `Register` for each.
- **`tx_interceptor.go`** — `TxInterceptor()` for automatic transaction wrapping.

### Enjoy Template Engine (`./enjoy`)

~2,500 lines, the framework's signature feature. Custom template language with its own lexer/parser:
- **DKFF algorithm** for tokenization, **DLRD recursive descent** for parsing
- Supports: `#()` expression output, `#if`/`#else`/`#elseif`, `#for`, `#set`/`#setLocal`/`#setGlobal`, `#define`/`#call`, `#include`, `#switch`/`#case`/`#default`, `#break`/`#continue`/`#return`
- Expression engine: arithmetic, comparison, logic, ternary, null-safe (`??`, `?.`), method calls, map/array literals, static access (`::`)
- `Engine` → `Template` → `Env`/`Scope` execution model
- Configured via `EngineConfig` (custom directives, shared functions/objects)
- Flat file structure (not subdirectory-based)

### Database (`./db`)

- **`db.go`** — Top-level convenience functions: `Use()`, `SQL()`, `Select()`, `Insert()`, `Update()`, `Delete()`, `FindByID()`, `FindBy()`, etc.
- **`dao.go`** — `Dao`: chainable query builder for single-table CRUD operations.
- **`row.go`** — `Row`: Active Record pattern with change tracking. `Set()` tracks changes (used for UPDATE), `Put()` does not.
- **`config.go`** — `Config`: connection management with `db.Init(driver, dsn, ...)`. Supports multiple named configs via `InitWithID()`. Lazy connection pool.
- **`batch.go`** / **`transaction.go`** — Batch operations and transaction support.
- **`dialect.go`** — Database-specific SQL generation (MySQL, PostgreSQL, SQLite).
- **`type_converter.go`** — Type conversion helpers (`ToInt`, `ToStr`, `ToTime`, etc.).
- **`table.go`** — `Table`: runtime table metadata for code generation.
- **`db/sql/`** — Enjoy SQL: `SqlKit` wrapping Enjoy engine with `#sql`, `#para`, `#where`, `#and`, `#orderBy` directives supporting 18 operators.

### Code Generator (`./tools/generator`)

Generates type-safe per-table packages from database schema:
- **`generator.go`** — Main entry point: `New(pool, dialect, outputDir, importRoot)`. `Qualified` switches to table-qualified identifiers for multi-table packages (`Generator.Force` regenerates model/dao/service even when they exist); rendered output is always gofmt'ed.
- **`names.go`** — `buildNames`: identifier parameterization. One template renders two naming scopes — default bare names (`Table`, `NewDao`, `FindById`; one table per package) vs Qualified (`TableUser`, `NewUserDao`, `UserFindById`, files `base_<table>.go`; several tables per domain package). Mapping several tables into one package without `Qualified` fails fast with the conflicting table list.
- **`meta_reader.go`** — Reads DB metadata (table names, columns, types) via `ColumnTypes`.
- **`meta_dialect.go`** — Dialect-specific metadata queries (MySQL, PostgreSQL, SQLite).
- **`type_mapping.go`** — SQL type → Go type mapping (30+ types).
- **`base_generator.go`** — Generates `base.go` (always overwritten): `BaseXxx` struct, `Table` var, getters/setters, and the exported typed-row bridge `FromRow`/`FromRows` (raw-SQL `[]*db.Row` → typed models; `NewWithRow` runs `initRow` so wrapped rows support Update/Delete).
- **`model_generator.go`** — Generates model file (skipped if exists; `Force` overwrites): model struct + a shadowing `InitRow` (satisfies `db.RowEntity` for the generic As-family; a zero-value model's embedded base is nil, so the promoted base method can't be used).
- **`dao_generator.go`** — Generates `dao.go` (skipped if exists; `Force` overwrites): typed wrapper whose terminals (`Find`/`FindFirst`/`FindOne`/`Paginate`/`FindBy`/`FindFirstBy`/`FindByID`) delegate one line to db's Go 1.27 As-family (`FindAs[T]`, `PaginateAs[T]`, …), plus `FindById`, `FindByIds`/`DeleteByIds`/`FindIn` (batch IN) and typed pagination via `XxxPage = db.PageAs[*Xxx]` alias (same json envelope as `db.Page`).
- **`service_generator.go`** — Generates `service.go`: HTTP service with method routing; `Template` field overrides the embedded `_service.af` with an app-specific Enjoy template (same data map, gofmt'ed output).
- **`init_generator.go`** — Generates `init.go` (always overwritten): blank imports per package (deduplicated when several tables share one package).
- **`templates/`** — Embedded Enjoy templates: `_base.af`, `_model.af`, `_dao.af`, `_service.af`, `_init.af`.

### Other Packages

- **`./json`** — Lightweight JSON marshal/unmarshal wrappers.
- **`./log`** — Logging interface (`Logger` with 5 levels) + default implementation.
- **`./nami`** — Lightweight HTTP RPC **client** framework (ported from Java Solon Nami). Channel transport (`channel/http`), Encoder/Decoder (`coder/json`), `Filter` chain, `Upstream`/`Discovery`, fluent `Builder`/`ClientFactory`, and a `util` package (`GetJSON[T]` etc.). Server-side counterpart to aifei; zero external deps.
- **`./ai/agent`** — Agent layer of the solon-ai migration (P2-a; depends on `ai` + `flow`). `Agent` interface = `flow.NamedTaskComponent`, so an agent drops into a flow graph via `s.AddActivityNamed(agent)` and runs through `AgentBase.Run` (default run): `_SESSION_` computeIfAbsent (`agent.NewMemorySession`), team seam via `_current_team_trace_key_` (interceptor veto → `[Skipped] Cancelled by …` record; `NodeStart`/`NodeEnd` agent events; `TeamProtocol.ResolveAgentOutput` post-processing — fallback text only when the *resolved* result is empty), then `self.Call(prompt, session)`. `Session` = `ai.Session` + `flow.Context` snapshot + `Pending(bool, reason)` suspend/resume (suspend = `ctx.Stop()` + cross-engine stopped flag via `flowContext.SetStopped`; resume clears the flag only — Java `stopped(false)` semantics; the exchanger-level stop is whole-run and never cleared here). `Trace`/`Metrics` accumulate usage per run and aggregate only the per-run delta into the parent team trace (duration excluded). `RetryTask` is full-jitter exponential backoff, ctx-aware, generic-method `Run[T]`. Subpackage `simple` ports `SimpleAgent`: instruction-augmented system prompt (`#{var}` rendered from the flow context), session history window (user-aligned rounds via `LatestMessages`), trace keyed `"__"+name` in the context (empty prompt resumes `OriginalPrompt`; empty-empty returns an empty assistant with zero HTTP), retry on transport errors only (LLM-no-return sentinel ∪ HTTP 5xx/429 ∪ net.Error ∪ EOF; business errors surface immediately), `OutputKey` writes the answer into `session.Context()`, `OutputSchema` → `response_format` + schema instruction, ThinkingOnly downgrade, tool-call answers not archived, `Handler` fallback when no `ChatModel`. Streaming: `Request.Stream`/`StreamChan` forward `SimpleStart`/`SimpleDelta` (TEXT/THINKING/MEDIA events) and aggregate a terminal `SimpleEnd`; consumer cancellation (sink sentinel or `context.Canceled` once the bound goCtx fired) maps to Java interrupt→null — empty assistant, no `SimpleEnd`, no retry. Subpackage `react` ports `ReActAgent` (P2-b): `Config`/`Builder`/`Agent` + `Request`/`Response` (per-call options copy — resume inherits the bound trace options), `Trace` (route state machine reason/action/end, turns, maxTurns incl. `AddMaxTurns` autoRethink续航, working memory, plans, extras), `Options` (shadows `ChatOptions.Interceptors` with the full react `Interceptor` lifecycle interface — the values still ride into chat-layer `Interceptors` on each request), `ReasonTask` (stable system prompt + per-turn messages incl. plan-context user message, empty-response retry with reflect/format-fix prompts, text-protocol `Action:`/finish-marker routing, retry-unsafe-stream detection, friendly-fallback error handling), `ActionTask` (native tool-calls + text-mode JSON Action streaming parse, `ToolExchanger` rich results, 100%-close per-tool finally incl. pendingWithoutResult, returnDirect all-success join, feedback-tool suspend), `StopLoopInterceptor` (canonical-JSON action fingerprint sliding window), `ToolRetryInterceptor` (physical linear-backoff retry + args self-heal via `ErrInvalidArguments` sentinel; error class travels in `ToolResult.Meta` because Go chain terminals mark errors instead of throwing). ReasonDelta forwards only TEXT/THINKING/MEDIA_DONE chat events; abnormal ends get a补位 TEXT_DELTA/TEXT_END pair before `RunEnd`. Subpackage `team` ports the multi-agent collaboration layer (P2-c): `Agent` wraps a flow graph built by a `Protocol` and driven per-call by `Agent.call` (trace `"__"+name` keyed in the session context; new prompt resets trace + deletes the graph's flow last-record anchor so the engine restarts from `start`, empty prompt resumes from the anchor via engine revert; team interceptors are mounted as engine flow-interceptors per eval and removed after). `Protocol` = 4-stage contract (graph build / agent-side inject+prepare / supervisor-side prepare+resolve+guard / routing callbacks) with `ProtocolBase` defaults; `PrepareAgentPrompt` copies the shared working-memory prompt before mutation (copy-before-mutate across members). `SupervisorTask` is the LLM decision hub: interceptor admission → max-turns/route-END fuse → protocol gate → dispatch (system prompt = base template + protocol dashboard, user content = history window + turn + standby experts) → decision parsing as a state machine (`commitRoute`: suspend via feedback tool → `Pending` + `Interrupt` with route kept at `supervisor` — NOT `end`, a deliberate 1-line deviation from Java so resume re-enters the decision loop instead of tripping the route-END fuse; FINISH rejected by protocol → re-assign, never silent fall-through to END; unresolvable → re-dispatch or terminated-with-reason). Protocols: `SequentialProtocol` (static pipeline, zero supervisor LLM; quality gate + per-stage retry; multimodal skip; state recovers from records on resume), `HierarchicalProtocol` (运行看板 `_reports`/`_results`/`_meta.errors` absorbed from member output, FINISH physically blocked while errors outstanding + first-error re-assign, active-instruction 透传, agent_usage load stats), `SwarmProtocol` (decentralized start → first expert, `__emerge_tasks__` tool + sub_tasks JSON 涌现 into a capped task pool, pheromone decay/reinforce scheduling, FINISH hard-blocked until pool drains). `Interceptor` embeds both `ai.Interceptor` and `flow.Interceptor` (one object, three layers: team lifecycle / supervisor decision / member + flow node callbacks); `LoopingTeamInterceptor` detects self-stall and A-B-(C) sequence loops via normalized edit-distance similarity. Nested teams: member `*Agent` finish detection reads the nested trace (`Route()==end` or non-empty `FinalAnswer`), and Hierarchical stops requiring the finish marker in the nested final answer.
- **`./ai/flow`** — AI 流编排层 of the solon-ai migration (P2-d; solon-ai-flow port), single flat `aiflow` package (files `inputs.go`/`outputs.go`/`models.go`/`repositorys.go` mirror the Java subpackages; flat avoids the root↔subpackage import cycle around `ComponentBase`). `Component` = `flow.NamedTaskComponent` + `Description()`; YAML task `@Name` resolves via container. `ComponentBase` carries the IO conventions (input name = meta `input` else `message`, read meta-first-then-context; output name = meta `output` else `message`; attachment name = meta `attachment` else `attachment`, read context-first) and property accumulation (context key `property_<name>` holding a list; `SetProperty` wraps single value). `RegisterDefaults(container)` registers the 12 components: VarInput/VarCopy/ConsoleInput/WebInput, VarOutput/ConsoleOutput/WebOutput, ChatModel/EmbeddingModel/GenerateModel (P5 stub), InMemoryRepository (P4 stub)/RedisRepository (P5 stub). ChatModel builds from `chatConfig` meta (JSON round-trip → `ai.Config`), caches the build (model + resolved tools) in the node attachment key `aiflow.chatModel` (D8 方案 A — second eval reuses, observable via identical `*ai.ChatModel` pointer in `property_chatModel`), session = context `chatSession` `PutIfAbsent` (meta-overridable), `systemPrompt` runs through the SnEL-style template (`#{expr}` enjoy-evaluated, `${name}` direct — no-marker short-circuit), `stream: true` outputs a **cold** `*StreamHandle` (request fires only on first `Stream()`/`Each()` — Java cold Flux semantics; `InputAsString` aggregates TEXT deltas). Each component's `Run` wraps doRun in node events `flow.node.start`/`flow.node.end` via `ctx.EventBus()` (end only on success — Java has no finally). `toolProviders` meta maps to the registry `RegisterToolProvider(name, tools...)`/`ToolProvider(name)` (Go has no class reflection), tools injected per-request via `req.Options`; `mcpServers` is a reserved seam → explicit "requires P3" error. IO seams are package vars for tests/deploy injection: `ConsoleWriter`/`WebWriter` (`io.Writer`), `Stdin` (`io.Reader`); WebOutput writes per-delta frames + flush, format meta renders once. Deliberate deviations from Java: EmbeddingModel writes the attachment back (Java read it but never cached — rebuild bug), WebInput errors on input+attachment both null (matches its message, not Java's contradictory condition), template nil renders as `""` not `"null"`.
- **`./ai/mcp`** — MCP→ai 桥 of the solon-ai migration (P3-b; package `aimcp` at `ai/mcp`,对标 McpClientProvider). `Provider` implements `ai.ToolProvider`: `Tools()` = ListAllTools → `ai.NewTool` mapping (title/desc from tool fields, `returnDirect` from annotations with default false, `InputSchema`/`OutputSchema` marshaled to JSON strings, `_meta` merged via MetaSet) with 白/黑名单 filtering at the single Go entry point (Java splits getTools/getToolsActivated — Go unifies, deliberate) + 游标缓存 (default 30s, `WithCacheTTL`; key `getTools:`, invalidated by the tools-change consumer registered in `NewProvider`). `CallTool` → `executeWithRetry` (package-level generic — Go methods can't take type params; transport error → `client.Reset()` + retry once, business errors pass through) → content mapping to a rich `*ai.ToolResult` (Blocks/IsError/Meta): TextContent → TextBlock (+`"Error: "` prefix when IsError and not already prefixed), Image/Audio → OfURL when data contains `"://"` else base64-decode → OfBase64, EmbeddedResource/ResourceLink → `*ai.ResourceLink` (Java drops both — Go enhancement), meta copied. The rich result rides the ai-core pass-through added in `toolImpl.Call` (handler returning `*ToolResult` bypasses stringification). Resource/prompt bridges defer to P4 (ResourceProvider/PromptProvider interfaces land with talents).
- **`./ai/rag`** — RAG SPI 与内置实现 of the solon-ai migration (P4-a/b; 契约 `docs/arch/ai/05-rag-design.md`). SPI：`Document`（内容+元数据+嵌入矢量；score 为瞬态未导出字段，`Meta`/`MetaMap` nil 值忽略；`String()` Java toString 同款）、`Loader`（`AdditionalMetadata` 链式返回接口类型——Java 同款；ctx 化 `Load` 为 Go 增量）、`Splitter`、`Repository` 只读检索面（`Search`/`SearchQuery`）+ 能力接口 `Storer`（Save 进度回调/DeleteByID/ExistsByID；Java `asyncSave` 不迁——调用方 `go` 即得）/`Lifecycle`/`FilterTransformer`（能译成原生过滤的后端返回 (native, ok)，否则使用方回退内存过滤）——database/sql 风格类型断言探测，替代 Java 接口继承。`Query`（QueryCondition 对应物）：`NewQuery` 预填缺省 limit=4/threshold=0.4/vector，`Refilter` 对结构体字面量的 `<=0` 亦有兜底；`SearchType` Vector 排首位故零值即缺省；`HybridParams` 钳制 [0,1] 两权重和 1；`Freshness` 为真实时间窗过滤（Java 同名枚举零消费方——Go 落地：文档时间取 `Metadata["createdAt"]` 接受 time.Time/int64 Unix 秒/RFC3339 串，窗口开启而无有效时间的文档即排除）；`FilterExpr` 以 enjoy 求值（Metadata 为作用域——SnEL 的 Go 对应物，`==`/`&&`/`||`/`!`/contains/startsWith 迁移表见 `_test/ai_test/rag_test.go`；解析缓存 `sync.Map`；真值映射同 flow：nil→false、bool→原值、其余→true）与 `FilterFunc` 叠加；`DisableRefilter` 仅跳过表达式/函数/新鲜度前置过滤——阈值/排序/截断恒执行（Java 同名语义）。`TextLoader` 收敛 Java 五构造为 string/[]byte/io.Reader/文件路径（构造无 IO，错误面收口在 Load；charset 零依赖收窄 utf-8 + utf-16 族含 BOM 嗅探且 BOM 优先于显式端序，其余编码显式报错）。`InMemoryRepository`：`Embedder` 窄接口缝（`Embed`/`EmbedTexts`/`BatchSize`，`*embedding.Model` 满足；测试与装配注入假实现）、Save 按 `BatchSize()`（`<=0`→10）分批 `EmbedTexts` 回填矢量（progress 批次粒度 done/total，空集回调 (0,0)；空 ID 补 crypto/rand v4 形态 UUID；同 ID 覆盖）、检索 = 嵌入查询 → 快照 → 暴力余弦 `CopyAndScore` 副本 → Refilter（无嵌入文档报错；nil 嵌入器晚失败——Save/Search 时报错，空集保存除外）、`DropRepository` 清表 / `InitRepository` 空操作。相似度工具（`CosineSimilarity`/`DotProduct`/`EuclideanDistance`/`EuclideanSimilarity`）Java IllegalArgumentException → Go error（nil 矢量/长度不等/零范数）。P4-b 切分器四件 + Pipeline（`splitter.go`/`splitter_json.go`/`splitter_semantic.go`；子块经 `chunkOf` 拷贝父元数据——Java 构造器 putAll 同款）：`RegexTextSplitter`（缺省空行；`KeepSeparator` Go 增量把匹配段并回前块）、`TokenSizeTextSplitter`（jtokkit 缺位 → `Tokenizer` 缝 `TokenCount(s) int` 可注入 + 内置 `ApproxTokenCount` 字符近似：CJK 1 字 ≈ 1 token、其余 4 字符 ≈ 1；算法移植为「计数不超过 chunkSize 的最长 rune 前缀（线性扫描——BPE 前缀计数非单调，二分不成立）→ 优先截到最后句读 .?!\n → 过短块过滤 → 按截短文本 rune 精确推进」，缺省 500/300/5/1000/true，maxChunkCount 为软上限——循环截断后尾巴仍补，Java 怪癖保留）、`JsonSplitter`（数组递归展开/对象叶子成文档/标量丢弃；`ContentSelector` 缺键渲染 null、`MetadataMapper` 产物整表直写、父元信息 putIfAbsent；数值 float64——"1" vs "1.0" 已知化妆级偏离）、`SemanticSplitter`（拆句保分隔符 → 滑窗与下一句嵌入余弦（一批收集按 BatchSize 分批）→ 低于阈值切点 + 最小句数约束 → 超长组按 token 再切；句子数 ≤ 窗口整文直返不求嵌入；构造校验 Java IllegalArgumentException → error；Java 硬编码 dimensions(512) 不迁——缝无逐次选项）、`SplitterPipeline`（`Next` 尾端追加，variadic 兼容 Java 单个/List）。检索辅助（`repository.go`）：`Reranker` 接口位（具体模型 P5 方言落地）、`PromptAugment`（对标 promptAugment 默认方法：Search → `ai.OfUserAugment`，References 段经 `Document.String()`）、`NewRepositoryTool`（对标 RepositoryTool：工具名 repository_query、queries ≤5 生效/空词跳过、topK `<=0`→3、多重检索 Markdown 汇总（标题/相关度 %.2f/内容 2000-rune 截断保护/引用地址）、`WithReranker` 命中后重排、nil repo panic=Java requireNonNull；经 `ai.ToolDesc` 构建 → 可直接挂 ChatOptions.Tools 进 agent 循环）。
- **`./mcp`** — MCP 协议层与双端 of the solon-ai migration (P3-a/b/c; D6 自研 per `docs/arch/ai/04-mcp-design.md`, no official go-sdk). 协议层四文件: `schema.go` JSON-RPC 2.0 frames + `UnmarshalMessage` 单一判别入口 (method+id→Request / method→Notification / result|error→Response / else reject) with a `json.Decoder.UseNumber()` probe map so large-int ids (2^53+1) don't lose float64 precision; `NormalizeID` restricts ids to string|int64 — float-fractional/null/bool/array/object → InvalidRequest (deliberate Go hardening; Java convertValue validates nothing; response id may be nil — JSON-RPC error responses — session discards with error log, Java same). `types.go` full domain types: method/notification constants, capabilities (client roots/sampling/elicitation, server tools/resources/prompts/logging/completions), Initialize, tools/resources/prompts families with pagination, sampling/elicitation/completion; Content polymorphism (text/image/audio/resource/resource_link) via constructors that stamp the `type` field + `UnmarshalContent` discriminator, ResourceContents via Java Id.DEDUCTION equivalent (presence of `text` vs `blob` key — no type field); interface-typed struct fields (`CallToolResult.Content`, `PromptMessage`, `SamplingMessage`, `CreateMessageResult`, `ReadResourceResult`, `EmbeddedResource`) each get a custom `UnmarshalJSON` through the discriminators; `LoggingLevel` carries `Severity()` ordering. `version.go` protocol version constants + `Latest` (client handshakes with the last/ascending entry) + `Negotiate` (server-returned version must be in client list else -32602 "Unsupported protocol version"). `session.go` ports `McpClientSession`: `Transport` seam (`Send`/`Receive`/`Close` — stdio + streamable-HTTP transports land in P3-b/c), `Session` = pending `sync.Map` (int64→buffered-1 chan) + atomic int64 ids (contract, not Java's string-prefix ids), pump dispatches responses synchronously (wake pending; nil-id → error log discard; unknown id → warn) and requests/notifications in goroutines (missing request handler → MethodNotFound, `roots/list` special "Roots not supported" + reason data; handler panic → InternalError); `SendRequest` timeout = caller ctx; `*JSONRPCError` implements `error` so callers `errors.As` code/data; `Close`/transport termination dismiss all pending with `ErrSessionClosed` ("MCP session with server terminated"). Uses stdlib `encoding/json` (internal `json` wrapper lacks RawMessage/Decoder/Number — noted deviation, still zero external deps). P3-b adds `client.go` (Client 门面 = McpAsyncClient + LifecycleInitializer + solon McpClientProvider 的心跳重连合流): `TransportFactory` per (re)initialization builds a fresh transport+Session (stdio = respawn; collapses Java's two reconnect levels), `ensureInitialized` under `sync.Mutex` (join semantics) with `Session.Done()` self-heal, `Negotiate` check → close on failure, notifications/initialized; ops (`Ping`/`ListTools`/`ListAllTools` auto-pagination/`CallTool`/resources 族 incl. subscribe/`ListPrompts`/`GetPrompt`/`SetLoggingLevel`/`CompleteCompletion`) wrap ctx with requestTimeout 20s unless caller deadline; capability checks mirror Java IllegalStateException; reverse handlers: ping → `{}`, roots/list when capability, sampling/elicitation when capability (nil-handler panics at NewClient); change-notification handlers auto re-list then fan to consumers (`Add*Consumer`/`With*Consumer`); heartbeat goroutine (Java idles when unstarted — Go deviation: every ping re-initializes, so heartbeat drives reconnect): fail → `Reset()` + delay doubling to 10min, success → reset; `Reset()`/`Close()` exported; `DialStdio` convenience (lazy spawn, errors surface on first op). `transport_stdio.go` (`StdioTransport`): newline-framed JSON on stdin/stdout (16MB line cap), in-frame newlines escaped to `\n`, stderr per-line hook (default log), buffered recv chan 64, `Connect` spawns `exec.Command` (+env overlay), parse error terminates transport, `Close` = stdin close → 3s wait → Kill, `TransportFactory()` respawns. `session.go` gains `TransportError`/`IsTransportError` (send failure ∪ ErrSessionClosed ∪ DeadlineExceeded) + `Session.Done()`. P3-c 落地服务端与双 HTTP 传输：`server.go` `Server` 门面——工具/资源/提示三注册表（`ToolSpec`/`ResourceSpec`/`PromptSpec` 链式元数据；同名工具替换=告警；严格工具名 panic 可关；uri 含 `{` 即 RFC 6570 模板注册）、能力协商（请求版本命中即回显、否则回落最新+告警）、变更广播按 listChanged 能力门禁（`Notify*ListChanged`）、资源订阅路由（`NotifyResourceUpdated`）、日志级别过滤（`NotifyLogging`）、`CallCtx` 反向 `Sample`/`Elicit`（能力门禁）与 `NotifyProgress`（无 progressToken 静默跳过）+ `RegisterTools` 反射批量注册（`*T` 导出方法形态二选一——map 参数或结构体参数 JSON 注入；返回 any JSON 文本化）；处理器 err → isError CallToolResult（协议层成功，Java McpResultResponder 语义）。`server_session.go` 服务端会话：反向请求 pending 表（int64 id）+ 出站双出口（请求期走请求绑定流、之外走监听流；无出口 = "Stream unavailable"）+ 三传输共用分发核心（handler panic → INTERNAL_ERROR）；`RunStdio` 启动即预建会话、行帧分发（请求 goroutine 异步、通知/响应同步）、stdout 归协议通道独占（日志切 stderr）、stdin EOF/ctx 结束清场。`server_http.go` streamable `Handler()` 单端点：POST initialize → JSON 响应+`Mcp-Session-Id` 头、其余请求 → SSE 回写流响应后收流、通知/响应帧 → 202、畸形/缺会话头/缺双类型 Accept → 400、未知会话 → 404、方法不符 → 405+Allow、closing → 503、安全钩子拒绝 → 403；GET 监听流常驻（Last-Event-ID 无回放存储=空流即收；keepalive ticker 发 ping 反向请求；**流断开即整会话终结**——close+removeSession，客户端自愈重握手）；DELETE 注销断流；`Middleware(prefix)` aifei 挂载缝（前缀命中走 MCP，其余穿透）。`SSEHandler(ssePath, messagePath)` 旧式双端点：GET 建会话+endpoint 事件（报文端点带 sessionId 查询参数）、POST 202 即回（请求 goroutine 脱离 r.Context()，响应回 GET 流）、GET 断开注销。`transport_http.go` 客户端 streamable 传输（POST 收发——200 JSON 单帧投递/200 SSE 异步消费至收流/202 容忍/404=会话失效终结传输；GET 监听流收服务端主动帧、流断即传输终结；Close=DELETE 注销+终结）。`transport_sse.go` 客户端旧式 SSE 传输（GET 流收全部帧+endpoint 事件定位报文端点；Close 取消 GET 断连即注销——旧协议会话生命周期=GET 流生命周期，服务端以流断开注销）。`util.go` 工具名校验（1..128 位 `A-Za-z0-9_-.`）+ URI 模板匹配（RFC 6570 子集：`{var}` 单段/`{+var}` 跨段）。
- **`./plugins/nacos`** — Nacos integration plugin built on nacos-sdk-go/v2. Implements `aifei.Plugin` for service registration (ephemeral instances with SDK heartbeats), config center (watch DataID, push changes via callback), and discovery (`NewNamiUpstream` converts Nacos discovery into `nami.Upstream`). Auto-registers a `config.CloudLoader` via `init()` so `config.Init()` automatically fetches config from Nacos at L5 when `nacos.server_addr` + `nacos.data_id` are set. `BindProps(props)` method chains `ConfigChangeCallback` to auto-update the Props on runtime config changes from Nacos.
- **`./config`** — Layered configuration loading with generic `Store` (key-value map). Supports L1-L5 loading order: `app.yml` + `app-{env}.yml` → extension configs → env vars + CLI args → programmatic `LoadInto()` → cloud loaders (e.g., Nacos). Provides `Get`/`GetStr`/`GetBool`/`GetInt` accessors, `Sub(prefix)` for scoped sub-props, `Bind(v)` for YAML round-trip to user-defined structs, and functional options (`WithEnvPrefix`, `WithEnv`, `WithConfigDir`, `WithBaseFiles`). Thread-safe (`sync.RWMutex`) — safe for concurrent reads and dynamic updates from cloud config watchers. Does NOT define application-level config structs — each app defines its own.
- **`./plugins/storage`** — Unified file-storage abstraction (ported from ficus `ficus-starter-storage`) with local filesystem and S3-compatible backends (AWS S3/Minio/OSS/COS) via minio-go. `Client` interface (`Exists`/`TempURL`/`Get`/`Put`/`Delete`/`DeleteBatch`, bucket-scoped) + `Media` model (`io.Reader` + content type/size, stdlib `mime` inference). `Manager` routes by bucket name with a default; `Plugin` (`aifei.Plugin`) reads `storage.*` from `config.Props` (`storage.default` + `storage.buckets.<name>.{driver,endpoint,regionId,accessKey,secretKey,autoCreateBucket}`) and installs the package-level default so top-level `storage.Put/Get/...` and `storage.Use(bucket)` work. Driver inferred from `driver` (`local`/`s3`) or endpoint scheme.
- **`./plugins/swagger`** — Knife4j-vue3 OpenAPI docs plugin. Implements `aifei.Plugin` to serve the compiled knife4j-vue3 UI (`web/` is embedded via `//go:embed`) plus a generated `services.json` group config and the OpenAPI spec at a configurable base path (default `/swagger`). The UI is pure static frontend (no springboot) compiled with `VITE_RELEASE_APP_TYPE=Knife4jFront`; it requests `/services.json` from the server root (hardcoded), which points it to `{basePath}/swagger.json` served via `swag.ReadDoc()`. Provides `Handler() func(http.Handler) http.Handler` middleware that intercepts matching requests to serve raw HTML/JSON/CSS/JS outside the aifei `{code, msg, data}` envelope; users wire it via `server.WithHTTPHandler(swagPlugin.Handler())`. Configured via `swagger.*` in the global config (`enabled`, `basePath`, `groupName`). Users run `swag init` to generate docs from Go comments, import the generated `docs` package (which registers the spec), and add `swagger.NewPlugin(nil)` to the app. Dependencies: `github.com/swaggo/swag`.
- **`./plugins/cache`** — Two-level (local + Redis) cache abstraction built on jetcache-go (inspired by ficus `CacheService`). `Cache` interface (`Get` returning a `found bool` distinct from miss, `Set`/`Delete`/`Exists`, and `GetOrStore` doing singleflight + cache-penetration protection) wraps jetcache-go, exposing FreeCache/TinyLFU L1 and go-redis L2; per-instance key prefixing isolates instances sharing one Redis. `Manager` routes by instance name with a default; `Plugin` (`aifei.Plugin`) reads `cache.*` from `config.Props` (`cache.default` + `cache.instances.<name>.{type,ttl,codec,keyPrefix,local,remote,refresh,syncLocal}`) and installs the package-level default so top-level `cache.Get/Set/Delete/Exists/GetOrStore` and `cache.Use(instance)` work. `Stop()` closes every instance (unlike storage, caches may run refresh goroutines). Type inferred from `type` (`local`/`remote`/`both`) or which of `local`/`remote` is configured; L1 driver `freecache`/`tinylfu`, L2 redis `addr` (single node) or `addrs` (ring). Advanced jetcache features (SetNX/Refresh/SyncLocal/...) are reachable via `Cache.JetCache()`.
- **`./plugins/kafka`** — Kafka producer/consumer abstraction built on franz-go (`twmb/franz-go`). `Client` interface (per-cluster) exposes `ProduceSync` (sync ack)/`Produce` (async w/ `Promise`)/`Flush`/`Subscribe` over `Message`/`Header` records; each `Subscribe` spawns a dedicated consumer client running an at-least-once poll loop — `AutoCommitMarks` is enabled so records are only committed once their handler returns nil (failed records are not committed and are redelivered on the next rebalance/restart); `Subscription.Close` does a final `CommitMarkedOffsets`. `Manager` routes by cluster name with a default; `Plugin` (`aifei.Plugin`) reads `kafka.*` from `config.Props` (`kafka.default` + `kafka.clusters.<name>.{brokers,clientId,sasl.{mechanism,user,password},tls.{enabled,caFile,certFile,keyFile,insecureSkipVerify},producer.{acks,compression,lingerMs,maxAttempts},consumer.{groupId,offsetReset,balancer,autoCommit.{enable,intervalMs}}}`) and installs the package-level default so top-level `kafka.ProduceSync/Produce/Flush/Subscribe` and `kafka.Use(cluster)` work. `Stop()` stops every running subscription (committing marked offsets) and closes every producer client. Defaults: acks=all, compression=snappy, offsetReset=latest, balancer=cooperativeSticky, autoCommit enable=true/5s. SASL plain/scram-sha-256/512 and TLS (incl. mTLS) supported; all built from the root franz-go module. Advanced needs (transactions, manual commits, seek, admin via `kadm`) are reachable via `Client.KgoClient()`/`Subscription.KgoClient()`. Integration tested against the in-memory `kfake` broker in `_test/kafka_test`.
- **`./plugins/dataisolate`** — Data isolation (tenant + row + column) for `db`, ported from the `docs/data-isolate.md` design. Three orthogonal policies share one mechanism (Principal from context → AST rewrite → rebuilt `?`-SQL with realigned args): `TenantPolicy` (shared-table strategy ③ injects `WHERE <alias>.tenant_id=?` and stamps INSERT rows; strategies ①/② database/schema route by `db.Config` with zero SQL rewrite via `Use(ctx)`), `DataScopePolicy` (row scope — self/dept/dept-tree/region/custom — from an app-supplied `ScopeRuleProvider`), and `FieldMaskPolicy` (column mask — NULL/constant/remove — on the outermost SELECT projection, from an app-supplied `FieldRuleProvider`). SQL parsing uses `github.com/ajitpratap0/GoSQLX`: it parses only PostgreSQL-style `$N` placeholders (the MySQL/SQLite `?` aifei renders is rejected), so the rewriter converts `?`→`$N` before parsing and `$N`→`?` after rendering, keeping placeholders and args aligned; PostgreSQL syntax (`::`, `RETURNING`, JSON operators) parses natively and is isolated (only genuinely malformed statements fail-closed via `Dao.Fail`). `Plugin` (`aifei.Plugin`) reads `dataisolate.*` from `config.Props` (`policies`, `enforce`, `allow_bypass`, `on_failure`, `configs`, `tenant.{strategy,column,scope.{mode,ignore_tables,tables},tenants}`, `scope.merge`, `field.default_mask`) and installs a `DbHookKit` (all 6 db hooks, composed with any pre-existing hook) on the listed `db.Config`s. `Middleware()` (`aifei.Handler`) resolves the `Principal` (built-in `SubdomainHeaderResolver` for tenant id; apps plug in JWT/session resolvers for the full principal) and writes it into the request context. `Bypass(ctx)`/`As(ctx, p)` escape hatches act on one call; identity columns are registered via `RegisterTableMeta` or inferred by convention. Application code stays isolation-unaware: just use `db.WithCtx(in.Context())` / `dataisolate.Use(in.Context())` (strategy ①/②) / `dataisolate.Sql(...)` (db.Sql directive path). Requires 5 backward-compatible additions to `db` (`Dao.Context()`, `Dao.SqlAndArgs()`, `Dao.Fail` veto, `db.Batch` triggering hooks, and INSERT field recompute after the stamp hook). Integration tested in `_test/dataisolate_test` against in-memory sqlite. GoSQLX requires Go ≥ 1.26.1 (workspace is on Go 1.27).

### Examples

- **`./_test/demo`** — Full web app demo using core + db + generator with SQLite driver. Generated in `Qualified` mode: `flow` package holds two tables (`sys_flow_task` + `sys_flow_log`, files `base_flow_task.go` etc.), and its service composes models from the `user` package (cross-package downward call, preserved across regenerations).
- **`./_test/db_test`** — Database integration tests (971 lines, ~80 test cases).

## Design Decisions (Java → Go)

| Java | Go |
|------|-----|
| Input + Output interfaces | `Input` / `Output` interfaces (preserved) |
| CGLIB/Javassist AOP proxy | `Handler` wrapper chain + `Interceptor` for method-level AOP |
| `@Path` annotation + reflection scanning | Code registration / `Register()` struct reflection |
| Undertow HTTP server | `net/http` via `http` adapter + `server` bootstrap |
| Service 互注（Java 包无环限制） | 包 = 领域（generator `Qualified` 多表一包）+ 跨包单向 + 窄接口/dami 事件/合并包三阀门 |
| Functional options for config | Same pattern preserved |

## Project State

The framework is functionally complete (~8,700 lines of library code + ~2,100 lines of tests across 78 Go files). Design docs are in `docs/` with detailed specs per phase (`00-overview.md` through `06-phase6-example.md`).

## Naming Conventions

- API methods follow Java Aifei naming: `GetStr`, `GetInt`, `GetBean`, `FindByID`, etc.
- `server.Register()` (in `register.go`) maps a service struct's exported methods to routes via two rules in `resolveRoute()`:
  - **Default actions (exact match)**, registered at the service prefix directly: `Paginate` → GET `/prefix`, `Create` → POST `/prefix`, `List` → GET `/prefix/list`.
  - **Verb prefixes**: method name must start with (and be longer than) one of `Get`/`Post`/`Put`/`Delete`/`Update`. The verb picks the HTTP method (`Get`→GET, `Post`→POST, `Put`→PUT, `Update`→PUT, `Delete`→DELETE); the remainder becomes the path suffix via camelCase→kebab-case (e.g. `GetProfile` → GET `/prefix/profile`, `PostApprove` → POST `/prefix/approve`).
  - Methods matching **neither** rule are skipped (not routed) — safe for private helpers. Note: there is NO prefix rule for `Save`/`Remove`/`Find`; `Create` is an exact-match default action only.
- `ById` in a method name becomes `:id`: `ById` → kebab `by-id` → path param `:id`, so `GetById` = GET `/prefix/:id`.
