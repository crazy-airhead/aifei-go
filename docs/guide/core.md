# Aifei-Go 核心包（./aifei）：传输无关的 Web 框架内核

> **`Aifei` = Router + Handler 链 + Plugin。**核心包不绑定任何 HTTP 实现——它只定义 `Input`/`Output`/`Handler` 契约与一棵 radix 路由树，由 [`http`](http.md)/[`server`](server.md) 适配器驱动。

---

## 1. 背景与定位

`aifei` 包是整个 Aifei-Go 框架的内核，对应 Java Aifei 的 `aifei-core` 模块。它回答一个问题：**一次"调用"由什么构成？**

- **输入**：参数读取（`Param`）+ 请求级元数据（`Meta`）
- **输出**：三元组 `code`/`msg`/`data`
- **处理**：`HandlerFunc func(in Input) Output`
- **编排**：`Handler` 包装链（替代 Java 的 CGLIB/Javassist 动态代理）
- **路由**：每个 HTTP 方法一棵 radix 树
- **扩展**：`Plugin` 生命周期 + `Interceptor` 方法级 AOP

关键设计取舍：

| 议题 | 决策 | 说明 |
|------|------|------|
| HTTP 耦合 | **零** | `Aifei` 自身**不**实现 `http.Handler`；HTTP 概念（method/remoteIP/cookie）留在 [`http`](http.md) 适配器的 `HTTPMeta` 上 |
| 外部依赖 | **零** | 仅用 Go 标准库 |
| AOP 实现 | Handler 包装链 + Interceptor | Go 无动态代理，用函数包装显式编排 |
| 路由数据结构 | radix 树（每方法一棵） | 注册时合并公共前缀，查找约 O(path 长度) |
| 配置风格 | 函数式选项 | `New(WithHandlers(...), WithPlugin(...))` |

当前版本：`aifei.Version = "1.0.0"`。本文聚焦 `./aifei` 包本身；框架全貌见 [aifei-go-intro](aifei-go.md)。

---

## 2. 总体架构

`Aifei` 是三样东西的组合：一棵路由树、一组全局 Handler、一组 Plugin。它本身**不接 socket**——把 `Router` 和 `Handlers` 暴露给适配器（`http.HttpHandler` 或 `server.IoHandler`），由适配器驱动整个请求生命周期。

```
                 ┌─────────────── aifei.Aifei ───────────────┐
   适配器          │  router *Router      handlers []Handler    │
   (http/server)─▶│  plugins []Plugin    config *Config        │
                 │                                            │
                 │  Router() Handlers() Plugins() OnStartFunc()│  ← 适配器读取这些
                 └──────────────────┬───────────────────────────┘
                                   │
                                   ▼
           Lookup(method, path) → (handlers, params, found)
                                   │
                                   ▼
           ChainHandlers(globalHandlers, routeFinal)(in) → Output
                                   │
                                   ▼
                       适配器把 Output 写回 socket
```

核心包的职责到"返回 `Output`"为止；序列化、HTTP 状态码、模板渲染都在适配器层。这条边界是核心包能保持零依赖、可被任意传输驱动的根本。

### 关键类型一览

| 类型 | 定义于 | 职责 |
|------|--------|------|
| `Aifei` | `aifei.go` | 框架实例，持有 router/handlers/plugins |
| `Input` / `Param` / `Meta` | `input.go` | 请求读取契约 |
| `Output` | `output.go` | 响应三元组契约 |
| `HandlerFunc` / `Handler` | `handler.go` | 处理函数与包装器 |
| `ChainHandlers` | `handler.go` | 构建包装链 |
| `Router` / `RouterGroup` | `router.go` | radix 路由 + 分组 |
| `Interceptor` / `MethodInterceptors` | `interceptor.go` | 方法级 AOP |
| `Config` / `Option` | `config.go` | 函数式配置 |
| `Plugin` | `plugin.go` | 生命周期扩展 |

---

## 3. Input / Output：调用的契约

### 3.1 Input = Param + Meta

`Input` 故意拆成两个子契约。`Param` 是任何调用源（HTTP、测试 fixture、程序内调用）都能满足的参数读取；`Meta` 是每次调用都有的请求级元数据。HTTP 特有概念（method 动词、remote 地址、cookie）**不在核心 `Input` 上**——它们在 [`http`](http.md) 的 `HTTPMeta` 上，通过类型断言按需获取。

