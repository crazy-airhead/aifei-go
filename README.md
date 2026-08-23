# Aifei-Go

轻量级 Go Web 框架，从 [Aifei](https://github.com/jfinal/aifei)（Java 版）移植。遵循"Just Service"理念——扁平架构，无 Controller/Service/DAO 分层。

> 📖 在线文档：<https://crazy-airhead.github.io/aifei-go/>

## 特性

- **Just Service** — 方法名即路由：`Register()` 按命名约定（动词前缀 + 默认动作）自动映射 struct 方法为 RESTful 端点
- **零外部依赖（核心）** — 核心库与独立框架（aifei/enjoy/db/json/log/nami/dami）仅用 Go 标准库；config 仅依赖 yaml.v3；插件（plugins/）按需引入第三方库
- **模块化设计** — 各模块可独立 `go get`，按需组合，不拉入多余依赖
- **Enjoy 模板引擎** — 自研模板语言（~2800 行），支持表达式、条件、循环、宏定义、空安全
- **Active Record ORM** — Row + Dao 链式操作，变更追踪
- **代码生成器** — 从数据库 Schema 自动生成类型安全的 CRUD 代码（每表一个独立包）
- **Enjoy SQL** — 模板 SQL 引擎，`#where`/`#and`/`#orderBy` + 18 种操作符，条件为空自动省略
- **基数树路由** — 高性能路由匹配，支持参数和通配符
- **Handler 包装链 + 拦截器** — Logger/Recover/CORS/Auth 等内置 Handler，方法级 AOP 拦截器
- **分层配置** — `app.yml` + 环境变量 + 命令行参数 + 云配置分层加载，支持运行时热更新
- **两级缓存** — 本地（FreeCache/TinyLFU）+ Redis 两级缓存，`GetOrStore` 自带 singleflight 与缓存穿透防护
- **文件存储** — 统一本地文件系统与 S3 兼容后端（AWS S3 / Minio / OSS / COS），按 bucket 路由
- **Kafka** — 基于 franz-go 的生产/消费，多集群，`Subscribe` 至少一次投递
- **Nacos 集成** — 服务注册与发现、配置中心，自动桥接到 nami RPC 客户端
- **Swagger 文档** — 内嵌 knife4j-vue3 UI 的 OpenAPI 文档插件
- **Nami RPC 客户端** — 轻量 HTTP RPC 客户端框架，Filter 链 + 服务发现
- **Dami 事件总线** — 进程内事件总线（send/call/stream/lpc），发布订阅 + 同步调用

## 模块结构

模块按角色分层：**核心框架**（aifei 本体）、**核心库**（零外部依赖原语，可独立使用）、**默认运行时**（aifei 的 net/http 适配 + 生产引导）、**独立框架**（不依赖 aifei 的兄弟框架）、**代码生成**（`tools/`）、**插件**（按需引入第三方库的集成）、**示例**（`_test/`，不发布）。

| 层 | 模块 | 说明 | 依赖 |
|----|------|------|------|
| 核心框架 | `aifei-go` | Input/Output 接口、Router、Handler wrapper、Interceptor | — |
| 核心库 | `aifei-go/enjoy` | 模板/SQL 引擎 | — |
| 核心库 | `aifei-go/db` | 数据库访问（Row/Dao/Dialect/Enjoy SQL） | — |
| 核心库 | `aifei-go/json` | JSON 工具 | — |
| 核心库 | `aifei-go/log` | 日志接口 | — |
| 核心库 | `aifei-go/config` | 分层配置（yml + 环境变量 + 命令行 + 云配置） | yaml.v3 |
| 默认运行时 | `aifei-go/http` | net/http 适配器 | aifei |
| 默认运行时 | `aifei-go/server` | 启动引导、内置 Handler、响应构建器、Register | aifei, http, db, enjoy, log |
| 独立框架 | `aifei-go/nami` | HTTP RPC 客户端框架（channel/coder/Filter/Discovery） | — |
| 独立框架 | `aifei-go/dami` | 进程内事件总线（send/call/stream/lpc） | — |
| 代码生成 | `aifei-go/tools/generator` | Schema → 类型安全 CRUD 代码 | db, enjoy + sqlite |
| 代码生成 | `aifei-go/tools/damigen` | dami 相关代码生成 | enjoy |
| 插件 | `aifei-go/plugins/cache` | 两级缓存（本地 + Redis） | aifei, config, log + jetcache-go, go-redis |
| 插件 | `aifei-go/plugins/dami` | dami 事件总线接入 aifei | aifei, dami, log |
| 插件 | `aifei-go/plugins/kafka` | Kafka 生产/消费（franz-go） | aifei, config, log + franz-go |
| 插件 | `aifei-go/plugins/nacos` | 服务注册、配置中心、发现 | aifei, nami, log + nacos-sdk-go |
| 插件 | `aifei-go/plugins/storage` | 文件存储（本地 + S3 兼容） | aifei, config, log + minio-go |
| 插件 | `aifei-go/plugins/swagger` | OpenAPI 文档（knife4j-vue3） | aifei, config, log + swaggo/swag |
| 示例 | `_test/demo` | 完整 Web 应用 Demo | core + db + generator + sqlite |
| 示例 | `_test/db_test` | 数据库集成测试 | db + sqlite |
| 示例 | `_test/cache_test` | 缓存集成测试 | miniredis |
| 示例 | `_test/kafka_test` | Kafka 集成测试 | franz-go/kfake |
| 示例 | `_test/enjoy_test` | Enjoy 引擎测试 | enjoy |

Requires Go 1.26.

## 快速开始

```bash
go get github.com/crazy-airhead/aifei-go
```

```go
package main

import (
    "github.com/crazy-airhead/aifei-go"
    "github.com/crazy-airhead/aifei-go/server"
)

func main() {
    app := aifei.New()

    // 全局 Handler 包装链
    app.Use(server.Logger(), server.Recover())

    // 路由 — HandlerFunc: func(in aifei.Input) aifei.Output
    app.GET("/", func(in aifei.Input) aifei.Output {
        return server.Ok("Hello, Aifei!")
    })

    app.GET("/hello/:name", func(in aifei.Input) aifei.Output {
        return server.Ok("Hello, " + in.Param("name"))
    })

    // 启动（支持 CORS、BasicAuth 等 HTTP 级包装器）
    server.Run(app, ":8080", server.WithCORS("*"))
}
```

### 数据库

`db` 模块不含驱动，用户自行引入所需驱动：

```go
import (
    "github.com/crazy-airhead/aifei-go/db"
    _ "modernc.org/sqlite" // 或 _ "github.com/go-sql-driver/mysql" 等
)

func main() {
    db.Init("sqlite", "./app.db")

    // Active Record — 插入
    row := db.NewRow("user").Set("name", "james").Set("age", 18)
    result, _ := db.Insert(row)

    // 主键查询
    found, _ := db.FindByID("user", result.GetID())

    // 分页查询
    page, _ := db.RawSql("SELECT * FROM user ORDER BY id DESC").Paginate(1, 10)

    // Enjoy SQL 模板 — 动态条件
    data := map[string]interface{}{"minAge": 18, "status": 1}
    list, _ := db.Sql(
        "SELECT * FROM user #where() #and(age > #para(minAge)) #and(status = #para(status))",
        data,
    ).Find()
}
```

### Enjoy 模板

```go
import "github.com/crazy-airhead/aifei-go/enjoy"

engine := enjoy.NewEngine("myEngine")
tpl := engine.GetTemplateByString("Hello, #(name)! Age: #(age)")
output := tpl.RenderToString(map[string]interface{}{"name": "james", "age": 18})
```

### 代码生成器

从数据库 Schema 自动生成类型安全的代码（每表一个包）：

```go
import (
    "github.com/crazy-airhead/aifei-go/tools/generator"
)

gen := generator.New(pool, dialect, "./myapp/db", "myapp/db")
gen.Generate() // 一次扫描所有表：user/、loginlog/ …，每张表生成独立包（base/model/dao/service）

// 使用生成的类型安全 API
u, _ := user.FindById(123)
u.SetName("new name").Update()
```

### 文件存储

`storage` 插件统一本地文件系统与 S3 兼容后端（AWS S3 / Minio / OSS / COS），按 bucket 路由；默认 bucket 由顶层 `storage.Put/Get/...` 操作：

```go
import (
    "os"
    "strings"

    "github.com/crazy-airhead/aifei-go/aifei"
    "github.com/crazy-airhead/aifei-go/config"
    "github.com/crazy-airhead/aifei-go/server"
    "github.com/crazy-airhead/aifei-go/plugins/storage"
)

func main() {
    config.Init(os.Args)                 // 读 app.yml，填充全局配置
    p, _ := storage.NewPlugin(nil)       // 自动读 config 的 storage.*，装配多 bucket（nil 用默认日志）
    app := aifei.New(aifei.WithPlugin(p))
    server.Run(app, ":8080")

    // 顶层便捷函数操作默认 bucket
    media := storage.NewMedia(strings.NewReader("hello"), "text/plain")
    storage.Put("a/b.txt", media)
    got, _ := storage.Get("a/b.txt") // *storage.Media

    // 指定 bucket
    storage.Use("avatars").Put("u1.png", media)
}
```

对应 `app.yml` 配置（驱动由 `driver` 字段或 endpoint 协议推断）：

```yaml
storage:
  default: local
  buckets:
    local:
      driver: local
      endpoint: /var/data
    s3:
      driver: s3
      endpoint: https://s3.example.com
      regionId: us-east-1
      accessKey: AK...
      secretKey: SK...
      autoCreateBucket: true
```

## Just Service

通过 `Register()` 自动映射 struct 方法为路由，方法签名遵循 `func(in aifei.Input) aifei.Output`：

```go
type UserService struct{}

func (s *UserService) List(in aifei.Input) aifei.Output      { /* GET /api/user/list */ }
func (s *UserService) Paginate(in aifei.Input) aifei.Output   { /* GET /api/user */ }
func (s *UserService) Create(in aifei.Input) aifei.Output     { /* POST /api/user */ }
func (s *UserService) GetById(in aifei.Input) aifei.Output    { /* GET /api/user/:id */ }
func (s *UserService) UpdateById(in aifei.Input) aifei.Output { /* PUT /api/user/:id */ }
func (s *UserService) DeleteById(in aifei.Input) aifei.Output { /* DELETE /api/user/:id */ }

server.Register(app.Router(), "/api/user", &UserService{})
```

### 路由映射规则

`Register()` 通过方法名约定自动推断 HTTP 方法和 URL 路径：

**精确匹配**（默认 RESTful 端点，直接映射到 service prefix）：

| 方法名 | HTTP 方法 | URL |
|--------|----------|-----|
| `List` | GET | `/prefix/list` |
| `Paginate` | GET | `/prefix` |
| `Create` | POST | `/prefix` |

**前缀匹配**（前缀剥离后，剩余部分转为 kebab-case 路径）：

| 方法名前缀 | HTTP 方法 | 示例 |
|-----------|----------|------|
| `Get*` | GET | `GetById` → `GET /prefix/:id` |
| `Post*` | POST | `PostStatus` → `POST /prefix/status` |
| `Put*` | PUT | `PutConfig` → `PUT /prefix/config` |
| `Delete*` | DELETE | `DeleteItems` → `DELETE /prefix/items` |
| `Update*` | PUT | `UpdateById` → `PUT /prefix/:id` |

**特殊转换**：
- `ById` 后缀自动转为 `/:id` 路径参数
- 未匹配任何规则的方法不会被注册为路由

### 自定义 ServicePrefix

代码生成器默认使用 struct 名的驼峰形式作为 URL 路径（如 `loginLog` → `/api/v1/loginLog`）。如需复数形式，直接修改生成的 `service.go` 中的 `ServicePrefix` 常量：

```go
const ServicePrefix = "/api/v1/loginLogs"   // 手动改为复数
```

## 代码统计

| 包 | 代码行数 | 测试行数 | 文件数 |
|---|---|---|---|
| enjoy | ~2,800 | — | 16 |
| db + db/sql | ~3,800 | 281 | 24 |
| server | ~1,540 | 334 | 13 |
| nami | ~1,700 | ~1,600 | 22 |
| generator | ~1,290 | 371 | 14 |
| config | ~1,060 | ~2,020 | 5 |
| _test | ~1,020 | ~2,020 | 16 |
| kafka | ~970 | 378 | 13 |
| storage | ~870 | 432 | 13 |
| cache | ~780 | 384 | 15 |
| nacos | ~650 | 95 | 7 |
| aifei | ~620 | — | 8 |
| swagger | ~510 | 341 | 5 |
| http | ~430 | — | 3 |
| log | ~110 | 114 | 2 |
| json | ~40 | 45 | 2 |
| **总计** | **~18,200** | **~8,400** | **178** |

## 协议

[Apache-2.0](LICENSE)
