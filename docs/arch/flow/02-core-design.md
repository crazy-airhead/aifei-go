# 核心引擎设计（flow-go）

> 本文是「Solon-Flow 迁移到 Go」系列的第三篇（核心引擎设计）。
> 前置：[`00-overview.md`](00-overview.md)、[`01-go-comparison.md`](01-go-comparison.md)。
> 本文给出 `./flow` 核心引擎的：包布局、核心类型关系、**执行模型（遍历算法逐节点类型）**、并发模型、子图与恢复机制。Go 签名仅到「契约」级别，不含完整实现。

---

## 1. 包布局与依赖方向

```
flow/                          package flow（根）
  graph.go        Graph / GraphSpec / Node / NodeSpec / Link / LinkSpec / NodeType
  engine.go       Engine 接口 + engine 实现（注册表 + eval 入口）
  exchanger.go    Exchanger（单次运行状态）
  context.go      Context 接口 + flowContext 实现（vars/with/trace/stop/eventBus/snapshot）
  driver.go       Driver 接口（条件/任务/钩子）
  descriptor.go   ConditionDesc / TaskDesc
  component.go    TaskComponent / ConditionComponent / NamedTaskComponent / GraphTaskComponent（func/interface）
  container.go    Container 接口
  trace.go        Trace / NodeRecord
  intercept.go    Interceptor 接口 + Invocation（也可放 intercept 子包）
  options.go      Options / EvalOption / EngineOption
  error.go        Error（哨兵）
  temporary.go    Temporary（counts/stacks/vars）
  load.go         FromText/FromURI/FromDom（YAML/JSON 解析）+ 图注册
  plugin.go       Plugin（aifei.Plugin 装配）
flow/util/
  stepper.go      StepperFrom
flow/container/
  map_container.go MapContainer（默认）
flow/evaluation/
  evaluation.go   Evaluation 接口
  enjoy_eval.go   EnjoyEvaluation（默认，复用 enjoy）
flow/driver/
  abstract.go     AbstractDriver（@/#/$ 派发）
  simple.go       SimpleDriver + Builder
flow/intercept/
  invocation.go   Invocation 链 + noopInterceptor
flow/workflow/                  （见 04-workflow-design.md）
```

**依赖方向（无环）**：
```
workflow ──► driver ──► evaluation ──► enjoy
   │           │          └► flow(根接口)
   │           └► container ──► flow(根接口)
   └──────────► flow(根) ──► dami, log, json
```
> `Evaluation`/`Container`/`Driver` **接口**都在根包 `flow`；具体实现（EnjoyEvaluation/MapContainer/AbstractDriver）在子包，依赖根接口。这样根包不反向依赖子包。

---

## 2. 核心类型关系图

```
            ┌─────────── Engine ───────────┐
            │  graphMap[id]*Graph           │
            │  driverMap[name]Driver        │
            │  interceptors []ranked        │
            └──┬─────────────────────────┬──┘
   Register/Load│                         │eval
               ▼                          ▼
            *Graph ──nodes──► *Node ──nextLinks──► Link ──nextNode──► *Node
              │ start        │ when:ConditionDesc  when:ConditionDesc
              │              │ task:TaskDesc
              │              ▼
              │          Driver.HandleCondition / HandleTask
              │              │
              ▼              ▼
          Exchanger ◄──── Context（vars/trace/stop/eventBus）
          (steps/stepCount/Temporary/interrupted/stopped/reverting)
              │
              ▼ RunGraph(子图) → 新 Exchanger（共享 stepCount）
```

- **Graph** 是不可变结构（构造后冻结 nodes/links/metas）。
- **Engine** 持有 Graph 注册表 + Driver 注册表 + 拦截器列表。
- **Exchanger** 是「一次 eval」的可变状态，与 Context 一一绑定（`ctx.SetExchanger`）。
- **Driver** 无状态（策略），被 Engine 按 `graph.Driver` 名解析。
- **Context** 跨多次 eval 复用（持有持久 vars/trace）；Exchanger 每次 eval 新建。

