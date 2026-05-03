# Phase 4: 高级特性

> 目标：实现参数注入、批量操作完善、SQL 条件构建器、内置中间件

## 1. 参数注入 (对应 `cn.aifei.argument` 包)

**Java 版 Argument 体系：**
```java
// ArgumentFactory 注册的类型处理器:
InputArgument    — 注入 Input 对象
OutputArgument   — 注入 Output 对象
StringArgument   — String 类型参数
IntArgument      — Integer 类型参数
LongArgument     — Long 类型参数
DoubleArgument   — Double 类型参数
BigDecimalArgument — BigDecimal 类型参数
BooleanArgument  — Boolean 类型参数
DateArgument     — Date 类型参数
LocalDateArgument — LocalDate 类型参数
LocalDateTimeArgument — LocalDateTime 类型参数
ArrayArgument    — 数组类型参数
ListArgument     — List 类型参数
MapArgument      — Map 类型参数
BeanArgument     — Bean 类型参数 (JSON 反序列化)
EnumArgument     — 枚举类型参数
PathParaArgument — 路径参数
```

**Go 版设计：**

Go 的参数注入比 Java 简单得多，因为不需要反射扫描方法参数。通过 Context 直接获取：

```go
// argument.go
package aifei

import "reflect"

// ArgumentInjector 参数注入器接口
type ArgumentInjector func(c *Context, targetType reflect.Type) (interface{}, error)

// 默认注入器注册表
var injectors = map[reflect.Type]ArgumentInjector{}

// RegisterInjector 注册自定义注入器
func RegisterInjector(targetType reflect.Type, injector ArgumentInjector)

// 内置注入器
func init() {
    // string
    injectors[reflect.TypeOf("")] = func(c *Context, t reflect.Type) (interface{}, error) {
        return c.GetStr(""), nil
    }
    // int
    injectors[reflect.TypeOf(0)] = func(c *Context, t reflect.Type) (interface{}, error) {
        return c.GetInt(""), nil
    }
    // ... 其他类型
}
```

**实际使用中，Go 通过 Context 方法直接获取参数，不需要复杂的注入框架：**

```go
func UserList(c *Context) {
    name := c.GetStr("name")
    age := c.GetIntDefault("age", 0)
    page := c.GetIntDefault("page", 1)
    // ...
}
```

---

## 2. 内置 Middleware

对应 Java 版通过 Interceptor 实现的常用功能。

### Logger 中间件

```go
// middleware.go
package aifei

import (
    "time"
)

func Logger() Middleware {
    return func(next HandlerFunc) HandlerFunc {
        return func(c *Context) {
            start := time.Now()
            path := c.Path()
            method := c.Method()

            next(c)

            log.Info("%s %s %d %v",
                method, path, c.status, time.Since(start))
        }
    }
}
```

### Recover 中间件

```go
func Recover() Middleware {
    return func(next HandlerFunc) HandlerFunc {
        return func(c *Context) {
            defer func() {
                if err := recover(); err != nil {
                    log.Error("panic recovered: %v", err)
                    c.Status(500).Json(map[string]interface{}{
                        "code": 500,
                        "msg":  "Internal Server Error",
                    })
                }
            }()
            next(c)
        }
    }
}
```

### CORS 中间件

```go
func CORS(allowOrigin string) Middleware {
    return func(next HandlerFunc) HandlerFunc {
        return func(c *Context) {
            c.Header("Access-Control-Allow-Origin", allowOrigin)
            c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,PATCH,OPTIONS")
            c.Header("Access-Control-Allow-Headers", "Content-Type,Authorization")
            if c.Method() == "OPTIONS" {
                c.Status(204)
                return
            }
            next(c)
        }
    }
}
```

### BasicAuth 中间件

```go
func BasicAuth(check func(user, pass string) bool) Middleware {
    return func(next HandlerFunc) HandlerFunc {
        return func(c *Context) {
            user, pass, ok := c.Request.BasicAuth()
            if !ok || !check(user, pass) {
                c.Header("WWW-Authenticate", `Basic realm="Restricted"`)
                c.Status(401).Text("Unauthorized")
                return
            }
            next(c)
        }
    }
}
```

---

## 3. SQL 条件构建器完善

对应 Java 版的 `#where()`, `#and()`, `#orderBy()`, `#para()` 指令。

