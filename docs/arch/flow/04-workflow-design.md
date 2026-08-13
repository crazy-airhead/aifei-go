# 工作流子系统设计（flow-go/workflow）

> 本文是「Solon-Flow 迁移到 Go」系列的第五篇。
> 前置：[`00-overview.md`](00-overview.md)～[`03-config-and-eval.md`](03-config-and-eval.md)。
> 涵盖：`solon-flow-workflow` 的 Go 设计——把核心引擎的「跑到底」语义，经一个**装饰 driver** 转译为「人工任务暂停 / 认领 / 提交」的工作流语义。含 Task/状态机/Action/Intent/StateController/StateRepository 的 Go 签名与端到端走查。

---

## 0. TL;DR

- 工作流是核心引擎之上的**薄状态机层**：`WorkflowDriver` 装饰真实 driver，接管 `HandleTask`，把引擎的 `stop`/`interrupt` 信号转译成**持久化的 WAITING 任务**。
- 「实例」没有显式创建——一个 `Context`（带 `instanceID`）+ 一个 `Graph` 即是实例；首次 `ClaimTask`/`SubmitTask` 写入 WAITING/COMPLETED 记录即物化。
- 状态机：`UNKNOWN → WAITING → {COMPLETED | TERMINATED}`；`BACK`/`RESTART` 把节点重置回 `UNKNOWN`。
- 状态存于 `StateRepository`（接口）；InMemory 内置（`./flow/workflow`），**MySQL 内置于 `./plugins/flow`**（P2 最后一项任务，见 [`06`](06-mysql-repository.md)）。

---

## 1. 工作流 = Driver 特化

核心（`Engine`/`nodeRun`）是通用图遍历器：每节点调 driver 四钩子（`OnNodeStart`/`HandleCondition`/`HandleTask`/`OnNodeEnd`），默认同步跑完、从不「暂停」。

`WorkflowDriver` 是**装饰器**，包住真实 driver，**不改遍历，只改「到达任务节点时发生什么」**：

```go
type WorkflowDriver struct {
    delegate Driver              // 真实 driver（SimpleDriver 等）
    ctrl     StateController
    repo     StateRepository
}
```

**核心状态转译逻辑在 `HandleTask`**：从 context 读 `WorkflowIntent`（由 Executor 设为临时变量），按 `ctrl.IsAutoForward(...)` 分支：

- **自动前进分支**（非人工节点 / 无 actor meta）：读持久状态。
  - `UNKNOWN`/`WAITING`：跑一次任务（`delegate.PostHandleTask`），再查 `exchanger.IsStopped()||IsInterrupted()`：
    - stopped/interrupted → 记录 `Task(WAITING)`，持久化 `WAITING`（节点变成待办）
    - 否则 → 记录 `Task(COMPLETED)`，持久化 `COMPLETED`（流自动前涌）
  - `TERMINATED` → `exchanger.Stop()`（停整个流）；`COMPLETED` → no-op（已完成）。
- **受控分支**（人工任务）：状态 `UNKNOWN`/`WAITING` 时，调 `ctrl.IsOperatable(ctx, node)`：
  - 可操作（actor 匹配）→ 建 `Task(WAITING)`，持久 `WAITING`，然后 `CLAIM`/`FIND` → `exchanger.Stop()`；`FIND_NEXT_TASKS` → `exchanger.Interrupt()`（让并行分支继续探索）。
  - 不可操作 → 建 `Task(UNKNOWN)` 加入 `nextTasks`，`Interrupt()` 当前分支（等他人）或 `FIND` → `Stop()`。

`OnNodeStart` 从 repo 注入节点变量（`VarsGet` → `ctx.PutAll`）；`OnNodeEnd` 在 CLAIM 中到达 END 时清 `intent.Task`（表示「无更多任务」）。条件处理原样委托 delegate。

> **这就是工作流从「即发即忘」引擎中涌现的全部机制**：把 stop/interrupt 信号翻译成持久 WAITING。

---

## 2. WorkflowExecutor（对外门面）

