# Java → Go 逐项对照（flow-go）

> 本文是「Solon-Flow 迁移到 Go」系列的第二篇（语言与生态对照）。
> 前置：[`00-overview.md`](00-overview.md)。
> 目的：把 solon-flow 的**每个关键类型/方法**与**每条 Java 特性**，落到 Go 的等价写法，作为 `02`–`04` 设计的依据。**不写完整实现**，只给签名级对照与取舍说明。

---

## 1. 语言差异总表（对设计的影响）

| 维度 | solon-flow (Java) | flow-go (Go) | 影响 / 取舍 |
|------|-------------------|--------------|------------|
| 接口 + `default` 方法 | `FlowEngine`/`FlowContext`/`FlowDriver`/`FlowInterceptor` 大量 default | ❌ Go 接口无默认方法 | **小接口（只含抽象方法）+ 可嵌入 base struct**；helper 放 base struct 或包级函数 |
| 接口上的 `static` 工厂 | `FlowEngine.newInstance()`、`FlowContext.of()`、`Graph.create()` | 包级函数 `flow.NewEngine()` / `flow.NewContext()` / `flow.NewGraph()` | 命名沿用 Aifei 习惯 |
| `@FunctionalInterface` 单方法接口 | `TaskComponent`/`ConditionComponent`/`Consumer`/`Function`/`BiConsumer` | Go **func 类型**：`type TaskFunc func(Context, *Node) error` 等 | 见 §3.5 |
| 受检异常 `throws Throwable` | 几乎所有 driver/eval/component 方法 | `(T, error)` 返回；`flow.Error` 哨兵 + `%w` 包装 | 见 §6 |
| 泛型 `<T> T getAs(key)` | `Node.getMetaAs`、`Context.GetAs` | `any` + 调用点类型断言；或泛型助手 `GetAs[T any]()` | 见 §3.3 |
| `instanceof` + 强转 | driver `component instanceof TaskComponent` | Go 接口类型断言 `tc, ok := comp.(TaskComponent)` | — |
| `transient`（排除序列化） | 几乎所有模型字段 | `json:"-"` 标签 / 自定义 `MarshalJSON` | — |
| 注解 `@Preview`/`@Internal`/`@Nullable`/`@Deprecated` | 遍布 | 文档注释；`@Deprecated` → `// Deprecated:` | 无运行时影响 |
| `ConcurrentHashMap` | `vars`、`MapContainer`、`Trace` | `map` + `sync.RWMutex`（对齐 `config`） | — |
| `AtomicInteger` | 步数计数、`Temporary.counts` | `atomic.Int64` + `AddInt64`/`LoadInt64` | — |
| `volatile` 布尔 | `interrupted/stopped/reverting`、`ctx.stopped` | `atomic.Bool` | — |
| `CountDownLatch` + `AtomicReference<Throwable>` | `parallel_run_out` | **`errgroup.Group`**（首错优先 + Wait） | 见 §4.4 |
| `ExecutorService`（线程池） | `Driver.getExecutor()` | `func() error` 工作池；nil → 顺序 | — |
| `Stack<Integer>` / `Stack<Iterator>` | inclusive/loop 网关聚合 | 切片当栈（`append` / `s[:len-1]`） | — |
| `Collections.sort` / `Comparable<Link>` | Link priority、`RankEntity` | `sort.Slice`（`less = a.Priority > b.Priority` 降序） | — |
| Java 原生 `Serializable` | `NodeRecord`/`Trace`/`Context` | 显式 JSON marshal（已有 toJson/fromJson） | 去掉 |
| `ResourceUtil.scanResources`（classpath） | `Engine.load("*")` | `//go:embed` + `filepath.Glob` + `os.ReadFile` | 见 `03` |
| snack4 `ONode` 多态（`Write_ClassName`+`Read_AutoType`） | 快照 vars | 限定 JSON 原生类型 / 显式 DTO | 见 `03` |
| 反射 | 引擎内**无**运行时反射（仅 `instanceof`） | 接口断言即可 | 无障碍 |