```go
// db/sql_builder.go
package db

import (
    "strings"
    "fmt"
)

type SQLBuilder struct {
    selectPart  string
    fromPart    string
    whereParts  []string
    whereArgs   []interface{}
    orderByPart string
    limit       int
    offset      int
}

// SELECT
func Select(fields string) *SQLBuilder

// FROM
func (b *SQLBuilder) From(table string) *SQLBuilder

// WHERE (可多次调用，自动 AND 连接)
func (b *SQLBuilder) Where(condition string, args ...interface{}) *SQLBuilder

// 条件 WHERE (值为空时自动跳过)
func (b *SQLBuilder) WhereIf(condition string, arg interface{}, apply bool) *SQLBuilder

// AND 等价于 Where
func (b *SQLBuilder) And(condition string, args ...interface{}) *SQLBuilder

// 条件 AND
func (b *SQLBuilder) AndIf(condition string, arg interface{}, apply bool) *SQLBuilder

// ORDER BY
func (b *SQLBuilder) OrderBy(order string) *SQLBuilder

// LIMIT / OFFSET
func (b *SQLBuilder) Limit(limit int) *SQLBuilder
func (b *SQLBuilder) Offset(offset int) *SQLBuilder

// 构建完整 SQL
func (b *SQLBuilder) Build() (string, []interface{})

// 直接查询
func (b *SQLBuilder) Find() ([]*Row, error)
func (b *SQLBuilder) FindFirst() (*Row, error)
func (b *SQLBuilder) Paginate(pageNum, pageSize int) (*Page, error)
func (b *SQLBuilder) Count() (int64, error)
```

**使用示例：**

```go
// 基本查询
rows, err := db.Select("*").From("user").Where("age > ?", 18).Find()

// 条件查询 (name 非空时才添加条件)
rows, err := db.Select("*").
    From("user").
    Where("age > ?", 18).
    AndIf("name like ?", "%"+name+"%", name != "").
    OrderBy("id desc").
    Paginate(1, 10)

// 直接 SQL + 条件追加
rows, err := db.SQL("select * from user").Where("age > ?", 18).Find()
```

---

## 4. Struct 注册路由详解

对应 Java 版 `@Path` 注解 + 包扫描注册。

```go
// router.go 中 Register 方法实现细节

import "reflect"

func (r *Router) Register(prefix string, service interface{}, middlewares ...Middleware) {
    t := reflect.TypeOf(service)
    v := reflect.ValueOf(service)
    prefix = strings.TrimRight(prefix, "/")

    for i := 0; i < t.NumMethod(); i++ {
        method := t.Method(i)
        // 方法名转为路径: List → /list, Save → /save
        path := prefix + "/" + camelToPath(method.Name)

        // 创建 handler
        handler := func(c *Context) {
            v.MethodByName(method.Name).Call([]reflect.Value{reflect.ValueOf(c)})
        }

        // 应用中间件
        finalHandler := handler
        for i := len(middlewares) - 1; i >= 0; i-- {
            finalHandler = middlewares[i](finalHandler)
        }

        // 注册为 POST (默认)
        r.POST(path, finalHandler)
    }
}

// camelToPath 将方法名转为路径
// "List" → "list", "FindById" → "find-by-id", "Save" → "save"
func camelToPath(name string) string {
    var buf strings.Builder
    for i, r := range name {
        if unicode.IsUpper(r) && i > 0 {
            buf.WriteByte('-')
        }
        buf.WriteRune(unicode.ToLower(r))
    }
    return buf.String()
}
```

**自定义 HTTP 方法映射：**

```go
// 通过接口约定方法名前缀
// Get* → GET, Post*/Save*/Delete*/Update* → POST
func (r *Router) Register(prefix string, service interface{}, middlewares ...Middleware) {
    // ...
    httpMethod := "POST"
    switch {
    case strings.HasPrefix(method.Name, "Get"):
        httpMethod = "GET"
    case strings.HasPrefix(method.Name, "Delete"):
        httpMethod = "DELETE"
    case strings.HasPrefix(method.Name, "Put", "Update"):
        httpMethod = "PUT"
    }
    r.Handle(httpMethod, path, finalHandler)
}
```

---

## 5. 优雅关闭

```go
// aifei.go 中 Run 方法

func (a *Aifei) Run(addr string) {
    a.server = &http.Server{
        Addr:    addr,
        Handler: a,
    }

    // 启动信号监听
    go func() {
        quit := make(chan os.Signal, 1)
        signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
        <-quit

        log.Info("Shutting down server...")

        // 调用 OnStop
        if a.config.OnStop != nil {
            a.config.OnStop()
        }

        // 停止插件
        for _, p := range a.plugins {
            p.Stop()
        }

        // 优雅关闭 HTTP 服务
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        a.server.Shutdown(ctx)

        log.Info("Server stopped")
    }()

    log.Info("Aifei %s starting on %s", Version, addr)

    // 启动插件
    for _, p := range a.plugins {
        if err := p.Start(); err != nil {
            log.Error("Plugin start failed: %v", err)
        }
    }

    // 调用 OnStart
    if a.config.OnStart != nil {
        a.config.OnStart()
    }

    if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
        log.Error("Server error: %v", err)
    }
}
```

---

## 6. 静态文件服务

对应 Java 版 Undertow 的 ResourceHandler。

```go
// server.go

type StaticConfig struct {
    Prefix     string   // URL 前缀, 如 "/static"
    Dir        string   // 文件目录, 如 "./webapp"
    IndexFiles []string // 默认首页文件, 如 ["index.html"]
}

func (a *Aifei) Static(prefix, dir string)
func (a *Aifei) StaticFile(prefix, file string)
func (a *Aifei) StaticFS(prefix string, fs http.FileSystem)
```
