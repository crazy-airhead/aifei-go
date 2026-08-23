# Aifei-Go log：接口化的 5 级日志抽象

> **用一个 `Logger` 接口把日志实现和业务代码解耦。** 默认实现走标准库 `log`（零依赖、可直接跑）；需要 zap/logrus/zapr 时，只换一行 `log.SetDefault(myLogger)`，业务代码零改动。

---

## 1. 背景与定位

Go 的日志生态极其碎片化：标准库 `log`、`log/slog`、`zap`、`zerolog`、`logrus`、`phuslu/log`……每个项目都有自己的偏好。一个框架如果**写死**日志实现（比如到处 `log.Printf` 或 `zap.L().Info`），就会强迫使用方接受同一选择，甚至出现在同一个进程里跑两套日志格式、两套滚动策略的尴尬场面。

Aifei-Go 的 `log` 模块用最朴素的方式解决这个问题：**定义一个最小的 `Logger` 接口，业务代码依赖接口，具体实现可替换**。

### Java Aifei 对应

Java Aifei 的 `aifei-log` 模块也是同样的思路——定义 `Log` 接口，对 slf4j / Log4j2 / Logback 做适配。Go 版本保留了同样的接口抽象理念，但实现层砍掉了 slf4j 那套 MDC/Factory/Adapter，只保留**一个接口 + 一个默认实现**，最小可用。

### 依赖

| 类型 | 依赖 |
|------|------|
| 外部第三方库 | 无 |
| 内部模块 | 无 |
| 标准库 | `log`、`fmt`、`os` |

模块路径：`github.com/crazy-airhead/aifei-go/log`。可独立 `go get`，是整个 Aifei-Go 里依赖最少的模块之一。

---

## 2. 核心概念

模块只有三个核心抽象：

| 抽象 | 类型 | 作用 |
|------|------|------|
| `Level` | `type Level int` | 日志级别枚举（6 档） |
| `Logger` | `interface` | 日志门面接口（5 个输出方法 + 5 个 `IsXxxEnabled` 判定） |
| `defaultLogger` | `struct`（未导出） | 基于标准库 `log.Logger` 的默认实现 |

外加一组**包级便捷函数**（`log.Info`、`log.Debug`...），让业务代码无需持有 `Logger` 实例即可调用。

### 日志级别

```go
const (
    LevelTrace Level = iota   // 0
    LevelDebug                // 1
    LevelInfo                 // 2
    LevelWarn                 // 3
    LevelError                // 4
    LevelOff                  // 5
)
```

| 级别 | 语义 | 典型用途 |
|------|------|---------|
| `LevelTrace` | 最细粒度 | 进入/退出函数、SQL 参数、循环内变量 |
| `LevelDebug` | 调试信息 | 关键中间状态、分支命中 |
| `LevelInfo` | 业务正常运行 | 启动、请求完成、定时任务执行 |
| `LevelWarn` | 异常但可恢复 | 重试、降级、配置缺失走默认 |
| `LevelError` | 错误，需关注 | 写库失败、外部依赖异常 |
| `LevelOff` | 关闭所有日志 | 测试静默运行 |

级别比较采用 `level <= LevelXxx`（数值越大级别越高），所以 `SetLevel(LevelWarn)` 会让 `Trace`/`Debug`/`Info` 全部被过滤，只保留 `Warn`/`Error`。

---

## 3. Logger 接口

```go
// Logger is the logging interface.
type Logger interface {
    Trace(msg string, args ...interface{})
    Debug(msg string, args ...interface{})
    Info(msg string, args ...interface{})
    Warn(msg string, args ...interface{})
    Error(msg string, args ...interface{})

    IsTraceEnabled() bool
    IsDebugEnabled() bool
    IsInfoEnabled() bool
    IsWarnEnabled() bool
    IsErrorEnabled() bool
}
```

设计要点：

1. **`msg string, args ...interface{}`**：和标准库 `Printf` 保持一致，调用方写 `log.Info("user %s login from %s", uid, ip)` 最顺手。
2. **`IsXxxEnabled()` 五连**：给「拼日志参数本身很贵」的场景用——例如序列化大对象后才打日志，应该先判 `if log.Default().IsDebugEnabled()` 再做序列化。这是 slf4j 的经典套路。
3. **不返回 `error`**：日志失败本就不该影响业务流程，默认实现用 `log.Logger.Printf` 内部吞掉写入错误。
4. **不带结构化字段（fields）**：接口刻意没做 `With(k, v)` / `InfoCtx(ctx, ...)`，保持最小。需要结构化日志的实现方可以在自己的 `Logger` 实现里自行加，但走门面时就退化为字符串模板——这是有意的「够用」取舍。

