# Aifei-Go Server 启动层（./server）：生产级 Web 装配

> **`In` 读请求、`Out` 建响应、`Register` 把方法名变路由、`Run` 管启停。**本层在 [`http`](http.md) 之上补齐生产所需：`IoHandler` 多模式响应分派、内置中间件、struct 方法→RESTful 路由的命名约定、声明式事务。

---

## 1. 背景与定位

[`aifei`](core.md) 核心 + [`http`](http.md) 适配器已经能跑起来，但只够"JSON API 教学"。生产 Web 应用还需要：

- 把业务 `code` 映射到 HTTP 状态码（404/500/...）
- 模板渲染、文件下载、重定向、forward 等多种响应模式
- 通用的日志/恢复/超时/CORS/鉴权中间件
- struct 方法名→RESTful 路由的自动映射（Just Service 范式）
- 优雅启停（信号、plugin 生命周期、在途请求等待）
- 方法级声明式事务

这就是 `server` 包的职责。它对应 Java Aifei 的 `aifei-vip-arch` 模块（`IoHandler`/`Headers`/`FileSender` 等类型名直接沿用），是应用开发者最常打交道的层。

| 议题 | 决策 |
|------|------|
| 依赖 | [`aifei`](core.md) + [`http`](http.md) + [`db`](db.md) + [`enjoy`](enjoy.md) + [`log`](log.md) + 标准库 |
| HTTP 状态码 | 按 `Out.Code()` 映射（见 §6） |
| 路由来源 | struct 反射 + 命名约定（`Register`/`AutoRegisterServices`） |
| 事务 | `TxInterceptor` + `Out.ShouldRollback()` 驱动提交/回滚 |

---

## 2. 总体架构

```
                      *http.Request
                            │
                            ▼
                     server.IoHandler.ServeHTTP
                     (implements http.Handler)
                            │
            ┌───────────────┼───────────────────────┐
            ▼               ▼                       ▼
        NewIn(r)      Router.Lookup           全局 Handler 链
        (内嵌          (方法+路径 →             (app.Handlers()
        HttpContext)    handlers, params)       反向包装 final)
            │               │                       │
            └───────┬───────┴───────────────────────┘
                    ▼
            wrapped(in) → aifei.Output  （通常是 *server.Out）
                    │
                    ▼
            IoHandler.Handle  ── 按 Out 的渲染意图分派 ──┐
       ┌────────────────────────────────────────────────┤
       1. redirect  2. headers  3. view  4. file
       5. raw       6. json                              │
                                                          ▼
                                              http.ResponseWriter
```

关键类型一览：

| 类型 | 定义于 | 职责 |
|------|--------|------|
| `In` | `in.go` | `aifei.Input` 实现（内嵌 `HttpContext`）+ 文件上传 |
| `Out` | `out.go` | `aifei.Output` 构建器 + 渲染意图 |
| `IoHandler` | `io_handler.go` | `http.Handler` 适配 + 响应分派 |
| `Logger`/`Recover`/`Timeout` | `handler.go` | Handler 级中间件 |
| `CORS`/`BasicAuth`/`RequestID`/`StaticFile` | `handler.go` | HTTP 级中间件 |
| `Register` / `resolveRoute` | `register.go` | 方法名→路由 |
| `RegisterService` / `AutoRegisterServices` | `service.go` | init() 自注册 |
| `Run` / `Option` | `run.go` | 优雅启停 |
| `TxInterceptor` | `tx_interceptor.go` | 方法级事务 |

---

## 3. In：请求读取 + 文件上传

`In` 内嵌 `*aifeihttp.HttpContext`，把 [`http`](http.md) 里已验证的请求读取实现直接复用——`server` 不重复造轮子。

```go
type In struct { *aifeihttp.HttpContext }

var _ aifei.Input = (*In)(nil)

func NewIn(r *http.Request) *In
```

`aifei.Input`（`Param`+`Meta`）、`HTTPMeta`（`Method`/`RemoteIP`/`Cookie`）、`SetParams`/`SetContext` 都从 `HttpContext` 提升。`IoHandler` 为每个请求构造 `*In`，所以 `*In` 上的方法（如 `GetFile`）对 service 可见。

### 文件上传（镜像 Java `In.getUploadedFiles`）

