# Solon Nami 与 aifei-go/nami 对照分析

> 本文是「Nami 迁移到 Go」的对照分析。与 dami/flow 不同，nami 模块先于设计文档实现（直接照 [Java Solon Nami](https://solon.noear.org/) 移植），本文为**回补文档**：以 Solon 3.10.x 的 Nami 能力清单为基准，逐项对照 `./nami` 已实现的形态，标注移植状态与替代方案。
> 第二部分为契约设计文档 [`02-design.md`](02-design.md)（as-built）。

---

## 1. Solon Nami 是什么

**一句话定位**：**声明式 RPC / HTTP 客户端**框架——定义一个接口 + 注解，运行时动态代理把方法调用翻译成一次远程调用（HTTP 或 Socket.D），是 Solon 微服务体系的客户端半边。

它由三组正交抽象构成（Java 侧组件生态即按此切分）：

| 抽象 | Java 接口 | 生态组件 |
|------|-----------|----------|
| 传输通道 | `Channel` | `nami-channel-http`、`nami-channel-socketd` |
| 序列化 | `Encoder` / `Decoder` | `nami-coder-snack3` / `fastjson` / `fastjson2` / `jackson` / `hessian` / `fury` / `kryo` / `protostuff` |
| 拦截 | `Filter`（`doFilter(Invocation)`） | 自带（接口自身 extends Filter）/ 全局（`@Component` 或 `NamiManager.reg`） |

外围是寻址与装配：`Upstream` / `Discovery` / `LoadBalance` 内核（`LoadBalance.get(group, service).getServer()`，与 httputils 共用）、`NamiBuilder` 构建器、`NamiManager` 全局注册表、`NamiConfiguration` 配置器。

### 1.1 Java 侧核心能力清单（对照基准）

1. **声明式接口客户端**：`@NamiClient` 注解在接口上，注入即用；字段 `url` / `group` / `name` / `path` / `headers` / `configuration` / `localFirst` / `timeout` / `heartbeat`。
2. **方法级映射**：`@NamiMapping("GET")` / `@NamiMapping("PUT user/a.0.1")`；无注解时**无参 GET、有参 POST**，path 默认为函数名；v3.3 起可直接复用 Solon 注解（`@Post` / `@Mapping` / `@Consumes` / `@Header` / `@Cookie` / `@Path`）。
3. **参数级注解**：`@NamiBody`（参数即请求体）、`@NamiParam`（参数名标注）。
4. **动态代理构建**：`Nami.builder().name("userapi").path("/rpc/v1/user").encoder(...).decoder(...).create(UserService.class)`。
5. **Filter 两级作用域**：接口自身 `extends Filter` + `default doFilter`（自身过滤器，可为不同站点定制编码策略）；`@Component` 全局过滤器；`NamiManager.reg(...)` 手动全局注册。filter 内可改 `inv.headers`、甚至换 `inv.config` 的 encoder/decoder。
6. **LoadBalance 内核**：`LoadBalance.get(serviceName).getServer()`；策略可换（`CloudLoadStrategyDefault` 轮询 / `CloudLoadStrategyIpHash` / 自定义 `LoadBalance.Factory`）。
7. **服务发现**：`@NamiClient(name=...)` 走发现（solon-cloud 本地发现，或 nacos/zookeeper/water 分布式发现），`url` 直连可不用发现。
8. **Socket.D 通道**：`sd:tcp/ws/udp`，支持双向通信、`SocketdProxy.create(url, Iface)` 代理调用；`heartbeat` 仅对 socket/websocket 通道有效，`timeout` 全通道有效。
9. **配置器**：`NamiConfiguration` 在容器里全局定制 encoder/decoder。
10. **localFirst**：本地有接口实现时优先本地调用（单体→微服务平滑过渡）。

---

## 2. 语言差异对移植的约束（必读）

与 dami 迁移时（见 [`../dami/01-go-comparison.md`](../dami/01-go-comparison.md) §2.8）同一组约束，但对 nami 更致命——因为**声明式接口客户端就是 Nami 的主入口**：

| Java 机制 | Go 现实 | 对 nami 的影响 |
|-----------|---------|---------------|
| `java.lang.reflect.Proxy` 动态实现接口 | **不可能**（[golang/go#41897](https://github.com/golang/go/issues/41897) 被拒）；reflect 只能调用已有方法 | `Nami.builder().create(Iface.class)` 无法 1:1 移植——**最大设计约束** |
| 注解（`@NamiClient`/`@NamiMapping`/`@NamiBody`） | 无注解 | 配置改走 Builder/Config 显式链式；方法映射改为调用点显式 `Action()` |
| `extends Filter` + `default` 方法（接口自带过滤器） | 接口无默认实现 | 改为 `FilterFunc` 函数适配 + `Config.FilterAdd` |
| SPI / 组件扫描（channel/coder 组件包） | `init()` 自注册 | import 子包即注册（Go 侧更轻） |
| 运行时泛型擦除 + 反射解码 | 编译期泛型 + `reflect.Type` | Go 侧反而更优：`util.GetJSON[T]` 编译期类型安全 |

**Go 版的替代主线**（详见 02-design.md）：动态代理 → **流式 API**（`Nami` 结构体：`Action/URL/Call*` 系列）+ **`ClientFactory.For(path)`**（一个服务一份模板配置，按路径克隆客户端）+ **`util` 泛型助手**（`GetJSON[T]`/`PostJSON[T]`/`RequestJSON[T]`）。三者合起来覆盖 Java 声明式客户端的常用面，代价是失去"接口即文档"的声明式表达。

---

## 3. 能力移植矩阵

> 图例：✅ 已移植；🟡 部分移植 / 有替代但语义有差；❌ 未移植。

| # | Java Nami 能力 | aifei-go/nami 对应 | 状态 | 说明 |
|---|----------------|--------------------|:----:|------|
| 1 | `Channel` 抽象 | `nami.Channel` 接口（`Call(ctx *Context)`） | ✅ | 接口同构；通道按 URL scheme 从注册表解析 |
| 2 | HTTP 通道 | `channel/http`（`init()` 注册 `http`/`https`） | ✅ | 基于 `net/http`；默认 30s 超时，可换 `*http.Client` |
| 3 | Socket.D 通道 | — | ❌ | 未移植；`joinURI` 预留了 `sd:` 前缀剥离，将来可加 `channel/socketd` |
| 4 | `Encoder`/`Decoder` 抽象 | `nami.Encoder`/`nami.Decoder`（`Enctype`/`BodyRequired`/`Pretreatment`） | ✅ | 接口同构，多出 `Pretreatment` 显式化为方法 |
| 5 | 编解码生态（8 种序列化） | `coder/json`（唯一内置，纯标准库） | 🟡 | 只内置 JSON；hessian/kryo/protobuf 等按需自实现 `Encoder`/`Decoder` 注册即可 |
| 6 | 内容类型驱动编解码选择 | `manager.go` 按 `Enctype()` 注册 + header（`Content-Type`/`Accept`）反查 | ✅ | `RegEncoderIfAbsent` 等兜底注册语义保留 |
| 7 | Filter（自身/全局） | `Config.FilterAdd`（每客户端）+ `nami.RegFilter`（全局） | 🟡 | 全局注册表已有，但 `RegFilter`/`GetFilters` **尚未接线进调用链**（见 02-design.md §7） |
| 8 | `Invocation` 过滤器链 | `nami.Invocation`（内嵌 `Context`，index 游标链 + actuator） | ✅ | 链序 = 配置注册序，末位是通道执行器 |
| 9 | `@NamiClient` 注解配置 | `Builder` / `Config` 显式链式（`URL/Name/Path/Group/HeaderSet/Timeout/FilterAdd`） | 🟡 | 字段一一对应（url/name/group/path/headers/timeout），显式替代注解 |
| 10 | 接口代理 `create(Iface)` | 流式 `Nami` + `ClientFactory.For(path)` + `util.GetJSON[T]` | 🟡 | 语言限制下的替代主线（见上） |
| 11 | `@NamiMapping` 方法映射（默认 GET/POST、path=函数名） | `Action("GET")` 显式设置（默认 POST）+ `URLWithPath` 的 `{fun}` 占位符 | 🟡 | 无方法级声明；`{fun}` 保留了"url 模板 + 函数名替换"的味道 |
| 12 | `@NamiBody`/`@NamiParam` | `Call(headers, args, body)` 三参显式分离 | 🟡 | body/args（query 或 form）/headers 显式传 |
| 13 | `timeout`（全通道） | `Config.Timeout`（秒）→ HTTP 通道每请求新建 client 生效 | ✅ | 语义一致 |
| 14 | `heartbeat`（仅 socket 通道） | `Config.Heartbeat` 字段 + Builder/Factory setter | 🟡 | **预留字段**：HTTP 通道不消费（对齐 Java 侧"仅 socket 有效"；无 socket 通道故闲置） |
| 15 | LoadBalance 内核 + 策略 | `Upstream func() string` + `UpstreamFixed`（多实例轮询、单实例无锁闭包） | 🟡 | 轮询有；ip-hash 等策略无。一等 `Balancer` 契约规划在 [`../microservice.md`](../microservice.md) P0-A |
| 16 | `Discovery` 发现接口 | `nami.Discovery`（`GetServer(group, name)`）+ `NewDiscoveryUpstream` 桥接 | ✅ | [plugins/nacos](../../guide/nacos.md) `NewNamiUpstream` 即此接入点（当前固定取第一个实例——已知缺口，P0-A 修） |
| 17 | `NamiManager` 全局注册表 | `manager.go`（`RegChannel/RegEncoder/RegDecoder/RegFilter` + `Get*First`） | ✅ | "首个注册者为默认"语义保留 |
| 18 | `NamiConfiguration` 配置器 | — | ❌ | 无容器概念，不需要；Builder 即配置器 |
| 19 | `localFirst` 本地优先 | — | ❌ | 未移植；单体场景用 [dami](../../guide/dami.md) lpc 或直接函数调用更 Go 味 |
| 20 | `Result` 响应包装 | `nami.Result`（code/headers/charset/body + `Bind`/`AsAny`/`AssertSuccess`） | ✅ | Go 版把"检查 2xx + 解码"上提为 `Result` 方法，更顺手；注意 `Bind` 直用 `encoding/json` 而非 `Decoder`（差异见 02-design.md §3.5） |

**总评**：传输/序列化/拦截三组核心抽象与注册表机制完整落地（✅ 占多数）；缺口集中在两类——**语言不可为**（接口代理 → 流式 API 替代）与**生态未铺**（Socket.D 通道、多序列化、LB 策略、全局 Filter 接线）。后者全部登记在 02-design.md §7 的限制清单与 microservice.md 路线图中。

---

## 4. 定位对照：nami 在 aifei-go 里的角色

Java Solon 体系里 Nami 是独立客户端框架（`org.noear:nami`），与 Solon 服务端解耦。aifei-go 保持了这个定位，并与兄弟模块形成三层"调用"光谱：

| 调用方式 | 模块 | 走网络 | 形态 |
|----------|------|--------|------|
| 进程内解耦调用 | [dami](../../guide/dami.md) | ❌ | 事件总线 / lpc |
| **跨进程 RPC 调用** | **[nami](../../guide/nami.md)** | ✅ HTTP | 流式客户端 + Channel/Coder/Filter/Upstream |
| 服务端暴露 | [aifei](../../guide/core.md) + [server](../../guide/server.md) | — | Just Service 路由 |

nami 是 aifei 服务端的**对偶**：aifei `{code,msg,data}` JSON 出口 ↔ nami `Result.Bind` JSON 入口；[nacos 插件](../../guide/nacos.md)在中间提供注册/发现，把两端接成微服务闭环。

---

## 5. 结论

1. **三组正交抽象是 Nami 的骨架，Go 版完整保留**——Channel / Encoder+Decoder / Filter 接口与 Java 侧一一同构，注册表机制（含 `*IfAbsent`、First-default）语义对齐。
2. **声明式接口客户端不可移植是唯一硬约束**，Go 版用「流式 API + ClientFactory + util 泛型助手」三件套覆盖其常用面，并换来编译期类型安全。
3. **Java 的注解配置 ↔ Go 的 Builder 链式**是全局替代关系：`@NamiClient` 的每个字段在 `Builder`/`Config` 上都有同名方法。
4. 剩余缺口（Socket.D、多序列化 coder、LB 策略、全局 Filter 接线、heartbeat 消费）均为**生态铺量工作**而非设计障碍，优先级见 [`02-design.md`](02-design.md) §7 与 [`../microservice.md`](../microservice.md)。

契约与调用时序的 as-built 细节见 [`02-design.md`](02-design.md)。

---

## 参考资料

- Solon Nami 官方文档（RPC / Nami 注解 / 过滤器 / LoadBalance）：<https://solon.noear.org/>（remoting 章节）
- Solon 源码：<https://github.com/opensolon/solon>（`nami` 内核 + channel/coder 组件）
- 本仓库 Go 实现：`nami/`（见 [`02-design.md`](02-design.md) 模块清单）
- 动态代理提案：[golang/go#41897](https://github.com/golang/go/issues/41897)
- 姊妹篇：[`../dami/01-go-comparison.md`](../dami/01-go-comparison.md)（含 Go 动态代理能力分析 §2.8）