```go
type Param interface {
    Has(name string) bool
    PathPara(index int) string
    PathParaByName(name string) string
    Param(name string) string // PathParaByName 的别名

    // 类型化读取，可选默认值：缺失/空/非法时返回默认
    GetStr(key string, def ...string) string
    GetInt(key string, def ...int) int
    GetInt64(key string, def ...int64) int64
    GetFloat64(key string, def ...float64) float64
    GetBool(key string, def ...bool) bool

    // 结构化绑定：GetBean(&user) / GetBean(&user, "data")
    GetBean(obj interface{}, keys ...string) error
    // 参数→map：GetMap() / GetMap("user")（前缀剥离）
    GetMap(keys ...string) map[string]interface{}
}

type Meta interface {
    Context() context.Context // 可取消，透传到 db/RPC
    Header(name string) string
    Path() string
    Body() []byte
}

type Input interface { Param; Meta }
```

`GetStr`/`GetInt`/... 的 variadic 默认值是核心包的一贯风格——调用方写 `in.GetInt("page", 1)` 即可，无需 if-else。

### 3.2 Output

`Output` 是极简三元组。HTTP 适配器默认把业务语义放在 `code` 字段，HTTP 状态码另算（见 [`server`](server.md) 的 `IoHandler`）。

```go
type Output interface {
    Code() int
    Msg() string
    Data() interface{}
}

// 内部最小实现 + 构造器
func NewResult(code int, msg string, data interface{}) Output
```

业务侧通常用 [`server`](server.md) 的 `Out` 构建器（`Ok()`/`Fail()`/`Of()`/...），它实现了 `aifei.Output` 并额外携带渲染意图（视图/文件/重定向）。核心包不依赖 `server`，故只定义接口。

---

## 4. Aifei 入口与函数式选项

```go
type Aifei struct {
    config   *Config
    router   *Router
    handlers []Handler
    plugins  []Plugin
}

func New(opts ...Option) *Aifei  // 选项直接作用在 *Aifei 上
```

`New` 创建实例后，选项函数依次执行；`Use(h ...Handler)` 追加全局 Handler；路由方法（`GET`/`POST`/`PUT`/`DELETE`/`PATCH`/`Any`/`Handle`）全部委托给内部 `router`。

```go
app := aifei.New(
    aifei.WithHandlers(myLogger, myRecover),       // 全局 Handler
    aifei.WithPlugin(cachePlugin, nacosPlugin),     // 生命周期插件
    aifei.WithOnStart(func() { /* 启动回调 */ }),
    aifei.WithOnStop(func()  { /* 停止回调 */ }),
)
app.Use(server.Logger(), server.Recover())          // 也可后续追加

app.GET("/ping", func(in aifei.Input) aifei.Output {
    return aifei.NewResult(0, "pong", nil)
})

api := app.Group("/api")                            // 路由分组
api.GET("/users/:id", getUser)
```

### 访问器（适配器用）

`Aifei` 没有把字段直接公开，而是提供一组访问器，供 [`http`](http.md)/[`server`](server.md) 适配器读取：

| 访问器 | 返回 |
|--------|------|
| `Router()` | 内部 `*Router`（适配器据此 `Lookup`） |
| `Handlers()` | 全局 `[]Handler` |
| `Plugins()` | 已注册 `[]Plugin` |
| `OnStartFunc()` / `OnStopFunc()` | 启停回调 |

### Config 与选项

```go
type Config struct {
    Handlers []Handler
    Plugins  []Plugin
    OnStart  func()
    OnStop   func()
}

func WithHandlers(h ...Handler) Option
func WithPlugin(p ...Plugin) Option
func WithOnStart(fn func()) Option
func WithOnStop(fn func()) Option
```

`Config` 只是一个普通结构体，存放那些不便用函数式选项表达的字段；应用代码用 `With*` 选项而非直接构造 `Config`。

---

## 5. Radix 树路由器

`Router` 为**每个 HTTP 方法维护一棵独立的 radix 树**（`trees map[string]*node`）。注册时合并公共前缀，查找复杂度约为路径长度。

```go
type Router struct { trees map[string]*node }

type node struct {
    path      string
    children  []*node
    handlers  []HandlerFunc
    wildChild bool
    param     bool      // :param 节点
    catchAll  bool      // *catchAll 节点
}
```

### 路径模式

| 模式 | 含义 | 示例 |
|------|------|------|
| `/users/list` | 精确静态匹配 | `GET /users/list` |
| `/users/:id` | 单段参数 `:id` | `GET /users/42` → `params["id"]="42"` |
| `/files/*path` | catch-all | `GET /files/a/b.txt` → `params["path"]="a/b.txt"` |