> **最重要的三条**：①「接口默认方法」→ 小接口 + base struct；②`@FunctionalInterface` → Go func 类型；③异常 → `error` + `flow.Error`。

---

## 2. 图模型对照（Graph / Node / Link / Spec / NodeType）

### 2.1 NodeType

```
// Java：enum NodeType { UNKNOWN(0), START(1), END(2), ACTIVITY(11),
//                       EXCLUSIVE(21), INCLUSIVE(31), PARALLEL(32), LOOP(33) }
```
```go
package flow

type NodeType int

const (
    NodeUnknown   NodeType = 0
    NodeStart     NodeType = 1
    NodeEnd       NodeType = 2
    NodeActivity  NodeType = 11
    NodeExclusive NodeType = 21  // XOR 单选
    NodeInclusive NodeType = 31  // OR  多选 + 汇合
    NodeParallel  NodeType = 32  // AND 全选 + 汇合
    NodeLoop      NodeType = 33  // 循环（$for/$in）
)

func (t NodeType) IsGateway() bool { return t > NodeActivity } // exclusive/inclusive/parallel/loop
func NodeTypeOf(name string, def NodeType) NodeType           // 大小写不敏感；"iterator"→Loop
```
对照：`isGateway(t) = code > ACTIVITY.code`、`nameOf(name, def)`（`"iterator"`→LOOP）一一映射。

### 2.2 Node / NodeSpec

```go
// Java Node：final graph,id,title,type,metas,when(ConditionDesc),task(TaskDesc),nextLinks(sorted desc by priority); +attachment
type Node struct {
    graph    *Graph
    ID       string
    Title    string
    Type     NodeType
    metas    map[string]any          // 冻结（构造后不变）
    when     ConditionDesc
    task     TaskDesc
    nextLinks []Link                 // 按 Priority 降序冻结
    Attachment any                   // 公开扩展槽
}
// 方法：PrevLinks()（懒、扫描 graph.links 反向、reverse）、NextLinks()、NextNode()、
//       PrevNodes()/NextNodes()、GetMeta/GetMetaAsString/GetMetaAsBool/GetMetaAsNumber/HasMeta
```
```go
// Java NodeSpec：可变构建器
type NodeSpec struct {
    ID, Title, When, Task string
    Type                  NodeType
    Meta                  map[string]any
    Links                 []LinkSpec
    WhenComponent         ConditionComponent // 硬编码，优先于 When 字符串
    TaskComponent         TaskComponent      // 硬编码，优先于 Task 字符串
}
// 方法：链式 Title/Meta/MetaPut/LinkAdd/LinkRemove/LinkClear/When/Task；
//       静态 StartOf/EndOf/ActivityOf/InclusiveOf/ExclusiveOf/ParallelOf/LoopOf(id)
```

### 2.3 Link / LinkSpec

```go
// Java Link：final graph,prevId,nextId,title,metas,priority(int,大先),when(ConditionDesc)
type Link struct {
    graph    *Graph
    PrevID, NextID, Title string
    Priority int                // 大者优先
    metas    map[string]any
    when     ConditionDesc
}
// Comparable<Link>：按 Priority 降序 → sort.Slice(less = a.Priority > b.Priority)
```
```go
type LinkSpec struct {
    NextID, Title, When string
    Priority            int
    Meta                map[string]any
    WhenComponent       ConditionComponent
}
// 链式：Title/Meta/MetaPut/When/Priority；Condition(string) 为 When 的弃用别名
```

### 2.4 Graph / GraphSpec

