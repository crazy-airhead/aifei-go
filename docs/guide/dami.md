# Aifei-Go dami：进程内事件总线

> **四种交互模式 + 零外部依赖。**`send`（广播）/ `call`（请求-应答）/ `stream`（流式）/ `lpc`（本地过程调用）共用同一条 `Router → Dispatcher → Interceptor 链 → Listener` 管道；Java DamiBus 的忠实 Go 移植。

---

## 1. 背景与定位

在单体应用内部，模块之间常常需要互发消息或互调方法——订单下单后通知库存、用户注册触发邮件 + 积分、未知/隔离模块之间低耦合通信。直接用 `chan` 能拼出 50% 的 EventBus，但一旦需要 **topic 路由、请求-响应、流式、监听者排序、附件协作、拦截器**，原语就不够用了。

`dami`（移植自 Java Solon 系的 [DamiBus](https://solon.noear.org/article/damibus) `dami2 2.0.5`，本仓库 `./dami` 模块）填补的就是这个空白：一个**进程内、同步、强类型、可当 RPC 用的解耦总线**。它与 nami / kafka 形成三层互补：

| 模块 | 职责 | 跨进程？ |
|------|------|----------|
| `dami` | 进程内事件总线 / 本地 RPC | 否 |
| [nami](nami.md) | HTTP RPC 客户端（跨进程） | 是 |
| `plugins/kafka` | 跨进程分布式消息 | 是（broker） |

`dami` 仅依赖 Go 标准库（`sync` / `reflect` / `context` / `slices` / `regexp`），约 1,287 行。代码生成层（`tools/damigen`）和插件层（[plugins/dami](dami-plugin.md)）独立成模块，`dami` 自身保持零依赖。

### 1.1 四种交互模式速览

| 模式 | 入口 API | 语义 | Java 对标 |
|------|----------|------|-----------|
| **send** | `Send[P](topic, payload, fallback...)` | 广播 / 发布订阅，**无返回值** | `bus.send(topic, payload)` |
| **call** | `Call[D, R](topic, data)` → `*Future[R]` | 一个请求，一个答复（可异步） | `bus.call(topic, data)` 返回 `CompletableFuture` |
| **stream** | `Stream[D, R](ctx, topic, data)` → `<-chan StreamItem[R]` | 一个请求，**多个**流式答复 | `bus.stream(topic, data)` 返回 `Publisher` |
| **lpc** | `lpc.RegisterProvider(impl)` + `Call1[R](...)` | 用反射把结构体方法暴露成"本地 RPC" | `DamiLpc.registerProvider` + `createConsumer` |

关键洞察：`call` 和 `stream` **并不是独立实现**，而是 `send` 的语法糖——它们构造特殊的 payload（`RequestPayload[D]`），把"接收器 Sink"塞进 payload 本身，再走同一条 `Send → Dispatch → Listen` 管道。`lpc` 又是 `call` 的上层包装。

---

## 2. 总体架构

```
               ┌──────────────────────────────────────────────┐
   应用代码 ──▶│ 包级便捷 API：Send/Listen/Call/Stream/ListenXxx │
               │   操作 defaultBus（对应 Java Dami.bus()）       │
               └──────────────────────┬───────────────────────┘
                                      │ SendOn/ListenOn/CallOn/StreamOn 显式传 *Bus
               ┌──────────────────────┴───────────────────────┐
   Bus 实例   │ Bus{ router, dispatcher }                      │
               │   Router()/Intercept()/UnlistenAll()/Stop()   │
               └──────────────────────┬───────────────────────┘
                                      │ dispatcher.Dispatch(ev, router)
               ┌──────────────────────┴───────────────────────┐
   调度层     │ Dispatcher：① Router.Match(topic)              │
               │   ② Interceptor 链（按 index 升序）           │
               │   ③ distribute 按序调 listener，首错即止       │
               │   ④ 全部命中后 markHandled                    │
               └──────────────────────┬───────────────────────┘
                                      │
            ┌─────────────────────────┴────────────────────────┐
   路由层   │ Router（hash 默认 / path 通配 / tag 标签）         │
            │   Match(topic) → []*holder（按 index 排序）        │
            └─────────────────────────┬────────────────────────┘
                                      │ holder.handle(ev) → 类型断言 *Event[P] → Listener[P]
            ┌─────────────────────────┴────────────────────────┐
   监听层   │ Listener[P] func(*Event[P]) error                 │
            │   P 的类型在 Send/Listen 两侧约定一致（运行时断言） │
            └──────────────────────────────────────────────────┘

   call/stream：payload 是 *RequestPayload[D]
      └─ Sink（Future[R] / chan StreamItem[R]）由发送方创建并塞进 payload
         listener 通过 Sink.Next / Sink.Complete 回复，与 send 同管道
```

核心类型一览：

| 类型 | 文件 | 职责 |
|------|------|------|
| `Bus` | `bus.go` | 总线实例（具体 struct，非 interface） |
| `Event[P]` | `event.go` | 事件载体（Topic/Payload/Attach/handled） |
| `Sink` | `event.go` | call/stream 的回复通道（Next/Complete） |
| `Listener[P]` / `holder` | `listener.go` | 类型化监听器 / 类型擦除的内部记录 |
| `Router` / `HashRouter` / `PathRouter` / `TagRouter` | `router*.go` | 主题路由（默认 hash） |
| `Interceptor` / `Dispatcher` | `interceptor.go` / `dispatcher.go` | 拦截链 + 调度器 |
| `Future[R]` | `future.go` | call 的答复句柄 |
| `RequestPayload[D]` | `payload.go` | call/stream 的统一荷载 |
| `StreamItem[R]` / `StreamSink[R]` | `stream.go` | 流式元素 + 流 sink |
| `Coder` / `CoderForIndex` | `coder.go` | lpc 参数↔payload 编解码 |
| `Lpc` | `lpc.go` | provider 反射注册 |

---

## 3. 关键 API 与核心约定

### 3.1 Bus 与包级泛型函数

Go 既不允许接口方法带类型参数，**也不允许 struct 方法带泛型**——这是与 Java 的关键差异。因此 `Bus` 是具体 struct，只承载非泛型操作（`Router`/`Intercept`/`UnlistenAll`/`Stop`）；泛型 API 落在**包级函数**上：

```go
// bus.go —— Bus 是具体 struct（零值不可用，用 New）
type Bus struct {
    router     Router
    dispatcher Dispatcher
}

func New(opts ...Option) *Bus   // 默认 HashRouter + 默认 Dispatcher
func (b *Bus) Router() Router
func (b *Bus) Intercept(index int, it Interceptor)
func (b *Bus) UnlistenAll(topic string)
func (b *Bus) Stop()                                  // 清空所有 listener，供 shutdown 用

// 包级泛型：On 变体显式传 *Bus，无 On 变体操作 defaultBus
func SendOn[P any](b *Bus, topic string, payload P, fallback ...func(P)) (*Event[P], error)
func ListenOn[P any](b *Bus, topic string, listener Listener[P], index ...int) (unlisten func())

func Send[P any](topic string, payload P, fallback ...func(P)) (*Event[P], error)        // defaultBus
func Listen[P any](topic string, listener Listener[P], index ...int) (unlisten func())  // defaultBus
```

`dami.go` 提供 `Default()` 取默认总线、`Configure(opts...)` 用新选项重建默认总线、`SetDefaultBus(b)` 替换默认总线（[plugins/dami](dami-plugin.md) 就是用它把自己的总线挂上去）。

### 3.2 Event 与类型约定

```go
type Event[P any] struct {
    Topic   string
    Payload P
    Attach  map[string]any   // 多 listener 共享，可相互读写协作
    handled bool             // dispatch 全部命中后置位
}

func (e *Event[P]) Handled() bool                  // 是否有 listener 处理
func (e *Event[P]) AttachMap() map[string]any      // 懒初始化 attach map
```

所有命中的 listener 收到**同一个 `*Event[P]` 指针**——可以经 `Attach` 在多个 listener 之间传递中间结果。P 的类型一致性是**运行时约定**：发送方和监听方必须约定同一个 P，否则 dispatch 时的类型断言会 panic（行为对齐 Java 的 `ClassCastException`）。

### 3.3 最小示例

```go
import "github.com/crazy-airhead/aifei-go/dami"

type User struct { ID int; Name string }

// 监听（返回 unlisten 函数，defer 调用即注销）
un := dami.Listen("user.created", func(e *dami.Event[User]) error {
    log.Printf("user: %+v", e.Payload)
    return nil
})
defer un()

// 发送
ev, err := dami.Send("user.created", User{ID: 42, Name: "alice"})
fmt.Println(ev.Handled(), err) // true <nil>

// 无监听者 → fallback
dami.Send("orphan.topic", "hi", func(p string) {
    log.Println("no listener, fallback:", p) // 走这里
})
```

---

## 4. send 模式：广播 / 发布订阅

最基础的模式：一个 topic 可挂多个 listener，`Send` **同步**遍历全部调用，listener 按 `index` 升序执行。

```go
// 三个监听者，按 index 升序执行
dami.Listen("order.created", fn1, 0)
dami.Listen("order.created", fn2, 10) // 晚于 fn1
dami.Listen("order.created", fn3, 5)  // 介于 fn1 和 fn2 之间

dami.Send("order.created", order)
// 执行序：fn1 → fn3 → fn2
```

| 特性 | 语义 |
|------|------|
| **同步分发** | listener 在 `Send` 调用栈内执行；异常透传等同栈内调用 |
| **首错即止** | 任一 listener 返回非 nil error，后续 listener 不再执行，error 由 `Send` 返回（事务传导语义） |
| **handled 标识** | 至少一个 listener 命中 → `ev.Handled()==true`；否则 `Send` 执行 fallback |
| **fallback** | `Send(..., fallback)` 仅在**无任何监听者**时执行（非错误处理） |
| **空 topic 检查** | `assertTopic` 对空 topic panic（编程错误，非运行时条件） |
| **附件 Attach** | 多 listener 共享 `ev.Attach` map，可读写协作（如累计结果） |

`pipeline`（`listener.go`）内部 `slices.SortFunc` 每次 add 后重排，`snapshot()` 在 dispatch 前拷一份 holders slice——确保 listener 并发 add/remove 时 dispatch 不 race。

---

## 5. call 模式：请求-应答

一个请求，一个答复。答复通过 `Future[R]`（对标 Java `CompletableFuture<R>`）异步返回：

```go
// 服务端：注册 handler
dami.ListenCall[CreateOrderReq, OrderID]("order.create",
    func(req CreateOrderReq) (OrderID, error) {
        return createOrder(req) // (R, error)
    })

// 客户端：发送请求（注意 Call 本身不传 ctx——ctx 在 Get 时控制等待）
fut := dami.Call[CreateOrderReq, OrderID](
    "order.create", CreateOrderReq{SKU: "A", Qty: 2})

id, err := fut.Get(ctx)         // 阻塞等待，ctx 取消返回 zero + ctx.Err()
// 或异步：fut.Then(func(id OrderID, err error) { ... })
```

### Future[R]

```go
type Future[R any] struct { ... }

func NewFuture[R]() *Future[R]
func (f *Future[R]) Get(ctx context.Context) (R, error)  // 阻塞，ctx 取消返回 zero + ctx.Err()
func (f *Future[R]) Done() <-chan futureResult[R]        // 用于 select
func (f *Future[R]) Then(fn func(R, error))              // 异步回调
func (f *Future[R]) Resolve(val R, err error)            // 手动 settle（fallback / 测试用）
```

关键语义：

- **第一个答复胜出**：`settle` 用 `select { case f.done <- r: default: }`，后续 Next/Complete 被丢弃（幂等 settle）
- **同步 handler 时 Future 已 settled**：`Call` 同步 dispatch，若 handler 在 `ListenCall` 回调内同步 reply，`Get` 立即返回
- **异步 handler**：handler 把 Sink 存起来稍后 reply，`Get(ctx)` 阻塞直到答复或 ctx 取消
- **类型不匹配不 panic**：`futureSink.Next` 对类型断言失败返回 error 而非 panic（比 Java 友好）
- **空 topic panic**：`assertTopic` 检查

### 实现机制

`Call[D, R]` 内部：

```go
fut := NewFuture[R]()
ev := &Event[*RequestPayload[D]]{
    Topic:   topic,
    Payload: &RequestPayload[D]{
        Data: data,
        Sink: &futureSink[R]{fut: fut}, // sink 把答复桥回 Future
    },
}
b.dispatcher.Dispatch(ev, b.router)
```

`ListenCall` 注册的 listener 收到 event 后调用 `handler(data)`，成功则 `sink.Next(r)`、失败则 `sink.Complete(err)`。**call 和 send 走完全相同的管道**，只是 payload 多塞了一个 Sink。

---

## 6. stream 模式：流式

一个请求，**多个**流式答复。Go 拥抱 channel：`Stream` 返回 `<-chan StreamItem[R]`，handler 通过 `StreamSink[R]` 推送。

```go
// 服务端：流式 handler
dami.ListenStream[Query, Row]("db.scan", func(q Query, sink dami.StreamSink[Row]) {
    defer sink.Complete(nil) // 必须！否则 channel 不关
    for _, row := range db.Scan(q) {
        sink.Next(row)       // 每行推一个
    }
})

// 客户端
ch := dami.Stream[Query, Row](ctx, "db.scan", Query{Table: "users"})
for it := range ch {       // handler Complete 后 channel 关闭，range 退出
    if it.Err != nil { return it.Err }
    process(it.Val)
}
```

### StreamItem[R] / StreamSink[R]

```go
type StreamItem[R any] struct {
    Val R
    Err error // 非 nil 时是终结错误，此为最后一项
}

type StreamSink[R any] struct{ s Sink }
func (ss StreamSink[R]) Next(v R)               // 推一项
func (ss StreamSink[R]) Complete(err error)     // 结束流；err 非 nil → 最后一项带 Err
```

### 背压与取消

Java DamiBus 用 Reactive Streams 的 Publisher/Subscriber 协议；dami 用**有界 channel + context** 等价实现：

- **有界缓冲**：`make(chan StreamItem[R], 16)`——慢消费者阻塞生产者，自然背压
- **ctx 取消**：ctx Done 时 `streamSink.cancel()` 关闭 channel（不推错误项，消费者通过 `ctx.Err()` 得知）
- **幂等 Complete**：`sync.Once` 保护，多次调用安全
- **类型不匹配**：`Next` 对错误类型生成一条带 Err 的终结项而非 panic

`StreamOn` 内部 `go func() { <-ctx.Done(); sink.cancel() }()` 把 ctx 生命周期绑到 stream——消费者超时/取消时生产者能及时收尾。

---

## 7. lpc 模式：本地过程调用

`lpc`（**L**ocal **P**rocedure **C**all）把结构体的方法暴露成"像 RPC"的本地调用——形态像 RPC，但**完全在进程内**，不走网络。这是 dami 最高层的封装。

### 7.1 Provider 侧：反射注册

```go
type OrderService struct{}

func (s *OrderService) Create(req CreateOrderReq) (OrderID, error) { ... }
func (s *OrderService) Cancel(id OrderID) error                     { ... }

lpcBus := dami.NewLpc(dami.Default())
lpcBus.RegisterProvider("order", &OrderService{}) // 主题前缀 "order"
// 暴露两个 call listener：
//   order.Create  → dami.CallOn(topic, args...) → Create(req) → reply
//   order.Cancel  → 同上
defer lpcBus.UnregisterProvider(&OrderService{})
```

`RegisterProvider` 反射 provider 的所有导出方法，每个方法变成一个 `ListenCall` 监听者：

- **主题规则**：`topicMapping + "." + Method.Name`（如 `order.Create`）
- **方法签名约定**：Go 习惯 `func(args...) (R, error)` 或 `func(args...) error`；invoke 自动识别尾部 error
- **去重**：同一 provider 类型只能注册一次（重复注册返回 error）
- **coder**：默认 `CoderForIndex`——参数按下标编码成 `{"0": a0, "1": a1, ...}`

### 7.2 Consumer 侧：Call0 / Call1

Java DamiBus 用 `Proxy.newProxyInstance` 动态生成接口代理；**Go 做不到**（[golang/go#41897](https://github.com/golang/go/issues/41897) 已被否决）。dami 提供双轨替代：

**P1 兜底：泛型助手**（无需 code-gen）

```go
// 调用返回值方法
id, err := dami.Call1[OrderID](lpcBus.Bus(), ctx, "order.Create", req)

// 调用仅返回 error 的方法
err := dami.Call0(lpcBus.Bus(), ctx, "order.Cancel", OrderID(42))
```

`Call1[R]` / `Call0` 内部用 `CallOn[map[string]any, R]`，参数按下标编码（与 provider 默认 coder 对齐）。R 必须与 provider 方法的返回类型约定一致。

**P2 默认：code-gen stub**

`tools/damigen`（参见 [damigen-intro](damigen.md)）解析接口定义、用 Enjoy 模板生成强类型 consumer stub——编译期类型检查，优于 Java 的运行时反射代理。这是 Java 版没有的额外优势。

### 7.3 Coder：参数↔payload 编解码

```go
type Coder interface {
    Encode(method reflect.Method, args []any) any
    Decode(method reflect.Method, payload any) ([]any, error)
}

// 默认实现：按下标对齐
type CoderForIndex struct{}
// Encode: args → {"0": a0, "1": a1, ...}
// Decode: method.Type.NumIn()-1 个参数（减去 receiver），按下标取
```

为什么默认用下标而非参数名？因为 **Go 反射拿不到形参名**（Java 需 `-parameters` 编译 flag）——这是 Java→Go 迁移的硬约束。code-gen stub 可以在生成时注入形参名，实现 `CoderForName` 风格的对齐。

---

## 8. 拦截器、路由器与附件

### 8.1 Interceptor：事件级 AOP

```go
// interceptor.go
type Interceptor func(ev eventView, next func() error) error

// eventView 是 Interceptor 看到的"事件视图"——非泛型，能处理任意 P 的事件
type eventView interface {
    viewTopic() string
    viewPayload() any
    viewAttach() map[string]any
    markHandled()
}
```

形式与 aifei 的 `Handler` wrapper 完全一致——`next()` 继续链、返回非 nil error 短路。`chain.proceed` 递归调用直到链尾的 `final`（真正的 distribute）。

- **按 index 升序**：与 listener 排序一致，`slices.SortFunc` 每次 add 后重排
- **短路语义**：interceptor 不调 `next()` → distribute 不执行 → `handled` 保持 false → fallback 触发
- **非泛型 eventView**：interceptor 看到的是事件的非泛型视图（Topic/Payload as `any`/Attach），可处理任意 `P` 类型的事件
- **共享底层事件**：`eventView` 与 `*Event[P]` 指向同一个对象，interceptor 的修改（如写 Attach）会被后续 listener 看到

> **API 现状说明**：`eventView` 接口及其方法目前为包内私有（小写），意味着 `Interceptor` 类型虽对外可见但**目前不便从 `dami` 包外直接实现**——自定义拦截器需要 dami 暴露一个公开的事件视图接口（或提供适配函数）才能优雅使用。当前主要供内部使用与未来扩展（对齐 Java `EventInterceptor` 的预留）。需要请求级 AOP 的应用，可在 `Send`/`Call` 外层套自己的 wrapper 函数达到等价效果。

### 8.2 三种路由器

| Router | 匹配规则 | 适用场景 | 性能 |
|--------|---------|---------|------|
| `HashRouter`（默认） | 精确字符串 map 直查 | 大多数场景 | 最快，O(1) |
| `PathRouter` | `*`（一段）/ `**`（多段）通配，正则实现 | 主题层级订阅（`user.*`） | 通配表达式走 regex |
| `TagRouter` | `topic:tag1,tag2`；topic 相等且 tag 交集 | 同 topic 多维度过滤 | 按 topic 分桶后线性扫 |

```go
// 切换路由器
dami.Configure(dami.WithRouter(dami.NewPathRouter()))

// PathRouter：监听所有 user.* 事件
dami.Listen("user.*", fn1)
dami.Listen("user.**.changed", fn2)  // ** 跨多段

// TagRouter：监听 user.created 带 'vip' 标签
dami.Listen("user.created:vip", fnVip)
dami.Send("user.created", u)                // Send 时不带 tag → 全部 tag listener 都匹配
// 注意：Send/Payload 不携带 tag；tag 在 topic 字符串里 "topic:tag1,tag2"
```

`PathRouter` 对无通配符的表达式走 fast map（`exact` 字段），只有带 `*` 的才落到 `patterns` regex 列表——绝大多数场景保持 O(1)。`.` 和 `/` 都被认可为分隔符。

### 8.3 Attach：多监听者协作

```go
dami.Listen("pipeline", func(e *dami.Event[Data]) error {
    e.AttachMap()["step1"] = step1Result // 后续 listener 可读
    return nil
}, 0)

dami.Listen("pipeline", func(e *dami.Event[Data]) error {
    prev := e.AttachMap()["step1"] // 读前一步的结果
    // ...
    return nil
}, 10)
```

`Attach` 懒初始化（首次 `AttachMap()` 调用时 allocate），多个 listener 共享同一 map——可用来做 pipeline 累计、上下文传递、事务标识等。

---

## 9. Java DamiBus → Go dami 的关键迁移决策

本节总结 `docs/arch/dami/` 两份设计文档的核心决策，详见 [01-go-comparison.md](../arch/dami/01-go-comparison.md) 与 [02-migration-design.md](../arch/dami/02-migration-design.md)。

### 9.1 可忠实迁移的 85%

| Java DamiBus | Go dami | 备注 |
|--------------|---------|------|
| `send`（事件分发） | `SendOn/ListenOn` + HashRouter | 直接迁移，map + 同步遍历 |
| `call`（请求-响应） | `CallOn` + `Future[R]` | 自造 chan+ctx Future 替代 CompletableFuture |
| `stream`（响应式流） | `StreamOn` + `<-chan StreamItem[R]` | 有界 chan 替代 Reactive Streams，背压 = buffer 满阻塞 |
| 路由器 hash/path/tag | 三种 Router 全实现 | map / 正则 / 字符串解析 |
| 拦截器链 | `Interceptor` + `chain` | 复用 aifei `Handler` wrapper 风格 |
| 监听者排序 | `pipeline` + `slices.SortFunc` | 每次 add 后按 index 排序 |
| attach 附件 | `Event.Attach map[string]any` | 直接迁移 |
| handled 标识 | `Event.handled` | dispatch 内置位 |
| fallback | `Send(..., fallback...)` | 仅在 Handled==false 时执行 |
| 泛型 | Go 1.18+ 泛型 | 优于 Java 运行时擦除 |

### 9.2 唯一无法 1:1 的：lpc 消费者代理

Java 的 `lpc.createConsumer(iface)` 用 `Proxy.newProxyInstance` **运行时动态实现任意接口**；Go 团队明确拒绝添加此特性（违背静态类型哲学）。dami 的双轨出路：

| 路径 | 实现 | 优点 | 缺点 |
|------|------|------|------|
| **泛型助手**（Call0/Call1） | `Call1[R](bus, ctx, "topic", args...)` | 零 code-gen，编译期检查 R | 主题字符串仍是运行时约定 |
| **code-gen stub**（`tools/damigen`） | Enjoy 模板生成强类型 consumer | 完全编译期类型安全 | 需要 `go generate` 步骤 |

provider 侧反射注册完全可行（`aifei-go` 的 `server.Register` 已验证此模式）——这是迁移中的"免费午餐"。

### 9.3 其他语义调整

- **异常**：Java `throw` + `InvocationTargetException` → Go `error` 返回值；listener error 直接 return，`Send` 透传（首错即止）
- **形参名**：Java `-parameters` → Go 拿不到；默认 `CoderForIndex`，code-gen 可注入名字
- **注解**：Java `@DamiTopic`（Solon/Spring starter） → Go 无注解，改用 `init()` 自注册 + [plugins/dami](dami-plugin.md) 生命周期
- **Serializable**：Java `Event implements Serializable` → 进程内直接传引用，去掉

---

## 10. 集成方式

### 10.1 直接使用包级默认总线

```go
package main

import "github.com/crazy-airhead/aifei-go/dami"

func init() {
    // 注册监听者
    dami.Listen("user.created", handleUserCreated)
}

func main() {
    // 发送事件
    dami.Send("user.created", User{ID: 42, Name: "alice"})
}
```

### 10.2 与 aifei 应用集成（经 plugins/dami）

[plugins/dami](dami-plugin.md) 把一个 `*dami.Bus` 包装成 `aifei.Plugin`，`Start` 时设为默认总线，`Stop` 时调 `Bus.Stop()` 清理 listener——随应用生命周期一起管理。

```go
import (
    "github.com/crazy-airhead/aifei-go/aifei"
    damiPlugin "github.com/crazy-airhead/aifei-go/plugins/dami"
)

func main() {
    app := aifei.New(
        aifei.WithPlugin(damiPlugin.NewPlugin(nil)), // 注册并管理 Bus 生命周期
    )
    // dami.Send / Listen / Call / Stream 全部走该插件的总线
    server.Run(app, ":8080")
}
```

### 10.3 与 codegen provider 集成

`tools/damigen`（参见 [damigen-intro](damigen.md)）扫描包内带 `//dami:provider <topic>` 注释的 interface，生成强类型 consumer stub（默认输出到 `dami_gen.go`）。

```go
// ordersvc/iface.go —— 声明接口并打 dami:provider 标记
package ordersvc

//dami:provider order
type OrderService interface {
    Create(req CreateOrderReq) (OrderID, error)
    Cancel(id OrderID) error
}
```

```go
//go:generate go run github.com/crazy-airhead/aifei-go/tools/damigen/cmd/damigen -src . -out .
```

生成的 `OrderServiceClient` 把每个方法变成 `dami.Call1[R]` / `dami.Call0` 调用：

```go
// provider 实现
lpcBus := dami.NewLpc(dami.Default())
lpcBus.RegisterProvider("order", &OrderServiceImpl{})

// 强类型 consumer——无字符串 topic，编译期检查参数与返回类型
client := ordersvc.NewOrderServiceClient(lpcBus.Bus())
id, err := client.Create(ctx, CreateOrderReq{SKU: "A", Qty: 2})
```

### 10.4 多总线隔离

```go
// 不同模块用独立总线，互不干扰
orderBus := dami.New(dami.WithRouter(dami.NewPathRouter()))
userBus  := dami.New()

dami.SendOn(orderBus, "order.created", order)
dami.SendOn(userBus,  "user.created", user)
```

---

## 11. 模块结构

```
dami/
├── doc.go              # 包文档 + Quick start（33 行）
├── dami.go             # defaultBus + 包级 Send/Listen/Intercept 便捷 API（44 行）
├── bus.go              # Bus struct + Option + SendOn/ListenOn + Stop（94 行）
├── event.go            # Event[P] + Sink + eventView + assertTopic（76 行）
├── listener.go         # Listener[P] + holder（类型擦除）+ pipeline（68 行）
├── payload.go          # RequestPayload[D]（call/stream 共用，11 行）
├── router.go           # Router 接口（Add/Remove/Match/Count/ClearAll）（21 行）
├── router_hash.go      # HashRouter 默认，map 直查（65 行）
├── router_path.go      # PathRouter 通配 * / **（136 行）
├── router_tag.go       # TagRouter topic:tag1,tag2 交集（128 行）
├── interceptor.go      # Interceptor + chain（对齐 aifei Handler wrapper）（31 行）
├── dispatcher.go       # Dispatcher：拦截链→分发→markHandled（77 行）
├── future.go           # Future[R]（call 答复）+ futureSink（88 行）
├── call.go             # CallOn/Call + ListenCallOn/ListenCall（56 行）
├── stream.go           # StreamOn/Stream + StreamSink + streamSink（136 行）
├── coder.go            # Coder + CoderForIndex（lpc 编解码）（50 行）
├── lpc.go              # Lpc.RegisterProvider（反射注册）+ invoke（141 行）
└── lpc_call.go         # Call0/Call1 泛型助手（32 行）
```

合计约 1,287 行；`go.mod` 声明 `module github.com/crazy-airhead/aifei-go/dami`，零外部依赖。测试（`_test/dami_test/`）覆盖 send/call/stream/lpc 全模式、三种路由器、拦截器、并发基准、plugin 集成。

---

## 12. 总结

Aifei-Go 的 dami 围绕几个核心设计原则构建：

1. **四种模式共用一条管道**：send/call/stream/lpc 并非四套实现，而是同一套 `Send → Dispatch → Listen` 管道上的不同 payload（`RequestPayload[D]` 携带 Sink）；call/stream/lpc 都是 send 的语法糖
2. **Bus 是具体 struct，泛型 API 落在包级函数**：Go 不允许 struct 方法带类型参数；`SendOn/ListenOn/CallOn/StreamOn` 显式传 `*Bus`，无 On 后缀的便捷版操作 `defaultBus`（对齐 Java `Dami.bus()`）
3. **同步分发 + 首错即止**：listener 在 `Send` 调用栈内执行，异常直接 return 透传——天然事务一致；这是与 Watermill/异步消息总线的根本差异
4. **拥抱 channel + context**：stream 用有界 chan 表达背压（不引入 rxgo），call 用 chan+ctx 自造 Future（替代 CompletableFuture）
5. **类型安全优于 Java**：Go 泛型在编译期保留类型；`futureSink` 类型断言失败优雅返回 error 而非 panic
6. **lpc 双轨 consumer**：泛型 `Call1[R]` 兜底（零 code-gen）+ codegen stub 默认（编译期强类型）——化 Java 的硬约束为 Go 的额外优势
7. **零外部依赖 + 可插拔扩展**：纯 Go 标准库；路由器/调度器/coder 都可换；`tools/damigen` 与 `plugins/dami` 独立成模块，核心 `dami` 保持零依赖

这种设计使得 dami 既能作为轻量事件总线用于简单解耦（`dami.Send`/`Listen`），也能扩展成进程内 RPC 框架（`Lpc.RegisterProvider` + codegen consumer），在不引入任何外部依赖的前提下补齐了 aifei-go 生态中"进程内多模块解耦"这一块。

### 延伸阅读

- [aifei-go 总览](aifei-go.md) — 整体模块地图
- [plugins/dami 插件](dami-plugin.md) — 把 `*dami.Bus` 包装为 `aifei.Plugin`，随应用生命周期管理
- [tools/damigen](damigen.md) — 接口→consumer stub 代码生成
- [nami HTTP RPC](nami.md) — 跨进程 RPC 客户端，与 dami 形成三层互补
- [docs/arch/dami/01-go-comparison.md](../arch/dami/01-go-comparison.md) — Go 生态同类库对比与可行性分析
- [docs/arch/dami/02-migration-design.md](../arch/dami/02-migration-design.md) — 详细迁移设计规范

