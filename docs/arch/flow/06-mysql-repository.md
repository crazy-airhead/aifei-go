# MySQL 状态仓储与任务历史（flow 插件内置）

> 本文是「Solon-Flow 迁移到 Go」系列的第七篇（持久化落地）。
> 前置：[`00-overview.md`](00-overview.md)～[`05-tdd-plan.md`](05-tdd-plan.md)。
> 本文给出 flow 插件**内置**的 `MysqlStateRepository` 设计——把工作流的状态/变量持久化到 MySQL，并把任务流转记入历史表。基于两张参考表 `bpm_flow_repository`（实例快照）与 `bpm_flow_task`（任务历史）。这是整个迁移的**最后一项任务（P2）**。

---

## 0. TL;DR

- **两张表 = 两个职责**：
  - `bpm_flow_repository`：**实例快照存储**（`instant_id` 唯一；`graph`/`states`/`vars` 三个 JSON）。它同时承载 `StateRepository`（`states`）与中断/恢复快照（`vars`/`graph`）。
  - `bpm_flow_task`：**任务历史/审计**（每次任务流转一行：源节点→目标节点、assignee、status、message、variables）。
- **模块归属**：新建 **`./plugins/flow`** 模块（`aifei.Plugin` 装配 + `MysqlStateRepository` + `TaskHistoryRecorder` + DDL），依赖 `./flow` + `./db` + `./config` + `./aifei` + `./log`。核心 `./flow` 保持零外部依赖、不耦合 db（InMemory 即可跑）。
- **关键映射**：`db.InsertOrUpdate`（MySQL `ON DUPLICATE KEY UPDATE`，命中 `uniq_instantId`）做实例 upsert；`db.FindFirstBy(instant_id)` 加载；JSON 列**写入显式 `json.Marshal`→`[]byte`，读取 `GetBytes`+`json.Unmarshal`**；跨表原子性靠 `db.TransactionCtx(ctx, fn)` + `db.WithCtx(ctx)` 透传事务（aifei-go 既有模式）。
- **缓存**：引擎每次 claim/submit 重算整图，`StateGet` 被调 N 次、`StatePut` 仅在实际状态变更时。故**按 instanceID 缓存 `states` map（懒加载、写透）**，读全走缓存，写为单行 UPDATE。

---

## 1. 两张表与 flow 概念的映射

### 1.1 `bpm_flow_repository`（实例快照）

| 列 | flow 概念 | 说明 |
|----|-----------|------|
| `id` (PK, auto) | — | 自增，不显式设 |
| `instant_id` (unique) | `ctx.InstanceID()` | 实例唯一键；`ON DUPLICATE KEY UPDATE` 命中 `uniq_instantId` |
| `graph` (JSON) | `*Graph` 序列化 或 graph id | 实例所用的图定义（持久化便于跨重启恢复；可存 `Graph.ToJSON()` 或仅存 id 由引擎重载） |
| `states` (JSON) | `StateRepository` 的状态 map | `{"graphId:nodeId": <State.Code>, ...}` |
| `vars` (JSON) | `Context.Vars()` 快照 | 运行变量（剔除 NonSerializable） |
| `creator`/`create_time`/`updater`/`update_time` | 审计 | 首次 insert 填 creator/create_time；每次 update 填 updater/update_time |

> 这张表**同时满足** `StateRepository`（states）与 `03` §3 的快照持久化（vars + graph）。即：原本「用户自己 `db.Save(instanceID, ctx.ToJSON())`」的断点恢复，现在由本表托管。

### 1.2 `bpm_flow_task`（任务历史）

| 列 | flow 概念 | 说明 |
|----|-----------|------|
| `id` (PK, auto) | — | 追加写，每次流转一行 |
| `flow_ins_id` | `ctx.InstanceID()` | 实例 id |
| `proc_def_id` | graph id | 流程定义 id（`node.Graph.ID`） |
| `task_id` | 节点 id（或节点 id+序号） | 任务标识 |
| `source_node_code` | 当前节点 id | `node.ID` |
| `source_node_name` | 当前节点 title | `node.Title` |
| `source_node_type` | 当前节点类型名 | `node.Type.String()` |
| `target_node_code/name/type` | 下一节点 | submit 后的下一任务节点（forward/back/jump 的目标） |
| `assignee` | actor | `node.GetMetaAsString("actor")` 或 `ctx.Get("actor")` |
| `status` | `TaskState.Code()` | 任务状态（int） |
| `form_id` | node meta | `node.GetMetaAsNumber("formId")`（可选） |
| `variables` (JSON) | `Context.Vars()` 快照 | 本次流转时的变量 |
| `message` | 审批意见 | submit 时由调用方传入（Action 附加） |
| 审计列 | — | creator/create_time/updater/update_time |

