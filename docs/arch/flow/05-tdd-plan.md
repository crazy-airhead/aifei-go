# TDD 路线图与分期（flow-go）

> 本文是「Solon-Flow 迁移到 Go」系列的第六篇（落地计划）。
> 前置：[`00-overview.md`](00-overview.md)～[`04-workflow-design.md`](04-workflow-design.md)。
> 本文给出**以 TDD 思想驱动的分期实现路线**：测试模块布局、每期「先红后绿」的测试用例清单、验收标准、与 solon-flow 原生测试的对照。

---

## 0. TDD 思想如何贯穿本迁移

**核心原则**：每个行为，**先写失败的测试（RED），再写最小实现使之通过（GREEN），再重构（REFACTOR）**。对一次「忠实迁移」而言，TDD 有三个特殊价值：

1. **行为契约来自原版**：solon-flow 自带大量 `features/flow/generated/*` 与 `features/workflow/generated/*` 测试。这些是**现成的金标准**——逐条翻成 Go 测试，RED 驱动实现，GREEN 即「与原版行为等价」。这把「忠实迁移」从口号变成可验证的事实。
2. **隔离语言风险**：`reverting` 恢复、并行网关首错优先、`@/#/$` 派发优先级、网关默认值差异（exclusive 默认 false）——这些是出错概率最高的地方，必须用针对性测试**先固化预期**再实现。
3. **零外部依赖自检**：aifei-go 测试约定要求「无外部服务」。所有测试用 YAML 字面量定义图、内存断言，`go test ./_test/flow_test` 一条命令跑完。

**工作流**（每张卡）：
```
写一个失败测试（引用尚未实现的 API）
  → go test：RED（编译错或断言失败）
  → 写最小实现
  → go test：GREEN
  → 重构（提取/重命名/去重），保持 GREEN
  → 提交（commit）+ 进入下一张卡
```

**验收纪律**：一期未全绿不得进入下一期；每期结束跑全量 `go test ./...`（CLAUDE.md 列出的模块 + `./_test/flow_test`）。

---

## 1. 测试模块布局（遵循 aifei-go 约定）

> 遵循 `CLAUDE.md` 的测试约定：所有 `_test.go` 在 `_test/` 下；外部测试包；自有 module + `replace` + `go.work use`；自包含依赖。

### 1.1 新建 `_test/flow_test`

```
_test/flow_test/
  go.mod                         module github.com/crazy-airhead/aifei-go/_test/flow_test
                                  require github.com/crazy-airhead/aifei-go/flow
                                  replace github.com/crazy-airhead/aifei-go/flow => ../../flow
                                  （+ enjoy/dami/log 间接经 flow）
  graph_test.go                  图模型 / FromDom 解析
  node_link_test.go              Node/Link/Spec/NodeType
  eval_test.go                   Evaluation（EnjoyEvaluation）+ 表达式真值
  driver_test.go                 AbstractDriver @/#/$ 派发
  engine_test.go                 顺序流 / 网关 / 子图 / 步数
  gateway_test.go                exclusive/inclusive/parallel/loop 专项
  interceptor_test.go            拦截器链 + Options
  snapshot_test.go               Context toJson/fromJson + 中断恢复
  stepper_test.go                StepperFrom
  workflow_test.go               Executor claim/find/submit + 状态机
  workflow_actor_test.go         ActorStateController
  workflow_state_test.go         StateController/StateRepository
  issueNNNN_test.go              回归（对应 docs/issues/NNNN，若有）
```

> **MySQL 仓储测试**（P2-b，最后一项任务）：因 MySQL 方言（`ON DUPLICATE KEY UPDATE`/JSON 列）无法内嵌，**纯逻辑测试**（stateKey/JSON codec/缓存）放 `_test/flow_test`（无 DSN 也绿）；**MySQL 集成测试**单列 `_test/flow_mysql_test`（module，`require ./plugins/flow`），由 `FLOW_MYSQL_DSN` 驱动，缺失则 `t.Skip`。详见 [`06-mysql-repository.md`](06-mysql-repository.md) §9 与本文 §12b。