```go
var ErrNoUpload = errors.New("no file uploaded")

func (in *In) GetFile(name string) (*UploadedFile, error)    // "" 取首个
func (in *In) GetFiles(name string) ([]*UploadedFile, error) // "" 取全部
```

`GetFile("")` 取请求里第一个上传字段；`GetFile("file")` 取指定字段。**副作用**：调用 `GetFile` 会触发 `r.FormFile` → `ParseMultipartForm`，顺带把 multipart 的文本字段填进 `r.Form`——所以之后 `in.GetStr("description")` 能取到表单文本字段。

`UploadedFile`（`upload.go`）封装单个上传，service 代码不碰 `*http.Request` 或 multipart 解析：

```go
type UploadedFile struct { ... }
func (f *UploadedFile) FieldName() string
func (f *UploadedFile) FileName() string              // 客户端原文件名
func (f *UploadedFile) Size() int64
func (f *UploadedFile) Extension() string             // 含 "."
func (f *UploadedFile) ContentType() string           // 头 → 扩展名推断 → octet-stream
func (f *UploadedFile) Open() (multipart.File, error) // 懒读取，调用方 Close
func (f *UploadedFile) Bytes() ([]byte, error)        // 小文件全量读
```

`defaultMultipartMemory = 32 << 20`（32 MiB）：解析上传时的内存阈值，超出落临时文件，与 `net/http` 的 `r.FormFile` 默认一致。

---

## 4. Out：响应构建器与渲染意图

`Out` 实现 `aifei.Output`，并在三元组之外**累积渲染意图**——`IoHandler.Handle` 据此决定**怎么**写响应。

```go
const ( CodeOK = 0; CodeFail = 500 )

type Out struct {
    code int; msg string; data interface{}; view string
    forwardPath string; headers *Headers
    fileSender func(*FileSender); rawContentType string; rawBody io.Reader; rawSize int64
    redirectURL string; redirectStatus int
}

var _ aifei.Output = (*Out)(nil)
```

### 静态构造器

| 构造器 | 用途 |
|--------|------|
| `Ok(msg ...string)` | 成功，默认 msg "ok" |
| `Fail(msg string, args ...interface{})` | 失败（code=500）；args 非空时按 `Sprintf` 格式化 |
| `FailWithCode(code int, msg string)` | 指定错误码 |
| `Of(data interface{})` | 成功 + data |
| `OfField(field, value)` | 成功 + 单键值对（data 变 map） |
| `Forward(path string)` | forward 到另一 action 路径（服务端再分派） |
| `Redirect(url string, status ...int)` | HTTP 重定向，默认 302 |
| `OfFile(fn func(*FileSender))` | 文件下载/导出 |
| `OfRaw(contentType string, data []byte)` | 内联字节（图片/PDF/SSE） |
| `OfRawReader(contentType string, body io.Reader)` | 流式内联字节 |

### 流式 setter（链式）

```go
out := server.Ok().SetMsg("done").SetData(result)
out.SetOk() / SetFail() / SetMsg(fmt, args...) / SetData(d) / Set(field, value) / Get(field)
out.SetView("order/detail.html")             // enjoy 模板
out.SetForward(path) / SetRedirect(url, ...) / SetFile(fn) / SetRaw(...) / SetRawReader(...)
out.SetRawSize(int64)                         // Reader 已知长度时设 Content-Length
out.SetHeaders(h *Headers)                    // 响应头/cookie
out.Clear()                                   // 全部重置
```

### 查询方法

```go
func (o *Out) IsOk() bool              // code == CodeOK
func (o *Out) ShouldRollback() bool    // code != CodeOK；TxInterceptor 据此回滚（见 §9）
```

### FileSender：下载/导出

`Out.OfFile(func(s *FileSender){...})` 在闭包里配置下载；`IoHandler` 负责实际 I/O，**绝不**把 `http.ResponseWriter` 交给业务代码。body 选择顺序：**Data > Reader > FileName**。

```go
func (s *FileSender) SetFileName(string)     // 磁盘路径（按 downloadBase 解析）
func (s *FileSender) SetSaveAsName(string)   // 客户端文件名（不可含路径分隔符）
func (s *FileSender) SetContentType(string)  // 覆盖 Content-Type
func (s *FileSender) SetData([]byte)         // 内存字节（如 excel 导出）
func (s *FileSender) SetReader(io.Reader)    // 流式内容
func (s *FileSender) SetSize(int64)          // 显式 Content-Length
```

