# Phase 4: JSON 模块 / 日志模块 / 工具函数

> 目标：实现 JSON 工具封装、日志抽象、通用工具函数。三个部分各自独立目录，方便后续扩展。

## 1. JSON 模块 (`json/`)

对应 Java 的 `cn.aifei.json` 包 (FastJSON2 集成)。

**目录结构：**
```
json/
└── json.go
```

**Java 版关键类：**
```java
// Json.java — 入口
static JsonString of(String str)
static JsonObject of(Object object)

// JsonKit.java — 配置
JsonKit setReadRowFieldCamelToSnake(boolean)
JsonKit setWriteRowFieldSnakeToCamel(boolean)
JsonKit setWriteDateFormat(String)
JsonKit setWritePrettyFormat(boolean)
JsonKit setJsonFactory(JsonFactory)

// JsonFactory.java — 工厂接口
JsonString getJsonString(String str)
JsonObject getJsonObject(Object object)
```

**Go 版 — 封装标准库 encoding/json：**

```go
// json/json.go
package json

import (
    "encoding/json"
)

// Marshal 序列化 (支持 Row, Page 等自定义类型)
func Marshal(v interface{}) ([]byte, error)

// MarshalIndent 带缩进的序列化
func MarshalIndent(v interface{}, prefix, indent string) ([]byte, error)

// Unmarshal 反序列化
func Unmarshal(data []byte, v interface{}) error

// MarshalString 序列化为字符串
func MarshalString(v interface{}) string

// UnmarshalString 从字符串反序列化
func UnmarshalString(s string, v interface{}) error
```

**后续扩展方向：**
- `json/json_kit.go` — 序列化配置 (驼峰/下划线转换、日期格式等)
- `json/row_reader.go` — Row 的自定义 JSON 反序列化
- `json/row_writer.go` — Row 的自定义 JSON 序列化
- 可替换底层实现 (如 sonic) 而不影响上层 API

**Row 的 JSON 支持 (在 `db/row.go` 中实现)：**

```go
func (r *Row) MarshalJSON() ([]byte, error) {
    return json.Marshal(r.data)
}

func (r *Row) UnmarshalJSON(data []byte) error {
    return json.Unmarshal(data, &r.data)
}
```

---

## 2. 日志模块 (`log/`)

对应 Java 的 `cn.aifei.log` 包 (SLF4J/Log4j2 抽象)。

**目录结构：**
```
log/
└── log.go
```

**Java 版 Log 接口：**
```java
// 5 个级别: trace, debug, info, warn, error
// 每个级别支持: msg, msg+throwable, format+arg, format+args..., Supplier
boolean isTraceEnabled()
void trace(String msg)
void trace(String format, Object arg)
void trace(String format, Object arg1, Object arg2)
void trace(String format, Object... arguments)
void trace(String msg, Throwable t)
void trace(Supplier<String> messageSupplier)
// ... debug, info, warn, error 同理
```

**Go 版 — 封装标准库 log + 接口抽象：**

```go
// log/log.go
package log

import (
    "log"
    "os"
)

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

// 默认实现
type defaultLogger struct {
    level  Level
    logger *log.Logger
}

var defaultLog Logger

func init() {
    defaultLog = &defaultLogger{
        level:  LevelInfo,
        logger: log.New(os.Stdout, "[aifei] ", log.LstdFlags|log.Lshortfile),
    }
}

func Default() Logger       { return defaultLog }
func SetDefault(l Logger)   { defaultLog = l }
func SetLevel(level Level)  { defaultLog.(*defaultLogger).level = level }

// 快捷方法
func Trace(msg string, args ...interface{}) { defaultLog.Trace(msg, args...) }
func Debug(msg string, args ...interface{}) { defaultLog.Debug(msg, args...) }
func Info(msg string, args ...interface{})  { defaultLog.Info(msg, args...) }
func Warn(msg string, args ...interface{})  { defaultLog.Warn(msg, args...) }
func Error(msg string, args ...interface{}) { defaultLog.Error(msg, args...) }
```

**后续扩展方向：**
- `log/zap.go` — Zap 适配器
- `log/logrus.go` — Logrus 适配器
- `log/log4j2.go` — Log4j2 风格适配器
- `log/slf4j.go` — SLF4J 风格适配器

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