- **包声明**：`package flow_test`，导入 `flow "github.com/crazy-airhead/aifei-go/flow"`，别名子包（`flowdriver "github.com/.../flow/driver"` 等）。
- **`go.work`** 增加 `use ./flow` 与 `use ./_test/flow_test`。
- **自包含**：YAML/JSON 图用字面量字符串（``const yamlDoc = `...` ``）或 `testdata/`；无外部服务。
- **对照原版**：文件名尽量映射 solon-flow 的 `features/flow/generated/coverage/*.java`（如 `SimpleFlowDriverTest`→`driver_test.go`）。

### 1.2 测试辅助

```go
// 解析内联 YAML/JSON 成 *Graph
func mustGraph(t *testing.T, doc string) *flow.Graph {
    t.Helper()
    g, err := flow.GraphFromText([]byte(doc))
    require.NoError(t, err)
    return g
}
// 内存 driver 记录访问的节点（断言遍历顺序）
type recordingDriver struct{ flow.Driver; visited []string }
// 内存组件容器 + 断言调用
```

---

## 2. 分期总览

| 期 | 主题 | 产出 | 预估行 | 依赖 |
|----|------|------|--------|------|
| **P0-a** | 图模型 + 配置解析 | Graph/Node/Link/Spec/NodeType + FromDom（YAML/JSON） | ~450 | — |
| **P0-b** | 求值 + 组件 + 容器 | Evaluation + EnjoyEvaluation + Container/MapContainer + 组件 func 类型 | ~300 | enjoy |
| **P0-c** | 驱动派发 | AbstractDriver（`@/#/$`）+ SimpleDriver + Builder | ~250 | P0-b |
| **P0-d** | 引擎 + 顺序流 | Engine + Exchanger + Temporary + Stepper + start/end/activity | ~500 | P0-a/c |
| **P0-e** | 网关 | exclusive/inclusive/parallel/loop | ~400 | P0-d |
| **P0-f** | 拦截器 + 子图 + 步数 | Interceptor/Invocation/Options + RunGraph + steps | ~250 | P0-e |
| **P0-g** | 轨迹 + 快照 + 恢复 | Trace/NodeRecord + Context.toJSON/fromJSON + reverting | ~300 | P0-f + dami |
| **P1-a** | 工作流基础 | Task/State/Action/Intent + StateController×3 + InMemoryRepo | ~350 | P0-g |
| **P1-b** | 工作流引擎 | WorkflowDriver + Executor（claim/find/submit/auto-forward） | ~450 | P1-a |
| **P2-a** | 扩展 | Event Bus 接线 / ToPlantuml / designer schema 兼容验证 / 集成插件骨架 | ~250 | P1-b |
| **P2-b** | **内置 MySQL 仓储（最后一项任务）** | `./plugins/flow`：MysqlStateRepository + TaskHistoryRecorder + `aifei.Plugin` 装配 | ~450 | P2-a + `db` |

> P0（核心）≈ 2450 行；P1（工作流）≈ 800 行；P2 ≈ 700 行（含 MySQL 仓储）。测试 ≈ 实现 1:1。MySQL 仓储设计详见 [`06-mysql-repository.md`](06-mysql-repository.md)。

---

## 3. P0-a：图模型 + 配置解析

**先红的测试**（`graph_test.go` / `node_link_test.go`）：