> 这是 solon-flow **没有**的新能力（solon 的 `Task` 是 transient）。它把每次人工任务流转落库，供审批轨迹/查询/回溯。

---

## 2. 模块归属：`./plugins/flow`

```
plugins/flow/                      package flow（插件）
  plugin.go          aifei.Plugin：读 flow.* 配置 → 建引擎 → 装配 driver/repo/recorder → 加载图
  config.go          插件配置（driver/repo/tables/图资源）
  use.go             包级默认（flow.Use / DefaultEngine / DefaultExecutor）
  mysql_state.go     MysqlStateRepository（StateRepository 实现 + 快照 Save/Load）
  mysql_task.go      TaskHistoryRecorder（bpm_flow_task 写入；Interceptor 或 Executor 钩子）
  schema.go          表名/列名可配（默认 bpm_flow_repository / bpm_flow_task）
  ddl.go             //go:embed 建表 SQL（可选，供初始化）
```

**go.mod**（对照 `plugins/dataisolate`）：
```
module github.com/crazy-airhead/aifei-go/plugins/flow
go 1.26
require (
    github.com/crazy-airhead/aifei-go/aifei v0.0.x
    github.com/crazy-airhead/aifei-go/config v0.0.x
    github.com/crazy-airhead/aifei-go/db v0.0.x
    github.com/crazy-airhead/aifei-go/flow v0.0.x
    github.com/crazy-airhead/aifei-go/log v0.0.x
)
replace ( ... → ../../各模块 )
```
> MySQL 驱动由应用在 `db.Init("mysql", dsn)` 时提供（与 dataisolate 一致：插件不绑死驱动）。

**核心 `./flow` 不依赖 db**：`InMemoryStateRepository` 在 `./flow/workflow` 内置，零外部依赖路径完整可用。MySQL 是可选的「上生产」持久化。

---

## 3. MysqlStateRepository 设计

### 3.1 实现 `workflow.StateRepository`

```go
package flow

type MysqlStateRepository struct {
    schema RepoSchema          // 表名/列名（默认见 §6）
    cache  sync.Map            // instanceID → *stateCache（懒加载、写透）
    log    log.Logger
}

// stateCache：单实例的内存状态
type stateCache struct {
    mu     sync.Mutex
    states map[string]int      // "graphId:nodeId" → State.Code()
    loaded bool
}

func NewMysqlStateRepository(opts ...RepoOption) *MysqlStateRepository

// ---- StateRepository 接口 ----
func (r *MysqlStateRepository) StateGet(ctx flow.Context, node *flow.Node) workflow.State {
    c := r.cacheOf(ctx.InstanceID())
    c.mu.Lock(); defer c.mu.Unlock()
    r.ensureLoaded(ctx, c)                          // 首次懒加载（一次 SELECT）
    return workflow.StateOf(c.states[stateKey(node)])
}

func (r *MysqlStateRepository) StatePut(ctx flow.Context, node *flow.Node, state workflow.State) {
    c := r.cacheOf(ctx.InstanceID())
    c.mu.Lock(); defer c.mu.Unlock()
    r.ensureLoaded(ctx, c)
    c.states[stateKey(node)] = int(state.Code())
    r.flush(ctx, c)                                 // 写透：单行 UPDATE states + 审计
}

func (r *MysqlStateRepository) StateRemove(ctx flow.Context, node *flow.Node) {
    // 删 key + flush
}
func (r *MysqlStateRepository) StateClear(ctx flow.Context) {
    // 清空 map + flush（states='{}'）
}
func (r *MysqlStateRepository) VarsGet(ctx flow.Context, node *flow.Node) map[string]any {
    // 可选：从本行 vars 列注入；默认返回 nil（与 solon 默认一致）
}
```

