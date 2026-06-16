# Aifei-Go

轻量级 Go Web 框架，从 [Aifei Java](https://github.com/jfinal/aifei) 移植。遵循"Just Service"理念——扁平架构，无 Controller/Service/DAO 分层。

## 特性

- **零外部依赖** — 所有库模块仅使用 Go 标准库
- **模块化设计** — enjoy/db/json/log 可独立引入
- **Enjoy 模板引擎** — 自研模板语言，支持表达式、条件、循环、宏定义
- **Active Record ORM** — Row + Dao 链式操作，变更追踪
- **基数树路由** — 高性能路由匹配，支持参数和通配符
- **中间件链** — Logger/Recover/CORS/Auth 等内置中间件
- **SQL Builder** — 链式 SQL 构建器，分页支持

## 模块结构

| 模块 | 说明 | 依赖 |
|------|------|------|
| `aifei-go` | 核心 Web 框架 | 无 |
| `aifei-go/enjoy` | Enjoy 模板引擎 | 无 |
| `aifei-go/db` | 数据库访问（Row/Dao） | 无 |
| `aifei-go/json` | JSON 工具 | 无 |
| `aifei-go/log` | 日志接口 | 无 |

## 快速开始

```bash
go get github.com/crazy-airhead/aifei-go
```

```go
package main

import "github.com/crazy-airhead/aifei-go"

func main() {
    app := aifei.New()

    app.Use(aifei.Logger(), aifei.Recover(), aifei.CORS("*"))

    app.GET("/", func(c *aifei.Context) {
        c.Text("Hello, Aifei!")
    })

    app.Run(":8080")
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

    // Active Record
    row := db.NewRow("user").Set("name", "james").Set("age", 18)
    result, _ := db.Insert(row)

    // 查询
    found, _ := db.FindByID("user", result.GetID())

    // SQL Builder
    page, _ := db.NewSQL("SELECT * FROM user").Where("age > ?", 20).OrderBy("id DESC").Paginate(1, 10)
}
```

### Enjoy 模板

```go
import "github.com/crazy-airhead/aifei-go/enjoy"

engine := enjoy.NewEngine("myEngine")
tpl := engine.GetTemplateByString("Hello, #(name)! Age: #(age)")
output := tpl.Render(map[string]interface{}{"name": "james", "age": 18})
```

## Just Service

通过 `Register()` 自动映射 struct 方法为路由：

```go
type UserService struct{}

func (s *UserService) List(c *aifei.Context)   { /* GET /api/user/list */ }
func (s *UserService) Save(c *aifei.Context)   { /* POST /api/user/save */ }
func (s *UserService) GetById(c *aifei.Context) { /* GET /api/user/:id */ }

app.Register("/api/user", &UserService{})
```

命名规则：`List*`/`Get*` → GET，`Save*`/`Create*` → POST，`Update*` → PUT，`Delete*` → DELETE。`ById` 后缀变为 `/:id` 路径参数。

## 代码统计

| 包 | 代码行数 | 测试行数 | 文件数 |
|---|---|---|---|
| enjoy | 2,455 | 150 | 17 |
| db | 1,830 | 53 | 12 |
| aifei | 1,146 | — | 8 |
| log | 111 | 114 | 2 |
| json | 37 | 45 | 2 |
| _example | 145 | 295 | 2 |
| **总计** | **5,579** | **657** | **43** |

## 协议

[Apache-2.0](LICENSE)