Content-Disposition 用 `attachment; filename*=UTF-8''<encoded>`，支持中文文件名。MIME 按扩展名查（标准库 `mime` + 内置常见类型表，含 zip/xlsx/pdf/csv/png/mp4 等）。

### Headers：响应头/Cookie

```go
type Cookie struct {
    Name, Value string; MaxAge int; Path, Domain string
    HttpOnly, Secure bool; SameSite http.SameSite
}

h := &server.Headers{}
h.SetHeader("X-Trace", "abc")          // 替换
h.AddHeader("Set-Cookie", "a=1")       // 追加（多值）
h.AddCookie(server.Cookie{Name:"sid", Value:"x", HttpOnly:true})
h.RemoveCookie("old")                   // 过期删除（MaxAge=-1）
out.SetHeaders(h)
```

`Headers` 同样是传输无关的描述对象；`IoHandler` 在写 body 前调 `apply(w)` 把它落地到真实的 `http.ResponseWriter`。

---

## 5. 内置中间件（两层）

`server` 提供两层中间件——一层作用在 aifei Handler 链上（`Input`→`Output`），一层作用在 `http.Handler` 链上。

### 5.1 Handler 级（`aifei.Handler`）

| 函数 | 作用 |
|------|------|
| `Logger()` | 记录 method/path/code/耗时；method 通过 `HTTPMeta` 断言取（不在则打印 `-`） |
| `Recover()` | panic → `Fail("Internal Server Error")` + 打印 4KB 栈 |
| `Timeout(d time.Duration)` | 超时返回 `Fail("Gateway Timeout")`（业务在独立 goroutine 跑） |

装配方式：`app.Use(server.Logger(), server.Recover())` 或 `aifei.WithHandlers(...)`。

`Logger` 的设计细节：method 是 HTTP 专属概念，所以它对 `in` 做类型断言 `if h, ok := in.(aifeihttp.HTTPMeta); ok`，而不是要求 `aifei.Input` 提供 `Method()`——这正是 [http](http.md) 把 HTTP 概念外置到 `HTTPMeta` 的用意。

`Timeout` 用 `select` + `time.After`：业务在独立 goroutine 跑，超时返回 Gateway Timeout。超时后业务 goroutine 仍在运行（Go 无抢占式取消），真正的取消应靠 `in.Context()` 透传。

### 5.2 HTTP 级（`func(http.Handler) http.Handler`）

| 函数 | 作用 |
|------|------|
| `CORS(origin string)` | 设 CORS 头；OPTIONS 预检直接回 204 |
| `BasicAuth(check func(user, pass string) bool)` | Basic 认证；失败回 200 + `{code:500,msg:"Unauthorized"}` + `WWW-Authenticate` 头 |
| `RequestID()` | 生成 32 位 hex `X-Request-ID` 写回响应头 |
| `StaticFile(prefix, root string)` | `http.StripPrefix` + `http.FileServer`，静态文件服务 |

这些不能进 `app.Use`（签名不同，包装的是 `http.Handler` 而非 `HandlerFunc`），而是通过 `Run` 的选项装配（见 §8）。

---

## 6. IoHandler：响应分派

`IoHandler` 是 [`http.HttpHandler`](http.md) 的"加强版"：Input 用 `*In`，Output 按 `Out` 的渲染意图分派，HTTP 状态码按业务 code 映射。

### 分派优先级（`Handle`）

```
1. headers   ← 始终先 apply（后续模式的 Content-Type 可覆盖）
2. redirect  ← Location + status（无 body）
3. view      ← enjoy 模板渲染 → HTML
4. file      ← FileSender → 附件下载
5. raw       ← 内联字节（自定义 Content-Type）
6. json      ← {code,msg,data}（默认）
```

forward 在 `ServeHTTP` 里单独处理（不进 `Handle`）：循环重分派目标路径，上限 `maxForwards=8`，目标等于当前路径报错防环。

### HTTP 状态码映射（`httpStatus`）

| `Out.Code()` | HTTP status |
|--------------|-------------|
| `< 0` | 404 Not Found（如路由未命中的 `-1`） |
| `400–499` | 等于 code 本身（客户端错误透传） |
| `≥ 500` | 500（统一，避免泄露 internals） |
| 其他（0–399） | 200 |

