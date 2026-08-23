# Aifei-Go：为 AI Coding 而生的 Go 服务端框架

> Aifei-Go 是 [Aifei](https://github.com/jfinal/aifei)（Java 版）的 Go 语言移植版。它继承了 Aifei 的核心设计理念 —— **Just Service** 扁平架构、**HIO** 自主 IO 模型、**Enjoy** 自研模板引擎、**Db + Row** 数据库模式，并在 Go 的语言特性与生态上重新落地：用接口与 Handler 包装链替代动态代理，用 `net/http` 替代 Undertow，用 Radix 树路由替代注解扫描，同时又参考了 Solon，引入 Dami 提供进程内事件总线，引入 Nami 作为 HTTP RPC **客户端**框架，用于支撑微服务。
> 
> **注意事项：Aifei-Go 由 GLM5.2 生成，不喜勿入**。Aifei 是基准，AI 翻译。我做决策，AI 生成。
>
> Java 版文档：<https://aifei.cn/doc>

---

## 1. 什么是 Aifei-Go

Aifei 原本是"一款用于 AI Coding 的 Java 服务端框架"，其核心设计目标是 **极小化 Token 消耗、极大化 Attention 浓度，让 AI 稳定生成高品质代码**。要做到这一点，最有效的手段就是消除冗余分层与样板代码 —— 这也是 Aifei 开创 **Just Service 范式** 的根本动因。

近十年间，前后端分离成为主流，页面路由、交互编排与渲染职责大量转移到前端（react-router、vue-router 已接管了过去由服务端 Controller 承担的职责）。路由既然在前端，服务端就不再需要 Controller 这一层。Just Service 范式之下，开发者无需编写 Controller、Render、Repository、Mapper 这类冗余代码，直接写业务即可。

Aifei-Go 把这套理念原汁原味地带到了 Go 语言：

- **只写 Service，不写 Controller** —— 方法名即路由，按命名约定自动映射为 RESTful 端点。
- **核心零外部依赖** —— 核心库与独立框架（aifei / enjoy / db / json / log / nami / dami）仅用 Go 标准库；插件按需引入第三方库。
- **模块化、按需组合** —— 每个模块可独立 `go get`，不拉入多余依赖。
- **AI 友好** —— 代码量少、结构扁平、约定明确，AI 生成时上下文负担小、命中率高。

### 1.1 为什么从 Java 移植到 Go

Aifei Java 版内核仅 3333 行（内核核心仅 260 行），零第三方依赖，已经是极致精简。移植到 Go 带来三点额外收益：

| 维度       | Java Aifei           | Go Aifei-Go                |
| -------- | -------------------- | -------------------------- |
| 部署形态     | JVM + 打包             | 单一静态二进制，毫秒级启动              |
| 并发模型     | 线程池 + 同步阻塞           | goroutine 天然并发             |
| 依赖管理     | Maven 多模块            | Go workspace 多模块，按需 import |
| 动态能力     | CGLIB/Javassist 动态代理 | 接口 + Handler 包装链（无运行时反射代理） |
| HTTP 服务器 | Undertow（去 Servlet）  | `net/http`（标准库，零依赖）        |
| 路由       | 注解 `@Path` + 包扫描     | Radix 树 + 代码注册（编译期确定）      |

Java Aifei 中被废弃的三个模块（`aifei-proxy`、`aifei-undertow`、`aifei-all`）在 Go 里不再需要：Go 没有动态代理机制，AOP 由 Handler 包装链 + Interceptor 接口实现；Go 有标准库 `net/http`；Go 的 import 机制天然按需引入。**保留下来的，是 Aifei 真正有价值的内核：HIO、Just Service、Enjoy、Db + Row。**

---

## 2. 核心理念：Just Service

Just Service 的本质是：**方法名即路由**。一个 Go struct 的导出方法，按命名约定自动映射成一条 HTTP 路由，无需任何注解、配置文件或注册代码（注册由生成器在 `init()` 中自动完成）。

```go
type UserService struct{}

func (s *UserService) List(in aifei.Input) aifei.Output      { /* GET    /api/user/list */ }
func (s *UserService) Paginate(in aifei.Input) aifei.Output  { /* GET    /api/user       */ }
func (s *UserService) Create(in aifei.Input) aifei.Output    { /* POST   /api/user       */ }
func (s *UserService) GetById(in aifei.Input) aifei.Output   { /* GET    /api/user/:id   */ }
func (s *UserService) UpdateById(in aifei.Input) aifei.Output{ /* PUT    /api/user/:id   */ }
func (s *UserService) DeleteById(in aifei.Input) aifei.Output{ /* DELETE /api/user/:id   */ }
```

`server.Register()` 通过两条规则把方法名翻译成 HTTP 方法 + URL：

1. **默认动作（精确匹配）** —— 直接挂在 service 前缀上：`Paginate` → `GET /prefix`，`Create` → `POST /prefix`，`List` → `GET /prefix/list`。
2. **动词前缀** —— 方法名以（且长于）`Get`/`Post`/`Put`/`Delete`/`Update` 开头，动词决定 HTTP 方法，剩余部分 camelCase→kebab-case 作为路径后缀。例如 `GetProfile` → `GET /prefix/profile`。

两个特殊约定：

- `ById` 后缀自动转为 `:id` 路径参数：`GetById` → `GET /prefix/:id`。
- 既非默认动作、也不符合动词前缀的方法**不会被路由** —— 天然成为私有 helper，无需额外的可见性控制。

> 对比 Java 版的 `@Path` 注解 + 包扫描：Go 没有注解，Aifei-Go 用「命名约定 + 代码注册」达成同样的效果，且路由在编译期就已确定，没有运行时反射扫描开销。

---

## 3. HIO 架构：Input / Output / Handler

Java Aifei 采用 **HIO 自主架构（Handler + Input + Output）**，让用户自主掌控处理流程与数据结构。Aifei-Go 完整保留了这一设计，并用 Go 接口重新表达：

```go
// 处理流程的单元：Input 进、Output 出
type HandlerFunc func(in Input) Output

// Input = Param（读参数）+ Meta（请求元信息）
type Input interface {
    Param  // Has / GetStr / GetInt / GetBean / GetMap / PathPara ...
    Meta   // Context / Header / Path / Body
}

// Output 由业务构建，IoHandler 决定如何渲染
type Output interface {
    Code() int
    Msg()  string
    Data() interface{}
}
```

几个关键点：

- **Input 与 HTTP 解耦**。`Input` 只承载「任何调用源都能满足」的契约——参数读取与请求元信息。HTTP 专属的概念（method 动词、remote 地址、cookie）不在这个接口上，它们留在 HTTP 适配层。这意味着同一个 Service 方法既可被 HTTP 请求驱动，也可被测试桩、内部调用驱动，业务代码不绑死 HTTP。

- **Output 是意图，不是渲染**。业务代码构建一个 `Out`（`server.Ok()` / `server.Of(data)` / `server.Fail(msg)`），它累积 code/msg/data 以及**渲染意图**（JSON、Enjoy HTML 视图、文件下载、原始字节、重定向）。`IoHandler` 读这些意图来决定 *如何* 写响应 —— 业务代码从不直接碰 `net/http`，文件下载、响应头都通过闭包/构建器表达。

- **两条包装链**。Aifei-Go 区分两类横切逻辑：**Handler 级**（`Input → Output`，作用于业务处理，如 `Logger`、`Recover`、`TxInterceptor`）与 **HTTP 级**（作用于 `http.Handler`，如 `CORS`、`BasicAuth`、`RequestID`）。前者对应 Java Aifei 的全局拦截器，后者处理纯传输层关切。

Java 用 CGLIB/Javassist 动态代理实现 AOP，Go 没有这套机制 —— Aifei-Go 用 **Handler 包装链 + Interceptor 接口** 替代：`ChainHandlers()` 把一组 `Handler` 包装器组合成一条链，`Interceptor` 提供方法级 AOP（`@Before` / `@Clear` 的等价物）。

---

## 4. 模块结构

Aifei-Go 是一个 **Go workspace 多模块**项目，按角色分层。每个模块独立版本化、可独立 `go get`，互不拉入多余依赖：

| 层    | 模块                         | 职责                                              | 依赖                          |
| ---- | -------------------------- | ----------------------------------------------- | --------------------------- |
| 核心框架 | `aifei-go/aifei`           | Input/Output、Router、Handler wrapper、Interceptor | —                           |
| 核心库  | `aifei-go/enjoy`           | 模板/SQL 引擎（自研）                                   | —                           |
| 核心库  | `aifei-go/db`              | 数据库访问（Row/Dao/Dialect/Enjoy SQL）                | enjoy                       |
| 核心库  | `aifei-go/json`            | JSON 工具                                         | —                           |
| 核心库  | `aifei-go/log`             | 日志接口                                            | —                           |
| 核心库  | `aifei-go/config`          | 分层配置（yml + 环境变量 + 命令行 + 云配置）                    | yaml.v3                     |
| 运行时  | `aifei-go/http`            | net/http 适配器                                    | aifei                       |
| 运行时  | `aifei-go/server`          | 启动引导、内置 Handler、`Out`、`Register`                | aifei, http, db, enjoy, log |
| 独立框架 | `aifei-go/nami`            | HTTP RPC **客户端**框架                              | —                           |
| 独立框架 | `aifei-go/dami`            | 进程内事件总线（send/call/stream/lpc）                   | —                           |
| 独立框架 | `aifei-go/flow`            | 流程编排引擎 + 工作流（claim/submit 人工任务）              | enjoy, dami, yaml.v3        |
| 代码生成 | `aifei-go/tools/generator` | Schema → 类型安全 CRUD 代码                           | db, enjoy                   |
| 代码生成 | `aifei-go/tools/damigen`   | dami 相关代码生成                                     | enjoy                       |
| 插件   | `plugins/cache`            | 两级缓存（本地 + Redis）                                | jetcache-go, go-redis       |
| 插件   | `plugins/kafka`            | Kafka 生产/消费                                     | franz-go                    |
| 插件   | `plugins/nacos`            | 服务注册、配置中心、发现                                    | nacos-sdk-go                |
| 插件   | `plugins/storage`          | 文件存储（本地 + S3 兼容）                                | minio-go                    |
| 插件   | `plugins/swagger`          | OpenAPI 文档（knife4j-vue3）                        | swaggo/swag                 |
| 插件   | `plugins/dataisolate`      | 租户 + 行/列数据隔离                                    | GoSQLX                      |
| 插件   | `plugins/elasticsearch`    | Elasticsearch 客户端封装                              | go-elasticsearch/v8         |
| 插件   | `plugins/xxljob`           | XXL-JOB 分布式任务调度执行器                             | go-basic/ipv4               |
| 插件   | `plugins/dami`             | dami 事件总线的生命周期封装                              | dami                        |
| 插件   | `plugins/flow`             | flow 引擎组装 + MySQL 状态仓储/任务历史                    | flow, db                    |

「核心库」层的模块（enjoy / db / json / log）刻意保持零外部依赖、可脱离框架独立使用——你完全可以只用 `enjoy` 做模板渲染，或只用 `db` 做数据库访问，而不引入整个 web 框架。插件层则把第三方库的集成集中隔离，不污染核心。

---

## 5. 快速开始

```bash
go get github.com/crazy-airhead/aifei-go/aifei
```

一个最小的 HTTP 服务：

```go
package main

import (
    "github.com/crazy-airhead/aifei-go/aifei"
    "github.com/crazy-airhead/aifei-go/server"
)

func main() {
    app := aifei.New()

    // 全局 Handler 包装链
    app.Use(server.Logger(), server.Recover())

    // HandlerFunc: func(in aifei.Input) aifei.Output
    app.GET("/", func(in aifei.Input) aifei.Output {
        return server.Of("Hello, Aifei!")
    })

    app.GET("/hello/:name", func(in aifei.Input) aifei.Output {
        return server.Ok("Hello, " + in.GetStr("name"))
    })

    // 启动（支持 CORS、BasicAuth 等 HTTP 级包装器）
    server.Run(app, ":8080", server.WithCORS("*"))
}
```

而一个真实的多 Service 应用，骨架甚至更短——所有表对应的 Service 通过生成器在各自的 `init()` 里自注册，主程序只需一行 `server.AutoRegisterServices(app)`：

```go
func main() {
    db.Init("sqlite", "./demo.db")
    // ...建表...

    app := aifei.New()
    app.Use(server.Logger(), server.Recover())

    // 每个 per-table 包在 init() 中注册自己的 Table 元数据与 Service 路由
    _ "github.com/crazy-airhead/aifei-go/_test/demo/internal/user"
    _ "github.com/crazy-airhead/aifei-go/_test/demo/internal/loginlog"

    server.AutoRegisterServices(app) // 一行注册全部 Service
    server.Run(app, ":8081", server.WithCORS("*"))
}
```

> 完整可运行示例见 `_test/demo`（`go run ./_test/demo`）。

---

## 6. 核心特性

### 6.1 Enjoy 模板引擎

Enjoy 是 Aifei 的招牌特性——一套自研的模板语言（~2800 行），自带词法分析器（DKFF 算法）、递归下降语法分析器（DLRD）和完整的表达式引擎。它不仅是页面渲染引擎，也是 **SQL 模板引擎**（见 6.3）。

```go
engine := enjoy.NewEngine("myEngine")
tpl := engine.GetTemplateByString("Hello, #(name)! Age: #(age)")
out := tpl.RenderToString(map[string]interface{}{"name": "james", "age": 18})
```

支持的语法：`#()` 表达式输出、`#if/#else/#elseif`、`#for`、`#set/#setLocal/#setGlobal`、`#define/#call`、`#include`、`#switch/#case/#default`、`#break/#continue/#return`；表达式层支持算术/比较/逻辑/三元、空安全（`??`、`?.`）、方法调用、map/数组字面量、静态访问（`::`）。

### 6.2 Db + Row + Dao

数据库访问沿用 Java Aifei 的 **Db + Row 模式**（与 JFinal 的 Db + Record 几乎一致），核心是链式 API 与 Active Record 变更追踪。`db` 模块本身不含驱动，用户自行引入所需驱动：

```go
import (
    "github.com/crazy-airhead/aifei-go/db"
    _ "modernc.org/sqlite" // 或 _ "github.com/go-sql-driver/mysql"
)

func main() {
    db.Init("sqlite", "./app.db")

    // Active Record —— 插入
    row := db.NewRow("user").Set("name", "james").Set("age", 18)
    result, _ := db.Insert(row)

    // 主键查询
    found, _ := db.FindByID("user", result.GetID())

    // 分页查询
    page, _ := db.RawSql("SELECT * FROM user ORDER BY id DESC").Paginate(1, 10)
    _ = found
    _ = page
}
```

内置 MySQL / PostgreSQL / SQLite 三种方言，支持事务、批量操作、类型转换。`Row.Set()` 追踪变更（用于 UPDATE），`Put()` 不追踪——精确控制更新范围。

### 6.3 Enjoy SQL：模板化的动态 SQL

`db.Sql(...)` 接受 Enjoy 模板作为 SQL，提供 `#where` / `#and` / `#orderBy` / `#para` 等指令，支持 18 种操作符，条件为空时自动省略——这是处理「动态查询条件」最优雅的方式，告别手写字符串拼接：

```go
data := map[string]interface{}{"minAge": 18, "status": 1}
list, _ := db.Sql(
    "SELECT * FROM user #where() #and(age > #para(minAge)) #and(status = #para(status))",
    data,
).Find()
```

`#orderBy` 指令接收一个字段白名单，实际排序字段从入参读取并校验——**防 SQL 注入**的同时支持多字段排序与字段名映射。

### 6.4 代码生成器

Aifei 没有 Model 概念——Model 的支持完全由生成器实现。`tools/generator` 扫描数据库 Schema，**每张表生成一个独立包**（`base.go` / `model.go` / `dao.go` / `service.go`），提供编译期类型安全的 CRUD API：

```go
gen := generator.New(pool, dialect, "./myapp/db", "myapp/db")
gen.Generate() // 一次扫描所有表：user/、loginlog/ …

// 使用生成的类型安全 API
u, _ := user.FindById(123)
u.SetName("new name").Update()
```

每表一包的策略让每张表的字段都有具名 getter/setter，AI 生成业务代码时不必猜测字段名拼法，命中率显著提升。

### 6.5 分层配置

`config` 模块提供分层加载（L1–L5）：`app.yml` + `app-{env}.yml` → 扩展配置 → 环境变量 + 命令行参数 → 编程式 `LoadInto()` → 云配置（如 Nacos）。线程安全，支持运行时热更新。提供 `Get/GetStr/GetBool/GetInt` 访问器、`Sub(prefix)` 作用域切片、`Bind(v)` YAML 往返到自定义结构体。所有插件统一从 `config.Props` 读取自身配置（`storage.*`、`cache.*`、`kafka.*` …），约定一致。

### 6.6 插件生态

插件实现 `aifei.Plugin`（`Start()/Stop()` 生命周期），从 `config.Props` 读自身配置并装配一个包级默认实例，让顶层便捷函数开箱即用：

- **`plugins/cache`** —— 两级缓存（本地 FreeCache/TinyLFU + Redis），`GetOrStore` 自带 singleflight 与缓存穿透防护，实例级 key 前缀隔离。
- **`plugins/storage`** —— 统一本地文件系统与 S3 兼容后端（AWS S3 / Minio / OSS / COS），按 bucket 路由。
- **`plugins/kafka`** —— 基于 franz-go 的生产/消费，多集群，`Subscribe` 至少一次投递（失败记录不提交、下次重投）。
- **`plugins/nacos`** —— 服务注册与发现、配置中心，自动桥接到 nami RPC 客户端；`init()` 自动注册云配置加载器。
- **`plugins/swagger`** —— 内嵌 knife4j-vue3 UI 的 OpenAPI 文档插件。
- **`plugins/dataisolate`** —— 租户 + 行 + 列三正交维度的数据隔离，AST 改写 SQL，应用代码零隔离感知（详见 [data-isolate.md](data-isolate.md)）。
- **`plugins/elasticsearch`** / **`plugins/xxljob`** —— Elasticsearch 客户端封装；XXL-JOB 执行器（自实现协议，仅依赖 net/http）。
- **`plugins/dami`** / **`plugins/flow`** —— 轻组装插件：dami 事件总线生命周期托管；flow 引擎组装 + 图加载 + MySQL 状态仓储与任务历史（详见 [flow.md](flow.md) / [flow-plugin.md](flow-plugin.md)）。

### 6.7 独立框架：Nami 与 Dami

两个不依赖 aifei 的兄弟框架，可独立使用：

- **`nami`** —— 轻量 HTTP RPC **客户端**框架（移植自 Java Solon Nami）。Channel 传输（`channel/http`）、编解码（`coder/json`）、Filter 链、`Upstream`/`Discovery` 服务发现、流式 `Builder`/`ClientFactory`。它是 aifei 服务端的对偶：aifei 暴露服务，nami 消费服务。
- **`dami`** —— 进程内事件总线（send/call/stream/lpc），发布订阅 + 同步调用 + 流式返回，用于解耦模块间通信。
- **`flow`** —— 流程编排引擎（移植自 Solon-Flow）。图模型（四类网关）+ 引擎求值 + 快照恢复；`flow/workflow` 子系统叠加 claim/submit 人工审批语义。表达式复用 enjoy，事件复用 dami（详见 [flow.md](flow.md)）。

---

## 7. Java Aifei → Go Aifei-Go 设计决策

移植并非逐行翻译，而是在保持 API 风格一致的前提下，用 Go 惯用模式替代 Java 机制：

| 决策点      | Java Aifei                         | Go Aifei-Go                     | 理由                 |
| -------- | ---------------------------------- | ------------------------------- | ------------------ |
| 请求上下文    | `Input` + `Output` 接口              | `Input` / `Output` 接口（保持分离）     | 与 Java API 一致，职责清晰 |
| AOP / 拦截 | CGLIB/Javassist 动态代理 + Interceptor | Handler 包装链 + Interceptor 接口    | Go 无运行时动态代理        |
| 路由注册     | `@Path` 注解 + 反射包扫描                 | 命名约定 + `Register()` 代码注册        | Go 无注解，编译期确定       |
| 泛型       | Java 泛型 `Handler<I, O>`            | Go 接口                           | 简化接口               |
| 配置       | `AifeiConfig` 接口 + 多个 `config()`   | Functional Options 模式           | Go 惯用配置模式          |
| HTTP 服务器 | Undertow（去 Servlet）                | `net/http`（http 适配 + server 启动） | 标准库，零依赖            |
| 包扫描      | ClassLoader + JAR 扫描               | 不需要（Go 静态编译）                    | 编译时确定所有代码          |
| 依赖注入     | `@Inject` + 反射                     | 构造函数注入                          | Go 惯例              |
| 路由匹配     | HashMap + ActionGroup              | Radix 树                         | 高性能路由标准做法          |
| 错误处理     | `throws Throwable`                 | `error` 返回值 + panic/recover     | Go 惯例              |
| 聚合模块     | `aifei-all`                        | import 按需引入                     | 不再需要               |

四条贯穿全局的重写原则：**保持 API 风格一致**、**Go 惯用优先**、**核心最小依赖**、**AI 友好**。

---

## 8. 继承与差异

**继承自 Java Aifei 的**（最有价值的设计）：Just Service 范式、HIO 自主架构、Enjoy 模板引擎、Db + Row 模式与链式 API、`sql(sql, para).find()` 式查询入口、生成器生成的 Model、集中式配置中心思想、拦截器（`@Before`/`@Clear` 的等价物）。

**Go 版新增/调整的**：

- **路由约定化** —— 用动词前缀 + 默认动作的命名约定替代 `@Path`，方法名直接表达 HTTP 语义。
- **代码生成深化** —— 每表一包 + typed Dao，编译期类型安全；Service 在 `init()` 中自注册，主程序零路由样板。
- **插件生态扩展** —— 在 Java 插件机制之上，新增 cache / kafka / storage / swagger / dataisolate 等开箱即用插件，全部配置驱动。
- **兄弟框架** —— nami（RPC 客户端）与 dami（事件总线）作为独立模块，与 aifei 服务端组合成完整微服务工具链。
- **数据隔离** —— 通过 `plugins/dataisolate` 提供声明式、AST 改写式的租户/行/列隔离，这是 Java 版未单独成插件的能力。

---

## 9. 小结

Aifei-Go 的定位很明确：**把 Aifei「为 AI Coding 而生」的设计哲学，用 Go 的方式重新实现一遍**。

它不追求大而全。核心框架零外部依赖，只依赖标准库；它免去了 Controller 这一层——Service 的一个 struct 方法就是一条路由，Dao 弱化为可选的薄层；它把模板引擎、SQL 模板、ORM、代码生成、配置、缓存、消息、存储、数据隔离做成正交的模块，按需取用。

如果你用过 JFinal 或 Java Aifei，你会对 Db + Row、Enjoy SQL 指令、集中式配置感到熟悉；如果你是 Go 开发者，你会发现它的接口设计、错误处理、并发模型都是地道的 Go 风格。而无论哪种背景，**Just Service 让你把注意力放在业务上** —— 这正是 Aifei 一开始就想做到的事。

### 延伸阅读

- Java 版官方文档：<https://aifei.cn/doc>
- Java 版仓库：<https://github.com/jfinal/aifei>
- 实施方案总览：[00-overview.md](../arch/00-overview.md)
- Java → Go 对照：[java-go-comparison.md](../arch/java-go-comparison.md)
- 数据隔离插件：[data-isolate.md](data-isolate.md)
- 多表关联映射：[multi-table-mapping.md](../arch/multi-table-mapping.md)
