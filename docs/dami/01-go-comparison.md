# DamiBus 与 Go 生态同类库对比分析

> 本文是「DamiBus 迁移到 Go」调研的第一部分。源码位于 `/Users/airhead/WorkSpace/goldsyear/damibus`（`org.noear:dami2` 2.0.5，作者 noear，与 Solon 同源）。
> 第二部分为迁移设计文档 [`02-migration-design.md`](./02-migration-design.md)。

---

## 1. DamiBus 是什么

**一句话定位**：本地（单体、单进程）多模块之间的**过程调用 / 事件总线**框架，核心卖点是**解耦**——尤其是未知模块、隔离模块、领域模块之间的解耦，被官方称为「DDD 开发的良配」。

它把三件事揉进同一个抽象（`DamiBus`）：

| 模式 | 入口 API | 语义 | 对标概念 |
|------|----------|------|----------|
| **事件分发（send）** | `bus.send(topic, payload)` | 广播 / 发布订阅，**无返回值** | EventBus / Pub-Sub |
| **请求-响应（call）** | `bus.call(topic, data) → CompletableFuture<R>` | 一个请求，一个答复，可异步、可传导异常 | RPC（同步）/ Promise |
| **响应式流（stream）** | `bus.stream(topic, data) → Publisher<R>` | 一个请求，**多个**流式答复 | Reactive Streams |
| **本地过程调用（lpc）** | `lpc.createConsumer(iface)` / `lpc.registerProvider(impl)` | 用**接口代理**把上面三种能力包成"像 RPC"的本地调用 | RPC Client/Server Stub |

> 关键：`call` 和 `stream` 并不是独立实现，而是 `send` 的**语法糖**——它们构造特殊的 `Payload`（`CallPayload` / `StreamPayload`），把"接收器 sink"塞进 payload 本身，再走同一条 `send` → `dispatch` → `listen` 管道。`lpc` 又是 `call` 的上层包装。

### 1.1 核心能力清单（建立对比维度）

源码层面（`dami2/src/main/java/org/noear/dami2/`）确认的能力：

1. **事件分发 / 广播**：一个 topic 可挂多个 listener，`send` 同步遍历全部调用。
2. **请求-响应（call）**：`CompletableFuture<R>`，支持等待 `.get()` 与回调 `whenComplete`。
3. **响应式流（stream）**：基于 `org.reactivestreams`（Publisher/Subscriber），可对接 Reactor/Flux。
4. **本地过程调用（lpc）**：`Proxy.newProxyInstance` 动态代理接口；反射注册 provider 方法。
5. **事务传导 / 异常透传**：`send` 是**同步**分发，listener 抛出的异常会原样向上抛给发送方（`EventDispatcherDefault.doDistribute` 不吞异常），等同栈内调用 → 天然事务一致。
6. **拦截器（AOP）**：`EventInterceptor` + `InterceptorChain`，可拦截任意事件、可短路。
7. **监听者排序**：`listen(topic, index, listener)`，按 `index` 升序排列（`EventListenPipeline` 每次 add 后 sort）。
8. **附件传递（attach）**：`Event` 携带 `Map<String,Object>`，多个 listener 共享同一个 event，可相互读写数据协作。
9. **主题路由器可插拔**：默认 `HashTopicEventRouter`（map 直查，最快）；可换 `PathTopicEventRouter`（`*`/`**` 通配，正则实现）；`TagTopicEventRouter`（`:` 分隔 topic 与 tag，`,` 分隔多 tag）。接口 `EventRouter` 可自行实现。
10. **调度器可插拔**：`EventDispatcher` 接口，默认实现包含拦截链 + 预检 + 分发，可替换为带监控的版本（demo17 `TopicDispatcherMonitor`）。
11. **事件工厂 / 编解码器可插拔**：`EventFactory`、`Coder`（参数名对齐 `CoderForName` vs 参数下标 `CoderForIndex`）。
12. **fallback（应急处理）**：`send(topic, payload, fallback)`，当**无任何订阅者**时执行 fallback。
13. **处理标识 handled**：`Result`/`Event` 带 `getHandled()`，区分"有监听者处理了"还是"空跑"。
14. **泛型类型安全**：`Event<P>`、`CallPayload<D,R>`、`StreamPayload<D,R>`。
15. **IoC 集成**：`@DamiTopic` 注解（Solon/Spring Boot starter），接口→自动建消费者代理，类→自动注册 provider/listener，随容器生命周期自动注销。
16. **纯进程内**：不跨网络、不跨进程，吞吐标称 5000 万/秒。

---

## 2. Go 生态同类库逐一分析

### 2.1 Go 语言原语（标准库）

Go 把"消息传递"当作一等公民，但**不提供任何框架级抽象**：