```go
package workflow

func NewExecutor(engine *flow.Engine, ctrl StateController, repo StateRepository) *Executor

type Executor struct {
    engine *flow.Engine
    ctrl   StateController
    repo   StateRepository
    locker sync.Mutex          // 守护 SubmitTask（对照 Java ReentrantLock LOCKER）
}

// 核心 API（对照 Java WorkflowExecutor）
func (e *Executor) Engine() *flow.Engine
func (e *Executor) StateController() StateController
func (e *Executor) StateRepository() StateRepository

// 认领：从 START 重算，停在首个 actor 匹配的人工任务，持久 WAITING，返回之；实例已结束返回 nil
func (e *Executor) ClaimTask(g *flow.Graph, ctx flow.Context) (*Task, error)
func (e *Executor) ClaimTaskByID(graphID string, ctx flow.Context) (*Task, error)

// 逻辑探测：找当前任务（不限 actor 权限；TERMINATED/COMPLETED 也可见）；不锁
func (e *Executor) FindTask(g *flow.Graph, ctx flow.Context) (*Task, error)

// 逻辑探测：枚举所有分支的下一可能任务（用 Interrupt 不 Stop，并行分支全收集到 intent.NextTasks）
func (e *Executor) FindNextTasks(g *flow.Graph, ctx flow.Context) ([]*Task, error)

func (e *Executor) GetState(node *flow.Node, ctx flow.Context) State

// 双检提交：task nil / 非 WAITING / 不可操作 → false；获 locker 后再查 repo 状态再执行
func (e *Executor) SubmitTaskIfWaiting(t *Task, action Action, ctx flow.Context) (bool, error)

// 规范提交：获 locker，包 SUBMIT_TASK intent，调 submitTaskDo
func (e *Executor) SubmitTask(g *flow.Graph, node *flow.Node, action Action, ctx flow.Context) error
func (e *Executor) SubmitTaskByID(graphID, nodeID string, action Action, ctx flow.Context) error
func (e *Executor) SubmitTaskFor(t *Task, action Action, ctx flow.Context) error
```

### 2.1 每次 claim/find/submit 的统一配方

```
1. 建 WorkflowIntent{Type: ...}
2. ctx.With(IntentKey, intent, func() {            // 临时变量（With 方法作用域，结束还原）
3.     ex = NewExchanger(graph, engine, driver, ctx, steps=-1, stepCount=0)
4.     ex.RecordClear()                            // 清 trace → lastNode 退回 graph.Start()
5.     engine.Eval(graph, ex)                      // 整图从 START 重走
6.     // WorkflowDriver.HandleTask 读 intent，决定停哪，写回 intent.Task / intent.NextTasks
7. })
8. return intent.Task（或 intent.NextTasks）
```

### 2.2 实例的「物化」与「推进」

- **无显式 createInstance**：实例 = `flow.NewContext(instanceID)` + `Graph`。`instanceID` 是 repo 的存储 key。首次 `ClaimTask`/`SubmitTask` 写 WAITING/COMPLETED 即物化。
- **「重算整图」**：`RecordClear()` 清 trace → `lastNode` 退回 start → 引擎从 START 重走整图；**持久状态（repo）**告诉 `WorkflowDriver` 哪些节点已完成（不是 trace）。trace 仅在一次运行内支持 `Task.IsEnd()`/`LastRecord()`。
- **自动前进**（跳过无 actor 节点）：`forwardHandle`——提交节点标 COMPLETED 并跑其 task 后，遍历 `node.NextNodes()`：若是网关，`FindTask` 让引擎解分支；若 `ctrl.IsAutoForward(ctx, next)`，`RecordClear()` 从该节点重算，级联穿过所有非人工节点，直到停在下一个 WAITING 人工任务或到 END。

> **reverting 与 workflow 的关系**：workflow 每次 `RecordClear()`，故 `lastNode` 总退回 start，`reverting` 立即翻 false——不需要核心的「深恢复」。状态权威在 repo。

---

## 3. Task 模型

### 3.1 Task

```go
// Java Task：transient、不可序列化的「某节点某时刻状态快照」
type Task struct {
    ex        *flow.Exchanger
    rootGraph *flow.Graph
    Node      *flow.Node
    State     State
}
// 方法：
func (t *Task) Run(ctx flow.Context) error     // 复制 exchanger，只重跑该节点 task（提交流之外的临时执行）
func (t *Task) LastRecord() *flow.NodeRecord   // 委托 ex.Context().LastRecord()（动态，非字段）
func (t *Task) IsEnd() bool                     // LastRecord != nil && LastRecord.IsEnd()
func (t *Task) NodeID() string
```
> **Task 无自己的 id/actor/title/meta**——这些来自底层 `Node`（`node.GetMeta("actor")`、`node.Title`、`node.Metas`）。测试 `OaActionDemo.case6` 用 `current.Node.GetMeta("actor")`。

