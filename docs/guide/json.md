# Aifei-Go json：标准库的轻量包装与容错约定

> **以一行「出错返回 `{}`」的容错约定，换取业务代码里永不崩的 JSON 序列化。**对标准库 `encoding/json` 做最薄一层封装，补足 Go 原生 API 在「直接要 string」「失败不 panic」两个高频场景下的缺口。

---

## 1. 背景与定位

Aifei-Go 的核心库遵循「**零外部依赖，仅用 Go 标准库**」的约束。JSON 处理也不例外：`json` 模块没有引入 `json-iterator/go`、`goccy/go-json` 或任何第三方高性能实现，而是直接基于标准库 `encoding/json`，在其之上加一层约定式的薄壳。

### 为什么还需要封装

Go 标准库 `encoding/json` 本身能力完备，但在业务代码里反复写下面这两类样板会让人疲惫：

| 痛点 | 标准库写法 | 每次都写的原因 |
|------|-----------|----------------|
| 拿到 `string` 而不是 `[]byte` | `b, err := json.Marshal(v); s := string(b)` | 模板渲染、HTTP 响应拼装等场景直接要字符串 |
| 序列化失败不想中断流程 | `if err != nil { return "{}" }` | 容错日志、降级响应里更关心「能不能继续跑」 |

Aifei-Go 的 `json` 模块把这两类样板固化成函数：**`MarshalString`/`ToJSON`**（直接返回 `string`）与 **`MarshalString` 出错返回 `"{}"`** 的容错约定。

### Java Aifei 对应

Java Aifei 的 `aifei-json` 模块基于 Jackson / FastJSON 做了一层工具类封装（`JsonUtil.toJson` / `JsonUtil.parse`），同样是「工具方法 + 容错」的定位。Go 版本对应保留了 `ToJSON` 这种 Java 风格的命名（与 Java 版 `JsonUtil.toJson` 对仗），便于 Java 团队迁移。

### 依赖

| 类型 | 依赖 |
|------|------|
| 外部第三方库 | 无 |
| 内部模块 | 无 |
| 标准库 | `encoding/json` |

模块路径：`github.com/crazy-airhead/aifei-go/json`。可独立 `go get`，不会传染任何依赖。

---

## 2. 核心 API

整个模块只有 7 个函数，全部定义在 `json/json.go`（约 37 行）：

| 函数 | 签名 | 行为 |
|------|------|------|
| `Marshal` | `func Marshal(v interface{}) ([]byte, error)` | 等价于 `encoding/json.Marshal` |
| `MarshalIndent` | `func MarshalIndent(v interface{}, prefix, indent string) ([]byte, error)` | 等价于 `encoding/json.MarshalIndent` |
| `Unmarshal` | `func Unmarshal(data []byte, v interface{}) error` | 等价于 `encoding/json.Unmarshal` |
| `MarshalString` | `func MarshalString(v interface{}) string` | 序列化为 `string`；**出错返回 `"{}"`** |
| `UnmarshalString` | `func UnmarshalString(s string, v interface{}) error` | 从 `string` 反序列化 |
| `ToJSON` | `func ToJSON(v interface{}) string` | `MarshalString` 的语义别名 |

三类接口的职责划分：

```
            []byte 入口                    string 入口
            ───────────                    ──────────
   序列化 →  Marshal         MarshalIndent   MarshalString / ToJSON
   反序列化 → Unmarshal                       UnmarshalString
```

---

## 3. 关键设计：容错约定

`Marshal` 与 `Unmarshal` 是对标准库的**完全透传**——签名一致、错误原样返回。真正的设计差异在 `MarshalString`：

```go
// MarshalString serializes a value to JSON string.
func MarshalString(v interface{}) string {
    data, err := json.Marshal(v)
    if err != nil {
        return "{}"
    }
    return string(data)
}
```

这里有一处刻意的权衡：**序列化失败时返回 `"{}"` 而不是 `""`**。

| 返回值 | 优点 | 缺点 |
|--------|------|------|
| `""`（空串） | 一眼能看出「出错了」 | 下游若直接拼接进 JSON 响应体，会破坏整体结构；前端 `JSON.parse` 必抛异常 |
| `"{}"`（空对象） | 下游 `JSON.parse` 永不崩；可继续走降级逻辑 | 看起来像「合法但空」的数据，错误被吞 |

Aifei-Go 选择了后者，理由是：

