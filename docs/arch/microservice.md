# 微服务能力补齐设计（Microservice Roadmap）

> 目标：在已有 `nacos`（注册 / 发现 / 配置）、`nami`（RPC 客户端）、`kafka`/`dami`/`xxljob`（异步 / 事件 / 调度）、`dataisolate`（多租户）的基础上，补齐微服务尚缺的**通信治理层**与**可观测层**，形成完整可用的微服务栈。
>
> 本文**不写实现代码**，只给出：模块清单、接口契约（Go 签名）、配置项、依赖与改动点、分期建议——作为后续逐期实现的**契约**。每个模块可独立编译、独立合入。

---

## 目录

1. 背景与现状
2. 设计原则
3. 总体路线图
4. P0-A：负载均衡（`nami` + `plugins/nacos`）
5. P0-B：健康检查与 Actuator（`server`）
6. P1-A：服务治理（breaker / ratelimit / retry）
7. P1-B：可观测性（trace / metrics）
8. P2：进阶模块（gateway / auth / lock / idgen）
9. 模块清单总表
10. 配置项汇总
11. 向后兼容性
12. 边界与限制（不做什么）
13. 实现步骤建议（分期）
14. 未来扩展
15. 附：与现有代码的衔接点速查

---

## 1. 背景与现状

### 1.1 已具备的能力

| 能力 | 载体 | 说明 |
|------|------|------|
| 服务注册 / 心跳 / 下线 | `plugins/nacos` | 临时实例（ephemeral），SDK 内部心跳，`Stop()` 时 deregister，IP 自动检测 |
| 服务发现 | `plugins/nacos` | `SelectInstances(HealthyOnly=true)` |
| 配置中心 | `plugins/nacos` + `config` | `ListenConfig` 推送 + 初始值投递；`BindProps` 深度合并；已挂 `config.CloudLoader`（L5） |
| RPC 客户端骨架 | `nami` | HTTP/1.1 通道、JSON 编解码、`Filter` 链、`Upstream`/`Discovery`、`Builder`/`ClientFactory`、泛型 `util.GetJSON[T]` |
| HTTP 服务端 | `aifei` + `http` + `server` | 路由、中间件、`Just Service` 反射注册、事务拦截器 |
| 异步消息 | `plugins/kafka` | franz-go，at-least-once |
| 进程内事件 | `dami` / `plugins/dami` | send/call/stream/lpc |
| 分布式调度 | `plugins/xxljob` | 任务调度 |
| 多租户隔离 | `plugins/dataisolate` | 租户 + 行 / 列隔离 |

### 1.2 关键缺口（已核实，非推测）

| 缺口 | 现状证据 | 影响 |
|------|----------|------|
| **负载均衡失效** | `plugins/nacos/nami.go:115` `inst := instances[0]` 永远取第一个；每次调用 `SelectInstances` 无本地缓存 | 多实例水平扩容失效，流量全压首实例；高频 RPC 压垮 nacos |
| **无就绪探针** | `server` 无 `/health` `/readiness` | nacos 注册后立即被发现，启动期请求 502；k8s 无探针 |
| **无服务治理** | `nami` 零内置 `Filter`；`server` 无 ratelimit | 下游故障无熔断 → 雪崩；无重试 / 限流 |
| **无可观测** | 全链路无 OpenTelemetry；无 `/metrics` | 跨服务调用无法排障；无 QPS / 延迟 / 错误率监控 |

### 1.3 nami 的设计哲学（决定补齐方式）

`nami` 是**传输层抽象框架**，不是完整微服务客户端栈：它提供扩展点（`Filter` / `Channel` / `Encoder/Decoder` / `Upstream` / `Discovery`），具体能力靠插件填充。因此治理与可观测能力**一律以 `Filter` 形式接入**，不污染 `nami` 核心，保持零外部依赖。

---

## 2. 设计原则

- **分层补齐**：先让"微服务能正确跑起来"（P0），再"生产可用"（P1），最后"进阶"（P2）。
- **核心库零外部依赖不变**：`aifei` / `nami` / `db` / `enjoy` 保持仅标准库；治理 / 可观测依赖（gobreaker、otel、prometheus）只进 `plugins/*`。`server` 的 actuator 与 ratelimit 中间件只用标准库（`x/time/rate` 属 `golang.org/x`，视同标准库扩展，可接受；若严格则 ratelimit 也下放 `plugins/ratelimit`）。
- **扩展点优先**：新增能力尽量作为 `nami.Filter` / `aifei.Handler` / `aifei.Plugin`，不改既有调用语义。
- **配置驱动**：所有模块读 `config.Props` 下的命名段（`breaker.*` / `trace.*` / `metrics.*` …），与 cache/kafka 等既有插件一致。
- **自包含测试**：每个新插件配 `_test/<area>_test`，优先嵌入式依赖（无外部服务）。
- **契约先行**：本文的接口签名是后续实现的契约；实现可优化，公共 API 不偏离。