```go
// Java Graph：final id,title,driver(name string,""=默认),metas,nodes(LinkedHashMap),links,start
type Graph struct {
    ID, Title string
    Driver    string            // 选择已注册的 driver 名；空=默认
    Metas     map[string]any
    nodes     map[string]*Node  // 保留插入序（用 ordered map 或 []*Node+index）
    Links     []Link
    start     *Node
}
// 方法：Start()/Node(id)/NodeOrThrow(id)/Nodes()/Links()/AsTask()(→GraphTaskComponent)/
//       ToPlantuml()/ToYaml()/ToJson()/ToMap()
// 工厂（包级）：NewGraph(id[,title[,driver]], func(*GraphSpec)) / CopyGraph(g, func(*GraphSpec)) /
//              GraphFromURI(uri) / GraphFromText(text)
```
```go
// Java GraphSpec：可变构建器；fromDom 逆序自动连边
type GraphSpec struct {
    ID, Title, Driver string
    Meta              map[string]any
    nodes             map[string]*NodeSpec  // 保留插入序
}
// 方法：Create()→*Graph、AddNode/RemoveNode/GetNode/ClearNodes、MetaPut、
//       AddStart/AddEnd/AddActivity/AddInclusive/AddExclusive/AddParallel/AddLoop（各 2 重载：by id / by NamedTaskComponent）
// 静态：Copy(g)、FromURI(uri)、FromText(text)、FromDom(map[string]any)
//   FromDom：读 layout（弃用别名 nodes）；【逆序遍历】对缺 link 的节点自动连到「文档序下一个」；
//            link 值可为：单字符串 / 单对象{nextId,title,meta,when} / 数组(混合)；
//            缺 id 自动生成 "n-{index}"
```

> **Go 注意**：Java 用 `LinkedHashMap` 保插入序；Go 用 `(slice + map)` 或 `ordered_map` 辅助。`fromDom` 的「逆序自动连边」逻辑必须忠实移植（直接影响 YAML 扁平布局的链式简写）。

---

## 3. 上下文 / 组件 / 描述符对照

### 3.1 FlowContext（接口 + 默认实现）

Java 接口方法众多（多数 default 委托 vars map）。Go 拆为：**公开接口 `Context`（只含抽象/关键方法）+ 具体类型 `flowContext`（承载所有 helper）**。

```go
// 抽象接口（小而稳）
type Context interface {
    Vars() map[string]any                 // 单一真源
    Get(key string) any
    GetAs(key string) any                 // 调用点 .(T)
    GetOrDefault(key string, def any) any
    Put(key string, val any) Context      // nil 静默丢弃（与 Java 一致）
    PutIfAbsent(key string, val any) Context
    PutAll(m map[string]any) Context
    Remove(key string)
    ContainsKey(key string) bool
    With(key string, val any, fn func())  // 方法作用域临时变量（save→set→run→restore）
    InstanceID() string                   // = Get("instanceId")
    Trace() *Trace
    LastRecord() *NodeRecord              // 根图最后节点
    LastNodeID() string
    IsStopped() bool
    Stop()                                // 委托 Exchanger.Stop()
    Interrupt()                           // 委托 Exchanger.Interrupt()（仅中断当前分支）
    EventBus() *dami.Bus                  // 懒初始化（复用 ./dami）
    ToJSON() ([]byte, error)              // 快照
}
```

```go
// 内部接口（引擎写）
type contextInternal interface {
    Context
    SetStopped(bool)                      // 跨引擎 stop 传播
    SetExchanger(*Exchanger)              // eval 前后 save/restore（支持嵌套子图）
    Exchanger() *Exchanger
}
```

```go
// 具体实现
type flowContext struct {
    mu        sync.RWMutex
    vars      map[string]any   // 构造时种入 instanceId + context(自身，序列化时剔除)
    trace     *Trace
    exchanger *Exchanger       // 引擎设置
    eventBus  *dami.Bus        // 懒
    stopped   atomic.Bool
}
```

**`With` 方法作用域**（关键，工作流重度依赖）：
```go
func (c *flowContext) With(key string, val any, fn func()) {
    bak, had := c.vars[key]
    c.Put(key, val)
    defer func() { if had { c.vars[key] = bak } else { delete(c.vars, key) } }()
    fn()
}
```
对照 Java `with(key,value,RunnableTx)`：save old → set → run → restore（无旧值则 remove）。Go 去掉 `<X extends Throwable>` 泛型，直接 `func()`（错误由 fn 内部处理或 panic→engine recover）。

### 3.2 vars vs model

完全等价。Java `model()` 是弃用别名（pre-3.8.4）。**Go 只保留 `Vars()`**，不提供弃用别名（Go 没有弃用注解机制，且新模块无需兼容旧名）。

