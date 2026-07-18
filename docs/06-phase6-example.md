# Phase 6: 示例应用与集成

> 目标：创建完整的示例应用，验证框架各模块的集成

## 1. 示例项目结构

```
_test/
└── demo/
    ├── main.go                    # 应用入口
    ├── generated_test.go          # 生成代码集成测试
    └── internal/
        └── user/                  # Generator 生成的 user 表代码
            ├── base.go            # BaseUser + Table + getter/setter (覆盖写入)
            ├── user.go            # User struct (存在则跳过)
            ├── dao.go             # Dao + FindById/FindBy 等 (存在则跳过)
            └── service.go         # Service + HTTP 路由 (存在则跳过)
```

## 2. 完整示例应用（基于实际 demo）

```go
package main

import (
    "fmt"

    "github.com/crazy-airhead/aifei-go"
    "github.com/crazy-airhead/aifei-go/db"
    "github.com/crazy-airhead/aifei-go/server"

    // 生成的 per-table 包：通过 init() 注册 Table 元数据和 Service 路由
    _ "github.com/crazy-airhead/aifei-go/_test/demo/internal/user"

    _ "modernc.org/sqlite"
)

func main() {
    // 初始化数据库
    err := db.Init("sqlite", "./demo.db", db.WithPrinter(func(sql string, args ...interface{}) {
        fmt.Printf("[SQL] %s %v\n", sql, args)
    }))
    if err != nil {
        panic(err)
    }

    // 确保表存在
    db.SQL(`CREATE TABLE IF NOT EXISTS user (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        name TEXT NOT NULL,
        age INTEGER DEFAULT 0,
        email TEXT,
        created_at TEXT DEFAULT CURRENT_TIMESTAMP
    )`).Update()

    app := aifei.New()

    // 全局 Handler 包装链
    app.Use(server.Logger(), server.Recover())

    // 路由 — HandlerFunc: func(in aifei.Input) aifei.Output
    app.GET("/", func(in aifei.Input) aifei.Output {
        return server.OkMsg("Aifei Go " + aifei.Version)
    })

    // 自动注册所有 init() 注册的服务
    server.AutoRegisterServices(app)

    // 自定义路由组
    admin := app.Group("/api/admin")
    admin.GET("/dashboard", func(in aifei.Input) aifei.Output {
        return server.OkMsg("admin dashboard")
    })

    // 启动
    server.Run(app, ":8080", server.WithCORS("*"))
}
```

## 3. Service 示例（使用生成代码 + 拦截器）

```go
// internal/user/service.go
package user

import (
    "strconv"

    "github.com/crazy-airhead/aifei-go"
    "github.com/crazy-airhead/aifei-go/db"
    "github.com/crazy-airhead/aifei-go/server"
)

const ServicePrefix = "/user"

func init() {
    server.RegisterService(ServicePrefix, &Service{})
}

// MethodInterceptors 声明方法级拦截器
func (s *Service) MethodInterceptors() map[string][]aifei.Interceptor {
    return map[string][]aifei.Interceptor{
        "Create": {server.TxInterceptor()},
    }
}

type Service struct{}

// List 分页列表 — GET /user/list
func (s *Service) List(in aifei.Input) aifei.Output {
    pageNum := in.GetInt("page", 1)
    pageSize := in.GetInt("size", 10)
    page, err := db.SQL("SELECT * FROM user ORDER BY id DESC").Paginate(pageNum, pageSize)
    if err != nil {
        return server.Fail(err.Error())
    }
    return server.Of(page)
}

// Create 创建 — POST /user/create
func (s *Service) Create(in aifei.Input) aifei.Output {
    u := New()
    if err := in.GetBean(u); err != nil {
        return server.Fail("invalid request: " + err.Error())
    }
    result, err := u.Insert()
    if err != nil {
        return server.Fail(err.Error())
    }
    return server.Of(result.GetID())
}

// GetById 主键查询 — GET /user/:id
func (s *Service) GetById(in aifei.Input) aifei.Output {
    id, err := strconv.Atoi(in.Param("id"))
    if err != nil {
        return server.Fail("invalid id")
    }
    u, err := FindById(id)
    if err != nil {
        return server.Fail(err.Error())
    }
    if u == nil {
        return server.Fail("User not found")
    }
    return server.Of(u)
}

// UpdateById 更新 — PUT /user/:id
func (s *Service) UpdateById(in aifei.Input) aifei.Output {
    id, _ := strconv.Atoi(in.Param("id"))
    existing, _ := FindById(id)
    if existing == nil {
        return server.Fail("User not found")
    }
    if err := in.GetBean(existing); err != nil {
        return server.Fail("invalid request")
    }
    if _, err := existing.Update(); err != nil {
        return server.Fail(err.Error())
    }
    return server.Of(id)
}

// DeleteById 删除 — DELETE /user/:id
func (s *Service) DeleteById(in aifei.Input) aifei.Output {
    id, _ := strconv.Atoi(in.Param("id"))
    if _, err := DeleteById(id); err != nil {
        return server.Fail(err.Error())
    }
    return server.Of(nil)
}
```

