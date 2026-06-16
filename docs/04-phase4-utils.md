# Phase 4: JSON 模块 / 日志模块 / 工具函数

> 目标：实现 JSON 工具封装、日志抽象。三个部分各自独立目录，方便后续扩展。

## 1. JSON 模块 (`json/`) — 已实现

对应 Java 的 `cn.aifei.json` 包 (FastJSON2 集成)。

**目录结构：**
```
json/
└── json.go
```

**Go 版 — 封装标准库 encoding/json：**

```go
// json/json.go
package json

// Marshal 序列化
func Marshal(v interface{}) ([]byte, error)

// MarshalIndent 带缩进的序列化
func MarshalIndent(v interface{}, prefix, indent string) ([]byte, error)

// Unmarshal 反序列化
func Unmarshal(data []byte, v interface{}) error

// MarshalString 序列化为字符串
func MarshalString(v interface{}) string

// UnmarshalString 从字符串反序列化
func UnmarshalString(s string, v interface{}) error

// ToJSON 快捷序列化（等同于 MarshalString）
func ToJSON(v interface{}) string
```

**后续扩展方向：**
- 可替换底层实现 (如 sonic) 而不影响上层 API
- Row 的 JSON 支持已在 `db/row.go` 中通过 `MarshalJSON`/`UnmarshalJSON` 实现

---

## 2. 日志模块 (`log/`) — 已实现

对应 Java 的 `cn.aifei.log` 包 (SLF4J/Log4j2 抽象)。

**目录结构：**
```
log/
└── log.go
```

**Go 版 — 封装标准库 log + 接口抽象：**

```go
// log/log.go
package log

type Level int

const (
    LevelTrace Level = iota
    LevelDebug
    LevelInfo
    LevelWarn
    LevelError
    LevelOff
)

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

func Default() Logger       // 获取默认 Logger
func SetDefault(l Logger)   // 替换默认 Logger
func SetLevel(level Level)  // 设置日志级别

// 快捷方法
func Trace(msg string, args ...interface{})
func Debug(msg string, args ...interface{})
func Info(msg string, args ...interface{})
func Warn(msg string, args ...interface{})
func Error(msg string, args ...interface{})
```

**与第三方日志库集成示例：**

```go
// 示例: 集成 zap
type ZapLogger struct {
    zap *zap.Logger
}

func (z *ZapLogger) Info(msg string, args ...interface{}) {
    z.zap.Sugar().Infof(msg, args...)
}
// ... 实现其他方法

log.SetDefault(&ZapLogger{zap: zapLogger})
```

---

## 3. 工具函数 — 待实现

对应 Java 的 `cn.aifei.util` 包。计划在 `aifei/` 根包中实现，当前状态：

### StrUtil — **待实现**

对应的 `util.go` 尚未创建。部分字符串转换函数（如 `camelToPath`）已内联在 `router.go` 中。计划实现：

```go
// util.go (计划中)
package aifei

func IsBlank(s string) bool
func NotBlank(s string) bool
func FirstCharToLower(s string) string
func FirstCharToUpper(s string) string
func ToCamelCase(s string) string
func ToSnakeCase(s string) string
```

### Prop — **待实现**

对应的 `prop.go` 尚未创建。计划支持 `.properties` 文件加载：

```go
// prop.go (计划中)
package aifei

type Prop struct { ... }

func LoadProp(fileName string) (*Prop, error)
func (p *Prop) Get(key string) string
func (p *Prop) GetDefault(key, def string) string
func (p *Prop) GetInt(key string) (int, error)
// ...
```