### 3.3 泛型助手（可选便利）

```go
func GetAs[T any](c Context, key string) (T, bool) {
    v := c.Get(key)
    t, ok := v.(T)
    return t, ok
}
```
对照 Java `<T> T getAs(key)`（未检查强转）。Go 显式 `ok` 更安全。

### 3.4 ConditionDesc / TaskDesc

```go
// Java：不可变描述符；description 已 trim；isEmpty = 空描述 且 组件 nil；+attachment
type ConditionDesc struct {
    Description string             // 已 TrimSpace；可带 @ 前缀（运行时派发）
    Component   ConditionComponent // 硬编码（优先）
    Attachment  any
}
func (d ConditionDesc) IsEmpty() bool { return d.Description == "" && d.Component == nil }

type TaskDesc struct {
    Node        *Node
    Description string
    Component   TaskComponent
    Attachment  any
}
func (d TaskDesc) IsEmpty() bool { return d.Description == "" && d.Component == nil }
```
> **注意**：`@`/`#`/`$` 前缀**不**在描述符里解析，而是在 `AbstractDriver` 运行时按前缀派发（见 §4.2）。硬编码 Component 绕过字符串。

### 3.5 组件（func 类型）

```go
// Java @FunctionalInterface
type TaskComponent      func(ctx Context, node *Node) error          // void run(ctx,node) throws Throwable
type ConditionComponent func(ctx Context) (bool, error)              // boolean test(ctx) throws Throwable
type NamedTaskComponent interface {
    TaskComponent
    Name() string
    Title() string                  // 默认 = Name()
}
// GraphTaskComponent：把 *Graph 包成 NamedTaskComponent；Run 调 exchanger.RunGraph(graph)
type graphTaskComponent struct{ g *Graph }
func (c graphTaskComponent) Name() string { return c.g.ID }
func (c graphTaskComponent) Title() string { return c.g.Title }
func (c graphTaskComponent) Run(ctx Context, node *Node) error {
    return ctx.(contextInternal).Exchanger().RunGraph(c.g)
}
```
对照：`ConditionComponent.test(ctx)`（**只取 ctx，无 node**）；`TaskComponent.run(ctx, node)`。一个 bean 不能同时是两者（driver 类型断言，不匹配返回 `flow.Error`）。

### 3.6 Container

```go
// Java：Object getComponent(name)
type Container interface {
    GetComponent(name string) any
}
// MapContainer（默认，纯 Go 友好）
type MapContainer struct {
    mu sync.RWMutex
    m  map[string]any
}
func (c *MapContainer) PutComponent(name string, comp any)   // Task/Condition 都直接存
func (c *MapContainer) RemoveComponent(name string)
func (c *MapContainer) GetComponent(name string) any
```
> **SolonContainer（`Solon.context().getBean`）不迁**：aifei-go 无 IoC。`MapContainer` 为默认；应用在 `Plugin.Start()` 里把组件注册进去。`@beanName` 解析链路不变。

---

## 4. 引擎 / 驱动 / 交换器 / 拦截器对照

### 4.1 FlowEngine

```go
// Java：接口大量 default + static；唯一真实入口 @Internal eval(Graph,Exchanger,Options)
type Engine interface {
    // 注册
    RegisterDriver(name string, d Driver)            // name="" 设默认
    RegisterDefaultDriver(d Driver)
    UnregisterDriver(name string)
    DriverOf(g *Graph) Driver                        // 按 g.Driver 名解析，空=默认

    AddInterceptor(it Interceptor, index int)        // index=rank
    AddInterceptorDefault(it Interceptor)            // = index 0
    RemoveInterceptor(it Interceptor)

    Load(g *Graph)                                   // 注册图
    Unload(id string)
    Graphs() []*Graph
    Graph(id string) (*Graph, bool)
    GraphOrThrow(id string) (*Graph, error)

    // eval 族（全部终结到具体 eval）
    Eval(g *Graph) error
    EvalCtx(g *Graph, ctx Context) error
    EvalSteps(g *Graph, steps int, ctx Context) error
    EvalOpts(g *Graph, steps int, ctx Context, opts *Options) error
    EvalByID(graphID string, opts ...EvalOption) error
}
// 包级工厂：NewEngine(opts ...EngineOption) *engine   // 默认 driver=SimpleDriver、container=MapContainer、eval=EnjoyEvaluation
```
Go 把 Java 的 `default eval(...)` 重载族收敛为几个方法 + `EvalOption`（functional options），避免接口爆炸。