`stateKey(node) = node.Graph.ID + ":" + node.ID`（对照 Java `InMemoryStateRepository`）。

### 3.2 加载与写透（db API）

```go
// 加载：一次 SELECT（FindFirstBy instant_id）
func (r *MysqlStateRepository) ensureLoaded(ctx flow.Context, c *stateCache) {
    if c.loaded { return }
    row, err := db.WithCtx(goCtx(ctx)).FindFirstBy(r.schema.RepoTable, "instant_id", ctx.InstanceID())
    if err == nil && row != nil {
        _ = json.Unmarshal(row.GetBytes("states"), &c.states)  // JSON 列：GetBytes + Unmarshal
    }
    if c.states == nil { c.states = map[string]int{} }
    c.loaded = true
}

// 写透：InsertOrUpdate（ON DUPLICATE KEY UPDATE 命中 uniq_instantId）
func (r *MysqlStateRepository) flush(ctx flow.Context, c *stateCache) error {
    statesJSON, _ := json.Marshal(c.states)                    // JSON 列：Marshal → []byte
    row := db.NewRow(r.schema.RepoTable).
        Set("instant_id", ctx.InstanceID()).
        Set("states", statesJSON).
        Set("updater", auditor(ctx)).
        Set("update_time", now())
    if !c.created {                                             // 首次补 create 审计 + graph/vars
        row.Set("creator", auditor(ctx)).Set("create_time", now())
        if g := graphOf(ctx); g != "" { row.Set("graph", g) }
        if v := varsJSON(ctx); v != nil { row.Set("vars", v) }
        c.created = true
    }
    _, err := db.WithCtx(goCtx(ctx)).InsertOrUpdate(row)       // upsert by uniq_instantId
    return err
}
```

**要点**：
- `db.InsertOrUpdate` → MySQL `INSERT ... ON DUPLICATE KEY UPDATE`（`dialect.go`），命中 `uniq_instantId`（**不是** PK `id`）。无需先查存在性。
- **JSON 列写入**：`json.Marshal(map) → []byte` 后 `Set`（驱动把 `[]byte` 作为 JSON 列值；勿直接塞 `map`）。**读取**：`row.GetBytes("states")` + `json.Unmarshal`（db 读取侧虽有 `DecodeJSONFields` 自动解码，但依赖表映射注册；显式 `GetBytes`+`Unmarshal` 最稳、不依赖映射配置）。
- **Go context 透传**：`db.WithCtx(goCtx(ctx))` —— 若调用方在 `db.TransactionCtx(goCtx, fn)` 内，则自动复用同一事务（`states` 更新与 `bpm_flow_task` 写入可跨表原子）。

### 3.3 快照 Save/Load（实例级，中断/恢复）

`bpm_flow_repository` 的 `graph`/`vars` 列让它天然是快照存储。扩展方法（非 `StateRepository` 接口，插件层提供）：

```go
// 保存完整实例快照（graph + states + vars）
func (r *MysqlStateRepository) SaveSnapshot(ctx flow.Context, graphJSON, varsJSON []byte) error

// 加载快照，重建 Context（用于数天后的恢复）
func (r *MysqlStateRepository) LoadSnapshot(instanceID string) (graphJSON, varsJSON []byte, states map[string]int, err error)
```

恢复闭环（对照 `03` §3.5）：
```go
graphJSON, varsJSON, states, _ := repo.LoadSnapshot(instanceID)
ctx, _ := flow.NewContextFromJSON(buildSnapshotJSON(varsJSON, states))  // 重建 Context
graph, _ := flow.GraphFromText(graphJSON)                              // 或 engine.Graph(graphID)
engine.Eval(graph, ctx)                                                // 从 trace.lastNode 恢复
```

### 3.4 缓存与并发