## 4. 事务示例

```go
func Transfer(in aifei.Input) aifei.Output {
    from := in.GetInt64("from")
    to := in.GetInt64("to")
    money := in.GetInt("money")

    err := db.Transaction(func() error {
        n1, err := db.SQL("UPDATE account SET balance = balance - ? WHERE id = ?", money, from).Update()
        if err != nil {
            return err
        }
        n2, err := db.SQL("UPDATE account SET balance = balance + ? WHERE id = ?", money, to).Update()
        if err != nil {
            return err
        }
        if n1 != 1 || n2 != 1 {
            return fmt.Errorf("transfer failed")
        }
        return nil
    })

    if err != nil {
        return server.Fail(err.Error())
    }
    return server.OkMsg("success")
}
```

## 5. 批量操作示例

```go
func BatchInsert(in aifei.Input) aifei.Output {
    batch := db.NewBatch()
    rows := make([]*db.Row, 0, 100)
    for i := 0; i < 100; i++ {
        row := db.NewRow("user").
            Set("name", fmt.Sprintf("user_%d", i)).
            Set("age", 18+i%50)
        rows = append(rows, row)
    }

    result, err := batch.Insert(rows)
    if err != nil {
        return server.Fail(err.Error())
    }
    return server.OkMsg(fmt.Sprintf("inserted %d rows", result.RowsAffected))
}
```

## 6. 响应格式约定

使用 `server.Out` 流畅构建器生成统一响应格式：

```go
// 成功响应
server.Ok()              // {"code": 0, "msg": "ok"}
server.OkMsg("success")  // {"code": 0, "msg": "success"}
server.Of(data)          // {"code": 0, "msg": "ok", "data": data}
server.OfField("user", u) // {"code": 0, "msg": "ok", "data": {"user": {...}}}

// 失败响应
server.Fail("error")       // {"code": 90000, "msg": "error"}
server.FailWithCode(40001, "invalid") // {"code": 40001, "msg": "invalid"}

// Out 常量
server.CodeOK   = 0
server.CodeFail = 90000
```

## 7. go.mod 依赖

```
module github.com/crazy-airhead/aifei-go

go 1.26

// 零第三方依赖！仅使用 Go 标准库
// database/sql, encoding/json, net/http, log, etc.
// 用户按需引入数据库驱动:
//   _ "github.com/go-sql-driver/mysql"
//   _ "github.com/lib/pq"
//   _ "modernc.org/sqlite"
```

## 8. 测试策略

已有测试覆盖：

| 测试文件 | 行数 | 覆盖范围 |
|---------|------|---------|
| `db/db_test.go` | 53 | 基础 DB 操作 |
| `db/sql/sql_test.go` | 228 | SqlKit、SqlPara、SQL 指令 |
| `enjoy/enjoy_test.go` | 182 | 模板引擎渲染 |
| `json/json_test.go` | 45 | Marshal/Unmarshal |
| `log/log_test.go` | 114 | 日志级别和输出 |
| `generator/generator_test.go` | 314 | 代码生成器和模板 |
| `_test/demo/generated_test.go` | 152 | 生成代码集成测试 |
| `_test/db_test/db_test.go` | 971 | CRUD、分页、事务、批量、Enjoy SQL 全指令 |

### 集成测试示例（SQLite 内存数据库）

```go
func setupTestDB(t *testing.T) {
    err := db.Init("sqlite", ":memory:")
    if err != nil {
        t.Fatal(err)
    }
    db.SQL(`CREATE TABLE user (id INTEGER PRIMARY KEY, name TEXT, age INTEGER)`).Update()
}

func TestInsert(t *testing.T) {
    setupTestDB(t)
    row := db.NewRow("user").Set("name", "james").Set("age", 18)
    result, err := db.Insert(row)
    if err != nil {
        t.Fatal(err)
    }
    if result.GetInt("id") == 0 {
        t.Error("expected non-zero id after insert")
    }
}

func TestEnjoySQL(t *testing.T) {
    setupTestDB(t)
    db.Insert(db.NewRow("user").Set("name", "alice").Set("age", 25))

    // 测试 #where + #and + #para 动态条件
    rows, err := db.Sql(
        "select * from user #where() #and(age > #para(minAge))",
        map[string]interface{}{"minAge": 20},
    ).Find()

    if err != nil || len(rows) != 1 {
        t.Fatal("expected 1 row")
    }
}
```