---

## 3. 总体路线图

```
            ┌──────────────────── 可观测层（P1-B）────────────────────┐
            │  trace（OTel）   metrics（Prometheus /metrics）         │
            └─────────────────────────────────────────────────────────┘
            ┌──────────────────── 治理层（P1-A）──────────────────────┐
            │  breaker（熔断）  ratelimit（限流）  retry（重试）       │
            └─────────────────────────────────────────────────────────┘
   ┌─────────────────── 通信层（P0）─────────────────────────┐
   │  LB（nami Balancer + nacos 接入）  Actuator（/health…） │   ← 本期最关键
   └────────────────────────────────────────────────────────┘
   ┌─────────────────── 已有地基 ────────────────────────────┐
   │  nacos 注册/发现/配置 · nami RPC · kafka/dami/xxljob   │
   │  aifei+server · dataisolate · cache · storage · swagger│
   └─────────────────────────────────────────────────────────┘
```

依赖方向自下而上：trace / metrics 依赖 actuator（端点）与 nami Filter（透传）；breaker / retry 作为 nami Filter 依赖 LB 已修（否则打到固定实例的熔断无意义）。

---

## 4. P0-A：负载均衡（`nami` + `plugins/nacos`）

> **这是 P0 中的 P0**——既有链路的 bug。不修它，水平扩容形同虚设，后续所有治理都建立在错误前提上。

### 4.1 现状（已核实）

```go
// nami/discovery.go
type Discovery interface {
    GetServer(group, name string) (string, error)   // 返回单个 URL
}
func NewDiscoveryUpstream(d Discovery, group, name string) Upstream

// nami/upstream.go —— 仅 round-robin，单实例无锁闭包
type UpstreamFixed struct { servers []string; mu sync.Mutex; index int }

// plugins/nacos/nami.go —— 固定取第一个，无缓存
func (d *nacosDiscovery) GetServer(group, name string) (string, error) {
    instances, _ := d.naming.SelectInstances(vo.SelectInstancesParam{HealthyOnly: true})
    inst := instances[0]                                   // ← 永远第一个
    return fmt.Sprintf("http://%s:%d", inst.Ip, inst.Port), nil
}
```

两个问题：① `GetServer` 单实例返回模型把 LB 推给了实现者，而 nacos 实现没做；② 无本地实例列表缓存，每次 RPC 都查 nacos。

### 4.2 设计目标

1. `nami` 引入一等公民的 `Balancer` 与实例列表发现接口。
2. nacos 实现 LB（默认 round-robin）+ 本地实例缓存（TTL，二期上 `Subscribe` 事件）。
3. 旧 `Discovery`/`GetServer`/`NewDiscoveryUpstream`/`NewNamiUpstream` 签名与行为向后兼容（"固定第一个"→"round-robin"视为 bugfix）。

### 4.3 核心契约（新增 `nami/balancer.go`）

```go
package nami

// Instance 是一个已发现的服务实例。
type Instance struct {
    URL      string            // 形如 http://ip:port
    Weight   float64           // 权重，默认 1.0
    Metadata map[string]string // 透传元数据（region/zone/version 等，供高级 LB 用）
}

// InstanceDiscovery 返回某服务的全部健康实例列表（新的发现接口）。
// 新的发现提供者应实现本接口；旧的 Discovery 仍可用（单实例语义）。
type InstanceDiscovery interface {
    GetInstances(group, name string) ([]Instance, error)
}

// Balancer 从实例列表中挑选一个实例。
type Balancer interface {
    Pick(instances []Instance) (Instance, bool) // bool=false 表示列表为空
}

// 内置 Balancer 实现
func NewRoundRobinBalancer() Balancer   // 加权轮询（忽略 Weight，等权）
func NewRandomBalancer() Balancer       // 随机
func NewWeightedBalancer() Balancer     // 平滑加权轮询（SWRR，尊重 Weight）
```

### 4.4 Upstream 构造（新增变体，旧的不动）

