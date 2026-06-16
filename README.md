# Aifei-Go

轻量级 Go Web 框架，从 [Aifei Java](https://github.com/jfinal/aifei) 移植。遵循"Just Service"理念——扁平架构，无 Controller/Service/DAO 分层。

## 特性

- **零外部依赖** — 所有库模块仅使用 Go 标准库
- **模块化设计** — enjoy/db/json/log 可独立引入
- **Enjoy 模板引擎** — 自研模板语言，支持表达式、条件、循环、宏定义
- **Active Record ORM** — Row + Dao 链式操作，变更追踪
- **代码生成器** — 从数据库 Schema 自动生成类型安全的 CRUD 代码
- **Enjoy SQL** — 模板 SQL 引擎，支持动态 WHERE/ORDER BY/参数占位（18种操作符）
- **基数树路由** — 高性能路由匹配，支持参数和通配符
- **Handler 包装链 + 拦截器** — Logger/Recover/CORS/Auth 等内置 Handler，方法级 AOP 拦截器

## 模块结构

| 模块 | 说明 | 依赖 |
|------|------|------|
| `aifei-go` | 核心框架（Input/Output 接口、Router、Handler wrapper、Interceptor） | 无 |
| `aifei-go/enjoy` | Enjoy 模板引擎 | 无 |
| `aifei-go/db` | 数据库访问（Row/Dao/Dialect/Enjoy SQL） | 无 |
| `aifei-go/json` | JSON 工具 | 无 |
| `aifei-go/log` | 日志接口 | 无 |
| `aifei-go/generator` | 代码生成器（Schema → 类型安全代码） | db, enjoy |
| `aifei-go/go-http` | net/http 适配器 | aifei |
| `aifei-go/server` | 服务启动、内置 Handler 包装器、响应构建器 | aifei, go-http |

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
        return server.OkMsg("Hello, Aifei!")
    })

    app.GET("/hello/:name", func(in aifei.Input) aifei.Output {
        return server.OkMsg("Hello, " + in.Param("name"))
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
    page, _ := db.SQL("SELECT * FROM user ORDER BY id DESC").Paginate(1, 10)

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
    "github.com/crazy-airhead/aifei-go/generator"
)

gen := generator.New(pool, dialect, "./myapp/db", "myapp/db")
gen.Generate() // 生成 user/、order/ 等包，每个包含 base.go + dao.go + service.go

// 使用生成的类型安全 API
u, _ := user.FindById(123)
u.SetName("new name").Update()
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

app.Register("/api/user", &UserService{})
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
| enjoy | ~2,500 | 182 | 16 |
| db + db/sql | ~2,670 | 53 + 228 | 20 |
| generator | ~1,250 | 314 | 13 |
| aifei | ~780 | — | 8 |
| server | ~740 | — | 6 |
| go-http | ~330 | — | 3 |
| log | ~110 | 114 | 1 |
| json | ~40 | 45 | 1 |
| _example | ~220 | 1,120 | 6 |
| **总计** | **~8,350** | **~2,060** | **74** |

## 协议

[Apache-2.0](LICENSE)
