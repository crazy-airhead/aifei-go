# Nami 迁移到 Go 的契约设计（nami，as-built）

> **状态**：已实现并经 `_test/nami_test` 黑盒测试覆盖（~80 用例）。本文为**回补设计文档**，忠实记录既有代码的契约与决策；对照分析见 [`01-java-comparison.md`](01-java-comparison.md)。
> 模块：`./nami`（独立模块，零外部依赖，仅标准库），使用指南见 [`../../guide/nami.md`](../../guide/nami.md)。

---

## 0. TL;DR（设计摘要）

1. **三组正交抽象 + 注册表**：`Channel`（传输）/ `Encoder`+`Decoder`（序列化）/ `Filter`（拦截）全部接口化，经 `manager.go` 全局注册表按 **scheme / 内容类型** 解析；import 子包即扩展。
2. **无动态代理 → 流式客户端**：`Nami` 结构体 fluent API（`Action/URL/CallAndBind/...`），`Config` 持全部配置，`Invocation` 以 index 游标驱动过滤器链。
3. **函数即发现**：`Upstream func() string` 一行接入任意寻址（固定地址 / 轮询 / Nacos）；`Discovery` 接口为有状态发现后端预留。
4. **三级易用性阶梯**：核心 `Nami`（全控制）→ `Builder`/`ClientFactory`（模板复用）→ `util`（`GetJSON[T]` 一行调用）。
5. 已知缺口集中在生态铺量（全局 Filter 接线、LB 策略、Socket.D 通道），登记在 §7。

---

## 1. 背景与目标

### 1.1 为什么移植