```go
// NewBalancedUpstream 基于 InstanceDiscovery + Balancer 构建 Upstream。
// 实例列表带本地缓存（WithInstanceCacheTTL），避免每次 Pick 都打注册中心。
func NewBalancedUpstream(
    d InstanceDiscovery, b Balancer, group, name string, opts ...UpstreamOption,
) Upstream

type UpstreamOption func(*upstreamConfig)

// WithInstanceCacheTTL 设置实例列表本地缓存时长，默认 10s。
// 缓存过期后下次 Pick 触发一次 GetInstances 刷新。
func WithInstanceCacheTTL(d time.Duration) UpstreamOption

// WithDiscoveryAdapter 将旧的 Discovery（单实例）适配为 InstanceDiscovery，
// 供尚未迁移的实现复用 BalancedUpstream。
func WithDiscoveryAdapter(old Discovery) UpstreamOption
```

### 4.5 nacos 侧改动（`plugins/nacos/nami.go`）

- `nacosDiscovery` **增加** `GetInstances(group, name) ([]Instance, error)`：把 `SelectInstances(HealthyOnly)` 结果映射为 `[]Instance`（携带 `Weight` 与 `Metadata`）。
- `NewNamiUpstream(name)` 改用 `nami.NewBalancedUpstream(d, balancer, cfg.Group, name)`，`balancer` 由配置决定（默认 round-robin）。
- **二期**（非本期）：用 nacos SDK `Subscribe`（事件回调）维护本地列表，替代 TTL 轮询缓存，实例上下线近实时。

### 4.6 配置（`nacos.*` 扩展）

```yaml
nacos:
  discovery:
    balancer: round-robin   # round-robin | random | weighted，默认 round-robin
    cache:
      ttl: 10s              # 实例列表本地缓存时长，默认 10s
```

### 4.7 向后兼容

| 调用方 | 现状行为 | 新行为 |
|--------|----------|--------|
| `nacos.NewNamiUpstream(name)` | 永远打到 `instances[0]` | round-robin 轮询（**bugfix**，调用方不应依赖"总是第一个"） |
| 直接实现 `nami.Discovery` 的第三方 | `GetServer` 仍可用 | 不变；可额外实现 `InstanceDiscovery` 升级 |
| `nami.NewUpstreamFixed` | round-robin | 不变 |

---

## 5. P0-B：健康检查与 Actuator（`server`）

### 5.1 现状

`server` 无任何探针端点；nacos 注册即标 `Healthy=true`，启动期与依赖未就绪期都会被调用方命中。

### 5.2 设计目标

`server` 内置一组 actuator 端点，供 k8s/nacos 探针与运维查询；插件可贡献就绪检查（db/redis ping 等）。

### 5.3 核心契约（新增 `server/actuator.go`，仅标准库）

```go
package server

// HealthChecker 贡献一个就绪检查。
// Check 返回 nil 表示就绪；返回 error 则其 Message 进 /readiness 明细，整体 503。
type HealthChecker interface {
    Check() error
}

// HealthCheckerFunc 是函数式适配。
type HealthCheckerFunc func() error

// RegisterHealthChecker 注册一个命名就绪检查（如 db、redis、kafka）。
// 插件在 Start() 时调用，把自身依赖的健康度接入 /readiness。
func RegisterHealthChecker(name string, c HealthChecker)

// WithActuator 在指定 base path（默认 "/"）下挂载 actuator 端点：
//   GET {base}/health      liveness——进程活着即 200（不查依赖）
//   GET {base}/readiness   readiness——聚合所有 HealthChecker，全 ok 才 200，否则 503 + JSON 明细
//   GET {base}/info        构建信息（app/version/build，来源 config 或编译期 -ldflags 注入）
//   GET {base}/metrics     占位，由 P1 metrics 插件接管（未装则 404/501）
func WithActuator(basePath string) Option
```

### 5.4 与 nacos 的就绪联动（可选增强）

nacos 插件 Start 时立即注册；可增加 `readiness-gate`：未就绪前不注册（或注册 `Enable=false`），首个就绪通过后再 `Enable=true`。一期简化：注册即就绪，`/readiness` 供 k8s 独立使用；二期接入 gate。

### 5.5 配置

```yaml
server:
  actuator:
    enabled: true
    base-path: "/"        # 端点挂载根
    info:                 # /info 展示字段，可由编译期 ldflags 覆盖
      app: ${app.name}
      version: ${app.version}
```

---

## 6. P1-A：服务治理

### 6.1 熔断降级（新增 `plugins/breaker`）