### 4.2 FlowDriver（核心：条件 + 任务派发）

```go
// Java FlowDriver：getExecutor / onNodeStart / onNodeEnd / handleCondition / handleTask(default→postHandleTask)
type Driver interface {
    Executor() Executor                             // nil → 顺序（仅并行网关用）
    OnNodeStart(ex *Exchanger, node *Node)          // 节点开始（revert 时不调）
    OnNodeEnd(ex *Exchanger, node *Node)            // 节点结束
    HandleCondition(ex *Exchanger, cond ConditionDesc) (bool, error)  // 引擎已预检 IsEmpty
    HandleTask(ex *Exchanger, task TaskDesc) error  // 默认 → PostHandleTask
    PostHandleTask(ex *Exchanger, task TaskDesc) error
}
// Executor = func() error 的简单接口（或 *errgroup.Group 工厂）
type Executor interface{ Submit(func() error) }
```

**AbstractDriver（`@/#/$` 派发，必须忠实）**：
```go
type AbstractDriver struct {
    evaluation Evaluation
    container  Container
    executor   Executor
}
// 构造：nil → 默认 EnjoyEvaluation + MapContainer
// 派发优先级（HandleCondition / PostHandleTask 完全对照 Java）：
//
// HandleCondition(cond):
//   1) cond.Component != nil → cond.Component.Test(ctx)
//   2) strings.HasPrefix(desc, "@") → 去 @，container.GetComponent(name) → 必须是 ConditionComponent，断言 → Test(ctx)
//   3) 否则 → evaluation.RunCondition(ctx, desc)   // 表达式
//
// PostHandleTask(task):
//   1) task.IsEmpty() → return nil                // 空任务 no-op
//   2) task.Component != nil → task.Component.Run(ctx, node)
//   3) HasPrefix(desc, "#") → 去 #，engine.GraphOrThrow(id) → exchanger.RunGraph(graph)   // 子图
//   4) HasPrefix(desc, "@") → 去 @，container.GetComponent → 必须是 TaskComponent → Run(ctx,node)
//   5) 否则（含 "$" meta 间接）：
//        - 若 HasPrefix(desc, "$")：取剩余为点分 meta key，深度解析 graph.Metas 得真实脚本；找不到 → flow.Error
//        - 临时把 node 绑入 vars["node"]（Node.TAG），finally 还原 exchanger（任务可能切换它）
//        - evaluation.RunTask(ctx, 脚本)
```
> **关键不变量**：派发优先级、`$` 深度 meta 解析、`node` 绑定 + finally 还原 exchanger——逐一对照。这是脚本/组件可替换性的基石。

**SimpleDriver**：仅 `HandleTask = PostHandleTask`（与接口默认相同，保留为可覆盖点）+ `Builder`（Evaluation/Container/Executor）。

### 4.3 FlowExchanger（单次运行可变状态）