| # | 测试 | 断言要点 | 对照原版 |
|---|------|----------|----------|
| 1 | `TestNodeTypeOf` | 大小写不敏感；`"iterator"→Loop`；缺省=`activity` | `NodeTypeTest` |
| 2 | `TestGraphFromYAML_FlatChain` | `com.graph.yml`（5 节点无显式 link）→ 自动连边 + 自动 id `n-1..n-5` | `GraphSpecTest` |
| 3 | `TestGraphFromJSON` | `com.graph.json` → 同结构 | `GraphTest` |
| 4 | `TestLinkFourForms` | 单串/单对象/串数组/对象数组四形态等价 | `LinkSpecTest`/`LinkTest` |
| 5 | `TestAutoLinkReverseOrder` | 逆序自动连边：缺 link 连到「文档序下一个」 | `GraphSpecTest` |
| 6 | `TestStartDeduction` | 显式 `start` 类型；否则首个无入边节点；无则报错 | `GraphTest` |
| 7 | `TestLinkPrioritySort` | `nextLinks` 按 Priority **降序** | `NodeTest` |
| 8 | `TestNodeMetaAccessors` | `GetMeta/AsString/AsBool/AsNumber/HasMeta` 类型转换 | `NodeTest` |
| 9 | `TestSpecBuilderChaining` | `GraphSpec.AddActivity(...).LinkAdd(...)` 链式 | `NodeSpecTest` |
| 10 | `TestConditionDeprecatedAlias` | `condition:` 作为 `when:` 弃用别名仍读 | `LinkSpecTest` |

**GREEN**：实现 §2.1–2.4 的类型 + `FromDom`（含逆序连边）+ `FromText`（按扩展名派发 yaml/json）。
**验收**：10 测试全绿；`go test ./_test/flow_test -run 'Graph|Node|Link|Spec'`。

---

## 4. P0-b：求值 + 组件 + 容器

**先红的测试**（`eval_test.go`）：

| # | 测试 | 断言要点 |
|---|------|----------|
| 1 | `TestRunCondition_Truthy` | null→false、true→true、false→false、非空非 bool→true（**与 Snel 一致**） |
| 2 | `TestRunCondition_Arith` | `amount>=100`、`a>1 && b<9` |
| 3 | `TestRunCondition_NullSafe` | `obj?.field ?? "def"` |
| 4 | `TestRunTask_Assign` | `context.put("result", 111)` 后 `vars["result"]==111` |
| 5 | `TestRunTask_VarBinding` | 裸变量名解析自 `vars`（`a` → `vars["a"]`） |
| 6 | `TestMapContainer` | `PutComponent`/`GetComponent`/`RemoveComponent`；类型断言 |
| 7 | `TestComponentFuncTypes` | `TaskComponent`/`ConditionComponent` 作为 func 类型可赋值 |

**GREEN**：`Evaluation` 接口 + `EnjoyEvaluation`（包装 enjoy）+ `MapContainer` + 组件 func 类型 + `NonSerializable` marker。
**Snel↔enjoy 差异表**：实现期逐操作符核对，差异项记入 `docs/arch/flow/03` 的表 4.3，不支持项标注。
**验收**：7 测试全绿；`go test ./_test/flow_test -run 'Eval|Condition|Container|Component'`。

---

## 5. P0-c：驱动派发

**先红的测试**（`driver_test.go`）：

| # | 测试 | 断言要点 | 对照 |
|---|------|----------|------|
| 1 | `TestTaskDispatch_Priority` | 空→no-op；硬编码 Component > `#子图` > `@组件` > `$meta` > 脚本 | `SimpleFlowDriverTest` |
| 2 | `TestConditionDispatch` | 硬编码 Component > `@组件` > 表达式 | 同 |
| 3 | `TestAtComponent_Task` | `task:"@a"` → container 取 TaskComponent → Run(ctx,node) | `ComJsonTest` |
| 4 | `TestAtComponent_Condition` | `when:"@c"` → ConditionComponent → Test(ctx) | 同 |
| 5 | `TestHashSubgraph` | `task:"#sub"` → engine.GraphOrThrow + RunGraph | `ComJavaTest` |
| 6 | `TestDollarMetaIndirect` | `task:"$script.s1"` → 深度解析 graph.meta → 脚本 | `script_case8` |
| 7 | `TestNodeBindingInTask` | 任务执行期 `vars["node"]` 已绑定；结束后还原 | — |
| 8 | `TestExchangerRestoreFinally` | 任务切换 exchanger 后 finally 还原 | — |
| 9 | `TestTypeMismatchError` | `@bean` 类型不符（task 期望 TaskComponent 却给 ConditionComponent）→ `*flow.Error` | — |