### 3.2 TaskState

```go
type State int
const (
    StateUnknown    State = 0      // 未到达 / 无记录
    StateWaiting    State = 1001   // 停靠，等待人工 actor
    StateCompleted  State = 1002   // 完成（通过）
    StateTerminated State = 1003   // 取消/杀死
)
func StateOf(code int) State                      // 反查，缺省 Unknown
```
**无显式状态迁移守卫表**——迁移在 `WorkflowDriver.HandleTask` + `submitTaskDo` 内过程式强制。有效生命周期：`UNKNOWN → WAITING → {COMPLETED | TERMINATED}`；`BACK`/`RESTART` 经 `stateRemove`/`stateClear` 重置回 `UNKNOWN`。

### 3.3 TaskAction

```go
type Action struct {
    Code        int
    TargetState State
}
var (
    ActionUnknown   = Action{0,    StateUnknown}    // 提交非法，返回 error
    ActionBack      = Action{1010, StateWaiting}    // 撤回一步
    ActionBackJump  = Action{1011, StateWaiting}    // 撤到指定目标节点
    ActionForward   = Action{1020, StateCompleted}  // 通过/前进一步
    ActionForwardJump = Action{1021, StateCompleted}// 前进到指定目标节点
    ActionTerminate = Action{1030, StateTerminated} // 终止实例
    ActionRestart   = Action{1040, StateUnknown}    // 清空状态重来
)
```
Action 是普通枚举（非策略模式）；分支在 `submitTaskDo` 内 `switch`。

### 3.4 WorkflowIntent

```go
type IntentType int
const (
    IntentUnknown IntentType = iota
    IntentClaimTask
    IntentFindTask
    IntentFindNextTasks
    IntentSubmitTask
    IntentSubmitTaskIfWaiting
)
type Intent struct {
    RootGraph  *flow.Graph
    Type       IntentType
    NextTasks  []*Task     // FindNextTasks 填（claim 时也顺带）
    Task       *Task       // claim/find/submit 的单一结果
}
```
经 `ctx.With(IntentKey, intent, fn)` 作**临时** context 变量，保证嵌套/递归调用（如 `forwardHandle` 内调 `FindTask`）不泄漏旧 intent。

---

## 4. StateController

```go
type StateController interface {
    IsOperatable(ctx flow.Context, node *flow.Node) bool               // 此 context/user 能否操作该节点
    IsAutoForward(ctx flow.Context, node *flow.Node) bool              // 流是否应自动穿过该节点
}
```
默认 `IsAutoForward`：`node.Type != NodeTypeActivity`（网关/start/end 自动前进；activity 不自动）。

### 4.1 三个实现

```go
// BlockStateController：每个 ACTIVITY 都是硬阻塞人工任务；其余自动前进
type BlockStateController struct{}
func (BlockStateController) IsOperatable(ctx, node) bool { return node.Type == NodeActivity }
// IsAutoForward 用默认（activity 不自动）

// ActorStateController：按 meta key（默认 ["actor"]）做 actor/角色匹配
type ActorStateController struct{ Keys []string }   // 默认 {"actor"}
func (a *ActorStateController) IsOperatable(ctx, node) bool {
    for _, k := range a.Keys {
        mv := node.GetMetaAsString(k)
        cv, _ := ctx.Get(k).(string)
        if mv != "" && mv == cv { return true }
    }
    return false
}
func (a *ActorStateController) IsAutoForward(ctx, node) bool {
    if node.Type == NodeEnd { return true }
    for _, k := range a.Keys { if node.HasMeta(k) { return false } }  // 有 actor key → 人工
    return true
}

// NotBlockStateController：全自动，从不阻塞
type NotBlockStateController struct{}
func (NotBlockStateController) IsOperatable(ctx, node) bool { return true }
func (NotBlockStateController) IsAutoForward(ctx, node) bool { return true }
```
> 对照 Java：`ActorStateController` 的 `IsAutoForward`——END 永远自动；否则有任意 actor key 即人工任务。`OaActionDemo` 用 `ctx.Put("actor","A")` 驱动。

---

## 5. StateRepository