**职责**：客户端调用下游时，失败率超阈值即熔断（快速失败 + 降级），防止雪崩。也可作服务端降级中间件。

```go
package breaker

// CircuitBreaker 是熔断器抽象。
type CircuitBreaker interface {
    // Do 在熔断器保护下执行 fn；熔断打开时返回 ErrCircuitOpen。
    Do(ctx context.Context, fn func() error) error
}

// Plugin（aifei.Plugin）按规则集合装配命名熔断器。
type Plugin struct{ /* ... */ }
func NewPlugin(logger log.Logger) *Plugin

// NamiFilter 返回一个 nami.Filter，按下游服务名选取熔断器，
// 包裹 inv.Invoke()；失败计数，超阈值打开。
func (p *Plugin) NamiFilter() nami.Filter

// Middleware 返回服务端降级 Handler（可选，按路由配置降级返回）。
func (p *Plugin) Middleware() aifei.Handler
```

**底座**：`github.com/sony/gobreaker`（轻量、纯 Go）；若需重试 + 熔断 + 限流组合，可换 `github.com/failsafe-go/failsafe`。**配置**：

```yaml
breaker:
  default:                 # 默认规则
    threshold: 0.5         # 失败率阈值（窗口内）
    window: 60s            # 统计窗口
    min-calls: 20          # 窗口最小样本
    open-duration: 30s     # 熔断打开时长
  rules:
    order-service:         # 按下游服务名覆盖
      threshold: 0.3
```

### 6.2 限流（`server.RateLimit` + 可选 `plugins/ratelimit`）

```go
// server/middleware.go 新增（仅 x/time/rate）
type Limiter interface {
    Allow(key string) bool
}

// RateLimit 返回按 key（IP/用户/路由）限流的 Handler；超出返回 429。
func RateLimit(l Limiter) aifei.Handler

// plugins/ratelimit 提供 token-bucket Limiter 的配置化装配与多维度 key。
```

**底座**：`golang.org/x/time/rate`。**配置**：

```yaml
ratelimit:
  default: { rate: 100, burst: 200 }
  routes:
    /api/login: { rate: 10, burst: 20 }
```

### 6.3 重试（新增 `nami/retry.go`，仅标准库）

```go
package nami

// RetryFilter 返回一个按策略重试的 Filter。
// 仅对幂等 / 网络类错误重试；默认指数退避 + 抖动。
func RetryFilter(opts ...RetryOption) Filter

type RetryOption func(*retryConfig)
func WithMaxAttempts(n int) RetryOption       // 默认 3
func WithBackoff(base, max time.Duration) RetryOption
func WithRetryable(fn func(*Result, error) bool) RetryOption
```

> 顺序建议：`RetryFilter` 包在 `breaker.NamiFilter` 之内（先重试再熔断计数），避免重试刷高熔断失败率——具体由配置或 Filter 注册顺序控制。

---

## 7. P1-B：可观测性

> 📌 **本节已细化为独立设计文档 [`observability.md`](./observability.md)**：存储统一收敛到 **GreptimeDB**（metrics+logs+traces 一个库，替代 Prometheus + 日志库 + 链路库整套），链路追踪接入 **SkyWalking**（`killme2008/skywalking` fork `v11.0.0-greptimedb.3`，OAP 直写 GreptimeDB）。下文的 OTel+Prometheus 双存储方案已被替换，保留仅作过渡参考；**实现以 `observability.md` 为准**。aifei-go 侧契约（`plugins/trace` 的 HTTPMiddleware/NamiFilter、`plugins/metrics`）不变，仅 exporter/endpoint 指向改变。

### 7.1 链路追踪（新增 `plugins/trace`）

**职责**：服务端为每请求创建 span（透传 W3C `traceparent`），客户端（nami）把 traceparent 注入下游请求头，形成跨服务调用链。

```go
package trace

// Plugin 装配 OTel tracer 与 exporter。
type Plugin struct{ /* ... */ }
func NewPlugin(logger log.Logger) *Plugin

// HTTPMiddleware（func(http.Handler)http.Handler）由 server.WithHTTPHandler 挂载：
// 解析入站 traceparent → 创建 span → 写入 request.context。
func (p *Plugin) HTTPMiddleware() func(http.Handler) http.Handler

// NamiFilter 从 context 读 traceparent，注入出站请求 header。
func (p *Plugin) NamiFilter() nami.Filter
```

**底座**：`go.opentelemetry.io/otel` + `otelhttp`；exporter 支持 otlp（jaeger/collector）。**配置**：