这与 [`http.HttpHandler`](http.md) 的"恒 200"形成对比——生产场景需要正确的 4xx/5xx 才能用上监控、CDN、浏览器原生行为。

### IoHandler 选项

```go
func WithViewEngine(e *enjoy.Engine) IoOption            // 复用已建引擎
func WithEngineName(name string) IoOption                // 默认 "FICUS"
func WithBaseTemplatePath(p string) IoOption             // 视图路径前缀
func WithDownloadBase(p string) IoOption                 // FileSender 磁盘根
func WithDevMode(b bool) IoOption                        // enjoy dev 模式（关缓存）
```

视图引擎懒构造：首次渲染视图时才 `enjoy.NewEngine(name)`，避免不用模板的应用付出启动代价。详见 [enjoy](enjoy.md)。

---

## 7. Register：方法名→RESTful 路由

**Just Service 的核心机制**：一个 service struct 的导出方法，按命名约定自动映射成 HTTP 路由，无需注解或配置。`Register` 用反射完成翻译，`resolveRoute` 是两条规则的实现。

### 两条规则

**规则 1 · 默认动作（精确匹配）** —— 直接挂在 service 前缀上（或前缀 + `/list`）：

| 方法名 | HTTP 方法 | 路径 |
|--------|-----------|------|
| `Paginate` | GET | `/prefix` |
| `Create` | POST | `/prefix` |
| `List` | GET | `/prefix/list` |

**规则 2 · 动词前缀** —— 方法名以（且**长于**）`Get`/`Post`/`Put`/`Delete`/`Update` 开头；动词决定 HTTP 方法，剩余部分 camelCase→kebab-case 作路径后缀：

| 前缀 | HTTP 方法 |
|------|-----------|
| `Get` | GET |
| `Post` | POST |
| `Put` | PUT |
| `Delete` | DELETE |
| `Update` | PUT |

特殊转换：路径后缀中的字面子串 `"by-id"` → `:id`（仅替换一次）。所以 `ById` 结尾的方法天然变成 `:id` 路径参数。

**规则 3 · 其他** —— 不匹配上述任一规则的方法**被跳过**（不注册），安全保留为私有 helper。注意：**没有** `Save`/`Remove`/`Find` 的前缀规则；`Create` 只是默认动作的精确匹配。

### 方法名 → HTTP 方法 + 路径对照

设 `prefix = /api/user`：

| 方法名 | HTTP | 路径 | 说明 |
|--------|------|------|------|
| `Paginate` | GET | `/api/user` | 默认动作 |
| `Create` | POST | `/api/user` | 默认动作 |
| `List` | GET | `/api/user/list` | 默认动作，`/list` 后缀 |
| `GetById` | GET | `/api/user/:id` | `Get` 前缀，`ById`→`by-id`→`:id` |
| `UpdateById` | PUT | `/api/user/:id` | `Update`→PUT |
| `DeleteById` | DELETE | `/api/user/:id` | `Delete`→DELETE |
| `GetProfile` | GET | `/api/user/profile` | camelCase→kebab-case |
| `PostApprove` | POST | `/api/user/approve` | |
| `PutFreeze` | PUT | `/api/user/freeze` | `Put`→PUT |
| `GetByOrderNo` | GET | `/api/user/by-order-no` | `by-order-no`，无 `by-id` 子串，不改写 |
| `Save` / `Remove` / `Find` / `Helper` | — | — | 不匹配，**不路由** |

### camelToPath

CamelCase → kebab-case：每个大写字母前加 `-`，然后转小写。`GetProfile` 的剩余 `Profile` → `profile`；`ByOrderNo` → `by-order-no`。`ById` → `by-id` → `:id`。

### 装配

```go
func Register(router *aifei.Router, prefix string, service interface{}, handlers ...aifei.Handler)
```

`Register` 反射遍历 `service` 的导出方法，对每个方法：

1. `resolveRoute(name)` 算出 `(httpMethod, pathSuffix, ok)`；`ok=false` 跳过
2. `pathSuffix` 里的 `"by-id"` → `":id"`
3. 拼接 `prefix + "/" + pathSuffix`（无后缀则直接 `prefix`）
4. 构造 handler：反射 `Call` 调用方法；若 service 实现 `MethodInterceptors`，按方法名套 Interceptor 链；再用传入的 `handlers` 反向包装
5. `router.Handle(httpMethod, path, handler)`

