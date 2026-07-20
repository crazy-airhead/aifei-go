# Phase 5: 高级特性

> 目标：内置 Handler 包装器、服务注册、优雅关闭、静态文件服务

## 1. 内置 Handler Wrapper（`server/` 包）

所有内置包装器在 `server/middleware.go` 中实现，分为两类：

### HandlerFunc 级包装器（返回 `aifei.Handler`）

```go
package server

import "github.com/crazy-airhead/aifei-go"

// Logger — 请求日志
func Logger() aifei.Handler

// Recover — panic 恢复
func Recover() aifei.Handler

// Timeout — 超时控制
func Timeout(d time.Duration) aifei.Handler
```

**使用：**
```go
app.Use(server.Logger(), server.Recover())
```

### HTTP 级包装器（返回 `func(http.Handler) http.Handler`）

```go
// CORS — 跨域
func CORS(origin string) func(http.Handler) http.Handler

// BasicAuth — 基础认证
func BasicAuth(check func(user, pass string) bool) func(http.Handler) http.Handler

// RequestID — 请求 ID
func RequestID() func(http.Handler) http.Handler

// StaticFile — 静态文件服务
func StaticFile(prefix, root string) http.Handler
```

**使用（通过 server.Run 选项传入）：**
```go
server.Run(app, ":8080",
    server.WithCORS("*"),
    server.WithBasicAuth(func(u, p string) bool { return u == "admin" && p == "123" }),
    server.WithRequestID(),
)
```

---

## 2. 服务注册（`server/service.go`）

支持集中式服务注册和自动注册：

```go
package server

type ServiceRegistration struct {
    Prefix  string
    Service interface{}
}

// RegisterService 注册服务（通常在 init() 中调用）
func RegisterService(prefix string, svc interface{})

// ServiceRegistrations 返回所有已注册服务
func ServiceRegistrations() []ServiceRegistration

// AutoRegisterServices 将所有已注册服务批量注册到 Aifei 实例
func AutoRegisterServices(app *aifei.Aifei, handlers ...aifei.Handler)
```

**典型用法（在生成的 service.go 中）：**

```go
// internal/user/service.go
package user

func init() {
    server.RegisterService("/user", &Service{})
}

// 在 main.go 中：
func main() {
    app := aifei.New()
    server.AutoRegisterServices(app) // 批量注册所有 init() 注册的服务
    server.Run(app, ":8080")
}
```

---

## 3. Struct 注册路由详解

对应 Java 版 `@Path` 注解 + 包扫描注册。

`Register()` 通过反射扫描 struct 方法，按命名约定自动生成路由：

```go
// router.go
func (r *Router) Register(prefix string, service interface{}, handlers ...Handler)
```

**方法签名要求：** `func(in aifei.Input) aifei.Output`

**HTTP 方法映射：**

| 方法名前缀 | HTTP 方法 |
|-----------|----------|
| `List*` / `Get*` | GET |
| `Post*` / `Save*` / `Create*` | POST |
| `Put*` / `Update*` | PUT |
| `Delete*` / `Remove*` | DELETE |
| 其他 | POST（默认） |

**路径转换规则：**
- 方法名转为小写路径：`List` → `/list`，`Create` → `/create`
- `ById` 后缀 → `/:id` 路径参数：`GetById` → `GET /:id`
- 复合路径：`GetByNameAndAge` → `GET /:name/:age`

---

## 4. 优雅关闭（`server/run.go`）

```go
// server.Run 启动 HTTP 服务并处理优雅关闭
func Run(app *aifei.Aifei, addr string, opts ...Option)
```

**Run() 内部流程：**
1. 创建 `http.Server`
2. 注册信号监听 (`SIGINT` / `SIGTERM`)
3. 启动所有 Plugin (`plugin.Start()`)
4. 调用 `OnStart` 回调
5. 启动 HTTP 服务 (`ListenAndServe`)
6. 收到信号后：
   - 调用 `OnStop` 回调
   - 停止所有 Plugin (`plugin.Stop()`)
   - 5 秒超时优雅关闭 HTTP 服务

**Run 选项：**

```go
func WithCORS(origin string) Option
func WithBasicAuth(check func(user, pass string) bool) Option
func WithRequestID() Option
func WithHTTPHandler(m func(http.Handler) http.Handler) Option
```

---

## 5. 事务拦截器（`server/tx_interceptor.go`）

方法级事务自动管理：

```go
// TxInterceptor 返回一个 Interceptor，自动开启/提交/回滚事务
func TxInterceptor() aifei.Interceptor
```

**使用：**
```go
func (s *Service) MethodInterceptors() map[string][]aifei.Interceptor {
    return map[string][]aifei.Interceptor{
        "Create": {server.TxInterceptor()},
    }
}
```

`TxInterceptor` 在方法返回 `ShouldRollback() == true` 时自动回滚。

---

## 6. 待实现特性

以下 Java Aifei 特性在 Go 版中尚未实现：

- **SQLBuilder 编程式链式 API** — 可通过 Dao 链式调用替代
- **Aifei.Static/StaticFile/StaticFS 方法** — 当前通过 `server.StaticFile()` 或 `http.FileServer` 实现
- **参数注入框架** — Go 通过 `Input` 接口方法直接获取参数，简化了注入逻辑