**GREEN**：`AbstractDriver`（`isGraph`/`isComponent` + 派发优先级 + `getDepthMeta`）+ `SimpleDriver` + `Builder`。
**验收**：9 测试全绿；派发优先级与 Java 逐条对齐。

---

## 6. P0-d：引擎 + 顺序流

**先红的测试**（`engine_test.go` / `stepper_test.go`）：

| # | 测试 | 断言要点 | 对照 |
|---|------|----------|------|
| 1 | `TestEval_Sequential` | start→A→B→end 顺序跑，task 按序执行 | `FlowEngineGraphTest` |
| 2 | `TestEval_NodeWhenGuard` | 节点 `when:false` → task 不跑 | `FlowTest` |
| 3 | `TestEval_StopInTask` | task 内 `ctx.Stop()` → 中止后续 | `StepFlowTest` |
| 4 | `TestEval_InterruptBranch` | interrupt 仅杀当前分支 | `ControlFlowTest` |
| 5 | `TestStepBudget` | `steps=N` 到限即 stop | `StepFlowTest` |
| 6 | `TestEngineRegistry` | Load/Unload/Graph(id)/GraphOrThrow；多图共存 | `FlowEngineTest` |
| 7 | `TestDriverRegistry` | RegisterDriver(name)/Default；`graph.driver` 名解析 | `FlowEngineEvalMultiGraphTest` |
| 8 | `TestStepperFrom` | `"1...9"`→[1..8]；`"1:9:2"`→[1,3,5,7]；step<=0 报错 | `StepperTest` |
| 9 | `TestTemporary_StackAndCount` | push/pop、CountIncr/CountSet | — |

**GREEN**：`Engine` + `Exchanger`（含 `NextStep`/`PrveStep`/`Stop`/`Interrupt`/reverting）+ `Temporary` + `StepperFrom` + `nodeRun`（start/end/activity 分支）+ `taskExec` + `condTest`。
**验收**：顺序流闭环可跑；步数/stop/interrupt 正确。

---

## 7. P0-e：网关

**先红的测试**（`gateway_test.go`）：

| # | 测试 | 断言要点 | 对照原版资源 |
|---|------|----------|--------------|
| 1 | `TestExclusive_FirstMatch` | 多条件出连接，首个真者走；默认连接兜底（when 空）；条件默认 **false** | `demo_case2.graph.yml` |
| 2 | `TestExclusive_DefaultLine` | 无条件命中 → 无条件默认连接 | 同 |
| 3 | `TestInclusive_MultiMatch` | 所有真出连接都激活 | `script_case7` |
| 4 | `TestInclusive_Join` | 入度>1 按栈计数等所有分支到齐再合并 | — |
| 5 | `TestParallel_ForkJoin` | 所有出连接跑；汇合等 `countIncr==prevLinks.size()` | — |
| 6 | `TestParallel_Concurrent` | 有 Executor + ≥2 分支 → 并行；`errgroup` 首错优先 | — |
| 7 | `TestLoop_Aggregate` | `$for` 空 → Iterator 栈聚合 | `loop-demo2` |
| 8 | `TestLoop_ForIn_List` | `$in:[a,b,c]` → 每项绑 `$for` 跑 body | `for_case1/2` |
| 9 | `TestLoop_ForIn_Var` | `$in:varName` → 从 context 解析 | `loop-demo1-fetch` |
| 10 | `TestLoop_ForIn_Stepper` | `$in:"1...9"` → Stepper | `loop_full` |