`prefix` 右侧的 `/` 会被 `TrimRight` 去掉，避免 `//`。

### 搭配 Interceptor

service 实现 `aifei.MethodInterceptors` 即可声明每个方法的切面链（见 [aifei 核心](core.md) §7）。`Register` 会把 map 里该方法的 `[]Interceptor` 从右往左包在反射调用外：

```go
func (s *UserService) MethodInterceptors() map[string][]aifei.Interceptor {
    return map[string][]aifei.Interceptor{
        "Create": {server.TxInterceptor()},   // Create 方法自动包事务
    }
}
```

---

## 8. AutoRegisterServices + Run：自注册与优雅启停

### 8.1 init() 自注册

生成器（[`tools/generator`](generator.md)）产出的 `service.go` 在 `init()` 里调用 `RegisterService`，把自己登记进全局 registry：

```go
// 生成器产出（勿手改）
func init() {
    server.RegisterService("/api/user", &UserService{})
}
```

应用启动时一行扫完所有 service：

```go
func AutoRegisterServices(app *aifei.Aifei, handlers ...aifei.Handler)
```

它遍历全局 `serviceRegistry`，对每条调 `Register(app.Router(), reg.Prefix, reg.Service, handlers...)`。可选的 `handlers` 会作用到所有自动注册的路由（组级中间件）。`ServiceRegistrations()` 可读取当前 registry（测试/审查用）。

### 8.2 Run：完整的启动流程

```go
func Run(app *aifei.Aifei, addr string, opts ...Option)
```

`Run` 是阻塞调用，执行序列：

1. 装配选项到 `options`
2. 构造 `IoHandler`（带 `ioOptions`）作为核心 `http.Handler`
3. 可选 `rootWrapper` 包核心（用于短路特定路径，如原始文件端点）
4. 从右往左套 HTTP 级中间件（`httpHandlers`）
5. `NewDefaultServer(addr)`
6. **启动**：遍历 `app.Plugins()` 调 `Start()`（失败 `log.Fatal`）；调 `OnStart` 回调
7. goroutine 里 `srv.Start(h)`；主 goroutine 等 `SIGINT`/`SIGTERM`
8. **停机**：`srv.Stop()`（5s 优雅）；调 `OnStop`；**逆序**遍历 plugins 调 `Stop()`（与启动顺序相反，后启动的先停）

### 8.3 Run 选项

| 选项 | 作用 |
|------|------|
| `WithCORS(origin)` | 装 CORS HTTP 中间件 |
| `WithBasicAuth(check)` | 装 Basic Auth |
| `WithRequestID()` | 装 RequestID |
| `WithHTTPHandler(m)` | 任意 `func(http.Handler) http.Handler` |
| `WithRootHandler(wrap)` | 包核心 aifei handler（短路特定路径） |
| `WithIoOptions(opts ...)` | 配置 IoHandler（视图引擎、下载根、dev 模式） |
| `WithMaxHeaderBytes(n)` | 限制请求头大小（字节）；0 = net/http 默认 1MB，常用收紧值 `16<<10` |

### 8.4 完整 main 模板

```go
func main() {
    _ = db.Init("mysql", dsn)                       // [db](db.md)
    if err := config.Init(os.Args); err != nil { log.Fatal(err) }  // [config](config.md)

    app := aifei.New(
        aifei.WithPlugin(cachePlugin, nacosPlugin),  // [plugins]
        aifei.WithHandlers(server.Logger(), server.Recover()),
    )

    server.AutoRegisterServices(app)                 // 扫 init() 自注册

    server.Run(app, ":8080",
        server.WithRequestID(),
        server.WithCORS("*"),
        server.WithIoOptions(
            server.WithBaseTemplatePath("views"),
            server.WithDevMode(true),
        ),
    )
}
```

---

## 9. TxInterceptor：方法级声明式事务

```go
func TxInterceptor() aifei.Interceptor
```

挂到 service 方法上（通过 `MethodInterceptors`），自动把方法包进数据库事务。机制：

1. `db.TransactionCtx(in.Context(), func(txCtx context.Context) error { ... })` 开事务
2. 通过 `ctxSetter` 接口（`*http.HttpContext`/`*In` 都满足）把 `txCtx` 注入回 `in`——`SetContext(txCtx)`
3. `invoke()` 调用业务方法；方法内用 `db.Ctx(in.Context())`（或 `db.InsertCtx` 等 ctx 感知入口）自动加入事务
4. 根据返回的 `Output` 决定提交/回滚
5. 事务报错（非 rollback）→ `Fail("transaction error: %s", err)`