1. 业务侧最常见的用法是「把任意值塞进 `{code, msg, data}` 响应结构的 `data` 字段」，此时即便序列化失败，框架仍能返回一个结构合法的响应（`data: {}`），由前端按空数据处理。
2. 真正需要感知错误的场景，应使用 `Marshal`（拿原始 `error`）而不是 `MarshalString`。

> **设计取舍**：如果业务对「序列化失败必须可见」有强需求（如审计、计费字段），请改用 `Marshal` 并显式处理 `error`；`MarshalString` 是为「写日志、拼响应、模板渲染」这种容忍降级的场景设计的。

---

## 4. 使用示例

### 序列化

```go
package main

import (
    "fmt"
    "github.com/crazy-airhead/aifei-go/json"
)

type User struct {
    Name string `json:"name"`
    Age  int    `json:"age"`
}

func main() {
    u := User{Name: "james", Age: 18}

    // 直接拿 string
    fmt.Println(json.MarshalString(u))
    // {"name":"james","age":18}

    // 带缩进（调试日志友好）
    b, _ := json.MarshalIndent(u, "", "  ")
    fmt.Println(string(b))
    // {
    //   "name": "james",
    //   "age": 18
    // }

    // 容错：不可序列化的值（如 channel）拿到 "{}"，不会 panic
    fmt.Println(json.MarshalString(make(chan int))) // {}
}
```

### 反序列化

```go
var u User
if err := json.UnmarshalString(`{"name":"james","age":18}`, &u); err != nil {
    // 处理错误
}
```

`UnmarshalString` 不会吞错误——反序列化失败通常意味着输入不合法，应该让上层决策。

### 与 `map[string]interface{}` 互转

```go
// 动态结构场景：map ↔ JSON string
m := map[string]interface{}{"name": "james", "age": 18}
s := json.MarshalString(m) // {"age":18,"name":"james"}

var m2 map[string]interface{}
_ = json.UnmarshalString(s, &m2)

// ToJSON 是 MarshalString 的别名，Jackson 习惯用 toJson，便于迁移
_ = json.ToJSON(m)
```

---

## 5. 何时该用、何时该绕过

| 场景 | 推荐 |
|------|------|
| HTTP 响应、模板渲染、日志中拼 JSON 字段 | `json.MarshalString` / `json.ToJSON` |
| 从 HTTP body / 配置字符串解析 JSON | `json.UnmarshalString` |
| 需要感知序列化错误（审计、强一致） | 直接用标准库 `encoding/json.Marshal`，或用本模块的 `Marshal` |
| 追求极致性能（>100w QPS 的热路径） | 引入 `json-iterator/go` 等第三方实现；本模块定位为「够用」而非「最快」 |
| 流式处理（ndjson、大文件） | 标准库 `json.Decoder` / `json.Encoder`（本模块未封装） |

---

## 6. 与框架其它模块的关系

`json` 是 Aifei-Go 最底层的核心库之一，**无任何内部依赖**；反过来，它被上层多处复用：

- [http](../http) / [server](../server)：HTTP 响应体序列化（`{code, msg, data}` 信封）
- [aifei](aifei-go.md) 核心：`Input.GetBean()` 解析请求体
- 各 [plugins](aifei-go.md)：插件配置、消息体的序列化

但因为模块本身只是 `encoding/json` 的薄封装，上层使用时并无特殊约定，按需 `import` 即可。

---

## 7. 模块结构

```
json/
└── json.go    # 7 个包装函数（Marshal / MarshalIndent / Unmarshal /
               # MarshalString / UnmarshalString / ToJSON）
```

- 源码约 37 行，零外部依赖
- 测试在 `_test/json_test`（外部测试包 `package json_test`，仅测导出 API）

---

## 8. 总结

1. **零依赖**：仅依赖标准库 `encoding/json`，不引入第三方 JSON 实现
2. **双形态 API**：`[]byte` 入口（`Marshal`/`Unmarshal`）保持与标准库一致；`string` 入口（`MarshalString`/`UnmarshalString`/`ToJSON`）省去 `string([]byte)` 转换
3. **容错约定**：`MarshalString` 出错返回 `"{}"`，让降级响应结构合法、`JSON.parse` 不抛异常
4. **错误透传**：`Marshal`/`Unmarshal` 不吞 `error`，严肃场景按标准库方式处理
5. **命名兼容**：`ToJSON` 对仗 Java Aifei 的 `JsonUtil.toJson`，降低团队迁移成本

### 延伸阅读

- [log](log.md)：同为基础工具库，Logger 接口抽象
- [config](config.md)：同为基础工具库，分层配置加载
- [aifei-go 整体介绍](aifei-go.md)：`json` 在框架分层中的位置
