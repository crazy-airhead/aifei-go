# JSON v2 评估（Go 1.27）—— 留档

> 结论先行：**当前代码不需要针对 `encoding/json/v2` 做任何调整**。仓库全部 JSON 调用走 v1 `encoding/json`，Go 1.27 中 v1 已默认由 v2 内核重新实现（桥接层保留 v1 语义）——性能红利不迁移 API 也能吃到，实测差异点也已验证无恙。本文留档评估过程、实测差异与**将来若直用 v2 API 的迁移纪律**，避免后续会话重新推导。

---

## 目录

1. 背景：Go 1.27 中 json/v2 的状态
2. 代码现状：JSON 触点盘点
3. 实测差异：v1 vs v2 探针（go1.27.0）
4. 决策：现在不动，理由
5. 将来迁移指引（若要直用 v2）
6. 附：验证与复现方式

---

## 1. 背景：Go 1.27 中 json/v2 的状态

- `encoding/json/v2` 在 Go 1.25 以 `GOEXPERIMENT=jsonv2` 引入，Go 1.26 保持实验，**Go 1.27 转正进入标准库**（[发布说明](https://go.dev/doc/go1.27)、[发布公告](https://go.dev/blog/go1.27)）。
- 关键事实：**v1 `encoding/json` 默认由 v2 内核重新实现**——v1 的 import 路径、API、语义都不变（这是转正前反复打磨的原因），但底层已换成 v2。也就是说，什么都不改，v1 调用就在跑 v2 内核。
- 逃生阀：若线上遇到桥接层差异，`GOEXPERIMENT=nojsonv2` 一键恢复原 v1 实现，无需改代码。
- v2 API 形态：`Marshal/Unmarshal`（`[]byte`）、`MarshalWrite/UnmarshalRead`（`io`）、`MarshalEncode/UnmarshalDecode`（`jsontext`），配 Options 调整语义/语法行为；自定义编解码走新的 `Marshaler/Unmarshaler` 系列接口。

## 2. 代码现状：JSON 触点盘点

全库仅使用 v1 `encoding/json`（无任何 v2 直接引用），触点分布：

| 路径 | 职责 |
|------|------|
| `json/json.go` | **仓库 JSON 包装层**（`Marshal/Unmarshal/MarshalString/Parse[T]/ParseString[T]`），唯一的收口点 |
| `http/context.go`、`http/handler.go` | 请求绑定（`GetBean`：JSON body 直接走 `encoding/json`，form/query 走逐字段强制转换） |
| `server/io_handler.go`、`server/handler.go` | 响应渲染（`{code, msg, data}` 包裹） |
| `db/json_codec.go` | JSON 列解码（`Table.FieldTypes` 注册的类型）、`Bind` 的 JSON 往返 |
| `nami/result.go`、`nami/coder/json/` | RPC 结果反序列化、JSON 编解码器 |
| `flow/context_impl.go` | 流程实例快照序列化（`ContextFromJSON`） |
| `plugins/elasticsearch`、`plugins/flow/mysql_*.go`、`plugins/swagger/handler.go`、`plugins/xxljob`（3 处） | 插件各自的请求/响应体处理 |
| `_test/`（6 处） | 测试内的断言解码 |

另有一批第三方依赖（jetcache-go、nacos-sdk、swaggo 等）内部自用 JSON，不在本仓库控制面内，跟随其上游演进。

## 3. 实测差异：v1 vs v2 探针（go1.27.0）

在 go1.27.0 上用最小探针实测（非纸面推演），对本仓库相关的五个维度：

| # | 差异点 | v1（现状） | v2 直用 | 对本仓库影响 |
|---|--------|-----------|---------|--------------|
| 1 | 字段名匹配 | 大小写不敏感（`{"Name"}` 能绑到 `name` 字段） | **精确匹配，不匹配字段静默丢弃** | `GetBean` 的 JSON 路径会失去对大小写不严格客户端的容忍度 |
| 2 | HTML 转义 | 默认转义 `<>&` | **默认不转义** | 响应包裹层输出变化，**安全相关**（需显式恢复转义选项） |
| 3 | nil map 序列化 | `null` | **`{}`** | 响应 `data` 为 nil map 时形状变化 |
| 4 | 解码进 `any` 的数字 | `float64` | `float64`（不变） | `ToInt/ToFloat64` 宽松转换家族、`In.GetMap`、`Props` 无影响 |
| 5 | v1 `json.Unmarshaler` | 支持 | **仍被调用**（不变） | `*db.Row` 模型的自定义反序列化（`GetBean` 明确探测依赖）无影响 |

曾担心的两个最大风险点（自定义 Unmarshaler 兼容、`any` 数字类型）实测无虞；真正的行为差异是 #1/#2/#3 三处，集中在**请求绑定**与**响应渲染**两条路径。

探针代码（可复现）：

```go
import (
	v1 "encoding/json"
	v2 "encoding/json/v2"
)

var m1, m2 map[string]any
v1.Unmarshal([]byte(`{"a":1}`), &m1) // m1["a"] → float64
v2.Unmarshal([]byte(`{"a":1}`), &m2) // m2["a"] → float64（不变）

type u1 struct{ Name string `json:"name"` }
// {"Name":"x"} → v1 绑上（大小写不敏感），v2 留空（精确匹配）

b1, _ := v1.Marshal(map[string]string{"u": "<a&b>"}) // {"u":"<a&b>"}
b2, _ := v2.Marshal(map[string]string{"u": "<a&b>"}) // {"u":"<a&b>"}

var nilMap map[string]string
c1, _ := v1.Marshal(nilMap) // null
c2, _ := v2.Marshal(nilMap) // {}
```

## 4. 决策：现在不动，理由

1. **无必要性**：v1 路径源兼容且长期稳定（Go 兼容承诺），v2 内核的性能收益已经通过桥接层免费获得。
2. **无风险收益比**：直用 v2 的增量收益（错误信息带位置、`omitzero`、流式 API）对本仓库当前场景价值有限，而 #1/#2/#3 三处行为差异全部落在请求绑定与响应渲染——恰是 Web 框架对外契约最敏感的位置，迁移的回归面大于收益。
3. **已验证**：全量测试（21 个 `_test` 包）在 go1.27.0 默认配置（即 v1-on-v2 桥接）下通过——`GetBean` 强制转换矩阵、db JSON 列解码、config Bind 往返、nami 编解码、flow 快照全绿，桥接层对本仓库行为保持的实证已在手。

## 5. 将来迁移指引（若要直用 v2）

届时按以下纪律执行，作为独立专项立项，不与其他改动混做：

1. **收口唯一**：所有 v2 直用只允许出现在 `json` 包装模块（`json/json.go`）内，其余模块一律经由包装层或保持 v1——绝不出现 v1/v2 混用（两套对同一类型的语义差异会静默改变行为）。
2. **三处差异对齐**（对应 §3 表）：
   - 响应渲染路径显式恢复 HTML 转义选项（默认不转义不等于可以不转义——历史行为是转义）；
   - 决定字段名匹配策略：精确匹配是 v2 默认，若要保留 v1 的大小写容忍需配选项并在契约文档明示行为变更；
   - nil map 序列化形状（`null` → `{}`）需要评估响应契约影响并在变更日志声明。
3. **先测后迁**：以 `_test/json_test` 为基础补齐 v2 语义矩阵用例（现有 `Parse[T]` 系列签名不变，仅换内核），全量回归后再切换。
4. **逃生阀常备**：迁移期间保留 `GOEXPERIMENT=nojsonv2` 回退路径的说明。

## 6. 附：验证与复现方式

- 桥接层验证：`go test ./_test/json_test ./_test/db_test ./_test/config_test ./_test/nami_test/... ./_test/server_test ./_test/flow_test`（在 go1.27.0 默认 GOEXPERIMENT 下即 v1-on-v2 桥接）。
- 差异探针：§3 代码片段存为 `main.go` 直接 `go run`。
- 官方依据：[Go 1.27 Release Notes](https://go.dev/doc/go1.27)（json/v2 转正与 v1 重实现说明）、[Go 1.27 Release Blog](https://go.dev/blog/go1.27)。