**GREEN**：`nodeRun` 的 exclusive/inclusive/parallel/loop 分支 + 各 `runIn`/`runOut`。
**并行安全重点**：parallel/inclusive 计数器用 `atomic.Int64`，栈操作在 `Temporary.mu` 下（`02` §5）。测试 6 用 `errgroup` 注入首错验证短路。
**验收**：10 测试全绿；网关默认值差异（exclusive false vs 其它 true）被测试固化。

---

## 8. P0-f：拦截器 + 子图 + 步数

**先红的测试**（`interceptor_test.go` / `engine_test.go` 续）：

| # | 测试 | 断言要点 | 对照 |
|---|------|----------|------|
| 1 | `TestInterceptor_FlowWrap` | `InterceptFlow` 包整次 eval；rank 序 | `FlowInterceptorImpl` |
| 2 | `TestInterceptor_NodeCallbacks` | `OnNodeStart`/`OnNodeEnd` per-node；先于 driver | 同 |
| 3 | `TestInterceptor_RankOrder` | 多拦截器按 index 升序 | — |
| 4 | `TestSubgraph_RunGraph` | `task:"#sub"` 跑子图；未到 END → Interrupt 当前分支 | `MultiGraphComplexTest` |
| 5 | `TestSubgraph_SharedStepCount` | 步数预算跨子图累计（Copy 共享指针） | `FlowEngineEvalMultiGraphTest` |
| 6 | `TestGraphTaskComponent` | `Graph.AsTask()` 组件形态 ≡ `#id` 字符串 | — |
| 7 | `TestSteps_PrveStepCompensation` | RunGraph 的 `PrveStep` 补偿 | — |

**GREEN**：`Interceptor` 接口 + `Invocation` 链 + `noopInterceptor` + `Options` + `RunGraph`（含 Interrupt 语义）。
**验收**：子图与步数预算跨图正确。

---

## 9. P0-g：轨迹 + 快照 + 恢复

**先红的测试**（`snapshot_test.go` / `engine_test.go` 续）：

| # | 测试 | 断言要点 | 对照 |
|---|------|----------|------|
| 1 | `TestTrace_RecordNode` | 每图最后节点 = 恢复点；`IsEnd` | `FlowTraceTest`/`NodeRecordTest` |
| 2 | `TestSnapshot_Shape` | JSON 含 stopped/vars/trace；vars 剔除 NonSerializable（含 context 自身） | `FlowContextTest` |
| 3 | `TestSnapshot_RoundTrip` | toJSON→fromJson→vars/stopped/trace 还原 | 同 |
| 4 | `TestInterrupt_Resume_Linear` | stop→快照→恢复→从 lastNode 继续（线性图） | `StopAndResumeDemo` |
| 5 | `TestReverting_FastForward` | 恢复期跳过 taskExec/拦截/记录/步数 | — |
| 6 | `TestReverting_GatewayCaveat` | 多入度网关恢复的已知限制（标注，断言「线性 + 单选」可恢复） | — |
| 7 | `TestContext_EventBus` | `EventBus()` 懒初始化；send/call 可用（dami） | `EventTest`/`event_case1` |

**GREEN**：`Trace`/`NodeRecord` + `Context.toJSON/fromJSON`（`MarshalJSON`/`UnmarshalJSON` + DTO + NonSerializable 过滤）+ reverting 完整链路 + `EventBus`（dami）。
**验收**：中断/恢复闭环；快照 JSON 形状与 Java 等价（除多态类型约束）。

> **P0 完成定义**：上述 §3–§9 全部测试绿；`go test ./_test/flow_test` 通过；核心引擎与 solon-flow `features/flow/generated` 行为等价。

---

## 10. P1-a：工作流基础

**先红的测试**（`workflow_state_test.go`）：