```go
// Java：graph,engine,driver,context,steps(-1=无限),stepCount(AtomicInteger,子图共享),temporary,interrupted/stopped/reverting(默认 true)
type Exchanger struct {
    Graph    *Graph
    Engine   Engine
    Driver   Driver
    Context  contextInternal
    Steps    int64          // -1 = 无限
    stepCount *atomic.Int64 // 跨子图共享（Copy 时共享指针）
    Temporary *Temporary
    interrupted atomic.Bool
    stopped     atomic.Bool
    reverting   atomic.Bool // 默认 true
}
// 方法：
//   Copy(g *Graph) / CopyCtx(g, ctx)        // 浅拷贝，共享 steps+stepCount；新 Temporary；reverting=true
//   RecordNode(g, node) / RecordClear()     // 委托 ctx.Trace()
//   RunGraph(g *Graph) error                // prveSetp(); engine.eval(g, copy); 若未到 END 且未 stop → Interrupt()
//   RunTask(node, desc) error               // engine.DriverOf(node.graph).HandleTask(...)
//   RunScript(script) (any, error)          // enjoy 求值（替代 liquor Scripts.eval）
//   NextStep(node) bool                     // steps<0 → true；否则 stepCount.Add(1) <= steps
//   PrveStep()                              // steps<0 no-op；否则 stepCount.Add(-1)
//   IsStopped() bool                        // stopped || ctx.IsStopped()（跨引擎传播）
//   Stop() / Interrupt() / IsInterrupted() / IsReverting() / SetReverting(bool)
```
> **reverting 机制**：每次新 exchanger 默认 `reverting=true`；引擎从 `graph.Start()` 走到「恢复点 startNode」（trace.lastNode 或 start）途中**跳过 task_exec / 拦截 / 记录 / 步数**，命中后翻 false 恢复正常。详见 `02` 第 6 节。

### 4.4 并行网关（parallel_run_out → errgroup）

Java：`ExecutorService != null && nextNodes.size() >= 2` → `CountDownLatch(n)` + `AtomicReference<Throwable>`（首错优先，兄弟分支短路）；否则顺序。

```go
// Go 等价
func parallelRunOut(ex *Exchanger, opts *Options, node *Node) {
    next := node.NextNodes()
    if ex.Driver.Executor() == nil || len(next) < 2 {
        for _, n := range next { nodeRun(ex, opts, n, startNode) }   // 顺序
        return
    }
    g := new(errgroup.Group)           // 首错优先 + Wait
    for _, n := range next {
        n := n
        g.Go(func() error { nodeRun(ex, opts, n, startNode); return firstErr })
    }
    _ = g.Wait()                       // 错误按 Java 规则包装/透传
}
```
> **注意**：`node_run` 递归会读写共享 `Temporary`（counts/stacks）与 `Context.vars`。并行分支对**同一图的汇合计数器**有数据竞争——Java 用 `ConcurrentHashMap`+`AtomicInteger` 规避。Go 侧 `Temporary` 的 count 必须用 `atomic`/`sync.Mutex` 保护（见 `02` 第 5 节）。这是并行正确性的关键。

### 4.5 Temporary（网关聚合暂存）

```go
// Java：counts(map<string,AtomicInteger>)、stacks(map<string,Stack>)、vars；key = graphId+"/"+key 或 "_ROOT/"+key
type Temporary struct {
    counts map[string]*atomic.Int64   // protected by mu
    stacks map[string][]any           // slice 当栈
    vars   map[string]any
    mu     sync.Mutex                 // 保护 counts/stacks 的懒创建与并行写
}
// API：Stack(g,key)[]any、Count(g,key)int64、CountSet / CountIncr(g,key,delta) / Vars() / VarAs(key)
```
> 切片当栈：`push = append`、`pop = s[len-1]; s = s[:len-1]`。inclusive 用 `[]int64`（激活分支数），loop 用 `[]Iterator`。

### 4.6 FlowInterceptor / FlowInvocation

```go
// Java：interceptFlow(包整次 eval，默认 invoke()) + onNodeStart/onNodeEnd（默认空）
type Interceptor interface {
    InterceptFlow(inv *Invocation) error      // 默认 inv.Invoke()
    OnNodeStart(ctx Context, node *Node)       // 默认空
    OnNodeEnd(ctx Context, node *Node)         // 默认空
}
// Go：用「空结构体基类」或 functional adapter 提供默认：
//   type noopInterceptor struct{}
//   func (noopInterceptor) InterceptFlow(inv *Invocation) error { return inv.Invoke() }
//   func (noopInterceptor) OnNodeStart(Context, *Node) {}
//   func (noopInterceptor) OnNodeEnd(Context, *Node) {}
// 用户嵌入 noopInterceptor 只覆盖关心的方法。

type Invocation struct {
    Exchanger *Exchanger
    Options   *Options
    StartNode *Node
    list      []rankedInterceptor   // 按 index 升序
    last      func(*Invocation, *Options) error   // = engine.evalDo
    index     int
}
func (inv *Invocation) Invoke() error {
    if inv.index < len(inv.list) {
        it := inv.list[inv.index]; inv.index++
        return it.target.InterceptFlow(inv)       // 期望它再调 inv.Invoke()
    }
    return inv.last(inv, inv.Options)
}
// 访问器：Context()/Graph()/StartNode()
```
> **区分**：interceptor 的 `OnNodeStart/End(ctx, node)` vs driver 的 `OnNodeStart/End(ex, node)`。引擎先跑**所有** interceptor（rank 序），再跑 driver。