---

## 3. 执行模型（核心算法）

### 3.1 入口：`Engine.EvalOpts(graph, steps, ctx, opts)`

```
1. driver = engine.DriverOf(graph)                      // 按 graph.Driver 名，空=默认
2. ex = NewExchanger(graph, engine, driver, ctx, steps, stepCount=new(0))
3. lastNode = ctx.Trace().LastNode(graph)               // 恢复点；空 → graph.Start()
4. bak = ctx.Exchanger(); ctx.SetExchanger(ex); ctx.SetStopped(false)
5. opts = opts or new; opts.merge(engine.interceptors)  // 合并引擎级拦截器（rank 排序）
6. inv = NewInvocation(ex, opts, lastNode, engine.evalDo)
7. err = inv.Invoke()                                   // 跑拦截器链，终结到 evalDo
8. finally: ctx.SetExchanger(bak)                       // 还原（支持嵌套子图）
```

`evalDo(inv, opts)` → `nodeRun(ex, opts, graph.Start(), lastNode)`。

### 3.2 `nodeRun(ex, opts, node, startNode)` —— 引擎心脏

```
1. if node == nil or ex.IsStopped(): return
2. if ex.IsInterrupted(): ex.SetInterrupted(false); return     // 仅当前分支死
3. 恢复逻辑：
     if ex.IsReverting():
         if node.ID == startNode.ID && node.Graph.ID == startNode.Graph.ID:
             ex.SetReverting(false)                             // 抵达恢复点
     else:
         ctx.Trace().RecordNode(graph, node)                   // 记录（便于崩溃/stop 后恢复）
4. 步数预算（仅非 reverting）：if !ex.NextStep(node): ex.Stop(); return
5. switch node.Type:
       START     → startRun
       END       → endRun
       ACTIVITY  → activityRun
       INCLUSIVE → inclusiveRun   (inclusiveRunIn → taskExec → inclusiveRunOut)
       EXCLUSIVE → exclusiveRun   (taskExec → exclusiveRunOut)
       PARALLEL  → parallelRun    (parallelRunIn → taskExec → parallelRunOut)
       LOOP      → loopRun        ($for 空 → 聚合路径；否则 → 迭代路径)
```

### 3.3 各节点类型的流出规则（必须忠实）

| 类型 | task? | 流出默认值 | 流出语义 |
|------|-------|-----------|----------|
| **start** | 否 | link/n_when 默认 **true** | 跑 onNodeStart→onNodeEnd，扇出**所有** when 为真的出连接 |
| **end** | 否 | — | 跑 onNodeStart→onNodeEnd，**终止**（无流出） |
| **activity** | 是（gated by node.when，默认 true） | link/n_when 默认 **true** | 跑 task，扇出所有真出连接 |
| **exclusive**（XOR） | 是 | 出连接条件默认 **false** | task 后，**首个** when 真的出连接；无则走无条件默认连接（when 空） |
| **inclusive**（OR） | 是 | 默认 **true** | task 后激活**所有**真出连接；入度>1 时按栈计数等待所有分支到齐再合并 |
| **parallel**（AND） | 是 | — | 入度>1 时计数等待 `countIncr == prevLinks.size()`；扇出**所有**出连接（≥2 且有 Executor 时并行） |
| **loop** | 是 | 默认 true | `$for` 空 → Iterator 栈聚合；`$for` 有 → 读 `$in`（list/变量名/Stepper 串）建迭代器，每项绑入 `vars[$for]` 后跑 body |

### 3.4 `taskExec(ex, opts, node)` —— 任务执行（activity/网关共用）