| # | 测试 | 断言要点 | 对照 |
|---|------|----------|------|
| 1 | `TestTaskState_Codes` | Unknown/Waiting/Completed/Terminated 编码 + `StateOf` 反查 | `TaskState` 用例 |
| 2 | `TestTaskAction_TargetState` | Forward→Completed、Back→Waiting、Terminate→Terminated、Restart→Unknown | `TaskActionTests` |
| 3 | `TestBlockStateController` | ACTIVITY 可操作；其余 IsAutoForward | `BasicGraphTests` |
| 4 | `TestActorStateController` | meta actor 匹配 ctx actor；有 actor key → 不自动前涌 | `ActorStateFlowTest` |
| 5 | `TestNotBlockStateController` | 全自动 | `NotBlockStateFlowTest` |
| 6 | `TestInMemoryRepo` | StateGet/Put/Remove/Clear；key=`graphId:nodeId` | — |
| 7 | `TestTask_Accessors` | `Task.NodeID`/`IsEnd`/`LastRecord`（来自 Node） | — |

**GREEN**：Task/State/Action/Intent + 三 StateController + InMemoryStateRepository。
**验收**：状态机数据模型与控制器策略可独立验证。

---

## 11. P1-b：工作流引擎

**先红的测试**（`workflow_test.go` / `workflow_actor_test.go`）：

| # | 测试 | 断言要点 | 对照 |
|---|------|----------|------|
| 1 | `TestClaimTask_Sequential` | start→A→B→C：claim A(Waiting)→submit Forward→claim B→…→claim 返回 nil（完结） | `TaskActionTests.testForwardAction` |
| 2 | `TestSubmit_Back` | ActionBack → 前节点回 Waiting | `TaskActionTests` |
| 3 | `TestSubmit_Terminate` | ActionTerminate → 全流 stop，状态 Terminated | 同 |
| 4 | `TestSubmit_Restart` | ActionRestart → 清状态重来 | 同 |
| 5 | `TestSubmit_Jump` | ForwardJump/BackJump 到指定节点 | `WorkflowJumpActionTest` |
| 6 | `TestAutoForward_Cascade` | 自动穿过无 actor 节点链，停在下一人工任务 | `AdvancedScenarioTests` |
| 7 | `TestFindTask` | 逻辑探测，不限 actor，COMPLETED/TERMINATED 可见 | `ExecutorMethodTests` |
| 8 | `TestFindNextTasks` | 并行分支枚举所有下一任务（Interrupt 不 Stop） | `WorkflowMultiGraphTest` |
| 9 | `TestSubmitTaskIfWaiting` | 双检：非 Waiting/不可操作 → false；锁内再查 | `ExecutorMethodTests` |
| 10 | `TestActorOA_Flow` | `OaActionDemo` 端到端：ctx actor 驱动 | `OaActionDemo`/`OaFlowTest` |
| 11 | `TestPersistence_Checkpoints` | 每次 StatePut 即写 repo；instanceID 为连接键 | — |
| 12 | `TestLocker_NoReentrancy` | `sync.Mutex` 非重入；submit 路径不重入 | — |

**GREEN**：`WorkflowDriver`（HandleTask 的 auto-forward/受控分支转译）+ `Executor`（claim/find/findNext/submit + `forwardHandle` 自动前涌 + `With(Intent)` 临时作用域）。
**端到端走查**：实现期对照 `04` §6 的 ①–④ 逐步断言。
**验收**：工作流子系统与 solon-flow `features/workflow/generated` 行为等价；`go test ./_test/flow_test` 全绿。

> **P1 完成定义**：claim/find/submit/auto-forward 全绿；状态机迁移正确；actor/block/notblock 三模式覆盖。

---

## 12. P2-a：扩展（按需）

| # | 主题 | 测试要点 | 备注 |
|---|------|----------|------|
| 1 | Event Bus 接线 | `context.eventBus().Send/Call` 跨任务解耦 | `event_case1` |
| 2 | `Graph.ToPlantuml()` | 状态图文本导出 | `PumlDemo` |
| 3 | designer schema 兼容 | 设计器产物 YAML 可被 flow-go 消费 | 仅 schema 验证，不迁前端 |
| 4 | 多态 vars 反序列化 | 类型注册表 / tagged union | 快照增强 |
| 5 | 集成插件骨架 | `aifei.Plugin` 生命周期 + config.Props 加载（InMemory 路径） | `03` §5 |
| 6 | 第三方 Eval（yaegi） | 完整语句脚本 | `Evaluation` 插拔 |

