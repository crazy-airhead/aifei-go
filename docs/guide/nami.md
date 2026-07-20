# Aifei-Go nami：HTTP RPC 客户端框架

> **aifei 服务端的对偶客户端。**以 `Channel` 传输 + `Encoder`/`Decoder` 编解码 + `Filter` 拦截链 + `Upstream`/`Discovery` 服务发现的可插拔分层，把"调一次远端 HTTP 接口"做得像本地调用一样直接；零外部依赖。

---

## 1. 背景与定位

`aifei` 提供"Just Service"服务端能力，而一个完整的应用往往还需要**调别人的接口**——调第三方 REST API、调同集群其他微服务、调 Nacos 注册下来的实例。直接用 `net/http` 写 client 会遇到一堆重复工程：序列化、错误码处理、超时、header 传递、服务发现、负载均衡、统一日志/追踪。

`nami`（移植自 Java Solon [Nami](https://solon.noear.org/article/555)，本仓库 `./nami` 模块）就是这个对偶客户端。它的设计哲学与 aifei 一致——**扁平、零侵入、零外部依赖、可组合**：

| 定位 | 对应物 |
|------|--------|
| `aifei`（服务端框架） | Java Solon / Undertow |
| `nami`（客户端框架） | Java Solon Nami |
| `dami`（进程内总线） | DamiBus（参见 [dami-intro](dami.md)） |
| `kafka`（跨进程消息） | franz-go |

`nami` 仅依赖 Go 标准库（`net/http`、`encoding/json`、`reflect`），约 1,128 行；`channel/http` 子包负责真正的网络 IO，`coder/json` 子包负责 JSON 编解码，`util` 子包提供零配置的 `GetJSON[T]` 等泛型助手。插件层（如 [plugins/nacos](nacos.md)）可在不修改 `nami` 的前提下接入新的服务发现后端。

---

## 2. 总体架构

nami 的核心抽象有四层，自底向上叠加：

```
                ┌─────────────────────────────────────────┐
   应用代码 ───▶ │ Nami（fluent 客户端） / util.GetJSON[T]  │
                │  Call / CallOrThrow / CallAndBind        │
                └───────────────────┬─────────────────────┘
                                    │ Invocation
                ┌───────────────────┴─────────────────────┐
   可插拔拦截层 │ Filter 链（config + 全局 RegFilter）     │
                │ FilterFunc / 自定义 / actuator 终结       │
                └───────────────────┬─────────────────────┘
                                    │
            ┌───────────────────────┴───────────────────────┐
   传输层   │ Channel（channel/http 默认；按 scheme 注册）   │
            │   HttpChannel.Call(ctx *Context) *Result      │
            └───────────────────────┬───────────────────────┘
                                    │
            ┌───────────────────────┴───────────────────────┐
   编解码   │ Encoder（请求体）/ Decoder（响应体）           │
            │ coder/json 默认；按 Content-Type/Accept 匹配   │
            └───────────────────────────────────────────────┘

   服务发现 ─▶ Upstream（func() string）/ Discovery（GetServer(group,name)）
                 NewUpstreamFixed 轮询；plugins/nacos.NewNamiUpstream 桥接
```

核心类型一览：

| 类型 | 文件 | 职责 |
|------|------|------|
| `Channel` | `nami.go` | 传输层抽象（`Call(ctx *Context) (*Result, error)`） |
| `Encoder` / `Decoder` | `nami.go` | 请求体序列化 / 响应体反序列化 |
| `Filter` / `FilterFunc` | `nami.go` | 拦截链节点（`DoFilter(inv *Invocation) (*Result, error)`） |
| `Invocation` | `invocation.go` | Filter 链 + `Context` 的组合，链尾是 actuator |
| `Context` | `context.go` | 单次调用的全部输入（URL/Headers/Args/Body/Ctx） |
| `Config` | `config.go` | 每客户端配置（timeout/headers/filters/encoder/...） |
| `Nami` | `client.go` | 主客户端，fluent API |
| `Builder` / `ClientFactory` | `builder.go` / `client_factory.go` | 预配置客户端/工厂 |
| `Upstream` / `Discovery` | `upstream.go` / `discovery.go` | 服务发现 |
| `Result` | `result.go` | 响应包装（code/body/headers + Bind/AsAny） |

---

## 3. 关键 API

### 3.1 四个核心 interface

```go
// Channel 是 RPC 调用的传输通道。
type Channel interface {
    Call(ctx *Context) (*Result, error)
}

// Encoder 序列化请求体，Enctype() 决定 Content-Type。
type Encoder interface {
    Enctype() string
    BodyRequired() bool                  // 是否强制要有 body
    Encode(obj any) ([]byte, error)
    Pretreatment(ctx *Context)           // 发送前的最后一手调整（设 header 等）
}

// Decoder 反序列化响应，Enctype() 决定 Accept。
type Decoder interface {
    Enctype() string
    Decode(rst *Result, typ reflect.Type) (any, error)
    Pretreatment(ctx *Context)
}

// Filter 是请求拦截器，对应 Java Nami 的 Filter。
type Filter interface {
    DoFilter(inv *Invocation) (*Result, error)
}
type FilterFunc func(inv *Invocation) (*Result, error) // 函数适配器
```

四者通过**注册表**（`manager.go`）全局可插拔：`RegChannel(scheme, ch)`、`RegEncoder(e)`、`RegDecoder(d)`、`RegFilter(f)`，以及对应的 `Get*` / `*IfAbsent` 变体。第一个注册的 encoder/decoder 自动成为默认。`channel/http` 与 `coder/json` 都在 `init()` 里完成注册，import 即生效。

### 3.2 最小可用示例

```go
import (
    "github.com/crazy-airhead/aifei-go/nami"
    _ "github.com/crazy-airhead/aifei-go/nami/channel/http" // 注册 http/https channel
    _ "github.com/crazy-airhead/aifei-go/nami/coder/json"    // 注册 application/json coder
)

// 1. 直接 GET 拿 string
result, err := nami.New().
    URL("https://api.example.com/users/42").
    Action(nami.MethodGet).
    CallOrThrow(nil, nil, nil)
if err != nil { return err }
body, err := result.BodyAsString(), result.AssertSuccess()

// 2. POST + JSON 反序列化（headers/body/args 全部可省）
var resp struct{ ID int `json:"id"` }
err = nami.New().URL("https://api.example.com/users").
    Action(nami.MethodPost).
    CallAndBind(nil, nil, map[string]any{"name": "alice"}, &resp)
```

### 3.3 全局注册表

```go
// 自定义 decoder（如 protobuf），按 Content-Type 自动匹配
nami.RegDecoder(myProtobufDecoder)

// 全局拦截器：每个请求都过一遍（如注入 Authorization）
nami.RegFilter(nami.FilterFunc(func(inv *nami.Invocation) (*nami.Result, error) {
    if token := tokenFromCtx(inv.Ctx); token != "" {
        inv.Headers["Authorization"] = "Bearer " + token
    }
    return inv.Invoke() // 继续链
}))

// IfAbsent 变体：仅在没人注册时兜底
nami.RegChannelIfAbsent("http", myChannel)
```

`manager.go` 用一把 `sync.RWMutex` 守护，注册与查询并发安全。

---

## 4. 调用链路：Invocation + Filter

`Nami.CallOrThrow` 不直接调 `Channel.Call`——它先构造一个 `Invocation`，把 config 级 filters + 一个 actuator（actuator 本身也是 `Filter`，链尾执行真正的 `channel.Call`）拼成链，然后 `inv.Invoke()`。

```go
// invocation.go 核心结构
type Invocation struct {
    Context                    // 嵌入 Context（URL/Headers/Args/Body/Ctx）
    filters  []Filter          // config.filters ++ [actuator]
    index    int
    actuator Filter
}

func (inv *Invocation) Invoke() (*Result, error) {
    if inv.index >= len(inv.filters) { return nil, nil }
    f := inv.filters[inv.index]
    inv.index++
    return f.DoFilter(inv) // Filter 内部决定是否继续 inv.Invoke()
}
```

这个结构带来两个关键能力：

1. **链式 AOP**：日志、重试、熔断、tracing、注入 header 都写成 `Filter`，顺序就是注册顺序，每个 filter 决定 `inv.Invoke()` 之前/之后做什么，或者干脆短路。
2. **`Context` 透传**：`Invocation` 嵌入 `Context`，filter 直接读写 `inv.Headers` / `inv.Args` / `inv.Ctx`——后者是 `context.Context`，用于和上游请求的 cancel/超时/value 打通。

`Nami.Context(ctx)` 把调用方的 `context.Context` 放进 `Invocation.Ctx`，自定义 filter 可以读取其中的 request-scoped 值（trace id、租户 id、token 等）。

---

## 5. 配置：Config + Builder + ClientFactory

### 5.1 Config

每个 `Nami` 实例持有一份独立的 `Config`（`config.go`），所有字段都有 getter/setter：

| 字段 | 说明 |
|------|------|
| `Timeout` / `Heartbeat` | 秒级超时与心跳间隔 |
| `Encoder` / `Decoder` / `Channel` | 三大可插拔组件 |
| `Upstream` | 服务发现函数（`func() string`） |
| `URL` / `Name` / `Path` / `Group` | 直接 URL 或服务名 + 路径 + 分组 |
| `Filters` | 每客户端的 filter 列表（另有全局 `RegFilter`） |
| `Headers` | 默认请求头（如 `Authorization`、`Content-Type`） |

`Config.clone()` 做深拷贝（headers、filters 复制，encoder/decoder/channel 等无状态组件共享引用），这是 `ClientFactory.For` 隔离各客户端的基础。

### 5.2 Builder：链式构造单个客户端

```go
n := nami.NewBuilder().
    Timeout(10).
    HeaderSet("Authorization", "Bearer xxx").
    FilterAdd(nami.FilterFunc(func(inv *nami.Invocation) (*nami.Result, error) {
        log.Println("->", inv.URL)
        return inv.Invoke()
    })).
    Build()

var u User
// URLWithPath 把 base + path 拼起来；{fun} 占位符会被 path 替换
n.URLWithPath("https://api.example.com", "/users/42").
    Action(nami.MethodGet).
    CallAndBind(nil, nil, nil, &u)
```

每个方法都返回 `*Builder`，典型 fluent 风格。

### 5.3 ClientFactory：多端点共享配置

微服务场景下，一个服务往往有多个端点——除 path 不同外其他配置完全一致。`ClientFactory` 为这种场景而生：先在工厂上设好 `ServiceName`/`Upstream`/`Timeout`/`Filter`/`Header`，再按 path 各自 `For` 一个独立客户端。

```go
factory := nami.NewClientFactory().
    ServiceName("user-service").     // 或 .Name(...)，等价
    Timeout(5).
    Upstream(nacosPlugin.NewNamiUpstream("user-service")).
    HeaderSet("X-Source", "my-app")

users  := factory.For("/api/v1/users")  // path=/api/v1/users
orders := factory.For("/api/v1/orders") // path=/api/v1/orders，互不影响

// Factory 字段在 For 时被深拷贝；之后修改 users.Config() 不会污染 orders
```

`For` 内部调用 `Config.clone()`——service name/upstream/timeout/filters/headers 全部继承且隔离，避免了"每写一个端点都要重复一长串配置"的样板代码。

---

## 6. Channel：HTTP 传输实现

`channel/http`（`channel/http/http_channel.go`，197 行）是默认的传输层，实现 `nami.Channel`：

- **构造**：`http.New()`（默认 30s 超时的 `http.Client`）、`http.NewWithClient(c)`（自定义 client，可挂 transport 池/mTLS）
- **请求构造**（`Call` 内部分流）：
  - GET / 有 body 且有 args：args 拼到 query string
  - Content-Type 为 `multipart/form-data` 或 `application/x-www-form-urlencoded`：走表单模式
  - 其他：用 encoder 序列化 `ctx.BodyOrArgs()`（body 优先，否则 args map）作为请求体
- **响应包装**：状态码 + body bytes + 所有响应头 + charset 写入 `nami.NewResult`
- **Pretreatment**：嵌入 `nami.ChannelBase`，自动按 `Accept`/`Content-Type` header 兜底匹配 decoder/encoder

`init()` 时把同一个 `HttpChannel` 实例同时注册到 `http` 和 `https` 两个 scheme——所以 `Nami` 看到一个 `https://...` URL 时能自动找到正确的 channel。

```go
// 自定义 channel：嵌入 ChannelBase 复用 pretreatment
type myChannel struct {
    nami.ChannelBase
    // ... 你的状态
}
func (c *myChannel) Call(ctx *nami.Context) (*nami.Result, error) {
    c.Pretreatment(ctx) // 必须：按 header 兜底设 decoder/encoder
    // ... 你的传输逻辑
}
nami.RegChannel("my", &myChannel{}) // 走 "my://..." URL 触发
```

### Result：响应处理

`Result`（`result.go`）是响应的统一包装，三种取值方式：

| 方法 | 返回 | 用途 |
|------|------|------|
| `BodyAsString()` | `string` | 原始 body（首次调用后缓存并释放 bytes） |
| `AssertSuccess()` | `error` | 状态码 ≥ 400 时带 body 返回 error |
| `Bind(&val)` | `error` | 先 `AssertSuccess`，再 `json.Unmarshal` 到指针 |
| `AsAny()` | `any, error` | 反序列化成 `map[string]any` / `[]any` / 基础类型 |
| `HeaderGet(name)` | `string` | 取首个响应头 |

---

## 7. Encoder / Decoder：JSON 实现

`coder/json`（68 + 40 行）是默认编解码器，内部直接用 `encoding/json`。

**Encoder** 的 `Pretreatment` 是 no-op；**Decoder** 的 `Pretreatment` 会把 `Accept` header 设为 `application/json`（服务端据此返回正确格式）。Decoder 还有一个友好的兜底：当目标类型是 `string` 但响应不是 JSON 字面量（如纯文本/HTML），直接返回原始字符串而非报错。

自定义 encoder/decoder 的两种方式：

1. **全局注册**：`nami.RegEncoder(myEncoder)`——所有未显式指定 encoder 的请求都会按 `Content-Type` 匹配到。
2. **单客户端指定**：`builder.Encoder(myEncoder)` 或 `cfg.SetEncoder(myEncoder)`——只影响这一个客户端，不影响全局。

预定义的 Content-Type 常量（`content_types.go`）：`JSONValue`、`FormURLEncodedValue`、`FormDataValue`、`HessianValue`、`FuryValue`、`KryoValue`、`ProtobufValue`、`ABCValue`——后五种仅作为常量，对应 encoder 需自行实现注册（nami 本身零外部依赖，不内置这些序列化库）。

---

## 8. Upstream / Discovery：服务发现

`nami` 把"服务发现"抽象到极简：`Upstream func() string`——**每次调用前问一句"这次找谁？"**。这种"函数即发现"的设计让任何服务发现后端都能以零侵入方式接入。

### 8.1 内置：UpstreamFixed

`NewUpstreamFixed(servers []string)` 提供两种语义：

- 单实例：直接返回 stateless closure，无锁
- 多实例：`UpstreamFixed.Get` 轮询（round-robin），`sync.Mutex` 保护 index

```go
u := nami.NewUpstreamFixed([]string{
    "http://10.0.0.1:8080",
    "http://10.0.0.2:8080",
    "http://10.0.0.3:8080",
})
cfg.SetUpstream(u)
```

### 8.2 Discovery 接口 + NewDiscoveryUpstream

需要"按 group/name 查找"的复杂发现后端，实现 `Discovery` 接口：

```go
type Discovery interface {
    GetServer(group, name string) (string, error)
}

// 桥接到 Upstream
u := nami.NewDiscoveryUpstream(myDiscovery, "default", "user-service")
```

### 8.3 与 Nacos 集成

[plugins/nacos](nacos.md) 直接提供 `NewNamiUpstream(name)`——把 Nacos 服务发现实例列表转换成 `nami.Upstream`（内部做实例选择）。一行即可把 nami 接到 Nacos：

```go
import "github.com/crazy-airhead/aifei-go/plugins/nacos"

cfg.SetUpstream(nacosPlugin.NewNamiUpstream("user-service"))
```

URL 解析细节（`joinURI`）：base URL 支持 `sd:` 前缀（service discovery 标记，会被剥除）、`{fun}` 占位符（替换为 path）、`?query` 保留、`//` 折叠（但保护 `scheme://`）——兼容 Java Nami 的 URL 约定。

---

## 9. util：零配置泛型助手

最高层的封装是 `util` 子包（`util/util.go`，277 行）——**import 即用，无需注册任何组件**。它绕开全局注册表，在 `init()` 内自建 `httpchannel.New()` + `jsoncoder.NewDecoder()` + `jsoncoder.NewEncoder()` 直接挂到每个新建 Config 上，因此对其他代码的 registry 修改免疫（适合测试并行场景）。

| 方法 | 返回 | 用途 |
|------|------|------|
| `Get(url)` / `GetWith(url, params, headers)` | `string, error` | GET 拿原文 |
| `GetJSON[T](url)` / `GetJSONWith[T](...)` | `T, error` | GET + JSON 反序列化 |
| `Post(url, body)` / `PostWith(...)` | `*Result, error` | POST 原始结果 |
| `PostJSON[T](url, body)` / `PostJSONWith[T](...)` | `T, error` | POST + JSON 反序列化 |
| `GetBind/PostBind/RequestBind(url, body, &val)` | `error` | 非 generic、传指针 |
| `Request(method, url, body, params, headers)` | `string, error` | 任意 method |
| `RequestJSON[T](method, url, body, params, headers)` | `T, error` | 任意 method + 泛型 |

`SetBaseURL(url)` 让所有以 `/` 开头的 URL 视为相对路径，自动走 `Upstream` 机制拼到 base URL 上——典型 SDK 模式：

```go
util.SetBaseURL("http://api.example.com")
util.SetTimeout(10)

// 以下都是 http://api.example.com/...
users, _  := util.GetJSON[[]User]("/users")
order, _  := util.PostJSON[Order]("/orders", newOrderReq)
```

所有方法内部走 `CallOrThrow` + `AssertSuccess`，非 2xx 自动转 error。`GetJSON[T]` / `PostJSON[T]` / `RequestJSON[T]` 充分利用 Go 1.18+ 泛型，避免 `any` + 类型断言的样板代码。

---

## 10. 集成方式

### 10.1 与 aifei 服务端互调

nami 与 aifei 是同源设计——aifei 服务端返回的 `{code, msg, data}` 结构可直接用 nami 反序列化：

```go
type Resp struct {
    Code int    `json:"code"`
    Msg  string `json:"msg"`
    Data User   `json:"data"`
}

var resp Resp
_ = nami.New().
    URL("http://user-svc:8080/users/42").
    Action(nami.MethodGet).
    CallAndBind(nil, nil, nil, &resp)
// 或：util.GetJSON[Resp]("http://user-svc:8080/users/42")
```

### 10.2 完整客户端：ClientFactory + Nacos + Filter

```go
func main() {
    config.Init(os.Args)
    nacosPlugin, _ := nacos.NewPlugin(nil) // 启动 Nacos 客户端

    // 共享配置：所有端点都过这个 factory
    factory := nami.NewClientFactory().
        ServiceName("user-service").
        Upstream(nacosPlugin.NewNamiUpstream("user-service")).
        Timeout(5).
        FilterAdd(nami.FilterFunc(func(inv *nami.Invocation) (*nami.Result, error) {
            inv.Headers["X-Trace-Id"] = traceIDFromCtx(inv.Ctx)
            return inv.Invoke()
        }))

    // 在 service 里直接用
    users  := factory.For("/api/v1/users")
    orders := factory.For("/api/v1/orders")

    var u User
    _ = users.Action(nami.MethodGet).
        Context(in.Context()). // 透传 trace/cancel
        CallAndBind(nil, map[string]string{"id": "42"}, nil, &u)
}
```

### 10.3 单次"调一个 URL"场景

不想搭任何架子，直接 `util`：

```go
import "github.com/crazy-airhead/aifei-go/nami/util"

resp, err := util.PostJSON[CreateOrderResp](
    "https://api.shop.com/orders",
    map[string]any{"sku": "ABC", "qty": 2})
```

---

## 11. 模块结构

```
nami/
├── nami.go              # 核心 interface：Channel/Encoder/Decoder/Filter/Upstream（51 行）
├── client.go            # Nami 主客户端，fluent Call/CallOrThrow/CallAndBind（239 行）
├── builder.go           # Builder 链式构造器（100 行）
├── client_factory.go    # ClientFactory 多端点工厂（122 行）
├── config.go            # Config 配置 + clone（175 行）
├── context.go           # Context 单次调用上下文（47 行）
├── invocation.go        # Invocation Filter 链 + actuator（44 行）
├── channel_base.go      # ChannelBase 共享 Pretreatment（26 行）
├── manager.go           # 全局注册表（channel/encoder/decoder/filter）（126 行）
├── discovery.go         # Discovery 接口 + NewDiscoveryUpstream（19 行）
├── upstream.go          # UpstreamFixed 单/多实例轮询（37 行）
├── result.go            # Result 响应包装 + Bind/AsAny（114 行）
├── content_types.go     # Content-Type/Header/Method 常量（28 行）
├── channel/
│   └── http/
│       └── http_channel.go  # net/http 传输层实现（197 行）
├── coder/
│   └── json/
│       ├── json_encoder.go  # JSON Encoder（40 行）
│       └── json_decoder.go  # JSON Decoder（68 行）
└── util/
    └── util.go          # 零配置泛型助手 GetJSON[T]/PostJSON[T]/...（277 行）
```

合计约 1,128 行；`go.mod` 声明 `module github.com/crazy-airhead/aifei-go/nami`，零外部依赖（仅 Go 标准库）。

---

## 12. 总结

Aifei-Go 的 nami 围绕几个核心设计原则构建：

1. **分层解耦**：Channel 传输 / Encoder+Decoder 编解码 / Filter 拦截 / Upstream 发现——四层各司其职，互不耦合，每层都可独立替换
2. **注册表驱动**：channel/encoder/decoder/filter 全部走全局注册表，`init()` 自注册 + `*IfAbsent` 兜底；import 子包即扩展能力
3. **Filter 链 AOP**：日志、鉴权、tracing、重试、熔断都是 Filter；顺序就是注册顺序，`Invocation` 嵌入 `Context` 让 filter 直接读写请求
4. **函数即发现**：`Upstream func() string` 极简抽象，单实例/轮询/Nacos/Consul 都能零侵入接入；`Discovery` 接口为复杂后端预留
5. **fluent + Factory**：`Builder` 链式构造单客户端，`ClientFactory.For(path)` 深拷贝隔离多端点；模板 + 克隆消除样板代码
6. **零依赖 + 泛型**：纯 Go 标准库；`util.GetJSON[T]` / `PostJSON[T]` / `RequestJSON[T]` 用泛型避免 `any` 断言

这种设计使得 nami 既能作为"一行调 URL"的轻量 HTTP 客户端（`util` 路径），也能扩展成完整的企业级 RPC 客户端（自定义 Channel + Filter + Discovery），与 aifei 服务端形成完整的请求闭环。

### 延伸阅读

- [aifei-go 总览](aifei-go.md) — 整体模块地图
- [Nacos 插件](nacos.md) — `NewNamiUpstream` 把 Nacos 服务发现桥接为 `nami.Upstream`
- [dami 事件总线](dami.md) — 进程内 RPC 的另一种选择（不走网络）
- `docs/arch/dami/01-go-comparison.md` — nami / dami / kafka 的定位对比