// 设置
log.SetDefault(&ZapLogger{zap: zapLogger})
```

---

## 3. 工具函数 (根包 `util.go` + `prop.go`)

对应 Java 的 `cn.aifei.util`，放在框架根包中。

### StrUtil (对应 `cn.aifei.util.StrUtil`)

**Java 版：**
```java
static String firstCharToLowerCase(String str)
static String firstCharToUpperCase(String str)
static boolean isBlank(String str)
static boolean notBlank(String str)
static boolean notBlank(String... strings)
static boolean hasBlank(String... strings)
static boolean notNull(Object... paras)
static String defaultIfBlank(String str, String defaultValue)
static String toCamelCase(String str)
static String toCamelCase(String str, boolean toLowerCaseAnyway)
static String join(String[] stringArray)
static String join(String[] stringArray, String separator)
static String join(List<String> list, String separator)
static boolean equals(String a, String b)
static String getRandomUUID()
static String requireNotBlank(String str, String message)
```

**Go 版：**

```go
// util.go
package aifei

import (
    "strings"
    "unicode"
)

func IsBlank(s string) bool {
    return strings.TrimSpace(s) == ""
}

func NotBlank(s string) bool {
    return !IsBlank(s)
}

func DefaultIfBlank(s, def string) string {
    if IsBlank(s) { return def }
    return s
}

func FirstCharToLower(s string) string {
    if len(s) == 0 { return s }
    runes := []rune(s)
    runes[0] = unicode.ToLower(runes[0])
    return string(runes)
}

func FirstCharToUpper(s string) string {
    if len(s) == 0 { return s }
    runes := []rune(s)
    runes[0] = unicode.ToUpper(runes[0])
    return string(runes)
}

func ToCamelCase(s string) string {
    // snake_case → camelCase
    parts := strings.Split(s, "_")
    for i := 1; i < len(parts); i++ {
        if len(parts[i]) > 0 {
            parts[i] = FirstCharToUpper(parts[i])
        }
    }
    return strings.Join(parts, "")
}

func ToSnakeCase(s string) string {
    // camelCase → snake_case
    var buf strings.Builder
    for i, r := range s {
        if unicode.IsUpper(r) {
            if i > 0 { buf.WriteByte('_') }
            buf.WriteRune(unicode.ToLower(r))
        } else {
            buf.WriteRune(r)
        }
    }
    return buf.String()
}

func Join(arr []string, sep string) string {
    return strings.Join(arr, sep)
}

func Equals(a, b string) bool {
    return a == b
}
```

### Prop (对应 `cn.aifei.util.Prop`)

**Java 版：**
```java
// 加载配置文件
Prop(String fileName)
Prop(File file)
// 追加配置
Prop append(Prop prop)
Prop append(String fileName)
Prop appendIfExists(String fileName)
// 类型安全 getter
String get(String key)
String get(String key, String defaultValue)
Integer getInt(String key)
Integer getInt(String key, Integer defaultValue)
Long getLong(key) / getLong(key, default)
Double getDouble(key) / getDouble(key, default)
Boolean getBoolean(key) / getBoolean(key, default)
boolean containsKey(String key)
boolean isEmpty()
```

**Go 版：**

```go
// prop.go
package aifei

import (
    "bufio"
    "os"
    "strconv"
    "strings"
)

type Prop struct {
    data map[string]string
}

func NewProp() *Prop
func LoadProp(fileName string) (*Prop, error)
func LoadPropIfExists(fileName string) *Prop

func (p *Prop) Append(other *Prop) *Prop
func (p *Prop) AppendFile(fileName string) error

func (p *Prop) Get(key string) string
func (p *Prop) GetDefault(key, def string) string
func (p *Prop) GetInt(key string) (int, error)
func (p *Prop) GetIntDefault(key string, def int) int
func (p *Prop) GetInt64(key string) (int64, error)
func (p *Prop) GetInt64Default(key string, def int64) int64
func (p *Prop) GetBool(key string) (bool, error)
func (p *Prop) GetBoolDefault(key string, def bool) bool
func (p *Prop) Contains(key string) bool
func (p *Prop) IsEmpty() bool
```