aifei-go 是 [Aifei Java](https://github.com/jfinal/aifei)（Solon 生态）的 Go 移植。服务端（aifei/http/server）落地后，微服务闭环缺客户端半边；Solon 体系里这个角色由 Nami 承担——传输、序列化、拦截、发现四层正交抽象恰好是 Go 里也成立的形状，故按原架构移植。

### 1.2 移植目标（优先级排序）

1. **保形**：Channel / Encoder / Decoder / Filter / Upstream / Discovery / Config / Invocation / Result 与 Java 同名同职责，Solon 使用者零学习成本。
2. **Go 味**：显式 Builder 替代注解；`error` 返回替代异常；泛型助手替代反射擦除。
3. **零依赖**：核心与内置 http/json coder 仅标准库。
4. **可扩展**：新通道/编码/发现各是一个子包 + `init()` 注册，不改核心。

### 1.3 非目标

- 不做接口动态代理（语言不可为，见 01 文档 §2）；
- 不内置熔断/重试/限流（治理能力规划在 [`../microservice.md`](../microservice.md)，以 Filter 形态接入）；
- 不做服务端（那是 [aifei](../../guide/core.md) 的事）；
- Socket.D 通道暂不移植。

---

## 2. 总体架构

### 2.1 分层与注册表

```
                ┌──────────── util（GetJSON[T] 一行调用；固定装配，不走全局表）
   易用层        ├──────────── ClientFactory（一服务一模板，For(path) 克隆客户端）
                └──────────── Builder → Nami（fluent 客户端：Action/URL/Call*）

   客户端层      Nami + Config（timeout/encoder/decoder/channel/upstream/
                              url/name/path/group/filters/headers）
                        │ CallOrThrow
                        ▼
   调用链层      Invocation（= Context + filters[index] + actuator）
                        │ Invoke() 逐个过滤器，末位是通道执行器
                        ▼
   传输层        Channel ── channel/http（init 注册 "http"/"https"）
                        │ Call(ctx)：Pretreatment → 构请求（body/form/query 三态）
                        ▼
   序列化层      Encoder（请求体）+ Decoder（响应；Pretreatment 设 Accept）
                ── coder/json（init 注册 "application/json"）

   寻址层        Upstream func() string ←─ UpstreamFixed（轮询）
                              ↑ NewDiscoveryUpstream ←─ Discovery 接口
                                              ↑ plugins/nacos NewNamiUpstream

   全局注册表    manager.go：channelMap[scheme] / encoderMap[enctype] /
                decoderMap[enctype] / filterSet（+ First 默认语义）
```

### 2.2 一次调用的时序

```text
n.CallOrThrow(headers, args, body)
  ├─ URL 解析优先级：n.url 直设 > Upstream()() + config.Path() 拼接
  │    （Upstream 返回空 → error "upstream not found server instance"）
  ├─ NewInvocation(config, target, method, action, callURL, body, actuator)
  │    └─ 合并 config.headers → ctx.Headers；filters = config.Filters() + [actuator]
  ├─ inv.Invoke()  → 用户 Filter 依次执行（可改 Headers/Body/…）
  │    └─ 末位 actuator = n.callDo
  │         ├─ channel = config.Channel() ?? GetChannel(url 的 scheme)
  │         └─ channel.Call(&inv.Context)
  │              ├─ ChannelBase.Pretreatment：按 Accept/Content-Type 头兜底解析 decoder/encoder
  │              ├─ GET 或 body+args 并存 → args 追加为 query string
  │              ├─ 非 GET：Content-Type 是 form 系 → form 编码；否则 encoder 编码 body（或回落 form）
  │              ├─ decoder.Pretreatment（如 JSON decoder 设 Accept: application/json）
  │              └─ 发请求（Config.Timeout>0 时每请求新建 client）→ NewResult(code, body) + 响应头/charset
  └─ Result：AssertSuccess（≥400 报错）/ Bind(val) / AsAny()
```

---

## 3. 核心类型契约

### 3.1 Channel / ChannelBase

```go
type Channel interface {
    Call(ctx *Context) (*Result, error)
}

// ChannelBase 提供通道共享的 Pretreatment：decoder/encoder 未显式配置时，
// 按请求头 Accept / Content-Type 从注册表反查（decoder 兜底 JSON，再兜底 First）。
type ChannelBase struct{}
func (cb *ChannelBase) Pretreatment(ctx *Context)
```

通道选择规则（`Nami.callDo`）：`Config.Channel()` 显式配置优先；否则取 URL scheme 查注册表；都没有则报错 `no channel available`。**这使 `sd://...` 这样的 URL 天然路由到未来的 socketd 通道**——注册即得。

### 3.2 Encoder / Decoder

```go
type Encoder interface {
    Enctype() string              // 如 "application/json"（注册键）
    BodyRequired() bool           // true 时 ctx.Body 为空则报错（如 protobuf）
    Encode(obj any) ([]byte, error)
    Pretreatment(ctx *Context)    // 发送前改请求上下文（JSON encoder 为 no-op）
}

type Decoder interface {
    Enctype() string
    Decode(rst *Result, typ reflect.Type) (any, error)  // 按目标类型解码
    Pretreatment(ctx *Context)    // JSON decoder 在此设 Accept 头
}
```

设计点：`Pretreatment` 把 Java 侧散在通道里的头设置逻辑**显式化为接口方法**，编解码器自管协议头。

### 3.3 Config（每客户端配置 + clone 语义）

字段与 Java `NamiBuilder` 一一对应：`timeout`/`heartbeat`（秒）、`encoder`/`decoder`/`channel`、`upstream`/`url`/`name`/`path`/`group`、`filters`/`headers`。要点：

- `init()`：无显式 decoder 时按 `Accept` 头（兜底 JSON）解析；无 encoder 时按 `Content-Type` 头解析。
- `clone()`：**深拷贝标量与 map/slice，共享 encoder/decoder/channel/upstream 引用**（它们被设计为无状态长寿命）。`ClientFactory.For(path)` 靠它保证端点间互不影响。
- `heartbeat` 为**预留字段**：仅 socket 系通道有意义（Java 同语义），当前 HTTP 通道不消费。

### 3.4 Context / Invocation（过滤器链）

```go
type Context struct {          // 单次调用的全部请求语境
    Config  *Config
    Target  any                // 预留：代理目标（对应 Java 的 method 元数据）
    Method  reflect.Method     // 预留：同上
    Action, URL string
    URI     *url.URL
    Headers map[string]string
    Args    map[string]any     // query / form 参数
    Body    any                // 请求体（优先于 Args）
    Ctx     context.Context    // 调用方 context，供 filter 注入请求域值（如鉴权头）
}

type Invocation struct {
    Context                    // 内嵌：filter 直接读写请求
    filters []Filter           // config.Filters() + [actuator]
    index   int                // 游标
    actuator Filter            // 末位执行器（真正调通道）
}

func (inv *Invocation) Invoke() (*Result, error)  // 取下一个 filter 执行
```

`Target`/`Method` 是**为代理形态预留的接口位**（对齐 Java `Invocation`），流式 API 恒为 nil——将来若引入代码生成 stub 可直接复用整条链。`FilterFunc` 函数适配器对应 Java 的 lambda filter。

### 3.5 Result（双解码路径，注意）

```go
type Result struct{ ... }  // code / headers / charset / body（BodyAsString 后释放字节）
func (r *Result) AssertSuccess() error        // ≥400 → error（带 body 摘要）
func (r *Result) Bind(val any) error          // 检查 2xx 后 encoding/json.Unmarshal
func (r *Result) AsAny() (any, error)         // 检查 2xx 后解为 any
```

**as-built 事实**：`Bind`/`AsAny` 直用 `encoding/json`，**不经过** `Decoder`；只有 `Nami.GetObject(val)` 走 `Decoder.Decode(result, typ)` 反射路径。两条路径行为基本等价（JSON decoder 的 string 容错除外，见 §4.2）。这是实用主义取舍：常用路径零反射开销；代价是"换 Decoder 不影响 Bind"——记录为已知语义边界。

### 3.6 Nami 客户端（URL 解析与 {fun} 模板）

```go
n := nami.New()                       // 默认 Action=POST
n.Action(nami.MethodGet).URLWithPath(base, path).CallAndBind(h, args, body, &v)
```

- **URL 优先级**：`URL()`/`URLWithPath()` 直设的 url 生效；否则 `Upstream()()` 取服务地址 + `Config.Path()` 经 `joinURI` 拼接。
- **`{fun}` 占位符**：`URLWithPath` 遇 base 含 `{fun}` 时以 path **替换**而非拼接——保留 Java `@NamiClient(url=".../{fun}")` + 方法名路由的表达力。
- `joinURI`：处理 base 尾斜杠、`sd:` 前缀剥离（为 socketd 预留）、scheme 后双斜杠清理、base 自带 query 串保序。
- 返回侧四档：`Call`（吞错存 Result）/ `CallOrThrow` / `CallAndBind`（+JSON 绑定）/ `CallAndGetBody`（+AsAny）；另有事后取值 `GetString`/`GetObject`。

### 3.7 Upstream / Discovery（函数即发现）

```go
type Upstream func() string                 // 每次调用返回一个服务地址
type Discovery interface {                  // 有状态发现后端
    GetServer(group, name string) (string, error)
}
func NewDiscoveryUpstream(d Discovery, group, name string) Upstream
func NewUpstreamFixed(servers []string) Upstream  // 1 台→无锁闭包；多台→轮询（带锁）
```

对比 Java：`LoadBalance` 内核接口被压成一个**函数类型**——Go 里函数即最小接口。轮询是唯一内置策略；ip-hash/加权及一等 `Balancer` 契约规划在 [`../microservice.md`](../microservice.md) P0-A（含 `plugins/nacos` 固定取 `instances[0]` 的 bugfix）。

### 3.8 NamiManager 注册表（manager.go）

包级全局表 + `sync.RWMutex`：`RegChannel(scheme, ch)` / `RegEncoder(e)` / `RegDecoder(d)` / `RegFilter(f)`，各带 `*IfAbsent` 变体；`GetEncoderFirst/GetDecoderFirst` 返回**首个注册者**作为兜底默认。`channel/http` 的 `init()` 注册 `http`+`https`，`coder/json` 的 `init()` 注册 JSON——所以 README 的快速开始需要那两个空 import。

**as-built 缺口**：`RegFilter`/`GetFilters` 存在但 `NewInvocation` 只合并 `config.Filters()`，**全局过滤器尚未进入调用链**（Java 侧 `NamiManager.reg` 是生效的）。接线是三行改动，见 §7。

### 3.9 Builder / ClientFactory

- `Builder`：链式设置全部 Config 字段后 `Build()` 出 `*Nami`——Java `Nami.builder()` 的直译（少了 `create(Iface)`）。
- `ClientFactory`：**一个上游服务一份模板 Config**，`For(path)` 以 `config.clone()` + `SetPath(path)` 产出独立客户端；`ServiceName/Timeout/FilterAdd/HeaderSet` 等对全体生效。对应 Java「一个 `@NamiClient(name=...)` 声明 + 多方法各占一个 path」的用法——Go 里"多方法"变成"For 多次"。

### 3.10 util（固定装配的极简层）

`util` 刻意**绕过全局注册表**：`init()` 直接持有 channel/decoder/encoder 实例设进每个临时 Config——注册表被别的代码（如测试）污染也不受影响。`SetBaseURL` 用 `UpstreamFixed` 机制实现"相对路径拼 base"；全 URL（含 `://`）直连。泛型家族 `GetJSON[T]/PostJSON[T]/RequestJSON[T]` 及 `*With`（query+headers）/`*Bind`（非泛型指针绑定）变体共 20+ 个函数，是 Java 侧不存在的 Go 增值层。

---

## 4. 内置实现细节

### 4.1 channel/http 请求构造（三态）

| 条件 | 构造方式 |
|------|----------|
| `Action == GET`（或 body 与 args 并存） | args 序列化为 **query string** 追加到 URL |
| `Content-Type` 以 `multipart/form-data` / `x-www-form-urlencoded` 开头 | `buildFormRequest`：args 全量 form 编码 |
| 其余（有 body 或能解析出 encoder） | `buildBodyRequest`：`encoder.Encode(BodyOrArgs())`（Body 为 nil 时用 Args），Content-Type 设为 `encoder.Enctype()` |
| 无 body 且无 encoder | 回落 form 请求 |

超时：`Config.Timeout() > 0` 时**每请求**新建 `http.Client{Timeout}`（as-built：未复用连接池配置——自定义连接池请 `SetClient` 或显式 `Channel`）。响应侧复制全部响应头、从 `Content-Type` 探测 charset。

### 4.2 coder/json 的容错

`Decode` 的三个贴心行为：空 body / `"null"` → nil；目标类型是 `string` 且响应不像 JSON（不以 `"`/`{`/`[` 开头，或 Unmarshal 失败）→ **原样字符串返回**（兼容裸文本接口）；指针类型解引用后重建。`Pretreatment` 设 `Accept: application/json`。

---

## 5. 与生态的集成

| 集成点 | 形态 |
|--------|------|
| aifei 服务端 | 对偶闭环：aifei `{code,msg,data}` 出口 ↔ `Result.Bind` 入口（同一 JSON 约定） |
| plugins/nacos | `NewNamiUpstream(name)` 把 Nacos 发现包成 `Upstream`；`GetServer` 当前取 `instances[0]`（P0-A 修为轮询 + 缓存） |
| 参考项目定制（ficus-catl-oa） | RPC 鉴权透传：`NamiAuthorizationFilter`（nami.Filter）从 `inv.Ctx` 取 token 注入 RPC header，配合 `ClientFactory` 使用（见 [`../../guide/server-customization.md`](../../guide/server-customization.md)） |
| 治理（规划） | 熔断/重试/限流/trace 全部以 `nami.Filter` 形态接入，不污染核心（microservice.md P1） |

---

## 6. 已定的设计决策

| # | 决策 | 理由 |
|---|------|------|
| D1 | 接口代理 → 流式 API 三件套（Nami/ClientFactory/util） | Go 无动态代理（#41897）；换来编译期类型安全 |
| D2 | `Upstream` 用函数类型而非接口 | 单实例/轮询写起来是一行闭包；`Discovery` 接口留给有状态后端 |
| D3 | 通道按 URL scheme 从注册表解析 | 新通道（如 socketd）注册 scheme 即全框架生效，无需改调用方 |
| D4 | 编解码器带 `Pretreatment` | 协议头（Accept/Content-Type）归属编解码器，通道保持通用 |
| D5 | `util` 固定装配绕过全局注册表 | 极简层的稳定性优先于可替换性 |
| D6 | `Config.clone()` 共享无状态组件、深拷贝配置 | Factory 克隆安全性的前提；组件无状态是注册表模式的约定 |
| D7 | `Result.Bind` 直用 encoding/json | 常用路径零反射；Decoder 只服务 `GetObject` 反射路径（语义边界见 §3.5） |
| D8 | `Target`/`Method`/`heartbeat` 字段预留 | 对齐 Java Invocation 契约，为代码生成 stub 与 socket 通道留位 |

---

## 7. 已知限制与后续规划

| # | 限制 | 现状 | 规划 |
|---|------|------|------|
| L1 | 全局 Filter 未接线 | `RegFilter`/`GetFilters` 存在，`NewInvocation` 未合并 | 小改动：`NewInvocation` 里 `append(nami.GetFilters(), ...)`（先全局后本地） |
| L2 | LB 策略单一 | 仅 `UpstreamFixed` 轮询；nacos 侧固定取第一个实例 | microservice.md **P0-A**：`nami/balancer.go` 一等 `Balancer` + `NewBalancedUpstream` |
| L3 | 无重试/熔断 | 零内置 Filter | microservice.md **P1**：`nami/retry.go`、`plugins/breaker`（均以 Filter 接入） |
| L4 | Socket.D 通道未移植 | `joinURI` 已预留 `sd:` 剥离 | 视需求加 `channel/socketd`（依赖 socket.d Go 客户端，需评估） |
| L5 | 仅 JSON coder | hessian/kryo/protobuf 常量已定义（`content_types.go`） | 按需实现 `Encoder`/`Decoder` 子包注册 |
| L6 | 超时未复用连接池 | `Timeout>0` 每请求新建 `http.Client` | 显式 `Channel`/`SetClient` 可绕过；通道侧可优化为 clone-transport |
| L7 | trace 透传无标准键 | `Ctx` 已透传 `context.Context` | observability.md：`X-Trace-Id` 注入做成官方 Filter |

---

## 8. 测试布局（`_test/nami_test`，黑盒）

| 文件/目录 | 覆盖 |
|-----------|------|
| `nami_test.go` | 注册表（channel scheme/First 语义）、UpstreamFixed 轮询、Result 全方法、Builder、Filter 链序、Upstream 调用 |
| `client_factory_test.go` | Factory 模板继承（ServiceName/Header/Filter）、For(path) 克隆隔离 |
| `channel/http/` | GET/POST、query+headers、form body、非 2xx、自定义 Client、scheme 默认注册 |
| `coder/json/` | 空 body/null/对象/切片/指针、string 原样回落、Pretreatment、包注册副作用 |
| `util/` | Get/Post 家族、泛型 JSON、非 2xx、base URL 相对路径 + 全 URL 旁路、超时设置 |

---

## 附：Java ↔ Go API 对照速查

| Java (Solon Nami) | Go (aifei-go/nami) |
|---|---|
| `Nami.builder()...create(Iface.class)` | `nami.NewBuilder()...Build()`（无代理，返回 `*Nami`） |
| `@NamiClient(url/name/group/path/headers/timeout)` | `Builder.URL/Name/Group/Path/HeaderSet/Timeout` |
| `@NamiClient(heartbeat)` | `Builder.Heartbeat`（预留，HTTP 不消费） |
| `@NamiMapping("GET")` / 默认无参 GET 有参 POST | `Nami.Action("GET")`（默认 POST） |
| `@NamiBody` 参数 | `Call(headers, args, body)` 第三参 |
| `@NamiParam` 参数 | `args` map |
| `Channel` 实现（nami-channel-http） | `channel/http.New()` + `init` 注册 http/https |
| `nami-coder-*` | `coder/json`（Enctype 注册） |
| `Filter.doFilter(inv)` / `inv.invoke()` | `Filter.DoFilter(inv)` / `inv.Invoke()` |
| 接口自身 extends Filter / `NamiManager.reg(filter)` | `Config.FilterAdd`（自身） / `nami.RegFilter`（全局，**未接线** L1） |
| `LoadBalance.get(group, service).getServer()` | `Upstream`（`NewUpstreamFixed` / `NewDiscoveryUpstream`） |
| `NamiManager` 注册表 | `manager.go`（Reg*/Get*，含 First 默认） |
| `NamiConfiguration` 配置器 | Builder/Config 显式配置（无容器） |
| `Result`（code/headers/charset/body） | `nami.Result`（+`Bind`/`AsAny`/`AssertSuccess`） |
| `@NamiClient(localFirst=true)` | —（未移植） |
| —（Java 无） | `util.GetJSON[T]` 等泛型家族、`ClientFactory.For(path)`、`Context.Ctx` 透传 |
