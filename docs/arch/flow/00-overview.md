# Solon-Flow 迁移到 Go 的设计（flow-go）：总览

> 本文是「Solon-Flow 迁移到 Go」系列的第一篇（总览与可行性）。
> 配套：[`01-go-comparison.md`](01-go-comparison.md)（Java→Go 逐项对照）、[`02-core-design.md`](02-core-design.md)（核心引擎设计）、[`03-config-and-eval.md`](03-config-and-eval.md)（配置 / 快照 / 表达式 / 集成）、[`04-workflow-design.md`](04-workflow-design.md)（工作流子系统）、[`05-tdd-plan.md`](05-tdd-plan.md)（TDD 路线图与分期）。
> 源码参照：`/Users/airhead/WorkSpace/goldsyear/solon-flow`（`solon-flow` 3.9.x + `solon-flow-workflow`）。
> 目标宿主：`aifei-go` 工作区（Go 1.26，零外部依赖，多模块）。
>
> 本文**不写实现代码**，只给出：solon-flow 是什么、迁什么不迁什么、概念词典、模块地图、可行性结论、设计原则——作为后续逐篇的**契约**。

---

## 0. TL;DR（设计摘要）

- **能忠实迁移约 90%**：图模型（Graph/Node/Link + 扁平 `layout`）、节点类型（start/end/activity/exclusive/inclusive/parallel/loop）、核心遍历引擎（`eval` → `node_run` 递归 + `reverting` 恢复）、驱动机制（`FlowDriver` 的 `onNodeStart/onNodeEnd/handleCondition/handleTask`）、`@/#/$` 前缀派发、`FlowContext` 变量域 + `with` 方法作用域、`FlowTrace`/`NodeRecord` 断点恢复、快照序列化（中断/恢复）、拦截器链、子图调用、步数预算、工作流子系统（claim/find/submit + 状态仓储 + 状态控制器）——这些都能在 Go 中以**等价语义**实现。
- **唯一无法 1:1 的是「脚本任务」的任意 Java 语句求值**：solon-flow 用 liquor 的 `Scripts.eval` 执行 `task: "context.put(...)"` 这类**语句级**脚本；Go 没有可用的零成本内嵌解释器。**对策**：默认 `Evaluation` 用 `aifei-go` 自带的 **`enjoy` 表达式引擎**覆盖 `when:` 条件与绝大多数表达式/赋值任务（enjoy 本身支持方法调用、赋值、三元、null-safe）；纯语句脚本走 **`Container` 组件引用（`@bean`）**——后者本就是生产环境推荐路径（类型安全、可测试）。`Evaluation` 是 2 方法接口，任意第三方引擎（yaegi 等）可后续插入。
- **定位**：作为 `aifei-go` 生态的新模块 **`./flow`**（`github.com/crazy-airhead/aifei-go/flow`），**零外部依赖**（仅 Go 标准库 + 内部 `enjoy` + `dami`），填补「**流程编排 / 规则引擎 / 可中断可恢复工作流**」的空白，与现有 `dami`（进程内事件总线）、`nami`（RPC）、`kafka`（跨进程消息）互补。
- **关键设计原则**：①「接口默认方法」→ 小接口 + 可嵌入的 base struct；②`@FunctionalInterface` → Go func 类型；③`ConcurrentHashMap`/`volatile`/`AtomicInteger` → `sync.RWMutex` / `atomic.Bool` / `atomic.Int64`；④`CountDownLatch + AtomicReference<Throwable>` → `errgroup`（并行网关首错优先）；⑤Solon IoC（`@Configuration`/`@Bean`/`getBean`）→ `MapContainer` + 命令式 `aifei.Plugin` 装配；⑥`snack4` + `snakeyaml` → `encoding/json` + `yaml.v3`；⑦异常 → `error` + 哨兵 `flow.Error`。

---

## 1. solon-flow 是什么

`solon-flow` 是面向全场景的 **Java 流程编排框架**，Solon 项目的一部分（亦可嵌入 SpringBoot / jFinal / Vert.X 等）。核心理念：**用扁平的 YAML/JSON「点 + 线」描述一个有向图，由引擎驱动执行**。

支持已知流程编排的各种场景：

| 场景 | 说明 | solon-flow 对应能力 |
|------|------|---------------------|
| 计算 / 任务编排 | 一串步骤按顺序/条件执行 | `activity` 节点 + `task` + `when` |
| 业务规则 / 决策 | 多条件分支、规则集 | `exclusive`/`inclusive` 网关 + 表达式 `when` |
| 可中断可恢复流程 | 人工审批、等回调、跨天长流程 | `context.stop()` + 快照持久化 + `reverting` 恢复 |
| 复杂智能体 | ReActAgent、TeamAgent、Multi-Agent | `parallel` 并行 + 子图 `#sub` + `Container` 组件 |
| 人工任务工作流 | 认领 / 审批 / 跳转 / 终止 | `solon-flow-workflow` 子系统 |