```
1. if ex.IsReverting(): return true                          // 恢复期不干活
2. if !onNodeStart(ex, node): return false                   // 跑所有 interceptor.OnNodeStart → driver.OnNodeStart；任意返回 false 中止
3. if condTest(ex, node.When, def=true):                     // 节点级 when 守卫
       err := driver.HandleTask(ex, node.Task)               // 默认 → PostHandleTask（@/#/$/脚本派发）
       if err != nil: panic/return newTaskError(graph,node,err)
4. if ex.IsStopped(): return false
5. if ex.IsInterrupted(): ex.SetInterrupted(false); return false
6. return onNodeEnd(ex, node)                                // interceptor.OnNodeEnd → driver.OnNodeEnd
```

**`onNodeStart`/`onNodeEnd` 包装顺序**（关键）：
```
onNodeStart(ex, node):
    for it in opts.Interceptors(): it.OnNodeStart(ctx, node)   // rank 序
    driver.OnNodeStart(ex, node)
    return !ex.IsStopped() && !ex.IsInterrupted()
（onNodeEnd 同构）
```

**`condTest(ex, cond, def)`**：
```
if cond.IsEmpty(): return def
ok, err := driver.HandleCondition(ex, cond)
if err != nil: ...wrap "condition handle"...
return ok
```
> 注意默认值的差异：link/n_when/网关入度默认 `true`，**exclusive 出连接默认 `false`**。

### 3.5 网关合并的栈/计数（Temporary）

- **inclusive**：`inclusiveRunIn` 用 `Temporary.Stack(graph, "inclusive_run")`。
  - 入度>1 且栈非空：`startSize = peek`；`inSize = CountIncr(graph, node.ID)`；`startSize > inSize` → `return false`（等其余分支）；否则 `pop`（聚合完成）。
  - `inclusiveRunOut`：收集 `matched` 真出连接；若非空 `push(matched.size())` 后各连接触发 `nodeRun`。
- **parallel**：`parallelRunIn`：`count = CountIncr(graph, node.ID)`；`prevLinks.size() > count` → `return false`（等齐）。`parallelRunOut`：`CountSet(graph, node.ID, 0)`（复位复用），然后扇出。
- **loop**（聚合模式）：`loopRunIn`：Iterator 栈顶 `hasNext` → 等；否则 pop。
- **loop**（迭代模式 `$for`）：见上表；迭代器压栈，`while hasNext: vars[$for]=item; activityRunOut`。

> ⚠️ **并行安全**：`Temporary` 的 count/stack 在并行分支下被并发读写。Java 用 `ConcurrentHashMap`+`AtomicInteger`。**Go 侧 count 必须用 `*atomic.Int64`；stack 的 push/pop 必须在 `Temporary.mu` 下**（见 §5）。

---

## 4. 子图调用（`#graphId` 与 `RunGraph`）

```
Exchanger.RunGraph(sub):
    1. ex.PrveStep()                                 // 补偿：子图入口会重计一步
    2. engine.Eval(sub, ex.Copy(sub), nil)           // 浅拷贝：共享 steps+stepCount；新 Temporary；reverting=true
    3. if !ex.IsStopped() && !ctx.Trace().IsEnd(sub.ID):
           ex.Interrupt()                            // 子图未到 END → 当前分支中断（子图状态留在 trace 供后继）
```

- `Copy(sub)` 共享 `stepCount` 指针 → 步数预算跨子图累计。
- 子图未结束即 `Interrupt`（不是 Stop）→ 兄弟分支可继续。
- `GraphTaskComponent`（`#id` 的组件形态）与字符串 `#id` 走同一路径。

---

## 5. 并发模型