```yaml
trace:
  enabled: true
  service-name: ${app.name}
  exporter: otlp            # otlp | stdout | none
  endpoint: localhost:4317
  sampling: { ratio: 0.1 }
```

### 7.2 监控指标（新增 `plugins/metrics`）

**职责**：暴露 Prometheus 指标（QPS、延迟分位、错误率、in-flight）。

```go
package metrics

type Plugin struct{ /* ... */ }
func NewPlugin(logger log.Logger) *Plugin

// HTTPMiddleware 统计 HTTP 维度指标。
func (p *Plugin) HTTPMiddleware() func(http.Handler) http.Handler

// MetricsHandler 返回 /metrics 的 http.Handler，供 actuator 挂载（WithActuator 预留点）。
func (p *Plugin) MetricsHandler() http.Handler
```

**底座**：`github.com/prometheus/client_golang`。**默认指标**：`http_requests_total{method,route,code}`、`http_request_duration_seconds`（histogram）、`http_inflight`。**配置**：

```yaml
metrics:
  enabled: true
  path: /metrics            # 与 actuator base-path 拼接
```

---

## 8. P2：进阶模块

| 模块 | 路径 | 职责 | 依赖 | 外部库 |
|------|------|------|------|--------|
| API 网关 | `plugins/gateway`（或独立 module） | 统一入口、路由转发（结合 nacos 发现）、聚合鉴权、全局限流 | aifei, nami, nacos, ratelimit | — |
| 服务间认证 | `plugins/auth`（+ nami JWT Filter） | 下游调用带 JWT/service token；服务端校验 | aifei, nami | `golang-jwt/jwt` |
| 分布式锁 | `plugins/lock` | 基于 redis（复用 cache）或 nacos 的互斥锁 | cache 或 nacos | — |
| 分布式 ID | `plugins/idgen` | 雪花算法，替代 DB 自增（分库分表） | — | — |
| 分布式事务 | （通常不纳入轻框架） | Seata 风格 Saga/TCC | — | 重量级，按需 |

> 网关可作为 aifei 的上层应用而非插件——aifei 本就是 web 框架，网关主要是"路由 + nacos 发现转发 + 鉴权 + 限流"的组合。是否独立模块视复用需求定。

---

## 9. 模块清单总表

| 优先级 | 模块 | 路径 | 依赖 | 外部库 | 改动现有 |
|--------|------|------|------|--------|----------|
| **P0-A** | 负载均衡 | `nami`（+ `plugins/nacos`） | nami, nacos | — | `nami` 加 `Balancer`/`InstanceDiscovery`；nacos `GetInstances` + 接入 |
| **P0-B** | Actuator | `server` | server | — | `server` 新增 `actuator.go` |
| **P1-A** | 熔断 | `plugins/breaker` | aifei, nami, log | sony/gobreaker | — |
| **P1-A** | 限流 | `server`（+ `plugins/ratelimit`） | server | x/time/rate | `server` 加 `RateLimit` |
| **P1-A** | 重试 | `nami` | nami | — | `nami` 加 `RetryFilter` |
| **P1-B** | 链路追踪 | `plugins/trace` | aifei, nami, server, log | otel | — |
| **P1-B** | 监控 | `plugins/metrics` | aifei, server | prometheus/client_golang | actuator `/metrics` 挂接 |
| **P2** | 网关 | `plugins/gateway` | aifei, nami, nacos | — | — |
| **P2** | 服务认证 | `plugins/auth` | aifei, nami | golang-jwt | — |
| **P2** | 分布式锁 | `plugins/lock` | cache / nacos | — | — |
| **P2** | 分布式 ID | `plugins/idgen` | — | — | — |

---

## 10. 配置项汇总

```
nacos.discovery.balancer          round-robin | random | weighted
nacos.discovery.cache.ttl         10s
server.actuator.enabled           true
server.actuator.base-path         "/"
breaker.default.{threshold,window,min-calls,open-duration}
breaker.rules.<service>.{...}
ratelimit.default.{rate,burst}
ratelimit.routes.<path>.{rate,burst}
trace.{enabled,service-name,exporter,endpoint,sampling.ratio}
metrics.{enabled,path}
# P2 略
```

---

## 11. 向后兼容性