### 1.1 模块全景（Java 侧）

```
solon-flow/                    # 核心引擎（98 个 .java）
  ├ org/noear/solon/flow/      # Graph/Node/Link/Engine/Driver/Context/...
  ├ driver/                    # AbstractFlowDriver / SimpleFlowDriver
  ├ container/                 # MapContainer / SolonContainer
  ├ evaluation/                # LiquorEvaluation（默认，Snel + liquor）
  ├ intercept/                 # FlowInterceptor / FlowInvocation
  ├ integration/               # FlowPlugin / FlowConfigurate（Solon 装配）
  ├ aot/                       # GraalVM 原生镜像资源注册
  └ util/                      # Stepper（区间迭代器）
solon-flow-workflow/           # 工作流子系统（42 个 .java）
  ├ WorkflowExecutor(+Default) # 对外门面：claim/find/submit
  ├ WorkflowDriver             # 装饰核心 driver，实现「人工任务暂停」
  ├ Task/TaskState/TaskAction/WorkflowIntent
  ├ StateController            # Actor/Block/NotBlock
  └ repository/                # InMemory / Redis
solon-flow-projects/           # 第三方表达式引擎适配
  ├ solon-flow-eval-aviator    # AviatorEvaluation
  ├ solon-flow-eval-beetl      # BeetlEvaluation
  └ solon-flow-eval-magic      # MagicEvaluation
solon-flow-designer/           # 可视化设计器（Vue 前端，knife4j 风格）
solon-flow-dataflow/           # 数据流（设计文档阶段，README only）
```

---

## 2. 迁移范围：迁什么、不迁什么

### 2.1 迁（P0–P2，详见 `05-tdd-plan.md`）

| # | 能力 | 来源模块 | 目标 |
|---|------|----------|------|
| 1 | 图模型：Graph/Node/Link/Spec + NodeType | `solon-flow` | `./flow` 根包 |
| 2 | 核心引擎：`eval` 遍历 + `node_run` + 网关语义 | `solon-flow` | `./flow` 根包 |
| 3 | 驱动机制：FlowDriver + AbstractDriver + SimpleDriver | `solon-flow.driver` | `./flow/driver` |
| 4 | 上下文：FlowContext + vars/with/trace/stop | `solon-flow` | `./flow` 根包 |
| 5 | 容器与组件：Container + MapContainer + Task/ConditionComponent | `solon-flow.container` | `./flow/container` |
| 6 | 表达式引擎：Evaluation 接口 + 默认实现 | `solon-flow.evaluation` | `./flow/evaluation`（**默认复用 enjoy**） |
| 7 | 拦截器：FlowInterceptor + FlowInvocation | `solon-flow.intercept` | `./flow/intercept` |
| 8 | 轨迹与恢复：FlowTrace + NodeRecord + `reverting` | `solon-flow` | `./flow` 根包 |
| 9 | 快照序列化：toJson/fromJson（中断/恢复） | `solon-flow` | `./flow` 根包 |
| 10 | 子图调用 / 步数预算 / 临时栈 | `solon-flow` | `./flow/util` + 根包 |
| 11 | YAML/JSON 配置解析 + 图注册 | `solon-flow.integration` | `./flow` 根包（`config.Props` + `embed`） |
| 12 | 工作流子系统：Executor/Driver/Task/State*/Repository | `solon-flow-workflow` | `./flow/workflow` |
| 13 | 集成插件：`aifei.Plugin` 生命周期装配 | `solon-flow.integration` | `./plugins/flow` |
| 14 | **内置 MySQL 持久化：`MysqlStateRepository` + 任务历史** | 新增（参考表 `bpm_flow_repository`/`bpm_flow_task`） | `./plugins/flow`（P2，最后一项任务，见 `06`） |

### 2.2 不迁（明确排除）