| 原语 | 能对应 DamiBus 的什么 | 局限 |
|------|---------------------|------|
| `chan` | stream / call 的底层载体 | 裸的，没有 topic 路由、广播、拦截器 |
| `context.Context` | call/stream 的取消与超时传导 | 只管取消，不管"发给谁" |
| `reflect` | provider 端反射注册方法（lpc 提供者侧） | **只能调用，不能创建类型/实现接口**（见 2.8） |
| `sync` / `errgroup` | 多 listener 广播的并发控制 | 无 |
| `net/rpc` | 进程内 RPC | 已被官方冻结（frozen），不推荐新项目用 |

> 结论：标准库能拼出 50% 的 DamiBus，但要自己造路由、拦截、sink、泛型包装、生命周期——这正是 DamiBus 的核心价值所在。

### 2.2 `asaskevich/EventBus` — 最经典的轻量事件总线

- **能力**：`Subscribe`/`Publish`，支持 `SubscribeAsync`（异步，内部用 goroutine 池）、`SubscribeOnce`。
- **匹配方式**：topic 是**精确字符串**，无通配符、无 path/tag。
- **类型**：handler 用 `interface{}` + reflect 调用，**无编译期泛型保证**。
- **对应 DamiBus**：只覆盖 **send（事件分发）** 的子集。
- **缺口**：无 call（请求-响应）、无 stream（响应式流）、无 lpc、无拦截器、无 index 排序、无 attach、无路由器可插拔、无 fallback、无 handled 标识。
- 维护活跃度一般，但 star 多、用得广。

### 2.3 `gookit/event` — 事件管理 + 通配符 + 优先级

- **能力**：`On`/`Fire`，支持 `app.*` 通配符匹配、监听器**优先级**（priority）、事件分组、`MustAdd`/`On` 链式。
- **对应 DamiBus**：覆盖 send，且**多了一点点 DamiBus 的 Path/Tag 路由味道**（通配符）和**监听者排序**（priority ≈ index）。
- **缺口**：仍无 call / stream / lpc / 拦截器 / attach / 可插拔路由器。payload 也是 `interface{}`，无泛型。
- 比 asaskevich 更接近 DamiBus 的"广播"语义，但同样止步于事件分发。

### 2.4 `ThreeDotsLabs/Watermill` — 事件驱动应用框架（最重、最全）

- **能力**：统一抽象 `Publisher` / `Subscriber` / `Message`（UUID+Metadata+Payload）/ `Router`（handler 接收消息、可做转换/过滤）/ **中间件链** / poison queue / retry / 指标。
- **后端**：`GoChannel`（**进程内**）、Kafka、NATS、SQL、Redis、AMQP、GCP……一套接口，换 broker。
- **对应 DamiBus**：
  - ✅ 事件分发（send）—— `GoChannel` 后端就是进程内 pub/sub
  - ✅ 拦截器 / 中间件 —— Router middleware（思路与 DamiBus `EventInterceptor` 相近）
  - ✅ 可插拔后端 —— 对应 DamiBus 可插拔 router/dispatcher
  - ❌ **请求-响应（call）不是一等公民**——官方明确需自行用 correlation ID 在 pub/sub 之上拼装
  - ❌ 响应式流（stream）—— 不直接对齐 Reactive Streams
  - ❌ lpc —— 无
  - ❌ 同步异常透传 / 事务传导 —— 它的定位是**异步**消息流，handler 在 worker goroutine 跑，异常靠 middleware/retry 处理，**不会抛回发布者调用栈**
- **定位差异**：Watermill 面向「event-driven / 消息流 / 可换 broker 的应用骨架」，DamiBus 面向「**进程内、同步、强类型、可当 RPC 用的解耦总线**」。Watermill 更像把 Kafka/NATS 抽象成库；DamiBus 更像「不需要网络的事件 RPC」。

### 2.5 `ReactiveX/RxGo` — 响应式流（仅覆盖 stream 维度）

- **能力**：Observable/Flowable + 大量操作符（map/filter/merge/...），对齐 Reactive Streams 语义，背压。
- **对应 DamiBus**：只覆盖 **stream** 维度，且是**独立的响应式框架**，不带 topic 路由、不带 send/call、不带 lpc。
- **关系**：DamiBus 的 stream 端**可以选择对接 RxGo**（正如 Java 版对接 Reactor Flux），但两者不能互相替代。
- 注意：rxgo 维护节奏偏慢，Go 社区响应式生态整体不如 Java/RxJS 成熟。

### 2.6 NATS / NSQ — 跨进程消息（仅作参照）