查找结果：`(handlers []HandlerFunc, params map[string]string, found bool)`。`params` 的键是模式里的参数名（`paramName` 从 `:id`/`*path` 中提取冒号后的部分）。

### 路由注册

```go
// Router 上的方法（Aifei 同名方法只是转发）
func (r *Router) GET(path string, handlers ...HandlerFunc)
func (r *Router) POST(path string, handlers ...HandlerFunc)
func (r *Router) PUT(path string, handlers ...HandlerFunc)
func (r *Router) DELETE(path string, handlers ...HandlerFunc)
func (r *Router) PATCH(path string, handlers ...HandlerFunc)
func (r *Router) HEAD(path string, handlers ...HandlerFunc)
func (r *Router) Any(path string, handlers ...HandlerFunc)  // 7 个方法全注册
func (r *Router) Handle(method, path string, handlers ...HandlerFunc)
func (r *Router) Lookup(method, path string) (handlers, params, found bool)
func (r *Router) Group(prefix string, handlers ...Handler) *RouterGroup
```

`Any` 覆盖 `GET/POST/PUT/DELETE/PATCH/HEAD/OPTIONS` 七个方法。`Handle` 是底层注册入口：规范化路径（空补 `/`、缺前导 `/` 补上）→ 取/建该方法的 radix 根节点 → `node.add`。

### RouterGroup：分组与共享 Handler

```go
type RouterGroup struct {
    prefix   string
    handlers []Handler   // 组级包装器
    router   *Router
}

func (g *RouterGroup) GET(path string, handlers ...HandlerFunc)
// ... POST/PUT/DELETE/Handle
func (g *RouterGroup) Group(prefix string, handlers ...Handler) *RouterGroup  // 子组嵌套
```

组注册时，`Handle` 取传入 handlers 的**最后一个** `HandlerFunc` 作最终处理，用组的 `handlers` 逐层包好，再注册到父路由的 `prefix+path` 上。子组继承父组的全部 `handlers` 并追加自己的——典型用法是版本前缀 + 鉴权 Handler：

```go
v1 := app.Group("/api/v1", authHandler)
v1.GET("/orders", listOrders)
admin := v1.Group("/admin", adminOnlyHandler)   // /api/v1/admin，带 auth + adminOnly
admin.GET("/stats", stats)
```

注意组的包装器是 `Handler`（包装 `HandlerFunc`），不是 `HandlerFunc` 本身——这让它能像全局中间件一样前置/后置/短路。

---

## 6. Handler 包装链：Go 的 AOP

Java Aifei 用 CGLIB/Javassist 做动态代理实现 AOP。Go 没有运行时动态代理，核心包用**函数包装**显式编排——这反而更易理解和测试。

```go
type HandlerFunc func(in Input) Output
type Handler    func(next HandlerFunc) HandlerFunc   // 包装器

// 从外到内构建链：handlers[0] 最外层
func ChainHandlers(handlers []Handler, final HandlerFunc) HandlerFunc
```

`ChainHandlers` 从右往左折叠：`final` 是最内层（真正的业务），`handlers[len-1]` 先包它，`handlers[0]` 最后包、成为最外层。调用结果时，请求从 `handlers[0]` 流向 `final`，每一层可以前置、后置或短路。

```go
// 一个最小的日志 Handler
func logHandler(next aifei.HandlerFunc) aifei.HandlerFunc {
    return func(in aifei.Input) aifei.Output {
        start := time.Now()
        out := next(in)
        fmt.Printf("%s → %d (%v)\n", in.Path(), out.Code(), time.Since(start))
        return out
    }
}
```

### 两层 AOP：Handler vs Interceptor

| 维度 | `Handler`（全局/组级） | `Interceptor`（方法级） |
|------|------------------------|------------------------|
| 作用域 | 整条请求管道 | 单个 service 方法 |
| 声明方 | 应用在 `Use`/`Group` 里装配 | service struct 自己声明 |
| 拿得到 method 名？ | 否（包装的是 `HandlerFunc`） | 是（`Intercept(method, in, invoke)`） |
| 典型用途 | 日志、recover、超时、CORS | 事务、权限、审计 |

[`server`](server.md) 提供了 `Logger`/`Recover`/`Timeout` 等现成 Handler，以及 `TxInterceptor` 这类 Interceptor。

---

## 7. Interceptor：方法级 AOP

