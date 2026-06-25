# Phase 1: 核心框架

> 目标：实现 Aifei Go 的核心 HTTP 框架，包含 Input/Output 接口、Router、Handler、Interceptor

## 1. 项目初始化

### 文件: `go.mod`

```
module github.com/crazy-airhead/aifei-go

go 1.26
```

### 文件: `aifei.go`

Aifei 核心入口类，对应 Java 的 `cn.aifei.core.Aifei`。

**Java 版关键方法：**
- `Aifei.start(AifeiConfig<I,O> config, String[] args)` — 启动框架
- `Aifei.stop()` — 停止框架
- `Aifei.makeHandlerChain(List<Handler<I,O>> handlers)` — 构建 Handler 责任链
- `Aifei.getVersion()` — 获取版本号

**Go 版设计：**

```go
package aifei

const Version = "1.0.0"

type Aifei struct {
    config      *Config
    router      *Router
    handlers []Handler
    plugins     []Plugin
}

// New 创建 Aifei 实例
func New(opts ...Option) *Aifei

// Use 注册全局 Handler wrapper
func (a *Aifei) Use(m ...Handler)

// 路由注册
func (a *Aifei) GET(path string, handlers ...HandlerFunc)
func (a *Aifei) POST(path string, handlers ...HandlerFunc)
func (a *Aifei) PUT(path string, handlers ...HandlerFunc)
func (a *Aifei) DELETE(path string, handlers ...HandlerFunc)
func (a *Aifei) PATCH(path string, handlers ...HandlerFunc)
func (a *Aifei) Any(path string, handlers ...HandlerFunc)
func (a *Aifei) Handle(method, path string, handlers ...HandlerFunc)
func (a *Aifei) Group(prefix string, handlers ...Handler) *RouterGroup
func (a *Aifei) Register(prefix string, service interface{}, handlers ...Handler)

// ServeHTTP 实现 http.Handler 接口
func (a *Aifei) ServeHTTP(w http.ResponseWriter, r *http.Request)

// 访问器
func (a *Aifei) Router() *Router
func (a *Aifei) Handlers() []Handler
func (a *Aifei) Plugins() []Plugin
func (a *Aifei) OnStartFunc() func()
func (a *Aifei) OnStopFunc() func()
```

> 注：`Run()` 方法不在 `Aifei` 上，而是 `server.Run(app, addr, opts...)` 包级函数。这样核心包保持零依赖，HTTP 服务器细节由 `server` 和 `go-http` 包处理。

---

## 2. Input + Output 接口（保持 Java 分离设计）

对应 Java 的 `cn.aifei.core.Input` + `cn.aifei.core.Output`。

**Java Input 接口完整方法清单：**
```
has(name) has(index) pathPara(string)
getBean(name, class) getList(name, class) getArray(name, class) getMap(name)
getStr(name) getStr(index) getInt(name) getInt(index) getLong(name) getLong(index)
getDouble(name) getDouble(index) getBigDecimal(name) getBigDecimal(index)
getBoolean(name) getBoolean(index)
getDate(name) getLocalDate(name) getLocalTime(name) getLocalDateTime(name)
// 带默认值的 default 方法
getStr(name, default) getInt(name, default) getLong(name, default) ...
```

**Go 版设计 — Input 接口（`input.go`）：**

```go
package aifei

type Input interface {
    // 请求参数
    Has(name string) bool
    GetStr(name string) string
    GetStr(name, def string) string
    GetInt(name string) int
    GetInt(name string, def int) int
    GetInt64(name string) int64
    GetInt64(name string, def int64) int64
    GetFloat64(name string) float64
    GetFloat64(name string, def float64) float64
    GetBool(name string) bool
    GetBool(name string, def bool) bool
    GetBean(ptr interface{}) error        // JSON body → struct

    // 路径参数
    PathPara(index int) string
    Param(name string) string             // 命名路径参数

    // 请求元数据
    Method() string
    Path() string
    RemoteIP() string
    Body() []byte                         // 原始请求体
    Query() url.Values                    // 查询参数
}
```

**Go 版设计 — Output 接口（`output.go`）：**

```go
package aifei

type Output interface {
    Code() int
    Msg() string
    Data() interface{}
}

// NewResult 创建简单 Output
func NewResult(code int, msg string, data interface{}) Output
```

> Output 接口极简，仅定义响应结构。实际的 JSON 渲染和流式构建器在 `server.Out` 中实现。

### 实现类

- **`go-http.HttpContext`** — 实现 `aifei.Input`，包装 `*http.Request`
- **`server.In`** — 完整 `aifei.Input` 实现（用于 server 包）
- **`server.Out`** — 流式 `aifei.Output` 构建器