| # | 项目 | 原因 |
|---|------|------|
| A | **`solon-flow-designer`**（Vue 可视化设计器） | 纯前端 Web 应用，与 Go 库无关；需要时单独建前端仓库。可在 `03-config-and-eval.md` 记其 `layout` schema 契约，保证设计器产物可直接被 flow-go 消费。 |
| B | **`solon-flow-dataflow`** | 仍处于设计文档阶段（README only），无实现可迁；列为未来方向。 |
| C | **GraalVM AOT（`aot/FlowRuntimeNativeRegistrar`）** | Go 静态编译即原生，`//go:embed` 已天然「AOT」，无需对应物。 |
| D | **Solon IoC 耦合（`@Configuration`/`@Bean`/`Solon.context().getBean`）** | aifei-go 无 IoC 容器；用 `MapContainer` + 命令式装配替代（见 `01` 第 7 节）。 |
| E | **snack4 `ONode` 多态序列化（`Write_ClassName`+`Read_AutoType`）** | Go `encoding/json` 无直接等价；快照 vars 限定为 JSON 原生类型或显式 DTO（见 `03`）。 |
| F | **liquor `Scripts.eval` 任意语句脚本** | 无零成本 Go 等价；用 enjoy（条件/表达式/赋值）+ `@组件`（复杂逻辑）双轨替代（见 `03`）。 |
| G | **RedisStateRepository（`redisx`）** | solon-flow 的 Redis 仓储绑死 `redisx`；aifei-go **不迁 Redis 实现**，改以内置 MySQL 仓储（`MysqlStateRepository`，见 `06`，P2 最后一项任务）。 |

> **结论**：迁移后的 flow-go 是一个**自包含、零外部依赖**的流程引擎库，覆盖 solon-flow 的核心编排能力 + 工作流子系统；脚本/语句求值与可视化设计器留作可插拔扩展。

---

## 3. 概念词典（Java → Go 命名）

> 完整方法签名对照见 [`01-go-comparison.md`](01-go-comparison.md)。

| 中文 | Java 概念 | Java 类型 | Go（建议） | 说明 |
|------|----------|-----------|-----------|------|
| 流程图 | 图 / 流图 | `Graph` / `GraphSpec` | `Graph` / `GraphSpec` | 点+线；Spec 是可变构建器 |
| 流程节点 | 节点 / 流节点 | `Node` / `NodeSpec` / `NodeType` | `Node` / `NodeSpec` / `NodeType` | 可带 task + when |
| 流程连接线 | 连接 / 流连接 | `Link` / `LinkSpec` | `Link` / `LinkSpec` | 可带 when 条件 + priority |
| 流程引擎 | 引擎 / 流引擎 | `FlowEngine` | `Engine`（包名 `flow`） | 执行图，注册多图 |
| 流程驱动器 | 驱动器 | `FlowDriver` | `Driver` | 像 JDBC 驱动，控制执行语义 |
| 流程上下文 | 上下文 | `FlowContext` | `Context` | vars 变量域 + trace + eventBus |
| 流程拦截器 | 拦截器 | `FlowInterceptor` | `Interceptor` | AOP，包整次 eval + per-node 回调 |
| 交换器 | 交换器 | `FlowExchanger` | `Exchanger` | 单次运行的可变状态（步数/栈/标志） |
| 轨迹 | 轨迹 | `FlowTrace` / `NodeRecord` | `Trace` / `NodeRecord` | 每图最后执行节点 = 恢复点 |
| 任务组件 | 任务组件 | `TaskComponent` | `func(Context, *Node) error` | `@bean` 或硬编码 |
| 条件组件 | 条件组件 | `ConditionComponent` | `func(Context) (bool, error)` | `@bean` 或硬编码 |
| 表达式引擎 | 求值器 | `Evaluation` | `Evaluation` | 2 方法：runCondition / runTask |
| 工作流执行器 | — | `WorkflowExecutor` | `workflow.Executor` | claim/find/submit |
| 任务（工作流） | — | `Task` / `TaskState` / `TaskAction` | `Task` / `State` / `Action` | 人工任务模型 |
| 状态控制器 | — | `StateController` | `StateController` | Actor/Block/NotBlock |
| 状态仓储 | — | `StateRepository` | `StateRepository` | InMemory（内置）+ MySQL（`./plugins/flow`，见 06） |

**节点类型（`NodeType`）**：`start` / `end` / `activity` / `exclusive`（XOR 单选）/ `inclusive`（OR 多选）/ `parallel`（AND 全选+汇合）/ `loop`（循环，`$for`/`$in`）。语义详见 `02-core-design.md`。

---

## 4. 模块地图与依赖

### 4.1 在 aifei-go 工作区中的位置