| 状态 | Java | Go | 保护 |
|------|------|----|------|
| `Context.vars` | `ConcurrentHashMap` | `map[string]any` + `sync.RWMutex` | 读多写少；`Get` 用 `RLock`，`Put`/`With` 用 `Lock` |
| `Trace.lastRecords` | `ConcurrentHashMap` | `map[string]*NodeRecord` + `sync.RWMutex` | 记录/清/读互斥 |
| `Exchanger.stepCount` | `AtomicInteger` | `*atomic.Int64` | `AddInt64`/`LoadInt64`；**子图共享指针** |
| `interrupted/stopped/reverting`、`ctx.stopped` | `volatile bool` | `atomic.Bool` | `Store`/`Load` |
| `Temporary.counts` | `ConcurrentHashMap<string,AtomicInteger>` | `map[string]*atomic.Int64` + 懒创建 `mu` | 懒创建与计数都原子；计数本身 `atomic.Int64` |
| `Temporary.stacks` | `ConcurrentHashMap<string,Stack>` | `map[string][]any` + `mu` | push/pop 在 `mu` 下（切片非并发安全） |
| `MapContainer.m` | `ConcurrentHashMap` | `map[string]any` + `sync.RWMutex` | — |
| 并行网关扇出 | `ExecutorService`+`CountDownLatch`+`AtomicReference<Throwable>` | `errgroup.Group` | 首错优先 + `Wait` |

**并行网关的正确性要点**：
1. `nodeRun` 递归在并行分支间并发 → 共享同一 `Exchanger.Temporary` 与 `Context`。
2. 汇合计数器（parallel `CountIncr`）必须原子；inclusive 的栈操作必须互斥。
3. `errgroup` 的 `Go` 提交各分支；首错被捕获，其余分支应在 `nodeRun` 入口检查「是否已有错」并短路（对照 Java 的 `AtomicReference` 检查）。
4. `errgroup` 默认无并发上限；若需限流，包一层 `semaphore`（P2，初版可不加，因分支数通常很少）。

---

## 6. 中断 / 停止 / 恢复（reverting）机制

### 6.1 三种控制信号

| 信号 | 范围 | 触发 | Java | Go |
|------|------|------|------|----|
| `stop` | 整个流（所有分支） | `ctx.Stop()` / `ex.Stop()` | 设 `stopped` + `ctx.stopped(true)` | `atomic.Bool` 双写 |
| `interrupt` | 仅当前分支 | `ex.Interrupt()` | 设 `interrupted`，下次 `nodeRun` 清除并 return | `atomic.Bool` |
| `reverting` | 单次 eval | 引擎内部 | 新 exchanger 默认 true，命中恢复点翻 false | `atomic.Bool` |

`IsStopped() = ex.stopped || ctx.IsStopped()`（跨引擎/子图传播）。

### 6.2 恢复（reverting）走查

**首次运行**（trace 空）：
```
lastNode = graph.Start()       // trace 无记录
ex.reverting = true
nodeRun(start): start.ID == lastNode.ID && 同图 → reverting=false  // 立即翻转
→ 正常执行
```

**恢复运行**（trace 有 `lastNode = N3`）：
```
lastNode = N3
ex.reverting = true
从 start 正向遍历：start→A→B→N3
  途中 reverting=true：跳过 taskExec（直接 return true）、跳过拦截/记录/步数
  抵达 N3（id==id 同图）→ reverting=false
→ 从 N3 正常恢复执行
```

> ⚠️ **caveat（必须测试固化）**：revert 途中，网关合并计数器（`parallel_run_in`/`inclusive_run_in`）**未被 revert 守卫**——它们仍会改 `Temporary`。故恢复**仅在**「start→lastNode 路径以相同次数重访合并节点」时良定义（典型为单一确定性路径）。多入度网关的恢复是已知边界，文档明示，测试覆盖「线性 + 单选网关」恢复，对「并行恢复」标注限制。

### 6.3 快照 → 恢复 的完整闭环

```
运行中 ctx.Stop()                           // 任务内按业务停止
snapshot = ctx.ToJSON()                     // 序列化 vars(剔除 NonSerializable) + stopped + trace
db.Save(instanceID, snapshot)               // 持久化（数天后）
snapshot = db.Load(instanceID)
ctx = flow.NewContextFromJSON(snapshot)     // 重建
engine.Eval(graph, ctx)                     // 从 trace.lastNode 恢复继续
```
快照 JSON 形状详见 [`03-config-and-eval.md`](03-config-and-eval.md) §3。