`Interceptor` 是 service 自己声明的方法级切面。核心包只定义契约，反射装配在 [`server.Register`](server.md) 里完成。

```go
type Interceptor interface {
    Intercept(method string, in Input, invoke func() Output) Output
}

// 函数适配器
type InterceptorFunc func(method string, in Input, invoke func() Output) Output

// service 实现此接口，为指定方法挂一组 Interceptor
type MethodInterceptors interface {
    MethodInterceptors() map[string][]Interceptor
}
```

`invoke func() Output` 是"下一个 Interceptor 或真正的业务方法"。多个 Interceptor 时，`Register` 从右往左包：第 0 个最外层。典型用法是声明式事务：

```go
type OrderService struct{}

func (s *OrderService) MethodInterceptors() map[string][]aifei.Interceptor {
    return map[string][]aifei.Interceptor{
        "Create": {server.TxInterceptor(), auditInterceptor},
    }
}
```

方法名作为 `method` 参数传入，所以一个 Interceptor 实例可以按方法名分流策略。与全局 `Handler` 的关键区别：Interceptor 只作用于 `Register` 反射注册的方法，且能拿到方法名——这是"业务切面"而非"管道切面"。

---

## 8. Plugin 生命周期

```go
type Plugin interface {
    Start() error
    Stop()  error
}
```

`Plugin` 是框架扩展的统一挂载点（[cache](config.md)、[kafka](config.md)、[nacos](config.md)、[storage](config.md)、[dataisolate](data-isolate.md) 等插件都实现此接口）。核心包**只定义接口**，不动它们——`Start()`/`Stop()` 的实际调用在 [`server.Run`](server.md)：

```go
for _, p := range app.Plugins() {
    if err := p.Start(); err != nil { log.Fatalf(...) }  // 启动时
}
// ... 收到 SIGINT/SIGTERM 后
for _, p := range app.Plugins() { _ = p.Stop() }          // 按注册序 Stop
```

`OnStart`/`OnStop` 回调（通过 `WithOnStart`/`WithOnStop` 设置）在 Plugin 启停之间执行，用于应用自身的初始化/清理（顺序：plugin.Start → OnStart → 服务运行 → 信号 → server.Stop → OnStop → plugin.Stop）。

这种"接口 + 外部驱动"的模式让核心包不必知道任何具体插件，插件也不必依赖核心包之外的东西——只要实现两方法即可接入。

---

## 9. 模块结构

```
aifei/
├── aifei.go        # Aifei 实例 + New/Use/路由方法/访问器；Version 常量
├── input.go        # Input = Param + Meta 契约（参数读取 + 请求元数据）
├── output.go       # Output 三元组 + NewResult
├── handler.go      # HandlerFunc / Handler / ChainHandlers（AOP 编排）
├── router.go       # Router（radix 树）+ node + RouterGroup
├── interceptor.go  # Interceptor / InterceptorFunc / MethodInterceptors（方法级 AOP）
├── config.go       # Config + Option（WithHandlers/WithPlugin/WithOnStart/WithOnStop）
└── plugin.go       # Plugin 接口（Start/Stop）
```

共 8 个文件，约 620 行，零外部依赖。

---

## 10. 总结

1. **传输无关**：`Aifei` 不实现 `http.Handler`，只暴露 `Router()`/`Handlers()`/`Plugins()`，由 [`http`](http.md)/[`server`](server.md) 适配器驱动
2. **契约最小**：`Input` 拆成 `Param`+`Meta`，把 HTTP 概念挡在核心之外；`Output` 只是三元组
3. **显式 AOP**：`Handler` 包装链替代 Java 动态代理，从外到内折叠，无运行时反射代理
4. **两层切面**：`Handler` 管请求管道，`Interceptor` 管 service 方法（能拿到方法名）
5. **radix 路由**：每方法一棵树，支持 `:param` 与 `*catchAll`，`RouterGroup` 嵌套前缀 + 组级 Handler
6. **零依赖 + 函数式选项**：仅标准库；`New(WithHandlers(...), WithPlugin(...))` 一行装配

### 延伸阅读

- [框架总览](aifei-go.md) —— Aifei-Go 全貌与 Just Service 理念
- [http 适配器](http.md) —— `HttpContext`/`HttpHandler` 如何把 `net/http` 桥接到本包
- [server 启动层](server.md) —— `In`/`Out`/中间件/`Register`/`Run`/`TxInterceptor`
- [db](db.md)、[enjoy](enjoy.md)、[config](config.md) —— 核心库伙伴模块