```go
// server.Out — 流式响应构建器
out := server.Ok()           // {code:0, msg:"ok"}
out := server.OkMsg("done")  // {code:0, msg:"done"}
out := server.Fail("error")  // {code:90000, msg:"error"}
out := server.Of(data)       // {code:0, msg:"ok", data: data}
out := server.OfField("user", user)  // {code:0, msg:"ok", data: {user: ...}}

// 链式修改
out.SetMsg("updated").SetData(newData)
out.IsOk()        // true if code == CodeOK (0)
out.ShouldRollback() // true if code != CodeOK (用于事务回滚判断)
```

---

## 3. Handler（替代 Java AOP + 责任链）

对应 Java 的 `cn.aifei.core.Handler` + `cn.aifei.aop.Interceptor`。

**Go 版设计：**

```go
package aifei

// HandlerFunc 处理函数 — 接收 Input，返回 Output
type HandlerFunc func(in Input) Output

// ChainHandlers 组装 Handler 包装链
// wrapper 按添加顺序执行，最后一个为最终 handler
func ChainHandlers(handlers []Handler, final HandlerFunc) HandlerFunc
```

> 没有 `Middleware` 类型。包装器就是普通的 `Handler`，无需引入额外概念。

**Java 拦截器映射到 Go Handler wrapper：**

Java:
```java
public class AuthInterceptor implements Interceptor {
    public void intercept(Invocation inv) {
        String token = inv.getInput().getStr("token");
        if (token == null) {
            inv.getOutput().json("未登录");
            return;
        }
        inv.invoke();
    }
}
```

Go:
```go
func AuthHandler(next aifei.HandlerFunc) aifei.HandlerFunc {
    return func(in aifei.Input) aifei.Output {
        token := in.GetStr("token")
        if token == "" {
            return server.Fail("未登录")
        }
        return next(in)
    }
}
```

---

## 4. Interceptor（方法级 AOP）

除 Handler wrapper 链外，Aifei-Go 还保留了 Java 的 Interceptor 概念用于方法级拦截：

```go
package aifei

// Interceptor 拦截器接口 — 方法级 AOP
type Interceptor interface {
    Intercept(method string, in Input, invoke func() Output) Output
}

// InterceptorFunc 函数适配器
type InterceptorFunc func(method string, in Input, invoke func() Output) Output

// MethodInterceptors 可选的 service 接口
type MethodInterceptors interface {
    MethodInterceptors() map[string][]Interceptor
}
```

**使用示例：**

```go
type UserService struct{}

func (s *UserService) MethodInterceptors() map[string][]aifei.Interceptor {
    return map[string][]aifei.Interceptor{
        "Create": {server.TxInterceptor()},
        "Delete": {logInterceptor},
    }
}
```

---

## 5. Router（路由系统）

对应 Java 的 `cn.aifei.router.Router` + `cn.aifei.router.Action` + `cn.aifei.router.ActionGroup`。

**Go 版设计 — Radix Tree 路由：**

```go
package aifei

type Router struct {
    trees map[string]*node  // method → radix tree
}

// 注册路由
func (r *Router) GET(path string, handlers ...HandlerFunc)
func (r *Router) POST(path string, handlers ...HandlerFunc)
func (r *Router) PUT(path string, handlers ...HandlerFunc)
func (r *Router) DELETE(path string, handlers ...HandlerFunc)
func (r *Router) PATCH(path string, handlers ...HandlerFunc)
func (r *Router) HEAD(path string, handlers ...HandlerFunc)
func (r *Router) Any(path string, handlers ...HandlerFunc)
func (r *Router) Handle(method, path string, handlers ...HandlerFunc)

// 路由组 (支持前缀和包装器)
func (r *Router) Group(prefix string, handlers ...Handler) *RouterGroup

// struct 注册 (保留 Java @Path 风格的简洁性)
func (r *Router) Register(prefix string, service interface{}, handlers ...Handler)

// 路由匹配
func (r *Router) Lookup(method, path string) (handlers []HandlerFunc, params map[string]string, found bool)

// Radix tree node
type node struct {
    path      string
    children  []*node
    handlers  []HandlerFunc
    wildChild bool
    param     bool
    catchAll  bool
}
```

**RouterGroup 设计：**

```go
type RouterGroup struct {
    prefix      string
    handlers    []Handler
    router      *Router
}

func (g *RouterGroup) GET(path string, handlers ...HandlerFunc)
func (g *RouterGroup) POST(path string, handlers ...HandlerFunc)
func (g *RouterGroup) PUT(path string, handlers ...HandlerFunc)
func (g *RouterGroup) DELETE(path string, handlers ...HandlerFunc)
func (g *RouterGroup) Handle(method, path string, handlers ...HandlerFunc)
func (g *RouterGroup) Group(prefix string, handlers ...Handler) *RouterGroup
```

**Register 方法签名约定：**