```go
type StateRepository interface {
    // 可选：节点级变量注入（默认返回 nil，无注入）
    VarsGet(ctx flow.Context, node *flow.Node) map[string]any

    StateGet(ctx flow.Context, node *flow.Node) State
    StatePut(ctx flow.Context, node *flow.Node, state State)
    StateRemove(ctx flow.Context, node *flow.Node)
    StateClear(ctx flow.Context)
}
```
状态 key = `instanceID`（`ctx.InstanceID()`）× 节点（`graph.ID + ":" + node.ID`）。**无 `VarsSave`**——vars 在此版本只读（`RedisStateRepositoryEx` demo 暗示未来写钩子）。

### 5.1 InMemoryStateRepository（内置）

```go
// Java：Map<instanceID, Map<"graphId:nodeId", int code>>，全 ConcurrentHashMap
type InMemoryStateRepository struct {
    mu   sync.RWMutex
    data map[string]map[string]int   // instanceID → (graphId:nodeId → State.Code)
}
func stateKey(node *flow.Node) string { return node.Graph.ID + ":" + node.ID }
```

### 5.2 MySQL（内置，`./plugins/flow`，P2 最后一项任务）

Java `RedisStateRepository` 基于 `redisx`，**Go 不迁 Redis 实现，改以内置 MySQL 仓储**（详见 [`06-mysql-repository.md`](06-mysql-repository.md)）：
- `MysqlStateRepository` 实现 `StateRepository`，落表 `bpm_flow_repository`（`states` JSON = `graphId:nodeId → State.Code()`；同表 `vars`/`graph` 列托管快照）；`TaskHistoryRecorder` 落表 `bpm_flow_task`（任务流转审计）。
- 关键 API：`db.InsertOrUpdate`（MySQL `ON DUPLICATE KEY UPDATE`，命中 `uniq_instant_id`）做实例 upsert；`db.FindFirstBy(instant_id)` 加载；JSON 列写入 `json.Marshal→[]byte`、读取 `GetBytes`+`Unmarshal`；跨表原子性 `db.TransactionCtx` + `db.WithCtx` 透传。
- 按 instanceID 缓存 `states` map（懒加载、写透），避免引擎重算整图时的 N 次 SELECT。

---

## 6. 端到端走查（BlockStateController + InMemory，图 start→A→B→C→end）

**Setup**：`engine := flow.NewEngine()`；`wf := workflow.NewExecutor(engine, BlockStateController{}, &InMemoryStateRepository{})`；`engine.Load(graph)`；`ctx := flow.NewContext("fwd-test")`。repo 无记录。

**① Claim A**：`wf.ClaimTask("action-test", ctx)`
- 建 `Intent{ClaimTask}`，`ctx.With`，`ex.RecordClear()`，`engine.Eval`。
- trace 清 → lastNode=start → 从 START 走 → A。
- `HandleTask(A)`：intent=CLAIM，`IsAutoForward(A)`=false（activity）。受控分支。
  - `StateGet(A)`=Unknown。`IsOperatable(A)`=true（activity）。
  - 建 `Task(Waiting)`，`intent.Task=task`，`intent.NextTasks=[task]`。`StatePut(A, Waiting)` → repo `{A→1001}`。
  - 非 FIND_NEXT → `ex.Stop()`。
- 返回 `Task{A, Waiting}`。

**② Submit Forward on A**：`wf.SubmitTaskByID("action-test","A",ActionForward,ctx)`
- 获 locker。包 `Intent{SubmitTask}`。`submitTaskDo`。
- action=Forward → `forwardHandle(graph, A, Completed, ex)`：
  - `ex.SetReverting(false)`；`delegate.PostHandleTask(A.task)`（A 无 task → 过滤空，no-op）。
  - `StatePut(A, Completed)` → `{A→1002}`。
  - A 的下一节点 B：非网关。`IsAutoForward(B)`=false（activity）→ 不自动前涌。结束。
- repo `{A→1002}`。

**③ Claim B**：同 ①，但 `StateGet(A)`=Completed → handleTask(A) 受控分支、状态 Completed、CLAIM → no task、不停 → 流过 A。到 B：Unknown、可操作 → 停 Waiting，`{B→1001}`。返回 `Task{B,Waiting}`。

**④ Complete B→C→end**：Forward B（`{B→1002}`）→ Claim C → Forward C（`{C→1002}`）。再 Claim：引擎走 START→A(完)→B(完)→C(完)→END。`OnNodeEnd(END)`（CLAIM + END）置 `intent.Task=nil`。`ClaimTask` 返回 nil——实例完结。`Task.IsEnd()`=true。