## 12b. P2-b：内置 MySQL 仓储（**最后一项任务**，详见 [`06-mysql-repository.md`](06-mysql-repository.md)）

新建模块 `./plugins/flow`（依赖 `./flow` + `./db` + `./config` + `./aifei` + `./log`）。先红的测试：

| # | 测试 | 断言要点 | 层 |
|---|------|----------|----|
| 1 | `TestStateKey` | `graphId:nodeId` 拼接 | 纯单元 |
| 2 | `TestStatesJSON_Codec` | `map→Marshal→GetBytes→Unmarshal` 往返 | 纯单元 |
| 3 | `TestCache_LazyLoad_WriteThrough` | 首次 Get 触发加载；Put 改缓存+写透 | 纯单元（伪 db） |
| 4 | `TestRepo_Upsert` | `InsertOrUpdate` 命中 `uniq_instant_id`：新实例 insert、同 id update | MySQL（DSN） |
| 5 | `TestRepo_StateGetPutRemoveClear` | 四操作往返；`stateKey` 隔离节点 | MySQL |
| 6 | `TestRepo_SnapshotSaveLoad` | graph+vars+states 往返；vars 剔除 NonSerializable | MySQL |
| 7 | `TestTaskHistory_Record` | submit 后 `bpm_flow_task` 一行；source/target/assignee/status/message 正确 | MySQL |
| 8 | `TestTx_Atomicity` | `TransactionCtx` 内 repo+recorder 同事务；中途 error 全回滚 | MySQL |
| 9 | `TestEndToEnd_OA_WithMySQL` | OA 端到端：claim→submit→历史落库→重启 LoadSnapshot 恢复 | MySQL |
| 10 | `TestConcurrency_SingleExecutor` | 同实例并发 submit 由 LOCKER 串行；缓存一致 | MySQL（-race） |

**测试约定（MySQL 方言无法内嵌）**：纯逻辑（stateKey/JSON codec/缓存）用单元+伪 db，无外部依赖必绿；MySQL 方言部分（`ON DUPLICATE KEY UPDATE`/JSON 列）连真实 MySQL，由环境变量 `FLOW_MYSQL_DSN` 提供，缺失则 `t.Skip`（与 aifei-go 既有内嵌约定 sqlite/miniredis/kfake 不同，文档明示）。

**GREEN 顺序**：RepoSchema + stateKey + JSON codec → MysqlStateRepository（ensureLoaded/flush/四操作）→ SaveSnapshot/LoadSnapshot → `Context.GoContext()` + `db.WithCtx` 透传 → TaskHistoryRecorder + Executor 钩子 → `db.TransactionCtx` 跨表原子 → `aifei.Plugin` 装配 + DDL embed → 端到端 OA + 重启恢复。

> 这是整个 flow 迁移的**最后一项任务**：完成后，flow 插件即可上生产（MySQL 持久化 + 任务历史审计 + 中断/恢复）。

---

## 13. 全局验收清单（Definition of Done）

