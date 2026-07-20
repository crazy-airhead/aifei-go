# Aifei-Go XXL-JOB 插件：分布式任务调度执行器

> **执行器自起一个 HTTP 服务，向调度中心（xxl-job-admin）注册心跳、接收 `/run` 触发、回传 `/api/callback` 结果。**以 `aifei.Plugin` 形式集成，业务侧只需 `p.RegTask("myHandler", func)`——调度、阻塞策略、panic 兜底、日志回传全部由插件托管。

---

## 1. 背景与定位

[XXL-JOB](https://github.com/xuxueli/xxl-job) 是国内使用最广的分布式任务调度框架之一，由「调度中心」（`xxl-job-admin`，Java/Dubbo 服务）和「执行器」（业务进程内嵌的 HTTP 端点）两部分组成。调度中心负责定时触发、路由策略、失败重试；执行器负责真正跑任务并把结果回调回去。

`plugins/xxljob` 是 Aifei-Go 的执行器实现，定位：

| 角色 | 职责 |
|------|------|
| **执行器 HTTP 服务** | 在业务进程内开一个独立 HTTP 端口（默认 `9999`），暴露 `/run`、`/kill`、`/log`、`/beat`、`/idleBeat` 五个端点 |
| **任务注册表** | 维护「handler 名 → `TaskFunc`」的注册表，调度中心按 `executorHandler` 名字触发 |
| **调度协议对接** | 启动时向 `xxl-job-admin` 注册、每 20s 心跳、停止时摘除；任务跑完回调 `/api/callback` |
| **框架集成** | 实现 `aifei.Plugin`，`Start()` 读配置建执行器、`Stop()` 摘除并关 HTTP |

### 来源

本插件是 `github.com/xxl-job/xxl-job-executor-go` 的**源码级移植**，适配到 Aifei-Go 的 `config` / `log` / `aifei.Plugin` 体系。原仓库的执行器逻辑、DTO、阻塞策略、调度协议都完整保留；改动集中在：

- 把原 `flag` / 硬编码配置改为 `config.Init` 读 `xxljob.*`
- 把原 `log` 替换为 `github.com/crazy-airhead/aifei-go/log`
- 暴露为 `aifei.Plugin`，并补上 `pendingTasks` 机制让 `RegTask` 可在 `Start` 之前调用

### 依赖

| 类型 | 依赖 |
|------|------|
| 外部第三方库 | `github.com/go-basic/ipv4 v1.0.0`（仅用于自动探测本机 IP 作为执行器注册地址） |
| 内部模块 | `aifei`（Plugin 接口）、`config`（读 `xxljob.*`）、`log`（日志） |
| 标准库 | `net/http`、`encoding/json`、`context`、`sync`、`time`、`runtime/debug` |

模块路径：`github.com/crazy-airhead/aifei-go/plugins/xxljob`。依赖面非常小（仅 `ipv4` 这一个外部库），适合对依赖敏感的生产环境。

---

## 2. 总体架构

执行器需要同时扮演「HTTP 服务端」（接收调度中心的触发）和「HTTP 客户端」（向调度中心注册/回调）。注意执行器端口 **独立于** aifei 应用自身的业务端口。

```
       ┌──────────────────────────────────────────────┐
       │       调度中心 xxl-job-admin (Java)          │
       │       （定时触发、路由、重试、日志查看）       │
       └─────────┬───────────────────────┬────────────┘
                 │                       │
     ①注册/心跳  │                       │ ⑤查日志
   POST /api/registry│                 POST /log
   (每20s, EXECUTOR │                       │
    注册组)          ▼                       │
       ┌──────────────────────────────────────────────┐
       │              xxljob Plugin (执行器)           │
       │                                              │
       │  ┌─────────────┐    ┌────────────────────┐   │
       │  │ registry()  │    │ HTTP server        │   │
       │  │ goroutine   │    │ :9999              │   │
       │  │ 20s 心跳    │    │  /run  /kill       │   │
       │  │             │    │  /log  /beat       │   │
       │  │ Stop→       │    │  /idleBeat         │   │
       │  │ registryRemove│  │                    │   │
       │  └─────────────┘    └─────────┬──────────┘   │
       │                               │ ②触发任务    │
       │                               ▼              │
       │     ┌──────────────────────────────────┐     │
       │     │ regList (handler 名 → TaskFunc)  │     │
       │     │ runList (jobId → 运行中 Task)    │     │
       │     └────────────────┬─────────────────┘     │
       │                      │ ③go task.Run()        │
       │                      ▼                       │
       │     ┌──────────────────────────────────┐     │
       │     │ Task: 执行 → recover → callback  │     │
       │     └────────────────┬─────────────────┘     │
       │                      │ ④结果回调             │
       └──────────────────────┼───────────────────────┘
                              ▼
                   POST /api/callback (code/msg)
```

数据流时序：①`Plugin.Start`→`Init`→registry goroutine 立即 `POST /api/registry`（后每 20s）→ ②admin 按 cron `POST /run` 携带 `RunReq` → ③查 `regList`、按阻塞策略分流、`go task.Run()` → ④跑完 `POST /api/callback` → ⑤admin 查日志时 `POST /log` 调 `LogHandler`。

核心组件：

| 组件 | 文件 | 职责 |
|------|------|------|
| `Plugin` | `plugin.go` | `aifei.Plugin` 实现 + `RegTask`/`Use`/`LogHandler` 代理到执行器 |
| `Executor`（接口） | `executor.go` | 执行器全部能力（初始化、HTTP 路由、注册、停止） |
| `executor`（实现） | `executor.go` | 默认实现：内置 HTTP server、registry goroutine、回调 |
| `Task` / `TaskFunc` | `task.go` | 单次任务抽象；`Task.Run` 带 panic recover |
| `taskList` | `task_list.go` | 线程安全的 `map[string]*Task`，用于 `regList`/`runList` |
| `Options` / `Option` | `options.go` | 函数式配置项 + 默认值 |
| `Config` | `config.go` | `xxljob.*` YAML 加载 + 转 Options |
| DTO | `dto.go` | 与 admin 通信的全部 JSON 结构（`RunReq`/`Registry`/`call`/`LogRes` 等） |
| `Middleware` | `middleware.go` | 任务级中间件链（FIFO） |
| `LogHandler` | `log_handler.go` | 日志查询钩子（默认返回占位响应） |

---

## 3. Executor 接口与生命周期

`Executor` 是执行器的核心接口，定义在 `executor.go`：

```go
type Executor interface {
    Init(...Option)                          // 初始化（开 registry goroutine）
    LogHandler(handler LogHandler)           // 设置日志查询 handler
    Use(middlewares ...Middleware)           // 加任务中间件
    RegTask(pattern string, task TaskFunc)   // 注册任务 handler
    RunTask(writer, request)                 // /run 处理
    KillTask(writer, request)                // /kill 处理
    TaskLog(writer, request)                 // /log 处理
    Beat(writer, request)                    // /beat 处理
    IdleBeat(writer, request)                // /idleBeat 处理
    Run() error                              // 启动后台 HTTP server（非阻塞）
    Stop()                                   // 摘除注册 + 关 HTTP server
}
```

| 方法 | 行为要点 |
|------|---------|
| `Init` | 应用选项、初始化 `regList`/`runList`、计算 `address = ip:port`、**开 registry 心跳 goroutine** |
| `Run` | 起 `http.Server` 监听 `ExecutorPort`；`ListenAndServe` 在 goroutine 里跑，本方法立即返回；`WriteTimeout` 固定 3s |
| `Stop` | 先 `registryRemove()` 摘除，再 `server.Shutdown(ctx)`（5s 超时） |
| `RegTask` | 把 `pattern → TaskFunc`（已套中间件链）写入 `regList` |
| `Use` | 设置中间件链，**仅对其后注册的 task 生效**（`RegTask` 时 `chain` 组装） |
| `LogHandler` | 设置自定义日志查询 handler；未设则用 `defaultLogHandler` |

### HTTP 端点与 admin 协议对应

| 路径 | admin 侧语义 | Executor 方法 |
|------|--------------|---------------|
| `/run` | 触发任务执行 | `RunTask` → `runTask` |
| `/kill` | 终止运行中任务 | `KillTask` → `killTask` |
| `/log` | 查询任务日志（用于前端展示） | `TaskLog` → `taskLog` |
| `/beat` | 心跳探活 | `Beat` → `beat` |
| `/idleBeat` | 忙碌检测（调度路由用） | `IdleBeat` → `idleBeat` |

所有端点共享一个 `http.ServeMux`，由 `executor.Run()` 注册。`mux.HandleFunc("/run", e.runTask)` 之类的小写方法才是实际 handler，导出版本（`RunTask` 等）只是代理。

### Plugin.Start 的执行顺序

```go
func (p *Plugin) Start() error {
    cfg, _ := LoadConfig(p.prefix)              // 1. 读配置
    opts := []Option{SetLogger(p.log)}
    opts = append(opts, cfg.toOptions()...)     // 2. 配置转 Option
    p.exec = NewExecutor(opts...)               // 3. 建执行器
    p.exec.Init()                               // 4. 初始化（开 registry goroutine）
    for _, pt := range pendingTasks {           // 5. 回放 Start 前的 RegTask
        p.exec.RegTask(pt.pattern, pt.task)
    }
    pendingTasks = nil
    SetDefault(p.exec)                          // 6. 安装为包级默认
    return p.exec.Run()                         // 7. 起 HTTP server
}
```

关键点：**registry goroutine 在 `Init` 就开了**（第 4 步），而非 `Run`——执行器会先注册到 admin 才开 HTTP 监听，窗口期内 admin 触发的 `/run` 会失败。这是原 `xxl-job-executor-go` 的固有行为，本移植保留。

---

## 4. 任务（JobHandler）：定义与触发

### TaskFunc 与 Task

```go
type TaskFunc func(cxt context.Context, param *RunReq) string

type Task struct {
    Id        int64              // JobID
    Name      string             // handler 名
    Ext       context.Context    // 执行 context（可被 Cancel）
    Param     *RunReq            // 调度参数
    fn        TaskFunc           // 已套中间件的任务函数
    Cancel    context.CancelFunc // kill 时调用
    StartTime int64
    EndTime   int64
    log       log.Logger
}
```

`TaskFunc` 返回的 `string` 会被原样回传给 admin 作为 `handleMsg`——适合放人类可读的执行结果摘要（如 `"processed 128 rows"`）。

### 注册任务

三种等价方式（按使用场景选）：

```go
// 1. 包级：注册到默认执行器（Plugin.Start 之后才生效）
xxljob.RegTask("cleanupOrders", func(ctx context.Context, param *xxljob.RunReq) string {
    return "deleted 42 rows"
})

// 2. Plugin 实例方法：可在 Start 之前调用（暂存 pendingTasks）
p, _ := xxljob.NewPlugin(nil)
p.RegTask("cleanupOrders", func(ctx context.Context, param *xxljob.RunReq) string {
    return "deleted 42 rows"
})

// 3. Executor 实例方法：手动建执行器场景
exec := xxljob.NewExecutor(xxljob.ServerAddr("http://admin/xxl-job-admin"))
exec.Init()
exec.RegTask("cleanupOrders", ...)
```

**方式 2 是推荐用法**：`Plugin.RegTask` 有 `pendingTasks` 缓冲——即使业务在 `main()` 里先调 `p.RegTask(...)` 再 `aifei.New(WithPlugin(p))`，注册也不会丢。

### 触发流程（runTask）

`/run` 收到请求后的处理逻辑：

```
解析 RunReq JSON
   │
   ▼
regList.Exists(handler)?
   │否 → 回 FailureCode "Task not registered"
   │是
   ▼
runList.Exists(jobId)?                  ← 阻塞策略判定
   │否 → 直接进执行
   │是
   ├── ExecutorBlockStrategy == COVER_EARLY
   │     → Cancel 旧任务、从 runList 删、继续执行新任务
   └── 其他（SERIAL_EXECUTION / DISCARD_LATER）
         → 回 FailureCode "There are tasks running"，结束
   │
   ▼
构造 task.ctx = WithTimeout(ExecutorTimeout) 或 WithCancel
task.Id = JobID; task.Param = param
runList.Set(jobId, task)
go task.Run(callback)           ← 异步执行，主流程立即返回
   │
   ▼
回 SuccessCode（表示已接收，不代表执行完毕）
```

注意：

- **HTTP 响应只表示「接收」**：`/run` 的成功响应 `returnGeneral()` 在任务真正跑完之前就返回。真正的执行结果通过 `/api/callback` 异步回传
- **超时控制**：`RunReq.ExecutorTimeout > 0` 时用 `context.WithTimeout`，否则 `context.WithCancel`（等价无限超时，需 `kill` 才能停）
- **互斥锁**：`runTask` 全程持有 `executor.mu`，所以同一执行器内并发触发是串行处理的

### 阻塞策略

XXL-JOB 协议规定三种阻塞策略，本插件行为：

| 策略 | 常量 | 行为 |
|------|------|------|
| 单机串行 | `SERIAL_EXECUTION` | 已有任务在跑 → 拒绝（回 FailureCode） |
| 丢弃后续 | `DISCARD_LATER` | 已有任务在跑 → 拒绝（与串行行为相同） |
| 覆盖之前 | `COVER_EARLY` | 已有任务在跑 → `Cancel` 旧任务，执行新任务 |

注：源码里 `SERIAL_EXECUTION` 与 `DISCARD_LATER` 走同一分支（都拒绝），这是移植自原仓库的行为，区别在调度中心侧的语义。

### Task.Run：panic 兜底

```go
func (t *Task) Run(callback func(code int64, msg string)) {
    defer func(cancel func()) {
        if err := recover(); err != nil {
            t.log.Info(t.Info()+" panic: %v", err)
            debug.PrintStack()
            callback(FailureCode, fmt.Sprintf("task panic:%v", err))
            cancel()
        }
    }(t.Cancel)
    msg := t.fn(t.Ext, t.Param)
    callback(SuccessCode, msg)
}
```

关键保障：

- **panic 不崩进程**：`defer recover()` 捕获后回调 `FailureCode`，admin 会按失败重试策略处理
- **成功路径回调**：`callback(SuccessCode, msg)` 把 `TaskFunc` 返回的 `msg` 带给 admin
- **回调内做清理**：`callback`（即 `executor.callback`）会 `runList.Del(jobId)` 移除运行记录

---

## 5. 调度协议：注册、心跳、回调

### 注册与心跳

`registry()` 是个无限循环 goroutine，定义在 `executor.go`：

```go
func (e *executor) registry() {
    t := time.NewTimer(0)               // 立即首次执行
    defer t.Stop()
    req := &Registry{
        RegistryGroup: "EXECUTOR",
        RegistryKey:   e.opts.RegistryKey,
        RegistryValue: "http://" + e.address,
    }
    for {
        <-t.C
        t.Reset(20 * time.Second)       // 每 20s 心跳一次
        // POST /api/registry，Header: XXL-JOB-ACCESS-TOKEN
    }
}
```

注册 payload 三字段：

| 字段 | 值 | 含义 |
|------|-----|------|
| `RegistryGroup` | `"EXECUTOR"`（固定） | 注册组，XXL-JOB 协议规定 |
| `RegistryKey` | 来自 `registryKey` 配置 | 执行器名，admin 按此路由任务 |
| `RegistryValue` | `"http://ip:port"` | 执行器 HTTP 地址，admin 触发任务时用 |

心跳间隔**固定 20 秒**（源码硬编码 `time.Second * 20`）。所有失败都只 `log.Error` 不退出——执行器会一直尝试注册。

### 摘除

`Stop()` → `registryRemove()`：

```go
// POST /api/registryRemove（一次性请求，非循环）
// payload 同 registry
```

摘除失败只 `log.Error`，不阻塞后续 Shutdown。

### 结果回调

任务跑完后，`executor.callback` 把结果 `POST /api/callback`：

```go
type call []*callElement

type callElement struct {
    LogID         int64          `json:"logId"`         // 调度日志 id
    LogDateTim    int64          `json:"logDateTim"`    // 调度日志时间
    ExecuteResult *ExecuteResult `json:"executeResult"` // 旧字段
    HandleCode    int            `json:"handleCode"`    // v2.3.0+ 字段
    HandleMsg     string         `json:"handleMsg"`     // v2.3.0+ 字段
}
```

回调 body 同时含新旧两套字段（`ExecuteResult` + `HandleCode/HandleMsg`），兼容不同版本的 admin——这是移植自原仓库的兼容性策略。

### AccessToken 鉴权

所有发往 admin 的请求都带 `XXL-JOB-ACCESS-TOKEN` header：

```go
request.Header.Set("Content-Type", "application/json;charset=UTF-8")
request.Header.Set("XXL-JOB-ACCESS-TOKEN", e.opts.AccessToken)
```

`AccessToken` 为空时仍会设置空字符串 header（admin 侧若没启用 token 校验，空值无影响）。

### kill 与 idleBeat

| 操作 | 触发 | 行为 |
|------|------|------|
| `killTask` | `POST /kill` `{jobId}` | 查 `runList`，找到则 `task.Cancel()` 并从列表删；找不到回 FailureCode |
| `idleBeat` | `POST /idleBeat` `{jobId}` | 查 `runList`，找到（仍在跑）回 FailureCode；未找到回 SuccessCode（表示空闲） |

`idleBeat` 的语义是「忙碌检测」：调度中心路由任务前先问「这个 jobId 是否在跑」，避免重复分发。注意它**按 jobId 查**，不是按 handler 名。

---

## 6. 日志回传（LogHandler）

admin 前端查看任务日志时，会调执行器的 `/log` 端点。`taskLog` 处理逻辑：

```go
req := &LogReq{LogDateTim, LogID, FromLineNum}
if e.logHandler != nil {
    res = e.logHandler(req)        // 自定义 handler
} else {
    res = defaultLogHandler(req)   // 占位响应
}
```

`LogReq` 字段（来自 admin）：

| 字段 | 含义 |
|------|------|
| `LogDateTim` | 调度日志时间戳 |
| `LogID` | 调度日志 id（与 `RunReq.LogID` 对应） |
| `FromLineNum` | 起始行号（滚动加载用） |

`LogRes` 字段（返回给 admin）：

| 字段 | 含义 |
|------|------|
| `FromLineNum` / `ToLineNum` | 当前返回的行号区间 |
| `LogContent` | 日志正文（纯文本） |
| `IsEnd` | 是否已加载到底（false 时 admin 会继续翻页） |

### 自定义 LogHandler

应用注入自己的日志实现（插件本身不落盘——执行器只负责协议对接，存储由应用决定）：

```go
p.LogHandler(func(req *xxljob.LogReq) *xxljob.LogRes {
    lines, isEnd := myLogStore.Read(req.LogID, req.FromLineNum)
    return &xxljob.LogRes{
        Code: xxljob.SuccessCode,
        Content: xxljob.LogResContent{
            FromLineNum: req.FromLineNum,
            ToLineNum:   req.FromLineNum + len(lines) - 1,
            LogContent:  strings.Join(lines, "\n"),
            IsEnd:       isEnd,
        },
    }
})
```

未配 handler 时走 `defaultLogHandler`，返回固定占位文本 `"This is the default log response, ..."` 保证协议仍能响应。

---

## 7. Middleware：任务级切面

`Middleware`（`middleware.go`）是 `TaskFunc` 的装饰器：

```go
type Middleware func(TaskFunc) TaskFunc

func (e *executor) chain(next TaskFunc) TaskFunc {
    for i := range e.middlewares {
        next = e.middlewares[len(e.middlewares)-1-i](next)
    }
    return next
}
```

### 组装顺序：FIFO

`chain` 的实现是「倒序遍历、依次包裹」，最终效果是 **FIFO**：

```go
exec.Use(A, B, C)
exec.RegTask("foo", taskFn)
// 实际执行链：A(B(C(taskFn)))
// 调用顺序：A 前 → B 前 → C 前 → taskFn → C 后 → B 后 → A 后
```

### 注册时机敏感

中间件链在 `RegTask` 时固化进 `Task.fn`。**后加的中间件不会作用于已注册的任务**：

```go
p.RegTask("early", earlyFn)    // early 的链为空
p.Use(recoverMiddleware)       // 新增
p.RegTask("late", lateFn)      // late 的链含 recover
// early 不会被 recoverMiddleware 包裹
```

实战建议：在 `Plugin.Start` **之前**调 `p.Use(...)`（依赖 `pendingTasks` 的缓冲，所有 `RegTask` 在 Start 时统一回放）。可惜 `Plugin.Use` 只在 `exec != nil` 时直接转发，**Start 前调用是空操作**（这是当前实现的限制，如需在 Start 前加中间件，建议改用 `NewExecutor` + 手动集成）。

### 典型用法

链路追踪/指标埋点是常见场景——middleware 里可注入 trace id 或记录耗时：

```go
exec.Use(func(next xxljob.TaskFunc) xxljob.TaskFunc {
    return func(ctx context.Context, param *xxljob.RunReq) string {
        ctx = context.WithValue(ctx, traceKey, newTraceID())
        start := time.Now()
        msg := next(ctx, param)
        metrics.Observe(param.ExecutorHandler, time.Since(start))
        return msg
    }
})
```

注意：插件自带的 `Task.Run` 已有 panic recover，middleware 里不必再做。

---

## 8. 配置

配置根 key 默认为 `xxljob`（可通过 `NewPlugin(logger, "custom.prefix")` 改写）。

### YAML 示例

```yaml
xxljob:
  serverAddr: "http://127.0.0.1:8080/xxl-job-admin"  # 调度中心地址（必填）
  accessToken: "xxxx-token"                          # admin 开启 token 校验时填
  executorIp: ""                                     # 留空 → 自动探测本机 IPv4
  executorPort: "9999"                               # 执行器 HTTP 端口
  registryKey: "my-app-jobs"                         # 执行器名，admin 按此路由
  timeoutMs: 5000                                    # 调 admin 的 HTTP 超时（ms）
  logDir: "/var/log/aifei/xxljob"                    # 应用自定义日志目录（插件本身不读）
```

### Config 结构体

```go
type Config struct {
    ServerAddr   string        `yaml:"serverAddr"`
    AccessToken  string        `yaml:"accessToken"`
    Timeout      time.Duration `yaml:"timeout"`
    ExecutorIp   string        `yaml:"executorIp"`
    ExecutorPort string        `yaml:"executorPort"`
    RegistryKey  string        `yaml:"registryKey"`
    LogDir       string        `yaml:"logDir"`
}
```

### 加载机制

`LoadConfig(prefix)`（`config.go`）逐字段读 `config.GetStr` / `config.GetInt`：

```go
cfg := &Config{
    ServerAddr:   config.GetStr(prefix + ".serverAddr"),
    AccessToken:  config.GetStr(prefix + ".accessToken"),
    Timeout:      time.Duration(config.GetInt(prefix+".timeoutMs")) * time.Millisecond,
    ExecutorIp:   config.GetStr(prefix + ".executorIp"),
    ExecutorPort: config.GetStr(prefix + ".executorPort"),
    RegistryKey:  config.GetStr(prefix + ".registryKey"),
    LogDir:       config.GetStr(prefix + ".logDir"),
}
// 额外：timeout 也可写成 duration 字符串（如 "5s"），覆盖 timeoutMs
if timeoutStr := config.GetStr(prefix + ".timeout"); timeoutStr != "" {
    cfg.Timeout, _ = time.ParseDuration(timeoutStr)
}
```

设计要点：

- **逐字段读，不 `SubBind`**：字段都是标量，无需 YAML round-trip；`GetStr`/`GetInt` 足够（见 [config.md](config.md)）
- **`timeout` 双写兼容**：`timeoutMs`（int）和 `timeout`（duration string）都支持，后者覆盖前者；对运维同学友好

### 配置 key 一览

| Key | 类型 | 默认 | 说明 |
|-----|------|------|------|
| `xxljob.serverAddr` | string | — | 调度中心 URL，**必填** |
| `xxljob.accessToken` | string | — | API 访问 token（admin 开启校验时用） |
| `xxljob.executorIp` | string | 自动探测 | 执行器注册时上报的 IP；留空走 `ipv4.LocalIP()` |
| `xxljob.executorPort` | string | `9999` | 执行器 HTTP 端口 |
| `xxljob.registryKey` | string | `golang-jobs` | 执行器名，admin 按此路由任务 |
| `xxljob.timeoutMs` | int | — | 调 admin 的 HTTP 超时（毫秒） |
| `xxljob.timeout` | duration string | — | 同上，duration 写法（如 `5s`），覆盖 `timeoutMs` |
| `xxljob.logDir` | string | — | 日志目录（应用自定义，插件不直接消费） |

### Option 函数式配置

不通过 YAML 时，可用 `Option` 直接传参（`options.go`）：

```go
exec := xxljob.NewExecutor(
    xxljob.ServerAddr("http://admin/xxl-job-admin"),
    xxljob.RegistryKey("my-app-jobs"),
    xxljob.ExecutorPort("9999"),
    xxljob.AccessToken("token"),
    xxljob.SetLogger(myLogger),
)
```

可用 Option：`ServerAddr` / `AccessToken` / `ExecutorIp` / `ExecutorPort` / `RegistryKey` / `SetLogger`。默认值在 `newOptions`：`ExecutorIp = ipv4.LocalIP()`、`ExecutorPort = "9999"`、`RegistryKey = "golang-jobs"`。

---

## 9. 集成方式

### 最小可用代码

```go
package main

import (
    "context"
    "os"

    "github.com/crazy-airhead/aifei-go/aifei"
    "github.com/crazy-airhead/aifei-go/config"
    "github.com/crazy-airhead/aifei-go/plugins/xxljob"
    "github.com/crazy-airhead/aifei-go/server"
)

func main() {
    // 1. 加载配置（读 xxljob.* 子树）
    if err := config.Init(os.Args); err != nil {
        panic(err)
    }

    // 2. 建插件，注册任务（Start 之前调用是安全的）
    p, err := xxljob.NewPlugin(nil)
    if err != nil {
        panic(err)
    }
    p.RegTask("syncOrders", func(ctx context.Context, param *xxljob.RunReq) string {
        // 业务逻辑：param.ExecutorParams 是 admin 配置的任务参数
        n := doSync(ctx, param.ExecutorParams)
        return "synced " + param.ExecutorParams + " rows: " + n
    })

    // 3. 注册为 aifei 插件，启动时自动 Init + Run
    app := aifei.New(aifei.WithPlugin(p))
    server.Run(app, ":8080")
}
```

### 多任务与自定义日志

```go
p.RegTask("cleanup", cleanupFn)
p.RegTask("report",  reportFn)

// 自定义日志 handler（可选，插件本身不落盘）
p.LogHandler(func(req *xxljob.LogReq) *xxljob.LogRes {
    return myLogStore.Read(req.LogID, req.FromLineNum)
})
```

### admin 侧配置

执行器跑起来后到 `xxl-job-admin` 后台：**执行器管理**→新建（AppName 填 `registryKey` 一致，注册方式选「自动注册」）→ **任务管理**→新建（选执行器、JobHandler 填注册名、配 Cron）。

### 手动用法（不用 Plugin）

```go
exec := xxljob.NewExecutor(
    xxljob.ServerAddr("http://admin/xxl-job-admin"),
    xxljob.RegistryKey("standalone-jobs"),
)
exec.Init()
exec.RegTask("foo", fooFn)
defer exec.Stop() // 优雅摘除 + Shutdown
if err := exec.Run(); err != nil { panic(err) }
```

适合不跑 aifei 服务、但需要嵌入任务执行能力的独立 worker。

---

## 10. DTO 与响应码

`dto.go` 定义了与 admin 通信的全部 JSON 结构：

| 方向 | 类型 | 用途 | 关键字段 |
|------|------|------|---------|
| ↓ | `RunReq` | 触发任务 | `JobID`、`ExecutorHandler`、`ExecutorParams`、`ExecutorBlockStrategy`、`ExecutorTimeout`、`LogID`、`BroadcastIndex/Total` |
| ↓ | `killReq` / `idleBeatReq` | 终止/忙碌检测 | `JobID` |
| ↓ | `LogReq` | 查日志 | `LogDateTim`、`LogID`、`FromLineNum` |
| ↑ | `Registry` | 注册/摘除 | `RegistryGroup`、`RegistryKey`、`RegistryValue` |
| ↑ | `call` (`[]*callElement`) | 结果回调 | `LogID`、`ExecuteResult{Code,Msg}`、`HandleCode`、`HandleMsg` |
| ↑ | `LogRes` | 日志响应 | `Code`、`Content{FromLineNum,ToLineNum,LogContent,IsEnd}` |

响应码（`constants.go`）只有 `SuccessCode = 200` / `FailureCode = 500`，是 XXL-JOB 协议级约定。`returnGeneral`/`returnCall`/`returnKill`/`returnIdleBeat`（`util.go`）是产出 `{"code":..., "msg":...}` JSON 的 helper。

回调 body 同时含新旧两套字段（`ExecuteResult` + `HandleCode/HandleMsg`），兼容不同版本 admin。`RunReq` 的 `GlueType`/`GlueSource` 是 GLUE 脚本模式字段——本插件**不执行 GLUE 脚本**（原 `xxl-job-executor-go` 也不支持，那是 Java 执行器的能力），GLUE 任务的脚本会以 `ExecutorParams` 传给 `TaskFunc`，业务自行解析。`BroadcastIndex/Total` 用于分片广播，业务在 `TaskFunc` 里读这两个字段做分片处理。

---

## 11. 模块结构

```
plugins/xxljob/
├── xxljob.go        # 包文档 + 包级默认（SetDefault/DefaultExecutor/RegTask）+ ErrNoDefault
├── plugin.go        # aifei.Plugin 实现 + RegTask/Use/LogHandler（含 pendingTasks 缓冲）
├── executor.go      # Executor 接口 + executor 实现（HTTP 路由/注册/回调/任务调度）
├── task.go          # TaskFunc + Task.Run（带 panic recover）
├── task_list.go     # taskList 线程安全 map（regList / runList 共用）
├── middleware.go    # Middleware 类型 + chain（FIFO 组装）
├── log_handler.go   # LogHandler 类型 + defaultLogHandler
├── options.go       # Options + Option 函数式配置项 + 默认值
├── config.go        # Config + LoadConfig（xxljob.* YAML 加载）
├── dto.go           # 全部 DTO（RunReq/Registry/call/LogReq/LogRes 等）+ 阻塞策略常量
├── constants.go     # SuccessCode / FailureCode
├── util.go          # Int64ToStr + returnCall/returnKill/returnIdleBeat/returnGeneral
├── go.mod           # 依赖 go-basic/ipv4 v1.0.0
├── go.sum
└── README.md        # 速查（配置表 + 最小示例）
```

源码约 1,020 行（不含 go.mod/go.sum/README），是 Aifei-Go 中较大的插件之一。无独立测试模块（调度协议测试需要 admin 实例，不在 `_test/` 体系内）。

---

## 12. 总结

Aifei-Go XXL-JOB 插件围绕几个核心设计原则构建：

1. **协议移植优先**：完整保留原 `xxl-job-executor-go` 的 HTTP 路由、注册/心跳/回调时序、DTO 结构，与现有 admin 后台零适配成本
2. **插件化集成**：业务代码只见 `p.RegTask(name, fn)`，执行器端口、注册协议、panic 兜底全部由插件托管；`aifei.Plugin` 接口本身见 [core.md](core.md)
3. **panic 不崩**：`Task.Run` 自带 `recover` + `debug.PrintStack()`，失败任务以 `FailureCode` 回调，admin 按重试策略兜底
4. **异步触发 + 异步回调**：`/run` 即时响应「已接收」，执行结果通过 `/api/callback` 异步回传；admin 的失败重试、慢任务监控都依赖这一解耦
5. **阻塞策略完备**：三种官方策略全部实现，其中 `COVER_EARLY` 通过 `context.CancelFunc` 取消旧任务，与 Go 的 context 模型天然契合
6. **日志策略开放**：插件只定义 `LogHandler` 接口和协议 DTO，不绑定存储实现；应用自由选文件/对象存储/ELK
7. **依赖极简**：仅一个外部库 `go-basic/ipv4`（探测本机 IP），核心逻辑全用标准库；适合对依赖敏感的生产环境
8. **双写配置友好**：`timeoutMs`（int）与 `timeout`（duration string）双 key 并存，照顾运维与开发者两种写代码风格

### 延伸阅读

- [core.md](core.md) — `aifei.Plugin` 接口（`Start()`/`Stop()` 生命周期）
- [config.md](config.md) — `config.GetStr`/`GetInt` 的分层加载机制
- [data-isolate.md](data-isolate.md) — 同系列 Plugin 风格范例
- XXL-JOB 官方仓库：`github.com/xuxueli/xxl-job`
- 原执行器移植源：`github.com/xxl-job/xxl-job-executor-go`