### 回滚决策（`shouldRollbackOutput`）

```go
if rd, ok := out.(db.RollbackDecision); ok {
    return rd.ShouldRollback()        // 优先：自定义 Output 的决策
}
return out.Code() != 0                 // 兜底：code != 0 即回滚
```

`server.Out` 实现了 `ShouldRollback()`（`code != CodeOK`），所以直接返回 `Fail(...)` 的方法会被自动回滚。任何实现 `db.RollbackDecision` 的 Output 都能驱动决策，不强依赖 `*Out`。

### 优雅降级

`setInContext` 检查 `in` 是否满足 `ctxSetter`——不满足（如测试 fixture）就不替换 context：事务照常提交/回滚，只是方法不会"自动"加入（除非自己透传 ctx）。`rollbackError` 是个哨兵错误类型，`TransactionCtx` 返回它时 `TxInterceptor` 不当作真错误、直接返回原 `out`。

---

## 10. 模块结构

```
server/
├── in.go             # In（内嵌 HttpContext）+ GetFile/GetFiles 文件上传
├── upload.go         # UploadedFile 封装（FieldName/Size/ContentType/Open/Bytes）
├── out.go            # Out 构建器（静态构造 + 流式 setter + 渲染意图）
├── headers.go        # Headers（响应头/cookie 描述）+ Cookie
├── file_sender.go    # FileSender（下载/导出，Data>Reader>FileName）
├── io_handler.go     # IoHandler（http.Handler + 响应分派 + httpStatus 映射）
├── handler.go        # 两层中间件：Logger/Recover/Timeout + CORS/BasicAuth/RequestID/StaticFile
├── register.go       # Register + resolveRoute（两条路由规则 + camelToPath + ById→:id）
├── service.go        # RegisterService/AutoRegisterServices（init() 自注册）
├── run.go            # Run（优雅启停 + 选项 + plugin 生命周期）
└── tx_interceptor.go # TxInterceptor（方法级声明式事务）
```

共 11 个文件，约 1,580 行；依赖 [`aifei`](core.md)/[`http`](http.md)/[`db`](db.md)/[`enjoy`](enjoy.md)/[`log`](log.md) + 标准库。

---

## 11. 总结

1. **复用而非重造**：`In` 内嵌 `HttpContext`，`Out` 的三元组对接 `aifei.Output`，server 是薄装配层
2. **响应多模式**：`Out` 累积渲染意图，`IoHandler.Handle` 按固定优先级分派（redirect→view→file→raw→json）
3. **HTTP 状态码映射**：业务 `code` → HTTP status（<0→404, 4xx→自身, ≥500→500, 其他→200）
4. **Just Service**：`Register` 两条规则（默认动作精确匹配 + 动词前缀 camelCase→kebab-case）+ `ById→:id`，方法名即路由
5. **init() 自注册**：生成器产出的 `service.go` 自登记，`AutoRegisterServices` 一行装配
6. **两层中间件**：Handler 级（`Logger`/`Recover`/`Timeout`）走 `app.Use`；HTTP 级（`CORS`/`BasicAuth`/`RequestID`/`StaticFile`）走 `Run` 选项
7. **声明式事务**：`TxInterceptor` 靠 `SetContext` 注入事务 ctx，`Out.ShouldRollback()` 驱动回滚，业务代码零事务感知

### 延伸阅读

- [aifei 核心](core.md) —— `Input`/`Output`/`Handler`/`Interceptor` 契约
- [http 适配器](http.md) —— `HttpContext`/`HttpHandler` 的薄桥接
- [db](db.md) —— `Dao`/`Row`、`TransactionCtx`、ctx 感知入口
- [enjoy](enjoy.md) —— 模板引擎（`Out.SetView` / `IoHandler` 视图分派）
- [config](config.md) —— 配置加载
- [generator](generator.md) —— 生成 `service.go` / `base.go` / `dao.go`
- [dataisolate](data-isolate.md) —— 数据隔离插件（`Middleware()` + hook）
- [server 定制指南](server-customization.md) —— 应用自带 server 包的定制（多模式响应 / JWT / RPC 鉴权透传 / 定制路由）