- [ ] `go test ./_test/flow_test` 全绿（P0+P1）。
- [ ] `go test`（CLAUDE.md 全模块清单 + `./_test/flow_test` + `./_test/flow_mysql_test`）全绿，无回归。
- [ ] `./flow` 模块零外部依赖（仅标准库 + 内部 enjoy/dami/log/config）；MySQL 仓储在独立 `./plugins/flow`（依赖 `./db`，驱动由应用提供）。
- [ ] go.work 含 `./flow`、`./_test/flow_test`、`./plugins/flow`（P2）；`go vet ./...` 干净。
- [ ] 测试覆盖：图模型/解析、求值、派发、顺序流、四网关、拦截器、子图、步数、轨迹、快照恢复、工作流 claim/find/submit/auto-forward、三 StateController、**MySQL 仓储 + 任务历史 + 跨表事务 + 重启恢复**。
- [ ] 与 solon-flow 原版测试逐条对照（§3–§11 的「对照」列）。
- [ ] 文档：本系列 7 篇 + `MEMORY.md` 更新 +（可选）`docs/issues/` 回归记录。
- [ ] Snel↔enjoy 差异表落实（`03` 表 4.3），不支持项明示。
- [ ] reverting 在多入度网关的限制写入文档并被测试标注。
- [ ] MySQL 仓储：纯单元测试无 DSN 也绿；DSN 存在时集成测试绿；跨进程 last-write-wins 限制文档化。

---

## 14. 风险与对策（TDD 视角）

| 风险 | 触发 | TDD 对策 |
|------|------|----------|
| 网关默认值记反 | exclusive 应 false，其它 true | §7 测试 1–2 先固化默认值再实现 |
| 并行分支计数竞争 | parallel/inclusive 共享 Temporary | §7 测试 6 用 `-race` 跑；计数 atomic、栈加锁 |
| reverting 恢复越界 | 多入度网关重访次数不定 | §9 测试 6 标注限制；线性/单选恢复必须绿 |
| enjoy/Snel 语法差异 | 个别操作符/内置函数缺失 | §4 差异表 + 测试逐操作符覆盖；缺失走 `@组件` |
| 快照多态类型 | vars 存自定义结构读回失真 | §9 测试 3 约束 JSON 原生类型；自定义结构需自带 UnmarshalJSON |
| `sync.Mutex` 非重入 | submit 重入死锁 | §11 测试 12 验证路径不重入（`SubmitTaskIfWaiting` 直调 `submitTaskDo`） |
| errgroup 无限并发 | 并行分支极多 | 初版不限（分支通常很少）；P2 加 semaphore 时补测试 |

> **`-race` 必跑**：所有涉及并行/并发的测试（网关、Context、Temporary）在 CI 以 `go test -race ./_test/flow_test` 运行。

---

## 15. 提交粒度建议

- **每个测试一个 commit**（RED+GREEN+REFACTOR 可拆 1–3 个 commit），commit message 体现对照的原版用例（如 `test(flow): exclusive first-match [solon-flow demo_case2]`）。
- P0 各子期（a–g）各一个集成 commit + tag；P1 同。
- 每期结束更新 `MEMORY.md` 的进度行。

---

## 16. 起步（P0-a 第一张卡示例）

```go
// _test/flow_test/node_link_test.go  —— RED
package flow_test

import (
    "testing"
    flow "github.com/crazy-airhead/aifei-go/flow"
    "github.com/stretchr/testify/require"
)

func TestNodeTypeOf_CaseInsensitive(t *testing.T) {
    require.Equal(t, flow.NodeStart, flow.NodeTypeOf("START", flow.NodeActivity))
    require.Equal(t, flow.NodeLoop, flow.NodeTypeOf("iterator", flow.NodeActivity)) // 弃用别名
    require.Equal(t, flow.NodeActivity, flow.NodeTypeOf("nonsense", flow.NodeActivity)) // 缺省
}

func TestGraphFromYAML_FlatChain(t *testing.T) {
    doc := `id: c1
layout:
  - { id: "n1", type: "start", link: "n2"}
  - { id: "n2", type: "activity", link: "n3"}
  - { id: "n3", type: "end"}
`
    g, err := flow.GraphFromText([]byte(doc))
    require.NoError(t, err)
    require.Equal(t, "c1", g.ID)
    require.Equal(t, flow.NodeStart, g.Start().Type)
    // n1 → n2 → n3 链式
    require.Equal(t, "n2", g.Start().NextNode().ID)
}
```
→ `go test ./_test/flow_test -run NodeTypeOf`：RED（API 未实现）→ 实现 → GREEN → 提交 → 下一张卡。