**持久化时机**：每次 `StatePut`/`StateRemove`/`StateClear` 即写 repo，**无单独「存实例」调用**。`instanceID` 是内存 Context 与持久状态的唯一连接键。

**Actor 变体**（`OaActionDemo`）：流程同上，但 claim 前 `ctx.Put("actor","A")`，`ActorStateController.IsOperatable` 查 `node.meta["actor"]==ctx["actor"]`；无 actor key 的节点自动前涌，只有标 actor 的 activity 停靠。

---

## 7. Java-ism 对照（工作流专属）

| Java | Go |
|------|----|
| 枚举 int code + `codeOf`（TaskState/Action/IntentType/NodeType） | typed `int` const + `StateOf/ActionOf` 反查 map |
| 接口 + 装饰器（WorkflowDriver 包 driver） | struct 持 inner `Driver`，转发条件/PostHandleTask；Go 原生支持 |
| `ReentrantLock LOCKER` | `sync.Mutex`（**非重入**——`submitTask`→`submitTaskDo` 不重入；`SubmitTaskIfWaiting` 锁后直调 `submitTaskDo`，避开重入） |
| `ConcurrentHashMap`（InMemoryRepo、vars） | `map` + `sync.RWMutex` |
| `transient final` 字段（Task/Exchanger/Node） | Go 无默认序列化，未导出字段 + getter |
| `context.with(key,val,runnable)` 临时作用域 | `Context.With(key,val,fn)`（见 `01` §3.1） |
| 受检异常 `throws Throwable` | `(T, error)`；`flow.Error` 包装 |
| `redisx.RedisClient` | **不迁**（改用 MySQL 内置仓储，见 `06`） |
| 反射/注解 | 工作流模块本身**无**反射；TaskComponent/ConditionComponent 是 func 类型 |

---

## 8. 外部依赖（工作流模块）

| 文件 | 非 JDK 依赖 | Go 处理 |
|------|-------------|---------|
| WorkflowExecutor / Default | solon-flow core + lang 标记 | `flow` 根包接口；标记删除 |
| WorkflowDriver | solon-flow core + `Assert` | `flow` 根；Assert 内联 |
| Task | solon-flow core + 标记 | `flow` 根 |
| TaskState / TaskAction | 纯 JDK | typed int |
| WorkflowIntent | `flow.Graph` + 标记 | `flow` 根 |
| StateController | `flow.{Context,Node,NodeType}` | `flow` 根 |
| Actor/Block/NotBlock | `flow.{Context,Node,NodeType}` + StateController | 同 |
| InMemoryStateRepository | `flow.{Context,Node}` + workflow.{StateRepository,TaskState} | 内置 |
| RedisStateRepository | **`redisx.RedisClient`**（唯一非 solon、非 JDK） | **不迁实现**；aifei-go 改用 MySQL 内置仓储（见 `06`） |

> **结论**：工作流模块依赖 (a) flow 核心接口、(b) 可删标记、(c) `Assert`（内联）、(d) Redis（仅 RedisRepo，**不迁**，改用 MySQL 内置仓储）。无 Spring、无 Jedis、无 JSON 库（快照是核心 Context 的职责）。

---

## 9. 实现顺序（与 TDD 对齐）

1. Task/State/Action/Intent 模型（纯数据，单测构造与编码）。
2. StateController 三实现（IsOperatable/IsAutoForward 单测）。
3. InMemoryStateRepository（put/get/remove/clear 单测）。
4. WorkflowDriver（HandleTask 的 auto-forward vs 受控分支，对照 Java 引擎重算语义）。
5. Executor.ClaimTask（BlockStateController 线性图，对照走查 §6 ①③）。
6. Executor.SubmitTask（Forward/Back/Terminate/Restart/Jump，对照 `TaskActionTests`）。
7. FindTask / FindNextTasks（并行分支收集，对照 `WorkflowMultiGraphTest`）。
8. ActorStateController（actor 匹配 + 自动前涌，对照 `OaActionDemo`）。
9. 自动前涌级联（`forwardHandle`，对照 `AdvancedScenarioTests`）。

完整测试用例与分期见 [`05-tdd-plan.md`](05-tdd-plan.md)。
