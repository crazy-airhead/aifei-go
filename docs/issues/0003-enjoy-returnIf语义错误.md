# ISSUE-0003 — enjoy `#returnIf` 被当成无条件 return

> **编号**：0003　**状态**：🔴 未处理　**严重程度**：⚠️ 一般
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

- 修复提交 / PR：
- 改动：
- 校验：`go build ./...` / `go vet ./...` 改动文件 0 新错
- 验收：
