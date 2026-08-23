# Aifei-Go Flow：轻量级流程编排引擎 + 工作流子系统

> **图驱动、可快照、可恢复**：`Graph`（不可变图）+ `Engine`（求值引擎）+ `Context`（变量域/快照）三件套跑通自动编排；`flow/workflow` 在其上叠加 **claim/submit 人工任务**语义——审批停在某个节点等人办，办完继续走。引擎零并发假设（单协程递归遍历），表达式复用 [enjoy](enjoy.md)，事件复用 [dami](dami.md)。

---

## 1. 背景与定位

`./flow` 是 [Solon-Flow](https://solon.noear.org/article/learn-solon-flow)（Java）的 Go 移植，与 `docs/arch/flow/` 七篇设计文档一一对应。它解决的问题是：**把"一串带分支/并行/循环/人工介入的步骤"从业务代码里抽出来，声明成一张图**，由引擎统一驱动、追踪、快照与恢复。

| 维度 | 说明 |
|------|------|
| 是什么 | 流程编排引擎（`flow`）+ 工作流子系统（`flow/workflow`） |
| Java 对应 | Solon-Flow 的 `FlowEngine`/`Graph`/`FlowDriver`/`FlowContext` 与 `WorkflowExecutor` 全套 |
| 依赖 | 内部 [enjoy](enjoy.md)（表达式求值）+ [dami](dami.md)（实例事件总线）+ 外部 `gopkg.in/yaml.v3`（配置解析）；**不依赖 aifei 框架**，可独立使用 |
| 生产集成 | [`plugins/flow`](flow-plugin.md) 组装引擎 + MySQL 状态仓储（另见该篇） |
| 代码量 | `flow` ~3,100 行 + `flow/workflow` ~800 行 |

与典型 BPM 引擎（Flowable/Camunda）的差别：flow 只做**编排本身**——没有内置表单、组织架构、定时器；需要人工任务时由 `workflow` 子系统提供"谁能办/停在哪/怎么办"三个扩展点，持久化由使用方通过 `StateRepository` 注入（插件内置 MySQL 实现）。

---

## 2. 核心概念与总体架构

### 2.1 两层能力：自动编排 vs 人工工作流

```
            【第一层：flow —— 自动编排（跑到完）】
  配置文本 / GraphSpec ──Create──▶ Graph（不可变：Nodes + Links + Start）
                                        │
        Engine.Load(graph)              ▼
        Engine.Eval(graph, ctx) ─▶ Exchanger（单次求值状态：步数/网关聚合/中断标志）
                                        │ 递归 nodeRun：节点任务 → 出边 when → 下一节点
                                        ▼
              Driver（默认 SimpleDriver ← AbstractDriver）
                 ├── Evaluation（默认 EnjoyEvaluation：when/task 表达式）
                 ├── Container（@组件 查找）
                 └── task 前缀分发：@组件 / #子图 / $meta / 表达式
              Context（实例变量域 + Trace + Stop/Interrupt + ToJSON 快照）

            【第二层：flow/workflow —— 人工任务（停在节点等人办）】
  Executor（claim/find/submit 门面）
     └── WorkflowDriver（包一层 Driver，把"跑到完"翻译成"停在可操作节点"）
           ├── StateController：谁可操作 / 是否自动前进
           └── StateRepository：每实例每节点状态（插件提供 MySQL 实现）
```

### 2.2 关键类型一览

| 类型 | 职责 | Java 对应 |
|------|------|-----------|
| `Engine` | 图注册表 + 求值入口 + 拦截器链 | `FlowEngine` |
| `Graph` / `GraphSpec` | 不可变图 / 可变构建器 | `Graph` / `GraphSpec` |
| `Node` / `NodeSpec` | 节点（类型 + when + task + meta + 出边） | `Node` / `NodeSpec` |
| `Link` / `LinkSpec` | 连线（when 条件 + 优先级） | `Link` / `LinkSpec` |
| `Context` | 实例变量域、trace、停止控制、快照 | `FlowContext` |
| `Exchanger` | 单次求值状态（步数预算、Temporary、标志位） | `FlowExchanger` |
| `Driver` / `AbstractDriver` / `SimpleDriver` | 任务/条件执行语义（类比 JDBC 驱动） | `FlowDriver` / `AbstractFlowDriver` / `SimpleFlowDriver` |
| `Evaluation` | when/task 脚本求值（默认 enjoy） | `LiquorEvaluation` |
| `Container` / `MapContainer` | `@name` 组件注册表 | `Container` / `MapContainer` |
| `TaskComponent` / `ConditionComponent` | Go 代码任务 / 条件 | 同名函数式接口 |
| `Interceptor` / `Invocation` | 求值拦截器 + 调用链 | `FlowInterceptor` / `FlowInvocation` |
| `Trace` / `NodeRecord` | 每图"最后执行节点"记录（恢复用） | `FlowTrace` / `NodeRecord` |
| `Temporary` | 网关 join 用的栈/计数器（不序列化） | `Temporary` |
| `workflow.Executor` | claim/find/submit 工作流门面 | `WorkflowExecutor` |
| `workflow.StateController` | 谁可操作 / 是否人工节点 | 同名接口 |
| `workflow.StateRepository` | 每实例节点状态存取 | 同名接口 |

### 2.3 节点类型（`NodeType`）

| 类型 | 码 | 语义 |
|------|-----|------|
| `START` | 1 | 唯一起点（无 START 节点时取第一个无入边的节点） |
| `END` | 2 | 终点，止步 |
| `ACTIVITY` | 11 | 普通活动节点：跑任务后沿所有 when 为真的出边流出 |
| `EXCLUSIVE` | 21 | 排他网关：按优先级取**第一条**为真分支；空 when 分支为默认 |
| `INCLUSIVE` | 31 | 包容网关：**所有**为真分支都走；汇聚点等齐 |
| `PARALLEL` | 32 | 并行网关：**全部分支**都走；计数汇聚（分支仍**顺序执行**） |
| `LOOP` | 33 | 循环网关：迭代 `$in` 集合，逐项绑定 `$for` 变量 |

`NodeType.IsGateway()`：码 > 11 即网关。`NodeTypeOf(name)` 大小写不敏感解析，空/未识别回退 `ACTIVITY`；`ITERATOR` 是 `LOOP` 的废弃别名。

---

## 3. 关键 API

### 3.1 建图：链式 `GraphSpec` → 不可变 `Graph`

```go
g, err := flow.Create("demo", func(s *flow.GraphSpec) {
    s.AddStart("s").LinkAdd("a")
    s.AddExclusive("x").
        LinkAddConfig("hi", func(l *flow.LinkSpec) { l.When("v > 10") }).
        LinkAddConfig("lo", func(l *flow.LinkSpec) { l.When("v > 0") }).
        LinkAdd("def")                      // 空 when = 默认分支
    s.AddActivity("a").Task("ran_a = 1").LinkAdd("x")
    s.AddEnd("hi")
    s.AddEnd("lo")
    s.AddEnd("def")
})
```

- 建图三入口：`flow.Create(id, fn)` / `CreateWithTitle` / `CreateWithDriver`，或 `spec := flow.NewGraphSpec(id)` 后 `spec.Create()`。
- 节点规格 `*NodeSpec` 全链式：`Title` / `Task` / `When` / `Meta` / `MetaPut` / `LinkAdd` / `LinkAddConfig(nextID, configure)` / `LinkRemove` / `LinkClear`；硬编码组件用 `TaskComp(c)` / `WhenCond(c)`。
- 连线规格 `*LinkSpec`：`Title` / `Meta` / `When` / `WhenCond` / `Priority`（出边按 priority **降序稳定排序**）。
- 组件驱动建图：`s.AddActivityNamed(c)`（`NamedTaskComponent` 以自身 `Name()` 作节点 id、`Title()` 作标题）。
- `NewGraph(spec)` 构建时急切算出反向视图（`PrevLinks`/`PrevNodes`/`NextNodes`），此后图不可变、读取无锁；找不到起点返回 `ErrNoStartNode`。

### 3.2 配置文本建图：`GraphFromText`（YAML/JSON）

```yaml
id: approval
title: 审批流
meta:
  notify: notified = true
layout:
  - id: s            # 缺 type 默认 ACTIVITY；缺 id 自动生成 n-<序号>
    type: start
    link: a          # 缺省 link 自动串到布局中的下一个节点
  - id: a
    title: 初审
    task: "@approve"                 # @ 组件
    link:                            # 数组形式 = 多条出边
      - nextId: pass
        when: agree == true
      - nextId: back
        when: agree == false
  - id: pass
    task: "$notify"                  # $ 从 graph.meta 取脚本再求值
    link: e2
  - id: back
    link: a                          # 打回重审（回到 a）
  - id: e2
    type: end
```

```go
g, err := flow.GraphFromText(text)   // JSON 亦可（YAML 子集，同一个 yaml.v3 解码）
spec, err := flow.GraphSpecFromText(text)  // 只要可变规格时
```

解析规则（`parse.go`）：`layout` **倒序遍历**实现"缺 link 自动串下一个"；`link` 支持单字符串 / 对象 `{nextId,title,meta,when}` / 数组混排；`nodes` 是 v3.1 废弃键、`condition` 是 v3.3 废弃的条件键，均仍兼容。

### 3.3 求值：Engine + Context

```go
e := flow.NewEngine()            // 默认 SimpleDriver（enjoy 表达式）
e.Load(g)

ctx := flow.NewContext("inst-1") // 可选实例 id（写入变量 instanceId）
ctx.Put("v", 20)

err := e.Eval(g, ctx)            // 或 EvalByID("demo", ctx)；EvalWithSteps(g, 100, ctx) 限步数
```

`Context` 即实例的**变量域**（表达式直接读写这些变量）：`Put`（链式，忽略 nil）/ `PutIfAbsent` / `Get` / `GetOrDefault` / `ContainsKey` / `Remove` / `With(key, value, fn)`（临时变量，结束还原）。控制类：`Stop()`（停整个流）/ `Interrupt()`（仅断当前分支）/ `Trace()` / `EnableTrace(false)` / `EventBus()`（实例级 [dami](dami.md) 总线）。快照：`ToJSON()` / `flow.ContextFromJSON(j)`。

> 实现类 `*flowContext` 另有 `SetGoContext/GoContext`（绑定 Go `context.Context`，供 db 事务/取消传播）——不在 `Context` 接口内，[flow 插件](flow-plugin.md)以类型断言使用。

---

## 4. 核心机制：Driver 与任务/条件分发

`Driver` 决定"任务怎么跑、条件怎么判"，类比 JDBC 驱动：

```go
type Driver interface {
    Executor() func(fn func())                          // 并行网关的异步提交器（nil=顺序）
    OnNodeStart(ex *Exchanger, node *Node)              // 节点进入（引擎在任务前调）
    OnNodeEnd(ex *Exchanger, node *Node)                // 节点离开（任务后调）
    HandleCondition(ex *Exchanger, cond ConditionDesc) (bool, error)
    HandleTask(ex *Exchanger, task TaskDesc) error      // 工作流语义覆写点
    PostHandleTask(ex *Exchanger, task TaskDesc) error  // 默认任务执行
}
```

`AbstractDriver`（`SimpleDriver` 内嵌它）实现描述符分发。**任务描述符四类前缀**：

| task 写法 | 解析为 | 说明 |
|-----------|--------|------|
| `@approve` | `Container` 组件 | 查 `MapContainer`，须实现 `TaskComponent` |
| `#subGraph` | 子图 | 加载引擎中 id 为 `subGraph` 的图，跑完回来；子图未到 END 则中断当前分支 |
| `$notify` | graph meta 脚本 | 按**点号路径**（如 `$a.b.c`）从 `graph.meta` 解析出字符串再当表达式跑 |
| 其他 | enjoy 表达式 | 如 `x = 1; y = x + 2`（`;` 分隔多语句）；当前节点以变量 `node` 暴露给脚本 |

**条件描述符**（when）：硬编码 `ConditionComponent` 优先；`@name` 查容器组件（须实现 `ConditionComponent`）；其余按 enjoy 表达式求值，真值规则为 **nil→false、bool→原样、其他→true**。

组件即普通 Go 代码，用适配器包函数即可：

```go
c := flow.NewMapContainer()
c.PutComponent("approve", flow.TaskFunc(func(ctx flow.Context, _ *flow.Node) error {
    ctx.Put("approved_by", ctx.Get("actor"))
    return nil
}))
c.PutComponent("isVip", flow.ConditionFunc(func(ctx flow.Context) (bool, error) {
    return ctx.Get("level") == "vip", nil
}))
e := flow.NewEngine(flow.NewSimpleDriver(flow.WithContainer(c)))
```

`WithEvaluation` 可替换表达式引擎（实现 `Evaluation` 的 `RunCondition/RunTask` 即可）；`WithExecutor` 提供异步提交器。`Engine.Register(name, driver)` 支持按图注册命名驱动，图配置里的 `driver:` 字段选择。

---

## 5. 核心机制：网关语义与 Temporary

四类网关的遍历实现都在 `engine.go`，聚合状态放 `Exchanger.Temporary()`（栈 + 计数器，键为 `graphID/key`，**不序列化**）：

| 网关 | 分出 | 汇聚 |
|------|------|------|
| `EXCLUSIVE` | 按 priority 降序逐条测 when，**首真即走**；全假走空 when 默认分支 | 无（只选一条） |
| `INCLUSIVE` | 测**所有**出边，真者全走；出栈记录分支数 | 入边 > 1 时计数等待，到齐弹出栈继续 |
| `PARALLEL` | **全部**出边都走（不测 when） | `CountIncr` 计数 == 入边数才过，过完清零 |
| `LOOP` | 出边即循环体；按 `$in` 迭代，逐项 `ctx.Put($for, item)` | 入边（循环体末端）等迭代器耗尽 |

循环网关的 `$in`（`buildIterator`）三种形态：

- 集合字面量：节点 meta 里直接写数组；或 `$in` 填变量名，从上下文取 `[]any`；
- 数字步进串 `1...5`：`[start, end)` 步长 1；
- 步长串 `1:10:2`：`start:end:step`（`Stepper`，步长必须为正）。

`$for` 是每次迭代绑定的元素变量名（meta 键 `$for`，如 `MetaPut("$for", "item")`）。

> **并行 ≠ 并发**：PARALLEL 分支默认**顺序执行**（对齐 Solon-Flow 无 executor 的默认）。fork-join 是语义上的"都走、等齐"；真并发需驱动提供 `Executor` 且 Trace 需线程安全——刻意未做。

---

## 6. 核心机制：拦截器、步数预算与子图

**拦截器**（`Interceptor`）：`InterceptFlow(inv)` 包住整次求值（须调 `inv.Invoke()` 续链），`OnNodeStart/OnNodeEnd` 为逐节点回调。引擎级 `e.AddInterceptor(ic, index...)`（index 小者先，可 `RemoveInterceptor`）；单次求值级 `Options.InterceptorAdd`。快速包装用 `flow.InterceptorFunc(fn)`。

```go
e.AddInterceptor(flow.InterceptorFunc(func(inv *flow.Invocation) error {
    log.Info("flow %s start", inv.Graph().GetID())
    defer log.Info("flow end")
    return inv.Invoke()
}), 0)
```

**步数预算**：`EvalWithSteps(g, steps, ctx)`；`steps < 0` 不限。计数器是原子共享的（跨子图累计），耗尽即 `Stop()`——防恶意/误配的死循环图。

**子图**：task 写 `#graphId` 时 `Exchanger.RunGraph` 以 `CopyFor(graph)` 换图续跑，共享步数预算（先退还一步）、共享 Temporary；子图未跑到 END 时中断父分支。`Exchanger.RunTask(node, desc)` 可脱离图遍历直接跑一个任务描述符（工作流的 `Task.Run` 用它）。

---

## 7. 核心机制：Trace 快照与断点恢复

`Trace` 为每张图记录**最后执行节点**（`NodeRecord{GraphID,ID,Title,Type,Timestamp}`），是恢复与"跑到哪了"查询的共同基础：

- `ctx.Trace().LastNode(g)` / `LastNodeID("")` / `IsEnd(graphID)`；`EnableTrace(false)` 关闭。
- `ctx.ToJSON()` 序列化 `vars（剔除不可序列化项：context 自引用、*Exchanger、*flowContext）+ stopped + trace`；`flow.ContextFromJSON(j)` 还原。

**恢复原理（reverting 重走）**：`Engine.eval` 先取 trace 的最后节点作为恢复起点；`Exchanger.reverting` 为真时引擎从 start **空跑**重走到该节点（不执行任务、不记步数），到位后清标志继续真跑。因此恢复点的任务**会再次执行**——停点组件的判断必须是数据驱动的（恢复前把闸门变量放进快照），见 `_test/flow_test/engine_test.go` 的 `TestEngine_SnapshotResume`。

```go
ctx := flow.NewContext("inst-1")
_ = e.Eval(g, ctx)                 // 跑到某节点 ctx.Stop() 停下
snap := ctx.ToJSON()               // 存库（插件里由 bpm_flow_repository.vars 承担）

ctx2, _ := flow.ContextFromJSON(snap)
ctx2.Put("ready", true)            // 外部信号就绪
_ = e.Eval(g, ctx2)                // 从停点继续，跑到 END
```

---

## 8. workflow 子系统：人工任务的 claim/submit

自动编排"跑到完"不等价于审批流：审批要**停在当前节点等人办**。`flow/workflow` 用三件套实现：

| 扩展点 | 接口 | 内置实现 |
|--------|------|----------|
| 谁可操作 / 是否人工节点 | `StateController` | `BlockStateController`（仅 ACTIVITY 人工，默认）/ `NotBlockStateController`（全可办）/ `ActorStateController(keys...)`（meta[key] == ctx[key] 才可办，默认键 `actor`） |
| 每实例节点状态存取 | `StateRepository` | `InMemoryStateRepository`（嵌套 map，键 `实例id → "graphId:nodeId"`）；MySQL 版在 [flow 插件](flow-plugin.md) |
| 工作流门面 | `Executor` | `ClaimTask` / `FindTask` / `FindNextTasks` / `SubmitTask` / `GetState` … |

任务状态与动作（码值与 Java 一致）：

- `TaskState`：`UNKNOWN 0` / `WAITING 1001`（待办）/ `COMPLETED 1002` / `TERMINATED 1003`（终止）。
- `TaskAction`：`FORWARD 1020`（办理通过→COMPLETED）/ `BACK 1010`（退回→WAITING）/ `BACK_JUMP 1011`（跳退）/ `FORWARD_JUMP 1021`（跳进）/ `TERMINATE 1030` / `RESTART 1040`（清态重来）。`TaskActionOf(code)` / `TaskStateOf(code)` 按码反解。

典型流转（对齐 `_test/flow_test/workflow_test.go`）：

```go
c := flow.NewMapContainer()
engine := flow.NewEngine(flow.NewSimpleDriver(flow.WithContainer(c)))
exec := workflow.NewExecutor(engine,
    workflow.NewBlockStateController(), workflow.NewInMemoryStateRepository())
engine.Load(g)

ctx := flow.NewContext("inst-1")

task, _ := exec.ClaimTask(g, ctx)          // 1. 认领：停在 a（WAITING），任务未执行
// task.NodeID()=="a" && task.State()==TaskStateWaiting

exec.SubmitTask(g, g.GetNode("a"), workflow.ActionForward, ctx)
                                           // 2. 提交通过：执行 a 的任务 → COMPLETED → 自动推进
                                           //    到下一个需人工的节点（或直达 END）

tasks, _ := exec.FindNextTasks(g, ctx)     // 3. 候选任务探测（不要求 actor 配置）
```

实现要点：

- `Executor` 内部以 `WorkflowDriver` 包装基础驱动，靠 `ctx.With(IntentKey, intent, fn)` 在上下文里装一个**意图**（claim/find/findNext/submit），`HandleTask` 成为状态机：自动前进节点照常跑并落状态；人工节点遇 claim 停在 WAITING、遇 submit 才放行。意图类型对使用者不可见，`IntentKey = "WorkflowIntent"`。
- `StateRepository.VarsGet` 在**每次节点进入**时合并进上下文——网关 when 条件跨请求可复算（MySQL 实现据此还原 `agree`/`formData` 等运行变量）。
- `SubmitTaskIfWaiting(task, action, ctx)`：锁内复查"WAITING 且可操作"再提交，防并发双办（返回 false 表示没办成）。
- `Task.Run(ctx)` 可在提交前对任务节点补跑驱动任务；`Task.IsEnd()` 判实例是否已到终点。

---

## 9. 配置与集成

`flow` 是库不是插件，无 `flow.*` 配置键；图即配置（YAML/JSON 文本），来源由使用方定。生产集成走 [plugins/flow](flow-plugin.md)（`WithGraphURIs`/`WithGraphDir` 文件加载、`WithGraphDB` 从 `oa_process.graph_bpmn` 表加载、`WithMySQL` 持久化、`WithRecordHistory` 任务历史）。

```go
// 纯库用法（无插件）
e := flow.NewEngine()
for _, f := range graphFiles {
    b, _ := os.ReadFile(f)
    g, err := flow.GraphFromText(string(b))
    if err != nil { panic(err) }
    e.Load(g)
}
_ = e.EvalByID("approval", flow.NewContext(idgen.Next()))
```

---

## 10. 模块结构

```
flow/
├── doc.go              # 包说明
├── graph.go            # Graph：不可变图、反向视图、起点解析
├── graph_spec.go       # GraphSpec：可变构建器（AddStart/AddExclusive/...）
├── node.go / node_spec.go      # Node / NodeSpec（meta 访问器、优先级排序）
├── link.go / link_spec.go      # Link / LinkSpec（when、priority）
├── node_type.go        # NodeType 八类型 + 解析
├── parse.go            # GraphFromText/GraphSpecFromText（yaml.v3，自动串链）
├── engine.go           # Engine：注册/求值/拦截器 + 五类节点遍历（网关/循环/迭代器）
├── exchanger.go        # Exchanger：单次求值状态、步数、子图、RunTask
├── context.go / context_impl.go  # Context 接口 + flowContext（变量域/快照/GoContext）
├── driver.go / abstract_driver.go / simple_driver.go  # Driver 接口 + 描述符分发 + 默认实现
├── evaluation.go / evaluation_enjoy.go  # Evaluation 接口 + enjoy 实现（AST 缓存）
├── component.go        # TaskComponent/ConditionComponent/NamedTaskComponent + 函数适配器
├── container.go        # Container / MapContainer
├── descriptor.go       # ConditionDesc / TaskDesc
├── interceptor.go      # Interceptor/Invocation/Options（调用链）
├── trace.go            # Trace/NodeRecord（每图最后节点，恢复基础）
├── temporary.go        # Temporary：网关 join 的栈/计数器
├── stepper.go          # Stepper：数字步进迭代器（1...5 / 1:10:2）
├── plantuml.go         # Graph.ToPlantuml() 状态图导出
├── error.go            # ErrNoStartNode/ErrNodeNotFound/ErrGraphNotFound
├── util.go             # 内部工具
└── workflow/
    ├── executor.go     # Executor：claim/find/submit + back/forward/jump 处理
    ├── driver.go       # WorkflowDriver（HandleTask 状态机）
    ├── controller.go   # StateController 三实现
    ├── repository.go   # StateRepository + InMemoryStateRepository
    ├── state.go        # TaskState/TaskAction（码值、转换）
    ├── task.go         # Task（Run/IsEnd）
    └── intent.go       # WorkflowIntent/IntentKey（内部意图）

测试：_test/flow_test（引擎/配置/求值/快照/工作流，黑盒）
```

---

## 11. 总结

1. **不可变图 + 递归遍历**：`GraphSpec`→`Graph` 一次成型，反向视图急切计算，求值期无锁读。
2. **Driver 即语义扩展点**：表达式（enjoy）、`@`组件、`#`子图、`$`meta 四类描述符一套分发；换表达式引擎/上工作流语义都只动 Driver。
3. **网关语义完整**：排他/包容/并行/循环四类，join 靠 `Temporary` 栈+计数；并行是 fork-join 语义而非并发。
4. **快照恢复 = trace + reverting 重走**：`ToJSON` 存变量与轨迹，恢复时空跑到停点续跑——停点任务幂等/数据驱动由使用方保证。
5. **工作流三件套正交**：谁办（Controller）、状态存哪（Repository）、怎么操作（Executor/Action）独立替换；审批的"停"是 Driver 层对引擎 Stop/Interrupt 的翻译，不改引擎。
6. **零框架耦合**：不依赖 aifei，表达式/事件复用内部库；生产化（MySQL 持久化、历史、图加载）全部下沉到插件层。

### 延伸阅读

- [flow 插件](flow-plugin.md) —— 引擎组装 + MySQL 状态仓储 + 任务历史（生产集成）
- [enjoy](enjoy.md) —— when/task 表达式的求值引擎
- [dami](dami.md) —— 实例事件总线
- [db](db.md) —— 插件持久化所用的 Db/Row API
- 设计文档：[../arch/flow/00-overview.md](../arch/flow/00-overview.md) ~ [06-mysql-repository.md](../arch/flow/06-mysql-repository.md)（总览、Java 对照、核心设计、配置与求值、工作流设计、TDD 路线、MySQL 仓储）
