# Phase 5: 示例应用与集成

> 目标：创建完整的示例应用，验证框架各模块的集成

## 1. 示例项目结构

```
_example/
└── demo/
    ├── go.mod
    ├── go.sum
    └── main.go
```

## 2. 完整示例应用

```go
package main

import (
    "fmt"
    "log"

    "github.com/aifei/aifei"
    "github.com/aifei/aifei/db"
)

func main() {
    app := aifei.New()

    // 初始化数据库
    err := db.Init("sqlite", "./demo.db",
        db.WithPrinter(func(sql string, args ...interface{}) {
            log.Printf("[SQL] %s %v", sql, args)
        }),
    )
    if err != nil {
        log.Fatal(err)
    }

    // 创建表
    db.SQL(`CREATE TABLE IF NOT EXISTS user (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        name TEXT NOT NULL,
        age INTEGER DEFAULT 0,
        email TEXT
    )`).Update()

    // 全局中间件
    app.Use(aifei.Logger(), aifei.Recover())

    // API 路由
    app.GET("/api/user/list", UserList)
    app.GET("/api/user/:id", UserGet)
    app.POST("/api/user/save", UserSave)
    app.POST("/api/user/delete", UserDelete)

    // struct 注册 (Java @Path 风格)
    app.Register("/api/v2/user", &UserService{})

    // 启动
    app.Run(":8080")
}

// ---- Handler 函数 ----

func UserList(c *aifei.Context) {
    name := c.GetStr("name")
    page := c.GetIntDefault("page", 1)
    size := c.GetIntDefault("size", 10)

    builder := db.SQL("select * from user where 1=1")
    if name != "" {
        builder.And("and name like ?", "%"+name+"%")
    }

    result, err := builder.Paginate(page, size)
    if err != nil {
        c.JsonFail(err.Error())
        return
    }
    c.JsonOK(result)
}

func UserGet(c *aifei.Context) {
    id := c.PathPara(0)
    row, err := db.FindByID("user", id)
    if err != nil {
        c.JsonFail(err.Error())
        return
    }
    if row == nil {
        c.JsonFail("用户不存在")
        return
    }
    c.JsonOK(row)
}

func UserSave(c *aifei.Context) {
    row := db.NewRow("user")
    row.Set("name", c.GetStr("name"))
    row.Set("age", c.GetIntDefault("age", 0))
    row.Set("email", c.GetStr("email"))

    id := c.GetIntDefault("id", 0)
    if id > 0 {
        row.ID(id)
        _, err := db.Update(row)
        if err != nil {
            c.JsonFail(err.Error())
            return
        }
    } else {
        _, err := db.Insert(row)
        if err != nil {
            c.JsonFail(err.Error())
            return
        }
    }
    c.JsonOK(nil)
}

func UserDelete(c *aifei.Context) {
    id := c.GetIntDefault("id", 0)
    if id <= 0 {
        c.JsonFail("id 不能为空")
        return
    }
    _, err := db.DeleteByID("user", id)
    if err != nil {
        c.JsonFail(err.Error())
        return
    }
    c.JsonOK(nil)
}

// ---- Struct 服务 (Java @Path 风格) ----

type UserService struct{}

func (s *UserService) List(c *aifei.Context) {
    rows, err := db.SQL("select * from user").Find()
    if err != nil {
        c.JsonFail(err.Error())
        return
    }
    c.JsonOK(rows)
}

func (s *UserService) Save(c *aifei.Context) {
    row := db.NewRow("user")
    row.Set("name", c.GetStr("name"))
    row.Set("age", c.GetIntDefault("age", 0))
    _, err := db.Insert(row)
    if err != nil {
        c.JsonFail(err.Error())
        return
    }
    c.JsonOK(nil)
}

func (s *UserService) Delete(c *aifei.Context) {
    _, err := db.DeleteByID("user", c.GetInt("id"))
    if err != nil {
        c.JsonFail(err.Error())
        return
    }
    c.JsonOK(nil)
}
```

---

## 3. 事务示例