- 方法签名必须为 `func(in aifei.Input) aifei.Output`
- 方法名映射 HTTP 方法：`List*`/`Get*` → GET，`Post*`/`Save*`/`Create*` → POST，`Put*`/`Update*` → PUT，`Delete*`/`Remove*` → DELETE
- `ById` 后缀 → `/:id` 路径参数

```go
type UserService struct{}

func (s *UserService) List(in aifei.Input) aifei.Output    { ... }
func (s *UserService) Create(in aifei.Input) aifei.Output   { ... }
func (s *UserService) GetById(in aifei.Input) aifei.Output  { ... }

// 自动注册:
//   GET    /api/user/list      → UserService.List
//   POST   /api/user/create    → UserService.Create
//   GET    /api/user/:id       → UserService.GetById
app.Register("/api/user", &UserService{})
```

---

## 6. Config（配置系统）

对应 Java 的 `cn.aifei.config.AifeiConfig` + `Settings` + `Plugins`。

**Go 版设计 — Functional Options 模式：**

```go
package aifei

type Config struct {
    Handlers []Handler
    Plugins     []Plugin
    OnStart     func()
    OnStop      func()
}

type Option func(*Aifei)

func WithHandlers(m ...Handler) Option
func WithPlugin(p ...Plugin) Option
func WithOnStart(fn func()) Option
func WithOnStop(fn func()) Option
```

---

## 7. Plugin（插件系统）

对应 Java 的 `cn.aifei.plugin.Plugin`。

```go
package aifei

type Plugin interface {
    Start() error
    Stop() error
}
```

---

## 8. HTTP 适配层

### go-http（`go-http/`）

桥接 `net/http` 和 aifei 框架：

```go
package gohttp

// HttpContext 实现 aifei.Input，包装 *http.Request
type HttpContext struct { ... }
func NewInput(r *http.Request) *HttpContext

// HttpHandler 实现 http.Handler，桥接到 aifei.Aifei
type HttpHandler struct { ... }
func NewHttpHandler(app *aifei.Aifei) *HttpHandler

// Server 接口
type Server interface {
    Start(handler http.Handler) error
    Stop() error
}
```

### server（`server/`）

生产级启动层，提供包装器函数、响应构建器、优雅关闭：

```go
// HandlerFunc 包装器（返回 func(aifei.HandlerFunc) aifei.HandlerFunc）
server.Logger()     // 请求日志
server.Recover()    // panic 恢复
server.Timeout(d)   // 超时控制

// HTTP 级包装器（返回 func(http.Handler) http.Handler）
server.CORS(origin)            // 跨域
server.BasicAuth(check)        // 基础认证
server.RequestID()             // 请求 ID
server.StaticFile(prefix, dir) // 静态文件

// 启动
server.Run(app, addr string, opts ...Option)
// opts: WithCORS, WithBasicAuth, WithRequestID, WithHTTPHandler
```

---

## 9. 文件依赖关系

```
aifei.go
  ├── config.go       (Config, Option)
  ├── input.go        (Input 接口)
  ├── output.go       (Output 接口)
  ├── handler.go      (HandlerFunc, ChainHandlers)
  ├── router.go       (Router, RouterGroup, node)
  ├── interceptor.go  (Interceptor, InterceptorFunc, MethodInterceptors)
  └── plugin.go       (Plugin)

go-http/
  ├── context.go      (HttpContext implements Input)
  ├── handler.go      (HttpHandler implements http.Handler)
  └── server.go       (Server interface, DefaultServer)

server/
  ├── in.go           (In implements Input)
  ├── out.go          (Out implements Output, fluent builder)
  ├── middleware.go   (Logger, Recover, Timeout, CORS, BasicAuth, RequestID, StaticFile)
  ├── run.go          (Run with graceful shutdown)
  ├── service.go      (RegisterService, AutoRegisterServices)
  └── tx_interceptor.go (TxInterceptor)
```

---

## 10. 完整使用示例

```go
package main

import (
    "github.com/crazy-airhead/aifei-go"
    "github.com/crazy-airhead/aifei-go/server"
)

func main() {
    app := aifei.New()

    // 全局 Handler wrapper 链
    app.Use(server.Logger(), server.Recover())

    // 路由注册 — HandlerFunc 签名: func(in aifei.Input) aifei.Output
    app.GET("/api/health", func(in aifei.Input) aifei.Output {
        return server.OkMsg("healthy")
    })

    app.GET("/api/user/:id", func(in aifei.Input) aifei.Output {
        id := in.Param("id")
        // 查询用户...
        return server.Of(user)
    })

    // 路由组
    admin := app.Group("/api/admin")
    admin.GET("/dashboard", func(in aifei.Input) aifei.Output {
        return server.OkMsg("admin dashboard")
    })

    // struct 注册 (Java @Path 风格)
    app.Register("/api/user", &UserService{})

    // 启动（server.Run 处理信号、优雅关闭、插件生命周期）
    server.Run(app, ":8080", server.WithCORS("*"))
}
```
