# Phase 3 实施提示词: JSON / 日志模块

## Prompt 3.1: JSON 模块

```
在 aifei 框架的 json/ 子包中创建 JSON 工具:

json/json.go:
- 封装标准库 encoding/json
- 提供简洁的 API

函数:
- Marshal(v interface{}) ([]byte, error)
- MarshalIndent(v interface{}, prefix, indent string) ([]byte, error)
- Unmarshal(data []byte, v interface{}) error
- MarshalString(v interface{}) string — 序列化为字符串，出错返回 "{}"
- UnmarshalString(s string, v interface{}) error
- ToJSON(v interface{}) string — MarshalString 别名

注意:
- 仅使用标准库 encoding/json
- MarshalString 对 error 做静默处理，返回 "{}"
- 这是纯工具包，无状态
```

---

## Prompt 3.2: 日志模块

```
在 aifei 框架的 log/ 子包中创建日志模块:

log/log.go:
- 封装标准库 log
- 提供 Level 控制和接口抽象
- 支持替换底层实现

Level 类型: int
- LevelTrace Level = iota
- LevelDebug
- LevelInfo
- LevelWarn
- LevelError
- LevelOff

Logger 接口:
- Trace(msg string, args ...interface{})
- Debug(msg string, args ...interface{})
- Info(msg string, args ...interface{})
- Warn(msg string, args ...interface{})
- Error(msg string, args ...interface{})
- IsTraceEnabled() bool
- IsDebugEnabled() bool
- IsInfoEnabled() bool
- IsWarnEnabled() bool
- IsErrorEnabled() bool

defaultLogger 结构体:
- level Level
- logger *log.Logger (标准库)
- 实现 Logger 接口
- 每个方法先检查 level，再格式化输出: [LEVEL] message

全局:
- var defaultLog Logger — 默认 defaultLogger，level=Info
- Default() Logger
- SetDefault(l Logger)
- SetLevel(level Level)
- 快捷函数: Trace/Debug/Info/Warn/Error (转发到 defaultLog)

输出格式: [LEVEL] 2006/01/02 15:04:05 file.go:123 message
```

---

## 验证 Prompt

```
验证 Phase 3:

1. 测试 json:
   data := map[string]interface{}{"name": "james", "age": 18}
   s := json.MarshalString(data)
   验证 s 包含 name 和 age

2. 测试 log:
   log.SetLevel(log.LevelDebug)
   log.Debug("test debug %s", "message")
   log.Info("test info")
   log.Warn("test warn")
   log.Error("test error")
   验证输出格式正确

3. 替换日志实现:
   type TestLogger struct { logs []string }
   实现 Logger 接口
   log.SetDefault(&TestLogger{})
   验证自定义 logger 工作
```
