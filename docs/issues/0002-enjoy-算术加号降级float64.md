# ISSUE-0002 — enjoy 算术 `+` 不支持字符串拼接，数值一律降级为 float64

> **编号**：0002　**状态**：🟢 已处理　**严重程度**：⚠️ 一般
> **发现日期**：2026-07-16　**相关任务**：enjoy 模块（对照 `docs/java-go-comparison.md` §3.1 Bug #1）

## 问题描述

表达式引擎对 `+` 运算符不做类型分派，一律将两侧 `toFloat64` 后相加。Java 中 `+` 任一侧为 String 时做拼接，且整数运算保留 int/long 类型。

## 复现步骤

1. 模板 `#(1 + 2)` → 输出 `3.0`（应 `3`）
2. 模板 `#("a" + "b")` → 输出 `0`（应 `ab`）
3. 模板 `#(10 / 3)` → 输出 `3.333...`（整数场景应 `3`）

## 期望行为

两侧均为数值时按 Java 规则（int+int→int，含 long/double 提升）保留类型；任一侧为 String 时拼接为字符串。

## 实际行为

全部转 float64 运算，字符串被当 0。

## 影响范围

所有 enjoy 表达式的算术与字符串拼接；模板渲染数值、拼 URL/字符串普遍异常。

## 相关文件 / 符号

- `enjoy/expr_eval.go:40-65` — 二元运算 `+` 统一 `toFloat64`
- 对照 Java：`aifei-enjoy/expr/ast/Arith.java` 按类型分派 + 字符串拼接

## 建议方案

`+` 运算前检查类型：若有 string 则拼接；纯数值按 int/int64/float64 分派，保持整数运算为整型（参考 Java `Arith`）。

## 解决记录

- 修复提交 / PR：（待提交）
- 改动：
  - `enjoy/expr_eval.go`：重写 `ArithExpr.Eval`，按 Java `Arith.java` 规则分派
    - `+` 任一侧为 string → 字符串拼接（新增 `toStr`，nil 转空串）
    - 两侧数值按种类分派：均为整数 → `int64` 运算保留整型；任一浮点 → `float64` 提升
    - 整数 `/` `%` 除零返回 `int64(0)`，浮点除零返回 `float64(0)`
    - `neg` 负号保留原数值种类（整数保持整型）
    - 新增 helpers：`numInfo`、`arithInt`、`arithFloat`、`negateNum`、`toStr`
  - `enjoy/expr_eval_test.go`（新增）：整数保留、浮点提升、字符串拼接、负号、除零、ISSUE-0002 端到端复现
- 校验：`go build ./enjoy` / `go vet ./enjoy` 0 新错；`go test ./enjoy ./_example/enjoy_test` 全绿（含原有 `TestArithExpr`）
- 验收：
  - `#(1 + 2)` → `3`（非 3.0）✓
  - `#("a" + "b")` → `ab`（非 0）✓
  - `#(10 / 3)` → `3`（非 3.333…）✓
  - `#("id=" + 42)` → `id=42` ✓
  - `#(1.5 + 1)` → `2.5` ✓

### 备注

- issue 复现步骤 1 描述 `#(1+2)→3.0` 与实测不符：Go 的 `fmt.Sprintf("%v", float64(3))` 本就输出 `3`。真正的缺陷是内部类型一律降级 `float64`（大整数精度丢失、与 Java 语义不一致），以及 `+` 字符串拼接缺失、整数除法降级 float64 —— 本次一并修复。
- `CompareExpr` 仍用 `toFloat64` 做数值比较，不在本 issue 范围，未改动。