- **NATS**：天然支持 `Request`（请求-响应）、`Publish/Subscribe`、`QueueSubscribe`，能力面甚至**比 DamiBus 的 bus 部分更广**。
- 但它们是**跨进程 / 跨网络**的消息中间件，需要 broker server，与 DamiBus「**零依赖、纯进程内、5000 万/秒**」的定位完全不同。属于另一层问题（分布式消息 vs 进程内解耦），不在等价比较范围内。

### 2.7 gRPC / rpcx / net-rpc — RPC 框架（仅作参照）

- **gRPC**（protoc 生成 stub）/ **rpcx**（code-gen + 反射）：跨进程 RPC，代码生成强类型 client/server。
- **与 DamiBus lpc 的关系**：lpc = **L**ocal **P**rocedure **C**all，明确是**本地的、不走网络**的"RPC 风格"。gRPC 这类是真正跨网络的；net/rpc 是进程内+跨进程但已 frozen。
- **可借鉴点**：gRPC 的 **code-gen stub 模式**，正是 Go 里实现 DamiBus「消费者接口代理」的现实出路（见 2.8 与设计文档）。

### 2.8 Go 的动态接口代理能力 —— 最关键的差异点

DamiBus 的 `lpc.createConsumer(iface)` 依赖 Java 的 `java.lang.reflect.Proxy`：**运行时动态实现任意接口**，把方法调用翻译成 `bus.call(...)`。

**Go 做不到这一点，且 Go 团队已明确拒绝添加该特性。**