```go
func Transfer(c *aifei.Context) {
    from := c.GetInt64("from")
    to := c.GetInt64("to")
    money := c.GetIntDefault("money", 0)

    err := db.Transaction(func() error {
        n1, err := db.SQL("update account set balance = balance - ? where id = ?", money, from).Update()
        if err != nil { return err }
        n2, err := db.SQL("update account set balance = balance + ? where id = ?", money, to).Update()
        if err != nil { return err }
        if n1 != 1 || n2 != 1 {
            return fmt.Errorf("转账失败")
        }
        return nil
    })

    if err != nil {
        c.JsonFail(err.Error())
        return
    }
    c.JsonOK("转账成功")
}
```

---

## 4. 批量操作示例

```go
func BatchInsert(c *aifei.Context) {
    rows := make([]*db.Row, 0, 100)
    for i := 0; i < 100; i++ {
        row := db.NewRow("user").
            Set("name", fmt.Sprintf("user_%d", i)).
            Set("age", 18+i%50)
        rows = append(rows, row)
    }

    result, err := db.Batch().Insert(rows)
    if err != nil {
        c.JsonFail(err.Error())
        return
    }
    c.JsonOK(fmt.Sprintf("插入 %d 行", result.RowsAffected))
}
```

---

## 5. 中间件示例

```go
// 认证中间件 (替代 Java Interceptor)
func AuthMiddleware() aifei.Middleware {
    return func(next aifei.HandlerFunc) aifei.HandlerFunc {
        return func(c *aifei.Context) {
            token := c.GetHeader("Authorization")
            if token == "" {
                c.Status(401).Json(map[string]interface{}{
                    "code": 401,
                    "msg":  "未登录",
                })
                c.Abort()
                return
            }
            // 验证 token...
            c.Next()
        }
    }
}

// 使用
api := app.Group("/api", AuthMiddleware())
api.GET("/user/list", UserList)
```

---

## 6. 响应格式约定

```go
// context.go 中统一响应方法

func (c *Context) JsonOK(data interface{}) {
    c.Json(map[string]interface{}{
        "code": 0,
        "msg":  "ok",
        "data": data,
    })
}

func (c *Context) JsonFail(msg string) {
    c.Json(map[string]interface{}{
        "code": -1,
        "msg":  msg,
        "data": nil,
    })
}
```

---

## 7. go.mod 依赖

```
module github.com/aifei/aifei

go 1.26

// 零第三方依赖！仅使用 Go 标准库
// database/sql, encoding/json, net/http, log, etc.
// 用户按需引入数据库驱动:
//   _ "github.com/go-sql-driver/mysql"
//   _ "github.com/lib/pq"
//   _ "modernc.org/sqlite"
```

---

## 8. 测试策略

```go
// db/db_test.go
package db_test

import (
    "testing"
    "github.com/aifei/aifei/db"
    _ "modernc.org/sqlite"
)

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

func TestFindById(t *testing.T) {
    setupTestDB(t)

    db.Insert(db.NewRow("user").Set("name", "james").Set("age", 18))

    row, err := db.FindByID("user", 1)
    if err != nil {
        t.Fatal(err)
    }
    if row.GetStr("name") != "james" {
        t.Error("expected name=james")
    }
}

func TestPaginate(t *testing.T) {
    setupTestDB(t)

    for i := 0; i < 25; i++ {
        db.Insert(db.NewRow("user").Set("name", fmt.Sprintf("user_%d", i)).Set("age", 18+i))
    }

    page, err := db.SQL("select * from user").Paginate(1, 10)
    if err != nil {
        t.Fatal(err)
    }
    if page.TotalRows != 25 {
        t.Errorf("expected totalRows=25, got %d", page.TotalRows)
    }
    if len(page.Rows) != 10 {
        t.Errorf("expected 10 rows, got %d", len(page.Rows))
    }
}

func TestTransaction(t *testing.T) {
    setupTestDB(t)

    err := db.Transaction(func() error {
        db.Insert(db.NewRow("user").Set("name", "user1"))
        db.Insert(db.NewRow("user").Set("name", "user2"))
        return nil
    })
    if err != nil {
        t.Fatal(err)
    }

    count, _ := db.Count("user")
    if count != 2 {
        t.Errorf("expected count=2, got %d", count)
    }
}
```