- **缓存粒度**：`sync.Map[instanceID → *stateCache]`。`WorkflowExecutor.LOCKER`（`sync.Mutex`）已串行化同一实例的 submit，故同实例缓存无竞争。
- **读全走缓存**：`StateGet` 仅首次触发一次 `FindFirstBy`；之后纯内存。
- **写透**：每次 `StatePut`/`StateRemove`/`StateClear` = 一次单行 UPDATE（O(1)，命中唯一索引）。
- **跨进程/分布式**（已知限制）：多个进程操作同一 `instanceID` 时，内存缓存各自独立 → last-write-wins。严格一致需 `SELECT ... FOR UPDATE` 或分布式锁，列为未来增强（P2 基础版假设单实例处理或可接受最终一致）。
- **缓存失效**：`StateClear` 后保留空 map（不删缓存条目）；实例真正结束可由应用调 `Evict(instanceID)` 释放。

---

## 4. TaskHistoryRecorder 设计（`bpm_flow_task`）

### 4.1 触发点

任务历史在**每次 submit**（claim 之后的提交：forward/back/jump/terminate/restart）时记录一行。最干净的接线是让 `WorkflowExecutor` 在 `submitTaskDo` 成功后回调 `Recorder`；或实现为 flow `Interceptor`（观测节点流转）。

**推荐**：`flow.Interceptor`（`OnNodeEnd`），在人工任务节点结束时记录——解耦于 Executor 内部，且复用核心拦截器机制：

```go
type TaskHistoryRecorder struct {
    schema RepoSchema
    log    log.Logger
}

func (h *TaskHistoryRecorder) InterceptFlow(inv *flow.Invocation) error { return inv.Invoke() } // 透传
func (h *TaskHistoryRecorder) OnNodeStart(flow.Context, *flow.Node) {}                          // 不用
func (h *TaskHistoryRecorder) OnNodeEnd(ctx flow.Context, node *flow.Node) {
    // 仅记录人工任务节点（有 actor / activity 且由 workflow 驱动）
    if !h.isHumanTask(ctx, node) { return }
    h.record(ctx, node)
}
```

> 也可在 `WorkflowExecutor` 暴露 `OnTaskSubmitted func(...)` 钩子（更显式、能拿到 Action/message）。两种方式二选一，**Executor 钩子**能拿到 `Action`（forward/back）与 `message`（审批意见），更贴合 `bpm_flow_task` 的语义；**Interceptor** 拿不到 Action/message。故推荐 **Executor 钩子**为主，Interceptor 为辅。

### 4.2 写入（Executor 钩子）

```go
// Executor 配置一个 recorder；submitTaskDo 成功后调用
func (h *TaskHistoryRecorder) Record(ctx flow.Context, rec TaskRecord) error {
    varsJSON, _ := json.Marshal(filterVars(ctx.Vars()))            // JSON 列
    row := db.NewRow(h.schema.TaskTable).
        Set("flow_ins_id", ctx.InstanceID()).
        Set("proc_def_id", rec.GraphID).
        Set("task_id", rec.TaskID).
        Set("source_node_code", rec.Source.ID).
        Set("source_node_name", rec.Source.Title).
        Set("source_node_type", rec.Source.Type.String()).
        Set("target_node_code", rec.Target.ID).
        Set("target_node_name", rec.Target.Title).
        Set("target_node_type", rec.Target.Type.String()).
        Set("assignee", rec.Assignee).
        Set("status", int(rec.State.Code())).
        SetIfNotNull("form_id", rec.FormID).
        Set("variables", varsJSON).
        SetIfNotBlank("message", rec.Message).
        Set("creator", auditor(ctx)).
        Set("create_time", now())
    _, err := db.WithCtx(goCtx(ctx)).Insert(row)                  // 追加写
    return err
}

type TaskRecord struct {
    GraphID  string
    TaskID   string
    Source   *flow.Node
    Target   *flow.Node
    Assignee string
    State    workflow.State
    FormID   any
    Message  string
}
```

### 4.3 跨表原子性

submit 要做：①`StatePut(当前节点, Completed)` ②`Record(task history)` ③推进。三者应原子。**aifei-go 模式**：调用方包 `db.TransactionCtx`，repo/recorder 用 `db.WithCtx` 复用事务。