- Go 的 `reflect`：能**读取**类型信息、能 `reflect.Value.Call` 调用已有方法，但**无法在运行时新建一个实现了任意接口的类型**。
- 官方提案 [golang/go#41897](https://github.com/golang/go/issues/41897)（Java 式 dynamic proxy）长期未接受，核心理由：会**绕过 Go 的静态类型检查**，违背语言哲学。
- 社区共识（Go Forum / Reddit）：除了**代码生成**（编译期产出 stub），没有运行时动态代理。

> 这意味着：**DamiBus 的 lpc（消费者代理）无法在 Go 中 1:1 移植**。这是迁移的最大设计约束，也是第二份文档的重头戏。
>
> 但要注意：**provider 侧**（反射读取方法 → 注册 listener）在 Go 里**完全可行**——`aifei-go` 自己的 `server.Register()` 已经证明了这一点（反射 struct 的导出方法映射到路由）。难的只有 consumer 侧。

---

## 3. 能力覆盖矩阵

> 图例：✅ 原生一等支持；🟡 可实现但非原生 / 需自己拼；❌ 不支持 / 定位外。

| 能力维度 | DamiBus | asaskevich/EventBus | gookit/event | Watermill | RxGo | NATS | gRPC | Go 原生(chan/ctx/reflect) |
|----------|:-------:|:-------------------:|:------------:|:---------:|:----:|:----:|:----:|:-------------------------:|
| 事件分发 send（广播） | ✅ | ✅ | ✅ | ✅(GoChannel) | 🟡 | ✅ | ❌ | 🟡(chan 自造) |
| 请求-响应 call | ✅ | ❌ | ❌ | 🟡(自拼 corr-id) | 🟡 | ✅ | ✅(unary) | 🟡(chan+resp) |
| 响应式流 stream | ✅ | ❌ | ❌ | 🟡 | ✅ | 🟡 | ✅(streaming) | 🟡(chan) |
| 本地过程调用 lpc（接口代理） | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ | 🟡(codegen) | ❌(语言限制) |
| 同步异常透传 / 事务传导 | ✅ | 🟡 | ❌ | ❌(异步) | ❌ | ❌ | 🟡 | 🟡 |
| 拦截器 AOP | ✅ | ❌ | ❌ | ✅(middleware) | ❌ | 🟡(拦截器) | ✅(interceptor) | 🟡(wrapper) |
| 监听者排序 index/priority | ✅ | ❌ | ✅(priority) | 🟡 | ❌ | ❌ | ❌ | 🟡(slice sort) |
| 附件 attach（多监听协作） | ✅ | ❌ | 🟡 | ✅(Metadata) | ❌ | ✅(header) | ✅(metadata) | 🟡(map) |
| 可插拔路由 hash/path/tag | ✅ | ❌ | 🟡(通配) | ✅(router) | ❌ | ✅(subject) | ❌ | 🟡 |
| 可插拔调度器/工厂/coder | ✅ | ❌ | ❌ | 🟡 | ❌ | ❌ | ✅(codec) | 🟡 |
| fallback（无订阅应急） | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | 🟡 |
| handled 处理标识 | ✅ | ❌ | ❌ | ✅(ack) | ❌ | ✅ | ❌ | ❌ |
| 泛型类型安全 | ✅ | ❌(`any`) | ❌(`any`) | ❌(`any`) | 🟡 | ❌ | ✅(生成) | ✅(1.18+) |
| IoC 注解集成 | ✅(@DamiTopic) | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ |
| 跨进程 / 跨网络 | ❌(纯进程内) | ❌ | ❌ | ✅(多 broker) | ❌ | ✅ | ✅ | ❌ |
| 零外部依赖 | ✅ | ✅ | 🟡 | ❌(重) | ❌ | ❌(broker) | ❌(protobuf) | ✅(stdlib) |

**读法**：

- 没有**任何一个 Go 库**能单列匹配 DamiBus 这一整行「✅」。
- 最接近的组合是 **Watermill（send + middleware + 可插拔后端）+ RxGo（stream）+ gRPC-style codegen（lpc 消费者）**，但仍凑不出 DamiBus 的 **call 语义、同步异常透传、handled、fallback**，且三个库加起来依赖很重。
- DamiBus 的**独特生态位**：**进程内 + 强类型 + 同步可当 RPC 用 + 解耦 + 零依赖**。Go 生态在这个位上是空白。

---

## 4. 结论

### 4.1 Go 生态现状判断

1. **Go 没有与 DamiBus 等价的库。** 最接近的 Watermill 走的是「异步消息流 / 可换 broker」路线，与 DamiBus「同步调用语义 / 纯进程内 / 解耦」是两个生态位。
2. Go 的强项是 **channel + context** 这套原语，社区普遍倾向**直接用 channel 而非引入事件总线框架**（见 Reddit/LinkedIn 上对 in-memory bus "hidden pitfalls" 的讨论）——但这只在小范围内成立；一旦需要 **topic 路由、请求-响应、多模块解耦、拦截、监听者排序、附件协作**，channel 原语就不够用了，而这正是 DamiBus 解决的问题。
3. **lpc（消费者接口代理）是迁移的硬约束**：Go 语言层面做不到运行时动态代理，必须用 **code-gen** 或 **泛型助手**替代；但 **provider 侧反射注册完全可行**。

### 4.2 迁移到 Go 的现实路径（引出第二份文档）

| DamiBus 能力 | Go 迁移出路 |
|--------------|------------|
| send（事件分发） | map[topic][]listener + 同步遍历，**直接可移植** |
| call（请求-响应） | 自造 `Future[R]`（chan + ctx）作为 sink，**可移植** |
| stream（响应式流） | 用 `chan R` + ctx 表达背压（不引入 rxgo），**可移植（语义略改）** |
| lpc provider（反射注册） | `reflect` 注册方法 → listener，**可移植**（aifei-go `server.Register` 已验证） |
| lpc consumer（接口代理） | **改方案**：code-gen stub（推荐）或泛型 `Call[R](ctx, topic, req)` 助手 |
| 路由器 hash/path/tag | map / 正则 / 字符串解析，**直接可移植** |
| 拦截器链 | 复用 `aifei.Handler`/`Interceptor` 风格的 wrapper 链，**可移植** |
| attach 附件 | `map[string]any`，**直接可移植** |
| 泛型 | Go 1.18+ 泛型（项目要求 1.26），**直接可移植** |
| 异常透传 | Go 用 `error` 返回 + `panic/recover`，**语义需调整** |
| @DamiTopic IoC | Go 无注解，改用 **`init()` 自注册 / code-gen / 配置式** |

**总体结论**：DamiBus 约 85% 可直接、忠实迁移到 Go；**唯一无法 1:1 的是 lpc 的消费者代理**，需用 code-gen / 泛型助手替代——这同时也是一个机会：Go 版可以比 Java 版更**类型安全**（编译期检查而非运行时反射）。

详细设计见 [`02-migration-design.md`](./02-migration-design.md)。

---

## 参考资料

- DamiBus 仓库（本分析对象）：`/Users/airhead/WorkSpace/goldsyear/damibus`，官网 https://solon.noear.org/article/damibus
- [asaskevich/EventBus](https://github.com/asaskevich/eventbus)
- [gookit/event](https://github.com/gookit/event)
- [ThreeDotsLabs/watermill](https://github.com/ThreeDotsLabs/watermill) · [官网](https://watermill.io)
- [ReactiveX/rxgo](https://github.com/reactivex/rxgo)
- [Go 动态代理提案 golang/go#41897](https://github.com/golang/go/issues/41897) · [Go Forum: Java Style Dynamic Proxies](https://forum.golangbridge.org/t/java-style-dynamic-proxies-in-go/20777)
- [In-Memory Message Bus Hidden Pitfalls（LinkedIn）](https://www.linkedin.com/posts/dcomartin_in-memory-message-bus-might-seem-like-a-quick-activity-7295560884670607360-L1Np)