| 改动 | 兼容性 | 说明 |
|------|--------|------|
| `nami.Balancer`/`InstanceDiscovery` | 纯新增 | 旧 `Discovery`/`GetServer` 保留 |
| `nacos.NewNamiUpstream` 行为 | bugfix | "固定第一个" → round-robin；调用方不应依赖旧行为 |
| `nami.RetryFilter` | 纯新增 | 默认不启用 |
| `server.WithActuator` / `RateLimit` | 纯新增 option | 不加则无端点 / 无限流，行为同现状 |
| 各 `plugins/*` | 纯新增 | 与既有插件同等模式（`Plugin` + `config.*` + `_test`） |
| `nami`/`aifei`/`db`/`enjoy` 外部依赖 | 不变 | 治理 / 可观测依赖只进 `plugins/*` |

---

## 12. 边界与限制（不做什么）

- **不做 gRPC / 自定义二进制 RPC 协议**：`nami` 维持 HTTP/1.1；Protobuf 编解码常量已存在但实现留待确有需求时（可作 `plugins/...` 单独补）。
- **不做服务网格 / sidecar**：治理在应用进程内（Filter / 中间件），不引入 istio 等。
- **分布式事务默认不做**：重量级，按需单独立项；本路线图不承诺。
- **熔断 / 限流不做控制面 UI**：仅配置驱动 + metrics 暴露状态。
- **trace 不内建采样后端**：只提供 exporter 接入（otlp/stdout），后端由部署方提供。

---

## 13. 实现步骤建议（分期）

每步可独立编译、独立测试、独立合入。

**第一期（P0，让微服务正确运行）**
1. `nami/balancer.go`：`Instance`/`InstanceDiscovery`/`Balancer` + 3 个内置实现 + 单测（纯逻辑、零依赖）。
2. `nami` `NewBalancedUpstream` + 缓存 + adapter；`plugins/nacos` 加 `GetInstances` 并切换 `NewNamiUpstream`；`_test/nacos_test` 补多实例 round-robin 断言。
3. `server/actuator.go`：`/health` `/readiness` `/info` + `RegisterHealthChecker` + `WithActuator`；`_test/server_test` 补端点测试。

**第二期（P1-A，服务治理）**
4. `plugins/breaker` + `nami.RetryFilter` + `server.RateLimit`（+ 可选 `plugins/ratelimit`）；各配 `_test`。

**第三期（P1-B，可观测）**
5. `plugins/trace`（server 中间件 + nami filter）+ `plugins/metrics`（与 actuator `/metrics` 挂接）；`_test` 用 in-memory exporter 断言。

**第四期（P2，按需）**
6. gateway / auth / lock / idgen，视实际需求排期。

---

## 14. 未来扩展

- **nacos Subscribe 事件驱动实例列表**：替代 P0-A 的 TTL 轮询缓存，实例上下线近实时。
- **就绪 gate 联动注册**：未就绪前不向 nacos 注册（P0-B 二期）。
- **自适应限流**：基于 metrics（CPU/延迟）动态调整 `ratelimit`，替代静态配额。
- **治理组合策略**：breaker + retry + timeout 的声明式组合（failsafe 风格）。
- **服务依赖拓扑**：基于 trace 自动绘制服务调用图。
- **Protobuf 编解码**：`nami` 已留常量，需求明确时补 `coder/protobuf`。

---

## 15. 附：与现有代码的衔接点速查

| 现有符号 | 位置 | 本期改动 |
|----------|------|----------|
| `Discovery` / `GetServer` | `nami/discovery.go:5` | 保留；新增 `InstanceDiscovery`/`GetInstances` |
| `NewDiscoveryUpstream` | `nami/discovery.go:11` | 保留；新增 `NewBalancedUpstream` |
| `UpstreamFixed` | `nami/upstream.go:7` | 保留；LB 内置实现独立于它 |
| `nacosDiscovery.GetServer` | `plugins/nacos/nami.go:103` | 保留；新增 `GetInstances`；`NewNamiUpstream` 切换到 balanced |
| `Filter` / `FilterFunc` | `nami/nami.go` / `nami/invocation.go` | 不变；breaker/retry/trace 以 `Filter` 接入 |
| `Builder.FilterAdd` | `nami/builder.go` | 不变；注入治理 Filter |
| `server.Option` / `WithHTTPHandler` | `server/run.go` | 新增 `WithActuator`；trace/metrics 经 `WithHTTPHandler` 挂载 |
| `server.Logger/Recover/Timeout` | `server/handler.go` | 不变；新增 `RateLimit` |
| `config.Props` / `Sub` | `config` | 不变；各插件读自身命名段 |
| `aifei.Plugin` | `aifei/plugin.go` | 不变；新插件实现之 |