```go
// 应用层（或 flow.Plugin 封装的 Executor）
err := db.TransactionCtx(goCtx, func(tctx context.Context) error {
    enrichedCtx := withGoCtx(ctx, tctx)            // 把 tctx 注入 flow Context
    return executor.SubmitTaskFor(task, action, enrichedCtx)
})
```
> 要求 flow `Context` 能携带 Go `context.Context`（见 §5）。若不携带，则 repo/recorder 用 `db.Use()` 各自提交——`states` 单行 UPDATE 本身原子，但与 task history 不在同一事务（可接受降级，文档明示）。

---

## 5. 核心 `./flow` 的最小扩展：携带 Go context

为支持事务透传与超时/取消，flow `Context` 增加 Go context 携带（小改动，对核心无侵入）：

```go
// flow/context.go
type Context interface {
    // ... 既有方法
    GoContext() context.Context      // 默认 context.Background()；server 集成时注入 r.Context()
}

// flowContext 实现持有 goCtx（构造或 WithGoContext 注入）
// server 集成：ctx = flow.NewContext(instanceID, flow.WithGoContext(in.Context()))
```
> `MysqlStateRepository`/`TaskHistoryRecorder` 统一 `db.WithCtx(ctx.GoContext())`。无 db 时（InMemory 路径）该字段闲置，零开销。

**替代方案**（若不想改核心）：把 Go ctx 存入 `vars["__go_ctx__"]`（`NonSerializable`，序列化剔除）。但显式方法更清晰，推荐。

---

## 6. 可配 schema（表名/列名）

```go
type RepoSchema struct {
    RepoTable string // "bpm_flow_repository"
    TaskTable string // "bpm_flow_task"
    // 列名默认与参考表一致；若用户表名/列名不同，可覆写
}
```
通过 `config.Props` 的 `flow.repo.*` 覆写：
```yaml
flow:
  repo:
    enabled: true
    driver: mysql           # 仅文档提示；实际驱动由 db.Init 决定
    repo_table: bpm_flow_repository
    task_table: bpm_flow_task
    record_history: true    # 是否写 bpm_flow_task
```

---

## 7. DDL（与参考表一致，embed 提供建表 SQL）

`plugins/flow/ddl.sql`（`//go:embed`）= 用户给的两张表原样。可选地由 `Plugin.Start()` 在 `flow.repo.auto_create: true` 时执行（生产建议外部迁移工具管理 schema，插件默认不自动建表）。

---

## 8. 插件装配（`plugins/flow` 的 `aifei.Plugin`）

```go
func NewPlugin(opts ...PluginOption) *Plugin
func (p *Plugin) Start() error {
    // 1. 读 config.Props["flow"]：dir/uris/repo/record_history
    // 2. engine := flow.NewEngine()；装配 SimpleDriver（EnjoyEvaluation + MapContainer）
    // 3. 仓储：
    //      if repo.enabled:
    //          repo := NewMysqlStateRepository(...)
    //      else:
    //          repo := workflow.NewInMemoryStateRepository()
    // 4. executor := workflow.NewExecutor(engine, stateController, repo)
    // 5. if record_history: executor.SetTaskRecorder(&TaskHistoryRecorder{...})
    // 6. 加载图资源（embed/glob）→ engine.Load
    // 7. SetDefault(engine/executor) 供业务取用
}
```

业务侧：
```go
db.Init("mysql", dsn)                      // 应用提供驱动
app.Use(aifei.WithPlugin(flowplugin.NewPlugin()))
// 业务：executor := flowplugin.DefaultExecutor(); executor.ClaimTask(graph, ctx)
```

---

## 9. TDD 测试用例（P2，最后一项任务）

测试模块 `_test/flow_test`（或新增 `_test/flow_mysql_test`）。MySQL 集成测试用 `modernc.org/sqlite` **模拟**？——不行，JSON 列与 `ON DUPLICATE KEY UPDATE` 是 MySQL 方言。**对策**：用 `github.com/docker/docker` 起真实 MySQL 过重；采用 **`db.Init("mysql", dsn)` 连测试库**（CI 提供）+ `t.Skip` 当无 DB。或用 `go-sql-driver/mysql` + 测试容器（P2 可选）。