---

## 4. 默认实现

```go
type defaultLogger struct {
    level  Level
    logger *log.Logger   // 标准库的 log.Logger
}

func init() {
    defaultLog = &defaultLogger{
        level:  LevelInfo,                                   // 默认 INFO
        logger: log.New(os.Stdout, "", log.LstdFlags|log.Lshortfile),
    }
}
```

默认实现的特征：

| 特性 | 取值 | 备注 |
|------|------|------|
| 输出目标 | `os.Stdout` | 容器友好；需要重定向到文件由运维处理 |
| 默认级别 | `LevelInfo` | 生产常用，避免 Trace/Debug 洪水 |
| 前缀 | 空 | 输出形如 `2026/07/21 10:23:11 server.go:42 [INFO] ...` |
| Flag | `log.LstdFlags \| log.Lshortfile` | 时间戳 + 文件名:行号 |
| 级别标签 | `[TRACE]`/`[DEBUG]`/`[INFO]`/`[WARN]`/`[ERROR]` | 5 字母级别（TRACE/DEBUG/ERROR）后 1 空格，4 字母级别（INFO/WARN）后 2 空格，消息列对齐 |

输出样例：

```
2026/07/21 10:23:11 service.go:42 [INFO]  user james logged in from 10.0.0.1
2026/07/21 10:23:12 dao.go:88  [DEBUG] SELECT * FROM users WHERE id=?
2026/07/21 10:23:13 server.go:7 [WARN]  retry attempt 2/3
2026/07/21 10:23:14 handler.go:15 [ERROR] db query failed: context deadline exceeded
```

### 级别过滤的实现

每个方法都先做级别判定，避免拼接消息字符串的开销：

```go
func (l *defaultLogger) Info(msg string, args ...interface{}) {
    if l.level <= LevelInfo {
        l.logger.Printf("[INFO]  %s", fmt.Sprintf(msg, args...))
    }
}
```

注意 `args...` 的 `fmt.Sprintf` **在过滤后**才执行，所以 `SetLevel(LevelWarn)` 能真正省下 `Debug`/`Info` 的格式化开销（前提是调用方没有预先把参数拼好）。

---

## 5. 包级 API：便捷函数与全局替换

业务代码里最常见的调用方式是包级函数，不用持有 `Logger` 实例：

```go
log.Info("server started on %s", addr)
log.Warn("cache miss for key %s", key)
log.Error("db error: %v", err)
```

这组函数背后都走一个**包级默认 Logger**：

```go
var defaultLog Logger

func Default() Logger { return defaultLog }
func SetDefault(l Logger) { defaultLog = l }

func SetLevel(level Level) {
    if dl, ok := defaultLog.(*defaultLogger); ok {
        dl.level = level
    }
}
```

`SetDefault` 是整个模块最关键的扩展点：**任何实现 `Logger` 接口的类型都可以替换掉默认实现**。

### 替换示例：接 zap

```go
import "go.uber.org/zap"

type zapLogger struct{ sugared *zap.SugaredLogger }

func (z *zapLogger) Info(msg string, args ...interface{})  { z.sugared.Infof(msg, args...) }
func (z *zapLogger) Warn(msg string, args ...interface{})  { z.sugared.Warnf(msg, args...) }
func (z *zapLogger) Error(msg string, args ...interface{}) { z.sugared.Errorf(msg, args...) }
func (z *zapLogger) Debug(msg string, args ...interface{}) { z.sugared.Debugf(msg, args...) }
func (z *zapLogger) Trace(msg string, args ...interface{}) { z.sugared.Debugf(msg, args...) }
func (z *zapLogger) IsTraceEnabled() bool                  { return true }
func (z *zapLogger) IsDebugEnabled() bool                  { return true }
func (z *zapLogger) IsInfoEnabled() bool                   { return true }
func (z *zapLogger) IsWarnEnabled() bool                   { return true }
func (z *zapLogger) IsErrorEnabled() bool                  { return true }

func main() {
    sugar := zap.NewProductionConfig().Build().Sugar()
    log.SetDefault(&zapLogger{sugared: sugar})
    defer sugar.Sync()

    // 业务代码完全不变
    server.Run(app, ":8080")
}
```

替换后，框架内部所有 `log.Info` 调用（[server](aifei-go.md) 启动日志、[plugins](aifei-go.md) 生命周期日志等）都会走 zap。

### 替换示例：测试用缓冲 Logger