```
Core library（零外部依赖）
  enjoy ──┐  （表达式/模板引擎 → 默认 Evaluation 后端）
  dami  ──┤  （进程内事件总线 → Context.EventBus）
  json    │
  log     │
Standalone framework
  flow ◄──┘  （本设计：核心编排引擎 + 工作流；零外部依赖，InMemory 仓储）
  nami
Runtime
  server / http
Plugin（可选）
  plugins/flow  （aifei.Plugin 装配 + 内置 MysqlStateRepository + 任务历史；见 06）
Example / Test
  _test/flow_test
```

### 4.2 `./flow` 内部包结构（建议）

| 包 | 职责 | 依赖 |
|----|------|------|
| `flow`（根） | Graph/Node/Link/Spec、NodeType、Engine、Exchanger、Context(+default)、Driver 接口、Interceptor 接口、Trace/NodeRecord、Options、Error、TaskDesc/ConditionDesc、Container/Component 接口、Evaluation 接口、加载/注册 | `enjoy`（仅 default eval 间接）、`dami`（仅 Context.EventBus）、`log` |
| `flow/util` | `Stepper`（区间迭代器） | 标准库 |
| `flow/container` | `MapContainer`（默认） | `flow` 根接口 |
| `flow/evaluation` | `Evaluation` 接口 + `EnjoyEvaluation`（默认，复用 enjoy） | `flow` 根接口 + `enjoy` |
| `flow/driver` | `AbstractDriver`（`@/#/$` 派发）+ `SimpleDriver` | `flow` 根 + `evaluation` + `container` |
| `flow/intercept` | `Interceptor` 适配 + `Invocation` 链 | `flow` 根 |
| `flow/workflow` | Executor + WorkflowDriver + Task/State/Action/Intent + StateController + InMemoryRepository | `flow` 根 + `flow/driver` |

> **插件层（`./plugins/flow`，依赖 `./db`，见 [`06-mysql-repository.md`](06-mysql-repository.md)）**：

| 包 | 职责 | 依赖 |
|----|------|------|
| `plugins/flow` | `aifei.Plugin` 装配：读 `flow.*` 配置、glob 图资源、构造引擎与 Executor | `aifei` + `config` + `flow` + `log` |
| `plugins/flow`（mysql_state） | **`MysqlStateRepository`**：`StateRepository` 实现 + 快照 Save/Load（`bpm_flow_repository`） | `flow` + `db` |
| `plugins/flow`（mysql_task） | **`TaskHistoryRecorder`**：任务流转历史（`bpm_flow_task`） | `flow` + `db` |

> 依赖方向严格单向：`workflow`/`driver` → 根接口；`evaluation`/`container` → 根接口；`driver` → `evaluation` + `container`。无环。

### 4.3 go.mod（建议）

```
module github.com/crazy-airhead/aifei-go/flow
go 1.26
require (
    github.com/crazy-airhead/aifei-go v0.0.x   // aifei（Plugin 接口）
    github.com/crazy-airhead/aifei-go/enjoy v0.0.x
    github.com/crazy-airhead/aifei-go/dami v0.0.x
    github.com/crazy-airhead/aifei-go/log v0.0.x
    github.com/crazy-airhead/aifei-go/config v0.0.x   // 仅 Plugin 装配用
    gopkg.in/yaml.v3 // 仅配置解析（config 已传递依赖）
)
```

并在 `go.work` 增加 `use ./flow`；新增测试模块 `_test/flow_test`（`use ./_test/flow_test`）。

> **`./plugins/flow` 是独立模块**（P2）：`require` `aifei`/`config`/`db`/`flow`/`log`（对照 `plugins/dataisolate` 的 go.mod 模式），MySQL 驱动由应用 `db.Init("mysql", dsn)` 提供。核心 `./flow` 因此保持零外部依赖、不耦合 db（InMemory 即可全功能运行）。`go.work` 另加 `use ./plugins/flow`。

---

## 5. 与 aifei-go 现有能力的关系（不重复造轮子）