---

## 5. 轨迹 / 选项 / 异常对照

### 5.1 FlowTrace / NodeRecord

```go
// Java FlowTrace：enabled(default true)、rootGraphId、lastRecords(每图最后节点=恢复点)
type Trace struct {
    enabled     atomic.Bool
    rootGraphID string
    lastRecords map[string]*NodeRecord   // graphId → record
    mu          sync.RWMutex
}
// 方法：Enable(bool)、Clear()、RecordNode(g,node)（rootGraphID 首次设；nil node 删条目）、
//       LastRecord(graphId)、LastNode(g)（无则 g.Start()）、LastNodeID(graphId)、IsEnd(graphId)
```
```go
// Java NodeRecord：graphId,id,title,type(枚举按名序列化),timestamp(millis)
type NodeRecord struct {
    GraphID   string
    ID        string
    Title     string
    Type      NodeType    // JSON 按名（MarshalText/UnmarshalText）
    Timestamp int64       // 构造时戳
}
func (r *NodeRecord) IsEnd() bool { return r.Type == NodeEnd }
```

### 5.2 FlowOptions

```go
// Java：仅 interceptorList（rank 排序）。无 maxSteps（步数在 Exchanger.Steps）
type Options struct {
    interceptors []rankedInterceptor   // 每次 Add 后按 rank 排序
}
func (o *Options) AddInterceptor(it Interceptor, index int) *Options
func (o *Options) AddInterceptorDefault(it Interceptor) *Options   // index 0
func (o *Options) Interceptors() []rankedInterceptor
```
> 纠偏：早期猜测的「maxSteps/write-safety 字段」**不存在**；步数预算是 `eval` 的 `steps` 参数（存于 `Exchanger.Steps`）。`Options` 纯粹是 per-eval 拦截器配置。

### 5.3 Stepper

```go
// Java：Iterator<Integer>，半开区间 [start,end) step；from("a...b") / from("a:b:step")
// Go：返回切片更简单（小集合），或 chan
func StepperFrom(s string) ([]int, error)   // "1...9"→[1..8]；"1:9:2"→[1,3,5,7]；step<=0 报错
```

### 5.4 FlowException → flow.Error

```go
// Java：RuntimeException，唯一错误类型；非 FlowException 被包装
type Error struct {
    GraphID, NodeID string   // 上下文（task/condition 失败时填）
    Op              string   // "task handle" / "condition handle"
    Err             error    // %w 包装原始
}
func (e *Error) Error() string
func (e *Error) Unwrap() error

func newTaskError(graphID, nodeID string, err error) *Error {
    if fe, ok := err.(*Error); ok { return fe }       // 已是 flow.Error 直接透传（对应 FlowException rethrow）
    return &Error{GraphID: graphID, NodeID: nodeID, Op: "task handle", Err: err}
}
```

---

## 6. 异常模型对照（汇总）

| Java | Go |
|------|----|
| `throws Throwable` | 返回 `error` |
| `throw new FlowException(msg)` | `return &flow.Error{...}` 或 `fmt.Errorf` |
| 非 FlowException → 包成 FlowException | `errors.As(&flow.Error{})` 判断；非则 `&flow.Error{Err: err}` |
| `try/finally` 还原 exchanger | `defer` |
| `CountDownLatch.await` 抛 `InterruptedException` | `ctx context.Context` + `select`（如需超时/取消） |

---

