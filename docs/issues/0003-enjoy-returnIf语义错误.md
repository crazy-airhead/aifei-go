# ISSUE-0003 — enjoy `#returnIf` 被当成无条件 return

> **编号**：0003　**状态**：🟢 已处理　**严重程度**：⚠️ 一般
> **发现日期**：2026-07-16　**相关任务**：enjoy 模块（对照 `docs/java-go-comparison.md` §3.1 Bug #2）

## 问题描述

`#returnIf(expr)` 的 `expr` 应是「返回条件」，仅当为真时才 return；当前解析逻辑把它当成「返回值」并与 `#return` 走同一无条件返回分支。

## 复现步骤

1. 模板：`#returnIf(count > 0)`，期望 `count<=0` 时不返回、继续渲染后续
2. 观察：无论 `count` 取值，都执行了 return

## 期望行为

`expr` 求值为 true 才从当前模板 / define 提前返回；false 则继续渲染。

## 实际行为

恒返回，等价于 `#return`。

## 影响范围

所有使用 `#returnIf` 的模板（提前结束渲染的常见控制流）。

## 相关文件 / 符号

- `enjoy/stat_parser.go:371-372` — `TokReturnIf` 与 `TokReturn` 复用 `parseReturnStat`，expr 被当作返回值且无条件 return
- 对照 Java：`aifei-enjoy/stat/ast/ReturnIf.java` exec 里先 eval 条件再决定 return

## 建议方案

为 `TokReturnIf` 单独处理，生成条件返回 stat（expr 作为条件，非返回值）。

## 解决记录

- 修复提交 / PR：（待提交）
- 改动：
  - `enjoy/stat_parser.go`：
    - 新增 `ReturnIfStat{Cond Expr}` 类型，`Exec` 中仅当 `isTruthy(Cond.Eval(scope, ctrl))` 为真才置 `ctrl.Return = true`（对照 Java `ReturnIf.java`：expr 作为「返回条件」，不写入 `ctrl.Attachment`）
    - `parseOneStat` 拆分 `TokReturn` / `TokReturnIf` 分支，`TokReturnIf` 不再复用 `parseReturnStat`
    - 新增 `parseReturnIfStat`：空参数报错（对照 Java `ReturnIf` 构造函数抛 `ParseException`），expr 作为条件而非返回值
  - `enjoy/stat_parser_test.go`（新增）：条件真/假两类返回行为、后续 `#(expr)` 跳过、空参数报错
- 校验：`go build ./enjoy` / `go vet ./enjoy` 0 新错；`go test ./enjoy ./_example/enjoy_test` 全绿（含新增 `TestReturnIfConditional` / `TestReturnIfSkipsFollowing` / `TestReturnIfEmptyParam`）
- 验收：
  - `#returnIf(count > 0)` + `count=0` → 继续渲染后续（非恒返回）✓
  - `#returnIf(count > 0)` + `count=3` → 提前返回 ✓
  - `A#returnIf(ok)B#(value)C` + `ok=true` → 仅输出 `A`（后续 `#(value)` 不渲染）✓
  - `#returnIf()` 空参数 → 解析报错 ✓

### 备注

- Go 版 `#return`（`ReturnStat`）支持返回值（写入 `ctrl.Attachment`），与 Java `Return.java`「不支持返回值」不同；该差异不在本 issue 范围，未改动，仅修正 `#returnIf` 的条件语义。