> 与 aifei-go 既有约定（sqlite/miniredis/kfake 内嵌）不同，MySQL 仓储**无法纯内嵌**。建议：单元逻辑（缓存/JSON 序列化/stateKey）用 sqlite/纯内存测；**MySQL 方言部分**（`ON DUPLICATE KEY UPDATE`、JSON 列）用真实 MySQL（CI 环境变量 `FLOW_MYSQL_DSN`，缺则 `t.Skip`）。

| # | 测试 | 断言要点 | 层 |
|---|------|----------|----|
| 1 | `TestStateKey` | `graphId:nodeId` 拼接 | 纯单元 |
| 2 | `TestStatesJSON_Codec` | `map→json.Marshal→GetBytes→Unmarshal` 往返 | 纯单元 |
| 3 | `TestCache_LazyLoad_WriteThrough` | 首次 Get 触发加载；Put 改缓存+标记脏 | 纯单元（伪 db） |
| 4 | `TestRepo_Upsert` | `InsertOrUpdate` 命中 `uniq_instantId`：新实例 insert、同 instant_id update | MySQL（DSN） |
| 5 | `TestRepo_StateGetPutRemoveClear` | 四操作往返；`stateKey` 隔离不同节点 | MySQL |
| 6 | `TestRepo_SnapshotSaveLoad` | graph+vars+states 往返；vars 剔除 NonSerializable | MySQL |
| 7 | `TestRepo_VarsGet` | 可选注入 | MySQL |
| 8 | `TestTaskHistory_Record` | submit 后 `bpm_flow_task` 一行；source/target/assignee/status/message 正确 | MySQL |
| 9 | `TestTx_Atomicity` | `TransactionCtx` 内 repo+recorder 同事务；中途 error 全回滚 | MySQL |
| 10 | `TestEndToEnd_OA_WithMySQL` | `OaActionDemo` 端到端接 MySQL：claim→submit→历史落库→重启后 LoadSnapshot 恢复 | MySQL |
| 11 | `TestConcurrency_SingleExecutor` | 同实例并发 submit 由 LOCKER 串行；缓存一致 | MySQL（-race） |
| 12 | `TestDistributed_Limitation` | 标注跨进程 last-write-wins 限制（文档化，断言单进程正确） | 文档/单元 |

**验收**：1–3 纯单元必绿（无外部依赖）；4–11 在 `FLOW_MYSQL_DSN` 存在时绿、缺失则 `t.Skip`；12 文档化。

---

## 10. 风险与对策

| 风险 | 对策 |
|------|------|
| JSON 列写入丢类型（直接塞 map） | 强制 `json.Marshal→[]byte`；测试 2 固化往返 |
| `ON DUPLICATE KEY UPDATE` 误命中 PK 而非 instant_id | 表有 `uniq_instantId` 唯一键；不显式设 `id`；测试 4 验证 upsert 命中 instant_id |
| 引擎重算整图 → N 次 StateGet | 按 instanceID 缓存懒加载；读全走缓存 |
| 跨表（states+task）非原子 | `db.TransactionCtx` + `db.WithCtx` 透传；需 `Context.GoContext()` |
| 跨进程同实例缓存不一致 | 标注限制；未来 `SELECT FOR UPDATE`/分布式锁 |
| MySQL 方言无法内嵌测试 | 纯逻辑用单元+伪 db；方言用 CI 真实 MySQL（`t.Skip` 降级） |
| 审计列（creator/update_time）来源 | 由 `Context`/调用方注入（`auditor(ctx)` 从 vars 或 server principal 取） |

---

## 11. 实现顺序（最后一项任务内部）

1. `RepoSchema` + `stateKey` + JSON codec（单元）。
2. `MysqlStateRepository`：ensureLoaded/flush/StateGet/Put/Remove/Clear（伪 db 单元 → MySQL 集成）。
3. `SaveSnapshot`/`LoadSnapshot`（MySQL）。
4. `Context.GoContext()` 核心小扩展 + `db.WithCtx` 透传。
5. `TaskHistoryRecorder` + Executor 钩子（MySQL）。
6. `db.TransactionCtx` 跨表原子性。
7. `aifei.Plugin` 装配 + 配置 + DDL embed。
8. 端到端 OA + 重启恢复（MySQL，CI）。

完成后即达成「flow 插件内置 MySQL 持久化」的最后一项任务。
