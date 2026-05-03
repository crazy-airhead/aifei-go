# Phase 1: 核心框架

> 目标：实现 Aifei Go 的核心 HTTP 框架，包含 Context、Router、Handler、Server、Middleware

## 1. 项目初始化

### 文件: `go.mod`

```
module github.com/aifei/aifei

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
    config   *Config
    server   *http.Server
    router   *Router
    plugins  []Plugin
}

// New 创建 Aifei 实例
func New() *Aifei

// Start 启动 HTTP 服务
func (a *Aifei) Start(addr string) error

// Stop 优雅关闭
func (a *Aifei) Stop() error

// Run 启动并监听信号 (阻塞)
func (a *Aifei) Run(addr string)
```

---

## 2. Context (合并 Input + Output)

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

**Go 版设计：**

```go
package aifei

type Context struct {
    Request  *http.Request
    Writer   http.ResponseWriter
    pathPara []string       // 路径参数
    params   url.Values     // 查询参数
    form     url.Values     // 表单参数
    body     []byte         // 请求体 (懒加载)
    bodyRead bool
    status   int
    handlers []HandlerFunc  // 当前 handler 链
    index    int            // 当前执行到的 handler 索引
}

// ---- 请求参数 (对应 Input) ----

func (c *Context) Has(name string) bool
func (c *Context) GetStr(key string) string
func (c *Context) GetStrDefault(key, def string) string
func (c *Context) GetInt(key string) int
func (c *Context) GetIntDefault(key string, def int) int
func (c *Context) GetInt64(key string) int64
func (c *Context) GetInt64Default(key string, def int64) int64
func (c *Context) GetFloat64(key string) float64
func (c *Context) GetFloat64Default(key string, def float64) float64
func (c *Context) GetBool(key string) bool
func (c *Context) GetBoolDefault(key string, def bool) bool
func (c *Context) GetBean(obj interface{}) error            // JSON body → struct
func (c *Context) GetMap(key string) map[string]interface{}
func (c *Context) PathPara(index int) string                // 路径参数
func (c *Context) HasPara(index int) bool
func (c *Context) Method() string
func (c *Context) Path() string
func (c *Context) RemoteIP() string
func (c *Context) Body() []byte                             // 原始请求体

// ---- 响应输出 (对应 Output) ----

func (c *Context) Status(code int)
func (c *Context) Header(key, value string)
func (c *Context) Json(data interface{})
func (c *Context) JsonOK(data interface{})
func (c *Context) JsonFail(msg string)
func (c *Context) Text(format string, args ...interface{})
func (c *Context) Html(html string)
func (c *Context) Redirect(url string)

// ---- 链式控制 ----

func (c *Context) Next()     // 调用下一个 handler
func (c *Context) Abort()    // 终止链
```

---

## 3. Handler + Middleware (替代 Java AOP + 责任链)

对应 Java 的 `cn.aifei.core.Handler` + `cn.aifei.aop.Interceptor`。

**Java 版关键设计：**
- `Handler<I,O>` 抽象类，有 `next` 字段支持链式调用
- `Interceptor.intercept(Invocation inv)` 拦截器接口
- `@Before` 注解配置拦截器
- `@Clear` 注解清除拦截器
- `Invocation` 封装调用上下文

**Go 版设计 — 统一为 Middleware 模式：**

```go
package aifei

// HandlerFunc 处理函数
type HandlerFunc func(c *Context)

// Middleware 中间件 (完全替代 Java Interceptor + AOP)
type Middleware func(next HandlerFunc) HandlerFunc

// ChainMiddleware 组装中间件链
// 中间件按添加顺序执行，最后一个为最终 handler
func ChainMiddleware(middlewares []Middleware, final HandlerFunc) HandlerFunc
```

**Java 拦截器映射到 Go Middleware：**

Java:
```java
public class AuthInterceptor implements Interceptor {
    public void intercept(Invocation inv) {
        String token = inv.getInput().getStr("token");
        if (token == null) {
            inv.getOutput().json("未登录");
            return;
        }
        inv.invoke();  // 继续执行
    }
}
```

Go:
```go
func AuthMiddleware(next aifei.HandlerFunc) aifei.HandlerFunc {
    return func(c *aifei.Context) {
        token := c.GetStr("token")
        if token == "" {
            c.JsonFail("未登录")
            c.Abort()
            return
        }
        next(c)  // 继续执行
    }
}
```

---

## 4. Router (路由系统)

对应 Java 的 `cn.aifei.router.Router` + `cn.aifei.router.Action` + `cn.aifei.router.ActionGroup`。

**Java 版关键设计：**
- `Router.mapping` — HashMap<String, Object> 存储路由
- `Router.scan(basePackage)` — 自动包扫描注册路由
- `Router.add(path, target)` — 手动添加路由
- `Router.getAction(path, input)` — 路由匹配
- `Action` — 封装路由元数据 (actionPath, targetClass, method, interceptors, arguments)
- `ActionGroup` — 同一路径下多个 Action (方法重载)
- `@Path` 注解 — 配置类级别和方法级别路径
- `RouterKit` — 单例工具

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
func (r *Router) Any(path string, handlers ...HandlerFunc)
func (r *Router) Handle(method, path string, handlers ...HandlerFunc)