## 7. 依赖映射（Solon 生态 → aifei-go）

| # | solon-flow 依赖 | 出现位置 | aifei-go 对应 | 说明 |
|---|-----------------|----------|--------------|------|
| 1 | `org.noear.solon.core.Plugin` 生命周期 | `FlowPlugin` | **`aifei.Plugin`**（`Start()`/`Stop()`） | `flow.Plugin` 实现之，命令式装配 |
| 2 | `@Configuration`/`@Bean`/`@Condition(onMissingBean)` | `FlowConfigurate` | **无 IoC**：`Plugin.Start()` 内 `flow.NewEngine()`；用 functional option 或包级 var 守护「用户已建」 | — |
| 3 | `AppContext.cfg().getList("solon.flow")` | `FlowConfigurate` | **`config.Props`**：`props.GetStrList("flow")` / `props.Sub("flow")` | 默认发现 `flow/*.yml` → `//go:embed` 或可配目录 glob |
| 4 | `subWrapsOfType(FlowDriver/Interceptor.class,...)` | `FlowConfigurate` | 显式 API：`engine.RegisterDriver`/`AddInterceptor`；应用在 `Start()` 调用 | — |
| 5 | `ResourceUtil.scanResources`（classpath `*`） | `Engine.load`、`GraphSpec.fromUri` | `embed.FS` + `filepath.Glob` + `os.ReadFile`；无 `classpath:` 协议 | 见 `03` |
| 6 | `SolonContainer.getBean(name)` | `container` | **`MapContainer`**（`map[string]any`） | `@bean` 解析不变 |
| 7 | `NonSerializable` 标记（Context/Engine/Driver + encoder） | `FlowContext` 等 | Go marker 接口 `type nonSerializable interface{ nonSerializable() }`；或更直接：`SerVars()` 过滤 + 自定义 `MarshalJSON` | 见 `03` |
| 8 | snack4 `ONode`（含 `Write_ClassName`/`Read_AutoType`） | `toJson/fromJson`、`fromDom` | `encoding/json` + `yaml.v3`；多态类型限定为 JSON 原生 / 显式 DTO | 见 `03` |
| 9 | snakeyaml（JSON/YAML 同解析器） | `fromText` | **分库**：按扩展名/内容派发 `yaml.Unmarshal` vs `json.Unmarshal` | 见 `03` |
| 10 | **dami2 `DamiBus`** | `Context.eventBus()` | **`./dami`**（已存在 send/call/stream/lpc） | `Context` 懒初始化 `*dami.Bus` |
| 11 | **liquor `Scripts.eval` + Snel** | `LiquorEvaluation` | **`./enjoy`** 表达式引擎（条件 + 表达式/赋值任务）；纯语句脚本走 `@组件` | `EnjoyEvaluation` 默认；`Evaluation` 接口可插第三方 |
| 12 | `FlowRuntimeNativeRegistrar`（GraalVM AOT） | `aot/` | **N/A**（Go 静态编译 + `//go:embed` 即原生） | 删除 |
| 13 | slf4j | 全局 | **`./log`** | `log.Logger` 接口 |
| 14 | `redisx.RedisClient` | `RedisStateRepository` | **不迁**；aifei-go 改用 MySQL 内置仓储（`MysqlStateRepository`，见 `06`） | 见 `04` |

> **核心结论**：耦合集中在 snack4/snakeyaml/liquor/Snel/dami2/Solon-IoC 六处。其中 dami 已有 Go 端口；YAML/JSON 换标准库；**liquor/Snel 是最大缺口**，由 enjoy + 组件双轨填补；Solon-IoC 用 MapContainer + 命令式 Plugin 替代（~90 行集成层，thin）。

---

## 8. 下一站

- 核心引擎的 Go 接口签名 + 执行模型 + 并发细节 → [`02-core-design.md`](02-core-design.md)。
- 配置 schema、加载注册、快照、表达式（enjoy 差异表）、集成插件 → [`03-config-and-eval.md`](03-config-and-eval.md)。
- 工作流子系统的状态机 → [`04-workflow-design.md`](04-workflow-design.md)。