---

## 7. 配置加载与图注册（简述，详见 03）

```
Engine.LoadGraphsFromDir("flow/")           // glob *.yml / *.json
   → GraphFromText(bytes)                   // 按扩展名派发 yaml/json
   → GraphSpec.FromDom(map)                 // 逆序自动连边
   → spec.Create()                          // 冻结成 *Graph
   → engine.Load(*graph)                    // graphMap[id] = graph
```

---

## 8. 核心接口签名汇总（契约）

> 重复 `01` 的关键签名，便于实现时一眼对照。完整版见 `01-go-comparison.md` §2–§5。

```go
// 引擎
func NewEngine(opts ...EngineOption) *Engine
type Engine interface {
    RegisterDriver(name string, d Driver); RegisterDefaultDriver(d Driver); DriverOf(g *Graph) Driver
    AddInterceptor(it Interceptor, rank int); RemoveInterceptor(it Interceptor)
    Load(g *Graph); Unload(id string); Graphs() []*Graph; Graph(id string)(*Graph,bool); GraphOrThrow(id string)(*Graph,error)
    Eval(g *Graph, opts ...EvalOption) error
}
type EvalOption func(*evalConfig)             // WithContext / WithSteps / WithOptions

// 驱动
type Driver interface {
    Executor() Executor
    OnNodeStart(ex *Exchanger, node *Node); OnNodeEnd(ex *Exchanger, node *Node)
    HandleCondition(ex *Exchanger, c ConditionDesc) (bool, error)
    HandleTask(ex *Exchanger, t TaskDesc) error
    PostHandleTask(ex *Exchanger, t TaskDesc) error
}

// 上下文
func NewContext(instanceID ...string) Context
func NewContextFromJSON([]byte) (Context, error)
type Context interface { /* 见 01 §3.1 */ }

// 组件
type TaskComponent      func(ctx Context, node *Node) error
type ConditionComponent func(ctx Context) (bool, error)

// 容器
type Container interface{ GetComponent(name string) any }

// 表达式
type Evaluation interface {
    RunCondition(ctx Context, code string) (bool, error)
    RunTask(ctx Context, code string) error
}

// 拦截器
type Interceptor interface {
    InterceptFlow(inv *Invocation) error
    OnNodeStart(ctx Context, node *Node)
    OnNodeEnd(ctx Context, node *Node)
}
```

---

## 9. 与工作流的关系（预告）

核心引擎是「火力全开跑到底」的图遍历器。工作流子系统（[`04`](04-workflow-design.md)）通过一个**装饰 driver（`WorkflowDriver`）**接管 `HandleTask`：把引擎的 `stop`/`interrupt` 信号转译成**持久化的 WAITING 任务状态**，从而在通用引擎之上涌现出「人工任务暂停 / 认领 / 提交」语义。核心不需要为工作流做任何特化改动——这正是 driver 机制的设计意图。

---

## 10. 实现顺序建议（与 TDD 对齐，详见 05）

1. 图模型（Graph/Node/Link/Spec/NodeType）+ `FromDom` 解析（YAML/JSON）—— 可独立测试。
2. Container/Component/Evaluation（EnjoyEvaluation）—— 求值单测。
3. Driver（AbstractDriver 的 `@/#/$` 派发）—— 派发单测。
4. Exchanger + Temporary + Stepper —— 栈/计数单测。
5. Engine + `nodeRun`（start/end/activity 顺序流）—— 最小闭环。
6. 网关（exclusive → inclusive → parallel → loop）—— 逐类型。
7. 拦截器 + Options。
8. 子图 + 步数预算。
9. Trace + reverting 恢复 + 快照 toJson/fromJson。
10. Context.EventBus（dami）。