// 路由组 (支持前缀和中间件)
func (r *Router) Group(prefix string, middlewares ...Middleware) *RouterGroup

// struct 注册 (保留 Java @Path 风格的简洁性)
// 将 struct 的公开方法自动注册为路由
func (r *Router) Register(prefix string, service interface{}, middlewares ...Middleware)

// 路由匹配
func (r *Router) Lookup(method, path string) (handlers []HandlerFunc, params map[string]string, found bool)

// Radix tree node
type node struct {
    path      string
    children  []*node
    handlers  []HandlerFunc
    wildChild bool
    param     bool       // 是否为 :param 路径参数
    catchAll  bool       // 是否为 *catchAll
}
```

**RouterGroup 设计：**

```go
type RouterGroup struct {
    prefix      string
    middlewares []Middleware
    router      *Router
}

func (g *RouterGroup) GET(path string, handlers ...HandlerFunc)
func (g *RouterGroup) POST(path string, handlers ...HandlerFunc)
func (g *RouterGroup) Group(prefix string, middlewares ...Middleware) *RouterGroup
```

**Register 示例 (替代 Java @Path 注解)：**

```go
type UserService struct{}

func (s *UserService) List(c *Context)   { ... }
func (s *UserService) Save(c *Context)   { ... }
func (s *UserService) Delete(c *Context) { ... }

// 自动注册:
//   GET  /api/user/list   → UserService.List
//   POST /api/user/save   → UserService.Save
//   POST /api/user/delete → UserService.Delete
router.Register("/api/user", &UserService{})
```

---

## 5. Config (配置系统)

对应 Java 的 `cn.aifei.config.AifeiConfig` + `Settings` + `Plugins`。

**Java 版关键设计：**
```java
public interface AifeiConfig<I extends Input, O extends Output> {
    void config(Settings<I, O> settings);    // 配置服务器、中间件
    void config(Routes routes);               // 配置路由
    void config(Plugins plugins);             // 配置插件
    default void onStart() {}                 // 启动回调
    default void onStop() {}                  // 停止回调
}
```

**Go 版设计 — Functional Options 模式：**

```go
package aifei

type Config struct {
    Middlewares []Middleware
    Plugins     []Plugin
    OnStart     func()
    OnStop      func()
}

type Option func(*Aifei)

func WithMiddleware(m ...Middleware) Option
func WithPlugin(p ...Plugin) Option
func WithOnStart(fn func()) Option
func WithOnStop(fn func()) Option
```

---

## 6. Plugin (插件系统)

对应 Java 的 `cn.aifei.plugin.Plugin`。

```go
package aifei

type Plugin interface {
    Start() error
    Stop() error
}
```

---

## 7. Server + Dispatcher

对应 Java 的 `cn.aifei.server.Server` + `Dispatcher`。

**Java 版：**
```java
interface Server<P1, P2> {
    void init(Dispatcher<P1, P2, ?, ?> dispatcher);
    void start();
    void stop();
}
interface Dispatcher<P1, P2, I extends Input, O extends Output> {
    void init(Handler<I, O> handler);
    void dispatch(P1 p1, P2 p2);
}
```

**Go 版 — 简化为直接使用 net/http：**

```go
package aifei

func (a *Aifei) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    // 1. 创建 Context
    c := &Context{
        Request: r,
        Writer:  w,
    }

    // 2. 路由匹配
    handlers, params, found := a.router.Lookup(r.Method, r.URL.Path)
    if !found {
        c.Status(404).Text("Not Found")
        return
    }

    // 3. 解析路径参数
    c.pathPara = extractPathParams(params)

    // 4. 执行 handler 链
    c.handlers = handlers
    c.index = -1
    c.Next()
}
```

---

## 8. 文件依赖关系

```
aifei.go
  ├── config.go      (Config, Option)
  ├── context.go     (Context)
  ├── handler.go     (HandlerFunc, Middleware)
  ├── router.go      (Router, RouterGroup, node)
  ├── action.go      (不需要独立文件，路由元数据在 router 中)
  ├── plugin.go      (Plugin)
  ├── server.go      (ServeHTTP 实现)
  └── util.go        (StrUtil, Prop)
```

---

## 9. 完整使用示例

```go
package main

import (
    "github.com/aifei/aifei"
    "github.com/aifei/aifei/db"
)

func main() {
    app := aifei.New()

    // 全局中间件
    app.Use(Logger(), Recover())

    // 路由注册
    app.GET("/api/user/list", UserList)
    app.POST("/api/user/save", UserSave)
    app.POST("/api/user/delete", UserDelete)

    // 路由组
    api := app.Group("/api/v2", AuthMiddleware())
    api.GET("/user/list", UserList)

    // struct 注册 (Java @Path 风格)
    app.Register("/api/order", &OrderService{})

    // 启动
    app.Run(":8080")
}

func UserList(c *aifei.Context) {
    page, err := db.Use().SQL("select * from user where 1=1").Paginate(1, 10)
    if err != nil {
        c.JsonFail(err.Error())
        return
    }
    c.Json(page)
}
```