| solon-flow 依赖 | aifei-go 对应 | 复用方式 |
|-----------------|--------------|----------|
| `dami2.DamiBus`（`Context.eventBus()`） | **`./dami`**（Send/Listen/Call/Stream/LPC） | `Context` 懒初始化一个 `*dami.Bus`，对外暴露 `EventBus()`；语义等价（send 事件、call 请求-响应） |
| liquor `Scripts.eval` + Snel（条件/任务求值） | **`./enjoy`** 表达式引擎（`Expr.Eval(scope, ctrl)`：算术/比较/逻辑/三元/null-safe/方法调用/字段/赋值/Map/Array） | 默认 `EnjoyEvaluation` 包装 `enjoy.Engine`：`runCondition` = `eval` 取布尔（null→false，bool→自身，其它→true，**与 Snel 完全一致的真值规则**）；`runTask` = enjoy 语句求值 |
| snack4 JSON | **`./json`** / `encoding/json` | 快照 `MarshalJSON`/`UnmarshalJSON` |
| snakeyaml | `gopkg.in/yaml.v3`（config 已用） | 图配置 YAML 解析 |
| Solon IoC（`getBean`） | **`MapContainer`**（已在 solon-flow 内） | `@bean` → `map[string]any` 注册表，类型断言取 `TaskComponent`/`ConditionComponent` |
| slf4j | **`./log`** | `log.Logger` 接口 |
| Solon `Plugin` 生命周期 | **`aifei.Plugin`**（`Start()`/`Stop()`） | `flow.Plugin` 实现，命令式装配 |
| `ResourceUtil.scanResources`（classpath glob） | `//go:embed` + `filepath.Glob` + `os.ReadFile` | 图资源加载 |

---

## 6. 设计原则（贯穿所有后续文档）

1. **零外部依赖**：`./flow` 仅用标准库 + 内部 `enjoy`/`dami`/`log`/`config`；不引入 yaegi / cel / redis 客户端等。Redis 仓储走接口，留待 P2 插件。
2. **类型安全**：能用 Go 泛型的地方不用 `any`（如 `GetAs[T]`）；但 vars 本质是 `map[string]any`，保留 `any` + 类型断言。
3. **API 命名**沿用 Java Aifei/solon-flow 习惯（`Eval`/`ClaimTask`/`SubmitTask`/`Register`），与 aifei-go 现有命名一致；包级工厂函数 `flow.NewEngine()` 替代 Java 静态工厂。
4. **并发安全**：`Context.vars` 用 `map` + `sync.RWMutex`（对齐 `config` 模式）；标志位 `atomic.Bool`；步数 `atomic.Int64`；并行网关 `errgroup`。
5. **错误模型**：`error` 返回；引擎内非 `flow.Error` 的 error 用 `flow.Error` 包装（带 graphId/nodeId 上下文），等价于 Java 把非 `FlowException` 包成 `FlowException`。
6. **可测试性（TDD 先行）**：核心逻辑全部可被 `_test/flow_test` 黑盒测试；图用 YAML 字面量定义；不依赖任何外部服务（与 aifei-go 测试约定一致）。
7. **忠实语义**：遍历顺序、网关默认值（link/node 默认 true、exclusive 默认 false）、`reverting` 恢复、`@/#/$` 派发优先级、并行首错优先——逐一对照 Java 实现，测试用例直接搬 solon-flow 的 `features/flow/generated`。

---

## 7. 可行性结论

- **可迁**：核心引擎 + 工作流子系统约 90% 语义等价；最大的语言鸿沟（任意 Java 语句脚本）有 enjoy + 组件双轨兜底，且 `Evaluation` 接口预留扩展。
- **收益**：aifei-go 获得一流的流程编排/规则/工作流能力，与已有 `dami`（事件）、`db`（持久化，可做 StateRepository 后端）、`server`（HTTP 暴露工作流 API）天然组合，形成「Just Service + 流程编排」闭环。
- **风险**：①`reverting` 恢复在多入度网关下的边界（Java 本身有 caveat，见 `02` 第 6 节，需测试固化）；②enjoy 与 Snel 的表达式语法**子集差异**（个别操作符/内置函数），需在 `03` 列出差异表并补齐或标注不支持；③快照多态类型（vars 存自定义结构）需约束为 JSON 友好类型。
- **工作量预估**：核心（P0）≈ 1800–2200 行 Go；工作流（P1）≈ 600–800 行；配置/集成/快照 ≈ 400–600 行；测试 ≈ 与实现 1:1。分期与验收见 `05-tdd-plan.md`。

---

## 8. 阅读路线

1. 先读本篇（总览 + 范围 + 概念）。
2. [`01-go-comparison.md`](01-go-comparison.md)：逐类型/方法对照，Java 特性 → Go 惯用法。
3. [`02-core-design.md`](02-core-design.md)：核心引擎的 Go 接口签名 + 执行模型 + 并发。
4. [`03-config-and-eval.md`](03-config-and-eval.md)：配置 schema、加载注册、快照、表达式、集成插件。
5. [`04-workflow-design.md`](04-workflow-design.md)：工作流子系统的状态机与状态仓储。
6. [`05-tdd-plan.md`](05-tdd-plan.md)：TDD 分期路线、每期测试用例、验收标准。
7. [`06-mysql-repository.md`](06-mysql-repository.md)：内置 MySQL 状态仓储 + 任务历史（最后一项任务）。