```go
type bufLogger struct{ logs []string }

func (b *bufLogger) Info(msg string, _ ...interface{})  { b.logs = append(b.logs, msg) }
// ... 其余方法类似，IsXxxEnabled 返回 true

func TestService(t *testing.T) {
    orig := log.Default()
    defer log.SetDefault(orig)

    bl := &bufLogger{}
    log.SetDefault(bl)

    runService()
    if len(bl.logs) != 1 || bl.logs[0] != "expected message" {
        t.Fatalf("unexpected logs: %v", bl.logs)
    }
}
```

这种用法在 Aifei-Go 自己的测试里就有（`_test/log_test/log_test.go` 的 `testLogger`）。

---

## 6. 为什么不用 `log/slog`

Go 1.21 引入了 `log/slog`，结构化日志的官方方案。Aifei-Go 没有直接绑定 slog，原因有三：

1. **slog 要求 Go 1.21+**，Aifei-Go 最低支持版本是 Go 1.26，版本虽然够，但**接口契约绑死 slog 会强迫使用方接受 `context.Context` 传参**——这在「Just Service」扁平架构里是负担。
2. **slog 的 `Handler` 抽象和 `Logger` 接口不在同一层**——换底层实现是替换 `Handler`，而 `log.SetDefault(slog.Default())` 并不能直接工作（slog 的 API 形态是 `Info(ctx, msg, args...)`，签名不兼容）。
3. **零依赖原则**：默认实现用 `log.Logger` 已经足够大多数场景；需要结构化的用户自己接 slog/zap 即可。

如果团队标准就是 slog，可以写一个 10 行的适配器把 `Logger` 接口桥接到 slog。

---

## 7. 使用示例

### 典型初始化

```go
package main

import (
    "os"
    "github.com/crazy-airhead/aifei-go/log"
)

func main() {
    // 生产环境调高级别（默认就是 Info，这里显式设置便于切换）
    log.SetLevel(log.LevelInfo)

    // 开发时可降级到 Debug
    if os.Getenv("APP_ENV") == "dev" {
        log.SetLevel(log.LevelDebug)
    }

    log.Info("application starting, pid=%d", os.Getpid())
    // 2026/07/21 10:23:11 main.go:18 [INFO]  application starting, pid=12345
}
```

### 性能敏感路径的级别判定

```go
func (s *UserService) Detail(uid string) (*User, error) {
    if log.Default().IsDebugEnabled() {
        // 拼这个字符串本身有开销，所以先判级别
        log.Debug("query user detail: uid=%s, req=%+v", uid, s.lastRequest)
    }
    // ...
}
```

### 框架内部使用

Aifei-Go 各 [plugins](aifei-go.md)（cache / kafka / nacos / storage / dataisolate...）都通过依赖 `log.Logger` 接口（或直接调用包级函数）来记日志，插件构造时通常接受一个 `logger Logger` 选项，未传则用 `log.Default()`。这保证了**所有插件的日志最终都汇入同一个实现**，不会出现「cache 用 zap、kafka 用 logrus」的割裂状态。

---

## 8. 模块结构

```
log/
└── log.go    # Level 枚举（6 档）+ Logger 接口 + defaultLogger 实现 +
              # 包级便捷函数（Trace/Debug/Info/Warn/Error + SetDefault/SetLevel）
```

- 源码约 111 行，零外部依赖
- 测试在 `_test/log_test`（外部测试包 `package log_test`，覆盖默认实现、自定义 Logger 注入、级别过滤）

---

## 9. 总结

1. **接口抽象**：`Logger` 是 5 个输出方法 + 5 个 `IsXxxEnabled` 的最小门面，业务代码依赖接口而非实现
2. **零依赖默认实现**：基于标准库 `log.Logger`，开箱即用，无需引入任何第三方库
3. **包级全局可替换**：`log.SetDefault(l)` 是唯一扩展点，zap/logrus/slog/自研实现都可一行接入
4. **6 档级别**：`Trace/Debug/Info/Warn/Error/Off`，过滤在格式化之前，避免无谓开销
5. **`IsXxxEnabled` 惯用法**：贵参数（大对象序列化、循环内拼接）先判级别再拼，沿用 slf4j 经典模式
6. **框架一致性**：所有 [plugins](aifei-go.md) 共用同一个 `Logger` 实例，日志格式/输出统一

### 延伸阅读

- [json](json.md)：同为基础工具库，对 `encoding/json` 的轻量封装
- [config](config.md)：同为基础工具库，分层配置加载
- [aifei-go 整体介绍](aifei-go.md)：`log` 在框架分层中的位置
